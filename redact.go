// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/reader"
)

// A Redaction names a region of a page whose content is to be REMOVED, and the
// colour to paint in its place.
//
// ⛔⛔ REMOVED, NOT COVERED, and that is the entire point. ISO 32000-1:2008
// §12.5.6.23 on applying a redaction:
//
//	Conforming readers that support redaction annotations shall provide a
//	mechanism for applying content removal, and they shall remove all traces
//	of the specified content. If a portion of an image is contained in a
//	redaction region, that portion of the image data shall be destroyed;
//	clipping or image masks shall not be used to hide that data.
//
// A black rectangle drawn over a paragraph is the standard data leak: the text
// is still in the content stream, so anyone's copy and paste — or `pdfops text`
// — reads it straight back out from under the box. The test that matters, and
// the one a drawn rectangle fails, is that the redacted string does not appear
// anywhere in the bytes that come out.
//
// The region is in the page's own coordinates, up from the bottom left corner,
// in points — the same space [Quad] uses for a markup annotation.
type Redaction struct {
	// Page is which page, counting from one.
	Page int
	// Quads is where on it. A quadrilateral per piece, as a markup annotation
	// has: see [QuadOf] for the upright rectangle a line of text is.
	Quads []Quad
	// R, G and B are the colour painted over the region once the content is
	// gone, each from zero to one. The spec leaves the region transparent when
	// a redaction names no colour; this paints it, because an area that was
	// emptied and not marked is one a reader cannot tell from one that was
	// always blank.
	R, G, B float64
}

// Redact removes the text inside each region and paints the region over.
//
// ⛔ IT REFUSES RATHER THAN HALF-REDACTS. A redaction that removed some of
// what it was asked to remove, and said nothing about the rest, would be worse
// than no redaction at all: the caller would believe the document safe to send.
// So this returns an error, naming the page and the reason, when a region holds
// anything it cannot take out:
//
//   - text inside a Form XObject. Its bytes are not this page's, and an
//     XObject is SHARED — editing it to clean one page would change every page
//     that draws it. [Doc.RecolourText] stops at the same wall, and counts what
//     it could not reach for the same reason.
//   - an image. The spec is explicit that a mask or a clip will not do and the
//     pixels themselves must be destroyed; until this can re-encode an image
//     with those pixels overwritten, an image in the region is a refusal.
//   - an annotation. Its /Contents can hold the very words being redacted.
//   - a text-showing operator only PART of whose text is inside the region.
//     Blanking it would take away text the caller did not ask about, and
//     rewriting it cannot be done by re-emitting the surviving words: the
//     bytes in the stream are font codes, and Unicode does not round-trip
//     back to them through a CID font's map. Keeping the surviving pieces'
//     original bytes and replacing the removed ones with an equivalent kern is
//     the way, and it is not in this release.
//
// What it does remove it removes from the bytes: the operator is overwritten
// with spaces, so the string is gone from the content stream rather than
// hidden.
func (d *Doc) Redact(rs ...Redaction) error {
	for _, r := range rs {
		if r.Page < 1 || r.Page > len(d.pages) {
			return fmt.Errorf("ops: there is no page %d to redact", r.Page)
		}
		if len(r.Quads) == 0 {
			return fmt.Errorf("ops: the redaction of page %d names no region", r.Page)
		}
		for _, v := range []float64{r.R, r.G, r.B} {
			if v < 0 || v > 1 {
				return fmt.Errorf("ops: a colour component is from 0 to 1, not %v", v)
			}
		}
		p := &d.pages[r.Page-1]
		if p.src == nil {
			return fmt.Errorf("ops: page %d was made rather than read, so it carries no text to redact", r.Page)
		}
		// ⛔ The same refusal ReplaceText makes, from the other side. A rule
		// enforced at only one of its two doors is a rule the caller meets or
		// misses depending on the order they happened to call things in.
		if len(p.textEdits) > 0 {
			return fmt.Errorf("ops: the text of page %d is already being "+
				"replaced, and one page cannot have its text replaced and be "+
				"redacted in the same write: this scans the ORIGINAL stream for "+
				"the operations to blank, and a replacement changes that "+
				"stream's length. Write the document, open it again, and redact "+
				"that", r.Page)
		}
		if err := d.checkRedactable(p, r); err != nil {
			return err
		}
		p.redactions = append(p.redactions, r)
	}
	return nil
}

