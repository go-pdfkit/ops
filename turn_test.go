// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// pipPagePDF is a page of the given size with a small black square at one
// place on it, named by the fraction of the page it sits at. One mark is all
// a turn needs: where it ends up says which way the page went round, and a
// second mark would only give a sign error somewhere to hide.
func pipPagePDF(t *testing.T, w, h, atX, atY float64) []byte {
	t.Helper()
	var c bytes.Buffer
	fmt.Fprintf(&c, "0 g %g %g 20 20 re f\n", atX*w-10, atY*h-10)
	wr := reader.NewWriter("1.7")
	pagesRef := wr.Reserve()
	content := wr.Add(&reader.Stream{Dict: reader.Dict{}, Raw: c.Bytes()})
	page := wr.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content})
	wr.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Real(w), reader.Real(h)},
	})
	root := wr.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := wr.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// whereTheInkIs renders the first page of a document and reports where the
// black mark sits, as a fraction of the drawing in each direction — across
// from the left, and DOWN from the top, which is how a reader looking at the
// page sees it.
func whereTheInkIs(t *testing.T, pdf []byte) (acrossFrac, downFrac float64) {
	t.Helper()
	src, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	img, err := render.Page(src, 1, render.Options{DPI: 72})
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	// The CENTRE OF MASS of the ink rather than its bounding box: a rotated
	// square has corners the renderer softens, and a box built from whichever
	// pixel the antialiasing happened to darken moves about by a pixel or two
	// from one angle to the next. The mean does not.
	var sumX, sumY, n float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.At(x, y)
			if c.R > 100 || c.G > 100 || c.B > 100 {
				continue
			}
			sumX, sumY, n = sumX+float64(x), sumY+float64(y), n+1
		}
	}
	if n == 0 {
		t.Fatal("the rendered page has no black mark on it at all")
	}
	return (sumX / n) / float64(b.Dx()), (sumY / n) / float64(b.Dy())
}

