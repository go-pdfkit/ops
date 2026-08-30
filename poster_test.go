// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"testing"
)

// region is the part of the source page one poster sheet shows, in the page's
// own coordinates: left, bottom, right, top, with the origin at the foot of
// the page the way a PDF counts.
type region [4]float64

// regionOf works out which part of the page a sheet shows, by asking where the
// sheet's own corners land once the tile's matrix is undone. A sheet is
// size[0] by size[1] with its corner at the origin, and the matrix scales and
// shifts the page onto it, so the inverse is a division and a subtraction.
func regionOf(m [6]float64, size [2]float64) region {
	return region{
		-m[4] / m[0],
		-m[5] / m[3],
		(size[0] - m[4]) / m[0],
		(size[1] - m[5]) / m[3],
	}
}

func nearRegion(a, b region) bool {
	for i := range a {
		if !near(a[i], b[i]) {
			return false
		}
	}
	return true
}

// posterSheets writes a document and reads back, for every sheet, its media
// box and the single tile it places.
func posterSheets(t *testing.T, d *Doc) ([][4]float64, []placed) {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([][4]float64, 0, d.PageCount())
	tiles := make([]placed, 0, d.PageCount())
	for i := 1; i <= d.PageCount(); i++ {
		mb, placed := sheetOf(t, out, i)
		if len(placed) != 1 {
			t.Fatalf("sheet %d places %d tiles, want one", i, len(placed))
		}
		boxes = append(boxes, mb)
		tiles = append(tiles, placed[0])
	}
	return boxes, tiles
}

// TestPosterSpreadsOverFourSheets pins the whole of a two-by-two poster: the
// sheets are the page's own size, the page is drawn at twice its size, and the
// four quarters come out in reading order.
func TestPosterSpreadsOverFourSheets(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Poster(2, 2); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 4 {
		t.Fatalf("PageCount() = %d, want 4", got)
	}
	boxes, tiles := posterSheets(t, d)

	// The source pages are 100 by 200. Every sheet is that size again: a piece
	// of a poster that is not the shape of the page will not tape back into
	// it.
	for i, mb := range boxes {
		if mb != [4]float64{0, 0, 100, 200} {
			t.Errorf("sheet %d is %v, want the page's own 0 0 100 200", i+1, mb)
		}
	}

	// The matrices, exactly. A PDF's origin is at the foot of the sheet, so
	// the sheet that shows the top of the page is the one that pushes the page
	// furthest down — which is why these two numbers are negative on the first
	// sheet and zero on the last.
	want := [][6]float64{
		{2, 0, 0, 2, 0, -200},    // top left
		{2, 0, 0, 2, -100, -200}, // top right
		{2, 0, 0, 2, 0, 0},       // bottom left
		{2, 0, 0, 2, -100, 0},    // bottom right
	}
	for i, tile := range tiles {
		if tile.content != "page 1" {
			t.Errorf("sheet %d carries %q", i+1, tile.content)
		}
		for k := range want[i] {
			if !near(tile.matrix[k], want[i][k]) {
				t.Errorf("sheet %d matrix = %v, want %v", i+1, tile.matrix, want[i])
				break
			}
		}
	}

	// The same claim said the other way round, in the page's coordinates: what
	// each sheet actually shows. Read as a table this is the poster laid out
	// on the floor, and it is upside down if the top and bottom rows swap.
	quarters := []region{
		{0, 100, 50, 200},   // top left
		{50, 100, 100, 200}, // top right
		{0, 0, 50, 100},     // bottom left
		{50, 0, 100, 100},   // bottom right
	}
	for i, tile := range tiles {
		if got := regionOf(tile.matrix, [2]float64{100, 200}); !nearRegion(got, quarters[i]) {
			t.Errorf("sheet %d shows %v of the page, want %v", i+1, got, quarters[i])
		}
	}
}

// TestPosterReadingOrderIsLeftToRightTopToBottom checks the order on a grid
// that is not square, where a transposed loop would still give the right
// number of sheets and the right set of regions.
func TestPosterReadingOrderIsLeftToRightTopToBottom(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Poster(2, 3); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 6 {
		t.Fatalf("PageCount() = %d, want 6", got)
	}
	_, tiles := posterSheets(t, d)

	// Two across and three down: the page grows twice, which is as far as it
	// goes across, and the half sheet left over vertically is shared between
	// the top and the bottom. So the page occupies the middle two thirds and a
	// bit of the sheets above and below it, and the top band shows only the
	// top half-sheet of white plus the top of the page.
	//
	// In the page's own coordinates each sheet is 50 wide and 100 tall, and
	// the six of them run from y=250 down to y=-50.
	want := []region{
		{0, 150, 50, 250}, {50, 150, 100, 250},
		{0, 50, 50, 150}, {50, 50, 100, 150},
		{0, -50, 50, 50}, {50, -50, 100, 50},
	}
	for i, tile := range tiles {
		if got := regionOf(tile.matrix, [2]float64{100, 200}); !nearRegion(got, want[i]) {
			t.Errorf("sheet %d shows %v of the page, want %v", i+1, got, want[i])
		}
	}
	// Said plainly, so the table above cannot be wrong in the same way as the
	// code: each sheet is left of or above the one after it.
	for i := 1; i < len(tiles); i++ {
		a := regionOf(tiles[i-1].matrix, [2]float64{100, 200})
		b := regionOf(tiles[i].matrix, [2]float64{100, 200})
		if a[3] < b[3] || (near(a[3], b[3]) && a[0] >= b[0]) {
			t.Errorf("sheet %d at %v does not read before sheet %d at %v", i, a, i+1, b)
		}
	}
}

