package ops

import (
	"fmt"
	"testing"

	"github.com/go-pdfkit/reader"
)

// The five links richPDF puts on every page, in the order the page lists them,
// and what each of them leads to from page i of a document of n pages.
//
// ⛔ The JAVASCRIPT one is in this table. It leads nowhere a reader can be sent
// and it is a link all the same; a list that left it out would answer "four"
// to a document carrying five.
func wantLinks(i, n int) []Link {
	return []Link{
		{Page: i, Rect: [4]float64{0, 0, 10, 10}, Target: i%n + 1},
		{Page: i, Rect: [4]float64{10, 0, 20, 10}, Target: 1},
		{Page: i, Rect: [4]float64{20, 0, 30, 10}},
		{Page: i, Rect: [4]float64{30, 0, 40, 10}, URI: "https://example.invalid/"},
		{Page: i, Rect: [4]float64{70, 0, 80, 10}, Target: 1},
	}
}

func TestLinksAreEveryLinkOfEveryPageInOrder(t *testing.T) {
	const pages = 3
	d, err := Open(richPDF(t, pages).bytes)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Links()

	var want []Link
	for i := 1; i <= pages; i++ {
		want = append(want, wantLinks(i, pages)...)
	}
	if len(got) != len(want) {
		t.Fatalf("%d links, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d is %+v, want %+v", i, got[i], want[i])
		}
	}

	// ⛔ And the three annotations on each page that are NOT links are not in
	// it. A list of links that also holds the squares and the attachment is a
	// list of annotations under the wrong name.
	if n := len(got); n != pages*5 {
		t.Errorf("%d entries for %d pages carrying 5 links and 3 other annotations each", n, pages)
	}
}

func TestALinkThatLeadsNowhereIsStillListedAndSaysSo(t *testing.T) {
	d, err := Open(richPDF(t, 2).bytes)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Links()

	leads, dead := 0, 0
	for _, l := range got {
		if l.Leads() {
			leads++
			continue
		}
		dead++
		if l.Target != 0 || l.URI != "" {
			t.Errorf("a link says it leads nowhere and carries %+v", l)
		}
	}
	// One of the five per page runs a script instead of going anywhere.
	if dead != 2 || leads != 8 {
		t.Fatalf("%d lead somewhere and %d nowhere; want 8 and 2", leads, dead)
	}
}