// checkRedactable refuses a region this release cannot empty completely.
//
// ⛔ It runs when Redact is CALLED, not when the document is written. A caller
// that is told at once can choose another region or another tool; one told at
// Save has already been promised a redacted document by everything in between.
func (d *Doc) checkRedactable(p *Page, r Redaction) error {
	runs, err := extractRuns(p.src, p.number)
	if err != nil {
		return fmt.Errorf("ops: page %d could not be read to redact it: %w", r.Page, err)
	}
	if err := unremovableText(r.Page, runs, r.Quads); err != nil {
		return err
	}
	images, err := extractImages(p.src, p.number)
	if err != nil {
		return fmt.Errorf("ops: the images of page %d could not be read: %w", r.Page, err)
	}
	for _, im := range images {
		// ⛔ DrawnWidth and DrawnHeight, not Width and Height: those two are the
		// picture's own SAMPLES. Comparing a sample count with a region in
		// points would weigh a 2048-pixel thumbnail against a 72-point box and
		// refuse every page that carried a picture at all.
		if overlaps(im.X, im.Y, im.X+im.DrawnWidth, im.Y+im.DrawnHeight, r.Quads) {
			return fmt.Errorf("ops: page %d places an image across the region, and a redaction "+
				"must destroy the pixels rather than cover them, which this cannot yet do",
				r.Page)
		}
	}
	// ⛔⛔ THE ANNOTATIONS THIS DOCUMENT HAS ADDED COUNT TOO, and reading only
	// the source page missed them. Held until the write, because a widget and a
	// markup annotation both name the page OBJECT they sit on, they are not in
	// p.src at all — so a caller that marked a passage and then redacted it got
	// the text removed and the annotation kept, with the marked-up words still
	// in the file as that annotation's own note. The workbench walked straight
	// into it: highlight a line, then decide to redact it.
	for _, m := range d.marks {
		if m.Page != p.number {
			continue
		}
		for _, q := range m.Quads {
			x0, y0, x1, y1 := q.bounds()
			if overlaps(x0, y0, x1, y1, r.Quads) {
				return fmt.Errorf("ops: page %d carries a mark this document has added across "+
					"the region, and its own note can hold the words being redacted: drop "+
					"the mark first", r.Page)
			}
		}
	}
	// ⛔ A field's places are its WIDGETS, each with its own page: one field can
	// show on several pages, so the field is not the thing with a position.
	for _, f := range d.addFields {
		for _, wdg := range f.Widgets {
			if wdg.Page != p.number {
				continue
			}
			x0, y0 := min(wdg.Rect[0], wdg.Rect[2]), min(wdg.Rect[1], wdg.Rect[3])
			x1, y1 := max(wdg.Rect[0], wdg.Rect[2]), max(wdg.Rect[1], wdg.Rect[3])
			if overlaps(x0, y0, x1, y1, r.Quads) {
				return fmt.Errorf("ops: page %d carries a form field this document has added "+
					"across the region, and a field holds a value of its own: drop the "+
					"field first", r.Page)
			}
		}
	}
	page, _ := p.src.Page(p.number)
	if annots, ok := resolveArray(p.src, page.Get("Annots")); ok {
		for _, a := range annots {
			dict, ok := resolveDict(p.src, a)
			if !ok {
				continue
			}
			box := annotRect(p.src, dict.Get("Rect"))
			if box == [4]float64{} {
				continue
			}
			if overlaps(box[0], box[1], box[2], box[3], r.Quads) {
				return fmt.Errorf("ops: page %d carries an annotation across the region, and its "+
					"own contents can hold the words being redacted", r.Page)
			}
		}
	}
	return nil
}