// TestPosterKeepsTheSheetShapeWhenTheGridDoesNot checks that the page is
// enlarged only as far as it fits, so a wide grid does not stretch it.
func TestPosterKeepsTheSheetShapeWhenTheGridDoesNot(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Poster(3, 1); err != nil {
		t.Fatal(err)
	}
	boxes, tiles := posterSheets(t, d)
	if len(boxes) != 3 {
		t.Fatalf("got %d sheets", len(boxes))
	}
	for i, tile := range tiles {
		// One sheet tall means the page cannot grow at all, and the three
		// sheets across leave a whole sheet of white on either side of it.
		if !near(tile.matrix[0], 1) || !near(tile.matrix[3], 1) {
			t.Errorf("sheet %d scale = %v, want no enlargement", i+1, tile.matrix)
		}
		if !near(tile.matrix[0], tile.matrix[3]) {
			t.Errorf("sheet %d is stretched: %v", i+1, tile.matrix)
		}
	}
	want := []float64{100, 0, -100}
	for i, tile := range tiles {
		if !near(tile.matrix[4], want[i]) || !near(tile.matrix[5], 0) {
			t.Errorf("sheet %d at (%g,%g), want (%g,0)", i+1, tile.matrix[4], tile.matrix[5], want[i])
		}
	}
}

// TestPosterSpreadsEveryPage checks that a document of several pages gives
// each of them its own set of sheets, in order.
func TestPosterSpreadsEveryPage(t *testing.T) {
	d, err := Open(simple(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Poster(2, 1); err != nil {
		t.Fatal(err)
	}
	_, tiles := posterSheets(t, d)
	want := []string{"page 1", "page 1", "page 2", "page 2", "page 3", "page 3"}
	if len(tiles) != len(want) {
		t.Fatalf("got %d sheets, want %d", len(tiles), len(want))
	}
	for i, tile := range tiles {
		if tile.content != want[i] {
			t.Errorf("sheet %d carries %q, want %q", i+1, tile.content, want[i])
		}
	}
}

// TestPosterFollowsARotatedPage checks that a page turned on its side is
// spread over sheets of the shape a reader sees, not of the shape the file
// stored.
func TestPosterFollowsARotatedPage(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetRotation("1", 90); err != nil {
		t.Fatal(err)
	}
	if err := d.Poster(2, 2); err != nil {
		t.Fatal(err)
	}
	boxes, tiles := posterSheets(t, d)
	for i, mb := range boxes {
		if mb != [4]float64{0, 0, 200, 100} {
			t.Errorf("sheet %d is %v, want the turned page's 0 0 200 100", i+1, mb)
		}
	}
	// The rotation is baked into the tile's own form, so the placing matrix is
	// the same as it would be for an upright page of that shape.
	want := [][2]float64{{0, -100}, {-200, -100}, {0, 0}, {-200, 0}}
	for i, tile := range tiles {
		if !near(tile.matrix[4], want[i][0]) || !near(tile.matrix[5], want[i][1]) {
			t.Errorf("sheet %d at (%g,%g), want %v", i+1, tile.matrix[4], tile.matrix[5], want[i])
		}
	}
}

// TestPosterOfOneSheetChangesNothing checks that the do-nothing grid leaves
// the pages borrowed rather than wrapping each in a form.
func TestPosterOfOneSheetChangesNothing(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Poster(1, 1); err != nil {
		t.Fatal(err)
	}
	if got := written(t, d); !equal(got, pages(1, 2)) {
		t.Errorf("Poster(1, 1) gave %v", got)
	}
}

func TestPosterRefusesWhatMakesNoSense(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, grid := range [][2]int{{0, 2}, {2, 0}, {-1, -1}} {
		if err := d.Poster(grid[0], grid[1]); err == nil {
			t.Errorf("Poster(%d, %d) was allowed", grid[0], grid[1])
		}
	}
	if err := New().Poster(2, 2); err == nil {
		t.Error("a poster of an empty document was allowed")
	}
	sizeless := New()
	sizeless.Blank(0, 0)
	if err := sizeless.Poster(2, 2); err == nil {
		t.Error("a poster of a page with no size was allowed")
	}
}
