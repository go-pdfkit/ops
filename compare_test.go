// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/reader"
)

// wordedPDF is a document whose pages say what they are given to say, one line
// of real text a page, so that extract reads the words back.
func wordedPDF(t *testing.T, pages ...string) []byte {
	t.Helper()
	return wordedPDFWith(t, nil, pages...)
}

// wordedPDFWith is the same with a hand on each page's dictionary, for the
// tests that put a picture or an annotation on one.
func wordedPDFWith(t *testing.T, touch func(w *reader.Writer, i int, d reader.Dict), pages ...string) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	widths := make(reader.Array, 0, 224)
	for i := 32; i < 256; i++ {
		widths = append(widths, reader.Integer(500))
	}
	font := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
		"BaseFont": reader.Name("Helvetica"), "FirstChar": reader.Integer(32),
		"LastChar": reader.Integer(255), "Widths": widths,
		"Encoding": reader.Name("WinAnsiEncoding"),
	})
	kids := make(reader.Array, 0, len(pages))
	for i, says := range pages {
		var body bytes.Buffer
		fmt.Fprintf(&body, "BT /F1 10 Tf 20 300 Td (%s) Tj ET\n", says)
		content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: body.Bytes()})
		d := reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
			"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
		}
		if touch != nil {
			touch(w, i+1, d)
		}
		kids = append(kids, w.Add(d))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": kids, "Count": reader.Integer(len(pages)),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(300), reader.Integer(400)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// changesOf is the comparison as a list of lines, which is how these tests read
// it: one line a change, with the pages it is on.
func changesOf(t *testing.T, before, after []byte) []string {
	t.Helper()
	pairs, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range pairs {
		for _, c := range p.Changes {
			out = append(out, fmt.Sprintf("%d/%d %s %s", p.Before, p.After, c.Kind, c.What))
		}
	}
	return out
}

func TestOnlyTheWordsThatChangedAreReported(t *testing.T) {
	got := changesOf(t,
		wordedPDF(t, "the first chapter of the book"),
		wordedPDF(t, "the second chapter of the book"))
	want := []string{
		"1/1 text removed first",
		"1/1 text added second",
	}
	if !equal(got, want) {
		t.Errorf("the comparison says %q, want %q", got, want)
	}
}

// ⛔ A PHRASE, not a word at a time. Three words becoming three other words is
// one edit to a reader, and six changes listed in a row is a report nobody
// reads. The runs are what diffWords exists to build.
func TestWordsThatChangedTogetherAreReportedTogether(t *testing.T) {
	got := changesOf(t,
		wordedPDF(t, "the quick brown fox jumps over the lazy dog"),
		wordedPDF(t, "the slow grey mole jumps over the lazy dog"))
	want := []string{
		"1/1 text removed quick brown fox",
		"1/1 text added slow grey mole",
	}
	if !equal(got, want) {
		t.Errorf("the comparison says %q, want %q", got, want)
	}
}

func TestTwoIdenticalDocumentsDifferInNothing(t *testing.T) {
	same := wordedPDF(t, "one two three", "four five six")
	pairs, err := Compare(same, same)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 {
		t.Fatalf("%d pair(s) for a two-page document", len(pairs))
	}
	for _, p := range pairs {
		if p.Changed() {
			t.Errorf("page %d/%d of a document compared with itself: %+v", p.Before, p.After, p.Changes)
		}
	}
}