// The two readings a refusal is decided from, replaceable so that a test can
// see what this does when a page cannot be read at all. The same shape as
// markup.go's `var now = time.Now`.
var (
	extractRuns   = extract.Runs
	extractImages = extract.Images
	// pageContent is wrapped rather than used directly for the same reason:
	// the difference between a recolouring and a redaction of a page that
	// cannot be read is the difference between a lost colour and a document
	// that must not be written, and a test has to be able to ask for both.
	pageContent = func(src *reader.Document, page int) ([]byte, error) {
		return src.PageContent(page)
	}
)

// unremovableText is the refusals that are decided from the runs alone, so
// that each can be put to it directly: a page that happens to hold a form, an
// unplaceable run and a half-covered operator all at once is a fixture nobody
// can read, and two of the three would never be the reason.
func unremovableText(page int, runs []extract.Run, quads []Quad) error {
	for _, run := range runs {
		if !runTouches(run, quads) {
			continue
		}
		if run.InForm {
			return fmt.Errorf("ops: page %d draws %q from a form XObject, whose bytes are "+
				"shared with every page that draws it, so this cannot be removed for one page alone",
				page, summarise(run.Text))
		}
		// ⛔ NOT a dead branch, and not one to drop. A run whose operation this
		// cannot locate is a run that would be SKIPPED by redactedSpans — left
		// on the page, under the fill, in a document the caller was told was
		// redacted. extract sets a span for every run it draws today; this is
		// what happens the day one of them does not.
		if run.Op.Empty() {
			return fmt.Errorf("ops: page %d draws %q from an operation this cannot locate in the stream",
				page, summarise(run.Text))
		}
		// ⛔⛔ AND THE RUN MUST BE WHOLLY INSIDE THE REGION. A run is the unit
		// that can be removed, so a run that merely OVERLAPS the region would
		// be blanked entire — and a producer that draws a whole line with one
		// Tj makes that line the run. Redacting one word of such a line would
		// take the line. That is not a leak, which is why it is tempting to
		// allow; it is the other way a redaction betrays its caller, by
		// removing what they did not ask about and not saying so.
		if !runInside(run, quads) {
			return fmt.Errorf("ops: on page %d the text %q reaches outside the region, and the "+
				"operation that draws it can only be removed whole, which would take text "+
				"the region does not cover",
				page, summarise(run.Text))
		}
	}
	// A text-showing operator whose text is partly inside the region and partly
	// outside cannot be blanked without taking away what was not asked for.
	for span, group := range bySpan(runs) {
		in, out := 0, 0
		for _, run := range group {
			if runTouches(run, quads) {
				in++
			} else {
				out++
			}
		}
		if in > 0 && out > 0 {
			return fmt.Errorf("ops: on page %d one operation (bytes %d..%d) draws both text to "+
				"redact and text to keep, and blanking it would remove both",
				page, span.From, span.To)
		}
	}
	return nil
}

// bySpan groups the runs of a page by the operation that drew them, which is
// what says whether an operator can be blanked whole.
func bySpan(runs []extract.Run) map[extract.Span][]extract.Run {
	out := map[extract.Span][]extract.Run{}
	for _, r := range runs {
		if r.Op.Empty() {
			continue
		}
		out[r.Op] = append(out[r.Op], r)
	}
	return out
}

// runTouches says whether a run's box meets any of the quads. The box is the
// baseline and the size above it, which is what a line of text occupies.
func runTouches(r extract.Run, quads []Quad) bool {
	return overlaps(r.X, r.Y, r.X+r.Width, r.Y+r.Size, quads)
}

