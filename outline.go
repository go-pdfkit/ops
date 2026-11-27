// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"errors"
	"fmt"
	"math"
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

// The bounds of one page's outlining. They are variables so that a test can
// lower them: the real ones are past what a test can afford to build.
var (
	// maxOutlineGlyphs is the glyphs drawn from one page, counting every form
	// and Type 3 glyph under it once.
	maxOutlineGlyphs = 1 << 20
	// maxOutlineBytes is what one page's outlining may write, and the decoded
	// content it may read, each.
	maxOutlineBytes = 256 << 20
	// maxOutlineNesting is how deep forms, patterns and Type 3 glyphs may nest.
	maxOutlineNesting = 12
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
		o = &outliner{w: w, src: p.src, ol: render.NewOutliner(p.src), forms: map[int]reader.Ref{}}
		d.outliners[p.src] = o
	}
	o.glyphs, o.read, o.wrote = 0, 0, 0
	res, _ := p.src.GetDict(src, "Resources")
	out, newRes, err := o.rewrite(content, res, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("page %d: %w", p.number, err)
	}
	d.outlineSkipped += o.annotationsWithFonts(src)
	return out, newRes, nil
}

// An outliner rewrites content streams of one source document into one writer.
type outliner struct {
	w   *reader.Writer
	src *reader.Document
	ol  *render.Outliner

	// forms are the forms and patterns already rewritten, by object number, so
	// that a shared one is written once and a form that draws itself ends.
	forms map[int]reader.Ref

	// The page in progress, against the bounds.
	glyphs, read, wrote int
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
	c := &textRun{o: o, res: res, xobj: xobj, depth: depth,
		cur: textState{tz: 1}, fonts: map[reader.Name]*render.GlyphFont{}}
	c.tm, c.tlm = identity, identity
	scan := reader.NewContentScanner(data)
	for {
		op, ok := scan.Next()
		if !ok {
			break
		}
		if err := c.step(op); err != nil {
			return nil, nil, err
		}
	}
	if len(xobj) > 0 {
		newRes["XObject"] = xobj
	}
	var sb strings.Builder
	at := 0
	for _, e := range c.edits {
		sb.Write(data[at:e.at])
		sb.WriteString(e.with)
		at = e.end
	}
	sb.Write(data[at:])
	if o.wrote += sb.Len(); o.wrote > maxOutlineBytes {
		return nil, nil, fmt.Errorf("%w: more than %d bytes of paths", ErrOutlineTooLarge, maxOutlineBytes)
	}
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
					if sk != "Font" {
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
	o.w.Put(out, contentStream(body, d))
	return out, nil
}

// annotationsWithFonts counts the annotations of a page whose normal
// appearance has a font in its resources.
func (o *outliner) annotationsWithFonts(page reader.Dict) int {
	annots, _ := reader.ToArray(resolveObj(o.src, page.Get("Annots")))
	n := 0
	for _, a := range annots {
		ad, ok := reader.ToDict(resolveObj(o.src, a))
		if !ok {
			continue
		}
		ap, _ := o.src.GetDict(ad, "AP")
		s, ok := reader.ToStream(resolveObj(o.src, ap.Get("N")))
		if !ok {
			continue
		}
		if res, ok := o.src.GetDict(s.Dict, "Resources"); ok {
			if fonts, ok := o.src.GetDict(res, "Font"); ok && len(fonts) > 0 {
				n++
			}
		}
	}
	return n
}

func resolveObj(d *reader.Document, o reader.Object) reader.Object {
	v, _ := d.Resolve(o) // an unresolvable reference is nil, which is no stream, dictionary or name
	return v
}

// A textRun is the state of one content stream being rewritten.
type textRun struct {
	o     *outliner
	res   reader.Dict
	xobj  reader.Dict
	depth int
	fonts map[reader.Name]*render.GlyphFont

	cur      textState
	stack    []textState
	tm, tlm  [6]float64
	clip     strings.Builder
	edits    []contentEdit
	glyphIDs map[glyphKey]reader.Name
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
		if fonts, ok := c.o.src.GetDict(c.res, "Font"); ok {
			if entry, has := fonts[name]; has {
				f, _ = c.o.ol.Font(entry)
			}
		}
		c.fonts[name] = f
	}
	return f
}

