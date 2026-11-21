package ops

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
)

// Filling in a form is not like the other verbs. The rest of this package
// takes pages apart and puts them together again, and what it does not
// understand it leaves behind. A form cannot be treated that way: it is tied
// into the document by object number in a dozen places at once — the field
// tree, the widget annotations on the pages, the resources the appearances
// name — and a document rebuilt around it would have to rebuild all of that
// correctly or quietly break it.
//
// So a filled form is written as an **incremental update**: the original file,
// byte for byte, with the objects that changed appended after it and a new
// cross-reference section pointing back at the old one. That is how every
// program that saves a form saves one, and it is the safest thing a program
// can do to somebody's document. Nothing that was already there is rewritten,
// so everything this package does not understand survives untouched — a
// signature, an embedded file, a piece of XFA — and if the update is wrong,
// the original is still the first part of the file and can be recovered.

// A Filling is a document's form, opened so that it can be filled in and
// written back.
type Filling struct {
	increment
	form *forms.Form
	// formRef is where the AcroForm dictionary lives, when it lives somewhere
	// of its own rather than written into the catalogue.
	formRef reader.Ref
	// streams are the appearances drawn for this filling, by the number each
	// was given.
	streams map[int]reader.Object
}

// OpenForm reads a document's form. It reports false, with no error, for a
// document that simply has none — including one carrying an AcroForm
// dictionary a producer left behind with an empty field list, which 561 of the
// figure corpus's 118 833 files do.
func OpenForm(b []byte) (*Filling, bool, error) {
	d, err := reader.Open(b)
	if err != nil {
		return nil, false, err
	}
	return openFormIn(b, d)
}

// OpenFormWithPassword is the same for a document that is protected.
func OpenFormWithPassword(b []byte, password string) (*Filling, bool, error) {
	d, err := reader.OpenWithPassword(b, password)
	if err != nil {
		return nil, false, err
	}
	return openFormIn(b, d)
}

func openFormIn(b []byte, d *reader.Document) (*Filling, bool, error) {
	form, ok := forms.Read(d)
	if !ok {
		return nil, false, nil
	}
	inc, err := openIncrement(b, d)
	if err != nil {
		return nil, false, err
	}
	f := &Filling{increment: inc, form: form}
	f.next = f.highest() + 1
	if catalog, err := d.Catalog(); err == nil {
		if ref, ok := catalog.Get("AcroForm").(reader.Ref); ok {
			f.formRef = ref
		}
	}
	return f, true, nil
}

// Form is what was read, to be asked about its fields and told what they hold.
func (f *Filling) Form() *forms.Form { return f.form }

// Fill sets one field by name, which is what a command line or a map of
// answers wants.
func (f *Filling) Fill(name, value string) error { return f.form.Fill(name, value) }

// Bytes writes the original file with the changes appended to it.
func (f *Filling) Bytes() ([]byte, error) {
	changed := f.form.Changed()
	if len(changed) == 0 {
		// Nothing was filled in, so the file is what it was. Handing back the
		// original rather than an update that says nothing is both smaller and
		// truer.
		return append([]byte(nil), f.src...), nil
	}

	written := map[int]reader.Object{}
	for _, fld := range changed {
		if err := f.update(fld, written); err != nil {
			return nil, err
		}
	}
	// The document said its appearances wanted drawing again; they have been.
	if f.form.NeedAppearances() && f.formRef != (reader.Ref{}) {
		dict := copyDict(f.form.Dict())
		delete(dict, "NeedAppearances")
		written[f.formRef.Num] = dict
	}
	out, _ := f.append(written, nil)
	return out, nil
}

// update works out what has to be written for one field that was filled in.
func (f *Filling) update(fld *forms.Field, written map[int]reader.Object) error {
	ref, ok := fld.Ref()
	if !ok {
		return fmt.Errorf("ops: %q is written inside another object and cannot be changed on its own", fld.Name)
	}
	dict := dictOf(written, ref, fld.Dict())
	dict["V"] = f.valueOf(fld)

	switch fld.Kind {
	case forms.Checkbox, forms.Radio:
		// A button's picture is already in the file; which of them shows is
		// what changes. Every widget of the group is told, since only the one
		// whose own name matches the value is on.
		for _, w := range fld.Widgets {
			wRef, ok := w.Ref()
			if !ok {
				continue
			}
			target := dictOf(written, wRef, w.Dict())
			if w.On != "" && w.On == fld.Value {
				target["AS"] = reader.Name(fld.Value)
			} else {
				target["AS"] = reader.Name("Off")
			}
		}
	default:
		// Everything else has to have its picture drawn, because a value is
		// not what gets drawn and a field filled in without one shows nothing.
		for _, w := range fld.Widgets {
			app, ok := fld.Appearance(w)
			if !ok {
				continue
			}
			wRef, ok := w.Ref()
			if !ok {
				continue
			}
			stream := f.appearanceStream(app)
			target := dictOf(written, wRef, w.Dict())
			target["AP"] = reader.Dict{"N": stream}
			delete(target, "AS")
			written[stream.Num] = f.streams[stream.Num]
		}
	}
	return nil
}