func TestALinkToAPageThatWasDroppedLeadsNowhere(t *testing.T) {
	d, err := Open(richPDF(t, 3).bytes)
	if err != nil {
		t.Fatal(err)
	}
	// Page 1 is what the GoTo and the named destination both point at, and
	// what page 3's first link points at by going round.
	if err := d.Delete("1"); err != nil {
		t.Fatal(err)
	}
	got := d.Links()

	// ⛔ Written out rather than counted. The first version of this test
	// computed the expected number of dead links and computed it WRONG, which
	// a table makes impossible to do quietly: every row below says which link
	// it is and where it now leads.
	//
	// Old page 2 is page 1 now and old page 3 is page 2.
	want := []Link{
		// Old page 2's five, in order.
		{Page: 1, Rect: [4]float64{0, 0, 10, 10}, Target: 2}, // round to old page 3
		{Page: 1, Rect: [4]float64{10, 0, 20, 10}},           // GoTo the dropped page
		{Page: 1, Rect: [4]float64{20, 0, 30, 10}},           // runs a script
		{Page: 1, Rect: [4]float64{30, 0, 40, 10}, URI: "https://example.invalid/"},
		{Page: 1, Rect: [4]float64{70, 0, 80, 10}}, // named the dropped page
		// Old page 3's five.
		{Page: 2, Rect: [4]float64{0, 0, 10, 10}},  // round to the dropped page
		{Page: 2, Rect: [4]float64{10, 0, 20, 10}}, // GoTo the dropped page
		{Page: 2, Rect: [4]float64{20, 0, 30, 10}}, // runs a script
		{Page: 2, Rect: [4]float64{30, 0, 40, 10}, URI: "https://example.invalid/"},
		{Page: 2, Rect: [4]float64{70, 0, 80, 10}}, // named the dropped page
	}
	if len(got) != len(want) {
		t.Fatalf("%d links over the two pages that are left, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	// ⛔ And no link leads out of the document that is left. This is the
	// property, as opposed to the ten rows above: a stale target is a reader
	// sent to a page that is not there.
	for i, l := range got {
		if l.Target > d.PageCount() {
			t.Errorf("link %d leads to page %d of a %d-page document", i, l.Target, d.PageCount())
		}
	}
}

func TestLinksFollowTheDocumentsOwnPageNumbering(t *testing.T) {
	d, err := Open(richPDF(t, 3).bytes)
	if err != nil {
		t.Fatal(err)
	}
	d.Reverse()
	got := d.Links()

	// Reversed, the former page 3 is page 1. Its GoTo and named-destination
	// links still name the former page 1, which is now page 3.
	if got[1].Page != 1 || got[1].Target != 3 {
		t.Errorf("after reversing, the first page's GoTo link is %+v, want page 1 -> 3", got[1])
	}
	// ⛔ And the page a link SITS on is renumbered too, not only the page it
	// goes to. A list that renumbered one and not the other would send a reader
	// to the right place from the wrong row.
	for i, l := range got {
		if want := i/5 + 1; l.Page != want {
			t.Errorf("link %d says it is on page %d, and it is the %dth of page %d", i, l.Page, i%5, want)
		}
	}
}

// ⛔ A file is free to write a rectangle from either pair of corners. A caller
// hit-testing an un-normalised one finds nothing, which looks exactly like a
// page with no links on it rather than like a rectangle written back to front.
func TestARectangleWrittenBackToFrontIsNormalised(t *testing.T) {
	b := onePageWithLink(t, reader.Dict{
		"Type":    reader.Name("Annot"),
		"Subtype": reader.Name("Link"),
		"Rect": reader.Array{reader.Integer(80), reader.Integer(90),
			reader.Integer(20), reader.Integer(30)},
		"A": reader.Dict{"S": reader.Name("URI"), "URI": reader.String("https://example.invalid/")},
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Links()
	if len(got) != 1 {
		t.Fatalf("%d links", len(got))
	}
	if want := [4]float64{20, 30, 80, 90}; got[0].Rect != want {
		t.Errorf("the rectangle came back %v, want %v", got[0].Rect, want)
	}
}

// ⛔ /Dest is not permitted beside /A, so a file carrying both is malformed and
// the ACTION is what a reader acts on. A list that reported the destination
// would name a different page from the one the reader lands on.
func TestAnActionIsReadBeforeADestination(t *testing.T) {
	b := twoPagesWithLink(t, func(first, second reader.Ref) reader.Dict {
		return reader.Dict{
			"Type":    reader.Name("Annot"),
			"Subtype": reader.Name("Link"),
			"Rect":    reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)},
			"Dest":    reader.Array{second, reader.Name("Fit")},
			"A":       reader.Dict{"S": reader.Name("GoTo"), "D": reader.Array{first, reader.Name("Fit")}},
		}
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Links()
	if len(got) != 1 {
		t.Fatalf("%d links", len(got))
	}
	if got[0].Target != 1 {
		t.Errorf("the link leads to page %d; the action names page 1 and the destination page 2", got[0].Target)
	}
}

// ⛔ A STRUCTURAL PROOF OF THE BOUND, NOT A TIMED ONE. pageNumberOf walks every
// page to answer one question; asked once per link that is links x pages, and
// both numbers are the file's to choose. The index is what makes it links +
// pages instead, and this is what holds the index to being built once.
func TestThePageIndexIsBuiltOncePerSource(t *testing.T) {
	d, err := Open(richPDF(t, 4).bytes)
	if err != nil {
		t.Fatal(err)
	}
	src := d.pages[0].src
	cache := map[*reader.Document]map[int]int{}

	first := pageIndex(src, cache)
	if len(first) != 4 {
		t.Fatalf("the index holds %d pages, want 4", len(first))
	}
	// A sentinel nothing else can produce: it comes back only if the second
	// call returned the map the first one built.
	first[-1] = 99
	if again := pageIndex(src, cache); again[-1] != 99 {
		t.Fatal("the second call built a second index, so every link pays for one")
	}
	if len(cache) != 1 {
		t.Fatalf("%d indexes cached for one source", len(cache))
	}
}

func TestADocumentWithNoPagesAndOneWithNoLinks(t *testing.T) {
	if got := New().Links(); got != nil {
		t.Errorf("a document with no pages has %d links", len(got))
	}
	b := onePageWithLink(t, reader.Dict{
		"Type":    reader.Name("Annot"),
		"Subtype": reader.Name("Square"),
		"Rect":    reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)},
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Links(); got != nil {
		t.Errorf("a page carrying one square has %d links: %+v", len(got), got)
	}
}

// ⛔ EVERY FIELD HERE IS THE FILE'S TO CHOOSE, so every one of them can be
// nonsense, and a reader of nonsense has two ways to be wrong: it can fall
// over, and it can invent. This asks for both — the list has to come back the
// right LENGTH, with the malformed entries present and leading nowhere, rather
// than shortened by the ones it could not read.
func TestALinkMadeOfNonsenseIsListedAndLeadsNowhere(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	w.Put(pageRef, reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page 1")}),
		"Annots": reader.Array{
			// Not a dictionary at all.
			reader.Integer(7),
			// A link with no rectangle.
			w.Add(reader.Dict{"Subtype": reader.Name("Link"),
				"A": reader.Dict{"S": reader.Name("URI"), "URI": reader.String("https://example.invalid/")}}),
			// A rectangle of two numbers.
			w.Add(reader.Dict{"Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(1), reader.Integer(2)}}),
			// A URI that is not a string.
			w.Add(reader.Dict{"Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1), reader.Integer(1)},
				"A":    reader.Dict{"S": reader.Name("URI"), "URI": reader.Integer(42)}}),
			// A destination naming a name nothing defines.
			w.Add(reader.Dict{"Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1), reader.Integer(1)},
				"Dest": reader.Name("nothing-defines-this")}),
			// A destination whose first element is a number, which is only
			// meaningful in a file this document is not.
			w.Add(reader.Dict{"Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1), reader.Integer(1)},
				"Dest": reader.Array{reader.Integer(0), reader.Name("Fit")}}),
			// An action of a kind this does not follow.
			w.Add(reader.Dict{"Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1), reader.Integer(1)},
				"A":    reader.Dict{"S": reader.Name("GoToR"), "F": reader.String("elsewhere.pdf")}}),
		},
	})
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{pageRef}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	b, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}

	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Links()
	// Six of the seven entries are dictionaries saying Link; the integer is not
	// an annotation at all and is the only one left out.
	if len(got) != 6 {
		t.Fatalf("%d links, want 6: %+v", len(got), got)
	}
	if got[0].URI != "https://example.invalid/" || got[0].Rect != ([4]float64{}) {
		t.Errorf("the link with no rectangle came back %+v; it still leads somewhere", got[0])
	}
	if got[1].Rect != ([4]float64{}) {
		t.Errorf("a rectangle of two numbers came back as %v rather than nothing", got[1].Rect)
	}
	for i, l := range got[1:] {
		if l.Leads() {
			t.Errorf("malformed link %d leads to %+v", i+1, l)
		}
	}
}

// ⛔ A page tree can name a page that is not a page, and a destination can name
// an object that is not one either. Neither is an error worth stopping a list
// for: the pages that ARE pages still have links, and the links that name
// something real still lead there.
func TestAPageThatIsNotAPageAndADestinationThatIsNotOne(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	realPage := w.Reserve()
	// A "page" that is an integer, which the tree counts and nothing can read.
	notAPage := w.Add(reader.Integer(3))
	// An object that exists and is not a page, for a destination to name.
	notATarget := w.Add(reader.Dict{"Type": reader.Name("Whatever")})

	w.Put(realPage, reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page")}),
		"Annots": reader.Array{w.Add(reader.Dict{"Subtype": reader.Name("Link"),
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1), reader.Integer(1)},
			"Dest": reader.Array{notATarget, reader.Name("Fit")}})},
	})
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{realPage, notAPage},
		"Count":    reader.Integer(2),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	b, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}

	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Links()
	if len(got) != 1 {
		t.Fatalf("%d links; the readable page carries one", len(got))
	}
	if got[0].Page != 1 || got[0].Leads() {
		t.Errorf("the link came back %+v; it names an object that is not a page", got[0])
	}
}

