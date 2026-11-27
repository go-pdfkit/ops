// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// ErrOutlineTooLarge is what outlining a page answers when the text it would
// write is past the bounds: a content stream can show a glyph per byte, and a
// glyph is tens of bytes of path, so the size of the result is not the size of
// the input.
var ErrOutlineTooLarge = errors.New("ops: the text to outline is past the bounds")

// ErrOutlineVertical is what outlining answers for a font written top to
// bottom. The renderer this package takes its glyphs from does not place them,
// and paths drawn at places nobody has checked are worse than text left alone.
var ErrOutlineVertical = errors.New("ops: a font written vertically cannot be outlined")

// The bounds of one write's outlining, shared by every page of a document: a
// bound per page is a bound times the page count, and fifty pages sharing one
// stream wrote 96 MB from 20 KB. They are variables so that a test can lower
// them: the real ones are past what a test can afford to build.
var (
	// maxOutlineGlyphs is the glyphs drawn from one document, counting every
	// form and Type 3 glyph under it once.
	maxOutlineGlyphs = 1 << 20
	// maxOutlineBytes is what one document's outlining may write, and the
	// decoded content it may read, each. Everything written is charged to it
	// the moment it is built, so the refusal comes before the work, not after.
	maxOutlineBytes = 64 << 20
	// maxOutlineNesting is how deep forms, patterns and Type 3 glyphs may nest.
	maxOutlineNesting = 12
	// maxOutlineQDepth is how deep q may nest: 28, the limit ISO 32000-1
	// Annex C (implementation limits, "graphics state nesting") gives, past
	// which a conforming reader need not follow. Deeper is refused, since the
	// saved states are memory the input's size does not bound.
	maxOutlineQDepth = 28
	// maxOutlineMarked is how deep marked-content sequences may nest.
	maxOutlineMarked = 256
	// maxOutlineCoord is the largest coordinate written. A page is metres at
	// most; a number past this is no drawing, and written out it is hundreds of
	// digits for a few bytes of input.
	maxOutlineCoord = 1e9
)

const (
	// outlineEditCost is charged for each operation that is rewritten: the edit
	// itself is memory, whatever text it puts back.
	outlineEditCost = 32
	// outlineFormCost is charged for each form or Type 3 glyph written, on top
	// of its content, so that fan-out through nesting is paid for per form.
	outlineFormCost = 512
	// outlineNodeCost is charged for each object of a resource dictionary built.
	outlineNodeCost = 16
)

// OutlineText writes the text of the pages a range names as paths, so that the
// page looks as it did and no longer holds a font: nothing is left to select,
// search, or be missing on a machine that lacks the font.
//
// The glyphs are the ones go-pdfkit/render draws, so a page looks after what the
// renderer showed before. What is carried over, and what is not:
//
//   - TrueType, CFF and Type 1 fonts, embedded or standing in for one of the
//     fourteen, become filled paths (render mode 0), stroked ones (1), both (2),
//     or nothing (3); modes 4 to 7 add the glyphs to the clip at the end of the
//     text object. A stand-in for a bold face is filled and not thickened.
//   - A Type 3 glyph becomes a Form XObject of its own, drawn where the glyph
//     was. A glyph that only sets a shape (d1) takes the colour of the text.
//   - Text in a Form XObject or a tiling pattern is outlined too, in a copy:
//     the original is shared with pages that were not named and stays as it was.
//   - A font written vertically makes the write fail with [ErrOutlineVertical],
//     and a page past the bounds with [ErrOutlineTooLarge].
//
// ⛔ What is NOT reached: the appearance streams of annotations (counted by
// [Doc.OutlineSkipped]), text added to the document afterwards by a stamp, and
// the text state a form inherits from the page that draws it. The fonts of the
// interactive form's default resources are left in the catalogue. A page cannot
// be both outlined and recoloured, redacted or have its text replaced.
func (d *Doc) OutlineText(spec string) error {
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	if len(nums) == 0 {
		return fmt.Errorf("ops: %q names no pages to outline", spec)
	}
	for _, n := range nums {
		p := &d.pages[n-1]
		if p.blank || p.tiles != nil || p.picture != nil {
			continue // made here: its text is a stamp's, which comes after
		}
		if p.recolour != nil || len(p.redactions) > 0 || len(p.textEdits) > 0 {
			return fmt.Errorf("ops: page %d is already to be recoloured, redacted or have its "+
				"text replaced, which cannot be combined with outlining it", n)
		}
		p.outline = true
	}
	return nil
}

// OutlineSkipped is how many annotations of the pages last written carry a font
// in their appearance and so still hold text after [Doc.OutlineText].
func (d *Doc) OutlineSkipped() int { return d.outlineSkipped }

