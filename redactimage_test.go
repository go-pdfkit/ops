package ops

import (
	"bytes"
	"testing"

	"github.com/go-pdfkit/reader"
)

// rowImage is a picture whose every ROW is a flat colour of its own, so that
// what was destroyed and what was kept can be told apart by reading the samples
// back — and so that reading the grid upside down is a failing test rather than
// a plausible picture.
//
// Row 0 is the TOP row of the picture, which is where the sample grid starts
// and where the page's own y runs the other way.
func rowImage(w, h int) (data []byte, rows []byte) {
	for row := range h {
		v := byte(10 * (row + 1))
		rows = append(rows, v)
		for range w {
			data = append(data, v, v, v)
		}
	}
	return data, rows
}

// picturePage is a page of the given size with one picture on it, drawn by the
// content given. opt lets a test make the picture something this release
// refuses to redact.
func picturePage(t *testing.T, content string, opt func(reader.Dict)) ([]byte, []byte) {
	t.Helper()
	data, _ := rowImage(4, 4)
	w := reader.NewWriter("1.7")
	pages := w.Reserve()
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(4), "Height": reader.Integer(4),
		"ColorSpace": reader.Name("DeviceRGB"), "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("FlateDecode"),
	}
	if opt != nil {
		opt(dict)
	}
	pic := w.Add(&reader.Stream{Dict: dict, Raw: deflate(data)})
	body := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pages, "Contents": body,
		"Resources": reader.Dict{"XObject": reader.Dict{"Im1": pic}},
	})
	w.Put(pages, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pages})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out, data
}

// samplesOf is the picture of the written document's first page, unfiltered.
func samplesOf(t *testing.T, b []byte) []byte {
	t.Helper()
	doc, err := reader.Open(b)
	if err != nil {
		t.Fatalf("the written document does not open: %v", err)
	}
	page, err := doc.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	res, ok := doc.GetDict(page, "Resources")
	if !ok {
		t.Fatal("the written page carries no resources")
	}
	xo, ok := doc.GetDict(res, "XObject")
	if !ok {
		t.Fatal("the written page carries no XObject resources")
	}
	o, err := doc.Resolve(xo.Get("Im1"))
	if err != nil {
		t.Fatal(err)
	}
	st, ok := reader.ToStream(o)
	if !ok {
		t.Fatalf("Im1 is a %s, not a stream", o.Kind())
	}
	data, _, err := doc.DecodeStream(st)
	if err != nil {
		t.Fatalf("the written picture will not decode: %v", err)
	}
	return data
}

// anyObjectHolds says whether ANY object of the file, stream or not, holds
// those bytes once every filter this reader understands is undone.
//
// ⛔⛔ EVERY OBJECT, NOT THE PAGE'S. A promise about the BYTES of a file cannot
// be checked by reading the document those bytes describe: the leak #74 was
// about lived in an object the page no longer pointed at, so following the page
// could not see it. A picture's samples are compressed, so a raw grep cannot
// see them either — this walks the object numbers the trailer says exist and
// decodes each one.
func anyObjectHolds(t *testing.T, file, needle []byte) bool {
	t.Helper()
	doc, err := reader.Open(file)
	if err != nil {
		t.Fatalf("the written document does not open: %v", err)
	}
	n, _ := reader.ToInt(doc.Trailer().Get("Size"))
	size := int(n)
	if size < 2 {
		t.Fatalf("the trailer says the file holds %d objects", size)
	}
	for num := 1; num < size; num++ {
		o, err := doc.Get(reader.Ref{Num: num})
		if err != nil {
			continue
		}
		st, ok := reader.ToStream(o)
		if !ok {
			continue
		}
		if bytes.Contains(st.Raw, needle) {
			return true
		}
		if data, _, err := doc.DecodeStream(st); err == nil && bytes.Contains(data, needle) {
			return true
		}
	}
	return false
}

