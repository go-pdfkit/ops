package ops

import (
	"bytes"
	"fmt"

	"github.com/go-pdfkit/reader"
)

// dangerousActions are the action types that do something other than move
// about inside the document. A sanitised file keeps none of them.
var dangerousActions = map[reader.Name]bool{
	"JavaScript":       true,
	"Launch":           true,
	"SubmitForm":       true,
	"ImportData":       true,
	"ResetForm":        true,
	"Movie":            true,
	"Sound":            true,
	"Rendition":        true,
	"RichMediaExecute": true,
	"GoToE":            true,
	"GoToR":            true,
	"Named":            true,
	"SetOCGState":      true,
	"Hide":             true,
	"Trans":            true,
}

// dangerousAnnots are the annotation types that exist to run or embed
// something rather than to mark the page.
var dangerousAnnots = map[reader.Name]bool{
	"Movie":          true,
	"Screen":         true,
	"RichMedia":      true,
	"FileAttachment": true,
	"Sound":          true,
	"3D":             true,
}

// annotKeysRebuilt are the entries of an annotation this package decides for
// itself rather than copying.
var annotKeysRebuilt = map[reader.Name]bool{
	"P":    true, // the page an annotation is on: the new one, not the old
	"Dest": true, // remapped
	"A":    true, // remapped, and filtered when sanitising
	"AA":   true, // an annotation's own actions run without anyone asking

	// The number under which an annotation is filed in the structure tree's
	// parent tree is handed out afresh, for the same reason a page's is.
	"StructParent": true,
}

// RemoveAnnotations drops every annotation: links, comments, form fields and
// all. What was drawn on the page stays; what sat on top of it goes.
func (d *Doc) RemoveAnnotations() { d.dropAnnots = true }

// Sanitize strips the parts of a file that do something rather than show
// something: page and annotation actions, JavaScript, launching, form
// submission, files travelling with a page, and the annotation types that
// exist to embed or play something.
//
// A document written by this package always carries a catalogue of this
// package's own making, so document-level scripts, automatic actions on
// opening and embedded file trees are gone whatever this setting says;
// sanitising deals with what travels attached to a page.
func (d *Doc) Sanitize() { d.sanitize = true }

// Flatten draws each annotation's own appearance into the page and then drops
// the annotation, so what one reader sees is what every reader sees — which is
// what filling in a form and then flattening it means.
func (d *Doc) Flatten() { d.flatten = true }

// copyAnnots rebuilds a page's annotations, pointing whatever they refer to at
// the pages of this document rather than of the one they came from.
func (d *Doc) copyAnnots(w *reader.Writer, p Page, src reader.Dict, where destinations, kept *keptAnnots) reader.Array {
	if d.dropAnnots || d.flatten {
		return nil
	}
	list, ok := d.annotsOf(p, src)
	if !ok {
		return nil
	}
	out := make(reader.Array, 0, len(list))
	for _, e := range list {
		annot, ok := resolveDict(p.src, e)
		if !ok {
			continue
		}
		copied := d.copyAnnot(w, p, annot, where)
		if copied == nil {
			continue
		}
		// The annotation is given its number now and written at the end,
		// because a form's fields have to be able to point at it and the
		// widget has to be able to point back — and neither is known until
		// every page has been walked.
		ref := w.Reserve()
		kept.add(p.src, e, ref, copied)
		out = append(out, ref)
	}
	return out
}

// A keptAnnots remembers where each annotation that survived ended up, so that
// the form it belonged to can be pointed at it again. Without this a document
// rebuilt around a form keeps every widget on the page and loses the field
// list that gives them meaning — which is not a form with something missing
// but half a form, and worse than none.
type keptAnnots struct {
	// at is the new reference for each source annotation, by the document it
	// came from and the number it had there.
	at map[annotKey]reader.Ref
	// dict is what will be written at that reference, still changeable.
	dict map[reader.Ref]reader.Dict
	// order is the references in the order they were made, so that what is
	// written comes out the same way every time.
	order []reader.Ref
}

// An annotKey names one annotation of one source document.
type annotKey struct {
	src *reader.Document
	num int
}

