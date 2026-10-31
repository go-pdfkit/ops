// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	imageops "github.com/go-images/images/ops"
	"github.com/go-pdfkit/reader"
)

// scanLikePDF builds a one-page document of filled bars standing in for lines
// of text, already tilted by angleDeg counter-clockwise about the page centre.
//
// ⛔ THE TILT IS BUILT BY ROTATING EACH BAR'S OWN CORNERS, NOT BY THE MATRIX
// THE CODE UNDER TEST USES. Drawing the fixture with [rotateAboutCentre] would
// make the whole measurement circular on exactly the thing most likely to be
// wrong: if that matrix turned pages the wrong way, a fixture built with it
// would be tilted the wrong way too and every assertion would still pass.
// These four corners are computed here, from the rotation written out in full.
func scanLikePDF(t *testing.T, angleDeg float64) []byte {
	t.Helper()
	const (
		w, h   = 612.0, 792.0 // US Letter
		left   = 60.0
		right  = 552.0
		bar    = 8.0  // the height of one "line of text"
		pitch  = 20.0 // and the distance to the next
		top    = 700.0
		bottom = 90.0
	)
	s, c := math.Sincos(angleDeg * math.Pi / 180)
	cx, cy := w/2, h/2
	turn := func(x, y float64) (float64, float64) {
		dx, dy := x-cx, y-cy
		return cx + dx*c - dy*s, cy + dx*s + dy*c
	}

	var content bytes.Buffer
	content.WriteString("0 g\n") // black fill
	for y := bottom; y+bar <= top; y += pitch {
		// A ragged right edge, so the projection has the troughs a page of
		// text has rather than one unbroken block.
		end := right - math.Mod(y*7, 90)
		x0, y0 := turn(left, y)
		x1, y1 := turn(end, y)
		x2, y2 := turn(end, y+bar)
		x3, y3 := turn(left, y+bar)
		fmt.Fprintf(&content, "%.3f %.3f m %.3f %.3f l %.3f %.3f l %.3f %.3f l h f\n",
			x0, y0, x1, y1, x2, y2, x3, y3)
	}

	wr := reader.NewWriter("1.7")
	pagesRef := wr.Reserve()
	stream := wr.Add(&reader.Stream{Dict: reader.Dict{}, Raw: content.Bytes()})
	page := wr.Add(reader.Dict{
		"Type":     reader.Name("Page"),
		"Parent":   pagesRef,
		"Contents": stream,
	})
	wr.Put(pagesRef, reader.Dict{
		"Type":     reader.Name("Pages"),
		"Kids":     reader.Array{page},
		"Count":    reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Real(w), reader.Real(h)},
	})
	root := wr.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := wr.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSkewAnglesMeasuresEveryPage(t *testing.T) {
	for _, planted := range []float64{-6, -1.5, 0, 2.25, 5} {
		found, err := SkewAngles(scanLikePDF(t, planted), DeskewOptions{})
		if err != nil {
			t.Fatalf("planted %+.2f°: %v", planted, err)
		}
		if len(found) != 1 {
			t.Fatalf("planted %+.2f°: %d pages reported", planted, len(found))
		}
		if found[0].Err != nil {
			t.Fatalf("planted %+.2f°: %v", planted, found[0].Err)
		}
		if found[0].Page != 1 {
			t.Errorf("page numbered %d, want 1", found[0].Page)
		}
		// ⛔ THE SIGN IS ASSERTED, not just the magnitude. A page drawn tilted
		// counter-clockwise must measure POSITIVE: recovering −6° from a page
		// built at +6° passes any check written on the absolute error, and
		// then Deskew doubles the tilt instead of removing it.
		if d := found[0].Angle - planted; d > 0.2 || d < -0.2 {
			t.Errorf("planted %+.2f°, measured %+.2f° (off by %+.2f°)", planted, found[0].Angle, d)
		} else {
			t.Logf("planted %+.2f°, measured %+.2f°", planted, found[0].Angle)
		}
	}
}

