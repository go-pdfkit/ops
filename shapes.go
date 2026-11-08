package ops

import (
	"bytes"
	"fmt"
	"math"

	"github.com/go-pdfkit/reader"
)

// Shapes drawn over a page: a line, which may carry an arrowhead, a rectangle
// and an ellipse.
//
// ⛔ ANNOTATIONS, NOT INK. Like the marks and the comments beside them, these
// sit over the page rather than in its content stream: the document underneath
// is the author's, the shape comes off again by dropping the annotation, and a
// reader who wants the paper as published gets it. Drawing a rectangle into the
// content stream would be a different document.
//
// ISO 32000-1:2008, 12.5.6.7 (Line, Table 175), 12.5.6.8 (Square and Circle,
// Table 177) and Table 176 for the line endings.

// A ShapeKind is which shape to draw.
type ShapeKind uint8

const (
	// ShapeLine is a single straight line: "The purpose of a line annotation is
	// to display a single straight line on the page" (12.5.6.7).
	ShapeLine ShapeKind = iota
	// ShapeRectangle and ShapeEllipse are Table 177's Square and Circle, whose
	// names the spec itself apologises for: "Despite the names square and
	// circle, the width and height of the annotation rectangle need not be
	// equal." They are named here for what they draw.
	ShapeRectangle
	ShapeEllipse
)

// subtype is the name the spec requires for this kind.
func (k ShapeKind) subtype() reader.Name {
	switch k {
	case ShapeRectangle:
		return "Square"
	case ShapeEllipse:
		return "Circle"
	}
	return "Line"
}

// A Shape is one line, rectangle or ellipse over a page.
type Shape struct {
	// Page is 1-based.
	Page int
	// Kind is which shape; the zero value is a line.
	Kind ShapeKind
	// X0, Y0, X1, Y1 are the line's two ends, or the box a rectangle or an
	// ellipse is drawn inside, in the page's own points.
	X0, Y0, X1, Y1 float64
	// R, G, B are the colour of the line itself, in 0..1. The zero value is
	// black, which is what somebody drawing a line on a page means by it.
	R, G, B float64
	// Fill, when set, is the colour inside a rectangle or an ellipse and under
	// an arrowhead.
	//
	// ⛔ A POINTER, because Table 177's IC distinguishes "no colour;
	// transparent" — an array of NO elements — from a colour that happens to be
	// black. A value with a flag beside it would be two fields that can
	// disagree; a nil here is the spec's empty array.
	Fill *[3]float64
	// Width is how thick the line is, in points; zero means one.
	Width float64
	// Arrow draws an arrowhead at the X1, Y1 end of a line. It is Table 176's
	// ClosedArrow, which is "a triangular closed arrowhead filled with the
	// annotation's interior color, if any".
	Arrow bool
	// Author and Note are /T and /Contents. Both may be empty.
	Author, Note string
}

// width is the line's thickness, with zero read as one rather than as
// invisible: a shape nobody can see is not what a caller who left it alone
// asked for.
func (s Shape) width() float64 {
	if s.Width <= 0 {
		return 1
	}
	return s.Width
}

// box is the shape's rectangle, normalised so x0 <= x1 and y0 <= y1.
func (s Shape) box() (x0, y0, x1, y1 float64) {
	return min(s.X0, s.X1), min(s.Y0, s.Y1), max(s.X0, s.X1), max(s.Y0, s.Y1)
}

// AddShapes puts line, rectangle and ellipse annotations into the document.
//
// ⛔ Refused before the write, as Mark, AddNotes and AddFreeText are, and for
// their reason: a caller told at once can draw somewhere else, and one told at
// Save has already been promised the shape by everything in between.
func (d *Doc) AddShapes(shapes ...Shape) error {
	for i, sh := range shapes {
		if sh.Page < 1 || sh.Page > len(d.pages) {
			return fmt.Errorf("shape %d is on page %d and the document has %d",
				i+1, sh.Page, len(d.pages))
		}
		if sh.Kind > ShapeEllipse {
			return fmt.Errorf("shape %d on page %d asks for kind %d, and there "+
				"are three: a line, a rectangle and an ellipse",
				i+1, sh.Page, sh.Kind)
		}
		x0, y0, x1, y1 := sh.box()
		if sh.Kind == ShapeLine {
			// ⛔ A line of no length is the one degenerate case worth naming: it
			// draws nothing, and the arrowhead has no direction to point in.
			if x0 == x1 && y0 == y1 {
				return fmt.Errorf("shape %d on page %d is a line from a point to "+
					"itself, which draws nothing", i+1, sh.Page)
			}
		} else if x1-x0 <= 0 || y1-y0 <= 0 {
			return fmt.Errorf("shape %d on page %d is a %s of %g by %g points",
				i+1, sh.Page, sh.Kind.subtype(), x1-x0, y1-y0)
		}
		for _, v := range []float64{sh.R, sh.G, sh.B} {
			if v < 0 || v > 1 {
				return fmt.Errorf("shape %d on page %d has a colour component of "+
					"%v, and they run from 0 to 1", i+1, sh.Page, v)
			}
		}
		if sh.Fill != nil {
			for _, v := range sh.Fill {
				if v < 0 || v > 1 {
					return fmt.Errorf("shape %d on page %d has a fill component "+
						"of %v, and they run from 0 to 1", i+1, sh.Page, v)
				}
			}
		}
		d.shapes = append(d.shapes, sh)
	}
	return nil
}