// TestTheSamplesUnderARedactionAreDestroyed is ISO 32000-1 12.5.6.23 as a test:
// "that portion of the image data shall be destroyed; clipping or image masks
// shall not be used to hide that data".
func TestTheSamplesUnderARedactionAreDestroyed(t *testing.T) {
	// The picture is drawn across the middle of the page, 100 by 100 points at
	// (50,50). Its four rows are therefore 25 points each.
	file, original := picturePage(t, "q 100 0 0 100 50 50 cm /Im1 Do Q", nil)
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	// The TOP half of the drawn picture: y from 100 to 150 on the page, which
	// is the top two rows of its grid.
	if err := d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 50, TopLeftY: 150, TopRightX: 150, TopRightY: 150,
		BottomLeftX: 50, BottomLeftY: 100, BottomRightX: 150, BottomRightY: 100,
	}}}); err != nil {
		t.Fatalf("Redact refused a picture it can blank: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	got := samplesOf(t, out)
	if len(got) != len(original) {
		t.Fatalf("the written picture holds %d bytes, the original %d", len(got), len(original))
	}
	// ⛔ THE TOP TWO ROWS AND NOT THE BOTTOM TWO. A grid read upside down blanks
	// the mirror image of what was asked for, which looks like a redaction until
	// somebody compares it with the page.
	for row := range 4 {
		at := row * 4 * 3
		line := got[at : at+12]
		blank := bytes.Equal(line, make([]byte, 12))
		if row < 2 && !blank {
			t.Errorf("row %d is %v and should have been destroyed", row, line)
		}
		if row >= 2 && blank {
			t.Errorf("row %d was destroyed and nobody asked about it", row)
		}
		if row >= 2 && line[0] != byte(10*(row+1)) {
			t.Errorf("row %d reads %v, not the %d it was", row, line, 10*(row+1))
		}
	}

	// ⛔⛔ AND THE ORIGINAL SAMPLES ARE NOWHERE IN THE FILE. Writing the blanked
	// picture while the original stayed, referenced by nothing, is the defect
	// #74 was about, one object along: the document would read as redacted and
	// still hold every destroyed pixel.
	if anyObjectHolds(t, out, original[:24]) {
		t.Error("the original samples are still in the file, in some object of it")
	}
}

// TestARedactionBesideAPictureLeavesItAlone: a region that does not reach the
// picture must not rewrite it, and must not cost it its compression either.
func TestARedactionBesideAPictureLeavesItAlone(t *testing.T) {
	file, original := picturePage(t, "q 100 0 0 100 50 50 cm /Im1 Do Q", nil)
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 0, TopLeftY: 40, TopRightX: 40, TopRightY: 40,
		BottomLeftX: 0, BottomLeftY: 0, BottomRightX: 40, BottomRightY: 0,
	}}}); err != nil {
		t.Fatalf("Redact refused a region that touches no picture: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := samplesOf(t, out); !bytes.Equal(got, original) {
		t.Errorf("a redaction elsewhere changed the picture: %v", got)
	}
}

// TestWhatARedactionOverAPictureRefuses: each arm names the reason, because a
// redaction that quietly did less than it was asked is worse than one that says
// it cannot.
func TestWhatARedactionOverAPictureRefuses(t *testing.T) {
	over := Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 50, TopLeftY: 150, TopRightX: 150, TopRightY: 150,
		BottomLeftX: 50, BottomLeftY: 100, BottomRightX: 150, BottomRightY: 100,
	}}}
	for _, c := range []struct {
		why     string
		content string
		opt     func(reader.Dict)
		says    string
	}{
		{"turned a quarter", "q 0 100 -100 0 150 50 cm /Im1 Do Q", nil, "turned or skewed"},
		{"skewed", "q 100 20 0 100 50 50 cm /Im1 Do Q", nil, "turned or skewed"},
		{"drawn twice", "q 100 0 0 100 50 50 cm /Im1 Do Q q 20 0 0 20 0 0 cm /Im1 Do Q", nil,
			`draws "Im1" 2 times`},
		{"a JPEG", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["Filter"] = reader.Name("DCTDecode")
		}, "samples are still DCTDecode"},
		{"an image mask", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["ImageMask"] = reader.Bool(true)
		}, "image mask"},
		{"a soft mask", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["SMask"] = reader.Ref{Num: 99}
		}, "/SMask"},
		{"four bits a sample", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["BitsPerComponent"] = reader.Integer(4)
		}, "4 bits each"},
		{"an indexed palette", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["ColorSpace"] = reader.Array{reader.Name("Indexed"), reader.Name("DeviceRGB")}
		}, "/Indexed, which is a family"},
		{"a colour space it does not know", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["ColorSpace"] = reader.Name("DeviceCMYK")
		}, "/DeviceCMYK"},
		{"samples that are not all there", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["Width"] = reader.Integer(5)
		}, "the samples are not all there"},
		{"a picture of no samples at all", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["Width"] = reader.Integer(0)
			d["Height"] = reader.Integer(0)
		}, "it says it is 0 by 0 samples"},
		{"a colour space that is not even a name", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["ColorSpace"] = reader.Integer(7)
		}, "its colour space is integer"},
		{"a colour space family with no name in it", "q 100 0 0 100 50 50 cm /Im1 Do Q", func(d reader.Dict) {
			d["ColorSpace"] = reader.Array{reader.Integer(1)}
		}, "its colour space is an array"},
	} {
		file, _ := picturePage(t, c.content, c.opt)
		d, err := Open(file)
		if err != nil {
			t.Fatalf("%s: %v", c.why, err)
		}
		err = d.Redact(over)
		if err == nil {
			t.Errorf("%s: accepted", c.why)
			continue
		}
		if !contains(err.Error(), c.says) {
			t.Errorf("%s: refused with %q, which does not say %q", c.why, err, c.says)
		}
		// ⛔ AND IT SAYS SO AT Redact, not at the write: a caller told here can
		// still choose another region or another tool.
		if !contains(err.Error(), "must destroy its pixels") {
			t.Errorf("%s: the refusal does not say why it matters: %q", c.why, err)
		}
	}
}

