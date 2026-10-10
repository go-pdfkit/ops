// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import "fmt"

// Interleave takes pages from each document in turn — the first of this, the
// first of the next, and round again — until every one of them is used up.
//
// It is what a duplex scanner leaves behind. A stack fed through a
// single-sided feeder twice comes out as two files: the fronts in order, and
// the backs, which is the same document taken apart. Merging them end to end
// gives every front then every back; this gives the document.
//
// The backs are usually in reverse: the stack is turned over as a stack, so
// the last sheet's back is scanned first. Reverse that one before interleaving
// and the pages land in the right order.
//
// Documents of different lengths do not stop it. A shorter one simply runs
// out, and the rest of the longer one follows in order — which is what a
// scanner leaves when the last sheet is single-sided.
func (d *Doc) Interleave(others ...*Doc) error {
	all := append([]*Doc{d}, others...)
	total := 0
	for _, o := range all {
		if o == nil {
			return fmt.Errorf("ops: cannot interleave with nothing")
		}
		total += len(o.pages)
	}
	if total == 0 {
		return fmt.Errorf("ops: there are no pages to interleave")
	}
	longest := 0
	for _, o := range all {
		if len(o.pages) > longest {
			longest = len(o.pages)
		}
	}
	out := make([]Page, 0, total)
	for i := 0; i < longest; i++ {
		for _, o := range all {
			if i < len(o.pages) {
				out = append(out, o.pages[i])
			}
		}
	}
	d.pages = out
	for _, o := range others {
		if d.version < o.version {
			d.version = o.version
		}
		if d.info == nil && o.info != nil {
			d.info = o.info
		}
	}
	return nil
}

// OnePage puts every page on a single sheet, one below the other.
//
// A page is a unit of paper, not a unit of reading: a receipt, a chat log and
// a web page cut into A4 are one thing that a printer divided. This puts them
// back, so what is read scrolls rather than turns.
//
// The sheet is as wide as the widest page and as tall as all of them stacked.
// Pages narrower than the widest are centred, because a column of text that
// jumps from side to side is harder to read than one that does not.
func (d *Doc) OnePage() error {
	if len(d.pages) == 0 {
		return fmt.Errorf("ops: an empty document has nothing to stack")
	}
	if len(d.pages) == 1 {
		return nil
	}
	width, height := 0.0, 0.0
	sizes := make([][2]float64, len(d.pages))
	for i, p := range d.pages {
		sizes[i] = d.effectiveSize(p)
		if sizes[i][0] > width {
			width = sizes[i][0]
		}
		height += sizes[i][1]
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("ops: these pages have no size to stack")
	}
	// Laid out from the bottom up, because that is where a PDF's origin is and
	// the first page belongs at the top.
	tiles := make([]tile, 0, len(d.pages))
	y := height
	for i, p := range d.pages {
		y -= sizes[i][1]
		x := (width - sizes[i][0]) / 2
		tiles = append(tiles, tile{from: p, matrix: [6]float64{1, 0, 0, 1, x, y}})
	}
	d.pages = []Page{{tiles: tiles, size: [2]float64{width, height}}}
	return nil
}

// DuplexCollate puts a duplex stack back in reading order.
//
// An automatic feeder that cannot turn a sheet over leaves ONE file holding
// two blocks: every front, then every back. This cuts it at split and
// interleaves the two halves, so front1, back1, front2, back2 and on.
//
// A split of zero or less means the middle, rounded UP — ceil(n/2) — because
// an odd stack has the unpaired sheet at the END of the fronts, not the start
// of the backs. Thirteen pages split at seven, not six.
//
// ⛔ reverseBacks is not a convenience, it is the common case: the stack is
// turned over AS A STACK, so the last sheet's back is scanned first. Without
// it a thirty-sheet batch comes out with every back on the wrong front, which
// is a defect that reads as "the scan is fine, the order is odd" rather than
// as a reversal.
//
// Blocks of different sizes do not stop it: the pairs collate first and
// whatever is left over follows in order, which is what the last single-sided
// sheet of a batch leaves behind.
func (d *Doc) DuplexCollate(split int, reverseBacks bool) error {
	n := len(d.pages)
	if n == 0 {
		return fmt.Errorf("ops: an empty document has nothing to collate")
	}
	if split <= 0 {
		split = (n + 1) / 2
	}
	if split > n {
		return fmt.Errorf("ops: cannot split at %d: there are %d pages", split, n)
	}
	fronts, backs := d.pages[:split], d.pages[split:]
	if reverseBacks {
		// ⛔ A copy. Reversing in place would reorder d.pages underneath
		// fronts, which shares the same array.
		rev := make([]Page, len(backs))
		for i, p := range backs {
			rev[len(backs)-1-i] = p
		}
		backs = rev
	}
	out := make([]Page, 0, n)
	for i := 0; i < len(fronts) || i < len(backs); i++ {
		if i < len(fronts) {
			out = append(out, fronts[i])
		}
		if i < len(backs) {
			out = append(out, backs[i])
		}
	}
	d.pages = out
	return nil
}