// addedShapes writes every shape onto the page it names.
func (d *Doc) addedShapes(w *reader.Writer, dicts []reader.Dict) {
	for _, sh := range d.shapes {
		attach(dicts[sh.Page-1], w.Add(d.shapeDict(w, sh)))
	}
}

// shapeDict is one shape annotation.
func (d *Doc) shapeDict(w *reader.Writer, sh Shape) reader.Dict {
	x0, y0, x1, y1 := sh.box()
	// ⛔ THE RECT IS GROWN BY HALF THE LINE WIDTH. 12.5.6.8 says the shape is
	// "inscribed within the annotation rectangle", and a stroke straddles the
	// path it follows — so a 10-point border on the box's own edge would put
	// five points of ink outside the Rect, which readers clip. Figure 63 shows
	// an 18-point border drawn inside the rectangle for exactly this reason.
	pad := sh.width() / 2
	dict := reader.Dict{
		"Type":    reader.Name("Annot"),
		"Subtype": sh.Kind.subtype(),
		"Rect":    rectArray(x0-pad, y0-pad, x1+pad, y1+pad),
		"C": reader.Array{reader.Real(clamp01(sh.R)), reader.Real(clamp01(sh.G)),
			reader.Real(clamp01(sh.B))},
		"BS": reader.Dict{"W": reader.Real(sh.width()), "S": reader.Name("S")},
		"F":  reader.Integer(annotPrint),
		"M":  reader.String(pdfDate(now())),
		"AP": reader.Dict{"N": w.Add(shapeAppearance(sh))},
	}
	// ⛔ IC is written only when there IS a fill. Table 177 gives an array of
	// NO elements the meaning "no colour; transparent", and an empty array is
	// not the same thing as the entry being absent — but a reader that honours
	// the appearance never consults either, and the two agree here only if the
	// dictionary says what the appearance draws.
	if sh.Fill != nil {
		dict["IC"] = reader.Array{reader.Real(sh.Fill[0]), reader.Real(sh.Fill[1]),
			reader.Real(sh.Fill[2])}
	}
	if sh.Kind == ShapeLine {
		// ⛔ REQUIRED by Table 175: "An array of four numbers, [x1 y1 x2 y2],
		// specifying the starting and ending coordinates of the line". And the
		// caller's own ends, not the normalised box — a line from right to left
		// is a line in that direction, which is where its arrowhead goes.
		dict["L"] = reader.Array{reader.Real(sh.X0), reader.Real(sh.Y0),
			reader.Real(sh.X1), reader.Real(sh.Y1)}
		ending := reader.Name("None")
		if sh.Arrow {
			ending = "ClosedArrow"
		}
		dict["LE"] = reader.Array{reader.Name("None"), ending}
	}
	if sh.Author != "" {
		dict["T"] = reader.String(sh.Author)
	}
	if sh.Note != "" {
		dict["Contents"] = reader.String(sh.Note)
	}
	return dict
}

// arrowLength is how long a closed arrowhead is, as a multiple of the line's
// width, and arrowSpread how wide at its base.
const (
	arrowLength = 6.0
	arrowSpread = 2.5
)

// ellipseKappa is the distance, as a fraction of the radius, at which a cubic
// Bézier's control points reproduce a quarter ellipse: 4(sqrt(2)-1)/3.
//
// ⛔ A PDF HAS NO ELLIPSE OPERATOR. 12.5.6.8 asks for one to be displayed and
// 8.5.2 offers lines and cubic curves, so an ellipse is four curves — and this
// is the constant that makes them an ellipse rather than something that looks
// like one. It is written out rather than approximated to three digits because
// the error of a rounded kappa shows at the quarter points of a large circle.
var ellipseKappa = 4 * (math.Sqrt2 - 1) / 3