// A page this document made rather than borrowed carries no annotations,
// because there is no file for them to have come from. Asking it is not an
// error; it is a page with no links.
func TestAPageThisDocumentMadeItselfHasNoLinks(t *testing.T) {
	d, err := Open(richPDF(t, 2).bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InsertBlank(1); err != nil {
		t.Fatal(err)
	}
	got := d.Links()
	if len(got) != 10 {
		t.Fatalf("%d links over a blank page and two borrowed ones", len(got))
	}
	// The blank page is page 1, so everything is one further down and the
	// links that named the first borrowed page name page 2 now.
	if got[0].Page != 2 || got[1].Target != 2 {
		t.Errorf("after a blank page was inserted first, the links read %+v and %+v", got[0], got[1])
	}
}

// ⛔ "1,1,2" really does mean three pages, and the first copy is the one
// anything pointing at that page is pointed at — the same choice the
// destination map and the structure tree already make. A link to the twice-used
// page must name one of them and not the later one.
func TestAPageUsedTwiceIsPointedAtOnce(t *testing.T) {
	d, err := Open(richPDF(t, 3).bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Select("1,1,2"); err != nil {
		t.Fatal(err)
	}
	for i, l := range d.Links() {
		if l.Target != 0 && l.Target != 1 && l.Target != 3 {
			t.Errorf("link %d leads to page %d; page 1 is the first copy of the source page", i, l.Target)
		}
	}
}

// ⛔ Links reads a name TREE, which a file can make a cycle of, and follows
// whatever a destination says. Everything it reads is the file's to choose, so
// it is a parser of untrusted structure whatever its signature says.
func FuzzLinksSurvivesAnyFile(f *testing.F) {
	f.Add(richPDF(&testing.T{}, 2).bytes)
	f.Add([]byte("%PDF-1.7\n"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := Open(b)
		if err != nil {
			return
		}
		for _, l := range d.Links() {
			if l.Page < 1 || l.Page > d.PageCount() {
				t.Fatalf("a link sits on page %d of a %d-page document", l.Page, d.PageCount())
			}
			if l.Target < 0 || l.Target > d.PageCount() {
				t.Fatalf("a link leads to page %d of a %d-page document", l.Target, d.PageCount())
			}
		}
	})
}

// onePageWithLink builds a one-page file carrying one annotation.
func onePageWithLink(t *testing.T, annot reader.Dict) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page 1")})
	w.Put(pageRef, reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
		"Annots": reader.Array{w.Add(annot)},
	})
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{pageRef}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// twoPagesWithLink builds a two-page file whose first page carries the
// annotation the caller builds from the two page references.
func twoPagesWithLink(t *testing.T, annot func(first, second reader.Ref) reader.Dict) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	refs := [2]reader.Ref{w.Reserve(), w.Reserve()}
	for i, ref := range refs {
		page := reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(fmt.Sprintf("page %d", i+1))}),
		}
		if i == 0 {
			page["Annots"] = reader.Array{w.Add(annot(refs[0], refs[1]))}
		}
		w.Put(ref, page)
	}
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{refs[0], refs[1]}, "Count": reader.Integer(2),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
