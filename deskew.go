// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"math"

	imageops "github.com/go-images/images/ops"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// Deskewing a scanned document: find how far each page is tilted and turn it
// back.
//
// # What this is parity with
//
// BentoPDF's deskew-pdf, read at docs/tools/deskew-pdf.md and
// src/js/logic/deskew-pdf-page.ts. Its contract is a skew threshold in degrees
// (0.1, 0.5, 1.0 or 2.0, default 0.5) and a processing DPI (100, 150, 200 or
// 300, default 150); it renders each page at that DPI, detects the angle, and
// — its own words — "if the detected skew exceeds the threshold, the page is
// rotated by the inverse angle to straighten it. Pages below the threshold are
// passed through unchanged." It then reports each page's angle and whether it
// was corrected. The same defaults and the same report are here.
//
// Two differences, both deliberate:
//
// Its detection is OpenCV's "dominant line angles", a Hough transform. This
// uses Baird's projection profile (see [imageops.SkewAngle]), which the survey
// that compares them — Mascaro, Cavalcanti et al., Pattern Recognition Letters
// 31 (2010) 1403–1411 — ranks first on documents with clear separation between
// text lines, which is what a page of text is.
//
// ⛔ AND THE CORRECTION IS NOT A RASTERISATION. The angle is measured on a
// render, but it is applied as a transformation of the page's own content, so
// text stays text and a vector drawing stays vector. BentoPDF lists its
// rasterize-pdf as a separate tool to run "to convert the deskewed output to a
// flat image-based PDF", so it is not rasterising here either — and a deskew
// that flattened the page would quietly undo the thing the user is usually
// deskewing FOR, which is OCR.

// DeskewOptions says how to measure and when to act. The zero value is
// BentoPDF's own default: half a degree, a hundred and fifty DPI.
type DeskewOptions struct {
	// Threshold is the tilt, in degrees, below which a page is left alone.
	// Zero means half a degree.
	//
	// A page under the threshold comes back untouched rather than turned by a
	// hundredth of a degree: every such turn resamples nothing but costs the
	// page a transformation it did not need, and a scan that is already level
	// should be byte-for-byte the page that came in.
	Threshold float64

	// DPI is how finely each page is drawn before its tilt is measured. Zero
	// means a hundred and fifty, which is BentoPDF's default and which its own
	// documentation calls "fast and accurate for most documents".
	//
	// ⛔ It is a measurement setting only. Nothing of the render reaches the
	// output — see the note above on not rasterising — so a higher DPI buys
	// accuracy and costs time, and never quality.
	DPI float64

	// MaxPixels is passed to the renderer and to the detector, so a media box
	// a file made up cannot be asked to fill memory. Zero leaves each to its
	// own default.
	MaxPixels int
}

// The defaults, which are BentoPDF's.
const (
	defaultDeskewThreshold = 0.5
	defaultDeskewDPI       = 150.0
)

func (o DeskewOptions) threshold() float64 {
	if o.Threshold > 0 {
		return o.Threshold
	}
	return defaultDeskewThreshold
}

func (o DeskewOptions) dpi() float64 {
	if o.DPI > 0 {
		return o.DPI
	}
	return defaultDeskewDPI
}

// A PageSkew is what was found on one page, numbered from one.
type PageSkew struct {
	Page int
	// Angle is the tilt found, in degrees, positive counter-clockwise. It is
	// reported whether or not the page was corrected, because "measured and
	// left alone" is a different answer from "not measured".
	Angle float64
	// Corrected says whether the page was turned.
	Corrected bool
	// Err is why this page could not be measured, and is nil otherwise. A page
	// with nothing on it has no skew, and one page that cannot be measured is
	// no reason to refuse the other three hundred.
	Err error
}

// SkewAngles measures the tilt of every page of a file, in order, numbered
// from one.
//
// ⛔ A page that cannot be measured is REPORTED, not fatal. A blank separator
// sheet in the middle of a stack of scans has no skew — there is nothing on it
// to be tilted — and refusing the whole document over it would be refusing to
// deskew a stack of scans because one page was blank. The reason lands in
// [PageSkew.Err], so a caller can say what happened per page.
func SkewAngles(pdf []byte, opt DeskewOptions) ([]PageSkew, error) {
	d, err := reader.Open(pdf)
	if err != nil {
		return nil, err
	}
	ropt := render.Options{DPI: opt.dpi(), MaxPixels: opt.MaxPixels}
	sopt := imageops.SkewOptions{MaxPixels: opt.MaxPixels}
	out := make([]PageSkew, 0, d.PageCount())
	for i := 1; i <= d.PageCount(); i++ {
		img, err := render.Page(d, i, ropt)
		if err != nil {
			out = append(out, PageSkew{Page: i, Err: fmt.Errorf("drawing page %d at %g DPI: %w", i, opt.dpi(), err)})
			continue
		}
		angle, err := imageops.SkewAngle(imageops.AsRGBA(img), sopt)
		if err != nil {
			out = append(out, PageSkew{Page: i, Err: fmt.Errorf("page %d: %w", i, err)})
			continue
		}
		out = append(out, PageSkew{Page: i, Angle: angle})
	}
	return out, nil
}

// Deskew measures every page and turns the ones tilted by more than the
// threshold, reporting what it found page by page.
//
// The page keeps its size, so the corners of the turned content move outside
// the media box and are clipped. That is what straightening a scan means — the
// paper is the same paper — and on a scan it costs nothing, because the text
// block sits well inside the margins. It is the same thing BentoPDF's does.
func (d *Doc) Deskew(opt DeskewOptions) ([]PageSkew, error) {
	pdf, err := d.Bytes()
	if err != nil {
		return nil, err
	}
	found, err := SkewAngles(pdf, opt)
	if err != nil {
		return nil, err
	}
	if len(found) != len(d.pages) {
		return nil, fmt.Errorf("ops: measured %d pages of a document of %d", len(found), len(d.pages))
	}
	threshold := opt.threshold()
	for i := range found {
		if found[i].Err != nil || math.Abs(found[i].Angle) < threshold {
			continue
		}
		p := d.pages[i]
		size := d.effectiveSize(p)
		if size[0] <= 0 || size[1] <= 0 {
			found[i].Err = fmt.Errorf("page %d has no size, so it cannot be turned", i+1)
			continue
		}
		d.pages[i] = Page{
			size:  size,
			tiles: []tile{{from: p, matrix: rotateAboutCentre(-found[i].Angle, size)}},
		}
		found[i].Corrected = true
	}
	return found, nil
}

// rotateAboutCentre is the content-stream matrix that turns a box of the given
// size by angleDeg about its own centre, counter-clockwise, in PDF user space.
//
// ⛔ THE SIGN IS NOT OBVIOUS AND IS NOT ASSERTED HERE. PDF user space counts y
// UPWARDS and a rendered image counts rows downwards, so one of the two
// conventions has to be reasoned through, and the reasoning is where a sign
// error hides: it survives every symmetric test, because recovering +5° from a
// page tilted −5° passes any check written on the absolute error. Deskew is
// therefore driven through the whole pipeline — render, measure, correct,
// render again — and the residual on the second render is what says the sign
// is right. See TestDeskewStraightensARealRender.
func rotateAboutCentre(angleDeg float64, size [2]float64) [6]float64 {
	s, c := math.Sincos(angleDeg * math.Pi / 180)
	cx, cy := size[0]/2, size[1]/2
	return [6]float64{c, s, -s, c, cx - cx*c + cy*s, cy - cx*s - cy*c}
}