// outlinedPage is the content and the resources of a page with its text as
// paths.
func (d *Doc) outlinedPage(w *reader.Writer, p Page, src reader.Dict) ([]byte, reader.Dict, error) {
	content, err := pageContent(p.src, p.number)
	if err != nil {
		return nil, nil, fmt.Errorf("ops: page %d was to have its text outlined and its content "+
			"stream could not be read: %w", p.number, err)
	}
	if d.outliners == nil {
		d.outliners = map[*reader.Document]*outliner{}
	}
	o := d.outliners[p.src]
	if o == nil {
		o = &outliner{w: w, src: p.src, ol: render.NewOutliner(p.src), forms: map[int]reader.Ref{},
			type3: map[type3Key]reader.Ref{}, pages: map[outPageKey]pageOut{}}
		d.outliners[p.src] = o
	}
	d.outlineSkipped += o.annotationsWithText(src)

	// Pages that share a content stream and a resource dictionary are one piece
	// of work: it is done once and its result reused, though each page still
	// pays for the bytes it carries.
	key := outPageKey{sum: sha256.Sum256(content), res: -1}
	if ref, ok := src.Get("Resources").(reader.Ref); ok {
		key.res = ref.Num
	}
	if hit, ok := o.pages[key]; ok && key.res >= 0 {
		if err := o.charge(len(hit.body)); err != nil {
			return nil, nil, fmt.Errorf("page %d: %w", p.number, err)
		}
		return hit.body, hit.res, nil
	}
	res, _ := p.src.GetDict(src, "Resources")
	out, newRes, err := o.rewrite(content, res, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("page %d: %w", p.number, err)
	}
	if key.res >= 0 {
		o.pages[key] = pageOut{out, newRes}
	}
	return out, newRes, nil
}

// An outliner rewrites content streams of one source document into one writer,
// for every page of it that is outlined in one write.
type outliner struct {
	w   *reader.Writer
	src *reader.Document
	ol  *render.Outliner

	// forms are the forms and patterns already rewritten, by object number, so
	// that a shared one is written once and a form that draws itself ends.
	forms map[int]reader.Ref
	// type3 are the Type 3 glyph forms already written, by font object and
	// code: a glyph is one form for the document, not one per stream that shows
	// it, and a glyph that shows itself ends.
	type3 map[type3Key]reader.Ref
	pages map[outPageKey]pageOut

	// The write in progress, against the bounds.
	glyphs, read, wrote int
}

type type3Key struct{ font, code int }

type outPageKey struct {
	sum [32]byte
	res int
}

type pageOut struct {
	body []byte
	res  reader.Dict
}

// traceCharge, when a test sets it, is told of every charge as it is made: the
// tests assert that nothing is charged, or done, after the one that refused.
var traceCharge func(n int)

// charge adds n bytes of what is about to be written to the document's
// running total, and refuses the moment it is past the bound.
func (o *outliner) charge(n int) error {
	if traceCharge != nil {
		traceCharge(n)
	}
	if o.wrote += n; o.wrote > maxOutlineBytes {
		return fmt.Errorf("%w: more than %d bytes of paths", ErrOutlineTooLarge, maxOutlineBytes)
	}
	return nil
}

// objCost is what a resource object built or copied directly costs: a node
// each, and the length of its names.
func objCost(o reader.Object, depth int) int {
	n := outlineNodeCost
	if depth > 32 {
		return n
	}
	switch v := o.(type) {
	case reader.Dict:
		for k, e := range v {
			n += len(k) + objCost(e, depth+1)
		}
	case reader.Array:
		for _, e := range v {
			n += objCost(e, depth+1)
		}
	}
	return n
}

// A textState is the part of the graphics state that text uses, which q and Q
// save and restore.
type textState struct {
	font                 *render.GlyphFont
	fontName             reader.Name
	size, tc, tw, tz, tl float64
	rise                 float64
	mode                 int
}

type contentEdit struct {
	at, end int
	with    string
}

// rewrite is a content stream with its text as paths, and the resources it now
// needs: the originals less /Font, with forms and patterns rewritten and a form
// for each Type 3 glyph drawn.
func (o *outliner) rewrite(data []byte, res reader.Dict, depth int) ([]byte, reader.Dict, error) {
	if depth > maxOutlineNesting {
		return nil, nil, fmt.Errorf("%w: forms nested more than %d deep", ErrOutlineTooLarge, maxOutlineNesting)
	}
	if o.read += len(data); o.read > maxOutlineBytes {
		return nil, nil, fmt.Errorf("%w: more than %d bytes of content", ErrOutlineTooLarge, maxOutlineBytes)
	}
	newRes, xobj, err := o.rebuild(res, depth)
	if err != nil {
		return nil, nil, err
	}
	// The resources are charged as they are built, a Type 3 glyph's and a form's
	// included: they are output like any path.
	if err := o.charge(objCost(newRes, 0)); err != nil {
		return nil, nil, err
	}
	c := &textRun{o: o, data: data, res: res, xobj: xobj, depth: depth,
		cur: textState{tz: 1}, fonts: map[reader.Name]*render.GlyphFont{}, fontRefs: map[reader.Name]int{}}
	c.tm, c.tlm, c.cmAcc = identity, identity, identity
	scan := reader.NewContentScanner(data)
	for {
		op, ok := scan.Next()
		if !ok {
			break
		}
		before := len(c.edits)
		if err := c.step(op); err != nil {
			return nil, nil, err
		}
		if n := len(c.edits) - before; n > 0 {
			if err := o.charge(n * outlineEditCost); err != nil {
				return nil, nil, err
			}
		}
	}
	// A text object left open still clips what follows it.
	pending, err := c.clipText()
	if err != nil {
		return nil, nil, err
	}
	if pending != "" {
		c.edits = append(c.edits, contentEdit{len(data), len(data), "\n" + pending})
	}
	if len(xobj) > 0 {
		newRes["XObject"] = xobj
	}
	o.stripProperties(newRes, res, c.stripped)
	if err := o.charge(len(data)); err != nil { // what is copied over as it was
		return nil, nil, err
	}
	var sb strings.Builder
	at := 0
	for _, e := range c.edits {
		sb.Write(data[at:e.at])
		sb.WriteString(e.with)
		at = e.end
	}
	sb.Write(data[at:])
	return []byte(sb.String()), newRes, nil
}

