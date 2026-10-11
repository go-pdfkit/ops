// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

var blue = [3]float64{0, 0, 1}

// ⛔ THE CASE THAT CHANGED THE DESIGN. Setting a colour before each BT is not
// enough: a text object that sets its OWN colour overrides it, and a page
// whose paragraphs say "1 0 0 rg" would come out unchanged while the tool
// reported success. The colour operators inside a text object are blanked.
func TestAColourSetInsideTheTextIsNotLeftToWin(t *testing.T) {
	const in = "BT /F1 12 Tf 1 0 0 rg (red please) Tj ET\n"
	out, _ := recolourContent([]byte(in), blue)
	got := string(out)
	if !strings.Contains(got, "0 0 1 rg") {
		t.Errorf("the asked-for colour is not in %q", got)
	}
	// ⛔ The original "1 0 0 rg" must no longer be an operation. It is blanked
	// rather than deleted, so the bytes are spaces — asserting on the SCAN
	// rather than on the text, because "1 0 0 rg" also appears nowhere once
	// blanked and a string search would pass on a stream that lost the text
	// too.
	for _, op := range mustOps(t, out) {
		if op.Operator == "rg" && len(op.Operands) == 3 {
			if f, _ := reader.ToFloat(op.Operands[0]); f == 1 {
				t.Errorf("the original red is still an operation in %q", got)
			}
		}
	}
	// And the text itself survives.
	if !strings.Contains(got, "(red please) Tj") {
		t.Errorf("the text was lost: %q", got)
	}
}

// ⛔ The colour goes OUTSIDE the text object, wrapped in q/Q. A text object
// does not save the graphics state, so a colour set inside one leaks into
// whatever is drawn after it — which is how a recolouring turns a logo the
// same shade as the paragraphs.
func TestTheColourDoesNotLeakPastTheText(t *testing.T) {
	const in = "BT (words) Tj ET\n1 0 0 rg 10 10 20 20 re f\n"
	out, _ := recolourContent([]byte(in), blue)
	got := string(out)
	// The q must come before the BT and the Q after the ET, so the rectangle
	// after it keeps its own red.
	q := strings.Index(got, "q ")
	bt := strings.Index(got, "BT")
	et := strings.Index(got, "ET")
	qq := strings.Index(got, " Q")
	rect := strings.Index(got, "re f")
	if !(q >= 0 && q < bt && bt < et && et < qq && qq < rect) {
		t.Errorf("the wrapping is in the wrong places (%d %d %d %d %d):\n%s",
			q, bt, et, qq, rect, got)
	}
	// ⛔ And the rectangle's own colour is untouched: recolouring the TEXT
	// must not recolour the drawing.
	var reds int
	for _, op := range mustOps(t, out) {
		if op.Operator == "rg" && len(op.Operands) == 3 {
			if f, _ := reader.ToFloat(op.Operands[0]); f == 1 {
				reds++
			}
		}
	}
	if reds != 1 {
		t.Errorf("%d red fills survive, want the rectangle's one:\n%s", reds, got)
	}
}

// Both the filling and the stroking colour are set, because text has render
// modes: 0 fills, 1 strokes, 2 does both. A tool that set only the fill would
// leave outlined text in its old colour.
func TestBothTheFillAndTheStrokeAreSet(t *testing.T) {
	out, _ := recolourContent([]byte("BT 1 Tr (outlined) Tj ET"), blue)
	got := string(out)
	if !strings.Contains(got, "0 0 1 rg") {
		t.Errorf("no filling colour in %q", got)
	}
	if !strings.Contains(got, "0 0 1 RG") {
		t.Errorf("no stroking colour in %q", got)
	}
}

