// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/xfa"
)

// Drawing the form inside a document whose pages are a placeholder.
//
// A dynamic XFA document is blank to look at. Its pages hold one panel reading
// "Please wait... your PDF viewer may not be able to display this type of
// document", and the form itself exists only as XML, laid out when Adobe's own
// reader opens it. No browser and no other reader lays one out, and the format
// was removed from PDF 2.0 — so a person handed such a file has a document
// that looks empty and is not.
//
// [FromXFA] lays that form out and draws it, which turns the file into one any
// reader can show.
//
// # What this draws, and what it does not
//
// WHERE each element goes — which sheet, and where on it — is
// [github.com/go-pdfkit/xfa]'s, measured against pdf.js and pdfium over a
// corpus of real government forms. What is drawn INSIDE each box is this
// package's own and much simpler: the four standard faces, a greedy line
// break, and a rule around each field so a reader can see where the answers
// go. A template naming Myriad at nine points is drawn in Helvetica at nine
// points.
//
// So this is legibility rather than fidelity: it is the difference between a
// blank sheet and a form somebody can read, not between this and Adobe. Where
// the two would differ visibly — a face much wider than Helvetica, text that
// overruns its box — the box is what the layout measured, and the text is
// clipped to it rather than drawn over its neighbour.
//
// # Pictures are not drawn, and one form in fourteen is mostly picture
//
// A template's <image> holds a picture, base64, inside the XML. None is drawn
// here, and for thirteen of the corpus's fourteen dynamic forms that costs a
// logo.
//
// The fourteenth is worth knowing about before it surprises somebody. French
// cerfa 12064 carries 212 fields, 7 draws, 4 images and — measured, not
// guessed — NO <text> and NO <caption> at all: the printed form is one of
// those pictures, and the fields are boxes laid over it. What comes out is
// therefore a correct grid of 212 empty boxes on a blank sheet. The layout is
// right and the sheet is nearly wordless, because the template has nearly no
// words in it.
//
// So the honest limit of this verb is pictures rather than text. A form that
// prints its labels as characters comes out readable; one that prints them as
// a picture comes out as the boxes alone.
//
// Scripts are not run, which is [github.com/go-pdfkit/xfa]'s decision and its
// reasoning: of 5 744 scripts across the corpus's dynamic forms, 5 131 are
// handlers for typing and clicking, and only 188 run at load.

// An XFAReport says what laying a form out came to. A caller showing somebody
// the result needs to be able to say what is missing from it.
type XFAReport struct {
	// Sheets is how many pages the form came to.
	Sheets int
	// Drawn is how many elements were put on them.
	Drawn int
	// Hidden is how many the template asks not to be shown. They are placed —
	// they take up room, and what follows them sits below — and not drawn.
	Hidden int
	// Unplaced is everything the layout could not place, one line each, with
	// the reason the engine gave. Empty is the ordinary case.
	Unplaced []string
}

// FromXFA lays out the XFA form a document carries and returns a document of
// its pages, drawn.
//
// The source document's own pages are not kept: for a dynamic form they are
// the placeholder, and for a static one they are the form itself and this is
// the wrong verb — see [forms.Form.Dynamic], which says which kind a document
// is. This does not refuse a static one, because a caller may legitimately
// want to see what the XML says as against what the pages show, but a caller
// that has not asked which kind it has will replace a perfectly good form with
// a plainer drawing of it.
func FromXFA(src *reader.Document) (*Doc, *XFAReport, error) {
	form, ok := forms.Read(src)
	if !ok {
		return nil, nil, fmt.Errorf("ops: the document has no form of any kind")
	}
	if !form.HasXFA() {
		return nil, nil, fmt.Errorf("ops: the document's form carries no XFA package")
	}
	packets := form.Packets()
	tmplXML := xfaPart(packets, "template")
	if tmplXML == nil {
		// A package written as one stream rather than as named parts holds the
		// whole XDP, and its root is <xdp> rather than <template>. Saying so is
		// worth more than "no template": the difference tells whoever reads the
		// message which of the two problems they have.
		if len(packets) == 1 && packets[0].Name == "" {
			return nil, nil, fmt.Errorf("ops: this XFA package is one unnamed stream, " +
				"which holds the whole XDP rather than the named parts this reads")
		}
		return nil, nil, fmt.Errorf("ops: this XFA package has no template part (it has %s)",
			partNames(packets))
	}
	template, err := xfa.ParseTemplate(bytes.NewReader(tmplXML))
	if err != nil {
		return nil, nil, fmt.Errorf("ops: %w", err)
	}
	// A form with no data still lays out — every field is simply empty — so
	// datasets that will not read are a reason to draw the blank form rather
	// than to refuse the document.
	var data *xfa.Node
	if b := xfaPart(packets, "datasets"); b != nil {
		data, _ = xfa.ParseDatasets(bytes.NewReader(b))
	}

	layout := xfa.Place(xfa.Expand(template, data))
	d := New()
	rep := &XFAReport{Sheets: len(layout.Pages)}
	for _, page := range layout.Pages {
		w, h := page.Width.Points(), page.Height.Points()
		if w <= 0 || h <= 0 {
			// A page area writing no medium gives no sheet size, and pdf.js
			// declines to guess at one too. US Letter is this package's guess
			// and is named as one.
			w, h = 612, 792
		}
		d.Blank(w, h)
		p := &d.pages[len(d.pages)-1]
		for _, b := range page.Boxes {
			if b.Hidden {
				rep.Hidden++
				continue
			}
			p.marks = append(p.marks, xfaMarks(b, h)...)
			rep.Drawn++
		}
	}
	for _, u := range layout.Unplaced {
		rep.Unplaced = append(rep.Unplaced, fmt.Sprintf("%s (%s): %s", u.Path, u.Kind, u.Why))
	}
	return d, rep, nil
}