// step handles one operation.
func (c *textRun) step(op reader.Operation) error {
	switch op.Operator {
	case "q":
		c.stack = append(c.stack, c.cur)
	case "Q":
		if n := len(c.stack); n > 0 {
			c.cur, c.stack = c.stack[n-1], c.stack[:n-1]
		}
	case "BT":
		c.tm, c.tlm = identity, identity
		c.replace(op, " ")
	case "ET":
		with := " "
		if c.clip.Len() > 0 {
			with = c.clip.String() + " W n"
			c.clip.Reset()
		}
		c.replace(op, with)
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
	for _, code := range f.Codes(s) {
		if c.o.glyphs++; c.o.glyphs > maxOutlineGlyphs {
			return fmt.Errorf("%w: more than %d glyphs on a page", ErrOutlineTooLarge, maxOutlineGlyphs)
		}
		if c.cur.mode != 3 {
			place := mul([6]float64{c.cur.size * c.cur.tz, 0, 0, c.cur.size, 0, c.cur.rise}, c.tm)
			var err error
			if f.Type3() {
				err = c.type3Glyph(f, code, place, sb)
			} else {
				c.outlineGlyph(f, code, place, sb)
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
		if sb.Len() > maxOutlineBytes {
			return fmt.Errorf("%w: more than %d bytes of paths", ErrOutlineTooLarge, maxOutlineBytes)
		}
	}
	return nil
}

// outlineGlyph writes a glyph as a path through place, which maps text space to
// user space, painted as the text render mode says.
func (c *textRun) outlineGlyph(f *render.GlyphFont, code int, place [6]float64, sb *strings.Builder) {
	segs, ok := f.Outline(code)
	if !ok {
		return
	}
	var path strings.Builder
	var cur, start [2]float64
	pt := func(p [2]float64) [2]float64 {
		return [2]float64{place[0]*p[0] + place[2]*p[1] + place[4], place[1]*p[0] + place[3]*p[1] + place[5]}
	}
	for _, s := range segs {
		switch s.Op {
		case render.GlyphMove:
			cur = pt(s.P[0])
			start = cur
			fmt.Fprintf(&path, "%s %s m ", olNum(cur[0]), olNum(cur[1]))
		case render.GlyphLine:
			cur = pt(s.P[0])
			fmt.Fprintf(&path, "%s %s l ", olNum(cur[0]), olNum(cur[1]))
		case render.GlyphQuad:
			q, e := pt(s.P[0]), pt(s.P[1])
			fmt.Fprintf(&path, "%s %s %s %s %s %s c ",
				olNum(cur[0]+2.0/3*(q[0]-cur[0])), olNum(cur[1]+2.0/3*(q[1]-cur[1])),
				olNum(e[0]+2.0/3*(q[0]-e[0])), olNum(e[1]+2.0/3*(q[1]-e[1])),
				olNum(e[0]), olNum(e[1]))
			cur = e
		case render.GlyphClose:
			path.WriteString("h ")
			cur = start
		}
	}
	mode := c.cur.mode
	paint := [8]string{"f", "S", "B", "", "f", "S", "B", ""}
	if mode < 0 || mode > 7 {
		mode = 0
	}
	if p := paint[mode]; p != "" {
		sb.WriteString(path.String())
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	if mode >= 4 {
		c.clip.WriteString(path.String())
	}
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
		ref, err := c.o.type3Form(f, proc, c.res, c.depth)
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
	fmt.Fprintf(sb, "q %s %s %s %s %s %s cm /%s Do Q\n",
		olFixed(place[0], 8), olFixed(place[1], 8), olFixed(place[2], 8), olFixed(place[3], 8),
		olNum(place[4]), olNum(place[5]), name)
	return nil
}

// type3Form is a Type 3 glyph procedure as a Form XObject whose matrix is the
// font's. The operators that only a glyph procedure may use (d0, d1) are taken
// out, and for a glyph that sets a shape alone (d1) so are the colours: it is
// painted in the colour of the text.
func (o *outliner) type3Form(f *render.GlyphFont, proc *reader.Stream, page reader.Dict, depth int) (reader.Ref, error) {
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
	return o.w.Add(contentStream(body, reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"BBox":      boxArray(bbox),
		"Matrix":    reader.Array{reader.Real(m[0]), reader.Real(m[1]), reader.Real(m[2]), reader.Real(m[3]), reader.Real(m[4]), reader.Real(m[5])},
		"Resources": newRes,
	})), nil
}

// olNum writes a number the way a content stream may: no exponent, which PDF
// does not have, and no digits past what a point is divided into.
func olNum(v float64) string { return olFixed(v, 4) }

// olFixed is olNum with its own number of decimals: a matrix that scales is
// written with more, since its error is multiplied by the size of what it moves.
func olFixed(v float64, decimals int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" || s == "-" {
		return "0"
	}
	return s
}
