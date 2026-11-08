package ops

import (
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// blankPage is paper with nothing on it, 200 by 300 points: room for a note in
// a corner and a text box beside it.
func blankPage(t *testing.T) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
	})
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(300)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// inkedPixels is how many pixels of a rendered page are not white, which is the
// only measurement that answers "did this annotation draw anything".
func inkedPixels(t *testing.T, pdf []byte) int {
	t.Helper()
	img := renderOf(t, pdf)
	n := 0
	for i := 0; i < img.W*img.H; i++ {
		r, g, b := img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2]
		if !blank(r, g, b) {
			n++
		}
	}
	return n
}

// inkedIn is how many pixels inside a rectangle of the RENDER are not white.
// The rectangle is given in raster rows and columns, not in PDF points.
func inkedIn(t *testing.T, pdf []byte, x0, y0, x1, y1 int) int {
	t.Helper()
	img := renderOf(t, pdf)
	n := 0
	for y := y0; y < y1 && y < img.H; y++ {
		for x := x0; x < x1 && x < img.W; x++ {
			i := (y*img.W + x) * 4
			if !blank(img.Pix[i], img.Pix[i+1], img.Pix[i+2]) {
				n++
			}
		}
	}
	return n
}

// withNote is the blank page with one sticky note on it.
func withNote(t *testing.T, n Note) []byte {
	t.Helper()
	d, err := Open(blankPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddNotes(n); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// withFreeText is the blank page with one text box on it.
func withFreeText(t *testing.T, f FreeText) []byte {
	t.Helper()
	d, err := Open(blankPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFreeText(f); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ⛔⛔ THE NOTE IS VISIBLE, which is the only thing a reader cares about and the
// thing a correct annotation dictionary does NOT guarantee. Table 172 gives a
// /Name for the reader's own icon, and go-pdfkit/render draws an annotation only
// from its appearance stream: a note with a name and no /AP is a correct
// annotation that shows nothing at all.
func TestANoteIsVisibleWhereItWasPut(t *testing.T) {
	// The page is blank, so every inked pixel came from the note.
	if n := inkedPixels(t, blankPage(t)); n != 0 {
		t.Fatalf("precondition: the blank page has %d inked pixel(s)", n)
	}
	pdf := withNote(t, Note{Page: 1, X: 20, Y: 260, Contents: "look at this"})
	if n := inkedPixels(t, pdf); n == 0 {
		t.Fatal("the note drew nothing on the page")
	}
	// ⛔ AND WHERE IT WAS ASKED FOR. A PDF counts up from the foot and a raster
	// down from the top, so a note at y 260..280 on a 300-point page is rows
	// 20..40. An icon drawn at the wrong end of the page would pass a test that
	// only counted ink.
	inside := inkedIn(t, pdf, 20, 20, 41, 41)
	if inside == 0 {
		t.Error("nothing was drawn where the note was put")
	}
	if all := inkedPixels(t, pdf); all != inside {
		t.Errorf("%d pixel(s) were drawn and %d of them are where the note is",
			all, inside)
	}
}

func TestANoteCarriesWhatTheFormatAsksFor(t *testing.T) {
	pdf := withNote(t, Note{
		Page: 1, X: 10, Y: 10, Author: "a reader", Contents: "look at this",
		Open: true, Icon: IconComment,
	})
	for _, want := range []string{
		"/Subtype /Text", // Table 172: required
		"/Name /Comment", // the icon asked for
		"/Open true",     // the pop-up showing
		"(look at this)", // Contents
		"(a reader)",     // /T
		"/AP",            // the appearance, without which nothing renders
	} {
		if !strings.Contains(string(pdf), want) {
			t.Errorf("the written note does not carry %s", want)
		}
	}
}

// ⛔ Table 172: a text annotation behaves "as if the NoZoom and NoRotate
// annotation flags were always set". Writing them says out loud what a reader
// would do anyway — and the print flag is the one the marks carry for the same
// reason, since a comment that vanishes when somebody prints the page is a
// comment that was not really made.
func TestANoteIsPrintedAndNeitherZoomedNorRotated(t *testing.T) {
	pdf := withNote(t, Note{Page: 1, X: 10, Y: 10, Contents: "x"})
	want := annotPrint | annotNoZoom | annotNoRotate
	if !strings.Contains(string(pdf), "/F "+itoa(want)) {
		t.Errorf("the note's flags are not %d", want)
	}
}

func TestWhatANoteRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		note Note
		says string
	}{
		{"a page the document has not", Note{Page: 2, Contents: "x"}, "the document has 1"},
		{"page zero", Note{Page: 0, Contents: "x"}, "the document has 1"},
		{"saying nothing", Note{Page: 1}, "says nothing"},
		{"an icon the format does not name", Note{Page: 1, Contents: "x", Icon: "Smiley"}, `icon "Smiley"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Open(blankPage(t))
			if err != nil {
				t.Fatal(err)
			}
			err = d.AddNotes(c.note)
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q", err)
			}
			// ⛔ And nothing was kept: a refusal that half-added the note would
			// put it in the file at the next write.
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "/Subtype /Text") {
				t.Error("a refused note was written anyway")
			}
		})
	}
}

// ⛔⛔ THE TEXT BOX IS VISIBLE, and this is the annotation where that is least
// obvious: Table 174 makes /DA *required*, so a writer can satisfy the format
// completely and still produce a box that renders empty, because render needs
// the /AP. The test looks at pixels for exactly that reason.
func TestFreeTextIsVisibleInItsBox(t *testing.T) {
	pdf := withFreeText(t, FreeText{
		Page: 1, X0: 20, Y0: 200, X1: 180, Y1: 280,
		Text: "the second reader disagrees",
	})
	if n := inkedPixels(t, pdf); n == 0 {
		t.Fatal("the text box drew nothing on the page")
	}
	// A box at y 200..280 on a 300-point page is rows 20..100.
	if n := inkedIn(t, pdf, 20, 20, 181, 101); n == 0 {
		t.Error("nothing was drawn inside the box")
	}
	if all, inside := inkedPixels(t, pdf), inkedIn(t, pdf, 20, 20, 181, 101); all != inside {
		t.Errorf("%d pixel(s) were drawn and %d of them are inside the box",
			all, inside)
	}
}

func TestFreeTextCarriesWhatTheFormatAsksFor(t *testing.T) {
	pdf := withFreeText(t, FreeText{
		Page: 1, X0: 20, Y0: 200, X1: 180, Y1: 280,
		Text: "a comment", Author: "a reader", Align: AlignCentre, Size: 9,
	})
	for _, want := range []string{
		"/Subtype /FreeText", // Table 174: required
		"/DA",                // Table 174: REQUIRED, even with an /AP present
		"/Q 1",               // centred
		"(a comment)",
		"(a reader)",
		"/AP",
	} {
		if !strings.Contains(string(pdf), want) {
			t.Errorf("the written text box does not carry %s", want)
		}
	}
	// ⛔ The /DA names the same face and size the appearance was drawn with. A
	// reader that lets somebody EDIT the comment regenerates the appearance from
	// the /DA and from nothing else, so a /DA that disagreed with the appearance
	// would change the comment's look the first time it was touched.
	if !strings.Contains(string(pdf), "/PdfopsF 9 Tf") {
		t.Error("the /DA does not name the face and size the box was drawn with")
	}
}

// ⛔ The text is CLIPPED to its box, which is the normal case: somebody types
// more than they measured. Without the clip the overflow paints over whatever
// the page says underneath, which reads as a corrupted page rather than as a
// comment that needs a bigger box.
func TestFreeTextDoesNotPaintOutsideItsBox(t *testing.T) {
	pdf := withFreeText(t, FreeText{
		Page: 1, X0: 20, Y0: 260, X1: 80, Y1: 280,
		// Far more than three lines of 60 points will hold.
		Text: strings.Repeat("a very long comment indeed ", 12),
	})
	// The box is rows 20..40. Anything drawn below row 41 escaped it.
	if n := inkedIn(t, pdf, 0, 41, 200, 300); n != 0 {
		t.Errorf("%d pixel(s) were drawn below the box", n)
	}
	if n := inkedIn(t, pdf, 0, 0, 200, 20); n != 0 {
		t.Errorf("%d pixel(s) were drawn above the box", n)
	}
	// And it did draw something inside it, so the test above is measuring a
	// clip rather than a box that drew nothing at all.
	if n := inkedIn(t, pdf, 20, 20, 81, 41); n == 0 {
		t.Error("nothing was drawn inside the box")
	}
}

// ⛔ Justification is a number in the file AND a position on the page. A /Q
// written without the appearance following it would be a file that says
// "centred" and looks left-aligned in every reader that honours the /AP — which
// is every reader.
func TestFreeTextIsJustifiedWhereItSaysItIs(t *testing.T) {
	centre := func(align Quadding) float64 {
		t.Helper()
		pdf := withFreeText(t, FreeText{
			Page: 1, X0: 20, Y0: 200, X1: 180, Y1: 280,
			Text: "short", Align: align,
		})
		img := renderOf(t, pdf)
		sum, n := 0.0, 0
		for y := 0; y < img.H; y++ {
			for x := 0; x < img.W; x++ {
				i := (y*img.W + x) * 4
				if !blank(img.Pix[i], img.Pix[i+1], img.Pix[i+2]) {
					sum += float64(x)
					n++
				}
			}
		}
		if n == 0 {
			t.Fatalf("justification %d drew nothing", align)
		}
		return sum / float64(n)
	}
	left, mid, right := centre(AlignLeft), centre(AlignCentre), centre(AlignRight)
	if !(left < mid && mid < right) {
		t.Errorf("the ink sits at %.1f left, %.1f centred, %.1f right, which is not "+
			"left of centre of right", left, mid, right)
	}
}

// ⛔ The newlines somebody typed are lines. The wrapper this shares with the
// form fields collapses every blank including a newline — right for a field
// value, wrong for a comment written on three lines — so freeTextLines honours
// them and leaves the word wrapping where it was.
func TestFreeTextKeepsTheLinesThatWereTyped(t *testing.T) {
	for _, c := range []struct {
		name  string
		text  string
		lines int
	}{
		{"one line", "one line", 1},
		{"two typed lines", "first\nsecond", 2},
		{"a blank line between them", "first\n\nthird", 3},
		{"carriage returns too", "first\r\nsecond", 2},
		// Measured, not guessed: six four-letter words at 10pt Helvetica fit
		// two to a 60-point line, so this is three. The first version of this
		// case said two and the code was right.
		{"wrapped because it is too wide", "aaaa bbbb cccc dddd eeee ffff", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := freeTextLines(c.text, Helvetica, 10, 60, 99)
			if len(got) != c.lines {
				t.Errorf("%q became %d line(s): %q", c.text, len(got), got)
			}
		})
	}
}

// ⛔⛔ A BOX BOUNDS THE WORK, not the text. The appearance is clipped, so lines
// past the foot of the box are never seen — and wrapping the whole text first
// would mean a one-line box holding a pasted megabyte measures and allocates a
// megabyte of lines to throw away. Asserted as a bound rather than as a timing:
// the number of lines asked for never exceeds what the box can show.
func TestABoxBoundsTheLinesThatAreMeasured(t *testing.T) {
	huge := strings.Repeat("word ", 200_000) // a megabyte of comment
	// Measured: a 20-point box at 10pt type shows 2 lines (20/12 = 1, plus the
	// first), so that is all it may wrap.
	fit := linesThatFit(20, 10)
	if fit != 2 {
		t.Fatalf("a 20-point box at 10pt holds %d line(s)", fit)
	}
	if got := freeTextLines(huge, Helvetica, 10, 60, fit); len(got) != fit {
		t.Errorf("a megabyte of comment became %d line(s) in a box that shows %d",
			len(got), fit)
	}
	// ⛔ And the bound holds for the other shape of a long comment: fifty lines
	// somebody TYPED, rather than one long line that wraps. The first version
	// of this test used only the second and left the paragraph loop's own guard
	// unmeasured — two ways in, and a bound is only a bound if both are shut.
	typed := strings.Repeat("a line\n", 50)
	if got := freeTextLines(typed, Helvetica, 10, 60, fit); len(got) != fit {
		t.Errorf("fifty typed lines became %d in a box that shows %d", len(got), fit)
	}
	// ⛔ And a box shorter than its own type still draws its first line: a
	// comment that vanished entirely would read as a verb that did nothing,
	// while a clipped first line reads as a box too small — which it is.
	if got := linesThatFit(2, 12); got != 1 {
		t.Errorf("a box shorter than its type holds %d line(s)", got)
	}
	// The whole text still reaches the file, because that is the comment. Only
	// the drawing stops.
	pdf := withFreeText(t, FreeText{
		Page: 1, X0: 20, Y0: 260, X1: 80, Y1: 280,
		Text: "the beginning " + strings.Repeat("and more ", 50) + "the end",
	})
	if !strings.Contains(string(pdf), "the end") {
		t.Error("the written file does not carry the whole comment")
	}
}

func TestWhatFreeTextRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		text FreeText
		says string
	}{
		{"a page the document has not",
			FreeText{Page: 2, Text: "x", X1: 10, Y1: 10}, "the document has 1"},
		{"saying nothing",
			FreeText{Page: 1, X1: 10, Y1: 10}, "says nothing"},
		{"a box of no width",
			FreeText{Page: 1, Text: "x", Y1: 10}, "0 by 10 points"},
		{"a box of no height",
			FreeText{Page: 1, Text: "x", X1: 10}, "10 by 0 points"},
		{"justification the format does not number",
			FreeText{Page: 1, Text: "x", X1: 10, Y1: 10, Align: 7}, "justification 7"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Open(blankPage(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.AddFreeText(c.text); err == nil {
				t.Fatal("it was accepted")
			} else if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q", err)
			}
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "/Subtype /FreeText") {
				t.Error("a refused text box was written anyway")
			}
		})
	}
}

// ⛔ The defaults are the ones a caller who chose nothing wants: Table 172's own
// icon default, a sticky note's yellow rather than a black square, Helvetica at
// 12 for a comment, and black text — the one place where the zero value is
// right, because a typewriter comment is black.
func TestTheDefaultsAnnotationsTakeWhenNothingWasChosen(t *testing.T) {
	pdf := withNote(t, Note{Page: 1, X: 10, Y: 10, Contents: "x"})
	if !strings.Contains(string(pdf), "/Name /Note") {
		t.Error("a note with no icon chosen is not a /Note")
	}
	if r, g, b := (Note{}).colour(); r != 1 || g != 0.86 || b != 0.3 {
		t.Errorf("a note with no colour chosen is %g %g %g, not yellow", r, g, b)
	}
	if got := (FreeText{}).size(); got != freeTextSize {
		t.Errorf("free text with no size chosen is %g", got)
	}
	if got := (FreeText{}).face(); got != Helvetica {
		t.Errorf("free text with no face chosen is %q", got)
	}
	// A size of zero or less is "I did not choose", not "draw nothing".
	if got := (FreeText{Size: -3}).size(); got != freeTextSize {
		t.Errorf("a negative size became %g", got)
	}
}

// ⛔ And a choice that WAS made is kept, which is the other half of every
// default above: a colour read as "unchosen" because it happened to be dark, or
// a face silently replaced by Helvetica, would be the defaults overriding the
// caller rather than filling in for them.
func TestTheChoicesAnnotationsKeepWhenTheyWereMade(t *testing.T) {
	// A colour outside 0..1 is clamped rather than refused: it is a number the
	// caller got wrong by a scale, not a document they cannot have.
	r, g, b := Note{R: 0.2, G: 1.4, B: -1}.colour()
	if r != 0.2 || g != 1 || b != 0 {
		t.Errorf("a chosen colour came back %g %g %g", r, g, b)
	}
	if got := (FreeText{Font: HelveticaBold}).face(); got != HelveticaBold {
		t.Errorf("a chosen face came back %q", got)
	}
	// And the chosen face reaches the file, in the /DA and in the appearance's
	// own resources.
	pdf := withFreeText(t, FreeText{
		Page: 1, X0: 20, Y0: 200, X1: 180, Y1: 280,
		Text: "bold", Font: HelveticaBold,
	})
	if !strings.Contains(string(pdf), "/Helvetica-Bold") {
		t.Error("the chosen face is not in the written file")
	}
	// A note drawn in a chosen colour draws something: the colour reaches the
	// appearance rather than only the /C entry.
	chosen := withNote(t, Note{Page: 1, X: 20, Y: 260, Contents: "x", R: 0.2, G: 0.4, B: 0.9})
	if n := inkedPixels(t, chosen); n == 0 {
		t.Error("a note in a chosen colour drew nothing")
	}
}

// ⛔ Both annotations go on the page they NAME, not on the first one. An
// annotation attached to the wrong page is a comment about the wrong paragraph.
func TestAnAnnotationGoesOnThePageItNames(t *testing.T) {
	d, err := Open(blankPage(t))
	if err != nil {
		t.Fatal(err)
	}
	d.Append(mustOpen(t, blankPage(t)))
	if d.PageCount() != 2 {
		t.Fatalf("the document has %d page(s)", d.PageCount())
	}
	if err := d.AddNotes(Note{Page: 2, X: 10, Y: 10, Contents: "on the second"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFreeText(FreeText{
		Page: 2, X0: 20, Y0: 200, X1: 180, Y1: 280, Text: "also on the second",
	}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if n := inkedPixels(t, out); n != 0 {
		t.Errorf("the FIRST page has %d inked pixel(s), and both annotations "+
			"name the second", n)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	page, err := src.Page(2)
	if err != nil {
		t.Fatal(err)
	}
	list, err := src.Resolve(page.Get("Annots"))
	if err != nil {
		t.Fatal(err)
	}
	annots, _ := reader.ToArray(list)
	if len(annots) != 2 {
		t.Errorf("the second page carries %d annotation(s)", len(annots))
	}
}

func mustOpen(t *testing.T, pdf []byte) *Doc {
	t.Helper()
	d, err := Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
