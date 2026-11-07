package ops

import (
	"strings"
	"testing"
	"time"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// barPage is a page with a black bar across the middle of it, standing in for
// a line of text: ink for a mark to be drawn over.
//
// ⛔ THE BAR IS SHORTER THAN THE QUAD THE TESTS MARK. It sits at y 95..105
// inside a mark of y 90..110, which leaves paper above and below it INSIDE the
// marked area. Without that there is nowhere to tell a highlight (which fills
// the quad) from an underline (which only touches its foot) — the first
// version of these tests covered the bar exactly and could distinguish
// nothing.
func barPage(t *testing.T) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	content := w.Add(&reader.Stream{Dict: reader.Dict{},
		Raw: []byte("0 0 0 rg 20 95 60 10 re f")})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
	})
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// withMark is the page bytes with one mark of the kind given over the bar.
func withMark(t *testing.T, k MarkupKind) []byte {
	t.Helper()
	d, err := Open(barPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Mark(Markup{Page: 1, Kind: k, Quads: []Quad{QuadOf(20, 90, 80, 110)}}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// renderOf rasterises a document's first page at one pixel per point.
func renderOf(t *testing.T, pdf []byte) *raster.Image {
	t.Helper()
	src, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	img, err := render.Page(src, 1, render.Options{Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// blank reports whether a pixel is untouched paper.
//
// ⛔⛔ ALL THREE CHANNELS. The first version of these tests asked whether the
// RED channel was 255 — and a yellow mark has r=255, so every test for "is
// there ink here" passed over a highlight it was looking straight at. The
// marks are coloured; a one-channel test cannot see them.
func blank(r, g, b uint8) bool { return r == 255 && g == 255 && b == 255 }

// pixelAt renders a page and returns one pixel, as r, g, b.
func pixelAt(t *testing.T, pdf []byte, x, y int) (uint8, uint8, uint8) {
	t.Helper()
	img := renderOf(t, pdf)
	if x < 0 || y < 0 || x >= img.W || y >= img.H {
		t.Fatalf("(%d,%d) is outside a %dx%d page", x, y, img.W, img.H)
	}
	i := (y*img.W + x) * 4
	return img.Pix[i], img.Pix[i+1], img.Pix[i+2]
}

// ⛔⛔ THE MARK IS VISIBLE, which is the only thing a reader cares about and
// the thing a correct annotation dictionary does NOT guarantee. Table 179 says
// the AP entry "shall take precedence over QuadPoints", and go-pdfkit/render
// goes further: it draws an annotation only from its appearance stream and
// skips one with none. A highlight written with QuadPoints alone is a correct
// annotation nothing shows.
func TestAHighlightIsVisibleOnThePage(t *testing.T) {
	// A PDF counts up from the foot of the page and a raster counts down from
	// its top, so on a page 200 tall the row is 200-y. The bar is y 95..105,
	// which is rows 95..105; row 92 is inside the mark and above the bar.
	plainR, plainG, plainB := pixelAt(t, barPage(t), 50, 100)
	if plainR != 0 || plainG != 0 || plainB != 0 {
		t.Fatalf("precondition: the bar should be black, got %d %d %d", plainR, plainG, plainB)
	}
	// Above the bar and still inside the mark, the page is white.
	if r, g, b := pixelAt(t, barPage(t), 50, 92); !blank(r, g, b) {
		t.Fatalf("precondition: above the bar should be white, got %d %d %d", r, g, b)
	}

	r, g, b := pixelAt(t, withMark(t, MarkHighlight), 50, 92)
	if blank(r, g, b) {
		t.Error("the highlight is not on the page: the paper inside the mark is still white")
	}
	// ⛔ And it is YELLOW, not black: a zero colour must read as the default
	// highlighter rather than as "paint over the words". Translucent over
	// white paper, so it is a pale yellow rather than a saturated one.
	if !(r > 200 && g > 200 && b < 230 && b < g) {
		t.Errorf("the highlight is %d %d %d, which is not a yellow", r, g, b)
	}
}

// ⛔⛔ AND THE WORDS SHOW THROUGH IT. That is what makes a highlighter a
// highlighter rather than a sticker, and it is the Multiply blend that does
// it. Drawn normally, the colour would cover what it marks — a highlight that
// hides the sentence it highlights.
func TestAHighlightDoesNotHideWhatItMarks(t *testing.T) {
	r, g, b := pixelAt(t, withMark(t, MarkHighlight), 50, 100)
	// The bar is black; under a 40% wash it must still be far darker than the
	// paper beside it. A mark that covered it would read as the yellow above.
	if r > 170 || g > 170 || b > 170 {
		t.Errorf("the bar under the highlight is %d %d %d; the mark has covered it", r, g, b)
	}
}

// ⛔ An underline marks the foot of the text and leaves the rest of it alone,
// which is how it differs from a highlight at the dictionary level and at the
// pixel level both.
func TestAnUnderlineMarksOnlyTheFoot(t *testing.T) {
	pdf := withMark(t, MarkUnderline)
	// Row 92 is inside the mark and above the bar: a highlight fills it, an
	// underline must leave it alone.
	if r, g, b := pixelAt(t, pdf, 50, 92); !blank(r, g, b) {
		t.Errorf("an underline coloured the body of the line: %d %d %d", r, g, b)
	}
	// Row 109 is the foot of the mark (y≈91), where the line goes.
	if r, g, b := pixelAt(t, pdf, 50, 109); blank(r, g, b) {
		t.Error("an underline drew nothing at the foot of the line")
	}
}

// ⛔⛔ THE VERTEX ORDER IS THE ONE THE FIGURE SHOWS, NOT THE ONE THE PROSE
// SAYS. Table 179 calls the four vertices "counterclockwise"; its own Figure
// 64 puts (x1,y1) and (x2,y2) along the TOP edge and (x3,y3), (x4,y4) along
// the bottom — a Z. Every widely used reader follows the figure, and a writer
// that follows the sentence produces bow-ties.
func TestTheQuadPointsAreInTheOrderReadersExpect(t *testing.T) {
	q := QuadOf(10, 20, 30, 40)
	got := q.numbers()
	want := []float64{
		10, 40, // top-left
		30, 40, // top-right
		10, 20, // bottom-left
		30, 20, // bottom-right
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("QuadPoints are %v, want %v", got, want)
		}
	}
	// And QuadOf does not care which corner it was given.
	if QuadOf(30, 40, 10, 20) != q {
		t.Error("a rectangle given from the far corner came back different")
	}
}

// ⛔ A markup with no quadrilaterals marks NOTHING, and QuadPoints is required
// by Table 179 — so it is refused before the document is written rather than
// written as an annotation with something missing.
func TestAMarkThatCoversNothingIsRefused(t *testing.T) {
	d, err := Open(barPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Mark(Markup{Page: 1}); err == nil {
		t.Error("a mark with no quadrilaterals was accepted")
	}
	for _, page := range []int{0, -1, 2} {
		if err := d.Mark(Markup{Page: page, Quads: []Quad{QuadOf(0, 0, 1, 1)}}); err == nil {
			t.Errorf("a mark on page %d of a one-page document was accepted", page)
		}
	}
}

// What the annotation says about itself, read back out of the written file.
func TestTheAnnotationSaysWhatItIs(t *testing.T) {
	was := now
	now = func() time.Time { return time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { now = was })

	d, err := Open(barPage(t))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Mark(Markup{
		Page: 1, Quads: []Quad{QuadOf(20, 90, 80, 110)},
		Author: "A reader", Note: "worth coming back to",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	page, err := src.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := src.Resolve(page.Get("Annots"))
	annots, _ := reader.ToArray(list)
	if len(annots) != 1 {
		t.Fatalf("the page carries %d annotations", len(annots))
	}
	dict, _ := src.GetDict(reader.Dict{"a": annots[0]}, "a")
	if sub, _ := reader.ToName(dict.Get("Subtype")); sub != "Highlight" {
		t.Errorf("the annotation is a %q", sub)
	}
	// ⛔ It PRINTS. An annotation with no flags shows on screen and does not
	// print, which makes a highlight that vanishes the moment somebody prints
	// what they marked up.
	if f, _ := reader.ToInt(dict.Get("F")); f&annotPrint == 0 {
		t.Errorf("the annotation's flags are %d, so it would not print", f)
	}
	if _, ok := dict["AP"]; !ok {
		t.Error("the annotation has no appearance, so nothing will show it")
	}
	if quads, _ := reader.ToArray(dict.Get("QuadPoints")); len(quads) != 8 {
		t.Errorf("the annotation carries %d QuadPoints numbers, want 8", len(quads))
	}
	if s, _ := reader.ToString(dict.Get("T")); string(s) != "A reader" {
		t.Errorf("the author reads %q", s)
	}
	if s, _ := reader.ToString(dict.Get("Contents")); string(s) != "worth coming back to" {
		t.Errorf("the note reads %q", s)
	}
	if s, _ := reader.ToString(dict.Get("M")); !strings.HasPrefix(string(s), "D:20261107093000") {
		t.Errorf("the date reads %q", s)
	}
}

// ⛔ THE PAGE'S OWN CONTENT IS NOT TOUCHED. That is the whole reason to write
// an annotation rather than draw on the page: dropping the annotation gives
// the document back exactly as its author wrote it.
func TestMarkingDoesNotChangeThePage(t *testing.T) {
	plain, err := reader.Open(barPage(t))
	if err != nil {
		t.Fatal(err)
	}
	marked, err := reader.Open(withMark(t, MarkHighlight))
	if err != nil {
		t.Fatal(err)
	}
	before, err := plain.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	after, err := marked.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	ao, _ := plain.Resolve(before.Get("Contents"))
	bo, _ := marked.Resolve(after.Get("Contents"))
	a, aok := reader.ToStream(ao)
	b, bok := reader.ToStream(bo)
	if !aok || !bok {
		t.Fatal("a page lost its content stream")
	}
	if string(a.Raw) != string(b.Raw) {
		t.Errorf("the page's content changed:\n%q\n%q", a.Raw, b.Raw)
	}
}

// Every kind writes its own subtype, and a kind nobody named is a highlight.
func TestEveryKindHasItsSubtype(t *testing.T) {
	for k, want := range map[MarkupKind]reader.Name{
		MarkHighlight:  "Highlight",
		MarkUnderline:  "Underline",
		MarkStrikeOut:  "StrikeOut",
		MarkSquiggly:   "Squiggly",
		MarkupKind(99): "Highlight",
	} {
		if got := k.subtype(); got != want {
			t.Errorf("kind %d is %q, want %q", k, got, want)
		}
	}
}

// ⛔ A date carries its offset from UTC, with its SIGN. Half the world is west
// of Greenwich, and a reader there would otherwise have every mark stamped as
// though it were made in the wrong place — ISO 32000-1, 7.9.4 gives the form
// D:YYYYMMDDHHmmSSOHH'mm, where O is +, - or Z.
func TestADateCarriesItsOffsetEitherWay(t *testing.T) {
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.FixedZone("CET", 3600))
	if got := pdfDate(at); got != "D:20261107093000+01'00" {
		t.Errorf("east of Greenwich reads %q", got)
	}
	west := time.Date(2026, 11, 7, 9, 30, 0, 0, time.FixedZone("EST", -5*3600-1800))
	if got := pdfDate(west); got != "D:20261107093000-05'30" {
		t.Errorf("west of Greenwich reads %q", got)
	}
	utc := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	if got := pdfDate(utc); got != "D:20261107093000+00'00" {
		t.Errorf("at Greenwich reads %q", got)
	}
}

// A colour the caller chose is the colour that is written, and one out of
// range is brought back rather than refused: a highlight is not worth failing
// a document over.
func TestAChosenColourIsKept(t *testing.T) {
	m := Markup{R: 0.2, G: 0.4, B: 0.6}
	if r, g, b := m.colour(); r != 0.2 || g != 0.4 || b != 0.6 {
		t.Errorf("got %v %v %v", r, g, b)
	}
	out := Markup{R: 5, G: -1, B: 0.5}
	if r, g, b := out.colour(); r != 1 || g != 0 || b != 0.5 {
		t.Errorf("an out-of-range colour came back %v %v %v", r, g, b)
	}
}

// A strikeout goes through the middle and a squiggly draws a line rather than
// a block: both are visible, and neither is the highlight's rectangle.
func TestStrikeOutAndSquigglyAreDrawn(t *testing.T) {
	for _, c := range []struct {
		kind MarkupKind
		name string
		y    int
	}{
		// The squiggly zigzags along the foot of the mark.
		{MarkSquiggly, "squiggly", 109},
	} {
		if r, g, b := pixelAt(t, withMark(t, c.kind), 50, c.y); blank(r, g, b) {
			t.Errorf("a %s drew nothing at row %d", c.name, c.y)
		}
	}
	// ⛔ A strikeout goes through the MIDDLE, which on this page is where the
	// bar already is — so "is there ink" cannot see it. What distinguishes it
	// is that it leaves the body of the line alone: row 92 is inside the mark,
	// above the bar, and a strikeout must not have coloured it, where a
	// highlight would.
	if r, g, b := pixelAt(t, withMark(t, MarkStrikeOut), 50, 92); !blank(r, g, b) {
		t.Errorf("a strikeout filled the body of the line: %d %d %d", r, g, b)
	}
	// And it did draw: over the black bar its own colour lightens the pixel.
	if r, g, b := pixelAt(t, withMark(t, MarkStrikeOut), 50, 100); r == 0 && g == 0 && b == 0 {
		t.Error("a strikeout drew nothing through the middle of the line")
	}
}
