package ops

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// What a damaged or hostile file may do to the outliner: each case here is a
// refusal or a fallback, and each says what it expects, so that a path that
// stops being taken is a failing test and not an uncovered line.

// garbage is a stream whose filter cannot decode it.
func garbage(w *reader.Writer) reader.Object {
	return w.Add(&reader.Stream{Dict: reader.Dict{"Filter": reader.Name("FlateDecode")},
		Raw: []byte("this is not zlib")})
}

func outlineErr(t *testing.T, src []byte, spec string) error {
	t.Helper()
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.OutlineText(spec); err != nil {
		t.Fatal(err)
	}
	_, err = d.Bytes()
	return err
}

func TestAPageMadeHereIsLeftAlone(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 12 Tf (x) Tj ET", stdFont, nil)
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	d.Blank(100, 100)
	if err := d.OutlineText("1-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Bytes(); err != nil {
		t.Fatal(err)
	}
}

func TestAnUnreadablePageIsRefusedNotWrittenWithItsText(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 12 Tf (x) Tj ET", stdFont, nil)
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.OutlineText("1"); err != nil {
		t.Fatal(err)
	}
	was := pageContent
	pageContent = func(*reader.Document, int) ([]byte, error) { return nil, errTest }
	t.Cleanup(func() { pageContent = was })
	_, err = d.Bytes()
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheByteBoundsRefuseBothWays(t *testing.T) {
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	// Reading: more content than the bound.
	maxOutlineBytes = 10
	src := buildDoc(t, 1, "0 0 10 10 re f 0 0 10 10 re f", stdFont, nil)
	if err := outlineErr(t, src, "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("read: err = %v", err)
	}
	// Writing: each glyph fits, the page does not.
	maxOutlineBytes = 300
	src = buildDoc(t, 1, "BT /F1 9 Tf (.) Tj (.) Tj (.) Tj (.) Tj (.) Tj (.) Tj (.) Tj (.) Tj ET", stdFont, nil)
	if err := outlineErr(t, src, "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("write: err = %v", err)
	}
}

func TestAGlyphBoundInsideAnArrayIsRefused(t *testing.T) {
	defer func(v int) { maxOutlineGlyphs = v }(maxOutlineGlyphs)
	maxOutlineGlyphs = 2
	src := buildDoc(t, 1, "BT /F1 9 Tf [(AAA) -20 (B)] TJ ET", stdFont, nil)
	if err := outlineErr(t, src, "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("err = %v", err)
	}
}

func TestAGraphicsStateCannotBringAFontBack(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		face := res["Font"].(reader.Dict)["F1"]
		res["ExtGState"] = reader.Dict{
			"G0": reader.Dict{"Type": reader.Name("ExtGState"), "LW": reader.Integer(2),
				"Font": reader.Array{face, reader.Integer(12)}},
			"G1": reader.Integer(5), // not a dictionary: copied as it is
		}
	}
	src := buildDoc(t, 1, "/G0 gs BT /F1 12 Tf 5 5 Td (x) Tj ET", build, nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("the font came back through the graphics state")
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	pg, _ := d.Page(1)
	res, _ := d.GetDict(pg, "Resources")
	gs, _ := d.GetDict(res, "ExtGState")
	g0, ok := d.GetDict(gs, "G0")
	if !ok || g0["Font"] != nil || g0["LW"] == nil {
		t.Errorf("G0 = %v, want LW kept and Font dropped", g0)
	}
}

func TestWhatIsNotAFormOrATilingPatternIsCopied(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		// Two references that name each other: no object to resolve to.
		a, b := w.Reserve(), w.Reserve()
		w.Put(a, b)
		w.Put(b, a)
		var loop reader.Object = a
		res["XObject"] = reader.Dict{
			"N": reader.Integer(1), // not a stream
			"I": w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Image"),
				"Width": reader.Integer(1), "Height": reader.Integer(1),
				"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8)},
				Raw: []byte{0}}),
			"Dangling": reader.Ref{Num: 99999},
			"Loop":     loop,
		}
		res["Pattern"] = reader.Dict{
			"Shading": reader.Dict{"PatternType": reader.Integer(2)},
			"Odd": w.Add(&reader.Stream{Dict: reader.Dict{"PatternType": reader.Integer(2)},
				Raw: []byte("0 0 1 1 re f")}),
		}
	}
	src := buildDoc(t, 1, "BT /F1 12 Tf 5 5 Td (x) Tj ET", build, nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("a font remains")
	}
}

