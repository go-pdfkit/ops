// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"testing"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// ladder builds a one-page A4 document with the given text stamped on it, or
// nothing at all.
func ladder(t *testing.T, text string, size float64) *Doc {
	t.Helper()
	d := New()
	d.Blank(595, 842)
	if text != "" {
		if err := d.Stamp("all", Stamp{Text: text, Font: Helvetica, Size: size}); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// ⛔ The gap this detector lives in, asserted rather than described. The
// default resolution was CHANGED because of these numbers: at 50 DPI an
// eight-point full stop covers exactly one pixel, which is the mark a speck of
// dust makes, so no threshold there can tell punctuation from dirt. If a
// renderer change moves either end of the gap, this fails instead of silently
// reclassifying somebody's pages.
func TestAFullStopIsMoreThanOnePixelAtTheDefaultResolution(t *testing.T) {
	blank := inkOf(t, ladder(t, "", 0))
	if blank != 0 {
		t.Errorf("a page with nothing on it inks %.3e, want exactly zero", blank)
	}
	stop := inkOf(t, ladder(t, ".", 8))
	w, h := pageSizeAt(t, defaultBlankDPI)
	onePixel := 1 / float64(w*h)
	if px := stop / onePixel; px < 3 {
		t.Errorf("an 8pt full stop covers %.0f pixel(s) at %g DPI; "+
			"below three it cannot be told from a speck of dust",
			px, defaultBlankDPI)
	}
	// And the floor of the gap: a speck must be BELOW a full stop by a margin
	// worth setting MaxInk from, or the knob documented in BlankOptions is a
	// lie.
	if stop < 3*onePixel {
		t.Errorf("a full stop (%.3e) leaves no room above one pixel (%.3e)",
			stop, onePixel)
	}
}

// With the zero value of BlankOptions — the strictest setting — a page holding
// a single full stop is NOT blank.
func TestAPageWithAFullStopIsNotBlank(t *testing.T) {
	d := New()
	d.Blank(595, 842)
	if err := d.Stamp("1", Stamp{Text: ".", Font: Helvetica, Size: 8}); err != nil {
		t.Fatal(err)
	}
	d.Blank(595, 842)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got, err := BlankPages(pdf, BlankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("BlankPages = %v, want [2]: page 1 holds a full stop", got)
	}
}

// ⛔ A page of solid colour is NOT blank, and this is the claim stated at the
// level it lives at: the test is distance from WHITE, not uniformity. A
// measure of variance would call a deliberate black separator sheet blank —
// uniform, featureless — and delete it.
//
// The first version of this test tried to make a solid page by stamping
// U+2588 FULL BLOCK at 72 point. Helvetica's encoding does not have that
// character, so NOTHING was drawn and the page really was blank: the test
// failed, and it was the FIXTURE that was wrong. A synthetic raster says the
// same thing without depending on what a font happens to contain.
func TestASolidColourIsInkNotPaper(t *testing.T) {
	solid := &raster.Image{W: 8, H: 8, Pix: make([]uint8, 4*8*8)}
	for i := 0; i+3 < len(solid.Pix); i += 4 {
		solid.Pix[i+3] = 255 // opaque black
	}
	if got := inkFraction(solid, defaultTolerance); got != 1 {
		t.Errorf("a page of solid black inks %v, want all of it", got)
	}
	// And the other uniform page, for the pair: solid WHITE is paper.
	paper := &raster.Image{W: 8, H: 8, Pix: make([]uint8, 4*8*8)}
	for i := range paper.Pix {
		paper.Pix[i] = 255
	}
	if got := inkFraction(paper, defaultTolerance); got != 0 {
		t.Errorf("a page of solid white inks %v, want none", got)
	}
}

// End to end over a real drawn page, so the claim above is not only true of a
// raster this test made itself: a page covered by a black picture is not blank.
func TestAPageCoveredByAPictureIsNotBlank(t *testing.T) {
	d := New()
	if err := d.Picture(blackPNG(t), 72); err != nil {
		t.Fatal(err)
	}
	d.Blank(595, 842)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got, err := BlankPages(pdf, BlankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("BlankPages = %v, want [2]: page 1 is a black picture", got)
	}
}

// The documented knob, measured: MaxInk at 1e-6 is about two pixels at the
// default resolution, so it tolerates a speck and still keeps a full stop.
func TestTheScanThresholdKeepsAFullStopAndForgivesASpeck(t *testing.T) {
	w, h := pageSizeAt(t, defaultBlankDPI)
	onePixel := 1 / float64(w*h)
	const scan = 1e-6
	if scan < 2*onePixel {
		t.Errorf("1e-6 is %.1f pixels, which does not forgive a speck", scan/onePixel)
	}
	stop := inkOf(t, ladder(t, ".", 8))
	if stop <= scan {
		t.Errorf("a full stop inks %.3e, which 1e-6 would call blank", stop)
	}
}

func TestDropBlankRemovesThemAndSaysWhich(t *testing.T) {
	d := New()
	for i, text := range []string{"one", "", "three", "", ""} {
		d.Blank(595, 842)
		if text != "" {
			if err := d.Stamp(itoa(i+1), Stamp{Text: text, Font: Helvetica, Size: 24}); err != nil {
				t.Fatal(err)
			}
		}
	}
	went, err := d.DropBlank(BlankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// ⛔ Numbered as they were BEFORE the removal: 2, 4 and 5 of five, not
	// 2, 3, 4 of what was left as each one went.
	if len(went) != 3 || went[0] != 2 || went[1] != 4 || went[2] != 5 {
		t.Errorf("DropBlank = %v, want [2 4 5]", went)
	}
	if got := d.PageCount(); got != 2 {
		t.Errorf("PageCount() = %d, want 2", got)
	}
}

func TestDropBlankWithNothingToDropChangesNothing(t *testing.T) {
	d := ladder(t, "words on the page", 24)
	went, err := d.DropBlank(BlankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if went != nil {
		t.Errorf("DropBlank = %v, want nothing", went)
	}
	if got := d.PageCount(); got != 1 {
		t.Errorf("PageCount() = %d, want 1", got)
	}
}

// ⛔ Refusing to empty the document. A file of no pages opens and has a size,
// so a caller handed one would be told nothing was wrong.
func TestDropBlankRefusesToEmptyTheDocument(t *testing.T) {
	d := New()
	d.Blank(595, 842)
	d.Blank(595, 842)
	_, err := d.DropBlank(BlankOptions{})
	if err == nil {
		t.Fatal("a document of nothing but blank pages was emptied")
	}
	if msg := err.Error(); !strings.Contains(msg, "2 pages") {
		t.Errorf("the refusal does not say how many there were: %q", msg)
	}
	if got := d.PageCount(); got != 2 {
		t.Errorf("the refusal still changed the document: %d pages", got)
	}
}

func TestBlankPagesRefusesWhatIsNotADocument(t *testing.T) {
	if _, err := BlankPages([]byte("not a PDF at all"), BlankOptions{}); err == nil {
		t.Error("rubbish was accepted")
	}
	// A media box the file made up cannot be asked to fill memory.
	d := ladder(t, "x", 12)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BlankPages(pdf, BlankOptions{MaxPixels: 4}); err == nil {
		t.Error("a page past MaxPixels was drawn")
	}
}

// inkFraction's own edges, which no document reaches: a nil image and one of
// no size are zero rather than a division by zero.
func TestInkFractionOfNothing(t *testing.T) {
	if got := inkFraction(nil, 12); got != 0 {
		t.Errorf("inkFraction(nil) = %v", got)
	}
	if got := inkFraction(&raster.Image{}, 12); got != 0 {
		t.Errorf("inkFraction(empty) = %v", got)
	}
	// Transparent black is ink, not paper: the alpha channel is counted.
	one := &raster.Image{Pix: []uint8{0, 0, 0, 0}, W: 1, H: 1}
	if got := inkFraction(one, 12); got != 1 {
		t.Errorf("a transparent pixel inks %v, want 1", got)
	}
}

// inkOf renders a document's first page at the default resolution and returns
// its ink fraction, which is the quantity every assertion above is about.
func inkOf(t *testing.T, d *Doc) float64 {
	t.Helper()
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	img, err := render.Page(src, 1, render.Options{DPI: defaultBlankDPI})
	if err != nil {
		t.Fatal(err)
	}
	return inkFraction(img, defaultTolerance)
}

// pageSizeAt says how many pixels an A4 page comes to at a resolution, so a
// test can talk in PIXELS rather than in fractions nobody can picture.
func pageSizeAt(t *testing.T, dpi float64) (int, int) {
	t.Helper()
	d := New()
	d.Blank(595, 842)
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	img, err := render.Page(src, 1, render.Options{DPI: dpi})
	if err != nil {
		t.Fatal(err)
	}
	return img.W, img.H
}

// itoa is strconv.Itoa under a shorter name, for the page ranges above.
func itoa(n int) string { return strconv.Itoa(n) }

// blackPNG is a small all-black opaque PNG, for a page that is covered in ink
// without depending on a font.
func blackPNG(t *testing.T) []byte {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			m.Set(x, y, color.RGBA{A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// An explicit DPI and an explicit Tolerance are honoured, and the assertions
// say what each one DOES rather than only that the field is read: a coarser
// drawing loses a full stop, and a wider tolerance calls a faint mark paper.
func TestTheResolutionAndTheToleranceAreHonoured(t *testing.T) {
	d := New()
	d.Blank(595, 842)
	if err := d.Stamp("1", Stamp{Text: ".", Font: Helvetica, Size: 8}); err != nil {
		t.Fatal(err)
	}
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// At the default resolution the full stop is four pixels, so with a
	// MaxInk of two pixels' worth the page is NOT blank.
	w, h := pageSizeAt(t, defaultBlankDPI)
	twoPixels := 2 / float64(w*h)
	got, err := BlankPages(pdf, BlankOptions{MaxInk: twoPixels})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("at %g DPI a full stop was called blank", defaultBlankDPI)
	}
	// ⛔ At 50 it is ONE pixel, which is what the default resolution was
	// raised to avoid: the same MaxInk now calls the page blank. This is the
	// measurement that chose the default, asserted.
	w50, h50 := pageSizeAt(t, 50)
	got, err = BlankPages(pdf, BlankOptions{DPI: 50, MaxInk: 2 / float64(w50*h50)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("at 50 DPI a full stop still survived a two-pixel threshold; "+
			"the reason for the %g DPI default no longer holds", defaultBlankDPI)
	}
	// A tolerance of 255 makes everything paper, which shows the field is read
	// and in which direction.
	got, err = BlankPages(pdf, BlankOptions{Tolerance: 255})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Error("a tolerance of 255 did not make the page paper")
	}
}

// ⛔ DropBlank's two error returns, both reachable and neither obvious.
//
// The first draft of this file had no test for the write, and I nearly removed
// the check as unreachable: Bytes() refuses an empty document, and DropBlank
// refuses one before it ever gets there. But Bytes() ALSO refuses a document
// whose form field has nowhere to be written, which is a non-empty document —
// so the check is live, and the third time this session I almost called
// reachable code dead.
func TestDropBlankSaysWhyItCouldNotLook(t *testing.T) {
	// A page past the pixel limit: the detector's own refusal comes back out.
	d := ladder(t, "x", 12)
	d.Blank(595, 842)
	if _, err := d.DropBlank(BlankOptions{MaxPixels: 4}); err == nil {
		t.Error("a page past MaxPixels was measured anyway")
	}
	// A document that cannot be written at all. This reaches DropBlank's write
	// through Bytes, which is deliberately the ONLY place that knows a
	// document with no pages cannot be written.
	if _, err := New().DropBlank(BlankOptions{}); err == nil {
		t.Error("an empty document was measured anyway")
	}
}

// ⛔ The Tolerance default, asserted rather than asserted ABOUT. A mutation
// setting defaultTolerance to zero survived the first version of this suite:
// every fixture rendered to exactly white, so no test could see the field at
// all and the doc comment's claim was unproven.
//
// Measured (see TestMeasureAFaintMark): a mark at 247/255 inks 2.2e-03 at
// tolerance zero and EXACTLY NOTHING at twelve. So this page is paper by
// default and ink if the tolerance is narrowed, which is the whole of what the
// field does and in which direction.
func TestAFaintMarkIsPaperByDefaultAndInkWithoutTheTolerance(t *testing.T) {
	d := New()
	d.Blank(595, 842)
	if err := d.Stamp("all", Stamp{
		Text: "faint", Font: Helvetica, Size: 48,
		Colour: [3]float64{0.97, 0.97, 0.97},
	}); err != nil {
		t.Fatal(err)
	}
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got, err := BlankPages(pdf, BlankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("a mark at 247/255 was called ink with the default tolerance; "+
			"the %d-level tolerance is not doing what it says", defaultTolerance)
	}
	// Narrow the tolerance to one level and the same mark is ink.
	got, err = BlankPages(pdf, BlankOptions{Tolerance: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Error("a mark at 247/255 is still paper at a tolerance of one level, " +
			"so the tolerance is inert")
	}
}

// ⛔ The alpha channel, isolated. A mutation dropping it from inkFraction
// survived the first version of this suite, because the only transparent pixel
// any test used was transparent BLACK — whose colour channels are ink anyway.
// White-and-transparent is the case the rule is about: a renderer asked for a
// transparent background would otherwise report blank paper as solid ink, and
// an opaque white page as the same thing.
func TestATransparentWhitePixelIsInkNotPaper(t *testing.T) {
	clear := &raster.Image{Pix: []uint8{255, 255, 255, 0}, W: 1, H: 1}
	if got := inkFraction(clear, defaultTolerance); got != 1 {
		t.Errorf("a transparent white pixel inks %v, want 1: "+
			"the alpha channel is not being counted", got)
	}
	opaque := &raster.Image{Pix: []uint8{255, 255, 255, 255}, W: 1, H: 1}
	if got := inkFraction(opaque, defaultTolerance); got != 0 {
		t.Errorf("an opaque white pixel inks %v, want 0", got)
	}
}