// valueOf is what a field's value looks like written down: a name for a
// button, since that is what a state is, and text for everything else.
func (f *Filling) valueOf(fld *forms.Field) reader.Object {
	switch fld.Kind {
	case forms.Checkbox, forms.Radio:
		return reader.Name(fld.Value)
	case forms.ListBox, forms.ComboBox:
		if len(fld.Values) > 1 {
			out := make(reader.Array, 0, len(fld.Values))
			for _, v := range fld.Values {
				out = append(out, textString(v))
			}
			return out
		}
	}
	return textString(fld.Value)
}

// appearanceStream writes one drawing out as a new object and gives back its
// reference.
func (f *Filling) appearanceStream(app forms.Appearance) reader.Ref {
	if f.streams == nil {
		f.streams = map[int]reader.Object{}
	}
	resources := reader.Dict{}
	if app.Font != nil {
		resources["Font"] = reader.Dict{reader.Name(app.FontName): app.Font}
	} else {
		// The document does not carry the font its own field named, so a
		// standard one is put in under that name: a stream naming a font
		// nothing can find draws nothing at all.
		resources["Font"] = reader.Dict{reader.Name(app.FontName): reader.Dict{
			"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
			"BaseFont": reader.Name("Helvetica"), "Encoding": reader.Name("WinAnsiEncoding"),
		}}
	}
	ref := reader.Ref{Num: f.next}
	f.next++
	f.streams[ref.Num] = &reader.Stream{
		Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":      boxArray(app.BBox),
			"Resources": resources,
			"Length":    reader.Integer(len(app.Content)),
		},
		Raw: app.Content,
	}
	return ref
}

// dictOf gives the copy of an object being changed, making one the first time
// it is asked for so that a field with several widgets does not lose the first
// change when the second is made.
func dictOf(written map[int]reader.Object, ref reader.Ref, from reader.Dict) reader.Dict {
	if have, ok := written[ref.Num]; ok {
		if dict, ok := have.(reader.Dict); ok {
			return dict
		}
	}
	dict := copyDict(from)
	written[ref.Num] = dict
	return dict
}