func TestAFormWithoutResourcesUsesThePages(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["XObject"] = reader.Dict{"Fm": w.Add(&reader.Stream{
			Dict: reader.Dict{"Subtype": reader.Name("Form"),
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(200), reader.Integer(100)}},
			Raw: []byte("BT /F1 12 Tf 5 5 Td (x) Tj ET")})}
	}
	src := buildDoc(t, 1, "/Fm Do", build, nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("the form's text was not outlined with the page's font")
	}
	if ink, _ := compared(t, shot(t, src, 1), shot(t, out, 1)); ink == 0 {
		t.Error("nothing drawn")
	}
}

func TestAnUnreadableFormOrPatternIsRefused(t *testing.T) {
	form := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["XObject"] = reader.Dict{"Fm": w.Add(&reader.Stream{
			Dict: reader.Dict{"Subtype": reader.Name("Form"), "Filter": reader.Name("FlateDecode"),
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(9), reader.Integer(9)}},
			Raw: []byte("not zlib")})}
	}
	pattern := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["Pattern"] = reader.Dict{"P": w.Add(&reader.Stream{
			Dict: reader.Dict{"PatternType": reader.Integer(1), "Filter": reader.Name("FlateDecode")},
			Raw:  []byte("not zlib")})}
	}
	for name, build := range map[string]func(*reader.Writer, reader.Dict){"form": form, "pattern": pattern} {
		src := buildDoc(t, 1, "/Fm Do", build, nil)
		if err := outlineErr(t, src, "1"); err == nil || !strings.Contains(err.Error(), "could not be read") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestOddOperandsAndModesDoNotBreakTheOutliner(t *testing.T) {
	// An out-of-range mode paints as fill; a number past a float is no number.
	content := "BT /F1 12 Tf 9 Tr 5 5 Td (x) Tj " + strings.Repeat("9", 400) + " Tc /Name Tw (y) Tj ET"
	src := buildDoc(t, 1, content, stdFont, nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("a font remains")
	}
}

// type3 with two glyphs' worth of trouble: B has no procedure, A's procedure
// sets a shape and a colour.
func TestATypeThreeGlyphWithoutAProcedureIsSkipped(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 40 Tf 10 20 Td (BA) Tj ET",
		type3Font("1000 0 0 0 1000 1000 d1 1 0 0 rg 0 0 500 500 re f"), nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("a font remains")
	}
}

func TestATypeThreeGlyphThatCannotBeReadOrNestedIsRefused(t *testing.T) {
	broken := func(w *reader.Writer, res reader.Dict) {
		type3Font("")(w, res)
		fonts := res["Font"].(reader.Dict)
		f := fonts["F1"].(reader.Ref)
		_ = f
		fonts["F1"] = w.Add(reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("Type3"),
			"FontBBox":   reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
			"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0), reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
			"CharProcs":  reader.Dict{"tri": garbage(w)},
			"Encoding": reader.Dict{"Type": reader.Name("Encoding"),
				"Differences": reader.Array{reader.Integer(65), reader.Name("tri")}},
			"FirstChar": reader.Integer(65), "LastChar": reader.Integer(65),
			"Widths": reader.Array{reader.Integer(1000)}})
	}
	src := buildDoc(t, 1, "BT /F1 40 Tf (A) Tj ET", broken, nil)
	if err := outlineErr(t, src, "1"); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("unreadable: err = %v", err)
	}

	defer func(v int) { maxOutlineNesting = v }(maxOutlineNesting)
	maxOutlineNesting = 0
	src = buildDoc(t, 1, "BT /F1 40 Tf (A) Tj ET", type3Font("1000 0 d0 0 0 5 5 re f"), nil)
	if err := outlineErr(t, src, "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("nested: err = %v", err)
	}
}

func TestWhatIsNotAnOutlineableThingIsNotDecoded(t *testing.T) {
	// An image or a non-tiling pattern is copied, never decoded: a bomb in an
	// image must not be inflated to be looked at for text it cannot hold.
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["XObject"] = reader.Dict{"I": w.Add(&reader.Stream{Dict: reader.Dict{
			"Subtype": reader.Name("Image"), "Filter": reader.Name("FlateDecode"),
			"Width": reader.Integer(1), "Height": reader.Integer(1)}, Raw: []byte("not zlib")})}
		res["Pattern"] = reader.Dict{"P": w.Add(&reader.Stream{Dict: reader.Dict{
			"PatternType": reader.Integer(2), "Filter": reader.Name("FlateDecode")}, Raw: []byte("not zlib")})}
	}
	src := buildDoc(t, 1, "BT /F1 12 Tf (x) Tj ET", build, nil)
	if err := outlineErr(t, src, "1"); err != nil {
		t.Fatalf("an image was decoded: %v", err)
	}
}