// TestDeskewStraightensARealRender drives the whole pipeline — render, measure,
// correct, render again — and asks what is left on the page. The residual on
// the SECOND render is what says the correction matrix turns the page the right
// way; nothing shorter than that can, because a sign error reproduces itself
// through any single measurement.
func TestDeskewStraightensARealRender(t *testing.T) {
	for _, planted := range []float64{-4.75, 3.5} {
		d, err := Open(scanLikePDF(t, planted))
		if err != nil {
			t.Fatal(err)
		}
		found, err := d.Deskew(DeskewOptions{})
		if err != nil {
			t.Fatalf("planted %+.2f°: %v", planted, err)
		}
		if len(found) != 1 || !found[0].Corrected {
			t.Fatalf("planted %+.2f°: %+v", planted, found)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		after, err := SkewAngles(out, DeskewOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if after[0].Err != nil {
			t.Fatalf("re-measuring the corrected page: %v", after[0].Err)
		}
		if math.Abs(after[0].Angle) > 0.3 {
			t.Errorf("planted %+.2f°, corrected by %+.2f°, and %+.2f° of tilt is STILL on the page "+
				"(a doubled tilt here means the correction matrix turns the wrong way)",
				planted, -found[0].Angle, after[0].Angle)
		} else {
			t.Logf("planted %+.2f° → measured %+.2f° → residual %+.3f°", planted, found[0].Angle, after[0].Angle)
		}
	}
}

func TestDeskewLeavesALevelPageAlone(t *testing.T) {
	// A page drawn straight must come back byte for byte, not turned by a
	// hundredth of a degree and rewritten as a form XObject.
	before := scanLikePDF(t, 0)
	d, err := Open(before)
	if err != nil {
		t.Fatal(err)
	}
	found, err := d.Deskew(DeskewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if found[0].Corrected {
		t.Errorf("a page drawn level was turned by %+.3f°", found[0].Angle)
	}
	after, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "/XObject") {
		t.Error("an untouched page was rewritten through a form XObject")
	}
}

func TestDeskewHonoursTheThreshold(t *testing.T) {
	// BentoPDF's own setting, and the whole of its behaviour: "pages with
	// detected skew below this value are left untouched". A page at 1.5° is
	// corrected at the 0.5° default and left alone at a 2° threshold.
	pdf := scanLikePDF(t, 1.5)
	for _, tc := range []struct {
		threshold float64
		want      bool
	}{{0, true}, {0.5, true}, {1.0, true}, {2.0, false}} {
		d, err := Open(pdf)
		if err != nil {
			t.Fatal(err)
		}
		found, err := d.Deskew(DeskewOptions{Threshold: tc.threshold})
		if err != nil {
			t.Fatal(err)
		}
		if found[0].Corrected != tc.want {
			t.Errorf("threshold %.1f° on a page at %+.2f°: corrected=%v, want %v",
				tc.threshold, found[0].Angle, found[0].Corrected, tc.want)
		}
		// The angle is reported either way: "measured and left alone" is a
		// different answer from "not measured".
		if math.Abs(found[0].Angle-1.5) > 0.2 {
			t.Errorf("threshold %.1f°: angle reported as %+.2f°", tc.threshold, found[0].Angle)
		}
	}
}

func TestDeskewReportsABlankPageWithoutRefusingTheRest(t *testing.T) {
	// ⛔ One unmeasurable page must not sink the document. A blank separator
	// sheet in a stack of scans has no skew — there is nothing on it to be
	// tilted — and refusing the stack over it would be refusing to deskew
	// scans because one page was blank.
	tilted := scanLikePDF(t, 4)
	d, err := Open(tilted)
	if err != nil {
		t.Fatal(err)
	}
	d.Blank(612, 792)
	d.Reverse() // put the blank page first, so it is not the last word either
	found, err := d.Deskew(DeskewOptions{})
	if err != nil {
		t.Fatalf("one blank page refused the whole document: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("%d pages reported, want 2", len(found))
	}
	if found[0].Err == nil || !errors.Is(found[0].Err, imageops.ErrSkewBlank) {
		t.Errorf("the blank page reported %v, want ErrSkewBlank", found[0].Err)
	}
	if found[0].Corrected {
		t.Error("a page that could not be measured was corrected anyway")
	}
	if found[1].Err != nil || !found[1].Corrected {
		t.Errorf("the tilted page was not corrected: %+v", found[1])
	}
	t.Logf("page 1 %v; page 2 %+.2f° corrected", found[0].Err, found[1].Angle)
}

func TestDeskewAtEachDocumentedDPI(t *testing.T) {
	// BentoPDF offers 100, 150, 200 and 300. All four must find the same page.
	pdf := scanLikePDF(t, -3.25)
	for _, dpi := range []float64{100, 150, 200, 300} {
		found, err := SkewAngles(pdf, DeskewOptions{DPI: dpi})
		if err != nil {
			t.Fatal(err)
		}
		if found[0].Err != nil {
			t.Fatalf("%g DPI: %v", dpi, found[0].Err)
		}
		if math.Abs(found[0].Angle+3.25) > 0.2 {
			t.Errorf("%g DPI: measured %+.2f°, planted -3.25°", dpi, found[0].Angle)
		} else {
			t.Logf("%g DPI: %+.2f°", dpi, found[0].Angle)
		}
	}
}

func TestSkewAnglesRefusesWhatItCannotRead(t *testing.T) {
	if _, err := SkewAngles([]byte("not a pdf at all"), DeskewOptions{}); err == nil {
		t.Error("a file that is not a PDF was accepted")
	}
}

func TestSkewAnglesRefusesADocumentItCannotMeasureAtAll(t *testing.T) {
	// ⛔ ONE PAGE FAILING IS A REPORT; EVERY PAGE FAILING IS AN ERROR, or a
	// caller who checks the error — the normal way to drive this — is told the
	// measurement was done. Two causes, because they take different paths: a
	// render that cannot happen at all, and pages with nothing on them.
	t.Run("a DPI no renderer will accept", func(t *testing.T) {
		found, err := SkewAngles(scanLikePDF(t, 3), DeskewOptions{DPI: 100000})
		if err == nil {
			t.Fatal("a DPI of 100000 was accepted")
		}
		// The report still comes back with it, naming each page.
		if len(found) != 1 || found[0].Err == nil {
			t.Errorf("the per-page report should survive the error: %+v", found)
		}
		if !strings.Contains(err.Error(), "not one of the 1 pages") {
			t.Errorf("error was %v", err)
		}
	})
	t.Run("pages with nothing on them", func(t *testing.T) {
		d, err := Open(scanLikePDF(t, 0))
		if err != nil {
			t.Fatal(err)
		}
		d.Blank(612, 792)
		d.Blank(612, 792)
		d.Delete("1") // leave only the two blanks
		pdf, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SkewAngles(pdf, DeskewOptions{}); err == nil {
			t.Error("a document of nothing but blank pages was accepted")
		}
	})
}

func TestDeskewPassesOnWhatItCannotDo(t *testing.T) {
	// An empty document cannot be written, and Deskew has to say so rather
	// than report on nothing.
	var empty Doc
	if _, err := empty.Deskew(DeskewOptions{}); err == nil {
		t.Error("a document of no pages was accepted")
	}
	// And a document it can write but cannot measure fails too.
	var blanks Doc
	blanks.Blank(612, 792)
	if _, err := blanks.Deskew(DeskewOptions{}); err == nil {
		t.Error("a document of one blank page was accepted")
	}
}

func TestRotateAboutCentreKeepsTheCentre(t *testing.T) {
	// The one property of the matrix that can be checked in isolation; the
	// direction it turns is checked by TestDeskewStraightensARealRender,
	// through a render, because that is the only thing a sign error cannot
	// survive.
	size := [2]float64{612, 792}
	for _, a := range []float64{-7, 0, 3.5, 11} {
		m := rotateAboutCentre(a, size)
		cx, cy := size[0]/2, size[1]/2
		x := m[0]*cx + m[2]*cy + m[4]
		y := m[1]*cx + m[3]*cy + m[5]
		if math.Abs(x-cx) > 1e-9 || math.Abs(y-cy) > 1e-9 {
			t.Errorf("%+.2f°: the centre moved to (%.6f, %.6f), not (%.1f, %.1f)", a, x, y, cx, cy)
		}
	}
}
