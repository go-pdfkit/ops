package ops

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

func TestWriteGivesAPageWithoutGeometryAPaperSize(t *testing.T) {
	// A page tree that names no media box anywhere: the written file must
	// still be one a viewer can show.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef})
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
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := back.Page(1)
	if s := string(reader.FormatObject(got.Get("MediaBox"))); s != "[0 0 595.276 841.89]" {
		t.Errorf("MediaBox = %s", s)
	}
}

func TestWriteDoesNotDragTheSourceTreeAlong(t *testing.T) {
	// One page taken out of a ten-page file must not carry the other nine.
	d, err := Open(simple(t, 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Select("3"); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d", got)
	}
	if got := contentsOf(t, out); !equal(got, pages(3)) {
		t.Errorf("pages = %v", got)
	}
	// And the file is small, because nothing else came with it.
	if len(out) > len(simple(t, 10))/2 {
		t.Errorf("the extracted page is %d bytes of an original %d", len(out), len(simple(t, 10)))
	}
}

func TestWriteKeepsInheritedResources(t *testing.T) {
	b := buildPDF(t, 1, nil)
	src, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := src.Page(1)
	if page.Get("MediaBox").Kind() == reader.KindNull {
		t.Fatal("the fixture has no inherited media box")
	}
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := back.Page(1)
	if s := string(reader.FormatObject(got.Get("MediaBox"))); s != "[0 0 100 200]" {
		t.Errorf("MediaBox = %s", s)
	}
}

func TestPagesFromTwoFilesKeepTheirOwnGeometry(t *testing.T) {
	small, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	large, err := Open(buildPDF(t, 1, func(i int, d reader.Dict) {
		d["MediaBox"] = reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(800), reader.Integer(900)}
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Merge(small, large).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := back.Page(1)
	two, _ := back.Page(2)
	if s := string(reader.FormatObject(one.Get("MediaBox"))); s != "[0 0 100 200]" {
		t.Errorf("page 1 MediaBox = %s", s)
	}
	if s := string(reader.FormatObject(two.Get("MediaBox"))); s != "[0 0 800 900]" {
		t.Errorf("page 2 MediaBox = %s", s)
	}
}