// rebuild copies resources without their fonts, rewriting the forms and
// patterns, and hands back the XObject dictionary being built so that Type 3
// glyphs can be added to it.
func (o *outliner) rebuild(res reader.Dict, depth int) (out, xobj reader.Dict, err error) {
	out, xobj = reader.Dict{}, reader.Dict{}
	for k, v := range res {
		switch k {
		case "Font":
			// Not carried over: this is the point.
		case "XObject":
			xo, _ := o.src.GetDict(res, "XObject")
			for name, entry := range xo {
				if xobj[name], err = o.member(entry, "Form", res, depth); err != nil {
					return nil, nil, err
				}
			}
		case "Pattern":
			pats, _ := o.src.GetDict(res, "Pattern")
			rebuilt := reader.Dict{}
			for name, entry := range pats {
				if rebuilt[name], err = o.member(entry, "", res, depth); err != nil {
					return nil, nil, err
				}
			}
			out[k] = rebuilt
		case "ExtGState":
			// A graphics state parameter dictionary may name a font (/Font), which
			// would bring the font back by the side door.
			states, _ := o.src.GetDict(res, "ExtGState")
			rebuilt := reader.Dict{}
			for name, entry := range states {
				st, ok := o.src.GetDict(reader.Dict{"x": entry}, "x")
				if !ok {
					rebuilt[name] = o.w.Copy(o.src, entry)
					continue
				}
				kept := reader.Dict{}
				for sk, sv := range st {
					switch sk {
					case "Font":
					case "SMask":
						// A soft mask draws a form, which may hold text and its font.
						if kept[sk], err = o.softMask(sv, res, depth); err != nil {
							return nil, nil, err
						}
					default:
						kept[sk] = o.w.Copy(o.src, sv)
					}
				}
				rebuilt[name] = kept
			}
			out[k] = rebuilt
		default:
			out[k] = o.w.Copy(o.src, v)
		}
	}
	return out, xobj, nil
}

// stripProperties replaces, in the resources being built, the property lists
// that named stripped entries by copies without the text entries.
func (o *outliner) stripProperties(newRes, res reader.Dict, names map[reader.Name]bool) {
	src, _ := o.src.GetDict(res, "Properties")
	out, _ := newRes["Properties"].(reader.Dict)
	for name := range names {
		pd, _ := o.src.GetDict(src, name)
		kept := reader.Dict{}
		for k, v := range pd {
			if !slices.Contains(markedText, k) {
				kept[k] = o.w.Copy(o.src, v)
			}
		}
		out[name] = kept
	}
}

// softMask is a soft-mask dictionary written out with the form it draws (/G)
// rewritten as any other form is. /None and what is not a dictionary are copied.
func (o *outliner) softMask(entry reader.Object, parent reader.Dict, depth int) (reader.Object, error) {
	sm, ok := o.src.GetDict(reader.Dict{"x": entry}, "x")
	if !ok {
		return o.w.Copy(o.src, entry), nil
	}
	out := reader.Dict{}
	for k, v := range sm {
		if k != "G" {
			out[k] = o.w.Copy(o.src, v)
			continue
		}
		g, err := o.member(v, "Form", parent, depth)
		if err != nil {
			return nil, err
		}
		out[k] = g
	}
	return out, nil
}

// member is a resource entry written out: a form (subtype) or a tiling pattern
// (empty subtype) rewritten, anything else copied as it is.
func (o *outliner) member(entry reader.Object, subtype reader.Name, parent reader.Dict, depth int) (reader.Object, error) {
	s, ok := reader.ToStream(resolveObj(o.src, entry))
	if !ok {
		return o.w.Copy(o.src, entry), nil
	}
	if subtype != "" {
		if st, _ := reader.ToName(resolveObj(o.src, s.Dict.Get("Subtype"))); st != subtype {
			return o.w.Copy(o.src, entry), nil
		}
	} else if pt, _ := reader.ToInt(resolveObj(o.src, s.Dict.Get("PatternType"))); pt != 1 {
		return o.w.Copy(o.src, entry), nil
	}
	ref, isRef := entry.(reader.Ref)
	if isRef {
		if done, hit := o.forms[ref.Num]; hit {
			return done, nil
		}
	}
	if err := o.charge(outlineFormCost); err != nil {
		return nil, err
	}
	out := o.w.Reserve()
	if isRef {
		o.forms[ref.Num] = out
	}
	data, _, err := o.src.DecodeStream(s)
	if err != nil {
		return nil, fmt.Errorf("ops: a form whose text was to be outlined could not be read: %w", err)
	}
	res, ok := o.src.GetDict(s.Dict, "Resources")
	if !ok {
		res = parent
	}
	body, newRes, err := o.rewrite(data, res, depth+1)
	if err != nil {
		return nil, err
	}
	d := reader.Dict{}
	for k, v := range s.Dict {
		switch k {
		case "Length", "Filter", "DecodeParms", "DL", "Resources":
		default:
			d[k] = o.w.Copy(o.src, v)
		}
	}
	d["Resources"] = newRes
	if err := o.charge(objCost(d, 0)); err != nil {
		return nil, err
	}
	o.w.Put(out, contentStream(body, d))
	return out, nil
}

