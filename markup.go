package ops

import (
	"bytes"
	"fmt"
	"time"

	"github.com/go-pdfkit/reader"
)

// Text markup annotations: the marks a reader makes OVER a page rather than
// on it — a highlight, an underline, a strikeout, a squiggly underline.
//
// ⛔ THEY DO NOT TOUCH THE PAGE'S CONTENT. That is the whole reason to write
// one rather than draw on the page: the glyphs underneath are untouched, the
// mark can be removed again by dropping the annotation, and a reader who wants
// the document as its author wrote it gets it. Drawing a yellow rectangle into
// the content stream would be a different document.
//
// ISO 32000-1:2008, 12.5.6.10 "Text Markup Annotations", Table 179.

// A Quad is one quadrilateral of a markup annotation, in the page's own
// coordinates: the four corners of the piece of text being marked.
//
// ⛔ THE ORDER IS THE ONE PRODUCERS AGREE ON, NOT THE ONE THE PROSE SAYS.
// Table 179 says the vertices are given "in counterclockwise order", and its
// own Figure 64 shows otherwise: (x1,y1) and (x2,y2) are the TOP edge, left
// then right, and (x3,y3), (x4,y4) the BOTTOM edge, left then right — which is
// a Z, not a loop. Every widely used producer and consumer follows the figure.
// A writer that follows the sentence produces highlights that render as
// bow-ties, or not at all, in most readers.
//
// Spelling the corners out rather than taking eight numbers is the other half
// of that: an [8]float64 would let the mistake be made silently.
type Quad struct {
	TopLeftX, TopLeftY         float64
	TopRightX, TopRightY       float64
	BottomLeftX, BottomLeftY   float64
	BottomRightX, BottomRightY float64
}

// QuadOf is the quadrilateral of an upright rectangle, which is what a line of
// horizontal text is.
func QuadOf(x0, y0, x1, y1 float64) Quad {
	lo, hi := min(y0, y1), max(y0, y1)
	left, right := min(x0, x1), max(x0, x1)
	return Quad{
		TopLeftX: left, TopLeftY: hi,
		TopRightX: right, TopRightY: hi,
		BottomLeftX: left, BottomLeftY: lo,
		BottomRightX: right, BottomRightY: lo,
	}
}

// numbers is the quad as the eight numbers Table 179 asks for, in order.
func (q Quad) numbers() []float64 {
	return []float64{
		q.TopLeftX, q.TopLeftY, q.TopRightX, q.TopRightY,
		q.BottomLeftX, q.BottomLeftY, q.BottomRightX, q.BottomRightY,
	}
}

// bounds is the smallest upright rectangle holding the quad.
func (q Quad) bounds() (x0, y0, x1, y1 float64) {
	xs := []float64{q.TopLeftX, q.TopRightX, q.BottomLeftX, q.BottomRightX}
	ys := []float64{q.TopLeftY, q.TopRightY, q.BottomLeftY, q.BottomRightY}
	x0, x1 = xs[0], xs[0]
	y0, y1 = ys[0], ys[0]
	for i := 1; i < 4; i++ {
		x0, x1 = min(x0, xs[i]), max(x1, xs[i])
		y0, y1 = min(y0, ys[i]), max(y1, ys[i])
	}
	return x0, y0, x1, y1
}

// A MarkupKind is which mark to make.
type MarkupKind uint8

const (
	MarkHighlight MarkupKind = iota
	MarkUnderline
	MarkStrikeOut
	MarkSquiggly
)

// subtype is the name Table 179 requires for this kind.
func (k MarkupKind) subtype() reader.Name {
	switch k {
	case MarkUnderline:
		return "Underline"
	case MarkStrikeOut:
		return "StrikeOut"
	case MarkSquiggly:
		return "Squiggly"
	}
	return "Highlight"
}

// A Markup is one mark over a stretch of a page's text.
type Markup struct {
	// Page is 1-based.
	Page int
	// Kind is which mark to make; the zero value is a highlight.
	Kind MarkupKind
	// Quads are the pieces of text to mark, one per line — "each quadrilateral
	// shall encompass a word or group of contiguous words" (Table 179).
	Quads []Quad
	// R, G, B are the mark's colour in 0..1. The zero value is the yellow a
	// highlighter is, rather than black, which would hide the words.
	R, G, B float64
	// Author and Note are what the mark says about itself: /T and /Contents.
	// Both may be empty.
	Author, Note string
}