func newKeptAnnots() *keptAnnots {
	return &keptAnnots{at: map[annotKey]reader.Ref{}, dict: map[reader.Ref]reader.Dict{}}
}

// add records one annotation that survived. When a page is written twice — a
// selection may ask for the same page more than once — the first copy is the
// one anything pointing at that annotation is pointed at, which is the same
// choice the destination map makes for the page itself and the copy the
// structure tree describes.
func (k *keptAnnots) add(src *reader.Document, was reader.Object, ref reader.Ref, dict reader.Dict) {
	if old, ok := was.(reader.Ref); ok {
		if _, already := k.at[annotKey{src, old.Num}]; !already {
			k.at[annotKey{src, old.Num}] = ref
		}
	}
	k.dict[ref] = dict
	k.order = append(k.order, ref)
}

// find says where a source annotation ended up.
func (k *keptAnnots) find(src *reader.Document, o reader.Object) (reader.Ref, bool) {
	ref, ok := o.(reader.Ref)
	if !ok {
		return reader.Ref{}, false
	}
	to, ok := k.at[annotKey{src, ref.Num}]
	return to, ok
}

// write puts every annotation down, once everything that had to point at them
// has been settled.
func (k *keptAnnots) write(w *reader.Writer) {
	for _, ref := range k.order {
		w.Put(ref, k.dict[ref])
	}
}

// annotsOf resolves a page's /Annots to a list.
func (d *Doc) annotsOf(p Page, src reader.Dict) (reader.Array, bool) {
	list, ok := resolveArray(p.src, src.Get("Annots"))
	return list, ok && len(list) > 0
}

// copyAnnot copies one annotation, or reports nil when it should not survive.
func (d *Doc) copyAnnot(w *reader.Writer, p Page, annot reader.Dict, where destinations) reader.Dict {
	subtype, _ := reader.ToName(annot.Get("Subtype"))
	if d.sanitize && dangerousAnnots[subtype] {
		return nil
	}
	out := reader.Dict{}
	for k, v := range annot {
		if annotKeysRebuilt[k] {
			continue
		}
		if d.sanitize && (k == "EF" || k == "AF") {
			continue
		}
		out[k] = w.Copy(p.src, v)
	}
	if dest, status := d.remapDestination(w, p.src, annot.Get("Dest"), where); status == destMapped {
		out["Dest"] = dest
	}
	if action := d.copyAction(w, p.src, annot.Get("A"), where); action != nil {
		out["A"] = action
	}
	return out
}

// copyAction copies an annotation's action, remapping where it goes and
// refusing the kinds that do more than go somewhere.
func (d *Doc) copyAction(w *reader.Writer, src *reader.Document, o reader.Object, where destinations) reader.Object {
	action, ok := resolveDict(src, o)
	if !ok {
		return nil
	}
	kind, _ := reader.ToName(action.Get("S"))
	if d.sanitize && dangerousActions[kind] {
		return nil
	}
	out := reader.Dict{}
	for k, v := range action {
		// A chain of further actions is not followed, and a destination is
		// rewritten rather than copied.
		if k == "D" || k == "Next" {
			continue
		}
		out[k] = w.Copy(src, v)
	}
	if kind == "GoTo" {
		dest, status := d.remapDestination(w, src, action.Get("D"), where)
		if status != destMapped {
			// A jump to a page that is no longer here is not a jump.
			return nil
		}
		out["D"] = dest
	}
	return out
}

// A destStatus says what became of a destination.
type destStatus uint8

const (
	// destMapped: it named a page that is in this document.
	destMapped destStatus = iota
	// destPageGone: it named a page of the source that is not here any more,
	// because someone removed it.
	destPageGone
	// destUnusable: it named nothing that could be followed, in the source
	// either — a file with broken links keeps them rather than losing them.
	destUnusable
)

