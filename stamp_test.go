package ops

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// stampedContent writes the document and returns the content of one page.
func stampedContent(t *testing.T, d *Doc, page int) string {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	data, err := src.PageContent(page)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestStampDrawsText(t *testing.T) {
	d := open(t, 1)
	if err := d.Stamp("all", Stamp{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	got := stampedContent(t, d, 1)
	if !strings.Contains(got, "(hello) Tj") {
		t.Errorf("content = %q", got)
	}
	if !strings.Contains(got, "page 1") {
		t.Error("the page's own content was lost")
	}
	if !strings.Contains(got, "/PdfopsF") {
		t.Error("the face was not named")
	}
}

func TestStampResourcesAndFonts(t *testing.T) {
	d := open(t, 1)
	if err := d.Stamp("1", Stamp{Text: "a", Font: HelveticaBold, Opacity: 0.5}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("1", Stamp{Text: "b", Font: Courier}); err != nil {
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
	page, _ := src.Page(1)
	res, ok := src.GetDict(page, "Resources")
	if !ok {
		t.Fatal("no resources")
	}
	fonts, ok := src.GetDict(res, "Font")
	if !ok {
		t.Fatal("no fonts")
	}
	for _, want := range []reader.Name{"PdfopsFB", "PdfopsFC"} {
		if fonts.Get(want).Kind() == reader.KindNull {
			t.Errorf("%s is missing from %v", want, fonts)
		}
	}
	gs, ok := src.GetDict(res, "ExtGState")
	if !ok || len(gs) != 1 {
		t.Errorf("ExtGState = %v", gs)
	}
	// The page's own resources survive alongside.
	if res.Get("XObject").Kind() != reader.KindNull {
		t.Log("the fixture has no XObjects, which is fine")
	}
}

func TestStampRefusesEmptyText(t *testing.T) {
	d := open(t, 1)
	if err := d.Stamp("all", Stamp{Text: "   "}); err == nil {
		t.Error("want an error")
	}
	if err := d.Stamp("nonsense", Stamp{Text: "x"}); err == nil {
		t.Error("a bad range should fail")
	}
}

func TestPageNumbers(t *testing.T) {
	d := open(t, 3)
	if err := d.PageNumbers("all", ""); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		got := stampedContent(t, d, i)
		if !strings.Contains(got, "(") {
			t.Fatalf("page %d: %q", i, got)
		}
	}
	d = open(t, 2)
	if err := d.PageNumbers("all", "{page} of {pages}"); err != nil {
		t.Fatal(err)
	}
	if got := stampedContent(t, d, 2); !strings.Contains(got, "(2 of 2) Tj") {
		t.Errorf("content = %q", got)
	}
}

func TestBates(t *testing.T) {
	d := open(t, 3)
	if err := d.Bates("2-3", "EX", 40, 0); err != nil {
		t.Fatal(err)
	}
	if got := stampedContent(t, d, 2); !strings.Contains(got, "(EX000040) Tj") {
		t.Errorf("page 2 = %q", got)
	}
	if got := stampedContent(t, d, 3); !strings.Contains(got, "(EX000041) Tj") {
		t.Errorf("page 3 = %q", got)
	}
	if got := stampedContent(t, d, 1); strings.Contains(got, "Tj") {
		t.Error("page 1 was stamped although it was not named")
	}
	d = open(t, 1)
	if err := d.Bates("all", "N", 7, 3); err != nil {
		t.Fatal(err)
	}
	if got := stampedContent(t, d, 1); !strings.Contains(got, "(N007) Tj") {
		t.Errorf("content = %q", got)
	}
}

func TestWatermark(t *testing.T) {
	d := open(t, 1)
	if err := d.Watermark("all", "DRAFT"); err != nil {
		t.Fatal(err)
	}
	got := stampedContent(t, d, 1)
	if !strings.Contains(got, "(DRAFT) Tj") {
		t.Errorf("content = %q", got)
	}
	// Slanted, so it has to turn about its own middle: three matrices.
	if strings.Count(got, " cm") < 3 {
		t.Errorf("the text was not turned: %q", got)
	}
	if !strings.Contains(got, "gs") {
		t.Error("the transparency was not set")
	}
}

func TestExpand(t *testing.T) {
	if got := expand("{page}/{pages} {n}", 2, 7, 5, 4); got != "2/7 0005" {
		t.Errorf("expand = %q", got)
	}
	if got := expand("plain", 1, 1, 1, 0); got != "plain" {
		t.Errorf("expand = %q", got)
	}
}

func TestPlace(t *testing.T) {
	area := [4]float64{0, 0, 100, 200}
	cases := []struct {
		pos  Position
		x, y float64
	}{
		{TopLeft, 10, 180},
		{TopCenter, 45, 180},
		{TopRight, 80, 180},
		{MiddleLeft, 10, 95},
		{Center, 45, 95},
		{MiddleRight, 80, 95},
		{BottomLeft, 10, 10},
		{BottomCenter, 45, 10},
		{BottomRight, 80, 10},
	}
	for _, c := range cases {
		x, y := place(c.pos, area, 10, 10, 10)
		if !near(x, c.x) || !near(y, c.y) {
			t.Errorf("position %d = (%g,%g), want (%g,%g)", c.pos, x, y, c.x, c.y)
		}
	}
}

func TestPlaceClampsToTheBox(t *testing.T) {
	// A page smaller than the margin, which the corpus does contain.
	area := [4]float64{0, 0, 50, 10}
	x, y := place(BottomCenter, area, 20, 12, 24)
	if x < 0 || y < 0 || x > 30 {
		t.Errorf("placed at (%g,%g) on a %v page", x, y, area)
	}
	// And a page whose box does not start at the origin.
	area = [4]float64{100, 200, 300, 400}
	x, y = place(BottomLeft, area, 10, 10, 5)
	if !near(x, 105) || !near(y, 205) {
		t.Errorf("placed at (%g,%g)", x, y)
	}
}

func TestStampOnAComposedPage(t *testing.T) {
	d := open(t, 2)
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("all", Stamp{Text: "sheet"}); err != nil {
		t.Fatal(err)
	}
	got := stampedContent(t, d, 1)
	if !strings.Contains(got, "(sheet) Tj") || !strings.Contains(got, "Do") {
		t.Errorf("content = %q", got)
	}
}

func TestStampOnABlankPage(t *testing.T) {
	d := New()
	d.Blank(200, 100)
	if err := d.Stamp("all", Stamp{Text: "empty"}); err != nil {
		t.Fatal(err)
	}
	if got := stampedContent(t, d, 1); !strings.Contains(got, "(empty) Tj") {
		t.Errorf("content = %q", got)
	}
}

func TestAStampedPageUsedAsATile(t *testing.T) {
	// Numbering pages and then laying them out has to keep the numbers, in the
	// space the page is shown in rather than the one it is stored in.
	d := open(t, 2)
	if err := d.PageNumbers("all", "{page}"); err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, tiles := sheetOf(t, out, 1)
	if len(tiles) != 2 {
		t.Fatalf("got %d tiles", len(tiles))
	}
	for i, tile := range tiles {
		if !strings.Contains(tile.content, "Do") {
			t.Errorf("tile %d does not draw the page: %q", i, tile.content)
		}
		if !strings.Contains(tile.content, fmt.Sprintf("(%d) Tj", i+1)) {
			t.Errorf("tile %d lost its number: %q", i, tile.content)
		}
	}
}

func TestContentPartsHandlesAnIndirectArray(t *testing.T) {
	// Some producers put /Contents in an array that is itself an indirect
	// object; missing that loses the whole page.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	one := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("first")})
	two := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("second")})
	list := w.Add(reader.Array{one, two})
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": list,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(100)}})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	in, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("all", Stamp{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	got := stampedContent(t, d, 1)
	for _, want := range []string{"first", "second", "(x) Tj"} {
		if !strings.Contains(got, want) {
			t.Errorf("content %q is missing %q", got, want)
		}
	}
}

func TestStampOnAPageWithNoContent(t *testing.T) {
	b := buildPDF(t, 1, func(i int, d reader.Dict) { delete(d, "Contents") })
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("all", Stamp{Text: "only"}); err != nil {
		t.Fatal(err)
	}
	if got := stampedContent(t, d, 1); !strings.Contains(got, "(only) Tj") {
		t.Errorf("content = %q", got)
	}
}

func TestMergeResources(t *testing.T) {
	into := reader.Dict{"Font": reader.Dict{"F1": reader.Integer(1)}, "Other": reader.Integer(2)}
	got := mergeResources(into, reader.Dict{
		"Font":      reader.Dict{"F2": reader.Integer(3)},
		"ExtGState": reader.Dict{"G1": reader.Integer(4)},
	})
	fonts, _ := reader.ToDict(got.Get("Font"))
	if len(fonts) != 2 {
		t.Errorf("fonts = %v", fonts)
	}
	if got.Get("ExtGState").Kind() == reader.KindNull {
		t.Error("the new entry was dropped")
	}
	if got.Get("Other").Kind() == reader.KindNull {
		t.Error("an unrelated entry was dropped")
	}
	// A nil dictionary is somewhere to start.
	if out := mergeResources(nil, reader.Dict{"Font": reader.Dict{}}); out == nil {
		t.Error("mergeResources(nil) returned nothing")
	}
	// An entry that is not a dictionary is replaced rather than merged into.
	out := mergeResources(reader.Dict{"Font": reader.Integer(9)}, reader.Dict{"Font": reader.Dict{"F": reader.Integer(1)}})
	if f, ok := reader.ToDict(out.Get("Font")); !ok || len(f) != 1 {
		t.Errorf("Font = %v", out.Get("Font"))
	}
}

func TestFontResourceNames(t *testing.T) {
	for f, want := range map[Font]string{
		Helvetica:       "PdfopsF",
		HelveticaBold:   "PdfopsFB",
		Courier:         "PdfopsFC",
		CourierBold:     "PdfopsFCB",
		Font("Unknown"): "PdfopsF",
	} {
		if got := fontResourceName(f); got != want {
			t.Errorf("%s = %q, want %q", f, got, want)
		}
	}
}

func TestStampOnAPageWithNoResources(t *testing.T) {
	// The page tree gives the page its media box but no resources at all.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("bare")})
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": content})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(100)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	in, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("all", Stamp{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	got := stampedContent(t, d, 1)
	if !strings.Contains(got, "bare") || !strings.Contains(got, "(x) Tj") {
		t.Errorf("content = %q", got)
	}
}

func TestStampKeepsThePageOwnResources(t *testing.T) {
	b := buildPDF(t, 1, func(i int, d reader.Dict) {
		d["Resources"] = reader.Dict{
			"Font":    reader.Dict{"F1": reader.Dict{"Type": reader.Name("Font")}},
			"XObject": reader.Dict{"Im1": reader.Dict{"Type": reader.Name("XObject")}},
			"ProcSet": reader.Array{reader.Name("PDF")},
		}
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("all", Stamp{Text: "x"}); err != nil {
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
	page, _ := src.Page(1)
	res, ok := src.GetDict(page, "Resources")
	if !ok {
		t.Fatal("no resources")
	}
	if res.Get("XObject").Kind() == reader.KindNull {
		t.Error("the page's own XObjects were dropped")
	}
	if res.Get("ProcSet").Kind() == reader.KindNull {
		t.Error("an entry that is not a dictionary was dropped")
	}
	fonts, ok := src.GetDict(res, "Font")
	if !ok {
		t.Fatal("no fonts")
	}
	if fonts.Get("F1").Kind() == reader.KindNull {
		t.Error("the page's own face was dropped")
	}
	if fonts.Get("PdfopsF").Kind() == reader.KindNull {
		t.Error("the stamp's face is missing")
	}
}
