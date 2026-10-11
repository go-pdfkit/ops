// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// BlankOptions say how little a page may hold and still count as blank.
//
// ⛔ The zero value is the STRICTEST setting, not a middle one: a page with a
// single inked pixel is kept. That is deliberate and it is the only safe
// default, because this is the input to deleting pages — a page wrongly called
// blank destroys content, while a blank page wrongly kept merely leaves a
// blank page.
type BlankOptions struct {
	// DPI is how finely the page is drawn before its pixels are counted. Zero
	// means a hundred and fifty.
	//
	// ⛔ It is not fifty, which is what this first used and what a thumbnail
	// would want. MEASURED on A4: at 50 DPI an eight-point full stop covers
	// exactly ONE pixel, which is the same mark a speck of dust makes, so no
	// threshold at that resolution can tell punctuation from dirt. At 150 it
	// covers four and a comma covers eight. See TestMeasureTheInkLadder.
	DPI float64

	// Tolerance is how far a channel may be from white and still count as
	// paper, out of 255. Zero means twelve.
	//
	// ⛔ Not zero-means-zero: nothing in a rendered page is exactly white.
	// Anti-aliasing, a JPEG background and a scan of real paper all land a few
	// levels below it, and a tolerance of zero calls every page inked.
	Tolerance uint8

	// MaxInk is the fraction of a page's pixels that may be ink and have the
	// page still count as blank. The zero value keeps any page with a mark on
	// it, which is what the zero value SHOULD mean here.
	//
	// For scans, where dust and the edge of the platen leave specks, the
	// figures to set it from are these, measured on A4 at the default 150 DPI:
	//
	//	one pixel            4.6e-07
	//	a full stop (8pt)    1.8e-06   (four pixels)
	//	a comma (8pt)        3.7e-06   (eight pixels)
	//	one word (12pt)      2.0e-04
	//
	// So 1e-06 tolerates a speck or two and still keeps a page holding a full
	// stop. The margin is a factor of four, and it is not offered as a default
	// because whether that margin holds depends on the scanner, which this
	// package cannot see.
	MaxInk float64

	// MaxPixels is passed to the renderer, so a media box a file made up
	// cannot be asked to fill memory.
	MaxPixels int

	// KeepAnnotations counts the notes, highlights and form fields a page
	// carries as ink. They are NOT counted by default: a page whose only mark
	// is a sticky note is blank paper somebody wrote on, and the note is not
	// part of what was printed.
	KeepAnnotations bool
}

// The measured defaults. See TestMeasureTheInkLadder for where they come from.
const (
	defaultBlankDPI  = 150.0
	defaultTolerance = 12
)

func (o BlankOptions) dpi() float64 {
	if o.DPI > 0 {
		return o.DPI
	}
	return defaultBlankDPI
}

func (o BlankOptions) tolerance() uint8 {
	if o.Tolerance > 0 {
		return o.Tolerance
	}
	return defaultTolerance
}

// inkFraction is the share of an image's pixels that are not paper.
//
// A pixel is paper when every one of its four channels is within tol of 255.
// ⛔ The ALPHA channel is included on purpose: a fully transparent pixel is
// black-with-nothing in this representation, so a renderer asked for a
// transparent background would otherwise report blank paper as solid ink.
func inkFraction(img *raster.Image, tol uint8) float64 {
	if img == nil || img.W <= 0 || img.H <= 0 {
		return 0
	}
	floor := 255 - tol
	ink := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] < floor || img.Pix[i+1] < floor ||
			img.Pix[i+2] < floor || img.Pix[i+3] < floor {
			ink++
		}
	}
	return float64(ink) / float64(img.W*img.H)
}

// BlankPages reports which pages of a file hold almost nothing, counting from
// one and in order.
//
// Each page is DRAWN and its pixels counted, because that is the only test
// that agrees with a reader. A page whose content stream is full of operators
// painting white, or drawing outside the media box, looks blank and is blank;
// a page carrying an image of nothing is blank too, and no amount of reading
// the content stream says so.
//
// ⛔ What it will not do is call a page of solid colour blank. The test is
// distance from WHITE, not uniformity: a deliberately black separator sheet
// carries information, and a measure of variance would delete it.
func BlankPages(pdf []byte, opt BlankOptions) ([]int, error) {
	d, err := reader.Open(pdf)
	if err != nil {
		return nil, err
	}
	ropt := render.Options{
		DPI:           opt.dpi(),
		MaxPixels:     opt.MaxPixels,
		NoAnnotations: !opt.KeepAnnotations,
	}
	tol := opt.tolerance()
	var blank []int
	for i := 1; i <= d.PageCount(); i++ {
		img, err := render.Page(d, i, ropt)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i, err)
		}
		if inkFraction(img, tol) <= opt.MaxInk {
			blank = append(blank, i)
		}
	}
	return blank, nil
}

// DropBlank removes the pages that hold almost nothing and reports which went,
// numbered as they were BEFORE the removal — because a caller who wants to say
// what happened needs the numbers the reader saw.
//
// ⛔ It refuses to empty the document. A file of no pages is still a file: it
// opens, it has a size, and a caller who handed over a stack of scans that
// turned out to be blank would be told nothing was wrong while being given
// something no reader can show. Every page blank is a result worth an error.
func (d *Doc) DropBlank(opt BlankOptions) ([]int, error) {
	// ⛔ No separate check for an empty document: Bytes refuses one, and that
	// is the one place that should know a document with no pages cannot be
	// written. A second copy of the rule here was a branch no test could
	// reach, and a rule stated twice is a rule that will disagree with itself.
	pdf, err := d.Bytes()
	if err != nil {
		return nil, err
	}
	blank, err := BlankPages(pdf, opt)
	if err != nil {
		return nil, err
	}
	if len(blank) == 0 {
		return nil, nil
	}
	if len(blank) == len(d.pages) {
		return nil, fmt.Errorf("ops: every one of the %d pages looks blank: "+
			"removing them all would leave a file no reader can show", len(d.pages))
	}
	drop := make(map[int]bool, len(blank))
	for _, n := range blank {
		drop[n] = true
	}
	keep := make([]Page, 0, len(d.pages)-len(blank))
	for i, p := range d.pages {
		if !drop[i+1] {
			keep = append(keep, p)
		}
	}
	d.pages = keep
	return blank, nil
}