// ⛔⛔ THE POINT OF PAIRING BY CONTENT. A page inserted in the middle must move
// NOTHING after it: pairing by page number would report the inserted page as an
// edit of the third, the third as an edit of the fourth, and so on to the end —
// every page of the document changed, which is both useless and false.
func TestAPageInsertedInTheMiddleMovesNothingAfterIt(t *testing.T) {
	before := wordedPDF(t, "alpha beta gamma", "delta epsilon zeta", "eta theta iota")
	after := wordedPDF(t, "alpha beta gamma", "a wholly new page", "delta epsilon zeta", "eta theta iota")
	pairs, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range pairs {
		got = append(got, fmt.Sprintf("%d/%d:%d", p.Before, p.After, len(p.Changes)))
	}
	want := []string{"1/1:0", "0/2:1", "2/3:0", "3/4:0"}
	if !equal(got, want) {
		t.Errorf("the pairing is %q, want %q — a page added in the middle moved the pages after it", got, want)
	}
	if pairs[1].Changes[0].Kind != PageAdded {
		t.Errorf("the inserted page is reported as %q", pairs[1].Changes[0].Kind)
	}
}

func TestAPageDroppedFromTheMiddleIsOnePageRemoved(t *testing.T) {
	before := wordedPDF(t, "alpha beta gamma", "delta epsilon zeta", "eta theta iota")
	after := wordedPDF(t, "alpha beta gamma", "eta theta iota")
	got := changesOf(t, before, after)
	want := []string{"2/0 page removed "}
	if !equal(got, want) {
		t.Errorf("the comparison says %q, want %q", got, want)
	}
}

// ⛔ A PAGE REPLACED WHOLESALE IS TWO CHANGES, not one page edited beyond
// recognition. Without the floor, the alignment pairs the least unalike pages
// it can find and reports every word of both as an edit of the other, which is
// a diff of two unrelated pages — and reads as though somebody rewrote a page
// rather than swapping it.
func TestAPageWithNothingInCommonIsRemovedAndAdded(t *testing.T) {
	got := changesOf(t,
		wordedPDF(t, "alpha beta gamma delta"),
		wordedPDF(t, "nothing here at all"))
	want := []string{"1/0 page removed ", "0/1 page added "}
	if !equal(got, want) {
		t.Errorf("the comparison says %q, want %q", got, want)
	}
}

// Half the words in common is the floor, so a page that kept half of itself is
// still that page.
func TestAPageThatKeptHalfOfItselfIsStillThatPage(t *testing.T) {
	pairs, err := Compare(
		wordedPDF(t, "one two three four"),
		wordedPDF(t, "one two seven eight"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].Before != 1 || pairs[0].After != 1 {
		t.Fatalf("the pairing is %+v", pairs)
	}
}

func TestHowAlikeTwoPagesAre(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b []string
		want float64
	}{
		{"two empty pages are the same page", nil, nil, 1},
		{"an empty page and a full one share nothing", nil, []string{"a"}, 0},
		{"and the other way round", []string{"a"}, nil, 0},
		{"the same words", []string{"a", "b"}, []string{"a", "b"}, 1},
		{"half of them", []string{"a", "b"}, []string{"a", "c"}, 0.5},
		// ⛔ REPEATS COUNT. Measured as sets, a page saying "a a a a" and a page
		// saying "a" would be identical.
		{"a word repeated is not a word shared", []string{"a", "a", "a", "a"}, []string{"a"}, 0.25},
		{"the longer page is the denominator", []string{"a"}, []string{"a", "b", "c", "d"}, 0.25},
	} {
		if got := sharedWords(c.a, c.b); got != c.want {
			t.Errorf("%s: %g, want %g", c.name, got, c.want)
		}
	}
}

