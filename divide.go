// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import "fmt"

// Divide cuts each page into an across×down grid of smaller pages, each the
// size of the piece it holds.
//
// It is for a page that holds things which should have been separate: a
// two-page spread scanned as one wide sheet, two tickets printed side by side,
// a sheet of labels. Dividing a wide spread down the middle with
// Divide(2, 1) gives two pages of half the width, each of them the page it
// was before somebody put two on a sheet.
//
// ⛔ This is NOT [Doc.Poster], although the grid is the same. Poster keeps
// every sheet the size of the page it came from, which means it ENLARGES: the
// pieces printed and taped together make one page the size of a wall. Divide
// keeps the content at its own size and makes the pages smaller, so the pieces
// laid side by side are the page again at 1:1. Confusing the two gives a
// result that looks right in a thumbnail and is wrong by a factor of across
// and down — which is why the distinction is in both doc comments rather than
// left to whoever reads the grid arguments.
//
// The pieces come out in reading order: left to right, top to bottom, the
// top-left one first.
//
// The cuts meet exactly. Nothing is repeated from one piece to the next and no
// bleed is left, because Divide is told how many pieces to make and not what a
// trimmer needs; every part of the page lands on one piece and one only, so
// the pieces are exactly the page again.
func (d *Doc) Divide(across, down int) error {
	if across < 1 || down < 1 {
		return fmt.Errorf("ops: dividing a page %d across and %d down makes no sense", across, down)
	}
	if len(d.pages) == 0 {
		return fmt.Errorf("ops: an empty document has nothing to divide")
	}
	if across == 1 && down == 1 {
		return nil
	}
	out := make([]Page, 0, len(d.pages)*across*down)
	for _, p := range d.pages {
		size := d.effectiveSize(p)
		if size[0] <= 0 || size[1] <= 0 {
			return fmt.Errorf("ops: a page with no size cannot be divided")
		}
		w, h := size[0]/float64(across), size[1]/float64(down)
		for row := 0; row < down; row++ {
			for col := 0; col < across; col++ {
				// The piece shows the part of the page it covers, so the page
				// is shifted by the piece's own corner — at scale one, which
				// is the whole difference from Poster. Rows are counted from
				// the TOP, the way the pieces are read, while a PDF counts up
				// from the bottom: row zero is the topmost band.
				x := -float64(col) * w
				y := -float64(down-1-row) * h
				out = append(out, Page{
					size:  [2]float64{w, h},
					tiles: []tile{{from: p, matrix: [6]float64{1, 0, 0, 1, x, y}}},
				})
			}
		}
	}
	d.pages = out
	return nil
}