// copyDict copies one level, which is all that is changed: everything deeper
// is left pointing where it pointed.
func copyDict(d reader.Dict) reader.Dict {
	out := make(reader.Dict, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

// textString writes a value the way a document holds text somebody typed:
// as bytes where the eight-bit alphabet has them all, and as UTF-16 with a
// mark at the front where it does not — which is what anything but the
// plainest English needs.
func textString(s string) reader.String {
	simple := true
	for _, r := range s {
		if r > 0xFF {
			simple = false
			break
		}
	}
	if simple {
		out := make([]byte, 0, len(s))
		for _, r := range s {
			out = append(out, byte(r))
		}
		return reader.String(out)
	}
	out := []byte{0xFE, 0xFF}
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			out = append(out, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
			continue
		}
		out = append(out, byte(r>>8), byte(r))
	}
	return reader.String(out)
}

// ErrEncrypted is what adding to an encrypted document is refused with. A
// document written through a key has every string and every stream in it
// written through that key too, so anything appended would have to be as well.
// That is not done here, and writing an update in the clear beside encrypted
// objects makes a file nothing can read.
var ErrEncrypted = errors.New("ops: the document is encrypted, and this does not yet write into one")

// ErrNotAppendable is what a file that cannot be added to is refused with: one
// that does not say where its cross-reference section is, or that had to be
// repaired to be read.
var ErrNotAppendable = errors.New("ops: the file cannot be appended to")

// An increment is an incremental update being made to a file (ISO 32000-1
// §7.5.6): the file itself, untouched, and what will be appended to it. It is
// shared by everything in this package that writes into a document rather than
// rebuilding one — filling a form and signing it.
type increment struct {
	src []byte
	doc *reader.Document
	// next is the object number to hand out for anything new.
	next int
	// prev is where the file's own cross-reference section begins, which the
	// update has to point back at. A file that does not say is one the reader
	// had to repair, and those are refused before this is ever set.
	prev int
}

// openIncrement says whether a file can be added to at all, and prepares the
// update if it can.
func openIncrement(b []byte, d *reader.Document) (increment, error) {
	if d.Encrypted() {
		return increment{}, ErrEncrypted
	}
	// Where the file says its own cross-reference section begins is what the
	// update points back at. This is asked before anything else about the
	// shape of the file, because a file that does not say cannot be added to
	// at all, whatever else is wrong with it.
	prev, ok := lastStartxref(b)
	if !ok {
		return increment{}, fmt.Errorf("%w: the file does not say where its cross-reference table is, so nothing can be appended to it", ErrNotAppendable)
	}
	// A file this package has had to repair has no cross-reference section
	// worth pointing back at, however confidently it says where one is: an
	// update appended to it would name offsets into a table that was never
	// right. Such a file is written out whole, which is not what this does.
	if d.Repaired() {
		return increment{}, fmt.Errorf("%w: the file had to be repaired to be read, so it cannot be added to", ErrNotAppendable)
	}
	return increment{src: b, doc: d, prev: prev}, nil
}

// highest is the largest object number the file already uses, so that nothing
// written now lands on top of something already there. The trailer's own count
// is believed only when the objects agree with it: a file may say anything, and
// one that says too little would have this overwrite what it names. refs are
// the objects the caller knows about and is about to write beside.
func (u *increment) highest() int {
	// The sections themselves are read for the numbers they list, whatever any
	// /Size says: a file whose /Size is too small still lists its objects, and
	// those are what a new object must not land on. Chosen over refusing such a
	// file because the update's own /Size can simply say the truth. Every
	// object a caller could be about to write beside is listed in a section, so
	// there is nothing further to check them against.
	high := xrefHighest(u.src, u.prev)
	if size, ok := reader.ToInt(u.doc.Trailer().Get("Size")); ok && size > 0 && int(size)-1 > high && size <= maxObjectNumber {
		high = int(size) - 1
	}
	return high
}

// maxObjectNumber bounds every number read from a file's own sections, so that
// adding one to it cannot overflow an int on a 32-bit build.
const maxObjectNumber = 1<<31 - 2

// maxSections bounds how many sections /Prev is followed through.
const maxSections = 4096

// xrefHighest is the largest object number any section of the chain beginning
// at `at` lists, free entries included, following /Prev and /XRefStm. A section
// it cannot read ends the walk with what it has found: the reader has already
// accepted the file, so this only ever adds to what it knows.
func xrefHighest(b []byte, at int) int {
	high := 0
	seen := map[int]bool{}
	// `at` is inside the file: the first came from lastStartxref, which refuses
	// one that is not, and each /Prev is checked below before it is followed.
	for hops := 0; at > 0 && !seen[at] && hops < maxSections; hops++ {
		seen[at] = true
		var d reader.Dict
		var h int
		if xrefIsStream(b, at) {
			d, h = streamSection(b, at)
		} else {
			d, h = tableSection(b, at)
		}
		if h > high {
			high = h
		}
		if hybrid, ok := reader.ToInt(d.Get("XRefStm")); ok && hybrid > 0 && hybrid < int64(len(b)) && !seen[int(hybrid)] {
			seen[int(hybrid)] = true
			if _, hh := streamSection(b, int(hybrid)); hh > high {
				high = hh
			}
		}
		prev, ok := reader.ToInt(d.Get("Prev"))
		if !ok || prev <= 0 || prev >= int64(len(b)) {
			break
		}
		at = int(prev)
	}
	return high
}

// tableSection reads one classic section: its subsections "first count" and
// the count entries after each, then the trailer dictionary.
func tableSection(b []byte, at int) (reader.Dict, int) {
	rest := b[at:]
	// xrefIsStream has said this begins with the word "xref".
	_, rest = nextToken(rest)
	high := 0
	// Every pass takes two words at least, so the loop ends with the bytes; a
	// file that stops before its trailer gives no dictionary.
	for len(rest) != 0 {
		var tok string
		tok, rest = nextToken(rest)
		if tok == "trailer" {
			break
		}
		first, err1 := strconv.Atoi(tok)
		var cs string
		cs, rest = nextToken(rest)
		count, err2 := strconv.Atoi(cs)
		if err1 != nil || err2 != nil || first < 0 || count < 0 || first > maxObjectNumber || count > maxObjectNumber {
			return nil, high
		}
		// Each entry is three words; a count that promises more than the file
		// holds stops where the file does.
		for i := 0; i < count; i++ {
			var last string
			for w := 0; w < 3; w++ {
				last, rest = nextToken(rest)
			}
			if last == "" {
				return nil, high
			}
			if n := first + i; n > high && n <= maxObjectNumber {
				high = n
			}
		}
	}
	// A trailer that does not parse is nil, which has no /Prev to follow.
	obj, _, _ := reader.ParseObject(rest)
	d, _ := obj.(reader.Dict)
	return d, high
}

// unresolved is the resolver a section is read with: the sections are what
// says where objects are, so none can be looked up while they are read.
func unresolved(reader.Ref) (reader.Object, error) { return reader.Null{}, nil }

// streamSection reads one cross-reference stream's /Index (or /Size) and the
// dictionary that carries /Prev.
func streamSection(b []byte, at int) (reader.Dict, int) {
	_, obj, _, err := reader.ParseIndirectObject(b[at:], unresolved)
	s, ok := obj.(*reader.Stream)
	if err != nil || !ok {
		return nil, 0
	}
	high := 0
	if idx, ok := s.Dict.Get("Index").(reader.Array); ok {
		for i := 0; i+1 < len(idx); i += 2 {
			first, ok1 := reader.ToInt(idx[i])
			count, ok2 := reader.ToInt(idx[i+1])
			if ok1 && ok2 && first >= 0 && count > 0 && first <= maxObjectNumber && count <= maxObjectNumber {
				if n := int(first + count - 1); n > high && n <= maxObjectNumber {
					high = n
				}
			}
		}
	} else if size, ok := reader.ToInt(s.Dict.Get("Size")); ok && size > 0 && size <= maxObjectNumber {
		high = int(size) - 1
	}
	return s.Dict, high
}

// nextToken is the next whitespace-separated word, and what follows it.
func nextToken(b []byte) (string, []byte) {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\r' || b[i] == '\n' || b[i] == '\t' || b[i] == 0 || b[i] == '\f') {
		i++
	}
	j := i
	for j < len(b) && !(b[j] == ' ' || b[j] == '\r' || b[j] == '\n' || b[j] == '\t' || b[j] == 0 || b[j] == '\f') {
		j++
	}
	return string(b[i:j]), b[j:]
}

