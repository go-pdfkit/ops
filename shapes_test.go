package ops

import (
	"strings"
	"testing"
)

// withShape is the blank page with one shape on it.
func withShape(t *testing.T, sh Shape) []byte {
	t.Helper()
	d, err := Open(blankPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddShapes(sh); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ⛔⛔ EACH SHAPE IS VISIBLE AND INSIDE ITS OWN RECTANGLE. A /Square with a Rect
// and a border style is a perfectly correct annotation that go-pdfkit/render
// shows as NOTHING, because it draws an annotation only from its appearance
// stream — so the only question worth asking is about pixels.
//
// ⛔ And "inside its own rectangle" is the second half. 12.5.6.8 says the shape
// is "inscribed within the annotation rectangle", and a stroke straddles the
// path it follows: a border drawn on the box's own edge puts half its width
// outside the Rect, where readers clip it. The Rect is grown by half the line
// width for exactly that reason, and this checks that no ink escapes it.
func TestEachShapeIsVisibleInsideItsOwnRectangle(t *testing.T) {
	// The page is 200 by 300; a shape in the middle leaves a wide margin, so
	// ink outside the rectangle is ink in the margin and easy to count.
	for _, c := range []struct {
		name string
		kind ShapeKind
	}{
		{"a line", ShapeLine},
		{"a rectangle", ShapeRectangle},
		{"an ellipse", ShapeEllipse},
	} {
		t.Run(c.name, func(t *testing.T) {
			if n := inkedPixels(t, blankPage(t)); n != 0 {
				t.Fatalf("precondition: the blank page has %d inked pixel(s)", n)
			}
			const w = 4.0
			pdf := withShape(t, Shape{
				Page: 1, Kind: c.kind,
				X0: 60, Y0: 160, X1: 140, Y1: 240, Width: w,
			})
			if n := inkedPixels(t, pdf); n == 0 {
				t.Fatal("the shape drew nothing on the page")
			}
			// The box is x 60..140, y 160..240 on a 300-point page, so rows
			// 60..140 — and the Rect is half a line width larger all round.
			pad := int(w/2) + 1
			inside := inkedIn(t, pdf, 60-pad, 60-pad, 140+pad+1, 140+pad+1)
			if all := inkedPixels(t, pdf); all != inside {
				t.Errorf("%d pixel(s) were drawn and %d of them are inside the "+
					"rectangle: %d escaped it", all, inside, all-inside)
			}
		})
	}
}

// ⛔ The three shapes are told APART, by where they put their ink rather than
// by the subtype they claim. A rectangle inks its corners; an ellipse inscribed
// in the same box does not; a line between two corners inks neither the
// top-left nor the bottom-right of the box it spans. Without this the three
// could all be drawing rectangles and every test above would pass.
func TestTheThreeShapesPutTheirInkInDifferentPlaces(t *testing.T) {
	const w = 4.0
	box := func(kind ShapeKind) Shape {
		return Shape{Page: 1, Kind: kind, X0: 60, Y0: 160, X1: 140, Y1: 240, Width: w}
	}
	// A square of pixels at the box's top-left corner, in raster rows: the box
	// is y 160..240 on a 300-point page, so its top edge is row 60.
	corner := func(pdf []byte) int { return inkedIn(t, pdf, 60, 60, 68, 68) }

	rect := corner(withShape(t, box(ShapeRectangle)))
	if rect == 0 {
		t.Error("a rectangle left its own corner blank")
	}
	ellipse := corner(withShape(t, box(ShapeEllipse)))
	if ellipse != 0 {
		t.Errorf("an ellipse inked %d pixel(s) in the corner of its box", ellipse)
	}
	// A line from the bottom left to the top right crosses neither of the other
	// two corners.
	diagonal := withShape(t, Shape{
		Page: 1, Kind: ShapeLine, X0: 60, Y0: 160, X1: 140, Y1: 240, Width: w,
	})
	if n := corner(diagonal); n != 0 {
		t.Errorf("a line inked %d pixel(s) in the top-left corner of its span", n)
	}
	// And it did draw something, so the corner tests are measuring a shape and
	// not an empty page.
	if n := inkedPixels(t, diagonal); n == 0 {
		t.Error("the line drew nothing")
	}
}

// ⛔ A fill is the difference between an outline and a solid, and Table 177
// makes it OPTIONAL by the number of elements in IC: "0 No colour;
// transparent". So Fill is a pointer, and the two answers must differ on the
// page as well as in the dictionary.
func TestAFillShowsOnThePageAndInTheFile(t *testing.T) {
	outline := withShape(t, Shape{
		Page: 1, Kind: ShapeRectangle, X0: 60, Y0: 160, X1: 140, Y1: 240, Width: 2,
	})
	solid := withShape(t, Shape{
		Page: 1, Kind: ShapeRectangle, X0: 60, Y0: 160, X1: 140, Y1: 240, Width: 2,
		Fill: &[3]float64{0.2, 0.4, 0.9},
	})
	thin, thick := inkedPixels(t, outline), inkedPixels(t, solid)
	if thick <= thin {
		t.Errorf("an outlined rectangle inked %d pixel(s) and a filled one %d",
			thin, thick)
	}
	// ⛔ The middle of the box is the test: an outline leaves it blank.
	if n := inkedIn(t, outline, 90, 90, 110, 110); n != 0 {
		t.Errorf("an outlined rectangle inked %d pixel(s) in its middle", n)
	}
	if n := inkedIn(t, solid, 90, 90, 110, 110); n == 0 {
		t.Error("a filled rectangle left its middle blank")
	}
	// And the file says so, because a reader that regenerates the appearance
	// works from IC and from nothing else.
	if strings.Contains(string(outline), "/IC") {
		t.Error("a shape with no fill wrote an /IC entry")
	}
	if !strings.Contains(string(solid), "/IC") {
		t.Error("a filled shape wrote no /IC entry")
	}
}

// ⛔ A line carries what Table 175 requires, and its L array holds the CALLER'S
// own ends rather than the normalised box: a line drawn right to left is a line
// in that direction, which is where its arrowhead goes.
func TestALineCarriesItsEndsInTheOrderTheyWereGiven(t *testing.T) {
	pdf := withShape(t, Shape{
		Page: 1, Kind: ShapeLine, X0: 140, Y0: 240, X1: 60, Y1: 160,
		Author: "a reader", Note: "look along here",
	})
	for _, want := range []string{
		"/Subtype /Line",
		"/L [140 240 60 160]", // the ends as given, not sorted
		"/LE",
		"(a reader)",
		"(look along here)",
		"/AP",
		"/BS",
	} {
		if !strings.Contains(string(pdf), want) {
			t.Errorf("the written line does not carry %s", want)
		}
	}
}

// ⛔⛔ THE ARROWHEAD POINTS ALONG THE LINE, and the direction comes from the
// caller's two ends rather than from the box — a box has forgotten which end
// was which. Measured by where the extra ink lands: an arrow at the low end
// thickens that end, and an arrow at the high end thickens the other.
func TestAnArrowheadPointsAtTheEndTheLineRunsTo(t *testing.T) {
	plain := withShape(t, Shape{
		Page: 1, Kind: ShapeLine, X0: 60, Y0: 200, X1: 140, Y1: 200, Width: 2,
	})
	// Left to right: the head is at x 140, the right end.
	rightward := withShape(t, Shape{
		Page: 1, Kind: ShapeLine, X0: 60, Y0: 200, X1: 140, Y1: 200, Width: 2,
		Arrow: true, Fill: &[3]float64{0, 0, 0},
	})
	// Right to left: the same two points, the other way round, so the head is
	// at x 60.
	leftward := withShape(t, Shape{
		Page: 1, Kind: ShapeLine, X0: 140, Y0: 200, X1: 60, Y1: 200, Width: 2,
		Arrow: true, Fill: &[3]float64{0, 0, 0},
	})
	// y 200 on a 300-point page is row 100; the head is a few points tall.
	leftHalf := func(pdf []byte) int { return inkedIn(t, pdf, 55, 90, 90, 112) }
	rightHalf := func(pdf []byte) int { return inkedIn(t, pdf, 110, 90, 145, 112) }

	if rightHalf(rightward) <= rightHalf(plain) {
		t.Errorf("an arrow pointing right inked %d pixel(s) at the right end and "+
			"a plain line inked %d", rightHalf(rightward), rightHalf(plain))
	}
	if leftHalf(leftward) <= leftHalf(plain) {
		t.Errorf("an arrow pointing left inked %d pixel(s) at the left end and "+
			"a plain line inked %d", leftHalf(leftward), leftHalf(plain))
	}
	// ⛔ And the control: each arrow leaves the OTHER end as thin as a plain
	// line. Without this a head drawn at both ends would pass both tests above.
	if leftHalf(rightward) != leftHalf(plain) {
		t.Errorf("an arrow pointing right also thickened the left end: %d against %d",
			leftHalf(rightward), leftHalf(plain))
	}
	if rightHalf(leftward) != rightHalf(plain) {
		t.Errorf("an arrow pointing left also thickened the right end: %d against %d",
			rightHalf(leftward), rightHalf(plain))
	}
	// The file says which ending it asked for.
	if !strings.Contains(string(rightward), "/ClosedArrow") {
		t.Error("the arrow's /LE does not name ClosedArrow")
	}
	if !strings.Contains(string(plain), "/LE [/None /None]") {
		t.Error("a plain line's /LE is not [None None]")
	}
}

// ⛔ An ellipse is four cubic curves, because a PDF has no ellipse operator —
// and the constant that makes them an ellipse rather than something that looks
// like one is 4(sqrt(2)-1)/3. A rounded 0.552 shows at the quarter points of a
// large circle, so the value is computed rather than typed.
func TestAnEllipseIsDrawnAsFourCurves(t *testing.T) {
	pdf := withShape(t, Shape{
		Page: 1, Kind: ShapeEllipse, X0: 40, Y0: 100, X1: 160, Y1: 220,
	})
	if n := strings.Count(string(pdf), " c\n"); n != 4 {
		t.Errorf("the ellipse is drawn with %d curve(s)", n)
	}
	if ellipseKappa < 0.5522 || ellipseKappa > 0.5523 {
		t.Errorf("the curve constant is %v", ellipseKappa)
	}
	// A circle inscribed in a square touches the middle of each edge: the
	// topmost inked row must be the box's top edge, which is row 80 for a box
	// of y 100..220 on a 300-point page.
	img := renderOf(t, pdf)
	top := -1
	for y := 0; y < img.H && top < 0; y++ {
		for x := 0; x < img.W; x++ {
			i := (y*img.W + x) * 4
			if !blank(img.Pix[i], img.Pix[i+1], img.Pix[i+2]) {
				top = y
			}
		}
	}
	if top < 78 || top > 82 {
		t.Errorf("the ellipse's highest ink is row %d, and its box's top edge is row 80", top)
	}
}

func TestWhatAShapeRefuses(t *testing.T) {
	for _, c := range []struct {
		name  string
		shape Shape
		says  string
	}{
		{"a page the document has not",
			Shape{Page: 2, X1: 10, Y1: 10}, "the document has 1"},
		{"a kind there is no shape for",
			Shape{Page: 1, Kind: 9, X1: 10, Y1: 10}, "there are three"},
		{"a line from a point to itself",
			Shape{Page: 1, Kind: ShapeLine}, "a line from a point to itself"},
		{"a rectangle of no width",
			Shape{Page: 1, Kind: ShapeRectangle, Y1: 10}, "Square of 0 by 10"},
		{"an ellipse of no height",
			Shape{Page: 1, Kind: ShapeEllipse, X1: 10}, "Circle of 10 by 0"},
		{"a colour outside its range",
			Shape{Page: 1, Kind: ShapeLine, X1: 10, Y1: 10, R: 1.5}, "they run from 0 to 1"},
		{"a fill outside its range",
			Shape{Page: 1, Kind: ShapeLine, X1: 10, Y1: 10, Fill: &[3]float64{0, -2, 0}},
			"fill component of -2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Open(blankPage(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.AddShapes(c.shape); err == nil {
				t.Fatal("it was accepted")
			} else if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q", err)
			}
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if n := inkedPixels(t, out); n != 0 {
				t.Errorf("a refused shape drew %d pixel(s) anyway", n)
			}
		})
	}
}