// xfaPart returns the named part of an XFA package, or nil.
func xfaPart(ps []forms.Packet, name string) []byte {
	for _, p := range ps {
		if p.Name == name {
			return p.Data
		}
	}
	return nil
}

// partNames lists what a package does hold, for a message that has to say why
// the one part wanted is not there.
func partNames(ps []forms.Packet) string {
	var names []string
	for _, p := range ps {
		if p.Name == "" {
			names = append(names, "an unnamed part")
			continue
		}
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return "nothing"
	}
	return strings.Join(names, ", ")
}

// xfaMarks turns one placed element into what is drawn for it: a rule around a
// field, and the text of its caption and its value.
//
// pageHeight is what turns XFA's coordinates into a PDF's. A form measures
// down from the top left of the sheet and a PDF draws up from the bottom left,
// so every y is subtracted rather than translated — and a box's y is its TOP,
// which is what makes the subtraction the box's height short of its baseline.
func xfaMarks(b xfa.Box, pageHeight float64) []stampInstance {
	x, y := b.Rect.X.Points(), b.Rect.Y.Points()
	w, h := b.Rect.W.Points(), b.Rect.H.Points()
	var out []stampInstance

	// A field is drawn with a rule around it. A draw is text the form prints
	// and has no box of its own to show.
	if b.Kind == "field" && w > 0 && h > 0 {
		out = append(out, stampInstance{
			outline: &[4]float64{x, pageHeight - y - h, w, h},
		})
	}

	// A caption is what the form calls the field, and it is set apart from
	// what somebody wrote in it: the two would otherwise read as one string.
	// A draw has neither — its text is its content.
	caption, value := xfaCaption(b.Node), b.Value
	if b.Kind == "draw" {
		caption, value = xfaContent(b.Node), ""
	}
	runs := []struct {
		s string
		f Font
	}{{caption, boldIf(b.Kind == "field")}, {value, Helvetica}}

	size := xfaShrinkToFit(runs, w, h, xfaFontSize(b.Node))

	// Both run down the same box, the caption first, because that is the
	// order XFA's own default caption placement puts them in and because a
	// value drawn over its caption is worse than one drawn below it.
	line := 0
	for _, r := range runs {
		if strings.TrimSpace(r.s) == "" {
			continue
		}
		for _, l := range wrapToWidth(r.f, r.s, size, w) {
			// Anything still past the bottom after shrinking is dropped
			// rather than drawn over whatever is under it.
			top := float64(line+1) * size * 1.2
			if h > 0 && top > h {
				break
			}
			out = append(out, stampInstance{
				stamp: Stamp{Text: l, Font: r.f, Size: size},
				text:  l,
				at:    &[2]float64{x + 1, pageHeight - y - top + size*0.28},
			})
			line++
		}
	}
	return out
}

// boldIf picks the face a run is drawn in. A field's caption is set in bold so
// that the question and the answer do not read as one sentence.
func boldIf(bold bool) Font {
	if bold {
		return HelveticaBold
	}
	return Helvetica
}

