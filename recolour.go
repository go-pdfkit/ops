// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-pdfkit/reader"
)

// RecolourText paints the text of the pages a range names in one colour,
// leaving everything else on the page as it was.
//
// The colour is red, green and blue, each from zero to one.
//
// ⛔ It reaches the text in each PAGE'S OWN content stream. Text drawn through
// a Form XObject is NOT recoloured, and the reason is not that it would be
// hard: an XObject is SHARED, so recolouring one would change pages the caller
// did not name. [Doc.RecolourText] therefore counts what it could not reach
// and [Doc.UnreachedText] reports it — because a tool that silently did
// nothing to a document whose text is all in XObjects would be worse than one
// that refused.
func (d *Doc) RecolourText(spec string, r, g, b float64) error {
	for _, v := range []float64{r, g, b} {
		if v < 0 || v > 1 {
			return fmt.Errorf("ops: a colour component is from 0 to 1, not %v", v)
		}
	}
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	if len(nums) == 0 {
		return fmt.Errorf("ops: %q names no pages to recolour", spec)
	}
	colour := [3]float64{r, g, b}
	for _, n := range nums {
		p := &d.pages[n-1]
		// ⛔ A page this package MADE rather than borrowed has no text of its
		// own to recolour: its content is the tiles and marks this document
		// composed, and a stamp's colour is the stamp's own business. Saying
		// so is better than appearing to work.
		if p.blank || p.tiles != nil || p.picture != nil {
			continue
		}
		p.recolour = &colour
	}
	return nil
}

// UnreachedText reports how many text-showing operations the last write left
// in a colour of their own because they are inside a Form XObject, which is
// shared and so cannot be recoloured for one page without changing others.
//
// It is zero until the document has been written, because until then there is
// nothing to have reached.
func (d *Doc) UnreachedText() int { return d.unreached }

// colourOps are the operators that set a colour. Every one of them is BLANKED
// inside a text object, because a colour set there would override the one
// being asked for — the first draft only set a colour before each BT, and any
// page whose text set its own colour came out unchanged.
var colourOps = map[string]bool{
	// Non-stroking, then stroking.
	"g": true, "rg": true, "k": true, "cs": true, "sc": true, "scn": true,
	"G": true, "RG": true, "K": true, "CS": true, "SC": true, "SCN": true,
}

// recolourContent rewrites a content stream so that every text object paints
// in one colour, and says how many text-showing operations it could not reach.
//
// ⛔ It SPLICES rather than re-serialises. Writing the operations back out
// would mean formatting every operand again — including an inline image's
// still-encoded bytes, where a formatter that is merely lossless-for-objects
// can change what the image means. Splicing keeps every byte it does not
// deliberately change, which is why [reader.Operation] carries its range.
//
// The edits are collected and applied in one pass, so that an insertion does
// not move the offsets of the ones after it.
func recolourContent(data []byte, colour [3]float64) (out []byte, unreached int) {
	type edit struct {
		at, end int
		with    string
	}
	var edits []edit
	set := colourText(colour)

	depth := 0
	scan := reader.NewContentScanner(data)
	for {
		op, ok := scan.Next()
		if !ok {
			break
		}
		switch {
		case op.Operator == "BT":
			depth++
			if depth == 1 {
				// q and the colour go BEFORE the BT; the Q after the ET. A
				// text object does not save the graphics state itself, so a
				// colour set inside one would leak into whatever is drawn
				// after it — which is how a recolouring turns a company's
				// logo the same shade as its paragraphs.
				edits = append(edits, edit{at: op.Start, end: op.Start, with: "q " + set + " "})
			}
		case op.Operator == "ET":
			if depth == 1 {
				edits = append(edits, edit{at: op.End, end: op.End, with: " Q"})
			}
			if depth > 0 {
				depth--
			}
		case depth > 0 && colourOps[op.Operator]:
			// ⛔ BLANKED, not removed: the replacement is spaces of the same
			// length, so every offset after it is still the offset the scan
			// reported. Deleting the bytes instead would be correct only if
			// the edits were applied back to front, and a rule that is
			// correct only in one order is a rule somebody will reorder.
			edits = append(edits, edit{at: op.Start, end: op.End,
				with: strings.Repeat(" ", op.End-op.Start)})
		case depth > 0 && op.Operator == "Do":
			// A Form XObject drawn inside a text object, or an image. Either
			// way its own content is not this stream's to change.
			unreached++
		case depth == 0 && op.Operator == "Do":
			// ⛔ Counted too, and this is the honest half of the limitation: a
			// page whose text is ALL inside an XObject would otherwise come
			// back unchanged with nothing said. Not every XObject holds text,
			// so this counts what MIGHT not have been reached rather than what
			// certainly was not — and over-reporting a limit is the safe
			// direction for a caller deciding whether to trust the result.
			unreached++
		}
	}
	if len(edits) == 0 {
		return data, unreached
	}
	var sb strings.Builder
	sb.Grow(len(data) + len(edits)*len(set) + 16)
	// ⛔ No guard against overlapping edits, and that is a removal rather than
	// an omission. The edits come from a scan whose ranges go/reader asserts
	// are ORDERED AND DISJOINT, so e.at is never behind the last end — which
	// made the guard dead code a coverage gap found. The invariant is named
	// where it is established rather than re-checked where it is relied on.
	at := 0
	for _, e := range edits {
		sb.Write(data[at:e.at])
		sb.WriteString(e.with)
		at = e.end
	}
	sb.Write(data[at:])
	return []byte(sb.String()), unreached
}

// colourText is the operators that set both the filling and the stroking
// colour, so that text comes out in the colour asked for whatever its render
// mode is: mode 0 fills, 1 strokes, 2 does both.
func colourText(c [3]float64) string {
	n := func(v float64) string {
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	rgb := n(c[0]) + " " + n(c[1]) + " " + n(c[2])
	return rgb + " rg " + rgb + " RG"
}