// ⛔⛔ THE EQUALITY ORACLE FOR A DIFF: the script must REBUILD BOTH SIDES. Every
// other assertion about a diff is a matter of taste — which of several
// shortest scripts it chose — but a script whose kept-and-removed words are not
// the earlier page, or whose kept-and-added words are not the later one, is
// simply wrong. Swept over pseudo-random pairs because a hand-written case
// exercises one shape of edit and the walk back through Myers' trace has a
// branch for each.
func TestTheEditScriptRebuildsBothPages(t *testing.T) {
	r := rand.New(rand.NewSource(1)) // ⛔ A FIXED SEED: a flaky diff is no diff.
	words := strings.Fields("alpha beta gamma delta epsilon zeta eta theta")
	pick := func(n int) []string {
		out := make([]string, 0, n)
		for range n {
			out = append(out, words[r.Intn(len(words))])
		}
		return out
	}
	for range 400 {
		was, now := pick(r.Intn(12)), pick(r.Intn(12))
		script, ok := myers(was, now)
		if !ok {
			t.Fatalf("the walk gave up on %d and %d words", len(was), len(now))
		}
		var left, right []string
		for _, e := range script {
			switch e.kind {
			case '=':
				left, right = append(left, e.word), append(right, e.word)
			case '-':
				left = append(left, e.word)
			case '+':
				right = append(right, e.word)
			default:
				t.Fatalf("the script holds a %q", e.kind)
			}
		}
		if !equal(left, was) {
			t.Fatalf("the kept and removed words are %q, and the earlier page was %q", left, was)
		}
		if !equal(right, now) {
			t.Fatalf("the kept and added words are %q, and the later page was %q", right, now)
		}
	}
}

// And it is a SHORTEST script, not merely a correct one: "remove everything,
// add everything" rebuilds both pages too.
func TestTheScriptKeepsWhatTheTwoPagesShare(t *testing.T) {
	script, ok := myers(strings.Fields("a b c d e"), strings.Fields("a x c y e"))
	if !ok {
		t.Fatal("the walk gave up")
	}
	kept := 0
	for _, e := range script {
		if e.kind == '=' {
			kept++
		}
	}
	if kept != 3 {
		t.Errorf("the script keeps %d words of the three the two pages share: %+v", kept, script)
	}
}

// ⛔ Past the bound the answer is the whole of both pages, and it says so by
// being the whole of both pages — not a diff of the half the walk got through
// before it gave up.
func TestTwoPagesTooUnalikeToWalkAreReportedWhole(t *testing.T) {
	was, now := diffEdits, pairingCells
	t.Cleanup(func() { diffEdits, pairingCells = was, now })
	diffEdits = 2

	added, removed := diffWords(strings.Fields("a b c d"), strings.Fields("w x y z"))
	if len(removed) != 1 || !equal(removed[0], strings.Fields("a b c d")) {
		t.Errorf("the words removed are %q", removed)
	}
	if len(added) != 1 || !equal(added[0], strings.Fields("w x y z")) {
		t.Errorf("the words added are %q", added)
	}
	// And a page that lost everything, or gained everything, says only the one
	// thing it has to say.
	if a, r := diffWords(nil, strings.Fields("w x y z")); len(r) != 0 || len(a) != 1 {
		t.Errorf("an empty page became four words: %q removed, %q added", r, a)
	}
	if a, r := diffWords(strings.Fields("a b c d"), nil); len(r) != 1 || len(a) != 0 {
		t.Errorf("four words became an empty page: %q removed, %q added", r, a)
	}
}

// ⛔ The alignment is a cell per pair of pages, so a document that says it has
// a hundred thousand of them must not be allowed to ask for the square of
// that. Past the bound the pages are paired where they stand, which is still a
// true report.
func TestTooManyPagesToAlignArePairedWhereTheyStand(t *testing.T) {
	was := pairingCells
	t.Cleanup(func() { pairingCells = was })
	pairingCells = 1

	pairs, err := Compare(
		wordedPDF(t, "alpha beta gamma", "delta epsilon zeta"),
		wordedPDF(t, "a wholly new page", "alpha beta gamma", "delta epsilon zeta"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range pairs {
		got = append(got, fmt.Sprintf("%d/%d", p.Before, p.After))
	}
	if want := []string{"1/1", "2/2", "0/3"}; !equal(got, want) {
		t.Errorf("the pairing is %q, want %q", got, want)
	}
}

func TestAPictureAddedToAPageIsReported(t *testing.T) {
	plain := wordedPDF(t, "the same words on both")
	withPicture := wordedPDFWith(t, func(w *reader.Writer, _ int, d reader.Dict) {
		data, _ := rowImage(2, 2)
		pic := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(2), "Height": reader.Integer(2),
			"ColorSpace": reader.Name("DeviceRGB"), "BitsPerComponent": reader.Integer(8),
		}, Raw: data})
		d["Resources"] = mergeResources(reader.Dict{"XObject": reader.Dict{"P0": pic}},
			d["Resources"].(reader.Dict))
		d["Contents"] = w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("q 100 0 0 100 10 10 cm /P0 Do Q\nBT /F1 10 Tf 20 300 Td (the same words on both) Tj ET\n")})
	}, "the same words on both")

	got := changesOf(t, plain, withPicture)
	if len(got) != 1 || !strings.Contains(got[0], "picture added 2 by 2, plain") {
		t.Fatalf("adding a picture to a page says %q", got)
	}
	// And the other way round, which is the same question asked backwards.
	back := changesOf(t, withPicture, plain)
	if len(back) != 1 || !strings.Contains(back[0], "picture removed 2 by 2, plain") {
		t.Errorf("taking the picture off says %q", back)
	}
}