// remapDestination turns a destination that names a page of a source document
// into one naming the page it became here, and says why it could not when it
// could not.
func (d *Doc) remapDestination(w *reader.Writer, src *reader.Document, o reader.Object, where destinations) (reader.Object, destStatus) {
	arr, ok := resolveDestination(src, o)
	if !ok {
		return nil, destUnusable
	}
	ref, ok := arr[0].(reader.Ref)
	if !ok {
		return nil, destUnusable
	}
	number := pageNumberOf(src, ref)
	if number == 0 {
		return nil, destUnusable
	}
	page, ok := where[pageKey{src, number}]
	if !ok {
		return nil, destPageGone
	}
	out := make(reader.Array, len(arr))
	out[0] = page
	for i := 1; i < len(arr); i++ {
		out[i] = w.Copy(src, arr[i])
	}
	return out, destMapped
}

// resolveDestination follows a destination to the array that names a page,
// through a name, a string, or a dictionary, whichever the file used.
func resolveDestination(src *reader.Document, o reader.Object) (reader.Array, bool) {
	resolved := resolve(src, o)
	if name, ok := reader.ToName(resolved); ok {
		if resolved, ok = lookupNamedDestination(src, string(name)); !ok {
			return nil, false
		}
	} else if s, ok := reader.ToString(resolved); ok {
		if resolved, ok = lookupNamedDestination(src, string(s)); !ok {
			return nil, false
		}
	}
	if dict, ok := reader.ToDict(resolved); ok {
		resolved = resolve(src, dict.Get("D"))
	}
	arr, ok := reader.ToArray(resolved)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	return arr, true
}

// pageNumberOf reports which page of a document a reference names, counting
// from one, or zero when it names none.
func pageNumberOf(src *reader.Document, ref reader.Ref) int {
	for i := 1; i <= src.PageCount(); i++ {
		if r, ok := src.PageRef(i); ok && r.Num == ref.Num {
			return i
		}
	}
	return 0
}

// lookupNamedDestination follows the catalogue to a destination given by name,
// in either of the two places a file may keep one.
func lookupNamedDestination(src *reader.Document, name string) (reader.Object, bool) {
	cat, _ := src.Catalog()
	// The modern place: /Names /Dests, a name tree.
	if names, ok := src.GetDict(cat, "Names"); ok {
		if tree, ok := src.GetDict(names, "Dests"); ok {
			if o, ok := searchNameTree(src, tree, name, 0); ok {
				return o, true
			}
		}
	}
	// The old place: /Dests, a plain dictionary.
	if dests, ok := src.GetDict(cat, "Dests"); ok {
		if o := dests.Get(reader.Name(name)); o.Kind() != reader.KindNull {
			return resolve(src, o), true
		}
	}
	return nil, false
}

// maxNameTreeDepth bounds a name tree, which a file can make a cycle.
const maxNameTreeDepth = 32

// searchNameTree walks a name tree looking for one key.
func searchNameTree(src *reader.Document, node reader.Dict, name string, depth int) (reader.Object, bool) {
	if depth > maxNameTreeDepth {
		return nil, false
	}
	if arr, ok := resolveArray(src, node.Get("Names")); ok {
		for i := 0; i+1 < len(arr); i += 2 {
			key, ok := reader.ToString(resolve(src, arr[i]))
			if !ok || string(key) != name {
				continue
			}
			return resolve(src, arr[i+1]), true
		}
	}
	kids, ok := resolveArray(src, node.Get("Kids"))
	if !ok {
		return nil, false
	}
	for _, kid := range kids {
		child, ok := resolveDict(src, kid)
		if !ok {
			continue
		}
		if o, ok := searchNameTree(src, child, name, depth+1); ok {
			return o, true
		}
	}
	return nil, false
}

// flattenAnnots draws each of a page's annotations where it sits, using the
// appearance the file already carries, and returns the content and the
// resources needed to draw them.
func (d *Doc) flattenAnnots(w *reader.Writer, p Page, src reader.Dict) ([]byte, reader.Dict) {
	list, ok := d.annotsOf(p, src)
	if !ok {
		return nil, nil
	}
	xobjects := reader.Dict{}
	var content bytes.Buffer
	for i, e := range list {
		annot, ok := resolveDict(p.src, e)
		if !ok || hidden(p.src, annot) {
			continue
		}
		stream, ok := appearanceOf(p.src, annot)
		if !ok {
			continue
		}
		rect, ok := rectangle(p.src, annot.Get("Rect"))
		if !ok {
			continue
		}
		m, ok := appearanceMatrix(p.src, stream, rect)
		if !ok {
			continue
		}
		name := reader.Name(fmt.Sprintf("PdfopsAn%d", i))
		xobjects[name] = w.Copy(p.src, stream)
		content.WriteString("q ")
		writeMatrix(&content, m)
		fmt.Fprintf(&content, " cm /%s Do Q\n", name)
	}
	if len(xobjects) == 0 {
		return nil, nil
	}
	return content.Bytes(), reader.Dict{"XObject": xobjects}
}