// append writes the original file, then the objects that changed, then a
// cross-reference section naming where each of them went and pointing back at
// the one before it. raw holds objects whose bytes the caller has written
// itself — a signature dictionary, whose placeholders it has to find again —
// and offsets says where each object begins.
func (u *increment) append(written map[int]reader.Object, raw map[int][]byte) (out []byte, offsets map[int]int) {
	out = append([]byte(nil), u.src...)
	// A file whose last byte is not a line ending would run its last line into
	// the first object of the update.
	if len(out) > 0 && out[len(out)-1] != '\n' && out[len(out)-1] != '\r' {
		out = append(out, '\n')
	}

	nums := make([]int, 0, len(written)+len(raw))
	for num := range written {
		nums = append(nums, num)
	}
	for num := range raw {
		nums = append(nums, num)
	}
	sort.Ints(nums)

	offsets = make(map[int]int, len(nums))
	for _, num := range nums {
		offsets[num] = len(out)
		out = append(out, []byte(objHeader(num))...)
		if body, ok := raw[num]; ok {
			out = append(out, body...)
		} else {
			out = reader.AppendObject(out, written[num])
		}
		out = append(out, []byte("\nendobj\n")...)
	}

	// The update has to say where its objects went in the same way the file
	// already says where its own are. A file whose table is a stream cannot
	// be followed back to by a plain table: a reader going to /Prev would
	// find an object where it expected the word "xref", and the strict ones
	// refuse the whole file. That is not a nicety — macOS's own renderer
	// draws nothing at all for such a file.
	start := len(out)
	if xrefIsStream(u.src, u.prev) {
		return u.appendStreamTable(out, nums, offsets, start), offsets
	}
	out = append(out, []byte("xref\n")...)
	for _, run := range runsOf(nums) {
		out = append(out, []byte(fmt.Sprintf("%d %d\n", run[0], len(run)))...)
		for _, num := range run {
			out = append(out, []byte(fmt.Sprintf("%010d %05d n \n", offsets[num], 0))...)
		}
	}
	out = append(out, []byte("trailer\n")...)
	out = reader.AppendObject(out, u.updateTrailer())
	out = append(out, []byte(fmt.Sprintf("\nstartxref\n%d\n%%%%EOF\n", start))...)
	return out, offsets
}