// ⛔ TWO PICTURES OF THE SAME SIZE ARE NOT THE SAME PICTURE. A report that
// compared only the shape would call a swapped logo no change at all, which is
// exactly the change somebody comparing two drafts is looking for.
func TestAPictureWhoseSamplesChangedIsBothGoneAndCome(t *testing.T) {
	page := func(fill byte) []byte {
		return wordedPDFWith(t, func(w *reader.Writer, _ int, d reader.Dict) {
			data := bytes.Repeat([]byte{fill}, 2*2*3)
			pic := w.Add(&reader.Stream{Dict: reader.Dict{
				"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
				"Width": reader.Integer(2), "Height": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceRGB"), "BitsPerComponent": reader.Integer(8),
			}, Raw: data})
			d["Resources"] = mergeResources(reader.Dict{"XObject": reader.Dict{"P0": pic}},
				d["Resources"].(reader.Dict))
			d["Contents"] = w.Add(&reader.Stream{Dict: reader.Dict{},
				Raw: []byte("q 100 0 0 100 10 10 cm /P0 Do Q\nBT /F1 10 Tf 20 300 Td (same words) Tj ET\n")})
		}, "same words")
	}
	got := changesOf(t, page(10), page(200))
	if len(got) != 2 {
		t.Fatalf("swapping the picture for another of the same size says %q", got)
	}
	if !strings.Contains(got[0], "picture removed") || !strings.Contains(got[1], "picture added") {
		t.Errorf("it says %q", got)
	}
}

func TestAnAnnotationAddedToAPageIsReported(t *testing.T) {
	plain := wordedPDF(t, "the same words on both")
	noted := wordedPDFWith(t, func(w *reader.Writer, _ int, d reader.Dict) {
		d["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Text"),
			"Rect":     reader.Array{reader.Integer(10), reader.Integer(10), reader.Integer(30), reader.Integer(30)},
			"Contents": reader.String("look again"),
		})}
	}, "the same words on both")

	got := changesOf(t, plain, noted)
	if len(got) != 1 || !strings.Contains(got[0], "annotation added /Text saying look again") {
		t.Errorf("adding a note says %q", got)
	}
}

// A page whose /Annots is not an array — which a damaged file can hold — has no
// annotations rather than an error: the question asked is what changed on the
// page, and a broken entry is not an answer to it.
func TestAnAnnotsEntryThatIsNotAnArrayIsNoAnnotations(t *testing.T) {
	odd := wordedPDFWith(t, func(_ *reader.Writer, _ int, d reader.Dict) {
		d["Annots"] = reader.Integer(7)
	}, "the same words on both")
	if got := changesOf(t, wordedPDF(t, "the same words on both"), odd); len(got) != 0 {
		t.Errorf("a broken /Annots says %q", got)
	}
}