// runSlack is how far a run's box may stand outside a quad and still count as
// inside it.
//
// ⛔ IT IS A MEASUREMENT, NOT A FUDGE. A caller that asks for the region a run
// occupies — the workbench, mapping a lit rectangle back into page space —
// arrives with a quad derived from screen pixels, so it is a fraction of a
// point out in each direction, and more at a zoom below 1:1. Two points is
// under half a line of ten-point text, so a run from the line above or below
// can never be swallowed by it, and the ordinary case of "this exact run" is
// not refused for a rounding error.
const runSlack = 2.0

// runInside says whether a run's box lies wholly within one of the quads, which
// is what makes the run's operator safe to blank whole.
func runInside(r extract.Run, quads []Quad) bool {
	for _, q := range quads {
		qx0 := min(q.TopLeftX, q.BottomLeftX) - runSlack
		qx1 := max(q.TopRightX, q.BottomRightX) + runSlack
		qy0 := min(q.BottomLeftY, q.BottomRightY) - runSlack
		qy1 := max(q.TopLeftY, q.TopRightY) + runSlack
		if r.X >= qx0 && r.X+r.Width <= qx1 && r.Y >= qy0 && r.Y+r.Size <= qy1 {
			return true
		}
	}
	return false
}

// overlaps says whether a box meets any of the quads, each taken as its own
// bounding box.
func overlaps(x0, y0, x1, y1 float64, quads []Quad) bool {
	for _, q := range quads {
		qx0 := min(q.TopLeftX, q.BottomLeftX)
		qx1 := max(q.TopRightX, q.BottomRightX)
		qy0 := min(q.BottomLeftY, q.BottomRightY)
		qy1 := max(q.TopLeftY, q.TopRightY)
		if x0 < qx1 && qx0 < x1 && y0 < qy1 && qy0 < y1 {
			return true
		}
	}
	return false
}

// summarise shortens a run's text for a message, so that an error about a
// secret does not print the whole of it.
func summarise(s string) string {
	const most = 24
	r := []rune(s)
	if len(r) <= most {
		return s
	}
	return string(r[:most]) + "…"
}

// redactContent blanks every text-showing operator whose text lies inside the
// region, and reports how many it blanked.
//
// ⛔ BLANKED WITH SPACES OF THE SAME LENGTH, which is what recolourContent does
// a file away and for the same reason: every offset after an edit is still the
// offset the scan reported, so the edits need no ordering rule. Here it does a
// second job — a span replaced by spaces no longer holds the string, so the
// text is gone from the bytes rather than merely unreferenced.
func redactContent(data []byte, spans map[extract.Span]bool) (out []byte, blanked int) {
	if len(spans) == 0 {
		return data, 0
	}
	edited := make([]byte, len(data))
	copy(edited, data)
	for span := range spans {
		if span.From < 0 || span.To > len(edited) || span.To <= span.From {
			continue
		}
		for i := span.From; i < span.To; i++ {
			edited[i] = ' '
		}
		blanked++
	}
	return edited, blanked
}

// redactedSpans is the operations to blank for a page's redactions.
//
// ⛔⛔ IT RETURNS THE ERROR RATHER THAN NO SPANS. Returning none would blank
// nothing and still paint the fill, which is a document that LOOKS redacted and
// is not — the one outcome worse than refusing. Doc.Bytes turns this into a
// failed write, so a caller gets no document at all rather than a leaky one.
func redactedSpans(src *reader.Document, page int, rs []Redaction) (map[extract.Span]bool, error) {
	runs, err := extractRuns(src, page)
	if err != nil {
		return nil, err
	}
	spans := map[extract.Span]bool{}
	for _, r := range rs {
		for _, run := range runs {
			if run.Op.Empty() || run.InForm {
				continue
			}
			// ⛔ INSIDE, the same test the refusal above uses. If these two
			// ever disagreed, the pair would be the defect: a run the check
			// passed over and this blanked would remove text nobody asked
			// about, and a run the check allowed and this skipped would be left
			// on the page under the fill.
			if runInside(run, r.Quads) {
				spans[run.Op] = true
			}
		}
	}
	return spans, nil
}