// objHeader is what begins an object of the update.
func objHeader(num int) string { return strconv.Itoa(num) + " 0 obj\n" }

// updateTrailer is what the update says about the file as a whole: how many
// objects there now are, where the section before it begins, and the entries
// that identify the document, which every section has to repeat.
func (u *increment) updateTrailer() reader.Dict {
	trailer := reader.Dict{
		"Size": reader.Integer(u.next),
		"Prev": reader.Integer(u.prev),
	}
	for _, key := range []reader.Name{"Root", "Info", "ID"} {
		if v, named := u.doc.Trailer()[key]; named {
			trailer[key] = v
		}
	}
	return trailer
}

// xrefIsStream says whether the section at an offset is a cross-reference
// stream rather than the plain table the older files use.
func xrefIsStream(b []byte, at int) bool {
	if at < 0 || at >= len(b) {
		return false
	}
	rest := b[at:]
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\r' || rest[0] == '\n' || rest[0] == '\t') {
		rest = rest[1:]
	}
	return !bytes.HasPrefix(rest, []byte("xref"))
}

// The widths of one entry of a cross-reference stream: a byte saying what sort
// of entry it is, four for where the object begins, and two for its
// generation. Four bytes reach four thousand megabytes, which is larger than
// any PDF anybody should be making.
const (
	xrefTypeWidth   = 1
	xrefOffsetWidth = 4
	xrefGenWidth    = 2
)

// appendStreamTable writes the update's own cross-reference as a stream, which
// is what a file whose table is already one requires.
func (u *increment) appendStreamTable(out []byte, nums []int, offsets map[int]int, start int) []byte {
	// The stream is itself an object, so it needs a number and an offset of
	// its own, and it has to be in its own table.
	self := u.next
	u.next++
	offsets[self] = start
	nums = append(nums, self)
	sort.Ints(nums)

	runs := runsOf(nums)
	index := reader.Array{}
	var body []byte
	for _, run := range runs {
		index = append(index, reader.Integer(run[0]), reader.Integer(len(run)))
		for _, num := range run {
			body = append(body, 1)
			off := offsets[num]
			body = append(body, byte(off>>24), byte(off>>16), byte(off>>8), byte(off))
			body = append(body, 0, 0)
		}
	}
	dict := u.updateTrailer()
	dict["Type"] = reader.Name("XRef")
	dict["Size"] = reader.Integer(u.next)
	dict["Index"] = index
	dict["W"] = reader.Array{reader.Integer(xrefTypeWidth),
		reader.Integer(xrefOffsetWidth), reader.Integer(xrefGenWidth)}
	dict["Length"] = reader.Integer(len(body))

	out = append(out, []byte(objHeader(self))...)
	out = reader.AppendObject(out, &reader.Stream{Dict: dict, Raw: body})
	out = append(out, []byte("\nendobj\n")...)
	out = append(out, []byte(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", start))...)
	return out
}

// runsOf breaks a sorted list of object numbers into the consecutive runs a
// cross-reference section is written in.
func runsOf(nums []int) [][]int {
	var out [][]int
	for i := 0; i < len(nums); {
		j := i + 1
		for j < len(nums) && nums[j] == nums[j-1]+1 {
			j++
		}
		out = append(out, nums[i:j])
		i = j
	}
	return out
}

// lastStartxref is where the file says its own cross-reference table begins,
// which the update has to point back at.
func lastStartxref(b []byte) (int, bool) {
	i := bytes.LastIndex(b, []byte("startxref"))
	if i < 0 {
		return 0, false
	}
	rest := b[i+len("startxref"):]
	j := 0
	for j < len(rest) && (rest[j] == ' ' || rest[j] == '\r' || rest[j] == '\n' || rest[j] == '\t') {
		j++
	}
	k := j
	for k < len(rest) && rest[k] >= '0' && rest[k] <= '9' {
		k++
	}
	// No digits is an empty string, which Atoi refuses.
	v, err := strconv.Atoi(string(rest[j:k]))
	if err != nil || v <= 0 || v >= len(b) {
		return 0, false
	}
	return v, true
}
