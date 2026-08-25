package ops

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// writeAndOpen writes a document and opens the result.
func writeAndOpen(t *testing.T, d *Doc) (*reader.Document, []byte) {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return back, out
}

func TestLinksArePointedAtTheNewPages(t *testing.T) {
	f := richPDF(t, 3)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)

	newPages := map[int]int{}
	for i := 1; i <= back.PageCount(); i++ {
		r, _ := back.PageRef(i)
		newPages[r.Num] = i
	}
	// Page one's first link goes to page two, in the new numbering.
	annots := annotsOfPage(t, back, 1)
	if len(annots) == 0 {
		t.Fatal("page one lost its annotations")
	}
	dest, _ := reader.ToArray(annots[0].Get("Dest"))
	if len(dest) == 0 {
		t.Fatalf("the first link lost its destination: %v", annots[0])
	}
	ref, ok := dest[0].(reader.Ref)
	if !ok {
		t.Fatalf("the destination is %v", dest[0])
	}
	if got := newPages[ref.Num]; got != 2 {
		t.Errorf("the link goes to page %d, want 2", got)
	}
}

func TestALinkToARemovedPageLosesItsJump(t *testing.T) {
	f := richPDF(t, 3)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	// Keep only page one; its link pointed at page two, which is gone.
	if err := d.Select("1"); err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	for _, a := range annotsOfPage(t, back, 1) {
		if a.Get("Dest").Kind() != reader.KindNull {
			dest, _ := reader.ToArray(a.Get("Dest"))
			if len(dest) > 0 {
				if _, ok := dest[0].(reader.Ref); ok {
					// The only surviving destination must be the page itself.
					self, _ := back.PageRef(1)
					if dest[0] != reader.Object(self) {
						t.Errorf("a link still points at %v", dest[0])
					}
				}
			}
		}
		action, ok := back.GetDict(a, "A")
		if !ok {
			continue
		}
		if kind, _ := reader.ToName(action.Get("S")); kind == "GoTo" {
			dest, _ := reader.ToArray(action.Get("D"))
			if len(dest) > 0 {
				self, _ := back.PageRef(1)
				if dest[0] != reader.Object(self) {
					t.Errorf("an action still points at %v", dest[0])
				}
			}
		}
	}
}

func TestExtractingOnePageDoesNotDragTheRestAlong(t *testing.T) {
	f := richPDF(t, 20)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Select("7"); err != nil {
		t.Fatal(err)
	}
	back, out := writeAndOpen(t, d)
	if back.PageCount() != 1 {
		t.Fatalf("PageCount() = %d", back.PageCount())
	}
	if len(out) > len(f.bytes)/3 {
		t.Errorf("one page of twenty came to %d bytes of %d", len(out), len(f.bytes))
	}
}

func TestNamedDestinationsAreFollowed(t *testing.T) {
	f := richPDF(t, 2)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	first, _ := back.PageRef(1)
	found := false
	for _, a := range annotsOfPage(t, back, 1) {
		dest, ok := reader.ToArray(a.Get("Dest"))
		if !ok || len(dest) == 0 {
			continue
		}
		if dest[0] == reader.Object(first) {
			found = true
		}
	}
	if !found {
		t.Error("the named destination was not resolved to page one")
	}
}

func TestSanitizeRemovesWhatRuns(t *testing.T) {
	f := richPDF(t, 2)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	d.Sanitize()
	back, out := writeAndOpen(t, d)

	for _, needle := range []string{"/JavaScript", "/EmbeddedFile", "app.alert"} {
		if bytes.Contains(out, []byte(needle)) {
			t.Errorf("the file still holds %q", needle)
		}
	}
	for i := 1; i <= back.PageCount(); i++ {
		page, _ := back.Page(i)
		for _, key := range []reader.Name{"AA", "AF"} {
			if page.Get(key).Kind() != reader.KindNull {
				t.Errorf("page %d kept /%s", i, key)
			}
		}
		for _, a := range annotsOfPage(t, back, i) {
			if sub, _ := reader.ToName(a.Get("Subtype")); sub == "FileAttachment" {
				t.Errorf("page %d kept an attachment", i)
			}
			if a.Get("AA").Kind() != reader.KindNull {
				t.Errorf("page %d kept an annotation's own actions", i)
			}
		}
	}
	// A link to the web is not executable and stays.
	uri := false
	for _, a := range annotsOfPage(t, back, 1) {
		if action, ok := back.GetDict(a, "A"); ok {
			if kind, _ := reader.ToName(action.Get("S")); kind == "URI" {
				uri = true
			}
		}
	}
	if !uri {
		t.Error("the link to the web was removed too")
	}
}