// xfaShrinkToFit is the size the runs are drawn at so that they fit the box
// the layout measured for them.
//
// The layout measured that box with the font the TEMPLATE names, and this
// draws in Helvetica, which is wider than the condensed faces government forms
// are set in. At the template's own size the text therefore comes to more
// lines than the box holds, and the tail of it is lost — "…property held at
// any time during the year exceeds $100,000 but was less than" stopped there,
// mid-sentence, on a Canada Revenue Agency form.
//
// Losing the end of a sentence is worse than setting it a point smaller, so
// the size comes down until the lines fit. It stops at four points, below
// which nothing is readable anyway and the text is clipped as before: a box
// far too small for its words is a layout this package got wrong, and
// shrinking to nothing would hide that rather than show it.
func xfaShrinkToFit(runs []struct {
	s string
	f Font
}, w, h, size float64) float64 {
	if h <= 0 || w <= 0 {
		return size
	}
	// The floor is checked after the shrink as well as before it: testing
	// only the loop's condition let a size of 4.3 become 3.87 and stop there,
	// which is below the floor this says it holds.
	const floor = 4
	for {
		lines := 0
		for _, r := range runs {
			lines += len(wrapToWidth(r.f, r.s, size, w))
		}
		if lines == 0 || float64(lines)*size*1.2 <= h {
			return size
		}
		if size <= floor {
			return floor
		}
		if size *= 0.9; size < floor {
			size = floor
		}
	}
}

// xfaFontSize is the size the template asks for, in points, or ten.
//
// Ten is [github.com/go-pdfkit/xfa]'s own default and pdf.js's before it: a
// leaf whose template writes no size is measured at ten points there, so
// drawing it at ten is drawing it at the size it was measured at.
func xfaFontSize(n *xfa.FormNode) float64 {
	if n == nil || n.Template == nil {
		return 10
	}
	if f := n.Template.Child("font"); f != nil {
		if m, ok, err := f.Measure("size"); ok && err == nil && m > 0 {
			return m.Points()
		}
	}
	return 10
}

// xfaCaption is what a field's <caption> says.
func xfaCaption(n *xfa.FormNode) string {
	if n == nil || n.Template == nil {
		return ""
	}
	c := n.Template.Child("caption")
	if c == nil {
		return ""
	}
	return xfaValueText(c.Child("value"))
}

// xfaContent is the text a draw prints.
func xfaContent(n *xfa.FormNode) string {
	if n == nil || n.Template == nil {
		return ""
	}
	return xfaValueText(n.Template.Child("value"))
}

// xfaValueKinds is what a <value> may hold that is worth reading as words, and
// it is pdf.js's list rather than "whatever is in there"
// (template.js:6027-6039, walked at :6080).
//
// <image> is the one left out, and leaving it out is the whole point of having
// a list. An image's element holds the PICTURE, base64 inside the XML, and a
// walk that gathers every string under a <value> gathers that too: the first
// draw of a French cerfa came out with 1.4 kB of "/9j/4AAQSkZJRgABAQ..."
// drawn across its title. pdf.js skips image for the same reason
// (:6081-6083) — the bytes of a picture are not a caption.
var xfaValueKinds = map[string]bool{
	"arc": true, "boolean": true, "date": true, "dateTime": true,
	"decimal": true, "exData": true, "float": true, "integer": true,
	"line": true, "rectangle": true, "text": true, "time": true,
}

// xfaValueText reads the words out of a <value>.
//
// A value holds exactly one of the kinds above — a <text>, a <date>, an
// <exData> of XHTML — and what is wanted from all of them is the same: the
// characters, in the order they are written. A rich text's characters sit in
// its elements rather than directly under it, which is why this descends into
// a kind once it has decided to read it.
func xfaValueText(v *xfa.Node) string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	for _, kid := range v.Kids {
		if !xfaValueKinds[kid.Kind] {
			continue
		}
		kid.Walk(func(n *xfa.Node) {
			if n.Text == "" {
				return
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(n.Text)
		})
	}
	return strings.TrimSpace(b.String())
}

// wrapToWidth breaks a string into the lines it comes to in the width given,
// at the last space that fits.
//
// A word longer than the whole width is not broken: it goes on a line of its
// own and overruns, because breaking inside a word turns an account number
// into two of them. A width of nothing does not break at all.
func wrapToWidth(f Font, s string, size, width float64) []string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return nil
	}
	if width <= 0 || f.Width(s, size) <= width {
		return []string{s}
	}
	var lines []string
	line := ""
	for _, word := range strings.Split(s, " ") {
		try := word
		if line != "" {
			try = line + " " + word
		}
		if f.Width(try, size) <= width || line == "" {
			line = try
			continue
		}
		lines = append(lines, line)
		line = word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
