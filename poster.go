// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"math"
)

// Poster spreads each page over across×down sheets, so that the sheets
// printed and taped edge to edge make one page the size of a wall. It is the
// inverse of [Doc.NUp], which puts several pages on one sheet.
//
// Every sheet is the size of the page it came from. A poster of an A4
// document therefore prints on A4, and each piece has the page's own
// proportions — pieces of some other shape do not tape back into the page they
// were cut from. Because the sheets keep their shape, a wall of them is only
// the page's shape again when across and down are equal; otherwise the page is
// enlarged as far as it will go inside the wall and centred, and the sheets at
// the edges carry the white that is left over.
//
// The sheets meet exactly: nothing is repeated from one to the next, and no
// margin is left for taping. An overlap would have to be a constant here,
// because Poster is told how many sheets to use and not how wide the printer's
// unprinted border is; too small a guess still leaves a seam, too large a one
// loses more of the poster to trimming than it saves, and neither could be
// turned off. Meeting exactly is also the only arrangement that is exactly
// undone: every part of the page lands on one sheet and one only, so the
// pieces butt together — taped from behind — with nothing to cut away and
// nothing printed twice.
//
// The sheets come out in reading order: left to right, top to bottom, the
// top-left one first, so a printed pile can be laid out on a table in the
// order it came off the printer.
func (d *Doc) Poster(across, down int) error {
	if across < 1 || down < 1 {
		return fmt.Errorf("ops: a poster %d sheets across and %d down makes no sense", across, down)
	}
	if len(d.pages) == 0 {
		return fmt.Errorf("ops: an empty document has nothing to spread")
	}
	if across == 1 && down == 1 {
		return nil
	}
	out := make([]Page, 0, len(d.pages)*across*down)
	for _, p := range d.pages {
		size := d.effectiveSize(p)
		if size[0] <= 0 || size[1] <= 0 {
			return fmt.Errorf("ops: a page with no size cannot be spread over sheets")
		}
		// The wall of sheets is across×down pages wide and tall, so the
		// largest the page can be drawn on it without changing shape is the
		// smaller of the two counts. What is left over is shared between the
		// two opposite edges.
		scale := math.Min(float64(across), float64(down))
		left := (float64(across) - scale) * size[0] / 2
		bottom := (float64(down) - scale) * size[1] / 2
		for row := 0; row < down; row++ {
			for col := 0; col < across; col++ {
				// A sheet shows the part of the wall it covers, so the page is
				// shifted by the sheet's own corner. Rows are counted from the
				// top, the way the sheets are read, while a PDF counts up from
				// the bottom: row zero is the topmost band of the wall.
				x := left - float64(col)*size[0]
				y := bottom - float64(down-1-row)*size[1]
				out = append(out, Page{
					size:  size,
					tiles: []tile{{from: p, matrix: [6]float64{scale, 0, 0, scale, x, y}}},
				})
			}
		}
	}
	d.pages = out
	return nil
}