// defaultYellow is the colour of a highlight nobody chose one for.
var defaultYellow = [3]float64{1, 0.93, 0.36}

// colour is the mark's colour, with the zero value read as the default rather
// than as black — which for a highlight would paint over the words it marks.
func (m Markup) colour() (r, g, b float64) {
	if m.R == 0 && m.G == 0 && m.B == 0 {
		return defaultYellow[0], defaultYellow[1], defaultYellow[2]
	}
	return clamp01(m.R), clamp01(m.G), clamp01(m.B)
}

func clamp01(v float64) float64 { return min(max(v, 0), 1) }

// Mark puts text markup annotations into the document.
//
// ⛔ IT REFUSES BEFORE THE DOCUMENT IS WRITTEN, as AddFields does: a mark on a
// page the document has not, or with no quadrilaterals, is a mistake the
// caller can still do something about. QuadPoints is REQUIRED by Table 179, so
// a markup with none is not an annotation with something missing — it is one
// that marks nothing.
//
// The marks are held until the write, because an annotation names the page
// object it sits on and the pages have no objects until then.
func (d *Doc) Mark(marks ...Markup) error {
	for i, m := range marks {
		if m.Page < 1 || m.Page > len(d.pages) {
			return fmt.Errorf("mark %d is on page %d and the document has %d",
				i+1, m.Page, len(d.pages))
		}
		if len(m.Quads) == 0 {
			return fmt.Errorf("mark %d on page %d covers nothing: a text markup "+
				"annotation needs at least one quadrilateral", i+1, m.Page)
		}
	}
	d.marks = append(d.marks, marks...)
	return nil
}

// addedMarks writes every mark onto the page it names.
func (d *Doc) addedMarks(w *reader.Writer, dicts []reader.Dict) {
	for _, m := range d.marks {
		attach(dicts[m.Page-1], w.Add(d.markDict(w, m)))
	}
}

// markDict is one annotation, with the appearance that makes it visible.
func (d *Doc) markDict(w *reader.Writer, m Markup) reader.Dict {
	quads := make(reader.Array, 0, 8*len(m.Quads))
	x0, y0, x1, y1 := m.Quads[0].bounds()
	for _, q := range m.Quads {
		for _, v := range q.numbers() {
			quads = append(quads, reader.Real(v))
		}
		a, b, c, e := q.bounds()
		x0, y0 = min(x0, a), min(y0, b)
		x1, y1 = max(x1, c), max(y1, e)
	}
	r, g, b := m.colour()
	dict := reader.Dict{
		"Type":       reader.Name("Annot"),
		"Subtype":    m.Kind.subtype(),
		"Rect":       rectArray(x0, y0, x1, y1),
		"QuadPoints": quads,
		"C":          reader.Array{reader.Real(r), reader.Real(g), reader.Real(b)},
		// ⛔ Printed with the page. The default for an annotation with no flags
		// is to show on screen and NOT to print, which makes a highlight that
		// vanishes the moment somebody prints what they marked up.
		"F":  reader.Integer(annotPrint),
		"M":  reader.String(pdfDate(now())),
		"AP": reader.Dict{"N": w.Add(markAppearance(m, x0, y0, x1, y1, r, g, b))},
	}
	if m.Author != "" {
		dict["T"] = reader.String(m.Author)
	}
	if m.Note != "" {
		dict["Contents"] = reader.String(m.Note)
	}
	return dict
}

// annotPrint is the flag that says an annotation is part of what is printed
// (ISO 32000-1, Table 165, bit position 3).
const annotPrint = 1 << 2

// rectArray is a rectangle as the four numbers a /Rect is.
func rectArray(x0, y0, x1, y1 float64) reader.Array {
	return reader.Array{reader.Real(x0), reader.Real(y0), reader.Real(x1), reader.Real(y1)}
}