// shapeAppearance draws the shape.
//
// ⛔⛔ WITHOUT THIS THE SHAPE IS INVISIBLE, which is the same wall the marks and
// the comments hit: go-pdfkit/render draws an annotation ONLY from its /AP and
// skips one that has none. A /Square with a Rect and a BS is a perfectly correct
// annotation that this fleet's own renderer shows as nothing at all, so the
// tests for these render the page and count pixels.
func shapeAppearance(sh Shape) *reader.Stream {
	x0, y0, x1, y1 := sh.box()
	pad := sh.width() / 2
	var body bytes.Buffer
	fmt.Fprintf(&body, "%s %s %s RG %s w\n",
		number(clamp01(sh.R)), number(clamp01(sh.G)), number(clamp01(sh.B)),
		number(sh.width()))
	if sh.Fill != nil {
		fmt.Fprintf(&body, "%s %s %s rg\n",
			number(sh.Fill[0]), number(sh.Fill[1]), number(sh.Fill[2]))
	}
	switch sh.Kind {
	case ShapeRectangle:
		fmt.Fprintf(&body, "%s %s %s %s re %s\n",
			number(x0), number(y0), number(x1-x0), number(y1-y0), paintOp(sh))
	case ShapeEllipse:
		writeEllipse(&body, x0, y0, x1, y1)
		fmt.Fprintln(&body, paintOp(sh))
	default:
		fmt.Fprintf(&body, "%s %s m %s %s l S\n",
			number(sh.X0), number(sh.Y0), number(sh.X1), number(sh.Y1))
		if sh.Arrow {
			writeArrow(&body, sh)
		}
	}
	return &reader.Stream{
		Dict: reader.Dict{
			"Type":    reader.Name("XObject"),
			"Subtype": reader.Name("Form"),
			"BBox":    rectArray(x0-pad, y0-pad, x1+pad, y1+pad),
		},
		Raw: body.Bytes(),
	}
}

// paintOp is how a closed shape is painted: stroked, or filled and stroked
// when there is an interior colour.
func paintOp(sh Shape) string {
	if sh.Fill != nil {
		return "B"
	}
	return "S"
}

// writeEllipse draws an ellipse inscribed in a box, as four cubic curves.
func writeEllipse(body *bytes.Buffer, x0, y0, x1, y1 float64) {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	rx, ry := (x1-x0)/2, (y1-y0)/2
	kx, ky := rx*ellipseKappa, ry*ellipseKappa
	fmt.Fprintf(body, "%s %s m\n", number(cx+rx), number(cy))
	for _, c := range [][6]float64{
		{cx + rx, cy + ky, cx + kx, cy + ry, cx, cy + ry},
		{cx - kx, cy + ry, cx - rx, cy + ky, cx - rx, cy},
		{cx - rx, cy - ky, cx - kx, cy - ry, cx, cy - ry},
		{cx + kx, cy - ry, cx + rx, cy - ky, cx + rx, cy},
	} {
		fmt.Fprintf(body, "%s %s %s %s %s %s c\n",
			number(c[0]), number(c[1]), number(c[2]),
			number(c[3]), number(c[4]), number(c[5]))
	}
	fmt.Fprintln(body, "h")
}

// writeArrow draws Table 176's ClosedArrow at the line's second end: "a
// triangular closed arrowhead filled with the annotation's interior color, if
// any".
//
// ⛔ IT POINTS ALONG THE LINE, which means the direction has to be computed
// from the caller's own two ends rather than from the normalised box — a line
// drawn from right to left has its head on the left, and a box has forgotten
// which end was which.
func writeArrow(body *bytes.Buffer, sh Shape) {
	dx, dy := sh.X1-sh.X0, sh.Y1-sh.Y0
	length := math.Hypot(dx, dy)
	ux, uy := dx/length, dy/length
	// Back along the line from the tip, then out to either side.
	back := sh.width() * arrowLength
	side := sh.width() * arrowSpread
	bx, by := sh.X1-ux*back, sh.Y1-uy*back
	// The perpendicular, which is the unit vector turned a quarter turn.
	px, py := -uy, ux
	fmt.Fprintf(body, "%s %s m %s %s l %s %s l h %s\n",
		number(sh.X1), number(sh.Y1),
		number(bx+px*side), number(by+py*side),
		number(bx-px*side), number(by-py*side),
		paintOp(sh))
}