// Every colour operator a text object can use is blanked, not just rg: a
// document in CMYK or in a named colour space would otherwise keep its own.
func TestEveryColourOperatorInsideTheTextIsBlanked(t *testing.T) {
	for _, op := range []string{
		"0 g", "1 0 0 rg", "0 0 0 1 k", "/CS0 cs", "0.5 sc", "0.1 0.2 0.3 scn",
		"0 G", "1 0 0 RG", "0 0 0 1 K", "/CS0 CS", "0.5 SC", "0.1 0.2 0.3 SCN",
	} {
		in := "BT " + op + " (x) Tj ET"
		out, _ := recolourContent([]byte(in), blue)
		// The operator must no longer be an operation of the stream.
		name := op[strings.LastIndex(op, " ")+1:]
		for _, got := range mustOps(t, out) {
			if got.Operator == name && !isAskedFor(got, blue) {
				t.Errorf("%q survived inside the text: %q", op, out)
			}
		}
		if !strings.Contains(string(out), "(x) Tj") {
			t.Errorf("%q: the text was lost: %q", op, out)
		}
	}
}

// ⛔ A page with no text is returned UNCHANGED, byte for byte. A recolouring
// that rewrote a stream it had nothing to do to would churn every file it
// touched and lose the one guarantee worth having: what is not recoloured is
// not altered.
func TestAStreamWithNoTextIsUntouched(t *testing.T) {
	const in = "q 1 0 0 rg 10 10 20 20 re f Q\n"
	out, unreached := recolourContent([]byte(in), blue)
	if string(out) != in {
		t.Errorf("a stream with no text was rewritten:\n%q\n%q", in, out)
	}
	if unreached != 0 {
		t.Errorf("unreached = %d, want 0", unreached)
	}
}

// ⛔ Text inside a Form XObject is COUNTED rather than silently left. A page
// whose text is all in XObjects would otherwise come back unchanged with
// nothing said, which is worse than a refusal.
func TestWhatCannotBeReachedIsCounted(t *testing.T) {
	const in = "q /Fm0 Do Q\nBT (on the page) Tj ET\n"
	out, unreached := recolourContent([]byte(in), blue)
	if unreached != 1 {
		t.Errorf("unreached = %d, want 1 for the one Do", unreached)
	}
	// And the text that IS on the page is still recoloured: a limitation is
	// not an excuse to do nothing.
	if !strings.Contains(string(out), "0 0 1 rg") {
		t.Errorf("the page's own text was not recoloured: %q", out)
	}
}

// Several text objects each get their own wrapping, and nested BT — which the
// format does not allow but a file can hold — does not produce a stray Q.
func TestSeveralTextObjectsAndANestedOne(t *testing.T) {
	const in = "BT (one) Tj ET BT (two) Tj ET"
	out, _ := recolourContent([]byte(in), blue)
	got := string(out)
	if n := strings.Count(got, "0 0 1 rg"); n != 2 {
		t.Errorf("%d colours set for two text objects:\n%s", n, got)
	}
	if strings.Count(got, "q ") != 2 || strings.Count(got, " Q") != 2 {
		t.Errorf("the wrapping is not balanced:\n%s", got)
	}
	// ⛔ A nested BT is illegal and a file can still hold one. The wrapping
	// must stay balanced: one q and one Q for the outer object, and none for
	// the inner, or the graphics state is left unbalanced and everything
	// after the page is drawn wrong.
	nested, _ := recolourContent([]byte("BT (a) Tj BT (b) Tj ET ET"), blue)
	ng := string(nested)
	if strings.Count(ng, "q ") != strings.Count(ng, " Q") {
		t.Errorf("a nested text object left the state unbalanced:\n%s", ng)
	}
	if strings.Count(ng, "q ") != 1 {
		t.Errorf("%d wrappings for a nested pair, want 1:\n%s", strings.Count(ng, "q "), ng)
	}
}

// ⛔ An ET with no BT before it, which a damaged file can hold, must not
// produce a Q that unbalances the state.
func TestAStrayEndOfTextDoesNotUnbalanceTheState(t *testing.T) {
	out, _ := recolourContent([]byte("ET 10 10 20 20 re f"), blue)
	got := string(out)
	if strings.Contains(got, "Q") {
		t.Errorf("a stray ET produced a Q:\n%s", got)
	}
}