func TestACatalogueIsAlwaysRebuilt(t *testing.T) {
	// Even without sanitising, nothing document-level survives, because the
	// catalogue is written from nothing.
	f := richPDF(t, 1)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	cat, err := back.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if cat.Get("OpenAction").Kind() != reader.KindNull {
		t.Error("the document's opening action survived")
	}
	if cat.Get("Names").Kind() != reader.KindNull {
		t.Error("the name tree survived")
	}
}

func TestRemoveAnnotations(t *testing.T) {
	f := richPDF(t, 2)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	d.RemoveAnnotations()
	back, _ := writeAndOpen(t, d)
	for i := 1; i <= back.PageCount(); i++ {
		if len(annotsOfPage(t, back, i)) != 0 {
			t.Errorf("page %d kept its annotations", i)
		}
	}
}

func TestFlattenDrawsAppearancesAndDropsTheAnnotations(t *testing.T) {
	f := richPDF(t, 2)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	d.Flatten()
	back, _ := writeAndOpen(t, d)
	for i := 1; i <= back.PageCount(); i++ {
		if len(annotsOfPage(t, back, i)) != 0 {
			t.Errorf("page %d kept its annotations", i)
		}
		content, err := back.PageContent(i)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "page ") {
			t.Errorf("page %d lost its own content", i)
		}
		// One appearance is drawn; the hidden one is not.
		if got := strings.Count(string(content), " Do"); got != 1 {
			t.Errorf("page %d draws %d appearances, want 1", i, got)
		}
	}
}

func TestFlattenLeavesAPageWithNothingToFlattenAlone(t *testing.T) {
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	d.Flatten()
	back, _ := writeAndOpen(t, d)
	content, err := back.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "page 1" {
		t.Errorf("content = %q", content)
	}
}

func TestAppearanceStates(t *testing.T) {
	// An appearance may be a dictionary of states rather than one stream, and
	// which one is drawn depends on what the annotation says it is showing.
	build := func(as string, states ...string) []byte {
		w := reader.NewWriter("1.7")
		pagesRef := w.Reserve()
		appearance := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
		}, Raw: []byte("1 0 0 rg")})
		byState := reader.Dict{}
		for _, s := range states {
			byState[reader.Name(s)] = appearance
		}
		annot := reader.Dict{
			"Type":    reader.Name("Annot"),
			"Subtype": reader.Name("Widget"),
			"Rect":    reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)},
			"AP":      reader.Dict{"N": byState},
		}
		if as != "" {
			annot["AS"] = reader.Name(as)
		}
		page := w.Add(reader.Dict{
			"Type":   reader.Name("Page"),
			"Parent": pagesRef,
			"Annots": reader.Array{w.Add(annot)},
		})
		w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
			"Kids": reader.Array{page}, "Count": reader.Integer(1),
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(100), reader.Integer(100)}})
		root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
		out, err := w.Finish(reader.Dict{"Root": root})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	cases := []struct {
		name   string
		as     string
		states []string
		draws  int
	}{
		{"the state the annotation is in", "On", []string{"On", "Off"}, 1},
		{"the only state there is", "", []string{"Only"}, 1},
		{"no state named and several to choose from", "", []string{"On", "Off"}, 0},
		{"a state that is not there", "Missing", []string{"On"}, 0},
		{"no states at all", "", nil, 0},
	}
	for _, c := range cases {
		d, err := Open(build(c.as, c.states...))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		d.Flatten()
		back, _ := writeAndOpen(t, d)
		content, err := back.PageContent(1)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := strings.Count(string(content), " Do"); got != c.draws {
			t.Errorf("%s: draws %d, want %d", c.name, got, c.draws)
		}
	}
}
