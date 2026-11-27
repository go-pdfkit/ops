package ops

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/go-pdfkit/reader"
)

// An element whose own page is not outlined, but whose marked content is on one
// that is, keeps no words of its own.
func TestAnElementWithMarkedContentOnAnOutlinedPageLosesItsWords(t *testing.T) {
	g := newTagged()
	mcr := reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(0), "Pg": g.page[0]}
	across := g.elem("P", reader.Dict{"Pg": g.page[1], "ActualText": reader.String("secretacross"),
		"Alt": reader.String("altacross")}, mcr)
	own := g.elem("P", reader.Dict{"Pg": g.page[1], "ActualText": reader.String("keepown")}, reader.Integer(0))
	src := g.finish(t, reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": g.elem("Document", nil, across, own)})
	out := outlinedBytes(t, src, "1")
	for _, gone := range []string{"secretacross", "altacross"} {
		if bytes.Contains(out, []byte(gone)) {
			t.Errorf("%q is still in the file", gone)
		}
	}
	if !bytes.Contains(out, []byte("keepown")) {
		t.Error("an element wholly on a page that was not outlined lost its words")
	}
}

// An element that names no page and holds nothing says nothing of where its
// words would be read, so they go wherever any page is outlined.
func TestAnEmptyElementWithNoPageLosesItsWordsWhenAPageIsOutlined(t *testing.T) {
	g := newTagged()
	empty := g.elem("Figure", reader.Dict{"ActualText": reader.String("secretempty")})
	src := g.finish(t, reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": g.elem("Document", nil, empty)})
	if out := outlinedBytes(t, src, "1"); bytes.Contains(out, []byte("secretempty")) {
		t.Error("the words of an element with no page are still in the file")
	}
}

// The cache of Type 3 fonts is keyed, for a font written directly in a font
// dictionary, by the identity of its dictionary. That holds because the reader
// keeps one copy of each object it has read; this pins that, so that a reader
// that evicted its objects fails here and not silently in the cache.
func TestTheReaderKeepsOneCopyOfAnObject(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 12 Tf (A) Tj ET", directType3, nil)
	r, err := reader.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	pg, _ := r.Page(1)
	res, _ := r.GetDict(pg, "Resources")
	identity := func() uintptr {
		fonts, _ := r.GetDict(res, "Font")
		f1, ok := fonts["F1"].(reader.Dict)
		if !ok {
			t.Fatalf("F1 is %T, want a direct dictionary", fonts["F1"])
		}
		return reflect.ValueOf(f1).Pointer()
	}
	first := identity()
	for n := 1; n < 400; n++ {
		r.GetDict(reader.Dict{"x": reader.Ref{Num: n}}, "x")
	}
	if again := identity(); again != first {
		t.Error("the reader returned another copy of an object it had read")
	}
}

// With no page outlined, an element with no page keeps its words.
func TestAnEmptyElementWithNoPageKeepsItsWordsWhenNothingIsOutlined(t *testing.T) {
	g := newTagged()
	empty := g.elem("Figure", reader.Dict{"ActualText": reader.String("keepempty")})
	src := g.finish(t, reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": g.elem("Document", nil, empty)})
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Rotate("1", 90); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("keepempty")) {
		t.Error("the words of an element were dropped though no page was outlined")
	}
}