// An annotation with no subtype and no contents is still an annotation, and is
// still reported: a reader comparing two drafts wants to know one appeared.
func TestAnAnnotationWithNothingToSayIsStillReported(t *testing.T) {
	bare := wordedPDFWith(t, func(w *reader.Writer, _ int, d reader.Dict) {
		d["Annots"] = reader.Array{
			w.Add(reader.Dict{"Rect": reader.Array{reader.Integer(1), reader.Integer(1),
				reader.Integer(2), reader.Integer(2)}}),
			// And something in the array that is not a dictionary at all, which
			// is skipped rather than counted.
			reader.Integer(4),
		}
	}, "the same words on both")
	got := changesOf(t, wordedPDF(t, "the same words on both"), bare)
	if len(got) != 1 || !strings.Contains(got[0], "annotation added an annotation") {
		t.Errorf("a bare annotation says %q", got)
	}
}

func TestComparingRefusesWhatItCannotRead(t *testing.T) {
	good := wordedPDF(t, "something")
	for _, c := range []struct {
		name          string
		before, after []byte
		says          string
	}{
		{"the earlier document", []byte("not a pdf"), good, "earlier document could not be read"},
		{"the later document", good, []byte("not a pdf"), "later document could not be read"},
	} {
		if _, err := Compare(c.before, c.after); err == nil {
			t.Errorf("%s: it was accepted", c.name)
		} else if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// ⛔ A PAGE THAT CANNOT BE READ STOPS THE COMPARISON. Skipping it would compare
// the pages that happened to work and report the rest as unchanged, which is
// the worst answer available: a reader is told nothing changed on a page
// nobody managed to look at.
func TestAPageThatCannotBeReadStopsTheComparison(t *testing.T) {
	was := extractText
	t.Cleanup(func() { extractText = was })
	extractText = func(*reader.Document, int) (string, error) {
		return "", errors.New("the stream is a mess")
	}
	_, err := Compare(wordedPDF(t, "one"), wordedPDF(t, "one"))
	if err == nil {
		t.Fatal("a page that could not be read was compared anyway")
	}
	if !strings.Contains(err.Error(), "page 1 of the earlier document") {
		t.Errorf("the refusal says %q, and does not name the page", err)
	}
}

// The same for the later document, which is a different arm and a different
// word in the message.
func TestAPageOfTheLaterDocumentThatCannotBeRead(t *testing.T) {
	good := wordedPDF(t, "one")
	was := extractText
	t.Cleanup(func() { extractText = was })
	calls := 0
	extractText = func(d *reader.Document, page int) (string, error) {
		calls++
		if calls > 1 {
			return "", errors.New("the stream is a mess")
		}
		return was(d, page)
	}
	_, err := Compare(good, good)
	if err == nil || !strings.Contains(err.Error(), "page 1 of the later document") {
		t.Errorf("the refusal is %v", err)
	}
}

// The pictures and the annotations of a paired page are read through the same
// seams the redaction uses, and a failure in either stops the comparison for
// the same reason a page that cannot be read does.
func TestTheThingsOnAPageThatCannotBeRead(t *testing.T) {
	good := wordedPDF(t, "one")
	was := extractImages
	t.Cleanup(func() { extractImages = was })
	calls := 0
	extractImages = func(*reader.Document, int) ([]extract.Image, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("the picture is a mess")
		}
		return nil, nil
	}
	if _, err := Compare(good, good); err == nil ||
		!strings.Contains(err.Error(), "pictures of page 1 of the later document") {
		t.Errorf("the refusal is %v", err)
	}
	calls = 0
	extractImages = func(*reader.Document, int) ([]extract.Image, error) {
		return nil, errors.New("the picture is a mess")
	}
	if _, err := Compare(good, good); err == nil ||
		!strings.Contains(err.Error(), "pictures of page 1 of the earlier document") {
		t.Errorf("the refusal is %v", err)
	}
}

// ⛔⛔ A REFERENCE THAT POINTS AT ITSELF. `5 0 obj 5 0 R endobj` is a file
// anybody can write and no reader can follow; reader stops at its depth limit
// and says so, and a comparison that swallowed that would report a page as
// carrying no annotations because it could not read the ones it has. Both
// documents are asked, because the two arms carry different words.
func TestAReferenceThatPointsAtItselfStopsTheComparison(t *testing.T) {
	loop := wordedPDFWith(t, func(w *reader.Writer, _ int, d reader.Dict) {
		ref := w.Reserve()
		w.Put(ref, ref)
		d["Annots"] = ref
	}, "the same words on both")
	plain := wordedPDF(t, "the same words on both")

	for _, c := range []struct {
		name          string
		before, after []byte
		says          string
	}{
		{"in the earlier document", loop, plain, "annotations of page 1 of the earlier document"},
		{"in the later document", plain, loop, "annotations of page 1 of the later document"},
	} {
		_, err := Compare(c.before, c.after)
		if err == nil {
			t.Errorf("%s: a reference chain with no end was followed to the end", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// The same inside the list, which is a second call to Resolve and a second
// chance to believe a damaged file.
func TestAnAnnotationThatPointsAtItself(t *testing.T) {
	loop := wordedPDFWith(t, func(w *reader.Writer, _ int, d reader.Dict) {
		ref := w.Reserve()
		w.Put(ref, ref)
		d["Annots"] = reader.Array{ref}
	}, "the same words on both")
	_, err := Compare(wordedPDF(t, "the same words on both"), loop)
	if err == nil || !strings.Contains(err.Error(), "annotations of page 1 of the later document") {
		t.Errorf("an annotation that is its own reference says %v", err)
	}
}

// ⛔ A PAGE ADDED AT THE FRONT. The walk back over the alignment runs out of
// one document before the other, and which of the two it is decides which of
// two tails finishes the pairing — the tail for the LATER document is the one
// a page prepended reaches.
func TestAPageAddedAtTheFrontIsTheOnlyChange(t *testing.T) {
	got := changesOf(t,
		wordedPDF(t, "alpha beta gamma"),
		wordedPDF(t, "a wholly new page", "alpha beta gamma"))
	if want := []string{"0/1 page added "}; !equal(got, want) {
		t.Errorf("the comparison says %q, want %q", got, want)
	}
}

// ⛔ A picture the two pages SHARE is not reported. The multiset difference has
// to take it off both sides, or every page that draws a logo would be reported
// as having gained one and lost one.
func TestAPictureOnBothPagesIsNotAChange(t *testing.T) {
	withPictures := func(fills ...byte) []byte {
		return wordedPDFWith(t, func(w *reader.Writer, _ int, d reader.Dict) {
			xo := reader.Dict{}
			var draw bytes.Buffer
			for i, fill := range fills {
				pic := w.Add(&reader.Stream{Dict: reader.Dict{
					"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
					"Width": reader.Integer(2), "Height": reader.Integer(2),
					"ColorSpace": reader.Name("DeviceRGB"), "BitsPerComponent": reader.Integer(8),
				}, Raw: bytes.Repeat([]byte{fill}, 2*2*3)})
				name := reader.Name(fmt.Sprintf("P%d", i))
				xo[name] = pic
				fmt.Fprintf(&draw, "q 50 0 0 50 %d 10 cm /%s Do Q\n", 10+60*i, name)
			}
			d["Resources"] = mergeResources(reader.Dict{"XObject": xo}, d["Resources"].(reader.Dict))
			draw.WriteString("BT /F1 10 Tf 20 300 Td (same words) Tj ET\n")
			d["Contents"] = w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: draw.Bytes()})
		}, "same words")
	}
	got := changesOf(t, withPictures(10), withPictures(10, 200))
	if len(got) != 1 || !strings.Contains(got[0], "picture added") {
		t.Errorf("a page that kept its picture and gained another says %q", got)
	}
}

func TestComparingTwoDocumentsWithNoPages(t *testing.T) {
	empty := wordedPDF(t)
	pairs, err := Compare(empty, empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 {
		t.Errorf("two documents with no pages have %d pair(s)", len(pairs))
	}
}