// markAppearance draws the mark.
//
// ⛔⛔ WITHOUT THIS THE MARK IS INVISIBLE. Table 179 says the AP entry "shall
// take precedence over QuadPoints", and go-pdfkit/render goes further: it
// draws an annotation ONLY from its appearance stream and skips one that has
// none (annots.go, appearanceOf). So a highlight written with QuadPoints alone
// is a correct annotation that this fleet's own renderer does not show, and
// several other readers do not either. The quadrilaterals stay because they
// are what a reader that re-flows or copies the marked text works from.
//
// ⛔⛔ A HIGHLIGHT IS DRAWN TRANSLUCENT, NOT WITH A MULTIPLY BLEND, AND THAT
// IS A MEASURED CHOICE. Multiply is what a highlighter really is — yellow over
// black leaves the black — and it is what Acrobat writes. go-pdfkit/render
// implements no blend modes at all: there is no "BM" anywhere in it. A
// Multiply highlight therefore paints an OPAQUE yellow block over the sentence
// it marks in this fleet's own renderer, which is the one failure a highlight
// must not have. Measured: the black bar under it came back 255 237 92.
//
// Constant alpha IS implemented (exec.go carries fillAlpha), is understood by
// every reader, and leaves the words legible. The alpha lives in the
// appearance's own graphics state rather than in the annotation's /CA, because
// a reader that applied both would dim the mark twice.
func markAppearance(m Markup, x0, y0, x1, y1, r, g, b float64) *reader.Stream {
	var body bytes.Buffer
	fmt.Fprintf(&body, "/GS gs %.4f %.4f %.4f rg %.4f %.4f %.4f RG\n", r, g, b, r, g, b)
	for _, q := range m.Quads {
		a, c, e, f := q.bounds()
		switch m.Kind {
		case MarkUnderline:
			// A line along the foot of the text, a tenth of its height thick.
			t := max((f-c)*0.08, 0.5)
			fmt.Fprintf(&body, "%.4f %.4f %.4f %.4f re f\n", a, c, e-a, t)
		case MarkStrikeOut:
			// Through the middle, where a pen would go.
			t := max((f-c)*0.08, 0.5)
			fmt.Fprintf(&body, "%.4f %.4f %.4f %.4f re f\n", a, (c+f)/2-t/2, e-a, t)
		case MarkSquiggly:
			writeSquiggle(&body, a, c, e, f)
		default:
			fmt.Fprintf(&body, "%.4f %.4f %.4f %.4f re f\n", a, c, e-a, f-c)
		}
	}
	return &reader.Stream{
		Dict: reader.Dict{
			"Type":    reader.Name("XObject"),
			"Subtype": reader.Name("Form"),
			"BBox":    rectArray(x0, y0, x1, y1),
			"Resources": reader.Dict{"ExtGState": reader.Dict{
				"GS": reader.Dict{
					"Type": reader.Name("ExtGState"),
					"ca":   reader.Real(alphaFor(m.Kind)),
					"CA":   reader.Real(alphaFor(m.Kind)),
				},
			}},
		},
		Raw: body.Bytes(),
	}
}

// highlightAlpha is how solid a highlight is: enough to see the colour, little
// enough to read the sentence under it.
const highlightAlpha = 0.4

// alphaFor is how solid the mark is. Only a highlight is a wash over the
// words; an underline, a strikeout and a squiggly are lines drawn over them
// and are meant to be seen.
func alphaFor(k MarkupKind) float64 {
	if k == MarkHighlight {
		return highlightAlpha
	}
	return 1
}

// squiggleStep is how wide one tooth of a squiggly underline is.
const squiggleStep = 3.0

// writeSquiggle draws the jagged underline of a Squiggly annotation, which is
// a line and not a rectangle: a zigzag along the foot of the text.
func writeSquiggle(body *bytes.Buffer, x0, y0, x1, y1 float64) {
	t := max((y1-y0)*0.06, 0.4)
	fmt.Fprintf(body, "%.4f w %.4f %.4f m\n", t, x0, y0)
	up := true
	for x := x0 + squiggleStep; x < x1; x += squiggleStep {
		y := y0
		if up {
			y = y0 + squiggleStep/2
		}
		fmt.Fprintf(body, "%.4f %.4f l\n", x, y)
		up = !up
	}
	fmt.Fprintf(body, "%.4f %.4f l S\n", x1, y0)
}

// now is a variable so a test can hold the clock still: an annotation carries
// the moment it was made, and a document that cannot be written twice to the
// same bytes is one nothing can check for reproducibility.
var now = time.Now

// pdfDate is a moment in the form a PDF date string takes
// (ISO 32000-1, 7.9.4): D:YYYYMMDDHHmmSSOHH'mm.
func pdfDate(t time.Time) string {
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return fmt.Sprintf("D:%04d%02d%02d%02d%02d%02d%s%02d'%02d",
		t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(),
		sign, offset/3600, (offset%3600)/60)
}