// TestARegionWiderThanThePictureDestroysAllOfIt: a region may hang off every
// edge, and the samples it names are clamped to the ones that exist rather than
// read past the end of the picture.
func TestARegionWiderThanThePictureDestroysAllOfIt(t *testing.T) {
	file, original := picturePage(t, "q 100 0 0 100 50 50 cm /Im1 Do Q", nil)
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	// The whole page, which overhangs the picture on all four sides.
	if err := d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: -20, TopLeftY: 220, TopRightX: 220, TopRightY: 220,
		BottomLeftX: -20, BottomLeftY: -20, BottomRightX: 220, BottomRightY: -20,
	}}}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := samplesOf(t, out)
	if !bytes.Equal(got, make([]byte, len(original))) {
		t.Errorf("a region over the whole picture left %v", got)
	}
}

// TestARegionOverPartOfOneRowDestroysThatWholeSample: a sample is destroyed or
// it is not, so a region ending a third of the way into one takes the whole of
// it — the other way round would leave a third of a secret.
func TestARegionOverPartOfOneRowDestroysThatWholeSample(t *testing.T) {
	// The picture is 4 by 4 over 100 points, so a sample is 25 points. This
	// region covers the leftmost 30 points: the first column, and a third of
	// the second.
	file, _ := picturePage(t, "q 100 0 0 100 50 50 cm /Im1 Do Q", nil)
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 50, TopLeftY: 150, TopRightX: 80, TopRightY: 150,
		BottomLeftX: 50, BottomLeftY: 125, BottomRightX: 80, BottomRightY: 125,
	}}}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := samplesOf(t, out)
	// Row 0, columns 0 and 1 destroyed; columns 2 and 3 of that row untouched.
	if !bytes.Equal(got[0:6], make([]byte, 6)) {
		t.Errorf("the first two samples of the top row are %v", got[0:6])
	}
	if got[6] != 10 || got[9] != 10 {
		t.Errorf("the rest of the top row reads %v and should be untouched", got[6:12])
	}
	if got[12] != 20 {
		t.Errorf("the second row was touched: %v", got[12:24])
	}
}

