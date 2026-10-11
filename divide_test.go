// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import "testing"

// ⛔ The SIZE of the pieces is the whole assertion. Divide and Poster make the
// same grid out of the same page and differ only in what the pieces measure,
// so a test that checked the count and the regions would pass on either and
// prove nothing about which one was called. TestDivideIsNotPoster below is the
// control for exactly that.
func TestDivideCutsAPageInTwoDownTheMiddle(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Divide(2, 1); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 2 {
		t.Fatalf("PageCount() = %d, want 2", got)
	}
	boxes, tiles := posterSheets(t, d)

	// The page is 100 by 200, so two pieces across are 50 by 200 each — half
	// the width, the full height, and the content at its own size.
	for i, mb := range boxes {
		if !near(mb[2]-mb[0], 50) || !near(mb[3]-mb[1], 200) {
			t.Errorf("piece %d measures %gx%g, want 50x200",
				i+1, mb[2]-mb[0], mb[3]-mb[1])
		}
	}
	// Left half first, right half second.
	want := []region{{0, 0, 50, 200}, {50, 0, 100, 200}}
	for i, tile := range tiles {
		if got := regionOf(tile.matrix, [2]float64{50, 200}); !nearRegion(got, want[i]) {
			t.Errorf("piece %d shows %v of the page, want %v", i+1, got, want[i])
		}
	}
}

// Across the other axis, because a spread is cut down the middle and a sheet
// of two labels is cut across it — and row zero is the TOP, which is the one
// thing a PDF's upward y makes easy to get backwards.
func TestDivideCutsAPageInTwoAcross(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Divide(1, 2); err != nil {
		t.Fatal(err)
	}
	boxes, tiles := posterSheets(t, d)
	for i, mb := range boxes {
		if !near(mb[2]-mb[0], 100) || !near(mb[3]-mb[1], 100) {
			t.Errorf("piece %d measures %gx%g, want 100x100",
				i+1, mb[2]-mb[0], mb[3]-mb[1])
		}
	}
	// ⛔ The TOP band first: the pieces come out in reading order, and the top
	// of the page is the HIGH y in a PDF.
	want := []region{{0, 100, 100, 200}, {0, 0, 100, 100}}
	for i, tile := range tiles {
		if got := regionOf(tile.matrix, [2]float64{100, 100}); !nearRegion(got, want[i]) {
			t.Errorf("piece %d shows %v of the page, want %v", i+1, got, want[i])
		}
	}
}

// ⛔ The control for both tests above. Poster makes the same NUMBER of pieces
// in the same reading order out of the same page; only the measurements differ.
// If this failed, every assertion about Divide could be satisfied by Poster.
func TestDivideIsNotPoster(t *testing.T) {
	sizes := func(f func(*Doc) error) [][2]float64 {
		d, err := Open(simple(t, 1))
		if err != nil {
			t.Fatal(err)
		}
		if err := f(d); err != nil {
			t.Fatal(err)
		}
		boxes, _ := posterSheets(t, d)
		out := make([][2]float64, 0, len(boxes))
		for _, mb := range boxes {
			out = append(out, [2]float64{mb[2] - mb[0], mb[3] - mb[1]})
		}
		return out
	}
	divided := sizes(func(d *Doc) error { return d.Divide(2, 2) })
	postered := sizes(func(d *Doc) error { return d.Poster(2, 2) })
	if len(divided) != len(postered) {
		t.Fatalf("different piece counts: %d and %d", len(divided), len(postered))
	}
	for i := range divided {
		if divided[i] == postered[i] {
			t.Errorf("piece %d is %v either way: Divide must make the pieces "+
				"SMALLER where Poster keeps them the page's size", i+1, divided[i])
		}
	}
	// And say what each one is, so a future change cannot make them differ in
	// the wrong direction and still pass.
	for i := range divided {
		if divided[i] != [2]float64{50, 100} {
			t.Errorf("Divide piece %d is %v, want 50x100", i+1, divided[i])
		}
		if postered[i] != [2]float64{100, 200} {
			t.Errorf("Poster piece %d is %v, want 100x200", i+1, postered[i])
		}
	}
}

// Dividing into one piece is the document: a no-op rather than a rebuild, so a
// caller who computed a grid of one does not pay for a copy.
func TestDivideIntoOneChangesNothing(t *testing.T) {
	d, err := Open(simple(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	before := written(t, d)
	if err := d.Divide(1, 1); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 3 {
		t.Errorf("PageCount() = %d, want 3", got)
	}
	after := written(t, d)
	if len(before) != len(after) {
		t.Fatalf("%d pages became %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("page %d changed", i+1)
		}
	}
}

// Every page is divided, not just the first: a four-page document cut in two
// is eight pieces.
func TestDivideSpreadsEveryPage(t *testing.T) {
	d, err := Open(simple(t, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Divide(2, 1); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 8 {
		t.Errorf("PageCount() = %d, want 8", got)
	}
}

func TestDivideRefusesWhatMakesNoSense(t *testing.T) {
	for _, c := range []struct{ across, down int }{{0, 1}, {1, 0}, {-2, 2}, {2, -2}} {
		d, err := Open(simple(t, 1))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Divide(c.across, c.down); err == nil {
			t.Errorf("Divide(%d, %d) was accepted", c.across, c.down)
		}
	}
	if err := New().Divide(2, 2); err == nil {
		t.Error("an empty document was divided")
	}
	// A page with no size cannot be cut into fractions of it, and saying so is
	// better than writing pieces of zero size that open as nothing.
	noSize := New()
	noSize.Blank(0, 0)
	if err := noSize.Divide(2, 2); err == nil {
		t.Error("a page with no size was divided")
	}
}
