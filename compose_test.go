package ops

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/go-pdfkit/reader"
)

// sheetOf reads back one written sheet: its media box and, for every form it
// draws, the matrix that places it and the content it carries.
type placed struct {
	matrix  [6]float64
	content string
	bbox    [4]float64
}

func sheetOf(t *testing.T, b []byte, page int) ([4]float64, []placed) {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := d.Page(page)
	if err != nil {
		t.Fatal(err)
	}
	var mb [4]float64
	arr, ok := reader.ToArray(pg.Get("MediaBox"))
	if !ok || len(arr) != 4 {
		t.Fatalf("page %d has no media box", page)
	}
	for i := range mb {
		mb[i], _ = reader.ToFloat(arr[i])
	}
	content, err := d.PageContent(page)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := reader.Operations(content)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := d.GetDict(pg, "Resources")
	xo, _ := d.GetDict(res, "XObject")

	var out []placed
	var matrix [6]float64
	for _, op := range ops {
		switch op.Operator {
		case "cm":
			for i := 0; i < 6 && i < len(op.Operands); i++ {
				matrix[i], _ = reader.ToFloat(op.Operands[i])
			}
		case "Do":
			name, _ := reader.ToName(op.Operands[0])
			o, err := d.Resolve(xo.Get(name))
			if err != nil {
				t.Fatal(err)
			}
			s, ok := reader.ToStream(o)
			if !ok {
				t.Fatalf("%s is not a stream", name)
			}
			data, _, err := d.DecodeStream(s)
			if err != nil {
				t.Fatal(err)
			}
			var bbox [4]float64
			if arr, ok := reader.ToArray(s.Dict.Get("BBox")); ok && len(arr) == 4 {
				for i := range bbox {
					bbox[i], _ = reader.ToFloat(arr[i])
				}
			}
			out = append(out, placed{matrix: matrix, content: string(data), bbox: bbox})
		}
	}
	return mb, out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestNUpLaysPagesOut(t *testing.T) {
	d, err := Open(simple(t, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(4); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d", got)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// The source pages are 100 by 200, so a two-by-two grid of 50 by 100 cells
	// takes each page at half size with no room to spare.
	mb, tiles := sheetOf(t, out, 1)
	if mb != [4]float64{0, 0, 100, 200} {
		t.Errorf("sheet = %v", mb)
	}
	if len(tiles) != 4 {
		t.Fatalf("got %d tiles", len(tiles))
	}
	want := [][2]float64{{0, 100}, {50, 100}, {0, 0}, {50, 0}}
	for i, tile := range tiles {
		if tile.content != fmt.Sprintf("page %d", i+1) {
			t.Errorf("tile %d carries %q", i, tile.content)
		}
		if !near(tile.matrix[0], 0.5) || !near(tile.matrix[3], 0.5) {
			t.Errorf("tile %d scale = %v", i, tile.matrix)
		}
		if !near(tile.matrix[4], want[i][0]) || !near(tile.matrix[5], want[i][1]) {
			t.Errorf("tile %d at (%g,%g), want %v", i, tile.matrix[4], tile.matrix[5], want[i])
		}
	}
}

func TestNUpReadingOrderRunsDownThePage(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
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
	// Tall pages on a tall sheet: one above the other, page one on top.
	if tiles[0].matrix[5] <= tiles[1].matrix[5] {
		t.Errorf("page 1 is not above page 2: %v then %v", tiles[0].matrix, tiles[1].matrix)
	}
}

func TestNUpLastSheetIsShort(t *testing.T) {
	d, err := Open(simple(t, 5))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 3 {
		t.Fatalf("PageCount() = %d", got)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, tiles := sheetOf(t, out, 3); len(tiles) != 1 {
		t.Errorf("the last sheet has %d tiles", len(tiles))
	}
}

func TestNUpOfOneChangesNothing(t *testing.T) {
	d, err := Open(simple(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(1); err != nil {
		t.Fatal(err)
	}
	if got := written(t, d); !equal(got, pages(1, 2, 3)) {
		t.Errorf("pages = %v", got)
	}
}

func TestNUpRefusesNonsense(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(0); err == nil {
		t.Error("zero pages to a sheet should fail")
	}
	if err := New().NUp(2); err == nil {
		t.Error("an empty document should fail")
	}
	if err := New().Booklet(); err == nil {
		t.Error("an empty document should fail")
	}
}

func TestBookletOrder(t *testing.T) {
	cases := map[int][]int{
		4: {4, 1, 2, 3},
		8: {8, 1, 2, 7, 6, 3, 4, 5},
		3: {4, 1, 2, 3},
		5: {8, 1, 2, 7, 6, 3, 4, 5},
	}
	for n, want := range cases {
		got := bookletOrder(n)
		if len(got) != len(want) {
			t.Errorf("%d pages: got %v", n, got)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%d pages: got %v, want %v", n, got, want)
				break
			}
		}
	}
}

func TestBooklet(t *testing.T) {
	d, err := Open(simple(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Booklet(); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 2 {
		t.Fatalf("PageCount() = %d", got)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// Two 100-by-200 pages side by side make a 200-by-200 sheet.
	mb, tiles := sheetOf(t, out, 1)
	if mb != [4]float64{0, 0, 200, 200} {
		t.Errorf("sheet = %v", mb)
	}
	if len(tiles) != 2 {
		t.Fatalf("got %d tiles", len(tiles))
	}
	// The first sheet of a four-page booklet carries page four then page one;
	// page four does not exist here, so it is blank.
	if tiles[0].content != "" {
		t.Errorf("the blank tile carries %q", tiles[0].content)
	}
	if tiles[1].content != "page 1" {
		t.Errorf("second tile carries %q", tiles[1].content)
	}
	_, second := sheetOf(t, out, 2)
	if second[0].content != "page 2" || second[1].content != "page 3" {
		t.Errorf("second sheet carries %q then %q", second[0].content, second[1].content)
	}
}

func TestOverlayAndUnderlay(t *testing.T) {
	base, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	mark, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Overlay(mark); err != nil {
		t.Fatal(err)
	}
	out, err := base.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	mb, tiles := sheetOf(t, out, 1)
	if mb != [4]float64{0, 0, 100, 200} {
		t.Errorf("page = %v", mb)
	}
	if len(tiles) != 2 || tiles[0].content != "page 1" || tiles[1].content != "page 1" {
		t.Fatalf("tiles = %+v", tiles)
	}
	// The second page had nothing to draw on it and is untouched.
	if got := contentsOf(t, out)[1]; got == "" {
		t.Error("page 2 lost its content")
	}

	under, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := under.Underlay(mark); err != nil {
		t.Fatal(err)
	}
	out, err = under.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, tiles = sheetOf(t, out, 1)
	if len(tiles) != 2 {
		t.Fatalf("tiles = %+v", tiles)
	}
	if err := under.Overlay(New()); err == nil {
		t.Error("an empty document has nothing to draw")
	}
}

func TestBlankPages(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InsertBlank(2); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 3 {
		t.Fatalf("PageCount() = %d", got)
	}
	if err := d.InsertBlank(4); err != nil {
		t.Fatal(err)
	}
	d.Blank(300, 400)
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := contentsOf(t, out)
	if len(got) != 5 || got[1] != "" || got[3] != "" || got[4] != "" {
		t.Errorf("pages = %q", got)
	}
	mb, _ := sheetOf(t, out, 2)
	if mb != [4]float64{0, 0, 100, 200} {
		t.Errorf("the inserted page took the wrong size: %v", mb)
	}
	mb, _ = sheetOf(t, out, 5)
	if mb != [4]float64{0, 0, 300, 400} {
		t.Errorf("the appended page = %v", mb)
	}
	for _, i := range []int{0, 99} {
		if err := d.InsertBlank(i); err == nil {
			t.Errorf("inserting before page %d should fail", i)
		}
	}
	// An empty document falls back on A4.
	empty := New()
	if err := empty.InsertBlank(1); err != nil {
		t.Fatal(err)
	}
	out, err = empty.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	mb, _ = sheetOf(t, out, 1)
	if !near(mb[3], 841.89) {
		t.Errorf("A4 was expected, got %v", mb)
	}
}

func TestRotationIsBakedIntoAComposedPage(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetRotation("all", 90); err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// A 100-by-200 page turned on its side is 200 by 100, so the sheet is too.
	mb, tiles := sheetOf(t, out, 1)
	if mb != [4]float64{0, 0, 200, 100} {
		t.Errorf("sheet = %v", mb)
	}
	if len(tiles) != 2 {
		t.Fatalf("got %d tiles", len(tiles))
	}
	if tiles[0].bbox != [4]float64{0, 0, 100, 200} {
		t.Errorf("the form kept the page's own box: %v", tiles[0].bbox)
	}
}

func TestRotationMatrix(t *testing.T) {
	b := [4]float64{10, 20, 110, 320} // 100 wide, 300 tall, not at the origin
	corners := [][2]float64{{10, 20}, {110, 20}, {110, 320}, {10, 320}}
	for _, rot := range []int{0, 90, 180, 270} {
		m := rotationMatrix(rot, b)
		w, h := 100.0, 300.0
		if rot == 90 || rot == 270 {
			w, h = h, w
		}
		for _, c := range corners {
			x := m[0]*c[0] + m[2]*c[1] + m[4]
			y := m[1]*c[0] + m[3]*c[1] + m[5]
			if x < -1e-6 || x > w+1e-6 || y < -1e-6 || y > h+1e-6 {
				t.Errorf("rotate %d: corner %v maps to (%g,%g), outside %gx%g", rot, c, x, y, w, h)
			}
		}
	}
}

func TestSourceBoxPrefersTheCropBox(t *testing.T) {
	b := buildPDF(t, 1, func(i int, d reader.Dict) {
		d["CropBox"] = reader.Array{reader.Integer(10), reader.Integer(10),
			reader.Integer(60), reader.Integer(110)}
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.effectiveSize(d.pages[0]); got != [2]float64{50, 100} {
		t.Errorf("size = %v", got)
	}
	// An explicit crop set here wins over the file's own.
	if err := d.Crop("1", [4]float64{0, 0, 20, 40}); err != nil {
		t.Fatal(err)
	}
	if got := d.effectiveSize(d.pages[0]); got != [2]float64{20, 40} {
		t.Errorf("size after cropping = %v", got)
	}
	// And a resize is used when there is no crop.
	d, _ = Open(simple(t, 1))
	if err := d.Resize("1", [4]float64{0, 0, 33, 44}); err != nil {
		t.Fatal(err)
	}
	if got := d.effectiveSize(d.pages[0]); got != [2]float64{33, 44} {
		t.Errorf("size after resizing = %v", got)
	}
}

func TestSourceBoxFallsBackToA4(t *testing.T) {
	// A page tree with no box anywhere, and one whose box is nonsense.
	for _, bad := range []reader.Object{nil, reader.Integer(1),
		reader.Array{reader.Integer(0)},
		reader.Array{reader.Name("x"), reader.Integer(0), reader.Integer(1), reader.Integer(1)},
		reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(0), reader.Integer(1)},
	} {
		w := reader.NewWriter("1.7")
		pagesRef := w.Reserve()
		page := reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef}
		if bad != nil {
			page["MediaBox"] = bad
		}
		kid := w.Add(page)
		w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
			"Kids": reader.Array{kid}, "Count": reader.Integer(1)})
		root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
		in, err := w.Finish(reader.Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(in)
		if err != nil {
			t.Fatal(err)
		}
		got := d.effectiveSize(d.pages[0])
		if !near(got[0], 595.276) || !near(got[1], 841.89) {
			t.Errorf("%v: size = %v", bad, got)
		}
	}
}

func TestRectangleNormalisesCorners(t *testing.T) {
	b := buildPDF(t, 1, func(i int, d reader.Dict) {
		// The corners the other way round, which files do write.
		d["MediaBox"] = reader.Array{reader.Integer(110), reader.Integer(320),
			reader.Integer(10), reader.Integer(20)}
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.effectiveSize(d.pages[0]); got != [2]float64{100, 300} {
		t.Errorf("size = %v", got)
	}
}

func TestBestGrid(t *testing.T) {
	// A square sheet holding square pages: as square a grid as will do.
	if c, r := bestGrid(4, 1, 1); c != 2 || r != 2 {
		t.Errorf("4 up = %dx%d", c, r)
	}
	// Two square pages on a square sheet fit either way round; only the
	// number of cells is settled.
	if c, r := bestGrid(2, 1, 1); c*r != 2 {
		t.Errorf("2 up = %dx%d", c, r)
	}
	if c, r := bestGrid(9, 1, 1); c != 3 || r != 3 {
		t.Errorf("9 up = %dx%d", c, r)
	}
	// A tall sheet holding tall pages puts them one above the other.
	if c, r := bestGrid(2, 0.5, 0.5); c != 1 || r != 2 {
		t.Errorf("2 up on a tall sheet = %dx%d", c, r)
	}
	// A prime number cannot tile exactly; the extra cell is accepted.
	c, r := bestGrid(5, 1, 1)
	if c*r < 5 {
		t.Errorf("5 up = %dx%d, too few cells", c, r)
	}
}

func TestComposedPageOfAComposedPage(t *testing.T) {
	// Laying out sheets that are themselves laid out has to nest, not flatten.
	d, err := Open(simple(t, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	if got := d.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d", got)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, tiles := sheetOf(t, out, 1)
	if len(tiles) != 2 {
		t.Fatalf("got %d tiles", len(tiles))
	}
	// Each tile is itself a sheet, so its content draws two more forms.
	for i, tile := range tiles {
		ops, err := reader.Operations([]byte(tile.content))
		if err != nil {
			t.Fatal(err)
		}
		draws := 0
		for _, op := range ops {
			if op.Operator == "Do" {
				draws++
			}
		}
		if draws != 2 {
			t.Errorf("nested sheet %d draws %d forms", i, draws)
		}
	}
}

func TestCropSurvivesComposition(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	d.pages[0].crop = []float64{1, 2, 3, 4}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := src.Page(1)
	if s := string(reader.FormatObject(page.Get("CropBox"))); s != "[1 2 3 4]" {
		t.Errorf("CropBox = %s", s)
	}
}

func TestWriteMatrixAndSorting(t *testing.T) {
	// Tile names have to sort the way they are drawn, which is what the
	// content stream's order already guarantees; this only checks the names
	// are the ones the resources dictionary holds.
	d, err := Open(simple(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(3); err != nil {
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
	res, _ := src.GetDict(page, "Resources")
	xo, _ := src.GetDict(res, "XObject")
	var names []string
	for k := range xo {
		names = append(names, string(k))
	}
	sort.Strings(names)
	want := []string{"Tile0", "Tile1", "Tile2"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v", names)
		}
	}
}

func TestRotatingAComposedSheet(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(2); err != nil {
		t.Fatal(err)
	}
	if err := d.Rotate("all", 90); err != nil {
		t.Fatal(err)
	}
	// The sheet is 100 by 200; turned on its side it shows as 200 by 100.
	if got := d.effectiveSize(d.pages[0]); got != [2]float64{200, 100} {
		t.Errorf("effective size = %v", got)
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
	// The media box is the sheet itself; the turn is an attribute of the page,
	// exactly as a viewer expects.
	if s := string(reader.FormatObject(page.Get("MediaBox"))); s != "[0 0 100 200]" {
		t.Errorf("MediaBox = %s", s)
	}
	if v, _ := reader.ToInt(page.Get("Rotate")); v != 90 {
		t.Errorf("Rotate = %v", page.Get("Rotate"))
	}
}

func TestARotatedBlankPage(t *testing.T) {
	d := New()
	d.Blank(100, 200)
	if err := d.SetRotation("1", 270); err != nil {
		t.Fatal(err)
	}
	if got := d.effectiveSize(d.pages[0]); got != [2]float64{200, 100} {
		t.Errorf("effective size = %v", got)
	}
}
