// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"math"
)

// Turning a page by an angle that is not a quarter of a circle.
//
// # What this is parity with
//
// BentoPDF's rotate-custom, read at docs/tools/rotate-custom.md and
// src/js/logic/rotate-custom-page.ts. Its contract is one angle a page, added
// to whatever the page already carries: "Rotate PDF pages by any arbitrary
// angle, not just 90-degree increments."
//
// Its geometry is ported exactly, and it is the geometry that matters: the
// sheet grows to the bounding box of the turned page —
//
//	W = w·|cos θ| + h·|sin θ|      H = w·|sin θ| + h·|cos θ|
//
// — and the page is placed so that its centre lands on the centre of that
// sheet. ⛔ NOTHING IS CUT OFF, which is the whole difference from [Doc.Deskew]:
// a deskew is a correction of a quarter of a degree on a scan whose text sits
// well inside the margins, so it keeps the paper it was given; a turn by
// thirty degrees would lose two corners of the page if the paper did not grow
// with it.
//
// ⛔ THE REFERENCE CONTRADICTS ITSELF ABOUT THE DIRECTION, AND THIS FOLLOWS ITS
// DOCUMENTATION. rotate-custom.md says "Positive values rotate clockwise". Its
// code has two arms: a total that is a multiple of ninety is written as the
// page's /Rotate, which ISO 32000-1 Table 30 defines as "the number of degrees
// by which the page shall be rotated CLOCKWISE when displayed"; everything else
// goes through pdf-lib's drawPage with `rotate: degrees(total)`, which
// concatenates [cos sin −sin cos], a COUNTER-clockwise turn in a space whose y
// counts upwards. So the two arms turn opposite ways, and the seam is at
// exactly 90°, where the tool switches from one to the other.
//
// Positive is clockwise here, on both arms. That is what its documentation
// promises, what the PDF specification means by a rotation, and what
// [Doc.Rotate] in this package has always done — and a library in which Turn(90)
// and Rotate(90) disagreed would be a worse trap than the one being ported.
// TestTurningAQuarterAgreesWithRotate is the gate.

// Turn turns the pages the range names by an arbitrary angle in degrees,
// clockwise, adding to whatever rotation they already carry. The sheet grows
// to hold the turned page, so no corner is cut off.
//
// An angle that is a multiple of ninety is written as the page's own /Rotate
// and nothing else happens: no transformation, no new content stream, and a
// page whose text was selectable stays selectable. That is both cheaper and
// more faithful than turning it by a matrix, and it is what the reference does
// too.
func (d *Doc) Turn(spec string, degrees float64) error {
	if math.IsNaN(degrees) || math.IsInf(degrees, 0) {
		return fmt.Errorf("ops: %g is not an angle to turn a page by", degrees)
	}
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	for _, n := range nums {
		if quarters, ok := quarterTurns(degrees); ok {
			d.pages[n-1].rotate = normaliseRotation(d.pages[n-1].rotate + quarters)
			continue
		}
		p := d.pages[n-1]
		sheet, m := turnOntoSheet(degrees, d.effectiveSize(p))
		d.pages[n-1] = Page{size: sheet, tiles: []tile{{from: p, matrix: m}}}
	}
	return nil
}

// quarterTurns reports the whole quarter-circles in an angle, and whether the
// angle is made of nothing else.
//
// ⛔ NOT math.Mod(degrees, 90) == 0 ON ITS OWN: that is true for an angle whose
// quarters do not fit in an int, and the caller is about to convert. Nothing a
// person types reaches 2³¹ degrees, but a file can hold anything and this is
// the kind of arithmetic that turns a document into a panic.
func quarterTurns(degrees float64) (int, bool) {
	turns := degrees / 90
	if turns != math.Trunc(turns) || math.Abs(turns) > math.MaxInt32 {
		return 0, false
	}
	return int(turns) * 90, true
}

// turnOntoSheet is the sheet a page of the given size needs once it is turned
// clockwise by degrees, and the matrix that puts the page on it.
//
// ⛔ THE SIGN IS REASONED THROUGH ONCE, HERE, AND MEASURED SOMEWHERE ELSE. A
// content-stream matrix [cos sin −sin cos] turns counter-clockwise, because PDF
// user space counts y upwards; so a CLOCKWISE turn by θ is that matrix built
// from −θ. Every test written on an absolute error survives a sign error —
// recovering +5° from a page tilted −5° looks the same as the right answer — so
// the gate renders the page and asks which corner the ink ended up in. See
// TestTurningClockwisePutsTheMarkWhereAClockWould.
func turnOntoSheet(degrees float64, size [2]float64) ([2]float64, [6]float64) {
	s, c := math.Sincos(-degrees * math.Pi / 180)
	w, h := size[0], size[1]
	sheet := [2]float64{
		w*math.Abs(c) + h*math.Abs(s),
		w*math.Abs(s) + h*math.Abs(c),
	}
	// The page's centre, turned about the origin, is moved to the centre of the
	// sheet. Every other point follows it, which is what makes the bounding box
	// above tight: the two corners that reach furthest in each direction land
	// exactly on the edges.
	return sheet, [6]float64{c, s, -s, c,
		sheet[0]/2 - (w/2*c - h/2*s),
		sheet[1]/2 - (w/2*s + h/2*c),
	}
}