// hidden reports whether an annotation asks not to be shown.
func hidden(src *reader.Document, annot reader.Dict) bool {
	flags, ok := reader.ToInt(resolve(src, annot.Get("F")))
	if !ok {
		return false
	}
	const hiddenFlag, noViewFlag = 2, 32
	return flags&hiddenFlag != 0 || flags&noViewFlag != 0
}

// appearanceOf finds the stream an annotation is drawn with: its normal
// appearance, and within that the state it is currently in.
func appearanceOf(src *reader.Document, annot reader.Dict) (reader.Object, bool) {
	ap, ok := src.GetDict(annot, "AP")
	if !ok {
		return nil, false
	}
	entry := ap.Get("N")
	resolved := resolve(src, entry)
	if _, ok := reader.ToStream(resolved); ok {
		return entry, true
	}
	states, ok := reader.ToDict(resolved)
	if !ok {
		return nil, false
	}
	// An annotation that says which state it is showing is taken at its word:
	// naming a state that is not there means it shows nothing, which is what a
	// viewer does with it.
	if as, ok := reader.ToName(annot.Get("AS")); ok {
		if e := states.Get(as); e.Kind() != reader.KindNull {
			return e, true
		}
		return nil, false
	}
	// Nothing said, and only one thing it could be.
	if len(states) == 1 {
		for _, e := range states {
			return e, true
		}
	}
	return nil, false
}

// appearanceMatrix works out how to place an appearance stream inside the
// rectangle an annotation occupies: its bounding box is transformed by its own
// matrix, and the result is fitted to the rectangle.
func appearanceMatrix(src *reader.Document, entry reader.Object, rect [4]float64) ([6]float64, bool) {
	stream, ok := reader.ToStream(resolve(src, entry))
	if !ok {
		return [6]float64{}, false
	}
	bbox, ok := rectangle(src, stream.Dict.Get("BBox"))
	if !ok {
		return [6]float64{}, false
	}
	m := [6]float64{1, 0, 0, 1, 0, 0}
	if arr, ok := resolveArray(src, stream.Dict.Get("Matrix")); ok && len(arr) == 6 {
		for i := range m {
			v, ok := resolveFloat(src, arr[i])
			if !ok {
				return [6]float64{}, false
			}
			m[i] = v
		}
	}
	// The corners of the box, once the matrix has had them.
	xs := []float64{
		m[0]*bbox[0] + m[2]*bbox[1] + m[4], m[0]*bbox[2] + m[2]*bbox[1] + m[4],
		m[0]*bbox[2] + m[2]*bbox[3] + m[4], m[0]*bbox[0] + m[2]*bbox[3] + m[4],
	}
	ys := []float64{
		m[1]*bbox[0] + m[3]*bbox[1] + m[5], m[1]*bbox[2] + m[3]*bbox[1] + m[5],
		m[1]*bbox[2] + m[3]*bbox[3] + m[5], m[1]*bbox[0] + m[3]*bbox[3] + m[5],
	}
	minX, maxX := minMax(xs)
	minY, maxY := minMax(ys)
	if maxX == minX || maxY == minY {
		return [6]float64{}, false
	}
	sx := (rect[2] - rect[0]) / (maxX - minX)
	sy := (rect[3] - rect[1]) / (maxY - minY)
	// The placement is applied on top of the stream's own matrix, which the
	// form carries itself.
	return [6]float64{sx, 0, 0, sy, rect[0] - minX*sx, rect[1] - minY*sy}, true
}

// minMax reports the extremes of a handful of numbers.
func minMax(v []float64) (lo, hi float64) {
	lo, hi = v[0], v[0]
	for _, x := range v[1:] {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	return lo, hi
}