// ⛔ An inline image inside a text object keeps its bytes. This is why the
// rewriting splices rather than re-serialises: an image's data is still
// encoded, and formatting it again can change what it means.
func TestAnInlineImageKeepsItsBytes(t *testing.T) {
	const in = "BT (x) Tj BI /W 1 /H 1 /CS /G /BPC 8 /F /AHx ID 00> EI ET"
	out, _ := recolourContent([]byte(in), blue)
	got := string(out)
	if !strings.Contains(got, "ID 00> EI") {
		t.Errorf("the image's data changed:\n%s", got)
	}
}

func TestTheColourIsFormattedShortly(t *testing.T) {
	if got := colourText([3]float64{0, 0, 1}); got != "0 0 1 rg 0 0 1 RG" {
		t.Errorf("colourText = %q", got)
	}
	if got := colourText([3]float64{0.5, 0.25, 0}); got != "0.5 0.25 0 rg 0.5 0.25 0 RG" {
		t.Errorf("colourText = %q", got)
	}
}

// mustOps scans a stream and fails if it will not scan.
func mustOps(t *testing.T, data []byte) []reader.Operation {
	t.Helper()
	ops, err := reader.Operations(data)
	if err != nil {
		t.Fatalf("the rewritten stream does not scan: %v\n%s", err, data)
	}
	return ops
}

// isAskedFor says whether an operation is the colour this test asked for,
// rather than one the original stream had.
func isAskedFor(op reader.Operation, c [3]float64) bool {
	if len(op.Operands) != 3 {
		return false
	}
	for i, want := range c {
		got, ok := reader.ToFloat(op.Operands[i])
		if !ok || got != want {
			return false
		}
	}
	return true
}

