package ops

import (
	"fmt"
	"testing"

	"github.com/go-pdfkit/reader"
)

// buildPDF assembles a document of n pages, page k carrying the content
// "page k", so an operation's effect can be read straight off the output.
func buildPDF(t *testing.T, n int, page func(i int, d reader.Dict)) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	kids := make(reader.Array, 0, n)
	for i := 1; i <= n; i++ {
		content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(fmt.Sprintf("page %d", i))})
		d := reader.Dict{
			"Type":     reader.Name("Page"),
			"Parent":   pagesRef,
			"Contents": content,
		}
		if page != nil {
			page(i, d)
		}
		kids = append(kids, w.Add(d))
	}
	w.Put(pagesRef, reader.Dict{
		"Type":     reader.Name("Pages"),
		"Kids":     kids,
		"Count":    reader.Integer(n),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	info := w.Add(reader.Dict{"Title": reader.String("a title"), "Author": reader.String("an author")})
	out, err := w.Finish(reader.Dict{"Root": root, "Info": info})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// simple is the fixture most tests want.
func simple(t *testing.T, n int) []byte { return buildPDF(t, n, nil) }

// contentsOf reads back the content stream of every page of a file.
func contentsOf(t *testing.T, b []byte) []string {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, d.PageCount())
	for i := 1; i <= d.PageCount(); i++ {
		data, err := d.PageContent(i)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		out = append(out, string(data))
	}
	return out
}

// written writes a document and reads its pages back.
func written(t *testing.T, d *Doc) []string {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return contentsOf(t, out)
}

// pages names the expected content of the given page numbers.
func pages(nums ...int) []string {
	out := make([]string, len(nums))
	for i, n := range nums {
		out[i] = fmt.Sprintf("page %d", n)
	}
	return out
}

// equal compares two lists of page contents.
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