// TestARedactionOverAPictureAndAStampKeepsBoth is the pair that undoes itself
// if the resources are rebuilt twice: decorate copies a page's resources to
// make its own, and copying the SOURCE's again would put the original picture
// back under the blanked one.
func TestARedactionOverAPictureAndAStampKeepsBoth(t *testing.T) {
	file, original := picturePage(t, "q 100 0 0 100 50 50 cm /Im1 Do Q", nil)
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 50, TopLeftY: 150, TopRightX: 150, TopRightY: 150,
		BottomLeftX: 50, BottomLeftY: 100, BottomRightX: 150, BottomRightY: 100,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("all", Stamp{Text: "COPY", Position: TopRight}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := samplesOf(t, out)
	if !bytes.Equal(got[:24], make([]byte, 24)) {
		t.Errorf("stamping the page put the destroyed samples back: %v", got[:24])
	}
	if anyObjectHolds(t, out, original[:24]) {
		t.Error("stamping the page left the original samples in the file")
	}
	if !bytes.Contains(mustContent(t, out), []byte("(COPY) Tj")) {
		t.Error("the stamp is not on the page")
	}
}

// TestTheOTHERPictureOnThePageIsLeftAlone: a page may carry several, and
// rebuilding its resources must carry the ones nobody asked about across
// untouched — not drop them, and not blank them.
func TestTheOtherPictureOnThePageIsLeftAlone(t *testing.T) {
	data, _ := rowImage(4, 4)
	w := reader.NewWriter("1.7")
	pages := w.Reserve()
	pic := func() reader.Object {
		return w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(4), "Height": reader.Integer(4),
			"ColorSpace": reader.Name("DeviceRGB"), "BitsPerComponent": reader.Integer(8),
			"Filter": reader.Name("FlateDecode"),
		}, Raw: deflate(data)})
	}
	body := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(
		"q 100 0 0 100 50 100 cm /Im1 Do Q q 40 0 0 40 10 10 cm /Im2 Do Q")})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pages, "Contents": body,
		"Resources": reader.Dict{"XObject": reader.Dict{"Im1": pic(), "Im2": pic()}},
	})
	w.Put(pages, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pages})
	file, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	// Over Im1 only: it is drawn from y=100 up, and Im2 from y=10 to y=50.
	if err := d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 50, TopLeftY: 200, TopRightX: 150, TopRightY: 200,
		BottomLeftX: 50, BottomLeftY: 150, BottomRightX: 150, BottomRightY: 150,
	}}}); err != nil {
		t.Fatalf("Redact refused: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	written, err := doc.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := doc.GetDict(written, "Resources")
	xo, ok := doc.GetDict(res, "XObject")
	if !ok || len(xo) != 2 {
		t.Fatalf("the written page carries %d pictures, want two", len(xo))
	}
	kept, _ := doc.Resolve(xo.Get("Im2"))
	st, ok := reader.ToStream(kept)
	if !ok {
		t.Fatalf("Im2 is a %s", kept.Kind())
	}
	got, _, err := doc.DecodeStream(st)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("the picture nobody asked about came back changed: %v", got[:12])
	}
}

// TestAnInlineImageAcrossTheRegionIsRefused: a picture written into the content
// stream is not a resource, so there is nothing to write back in its place —
// and the stream it lives in is the one the redaction is already rewriting.
func TestAnInlineImageAcrossTheRegionIsRefused(t *testing.T) {
	w := reader.NewWriter("1.7")
	pages := w.Reserve()
	// BI … ID … EI: four grey samples, drawn across the middle of the page.
	body := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(
		"q 100 0 0 100 50 50 cm BI /W 2 /H 2 /CS /G /BPC 8 ID \x00\x40\x80\xff EI Q")})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pages, "Contents": body,
		"Resources": reader.Dict{},
	})
	w.Put(pages, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pages})
	file, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}

	err = d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 50, TopLeftY: 150, TopRightX: 150, TopRightY: 150,
		BottomLeftX: 50, BottomLeftY: 100, BottomRightX: 150, BottomRightY: 100,
	}}})

	if err == nil {
		t.Fatal("an inline image across the region was accepted")
	}
	if !contains(err.Error(), "written into the content stream") {
		t.Errorf("it refused with %q", err)
	}
}

// TestStampingAPageWithNoResourcesAtAll is the other side of the resources
// rebuild: a page may carry none, and the rebuild has to hand back an empty
// dictionary rather than reach into one that is not there.
func TestStampingAPageWithNoResourcesAtAll(t *testing.T) {
	w := reader.NewWriter("1.7")
	pages := w.Reserve()
	body := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("0 g 10 10 30 30 re f")})
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pages, "Contents": body})
	w.Put(pages, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pages})
	file, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("all", Stamp{Text: "COPY", Position: TopRight}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatalf("stamping a page with no resources: %v", err)
	}
	if !bytes.Contains(mustContent(t, out), []byte("(COPY) Tj")) {
		t.Error("the stamp is not on the page")
	}
}

// mustContent is the first page's content stream of a written document.
func mustContent(t *testing.T, b []byte) []byte {
	t.Helper()
	doc, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	data, err := doc.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// contains is strings.Contains without the import, for a file that otherwise
// needs none.
func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