// annotationsWithText counts the annotations of a page that still hold text
// after the page is outlined, because their appearances are not rewritten: an
// appearance with a font in its resources, whether the stream is /N itself or
// one of the states of a dictionary /N, or a form it draws; and a FreeText
// annotation's /Contents or a widget's value, which are text whatever the
// appearance says. /R and /D (rollover, down) are appearances too.
func (o *outliner) annotationsWithText(page reader.Dict) int {
	annots, _ := reader.ToArray(resolveObj(o.src, page.Get("Annots")))
	n := 0
	seen := map[int]bool{}
	for _, a := range annots {
		if ad, ok := reader.ToDict(resolveObj(o.src, a)); ok && o.holdsText(ad, seen) {
			n++
		}
	}
	return n
}

// holdsText says whether one annotation still holds text.
func (o *outliner) holdsText(ad reader.Dict, seen map[int]bool) bool {
	sub, _ := reader.ToName(resolveObj(o.src, ad.Get("Subtype")))
	for _, key := range textKeys[sub] {
		if s, ok := reader.ToString(resolveObj(o.src, ad.Get(key))); ok && len(s) > 0 {
			return true
		}
	}
	ap, _ := o.src.GetDict(ad, "AP")
	for _, key := range []reader.Name{"N", "R", "D"} {
		v := resolveObj(o.src, ap.Get(key))
		if s, ok := reader.ToStream(v); ok {
			if o.formHoldsFont(s, seen, 0) {
				return true
			}
			continue
		}
		// An appearance state dictionary: one stream per state.
		states, _ := reader.ToDict(v)
		for _, st := range states {
			if s, ok := reader.ToStream(resolveObj(o.src, st)); ok && o.formHoldsFont(s, seen, 0) {
				return true
			}
		}
	}
	return false
}

// textKeys are the entries that hold text of their own, by annotation type.
var textKeys = map[reader.Name][]reader.Name{
	"FreeText": {"Contents", "RC"},
	"Widget":   {"V"},
}

// formHoldsFont says whether a form, or a form it draws, has a font in its
// resources. seen holds the verdict on each form already looked at, which is
// also what ends a cycle, and the nesting is bounded as it is everywhere else.
func (o *outliner) formHoldsFont(s *reader.Stream, seen map[int]bool, depth int) bool {
	if depth > maxOutlineNesting {
		return true // too deep to look at: assume the worst, which is what the count is for
	}
	res, _ := o.src.GetDict(s.Dict, "Resources") // none: no fonts, no forms, nothing found below
	if fonts, ok := o.src.GetDict(res, "Font"); ok && len(fonts) > 0 {
		return true
	}
	xo, _ := o.src.GetDict(res, "XObject")
	for _, entry := range xo {
		ref, isRef := entry.(reader.Ref)
		if isRef {
			if verdict, done := seen[ref.Num]; done {
				if verdict {
					return true
				}
				continue
			}
			seen[ref.Num] = false
		}
		inner, ok := reader.ToStream(resolveObj(o.src, entry))
		if !ok {
			continue
		}
		if o.formHoldsFont(inner, seen, depth+1) {
			if isRef {
				seen[ref.Num] = true
			}
			return true
		}
	}
	return false
}

func resolveObj(d *reader.Document, o reader.Object) reader.Object {
	v, _ := d.Resolve(o) // an unresolvable reference is nil, which is no stream, dictionary or name
	return v
}

// A textRun is the state of one content stream being rewritten.
type textRun struct {
	o     *outliner
	data  []byte
	res   reader.Dict
	xobj  reader.Dict
	depth int
	fonts map[reader.Name]*render.GlyphFont
	// fontRefs are the object numbers of the fonts named, -1 for a direct
	// dictionary, which has no identity a document-wide cache could use.
	fontRefs map[reader.Name]int

	cur      textState
	stack    []textState
	tm, tlm  [6]float64
	edits    []contentEdit
	glyphIDs map[glyphKey]reader.Name

	// clip is the pending text clip of modes 4 to 7, applied at ET. Its chunks
	// remember the cm operations seen before them, since a cm between a glyph
	// and ET changes the space the path is written in.
	clip  []clipChunk
	cmAcc [6]float64 // the cm operations since BT, as one matrix

	// marks are the open marked-content sequences. One that carries text of its
	// own (ActualText, Alt, E) is an edit that is put back as it was until text
	// is drawn inside it, when it is rewritten without them.
	marks []mark

	// stripped are the /Properties entries whose text entries were drawn over:
	// the resource is rewritten without them, since a property list is read by
	// whoever extracts the text, in place of what is drawn.
	stripped map[reader.Name]bool

	buf  []byte // a glyph's path, reused
	over bool   // a number was past maxOutlineCoord
}

type clipChunk struct {
	acc  [6]float64
	path []byte
}

type mark struct {
	edit int // the index of its edit, -1 when it carries no text of its own
	tag  reader.Name
	prop reader.Name // the /Properties entry it names, "" for an inline list
	mcid int         // -1 when it has none
	done bool
}

type glyphKey struct {
	font reader.Name
	code int
}

