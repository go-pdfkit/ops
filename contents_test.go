package ops_test

import (
	"strings"
	"testing"

	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
)

// contentsFixture is a document of n pages of the size given, each drawing
// something so that a page is not blank for other reasons.
func contentsFixture(t *testing.T, n int, w, h float64) *ops.Doc {
	t.Helper()
	wr := reader.NewWriter("1.7")
	pagesRef := wr.Reserve()
	kids := make(reader.Array, 0, n)
	for range n {
		content := wr.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("0 g 10 10 20 20 re f")})
		kids = append(kids, wr.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
		}))
	}
	wr.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count": reader.Integer(n),
		"MediaBox": reader.Array{reader.Real(0), reader.Real(0),
			reader.Real(w), reader.Real(h)}})
	root := wr.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := wr.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := ops.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// pageText is what one page of the written document draws, as the raw content
// stream: enough to say which strings were shown and in what order.
func pageText(t *testing.T, d *ops.Doc, page int) string {
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

func TestATableOfContentsIsWrittenAsAPage(t *testing.T) {
	d := contentsFixture(t, 9, 595, 842)
	marks := []ops.Bookmark{
		{Title: "Introduction", Page: 1, Children: []ops.Bookmark{{Title: "Why", Page: 2}}},
		{Title: "Chapter one", Page: 7},
	}

	added, err := d.ContentsPage(marks, ops.ContentsOptions{})
	if err != nil {
		t.Fatalf("ContentsPage: %v", err)
	}
	if added != 1 {
		t.Fatalf("it added %d pages for three entries", added)
	}
	if d.PageCount() != 10 {
		t.Fatalf("the document has %d pages", d.PageCount())
	}

	said := pageText(t, d, 1)
	for _, want := range []string{"(Contents)", "(Introduction)", "(Why)", "(Chapter one)"} {
		if !strings.Contains(said, want) {
			t.Errorf("the contents page does not show %s; it draws:\n%s", want, said)
		}
	}
	// ⛔ THE NUMBERS ARE MOVED BY THE PAGE IT ADDED. Chapter one was on page 7
	// and is on page 8 now, and printing the 7 would send a reader a page short
	// of every heading in the book.
	if !strings.Contains(said, "(8)") {
		t.Errorf("the contents does not point Chapter one at page 8; it draws:\n%s", said)
	}
	if strings.Contains(said, "(7)") {
		t.Errorf("the contents still prints the page number from before it existed:\n%s", said)
	}
	// And the dots between a title and its number.
	if !strings.Contains(said, "(....") {
		t.Errorf("no leader of dots was drawn:\n%s", said)
	}
}

// TestTheContentsRunsOntoAsManyPagesAsItNeeds is the promise that nothing is
// left out: a table of contents quietly missing its last entries is worse than
// one that refuses to be written.
func TestTheContentsRunsOntoAsManyPagesAsItNeeds(t *testing.T) {
	d := contentsFixture(t, 3, 595, 842)
	var marks []ops.Bookmark
	for i := range 80 {
		marks = append(marks, ops.Bookmark{Title: "Heading " + strings.Repeat("x", i%7), Page: 1 + i%3})
	}

	added, err := d.ContentsPage(marks, ops.ContentsOptions{})
	if err != nil {
		t.Fatalf("ContentsPage: %v", err)
	}
	if added < 2 {
		t.Fatalf("eighty entries went onto %d page(s)", added)
	}
	if d.PageCount() != 3+added {
		t.Fatalf("the document has %d pages after adding %d", d.PageCount(), added)
	}
	// Every entry is on one of them, and the last page carries the last entry.
	seen := 0
	for i := 1; i <= added; i++ {
		seen += strings.Count(pageText(t, d, i), " Tj")
	}
	// Each row draws a number, a title and a leader; the first page also draws
	// the heading. Counting the shown strings is enough to say that no row was
	// dropped: fewer than two per entry would mean entries with nothing drawn.
	if seen < 2*len(marks) {
		t.Errorf("%d strings drawn for %d entries over %d pages", seen, len(marks), added)
	}
}

func TestWhatAContentsPageRefuses(t *testing.T) {
	for _, c := range []struct {
		why   string
		pages int
		w, h  float64
		marks []ops.Bookmark
		opt   ops.ContentsOptions
		says  string
	}{
		{"no entries at all", 3, 595, 842, nil, ops.ContentsOptions{},
			"a table of contents with no entries is a blank page"},
		{"an entry past the last page", 3, 595, 842,
			[]ops.Bookmark{{Title: "Nowhere", Page: 12}}, ops.ContentsOptions{},
			`entry 1 ("Nowhere") points at page 12, and the document has 3`},
		{"an entry on page nought", 3, 595, 842,
			[]ops.Bookmark{{Title: "Nowhere", Page: 0}}, ops.ContentsOptions{},
			`entry 1 ("Nowhere") points at page 0, and the document has 3`},
		{"a page that does not exist to go before", 3, 595, 842,
			[]ops.Bookmark{{Title: "One", Page: 1}}, ops.ContentsOptions{Before: 9},
			"cannot put a table of contents before page 9 of 3"},
		{"a page before the first one", 3, 595, 842,
			[]ops.Bookmark{{Title: "One", Page: 1}}, ops.ContentsOptions{Before: -1},
			"cannot put a table of contents before page -1 of 3"},
		{"type too large for the sheet", 1, 200, 60,
			[]ops.Bookmark{{Title: "One", Page: 1}}, ops.ContentsOptions{Size: 40, Margin: 10},
			"has no room for 40 point text"},
	} {
		d := contentsFixture(t, c.pages, c.w, c.h)
		added, err := d.ContentsPage(c.marks, c.opt)
		if err == nil {
			t.Errorf("%s: accepted, adding %d page(s)", c.why, added)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: refused with %q, which does not say %q", c.why, err, c.says)
		}
		if d.PageCount() != c.pages {
			t.Errorf("%s: a refusal left the document with %d pages, not %d",
				c.why, d.PageCount(), c.pages)
		}
	}
}

// TestAContentsAtTheEndTakesTheLastPagesSize: a contents may go anywhere, and
// one appended past the last page has no page in front of it to copy.
func TestAContentsAtTheEndTakesTheLastPagesSize(t *testing.T) {
	d := contentsFixture(t, 2, 400, 500)
	added, err := d.ContentsPage([]ops.Bookmark{{Title: "One", Page: 1}},
		ops.ContentsOptions{Before: 3, Title: "At the back"})
	if err != nil {
		t.Fatalf("ContentsPage: %v", err)
	}
	if added != 1 || d.PageCount() != 3 {
		t.Fatalf("%d page(s) added, %d in all", added, d.PageCount())
	}
	said := pageText(t, d, 3)
	if !strings.Contains(said, "(At the back)") {
		t.Errorf("the last page is not the contents:\n%s", said)
	}
	// ⛔ And the entry is NOT moved: it points at page 1, the contents went in
	// after it, and nothing in front of page 1 moved.
	if !strings.Contains(said, "(1)") {
		t.Errorf("the entry was moved by a page added behind it:\n%s", said)
	}
}

// TestALongTitleIsCutRatherThanDrawnOverItsPageNumber: two strings overlapping
// is a line that reads as neither.
func TestALongTitleIsCutRatherThanDrawnOverItsPageNumber(t *testing.T) {
	d := contentsFixture(t, 2, 300, 400)
	long := "A heading so long that it cannot possibly fit across a narrow page beside its own page number"
	if _, err := d.ContentsPage([]ops.Bookmark{{Title: long, Page: 2}},
		ops.ContentsOptions{}); err != nil {
		t.Fatalf("ContentsPage: %v", err)
	}
	said := pageText(t, d, 1)
	if strings.Contains(said, "("+long+")") {
		t.Errorf("the whole title was drawn on a page too narrow for it:\n%s", said)
	}
	if !strings.Contains(said, "...)") {
		t.Errorf("a cut title does not say it was cut:\n%s", said)
	}
}

// TestATitleWithNoRoomAtAllIsRefused is the far end of the same rule, and the
// one that cost an afternoon: a line with a page number and nothing to read
// beside it cannot be told from a document whose headings have no names, so it
// is a refusal naming the entry rather than a shorter line.
//
// ⛔⛔ AND THE REFUSAL COMES BEFORE A PAGE IS INSERTED. The first version laid
// the rows out as it drew them, so a document could come back one page longer
// with a contents page on it that said "2" and nothing else.
func TestATitleWithNoRoomAtAllIsRefused(t *testing.T) {
	d := contentsFixture(t, 2, 220, 400)
	deep := ops.Bookmark{Title: "One", Page: 1, Children: []ops.Bookmark{
		{Title: "Two", Page: 1, Children: []ops.Bookmark{
			{Title: "A nested heading", Page: 2}}}}}

	added, err := d.ContentsPage([]ops.Bookmark{deep}, ops.ContentsOptions{Margin: 70})

	if err == nil {
		t.Fatalf("a page with no room for a nested title accepted it, adding %d page(s)", added)
	}
	if !strings.Contains(err.Error(), `entry 3 ("A nested heading") has no room for its title`) {
		t.Errorf("it refused with %q, which does not name the entry", err)
	}
	if d.PageCount() != 2 {
		t.Errorf("the refusal left the document with %d pages", d.PageCount())
	}
	// And with room it is written, which is what says the refusal is about the
	// room and not about the entry.
	if _, err := d.ContentsPage([]ops.Bookmark{deep}, ops.ContentsOptions{Margin: 20, Size: 8}); err != nil {
		t.Errorf("the same entries on the same page with room: %v", err)
	}
}

// TestFlattenIsNestTheOtherWayRound: the two are a pair, and a tree that comes
// back from the round trip unchanged is what lets a caller hold either shape.
func TestFlattenIsNestTheOtherWayRound(t *testing.T) {
	marks := []ops.Bookmark{
		{Title: "One", Page: 1, Children: []ops.Bookmark{
			{Title: "Under one", Page: 2, Children: []ops.Bookmark{
				{Title: "Deeper", Page: 3}}},
			{Title: "Also under one", Page: 4}}},
		{Title: "Two", Page: 5},
	}
	rows := ops.Flatten(marks)
	want := []ops.Level{
		{Title: "One", Page: 1},
		{Title: "Under one", Page: 2, Depth: 1},
		{Title: "Deeper", Page: 3, Depth: 2},
		{Title: "Also under one", Page: 4, Depth: 1},
		{Title: "Two", Page: 5},
	}
	if len(rows) != len(want) {
		t.Fatalf("flattened to %d rows: %+v", len(rows), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d is %+v, want %+v", i, rows[i], want[i])
		}
	}
	back, err := ops.Nest(rows)
	if err != nil {
		t.Fatalf("Nest: %v", err)
	}
	if got := ops.Flatten(back); len(got) != len(rows) {
		t.Errorf("the round trip gave %d rows for %d", len(got), len(rows))
	}
	if len(back) != 2 || len(back[0].Children) != 2 ||
		len(back[0].Children[0].Children) != 1 {
		t.Errorf("the tree came back the wrong shape: %+v", back)
	}
	if ops.Flatten(nil) != nil {
		t.Error("flattening nothing gave something")
	}
}

// TestShiftingBookmarksLeavesTheOnesInFrontAlone is the edge the rule exists
// for: a document with a preface has entries BEFORE the insertion, and moving
// those too sends a reader to the wrong page in exactly those documents.
func TestShiftingBookmarksLeavesTheOnesInFrontAlone(t *testing.T) {
	marks := []ops.Bookmark{
		{Title: "Preface", Page: 1},
		{Title: "Chapter one", Page: 3, Children: []ops.Bookmark{
			{Title: "Under it", Page: 4}}},
	}
	got := ops.ShiftBookmarks(marks, 3, 2)
	if got[0].Page != 1 {
		t.Errorf("the preface moved to page %d", got[0].Page)
	}
	if got[1].Page != 5 {
		t.Errorf("chapter one is on page %d, want 5", got[1].Page)
	}
	if got[1].Children[0].Page != 6 {
		t.Errorf("the nested entry is on page %d, want 6", got[1].Children[0].Page)
	}
	// ⛔ And the entries handed in are not touched: a caller that keeps its own
	// copy for the printed page and the tree must get two answers, not one
	// mutated under both.
	if marks[1].Page != 3 || marks[1].Children[0].Page != 4 {
		t.Errorf("the entries handed in were moved as well: %+v", marks)
	}
	if ops.ShiftBookmarks(nil, 1, 1) != nil {
		t.Error("shifting nothing gave something")
	}
}

// TestTheContentsAndTheOutlineAgreeAboutWhereAChapterIs is the pair of verbs
// used the way the doc comment says, which is the only way they are right:
// the printed number and the bookmark land on the same page.
func TestTheContentsAndTheOutlineAgreeAboutWhereAChapterIs(t *testing.T) {
	d := contentsFixture(t, 6, 595, 842)
	marks := []ops.Bookmark{{Title: "Chapter one", Page: 4}}

	n, err := d.ContentsPage(marks, ops.ContentsOptions{})
	if err != nil {
		t.Fatalf("ContentsPage: %v", err)
	}
	d.SetOutline(ops.ShiftBookmarks(marks, 1, n))

	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	// The printed contents says 5.
	if said := pageText(t, d, 1); !strings.Contains(said, "(5)") {
		t.Errorf("the printed contents does not say page 5:\n%s", said)
	}
	// And so does the bookmark: its destination is the fifth page's object.
	cat, _ := src.Catalog()
	root, ok := src.GetDict(cat, "Outlines")
	if !ok {
		t.Fatal("the written document carries no outline")
	}
	first, ok := src.GetDict(root, "First")
	if !ok {
		t.Fatal("the outline carries no entries")
	}
	dest, _ := src.Resolve(first.Get("Dest"))
	arr, ok := dest.(reader.Array)
	if !ok || len(arr) == 0 {
		t.Fatalf("the entry's destination is %T", dest)
	}
	want, ok := src.PageRef(5)
	if !ok {
		t.Fatal("the written document has no fifth page")
	}
	if arr[0] != reader.Object(want) {
		t.Errorf("the bookmark points at %v and page 5 is %v", arr[0], want)
	}
}