// ⛔⛔ WHICH WAY ROUND, MEASURED ON A RENDER. Every assertion written on how far
// a page turned survives a sign error — a page turned 30° the wrong way is as
// far from straight as one turned 30° the right way — so the only honest
// question is where the ink ENDED UP. The mark starts at the top of the page,
// in the middle: turned clockwise by ninety it must be at the right-hand edge,
// the way a clock's hand goes from twelve to three.
func TestTurningClockwisePutsTheMarkWhereAClockWould(t *testing.T) {
	// ⛔ THE FRACTIONS ARE OF THE SHEET THE PAGE ENDS UP ON, not of the one it
	// started on. At 45° a square page's sheet is √2 times as wide, so a mark
	// four tenths of the way out from the centre comes back two tenths of the
	// way out: 0.4/√2 points of travel over a sheet √2 times as long. Reading
	// those two tenths as 0.4/√2 is the mistake this comment exists to stop,
	// and it is the first thing that went wrong when this was written.
	const atFortyFive = 0.4 / math.Sqrt2 / math.Sqrt2 // = 0.2
	for _, tc := range []struct {
		by                   float64
		wantAcross, wantDown float64
		what                 string
	}{
		{0, 0.5, 0.1, "twelve o'clock, untouched"},
		{90, 0.9, 0.5, "three o'clock"},
		{180, 0.5, 0.9, "six o'clock"},
		{-90, 0.1, 0.5, "nine o'clock"},
		{45, 0.5 + atFortyFive, 0.5 - atFortyFive, "half past one"},
		{-45, 0.5 - atFortyFive, 0.5 - atFortyFive, "half past ten"},
	} {
		// A SQUARE page, so that the fractions below are the same question
		// whichever way it went round: on an oblong, a quarter turn changes
		// which side the fraction is of and the arithmetic stops being readable.
		d, err := Open(pipPagePDF(t, 400, 400, 0.5, 0.9))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Turn("all", tc.by); err != nil {
			t.Fatalf("%+g°: %v", tc.by, err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		across, down := whereTheInkIs(t, out)
		if math.Abs(across-tc.wantAcross) > 0.03 || math.Abs(down-tc.wantDown) > 0.03 {
			t.Errorf("turned %+g° (%s): the mark is %.2f across and %.2f down, want %.2f and %.2f "+
				"— a mark on the wrong side means the page turned the wrong way",
				tc.by, tc.what, across, down, tc.wantAcross, tc.wantDown)
		}
	}
}

// ⛔⛔ THE SEAM. A quarter turn is written as the page's own /Rotate and a
// degree either side of it goes through a matrix, so the two arms meet at
// exactly ninety — which is where the reference this is ported from turns one
// way on one side and the other way on the other. Nothing but a measurement
// across the seam can catch that, so this asks where the mark is at 89, 90 and
// 91 degrees and insists it moves in one direction.
func TestTurningAQuarterAgreesWithRotate(t *testing.T) {
	at := func(by float64) (float64, float64) {
		t.Helper()
		d, err := Open(pipPagePDF(t, 400, 400, 0.5, 0.9))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Turn("all", by); err != nil {
			t.Fatal(err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return whereTheInkIs(t, out)
	}
	// ⛔ TWO QUESTIONS, because neither alone crosses the seam. A mark a degree
	// short of three o'clock and a mark a degree past it are the same distance
	// ACROSS the page, so that coordinate cannot tell 89° from 91° — but it is
	// the one that catches a quarter-turn arm going the other way, which would
	// put the mark at nine o'clock. How far DOWN the page it is does separate
	// 89° from 91°, and it is what catches the matrix arm going the other way.
	acrossBefore, downBefore := at(89)
	acrossQuarter, downQuarter := at(90)
	acrossAfter, downAfter := at(91)
	if math.Abs(acrossQuarter-acrossBefore) > 0.02 || math.Abs(acrossQuarter-acrossAfter) > 0.02 {
		t.Errorf("the mark is %.3f across at 89°, %.3f at 90° and %.3f at 91°: the quarter-turn arm "+
			"and the matrix arm disagree about which way round a page goes",
			acrossBefore, acrossQuarter, acrossAfter)
	}
	if !(downBefore < downQuarter && downQuarter < downAfter) {
		t.Errorf("the mark is %.3f down at 89°, %.3f at 90° and %.3f at 91°: a page turned further "+
			"clockwise must carry a mark that started at the top further down",
			downBefore, downQuarter, downAfter)
	}
	// And the quarter turn really is the cheap arm: no transformation, no new
	// content stream, the page's own /Rotate and nothing else.
	d, err := Open(pipPagePDF(t, 400, 300, 0.5, 0.9))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Turn("all", 90); err != nil {
		t.Fatal(err)
	}
	if d.pages[0].tiles != nil {
		t.Error("a quarter turn put the page inside a form instead of setting /Rotate")
	}
	if d.pages[0].rotate != 90 {
		t.Errorf("/Rotate is %d after a quarter turn, want 90", d.pages[0].rotate)
	}
}

// ⛔ NOTHING IS CUT OFF, which is the whole difference from a deskew. The sheet
// grows to the bounding box of the turned page, so all four corners land inside
// it — and it is TIGHT, so each edge is touched. Swept over angles rather than
// tried at one, because a bounding box built from the wrong pair of terms is
// right at 0°, 90° and 45° and wrong everywhere between.
func TestTheSheetGrowsToHoldEveryCornerAndNoMore(t *testing.T) {
	size := [2]float64{400, 250}
	for by := -350.0; by <= 350; by += 7 {
		sheet, m := turnOntoSheet(by, size)
		var lo, hi [2]float64
		lo = [2]float64{math.Inf(1), math.Inf(1)}
		hi = [2]float64{math.Inf(-1), math.Inf(-1)}
		for _, corner := range [4][2]float64{{0, 0}, {size[0], 0}, {size[0], size[1]}, {0, size[1]}} {
			x := m[0]*corner[0] + m[2]*corner[1] + m[4]
			y := m[1]*corner[0] + m[3]*corner[1] + m[5]
			lo[0], lo[1] = math.Min(lo[0], x), math.Min(lo[1], y)
			hi[0], hi[1] = math.Max(hi[0], x), math.Max(hi[1], y)
		}
		const slack = 1e-9
		if lo[0] < -slack || lo[1] < -slack {
			t.Fatalf("turned %+g°: a corner is at (%.6f, %.6f), off the bottom-left of the sheet",
				by, lo[0], lo[1])
		}
		if hi[0] > sheet[0]+slack || hi[1] > sheet[1]+slack {
			t.Fatalf("turned %+g°: a corner is at (%.6f, %.6f) on a %g by %g sheet: it would be cut off",
				by, hi[0], hi[1], sheet[0], sheet[1])
		}
		if math.Abs(lo[0]) > 1e-9 || math.Abs(lo[1]) > 1e-9 ||
			math.Abs(hi[0]-sheet[0]) > 1e-9 || math.Abs(hi[1]-sheet[1]) > 1e-9 {
			t.Fatalf("turned %+g°: the content occupies (%.6f,%.6f)-(%.6f,%.6f) of a %g by %g sheet, "+
				"which is bigger than it needs to be", by, lo[0], lo[1], hi[0], hi[1], sheet[0], sheet[1])
		}
	}
}

// The sheet of a turned page is the one BentoPDF computes, which is the part of
// its logic worth porting exactly: w·|cos| + h·|sin| across, w·|sin| + h·|cos|
// down.
func TestTheTurnedSheetIsTheReferencesOwnBoundingBox(t *testing.T) {
	d, err := Open(pipPagePDF(t, 200, 100, 0.5, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Turn("all", 45); err != nil {
		t.Fatal(err)
	}
	want := (200 + 100) / math.Sqrt2
	got := d.pages[0].size
	if math.Abs(got[0]-want) > 1e-9 || math.Abs(got[1]-want) > 1e-9 {
		t.Errorf("a 200 by 100 page turned 45° sits on a %g by %g sheet, want %g by %g",
			got[0], got[1], want, want)
	}
}

// A turn adds to what the page already carries, as the reference's does: it
// reads the page's current rotation and turns by current + asked.
func TestTurningTwiceAddsUp(t *testing.T) {
	d, err := Open(pipPagePDF(t, 400, 400, 0.5, 0.9))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Turn("all", 90); err != nil {
		t.Fatal(err)
	}
	if err := d.Turn("all", 90); err != nil {
		t.Fatal(err)
	}
	if d.pages[0].rotate != 180 {
		t.Fatalf("two quarter turns left /Rotate at %d, want 180", d.pages[0].rotate)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, down := whereTheInkIs(t, out)
	if down < 0.8 {
		t.Errorf("after two quarter turns the mark is %.2f down the page; it started at the top "+
			"and two quarters put it at the bottom", down)
	}
	// And a turn by a matrix on top of a quarter turn keeps both: the sheet is
	// the turned page's bounding box, not the original page's.
	if err := d.Turn("all", 30); err != nil {
		t.Fatal(err)
	}
	if d.pages[0].tiles == nil {
		t.Fatal("a thirty-degree turn did not compose the page")
	}
	want := 400*math.Cos(30*math.Pi/180) + 400*math.Sin(30*math.Pi/180)
	if math.Abs(d.pages[0].size[0]-want) > 1e-9 {
		t.Errorf("the sheet is %g across, want %g", d.pages[0].size[0], want)
	}
}

// A page turned by a whole circle, or by nothing, is the page that came in: no
// transformation, no re-encoded content, the same bytes.
func TestAFullTurnChangesNothing(t *testing.T) {
	in := pipPagePDF(t, 400, 300, 0.5, 0.9)
	plain, err := Open(in)
	if err != nil {
		t.Fatal(err)
	}
	was, err := plain.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, by := range []float64{0, 360, -720} {
		d, err := Open(in)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Turn("all", by); err != nil {
			t.Fatalf("%+g°: %v", by, err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, was) {
			t.Errorf("turning by %+g° changed the file", by)
		}
	}
}

// Only the pages the range names are turned.
func TestTurningOnePageLeavesTheOthers(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Turn("2", 30); err != nil {
		t.Fatal(err)
	}
	if d.pages[0].tiles != nil {
		t.Error("page 1 was turned and nobody asked")
	}
	if d.pages[1].tiles == nil {
		t.Error("page 2 was not turned")
	}
}

func TestTurnRefusesWhatIsNotAnAngle(t *testing.T) {
	for _, by := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		d, err := Open(pipPagePDF(t, 400, 300, 0.5, 0.9))
		if err != nil {
			t.Fatal(err)
		}
		err = d.Turn("all", by)
		if err == nil {
			t.Fatalf("%v was accepted as an angle", by)
		}
		if !strings.Contains(err.Error(), "is not an angle") {
			t.Errorf("%v: %v", by, err)
		}
	}
}

func TestTurnRefusesARangeItCannotRead(t *testing.T) {
	d, err := Open(pipPagePDF(t, 400, 300, 0.5, 0.9))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Turn("fourteen", 30); err == nil {
		t.Fatal("a range of nonsense was accepted")
	}
}

// ⛔ An angle whose quarter-circles do not fit in an int goes through the
// matrix arm rather than through the int conversion, which would wrap and turn
// the page by something nobody asked for.
func TestAnAbsurdAngleDoesNotWrapAround(t *testing.T) {
	if _, ok := quarterTurns(90 * (math.MaxInt32 + 1024.0)); ok {
		t.Error("an angle past the reach of an int was offered as a count of quarter turns")
	}
	if q, ok := quarterTurns(-270); !ok || q != -270 {
		t.Errorf("quarterTurns(-270) = %d, %v", q, ok)
	}
	if _, ok := quarterTurns(90.5); ok {
		t.Error("90.5° was called a whole number of quarter turns")
	}
}