var identity = [6]float64{1, 0, 0, 1, 0, 0}

// mul is a then b: the matrix that maps a point through a and then through b.
func mul(a, b [6]float64) [6]float64 {
	return [6]float64{
		a[0]*b[0] + a[1]*b[2], a[0]*b[1] + a[1]*b[3],
		a[2]*b[0] + a[3]*b[2], a[2]*b[1] + a[3]*b[3],
		a[4]*b[0] + a[5]*b[2] + b[4], a[4]*b[1] + a[5]*b[3] + b[5],
	}
}

func (c *textRun) replace(op reader.Operation, with string) {
	c.edits = append(c.edits, contentEdit{op.Start, op.End, with})
}

func numbers(ops []reader.Object, n int) ([]float64, bool) {
	if len(ops) < n {
		return nil, false
	}
	out := make([]float64, n)
	for i := range out {
		v, ok := reader.ToFloat(ops[len(ops)-n+i])
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

func (c *textRun) font(name reader.Name) *render.GlyphFont {
	f, loaded := c.fonts[name]
	if !loaded { // a direct font dictionary is loaded again each time it is named
		c.fontRefs[name] = -1
		if fonts, ok := c.o.src.GetDict(c.res, "Font"); ok {
			if entry, has := fonts[name]; has {
				f, _ = c.o.ol.Font(entry)
				if ref, isRef := entry.(reader.Ref); isRef {
					c.fontRefs[name] = ref.Num
				}
			}
		}
		c.fonts[name] = f
	}
	return f
}

// clipText is the pending text clip as content, and forgets it: each chunk is
// written under the matrix that puts it back where its glyphs were, and the
// matrices are undone after it, so that the clip is the same whatever cm came
// between a glyph and the end of the text object. "" when nothing is pending.
func (c *textRun) clipText() (string, error) {
	if len(c.clip) == 0 {
		c.cmAcc = identity
		return "", nil
	}
	inv, ok := invert(c.cmAcc)
	if !ok {
		return "", fmt.Errorf("%w: a text object clips under a matrix that cannot be inverted", ErrOutlineTooLarge)
	}
	var sb strings.Builder
	for _, ch := range c.clip {
		if ch.acc == c.cmAcc {
			sb.Write(ch.path)
			continue
		}
		x := mul(ch.acc, inv)
		back, ok := invert(x)
		if !ok {
			return "", fmt.Errorf("%w: a text object clips under a matrix that cannot be inverted", ErrOutlineTooLarge)
		}
		fmt.Fprintf(&sb, "%s cm\n", c.matrix(x))
		sb.Write(ch.path)
		fmt.Fprintf(&sb, "%s cm\n", c.matrix(back))
	}
	sb.WriteString("W n")
	c.clip, c.cmAcc = nil, identity
	if c.over {
		return "", fmt.Errorf("%w: a coordinate past %g", ErrOutlineTooLarge, maxOutlineCoord)
	}
	return sb.String(), nil
}

// invert is the inverse of a matrix, and false when there is none.
func invert(m [6]float64) ([6]float64, bool) {
	det := m[0]*m[3] - m[1]*m[2]
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return identity, false
	}
	a, b, cc, d := m[3]/det, -m[1]/det, -m[2]/det, m[0]/det
	return [6]float64{a, b, cc, d, -(m[4]*a + m[5]*cc), -(m[4]*b + m[5]*d)}, true
}

// matrix writes six numbers of a cm, with the precision a scale needs.
func (c *textRun) matrix(m [6]float64) string {
	var b []byte
	for i, v := range m {
		if i > 0 {
			b = append(b, ' ')
		}
		if i < 4 {
			b = c.appendNum(b, v, 8)
		} else {
			b = c.appendNum(b, v, 4)
		}
	}
	return string(b)
}

// step handles one operation.
func (c *textRun) step(op reader.Operation) error {
	switch op.Operator {
	case "q":
		if len(c.stack) >= maxOutlineQDepth {
			return fmt.Errorf("%w: q nested more than %d deep", ErrOutlineTooLarge, maxOutlineQDepth)
		}
		c.stack = append(c.stack, c.cur)
	case "Q":
		if n := len(c.stack); n > 0 {
			c.cur, c.stack = c.stack[n-1], c.stack[:n-1]
		}
	case "cm":
		if v, ok := numbers(op.Operands, 6); ok {
			c.cmAcc = mul([6]float64{v[0], v[1], v[2], v[3], v[4], v[5]}, c.cmAcc)
		}
	case "BT", "ET":
		// A text object that was not closed still clips what follows it.
		with, err := c.clipText()
		if err != nil {
			return err
		}
		if with == "" {
			with = " "
		}
		if op.Operator == "BT" {
			c.tm, c.tlm = identity, identity
			with += " "
		}
		c.replace(op, with)
	case "BMC", "BDC":
		return c.beginMarked(op)
	case "EMC":
		if n := len(c.marks); n > 0 {
			c.marks = c.marks[:n-1]
		}
	case "Do":
		if c.drawsForm(op) {
			c.taint()
		}
	case "Tf":
		v, ok := numbers(op.Operands, 1)
		name, isName := reader.ToName(first(op.Operands))
		if ok && isName && len(op.Operands) == 2 {
			c.cur.fontName, c.cur.size = name, v[0]
			c.cur.font = c.font(name)
			if c.cur.font != nil && c.cur.font.Vertical() {
				return fmt.Errorf("%w (/%s)", ErrOutlineVertical, name)
			}
		}
		c.replace(op, " ")
	case "Tc", "Tw", "Tz", "TL", "Ts", "Tr":
		if v, ok := numbers(op.Operands, 1); ok {
			switch op.Operator {
			case "Tc":
				c.cur.tc = v[0]
			case "Tw":
				c.cur.tw = v[0]
			case "Tz":
				c.cur.tz = v[0] / 100
			case "TL":
				c.cur.tl = v[0]
			case "Ts":
				c.cur.rise = v[0]
			case "Tr":
				c.cur.mode = int(v[0])
			}
		}
		c.replace(op, " ")
	case "Td", "TD":
		if v, ok := numbers(op.Operands, 2); ok {
			if op.Operator == "TD" {
				c.cur.tl = -v[1]
			}
			c.move(v[0], v[1])
		}
		c.replace(op, " ")
	case "T*":
		c.move(0, -c.cur.tl)
		c.replace(op, " ")
	case "Tm":
		if v, ok := numbers(op.Operands, 6); ok {
			c.tm = [6]float64{v[0], v[1], v[2], v[3], v[4], v[5]}
			c.tlm = c.tm
		}
		c.replace(op, " ")
	case "Tj", "'", "\"", "TJ":
		return c.show(op)
	}
	return nil
}

// markedText are the entries of a marked-content property list that are text
// in their own right: the replacement text (ISO 32000-1 14.9.4), the alternate
// description (14.9.3) and the expansion of an abbreviation (14.9.5). Text
// extraction reads them in place of what is drawn, so outlining the text and
// leaving them would leave the text a reader can select and search.
var markedText = []reader.Name{"ActualText", "Alt", "E"}

// beginMarked opens a marked-content sequence. Its operation is put back as it
// was, and remembered, if it carries text of its own.
func (c *textRun) beginMarked(op reader.Operation) error {
	if len(c.marks) >= maxOutlineMarked {
		return fmt.Errorf("%w: marked content nested more than %d deep", ErrOutlineTooLarge, maxOutlineMarked)
	}
	m := mark{edit: -1, mcid: -1}
	if op.Operator == "BDC" && len(op.Operands) == 2 {
		props, ok := reader.ToDict(op.Operands[1])
		if name, isName := reader.ToName(op.Operands[1]); isName {
			if lists, has := c.o.src.GetDict(c.res, "Properties"); has {
				props, ok = c.o.src.GetDict(lists, name)
				m.prop = name
			}
		}
		if ok && holdsAny(props, markedText) {
			m.tag, _ = reader.ToName(op.Operands[0])
			if id, has := reader.ToInt(resolveObj(c.o.src, props.Get("MCID"))); has {
				m.mcid = int(id)
			}
			m.edit = len(c.edits)
			c.edits = append(c.edits, contentEdit{op.Start, op.End, string(c.data[op.Start:op.End])})
		}
	}
	c.marks = append(c.marks, m)
	return nil
}

func holdsAny(props reader.Dict, keys []reader.Name) bool {
	for _, k := range keys {
		if _, has := props[k]; has {
			return true
		}
	}
	return false
}

// taint rewrites the open sequences that carry text of their own, now that text
// is drawn inside them: what is left is the tag and the MCID, which the
// structure tree reaches the content by.
func (c *textRun) taint() {
	for i := range c.marks {
		m := &c.marks[i]
		if m.edit < 0 || m.done {
			continue
		}
		m.done = true
		if m.prop != "" {
			if c.stripped == nil {
				c.stripped = map[reader.Name]bool{}
			}
			c.stripped[m.prop] = true
		}
		tag := "Span"
		if safeName(m.tag) {
			tag = string(m.tag)
		}
		if m.mcid >= 0 {
			c.edits[m.edit].with = fmt.Sprintf("/%s <</MCID %d>> BDC", tag, m.mcid)
		} else {
			c.edits[m.edit].with = "/" + tag + " BMC"
		}
	}
}

// safeName says a tag can be written after a solidus as it is.
func safeName(n reader.Name) bool {
	if n == "" || len(n) > 64 {
		return false
	}
	for _, r := range string(n) {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// drawsForm says whether a Do draws a form, which may be text.
func (c *textRun) drawsForm(op reader.Operation) bool {
	name, _ := reader.ToName(first(op.Operands)) // not a name: "" is no entry
	xo, _ := c.o.src.GetDict(c.res, "XObject")
	s, ok := reader.ToStream(resolveObj(c.o.src, xo[name]))
	if !ok {
		return false
	}
	sub, _ := reader.ToName(resolveObj(c.o.src, s.Dict.Get("Subtype")))
	return sub == "Form"
}

func first(ops []reader.Object) reader.Object {
	if len(ops) == 0 {
		return reader.Null{}
	}
	return ops[0]
}

func (c *textRun) move(tx, ty float64) {
	c.tlm = mul([6]float64{1, 0, 0, 1, tx, ty}, c.tlm)
	c.tm = c.tlm
}

// show replaces a text-showing operation with the paths of its glyphs.
func (c *textRun) show(op reader.Operation) error {
	var sb strings.Builder
	ops := op.Operands
	switch op.Operator {
	case "'":
		c.move(0, -c.cur.tl)
	case "\"":
		if len(ops) == 3 {
			if v, ok := numbers(ops[:2], 2); ok {
				c.cur.tw, c.cur.tc = v[0], v[1]
			}
		}
		c.move(0, -c.cur.tl)
	}
	var err error
	switch op.Operator {
	case "TJ":
		arr, _ := reader.ToArray(first(ops))
		for _, el := range arr {
			if s, ok := reader.ToString(el); ok {
				err = c.glyphsOf(s, &sb)
			} else if v, ok := reader.ToFloat(el); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
				c.tm = mul([6]float64{1, 0, 0, 1, -v / 1000 * c.cur.size * c.cur.tz, 0}, c.tm)
			}
			if err != nil {
				return err
			}
		}
	default:
		if len(ops) > 0 {
			if s, ok := reader.ToString(ops[len(ops)-1]); ok {
				err = c.glyphsOf(s, &sb)
			}
		}
	}
	if err != nil {
		return err
	}
	c.replace(op, sb.String())
	return nil
}

// glyphsOf writes the glyphs of one string and moves the pen over them.
func (c *textRun) glyphsOf(s []byte, sb *strings.Builder) error {
	f := c.cur.font
	if f == nil {
		return nil // no font to draw it with: what the renderer draws as well
	}
	c.taint() // text is drawn here, in whatever marked content is open
	for _, code := range f.Codes(s) {
		if c.o.glyphs++; c.o.glyphs > maxOutlineGlyphs {
			return fmt.Errorf("%w: more than %d glyphs in the document", ErrOutlineTooLarge, maxOutlineGlyphs)
		}
		if c.cur.mode != 3 {
			place := mul([6]float64{c.cur.size * c.cur.tz, 0, 0, c.cur.size, 0, c.cur.rise}, c.tm)
			var err error
			if f.Type3() {
				err = c.type3Glyph(f, code, place, sb)
			} else {
				err = c.outlineGlyph(f, code, place, sb)
			}
			if err != nil {
				return err
			}
		}
		tx := f.Advance(code)*c.cur.size + c.cur.tc
		if f.Simple() && code == 32 {
			tx += c.cur.tw
		}
		c.tm = mul([6]float64{1, 0, 0, 1, tx * c.cur.tz, 0}, c.tm)
	}
	return nil
}

// outlineGlyph writes a glyph as a path through place, which maps text space to
// user space, painted as the text render mode says.
func (c *textRun) outlineGlyph(f *render.GlyphFont, code int, place [6]float64, sb *strings.Builder) error {
	segs, ok := f.Outline(code)
	if !ok {
		return nil
	}
	b := c.buf[:0]
	var cur, start [2]float64
	pt := func(p [2]float64) [2]float64 {
		return [2]float64{place[0]*p[0] + place[2]*p[1] + place[4], place[1]*p[0] + place[3]*p[1] + place[5]}
	}
	xy := func(b []byte, x, y float64, op string) []byte {
		b = c.appendNum(b, x, 4)
		b = append(b, ' ')
		b = c.appendNum(b, y, 4)
		return append(append(b, ' '), op...)
	}
	for _, s := range segs {
		switch s.Op {
		case render.GlyphMove:
			cur = pt(s.P[0])
			start = cur
			b = xy(b, cur[0], cur[1], "m ")
		case render.GlyphLine:
			cur = pt(s.P[0])
			b = xy(b, cur[0], cur[1], "l ")
		case render.GlyphQuad:
			q, e := pt(s.P[0]), pt(s.P[1])
			b = xy(b, cur[0]+2.0/3*(q[0]-cur[0]), cur[1]+2.0/3*(q[1]-cur[1]), "")
			b = xy(b, e[0]+2.0/3*(q[0]-e[0]), e[1]+2.0/3*(q[1]-e[1]), "")
			b = xy(b, e[0], e[1], "c ")
			cur = e
		case render.GlyphClose:
			b = append(b, "h "...)
			cur = start
		}
	}
	c.buf = b // kept for the next glyph, whatever happens below
	if c.over {
		return fmt.Errorf("%w: a coordinate past %g", ErrOutlineTooLarge, maxOutlineCoord)
	}
	mode := c.cur.mode
	paint := [8]string{"f", "S", "B", "", "f", "S", "B", ""}
	if mode < 0 || mode > 7 {
		mode = 0
	}
	p := paint[mode]
	// Charged before it is written: the work of a page is its paths, and the
	// refusal has to come while the budget is being spent, not after.
	cost := 0
	if p != "" {
		cost += len(b) + len(p) + 1
	}
	if mode >= 4 {
		cost += len(b)
	}
	if err := c.o.charge(cost); err != nil {
		return err
	}
	if p != "" {
		sb.Write(b)
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	if mode >= 4 {
		c.addClip(b)
	}
	return nil
}

// addClip adds a glyph's path to the pending clip, in the chunk of the cm
// operations seen so far.
func (c *textRun) addClip(path []byte) {
	n := len(c.clip)
	if n == 0 || c.clip[n-1].acc != c.cmAcc {
		c.clip = append(c.clip, clipChunk{acc: c.cmAcc})
		n++
	}
	c.clip[n-1].path = append(c.clip[n-1].path, path...)
}

// type3Glyph draws a Type 3 glyph as a form.
func (c *textRun) type3Glyph(f *render.GlyphFont, code int, place [6]float64, sb *strings.Builder) error {
	if c.glyphIDs == nil {
		c.glyphIDs = map[glyphKey]reader.Name{}
	}
	key := glyphKey{c.cur.fontName, code}
	name, done := c.glyphIDs[key]
	if !done {
		proc, ok := f.Type3Proc(code)
		if !ok {
			return nil
		}
		ref, err := c.o.type3Form(f, proc, type3Key{c.fontRefs[c.cur.fontName], code}, c.res, c.depth)
		if err != nil {
			return err
		}
		for i := len(c.xobj); ; i++ {
			name = reader.Name("OlG" + strconv.Itoa(i))
			if _, taken := c.xobj[name]; !taken {
				break
			}
		}
		c.xobj[name] = ref
		c.glyphIDs[key] = name
	}
	use := "q " + c.matrix(place) + " cm /" + string(name) + " Do Q\n"
	if c.over {
		return fmt.Errorf("%w: a coordinate past %g", ErrOutlineTooLarge, maxOutlineCoord)
	}
	if err := c.o.charge(len(use)); err != nil {
		return err
	}
	sb.WriteString(use)
	return nil
}

// type3Form is a Type 3 glyph procedure as a Form XObject whose matrix is the
// font's. The operators that only a glyph procedure may use (d0, d1) are taken
// out, and for a glyph that sets a shape alone (d1) so are the colours: it is
// painted in the colour of the text.
//
// A glyph of a font that is an indirect object is one form for the document:
// the form is reserved before its content is read, so that a glyph that shows
// itself, or a chain of fonts that show each other, ends, and a font reached
// from a thousand streams is written once. Each form is charged on top of its
// content.
func (o *outliner) type3Form(f *render.GlyphFont, proc *reader.Stream, key type3Key, page reader.Dict, depth int) (reader.Ref, error) {
	if key.font >= 0 {
		if done, hit := o.type3[key]; hit {
			return done, nil
		}
	}
	if err := o.charge(outlineFormCost); err != nil {
		return reader.Ref{}, err
	}
	out := o.w.Reserve()
	if key.font >= 0 {
		o.type3[key] = out
	}
	data, _, err := o.src.DecodeStream(proc)
	if err != nil {
		return reader.Ref{}, fmt.Errorf("ops: a Type 3 glyph whose text was to be outlined could not be read: %w", err)
	}
	var edits []contentEdit
	bbox := [4]float64{-2000, -2000, 2000, 2000}
	shape := false
	scan := reader.NewContentScanner(data)
	for {
		op, ok := scan.Next()
		if !ok {
			break
		}
		switch {
		case op.Operator == "d0":
			edits = append(edits, contentEdit{op.Start, op.End, " "})
		case op.Operator == "d1":
			shape = true
			if v, ok := numbers(op.Operands, 6); ok {
				bbox = [4]float64{v[2], v[3], v[4], v[5]}
			}
			edits = append(edits, contentEdit{op.Start, op.End, " "})
		case shape && colourOps[op.Operator]:
			edits = append(edits, contentEdit{op.Start, op.End, " "})
		}
	}
	var sb strings.Builder
	at := 0
	for _, e := range edits {
		sb.Write(data[at:e.at])
		sb.WriteString(e.with)
		at = e.end
	}
	sb.Write(data[at:])
	res := f.Resources()
	if res == nil {
		res = page
	}
	body, newRes, err := o.rewrite([]byte(sb.String()), res, depth+1)
	if err != nil {
		return reader.Ref{}, err
	}
	m := f.FontMatrix()
	form := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"BBox":      boxArray(bbox),
		"Matrix":    reader.Array{reader.Real(m[0]), reader.Real(m[1]), reader.Real(m[2]), reader.Real(m[3]), reader.Real(m[4]), reader.Real(m[5])},
		"Resources": newRes,
	}
	if err := o.charge(objCost(form, 0)); err != nil {
		return reader.Ref{}, err
	}
	o.w.Put(out, contentStream(body, form))
	return out, nil
}

// olNum writes a number the way a content stream may: no exponent, which PDF
// does not have, and no digits past what a point is divided into.
func olNum(v float64) string { return olFixed(v, 4) }

// olFixed is olNum with its own number of decimals: a matrix that scales is
// written with more, since its error is multiplied by the size of what it moves.
func olFixed(v float64, decimals int) string {
	var c textRun
	return string(c.appendNum(nil, v, decimals))
}

// appendNum appends a number the way olFixed writes it, with the digits
// bounded: a coordinate past maxOutlineCoord is not written (a zero is), and
// c.over says one was, so that the caller refuses. Without that a 3-byte
// "1e300" in an operand became 300 digits of output.
func (c *textRun) appendNum(b []byte, v float64, decimals int) []byte {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return append(b, '0')
	}
	if v > maxOutlineCoord || v < -maxOutlineCoord {
		c.over = true
		return append(b, '0')
	}
	start := len(b)
	b = strconv.AppendFloat(b, v, 'f', decimals, 64)
	for len(b) > start && b[len(b)-1] == '0' {
		b = b[:len(b)-1]
	}
	if len(b) > start && b[len(b)-1] == '.' {
		b = b[:len(b)-1]
	}
	if s := b[start:]; len(s) == 0 || string(s) == "-0" || string(s) == "-" {
		b = append(b[:start], '0')
	}
	return b
}
