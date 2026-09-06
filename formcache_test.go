package ops

import (
	"crypto/rand"
	"testing"

	"github.com/go-pdfkit/reader"
)

// fatPDF is one page carrying a content stream of the given size, which is
// what makes the duplication visible: the tiles' own operators are a few
// dozen bytes each and would hide it.
func fatPDF(t *testing.T, pages, bytesEach int) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	kids := make(reader.Array, 0, pages)
	// Incompressible, so the duplication shows in the file size rather than
	// being hidden by the deflate the writer applies.
	body := make([]byte, bytesEach)
	if _, err := rand.Read(body); err != nil {
		t.Fatal(err)
	}
	for i, b := range body {
		body[i] = 'A' + b%26 // keep it inside the printable range
	}
	body = append([]byte("% "), body...)
	for i := 1; i <= pages; i++ {
		content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: body})
		kids = append(kids, w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(612), reader.Integer(792)},
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": kids, "Count": reader.Integer(pages)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAPageDrawnManyTimesIsEmbeddedOnce(t *testing.T) {
	// Measured before the cache existed, on one page with a 20 kB content
	// stream: 4 tiles 50 228 bytes and 4 forms, 9 tiles 112 763 and 9, 16
	// tiles 200 310 and 16 -- ten times the source for a 4x4 poster. Poster is
	// the verb that makes it acute because its tile count grows as a product.
	const streamBytes = 20000
	src := fatPDF(t, 1, streamBytes)
	sizes := map[int]int{}
	for _, n := range []int{2, 3, 4} {
		d, err := Open(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Poster(n, n); err != nil {
			t.Fatal(err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if got := formStreams(t, out); got != 1 {
			t.Errorf("%dx%d poster of ONE page holds %d forms, want 1", n, n, got)
		}
		sizes[n] = len(out)
	}
	// And the size no longer grows with the tile count. Sixteen tiles used to
	// cost four times what four did; now the difference is the tiles' own
	// operators, a few dozen bytes each.
	if sizes[4] > sizes[2]+len(src)/4 {
		t.Errorf("a 4x4 poster is %d bytes against a 2x2's %d, over a %d-byte source",
			sizes[4], sizes[2], len(src))
	}
}

func TestPagesThatDifferAreNotShared(t *testing.T) {
	// The cache is keyed on what the form is built from. Eight different pages
	// laid four to a sheet are eight forms, and saying otherwise would put the
	// same page on every tile.
	src := fatPDF(t, 8, 200)
	for _, c := range []struct {
		name string
		do   func(*Doc) error
		want int
	}{
		{"NUp(4) over 8 pages", func(d *Doc) error { return d.NUp(4) }, 8},
		{"Booklet over 8 pages", func(d *Doc) error { return d.Booklet() }, 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Open(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.do(d); err != nil {
				t.Fatal(err)
			}
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if got := formStreams(t, out); got != c.want {
				t.Errorf("%d forms, want %d", got, c.want)
			}
		})
	}
}

func TestTheSamePageTwiceOnOneSheetIsOneForm(t *testing.T) {
	// Select can repeat a page, and then NUp draws it more than once. This is
	// the case the poster made acute, reached by the other verb.
	d, err := Open(fatPDF(t, 2, 200))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Select("1,1,1,1"); err != nil {
		t.Fatal(err)
	}
	if err := d.NUp(4); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := formStreams(t, out); got != 1 {
		t.Errorf("one page four times holds %d forms, want 1", got)
	}
}

func TestWhatIsNotSharedAtAll(t *testing.T) {
	// Only a BORROWED page is cached. Everything else is either cheap to write
	// or carries something the key does not name, and a key that misses a
	// difference puts the wrong picture on the page.
	for _, c := range []struct {
		name string
		page Page
	}{
		{"a blank page", Page{blank: true, size: [2]float64{10, 10}}},
		{"a composed page", Page{tiles: []tile{{}}, size: [2]float64{10, 10}}},
		{"a page made of a picture", Page{picture: &picture{}, size: [2]float64{10, 10}}},
		{"a page with text stamped on it", Page{marks: []stampInstance{{text: "x"}}}},
		{"a page from nowhere", Page{}},
	} {
		if _, ok := formKey(c.page); ok {
			t.Errorf("%s was offered for sharing", c.name)
		}
	}
	// A borrowed one is, and its identity is the source, the number, the
	// rotation and the two boxes.
	base := Page{src: &reader.Document{}, number: 1}
	k, ok := formKey(base)
	if !ok {
		t.Fatal("a borrowed page was not offered for sharing")
	}
	for _, c := range []struct {
		name string
		page Page
	}{
		{"another page of the same document", Page{src: base.src, number: 2}},
		{"the same page turned", Page{src: base.src, number: 1, rotate: 90}},
		{"the same page cropped", Page{src: base.src, number: 1, crop: []float64{0, 0, 1, 1}}},
		{"the same page resized", Page{src: base.src, number: 1, media: []float64{0, 0, 1, 1}}},
		{"the same number in another document", Page{src: &reader.Document{}, number: 1}},
	} {
		other, ok := formKey(c.page)
		if !ok {
			t.Fatalf("%s was not offered for sharing", c.name)
		}
		if other == k {
			t.Errorf("%s got the same key", c.name)
		}
	}
}

// formStreams counts the Form XObjects a document holds. It is the count the
// cache is about, and it does not depend on how the writer compressed them.
func formStreams(t *testing.T, file []byte) int {
	t.Helper()
	d, err := reader.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	// Deduped across the WHOLE document, not per page: a poster makes one page
	// per tile, so counting per page returns the tile count whether or not the
	// forms are shared.
	seen := map[reader.Ref]bool{}
	for i := 1; i <= d.PageCount(); i++ {
		page, err := d.Page(i)
		if err != nil {
			t.Fatal(err)
		}
		res, _ := d.Resolve(page.Get("Resources"))
		rd, _ := reader.ToDict(res)
		xo, _ := d.Resolve(rd.Get("XObject"))
		xd, _ := reader.ToDict(xo)
		for _, v := range xd {
			if ref, ok := v.(reader.Ref); ok {
				seen[ref] = true
			}
		}
	}
	return len(seen)
}