// rewrittenContent is a borrowed page's own content stream with the redactions
// blanked out of it and the recolouring applied, or an error that must stop the
// write.
//
// ⛔ THE REDACTION GOES FIRST, on the bytes as they were. Its spans came from a
// scan of exactly these bytes, and a recolouring INSERTS operators — "q 1 0 0
// rg " before every BT — so every offset after the first insertion has moved.
// Blanking is the other way round: it writes spaces over the same number of
// bytes, so the offsets a later scan reports are still the ones it reported.
//
// ⛔ And the two failures are not the same failure. A page whose stream cannot
// be read is, for a recolouring, a page left in its own colours — which is what
// this did before redaction existed and is no worse than a colour. For a
// redaction it is a document that must not be written at all.
func (d *Doc) rewrittenContent(p Page) (out []byte, rewritten bool, err error) {
	if !p.rewroteContent() {
		return nil, false, nil
	}
	content, cerr := pageContent(p.src, p.number)
	// ⛔⛔ THE ORDER IS A FACT ABOUT OFFSETS, NOT A PREFERENCE, and a page may
	// not carry both a redaction and a replacement at all — ReplaceText and
	// Redact refuse the pair, for reasons the first of them explains. What is
	// left is a sequence in which each step's spans are still true when it runs:
	//
	//   - a replacement CHANGES THE LENGTH of the stream, so every offset after
	//     it moves. Its own spans came from a scan of these bytes, so it runs
	//     before anything else edits them, and internally from the end
	//     backwards.
	//   - a redaction writes spaces over the same number of bytes, leaving every
	//     offset where it was, and it re-scans the SOURCE for its spans — which
	//     is exactly why it cannot follow a replacement, and why the pair is
	//     refused rather than ordered.
	//   - a recolouring INSERTS operators, so it is last whatever else happened.
	if len(p.textEdits) > 0 {
		edits, eerr := d.plannedEdits(p)
		if cerr != nil || eerr != nil {
			return nil, false, fmt.Errorf("ops: the text of page %d was to be "+
				"replaced and its own content stream could not be read, so the "+
				"replacement cannot be applied: %w", p.number, errors.Join(cerr, eerr))
		}
		content = editedContent(content, edits)
	}
	if len(p.redactions) > 0 {
		spans, serr := redactedSpans(p.src, p.number, p.redactions)
		if cerr != nil || serr != nil {
			return nil, false, fmt.Errorf("ops: page %d was to be redacted and its own content "+
				"stream could not be read, so the redaction cannot be applied: %w",
				p.number, errors.Join(cerr, serr))
		}
		content, _ = redactContent(content, spans)
	}
	if cerr != nil {
		return nil, false, nil // a recolouring alone: leave the page as it was
	}
	if p.recolour != nil {
		var unreached int
		content, unreached = recolourContent(content, *p.recolour)
		d.unreached += unreached
	}
	return content, true, nil
}

// redactionFill is the content that paints the emptied regions over, so a
// reader can see that something was taken out.
func redactionFill(rs []Redaction) string {
	var sb strings.Builder
	for _, r := range rs {
		for _, q := range r.Quads {
			x0 := min(q.TopLeftX, q.BottomLeftX)
			x1 := max(q.TopRightX, q.BottomRightX)
			y0 := min(q.BottomLeftY, q.BottomRightY)
			y1 := max(q.TopLeftY, q.TopRightY)
			if x1 <= x0 || y1 <= y0 {
				continue
			}
			fmt.Fprintf(&sb, " q %g %g %g rg %g %g %g %g re f Q",
				r.R, r.G, r.B, x0, y0, x1-x0, y1-y0)
		}
	}
	return sb.String()
}
