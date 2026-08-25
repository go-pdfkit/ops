package ops

import (
	"reflect"
	"testing"

	"github.com/go-pdfkit/reader"
)

func TestBookmarksSurvive(t *testing.T) {
	f := richPDF(t, 3)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	got := outlineTitles(t, back)
	want := []string{"chapter 1", "a section", "chapter 2", "chapter 3", "broken"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bookmarks = %v, want %v", got, want)
	}
}

func TestABookmarkThatLedNowhereStays(t *testing.T) {
	// The fixture's last bookmark points at nothing in the source; a file with
	// broken bookmarks keeps its shape rather than losing them.
	f := richPDF(t, 2)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	cat, err := back.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	root, ok := back.GetDict(cat, "Outlines")
	if !ok {
		t.Fatal("no bookmarks")
	}
	var broken reader.Dict
	entry := root.Get("First")
	for i := 0; i < 10; i++ {
		o, err := back.Resolve(entry)
		if err != nil {
			break
		}
		node, ok := reader.ToDict(o)
		if !ok {
			break
		}
		if title, _ := reader.ToString(node.Get("Title")); string(title) == "broken" {
			broken = node
			break
		}
		entry = node.Get("Next")
		if entry.Kind() == reader.KindNull {
			break
		}
	}
	if broken == nil {
		t.Fatal("the broken bookmark was dropped")
	}
	if broken.Get("Dest").Kind() != reader.KindNull {
		t.Errorf("it was given a destination: %v", broken.Get("Dest"))
	}
}

func TestABookmarkToARemovedPageGoesWithIt(t *testing.T) {
	f := richPDF(t, 3)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Select("1"); err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	got := outlineTitles(t, back)
	// Chapter one stays; its section pointed at page three and goes. Chapters
	// two and three go. The broken one, which never led anywhere, stays.
	want := []string{"chapter 1", "broken"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bookmarks = %v, want %v", got, want)
	}
}

func TestAParentWhoseChildrenAllSurviveIsKept(t *testing.T) {
	// Chapter one's own destination is dropped but its child's is not, so the
	// heading stays to hold the child.
	f := richPDF(t, 3)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Select("3"); err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	got := outlineTitles(t, back)
	want := []string{"chapter 1", "a section", "chapter 3", "broken"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bookmarks = %v, want %v", got, want)
	}
}

func TestDropOutlines(t *testing.T) {
	f := richPDF(t, 2)
	d, err := Open(f.bytes)
	if err != nil {
		t.Fatal(err)
	}
	d.DropOutlines()
	back, _ := writeAndOpen(t, d)
	if got := outlineTitles(t, back); len(got) != 0 {
		t.Errorf("bookmarks = %v", got)
	}
}

func TestBookmarksOfTwoDocumentsAreJoined(t *testing.T) {
	a := richPDF(t, 2)
	b := richPDF(t, 2)
	one, err := Open(a.bytes)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Open(b.bytes)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, Merge(one, two))
	got := outlineTitles(t, back)
	if len(got) != 8 {
		t.Errorf("bookmarks = %v, want eight", got)
	}
	if back.PageCount() != 4 {
		t.Errorf("PageCount() = %d", back.PageCount())
	}
}

func TestADocumentWithoutBookmarksGetsNone(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndOpen(t, d)
	cat, err := back.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if cat.Get("Outlines").Kind() != reader.KindNull {
		t.Error("an empty outline was written")
	}
}

func TestBookmarksOfMadePagesAreNone(t *testing.T) {
	// A document of pages this package built borrows from nothing, so there
	// is nothing to carry over.
	d := New()
	d.Blank(100, 100)
	back, _ := writeAndOpen(t, d)
	if got := outlineTitles(t, back); len(got) != 0 {
		t.Errorf("bookmarks = %v", got)
	}
}