// ⛔ A width of zero is "I did not choose one", not "draw nothing" — and a
// shape nobody can see is not what a caller who left the field alone asked for.
func TestAShapeWithNoWidthChosenIsStillDrawn(t *testing.T) {
	if got := (Shape{}).width(); got != 1 {
		t.Errorf("an unchosen width is %v", got)
	}
	if got := (Shape{Width: -3}).width(); got != 1 {
		t.Errorf("a negative width is %v", got)
	}
	pdf := withShape(t, Shape{
		Page: 1, Kind: ShapeRectangle, X0: 60, Y0: 160, X1: 140, Y1: 240,
	})
	if n := inkedPixels(t, pdf); n == 0 {
		t.Error("a shape with no width chosen drew nothing")
	}
}

// ⛔ The box is normalised for the shapes that have one, so a rectangle given
// its corners the other way round is the same rectangle. A line is NOT
// normalised, which is the point of the arrow test above.
func TestABoxIsTheSameWhicheverCornersItWasGivenBy(t *testing.T) {
	a := Shape{X0: 140, Y0: 240, X1: 60, Y1: 160}
	x0, y0, x1, y1 := a.box()
	if x0 != 60 || y0 != 160 || x1 != 140 || y1 != 240 {
		t.Errorf("the box came back %g %g %g %g", x0, y0, x1, y1)
	}
	forwards := withShape(t, Shape{Page: 1, Kind: ShapeRectangle,
		X0: 60, Y0: 160, X1: 140, Y1: 240, Width: 2})
	backwards := withShape(t, Shape{Page: 1, Kind: ShapeRectangle,
		X0: 140, Y0: 240, X1: 60, Y1: 160, Width: 2})
	if inkedPixels(t, forwards) != inkedPixels(t, backwards) {
		t.Errorf("the same rectangle inked %d pixel(s) one way and %d the other",
			inkedPixels(t, forwards), inkedPixels(t, backwards))
	}
}

// ⛔ A shape goes on the page it NAMES, as the notes and the free text do.
func TestAShapeGoesOnThePageItNames(t *testing.T) {
	d, err := Open(blankPage(t))
	if err != nil {
		t.Fatal(err)
	}
	d.Append(mustOpen(t, blankPage(t)))
	if err := d.AddShapes(Shape{
		Page: 2, Kind: ShapeRectangle, X0: 60, Y0: 160, X1: 140, Y1: 240, Width: 2,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if n := inkedPixels(t, out); n != 0 {
		t.Errorf("the FIRST page has %d inked pixel(s), and the shape names the second", n)
	}
}