// textPDF builds a document whose pages each hold a text object that sets its
// own colour, which is the case a recolouring has to win against.
func textPDF(t *testing.T, pages int) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	kids := make(reader.Array, 0, pages)
	for i := 1; i <= pages; i++ {
		body := fmt.Sprintf("1 0 0 rg 5 5 10 10 re f\nBT /F1 12 Tf 1 0 0 rg (page %d) Tj ET\n", i)
		kids = append(kids, w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(body)}),
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count": reader.Integer(pages),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(200)}})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pageText reads back one page's content from a written file.
func pageContentOf(t *testing.T, b []byte, page int) string {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	content, err := d.PageContent(page)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// End to end: the text comes out in the colour asked for, and the rectangle
// beside it keeps its own.
func TestRecolourTextEndToEnd(t *testing.T) {
	d, err := Open(textPDF(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RecolourText("all", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 2; page++ {
		got := pageContentOf(t, out, page)
		if !strings.Contains(got, "0 0 1 rg") {
			t.Errorf("page %d has no blue:\n%s", page, got)
		}
		if !strings.Contains(got, "re f") {
			t.Errorf("page %d lost its rectangle:\n%s", page, got)
		}
		// ⛔ The rectangle's own red survives: exactly ONE red fill, the one
		// outside the text object.
		if n := strings.Count(got, "1 0 0 rg"); n != 1 {
			t.Errorf("page %d has %d red fills, want the rectangle's one:\n%s", page, n, got)
		}
	}
}

// A page range recolours those pages and no others.
func TestRecolourARange(t *testing.T) {
	d, err := Open(textPDF(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RecolourText("2", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := pageContentOf(t, out, 2); !strings.Contains(got, "0 0 1 rg") {
		t.Errorf("page 2 was not recoloured:\n%s", got)
	}
	for _, page := range []int{1, 3} {
		if got := pageContentOf(t, out, page); strings.Contains(got, "0 0 1 rg") {
			t.Errorf("page %d was recoloured and should not have been:\n%s", page, got)
		}
	}
}

// ⛔⛔ THE COMPOSITION. A page both recoloured and watermarked must keep both.
// The first draft lost the recolouring: `decorate` rebuilt the page's Contents
// from the SOURCE's parts, throwing away the rewritten stream, and nothing
// failed — the file was valid and the text was its old colour.
func TestRecolourAndWatermarkTogether(t *testing.T) {
	d, err := Open(textPDF(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RecolourText("all", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := d.Watermark("all", "DRAFT"); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := pageContentOf(t, out, 1)
	if !strings.Contains(got, "0 0 1 rg") {
		t.Errorf("the recolouring was lost to the watermark:\n%s", got)
	}
	if !strings.Contains(got, "DRAFT") {
		t.Errorf("the watermark is not there:\n%s", got)
	}
	// ⛔ And the watermark keeps ITS own colour: a stamp's colour is the
	// stamp's business, which is why the recolouring happens before the
	// decoration and not over it.
	if !strings.Contains(got, "(page 1) Tj") {
		t.Errorf("the page's text was lost:\n%s", got)
	}
}

// A page this package MADE has no text of its own to recolour, and saying so
// by doing nothing is better than appearing to work.
func TestRecolouringAMadePageDoesNothing(t *testing.T) {
	d := New()
	d.Blank(100, 200)
	if err := d.Stamp("all", Stamp{Text: "made", Font: Helvetica, Size: 12}); err != nil {
		t.Fatal(err)
	}
	if err := d.RecolourText("all", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := pageContentOf(t, out, 1); !strings.Contains(got, "made") {
		t.Errorf("the made page lost its stamp:\n%s", got)
	}
}

func TestRecolourRefusals(t *testing.T) {
	d, err := Open(textPDF(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][3]float64{{-1, 0, 0}, {0, 2, 0}, {0, 0, -0.1}} {
		if err := d.RecolourText("all", c[0], c[1], c[2]); err == nil {
			t.Errorf("a colour of %v was accepted", c)
		}
	}
	if err := d.RecolourText("not a range", 0, 0, 1); err == nil {
		t.Error("a range that is not one was accepted")
	}
	if err := New().RecolourText("all", 0, 0, 1); err == nil {
		t.Error("an empty document was recoloured")
	}
}

// What could not be reached is reported after the write, because until then
// there is nothing to have reached.
func TestUnreachedTextIsReportedAfterTheWrite(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	form := w.Add(&reader.Stream{
		Dict: reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(10), reader.Integer(10)}},
		Raw: []byte("BT (inside a form) Tj ET"),
	})
	kids := reader.Array{w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"Resources": reader.Dict{"XObject": reader.Dict{"Fm0": form}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q /Fm0 Do Q\nBT (on the page) Tj ET\n")}),
	})}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(100)}})
	src, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.UnreachedText(); got != 0 {
		t.Errorf("UnreachedText before the write = %d, want 0", got)
	}
	if err := d.RecolourText("all", 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.UnreachedText(); got != 1 {
		t.Errorf("UnreachedText = %d, want 1 for the form", got)
	}
	// The page's own text IS recoloured: a limitation is not an excuse to do
	// nothing.
	if got := pageContentOf(t, out, 1); !strings.Contains(got, "0 0 1 rg") {
		t.Errorf("the page's own text was not recoloured:\n%s", got)
	}
}

// ⛔ A Do inside a text object is counted too. Drawing an XObject there is not
// something the format allows and files do it anyway, and the count must not
// depend on where the Do sits — a page whose only unreachable text is inside
// one would otherwise be reported as fully recoloured.
func TestADoInsideATextObjectIsCounted(t *testing.T) {
	out, unreached := recolourContent([]byte("BT (a) Tj /Fm0 Do ET"), blue)
	if unreached != 1 {
		t.Errorf("unreached = %d, want 1", unreached)
	}
	if !strings.Contains(string(out), "0 0 1 rg") {
		t.Errorf("the text beside it was not recoloured: %q", out)
	}
}