func TestTheRefusalsSaySWhichBoundWasPast(t *testing.T) {
	d, _ := Open(buildDoc(t, 1, "", stdFont, nil))
	if err := d.OutlineText("nonsense"); err == nil || strings.Contains(err.Error(), "names no pages") {
		t.Errorf("a bad range was reported as an empty one: %v", err)
	}
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	maxOutlineBytes = 10
	src := buildDoc(t, 1, "0 0 10 10 re f 0 0 10 10 re f", stdFont, nil)
	if err := outlineErr(t, src, "1"); err == nil || !strings.Contains(err.Error(), "of content") {
		t.Errorf("err = %v, want the content bound", err)
	}
}

func TestAnEmptyStringDoesNotForgetAnError(t *testing.T) {
	defer func(v int) { maxOutlineGlyphs = v }(maxOutlineGlyphs)
	maxOutlineGlyphs = 2
	src := buildDoc(t, 1, "BT /F1 9 Tf [(AAA) ()] TJ ET", stdFont, nil)
	if err := outlineErr(t, src, "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("err = %v", err)
	}
}

func TestTheOutputBoundStopsTheWorkEarly(t *testing.T) {
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	maxOutlineBytes = 3000
	src := buildDoc(t, 1, "BT /F1 9 Tf ("+strings.Repeat("A", 2000)+") Tj ET", stdFont, nil)
	d, _ := Open(src)
	_ = d.OutlineText("1")
	if _, err := d.Bytes(); !errors.Is(err, ErrOutlineTooLarge) {
		t.Fatalf("err = %v", err)
	}
	// The outliners survive a failed write, and the counter says where it stopped.
	for _, o := range d.outliners {
		if o.glyphs > 100 {
			t.Errorf("%d glyphs were drawn after the bound was past", o.glyphs)
		}
	}
	if len(d.outliners) == 0 {
		t.Error("no outliner to read the count from")
	}
}

func TestAGlyphWithoutAnOutlineDrawsNothing(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 12 Tf (   ) Tj ET", stdFont, nil)
	out := outlinedBytes(t, src, "1")
	if c := pageContentOf(t, out, 1); strings.Contains(c, "f") {
		t.Errorf("a glyph with no outline painted something: %q", c)
	}
}

func TestNumbersRefusesWhatIsNotANumber(t *testing.T) {
	if _, ok := numbers([]reader.Object{reader.Name("x")}, 1); ok {
		t.Error("a name was a number")
	}
	if _, ok := numbers([]reader.Object{reader.Real(math.NaN())}, 1); ok {
		t.Error("NaN was a number")
	}
	if _, ok := numbers([]reader.Object{reader.Real(math.Inf(1))}, 1); ok {
		t.Error("Inf was a number")
	}
	if v, ok := numbers([]reader.Object{reader.Integer(7), reader.Real(2.5)}, 2); !ok || v[0] != 7 || v[1] != 2.5 {
		t.Errorf("numbers = %v, %v", v, ok)
	}
}

func TestWhatHasNothingToDrawIsLeftWithoutPaths(t *testing.T) {
	// No font selected, a TJ without an array, a Td without its operands: each
	// is skipped, and none of them paints.
	for _, content := range []string{
		"BT (x) Tj ET",
		"BT /F1 12 Tf TJ ET",
		"BT /F1 12 Tf 5 Td (   ) Tj ET",
	} {
		out := outlinedBytes(t, buildDoc(t, 1, content, stdFont, nil), "1")
		if c := pageContentOf(t, out, 1); strings.Contains(c, "f\n") || strings.Contains(c, " f ") {
			t.Errorf("%q painted: %q", content, c)
		}
	}
	if _, ok := numbers(nil, 1); ok {
		t.Error("no operands were a number")
	}
	if _, ok := numbers([]reader.Object{reader.Integer(1)}, 2); ok {
		t.Error("one operand were two numbers")
	}
}

func TestATypeThreeGlyphWithoutAProcedureDrawsNoForm(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 40 Tf (BA) Tj ET", type3Font("1000 0 d0 0 0 5 5 re f"), nil)
	out := outlinedBytes(t, src, "1")
	if n := strings.Count(pageContentOf(t, out, 1), " Do"); n != 1 {
		t.Errorf("%d glyph forms drawn for one glyph with a procedure", n)
	}
}

func TestAnEntryThatIsNotAStreamIsCopiedAsItIs(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["XObject"] = reader.Dict{"N": reader.Integer(1)}
	}
	out := outlinedBytes(t, buildDoc(t, 1, "BT /F1 12 Tf (x) Tj ET", build, nil), "1")
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	pg, _ := d.Page(1)
	res, _ := d.GetDict(pg, "Resources")
	xo, _ := d.GetDict(res, "XObject")
	if n, _ := reader.ToInt(xo["N"]); n != 1 {
		t.Errorf("XObject N = %v, want 1", xo["N"])
	}
}
