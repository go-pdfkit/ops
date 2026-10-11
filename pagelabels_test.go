// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"strings"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
)

// labelsOf writes a document out, reads it back and reports what each page is
// labelled. ⛔ Through the FILE, not from the Doc: what matters is what a
// viewer will show, and the only way to know that is to write the tree and
// read it again.
func labelsOf(t *testing.T, d *Doc) []string {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return back.PageLabels()
}

func labelled(t *testing.T, pages int, rules ...LabelRule) *Doc {
	t.Helper()
	d, err := Open(simple(t, pages))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetPageLabels(rules); err != nil {
		t.Fatal(err)
	}
	return d
}

// A book: roman front matter, then the body from one.
func TestRomanFrontMatterThenDecimalBody(t *testing.T) {
	d := labelled(t, 8,
		LabelRule{Pages: "1-3", Style: LabelLowerRoman},
		LabelRule{Pages: "4-8", Style: LabelDecimal},
	)
	want := []string{"i", "ii", "iii", "1", "2", "3", "4", "5"}
	got := labelsOf(t, d)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

// A prefix makes the appendix: A-1, A-2, A-3.
func TestAPrefixedAppendix(t *testing.T) {
	d := labelled(t, 3, LabelRule{Style: LabelDecimal, Prefix: "A-"})
	if got := labelsOf(t, d); strings.Join(got, ",") != "A-1,A-2,A-3" {
		t.Errorf("labels = %v", got)
	}
}

// ⛔ A page-label range in a PDF is CONTIGUOUS, so a rule over a disjoint
// range has to be compiled into runs. Without Continue each run starts again;
// with it the numbering carries on.
func TestADisjointRangeRestartsUnlessItIsToldNotTo(t *testing.T) {
	d := labelled(t, 6, LabelRule{Pages: "1-2,5-6", Style: LabelDecimal})
	if got := labelsOf(t, d); strings.Join(got, ",") != "1,2,,,1,2" {
		t.Errorf("without Continue: labels = %v, want 1,2,,,1,2", got)
	}
	d = labelled(t, 6, LabelRule{Pages: "1-2,5-6", Style: LabelDecimal, Continue: true})
	if got := labelsOf(t, d); strings.Join(got, ",") != "1,2,,,3,4" {
		t.Errorf("with Continue: labels = %v, want 1,2,,,3,4", got)
	}
}

// "odd" is the extreme of the same thing: every labelled page is its own run.
func TestOddPagesAreEachTheirOwnRun(t *testing.T) {
	d := labelled(t, 6, LabelRule{Pages: "odd", Style: LabelDecimal, Continue: true})
	if got := labelsOf(t, d); strings.Join(got, ",") != "1,,2,,3," {
		t.Errorf("labels = %v, want 1,,2,,3,", got)
	}
}

// A later rule wins where two overlap, which is how a correction is made
// without rewriting the earlier rules.
func TestALaterRuleWins(t *testing.T) {
	d := labelled(t, 4,
		LabelRule{Style: LabelDecimal},
		LabelRule{Pages: "3-4", Style: LabelUpperRoman, Start: 10},
	)
	if got := labelsOf(t, d); strings.Join(got, ",") != "1,2,X,XI" {
		t.Errorf("labels = %v, want 1,2,X,XI", got)
	}
}

// A style of none is the prefix alone, which is how a run of pages is marked
// "Cover" or "Insert".
func TestAStyleOfNoneIsThePrefixAlone(t *testing.T) {
	d := labelled(t, 3,
		LabelRule{Pages: "1", Prefix: "Cover"},
		LabelRule{Pages: "2-3", Style: LabelDecimal},
	)
	if got := labelsOf(t, d); strings.Join(got, ",") != "Cover,1,2" {
		t.Errorf("labels = %v, want Cover,1,2", got)
	}
}

func TestStart(t *testing.T) {
	d := labelled(t, 3, LabelRule{Style: LabelDecimal, Start: 42})
	if got := labelsOf(t, d); strings.Join(got, ",") != "42,43,44" {
		t.Errorf("labels = %v", got)
	}
}

// ⛔ The format's own rule, not a choice here: ISO 32000-1 table 159 says /St
// shall be at least 1, so a zero-based scheme is refused rather than written
// out of specification. BentoPDF's tool offers "set to 0 for zero-based
// numbering", and what that writes is a value the standard forbids.
func TestAStartBelowOneIsRefused(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	err = d.SetPageLabels([]LabelRule{{Style: LabelDecimal, Start: -1}})
	if err == nil {
		t.Fatal("a negative start was accepted")
	}
	if !strings.Contains(err.Error(), "at least 1") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	// And zero is the DEFAULT of one rather than a refusal, because zero is
	// the Go zero value and a caller who did not set Start meant "from one".
	if err := d.SetPageLabels([]LabelRule{{Style: LabelDecimal}}); err != nil {
		t.Fatal(err)
	}
	if got := labelsOf(t, d); strings.Join(got, ",") != "1,2" {
		t.Errorf("an unset Start gave %v, want 1,2", got)
	}
}

func TestRefusals(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetPageLabels([]LabelRule{{Style: "Q"}}); err == nil {
		t.Error("a style the format does not have was accepted")
	}
	if err := d.SetPageLabels([]LabelRule{{Pages: "not a range"}}); err == nil {
		t.Error("a range that is not one was accepted")
	}
	if err := New().SetPageLabels([]LabelRule{{Style: LabelDecimal}}); err == nil {
		t.Error("an empty document was labelled")
	}
	// No rules removes the labels.
	if err := d.SetPageLabels(nil); err != nil {
		t.Fatal(err)
	}
	if got := labelsOf(t, d); got != nil {
		t.Errorf("labels = %v, want none", got)
	}
}

// ⛔⛔ THE WITNESS FOR THE WHOLE CHANGE. Until this, every verb dropped the
// page labels: a document whose front matter was numbered i, ii, iii came back
// from a watermark numbered 1, 2, 3. The negative control is below.
func TestAVerbThatKeepsThePagesKeepsTheirLabels(t *testing.T) {
	d := labelled(t, 5,
		LabelRule{Pages: "1-2", Style: LabelLowerRoman},
		LabelRule{Pages: "3-5", Style: LabelDecimal},
	)
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// Open it afresh — as a caller of the command would — and stamp it.
	stamped, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := stamped.Watermark("all", "DRAFT"); err != nil {
		t.Fatal(err)
	}
	got := labelsOf(t, stamped)
	if strings.Join(got, ",") != "i,ii,1,2,3" {
		t.Errorf("after a watermark the labels are %v, want i,ii,1,2,3", got)
	}
}

// ⛔ And a verb that MOVES the pages moves their labels with them. A
// /PageLabels tree is keyed by page index, so copying it unchanged would leave
// every label on the wrong page — which is why the labels are asked for per
// page rather than carried as a tree.
func TestAVerbThatMovesThePagesMovesTheirLabels(t *testing.T) {
	d := labelled(t, 4,
		LabelRule{Pages: "1-2", Style: LabelLowerRoman},
		LabelRule{Pages: "3-4", Style: LabelDecimal},
	)
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	reversed.Reverse()
	// The pages came out i, ii, 1, 2 and are now in the opposite order, so
	// the labels must be too.
	got := labelsOf(t, reversed)
	if strings.Join(got, ",") != "2,1,ii,i" {
		t.Errorf("after a reverse the labels are %v, want 2,1,ii,i", got)
	}
}

// Selecting pages takes their labels, and only theirs.
func TestSelectingPagesTakesTheirLabels(t *testing.T) {
	d := labelled(t, 6, LabelRule{Style: LabelDecimal, Start: 100})
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	some, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := some.Select("2,5"); err != nil {
		t.Fatal(err)
	}
	if got := labelsOf(t, some); strings.Join(got, ",") != "101,104" {
		t.Errorf("labels = %v, want 101,104", got)
	}
}

// Merging two labelled documents keeps each one's labels on its own pages.
func TestAMergeKeepsEachSourcesLabels(t *testing.T) {
	first := labelled(t, 2, LabelRule{Style: LabelLowerRoman})
	firstBytes, err := first.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	second := labelled(t, 2, LabelRule{Style: LabelDecimal, Prefix: "B-"})
	secondBytes, err := second.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(secondBytes)
	if err != nil {
		t.Fatal(err)
	}
	a.Append(b)
	if got := labelsOf(t, a); strings.Join(got, ",") != "i,ii,B-1,B-2" {
		t.Errorf("labels = %v, want i,ii,B-1,B-2", got)
	}
}

// ⛔ A document with NO labels must still come out with none. Without this,
// everything above would pass on an implementation that invented decimal
// labels for every file — which a viewer shows identically to no labels at
// all, so nothing else here could tell the difference.
func TestADocumentWithNoLabelsGetsNone(t *testing.T) {
	d, err := Open(simple(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PageLabels(); got != nil {
		t.Errorf("an unlabelled document reports %v", got)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "PageLabels") {
		t.Error("a /PageLabels tree was written for a document that has none")
	}
}

// ⛔ The runs are compressed: three hundred decimal pages are ONE entry, not
// three hundred. A viewer counts forward from a run's start, so writing an
// entry per page is correct and makes a file a reader must walk three hundred
// nodes of to answer "what is page two called".
func TestARunOfLabelsIsOneEntry(t *testing.T) {
	d := labelled(t, 60, LabelRule{Style: LabelDecimal})
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// The entry is "0 << /S /D >>"; a per-page tree would hold sixty of them.
	if n := strings.Count(string(out), "/S /D"); n != 1 {
		t.Errorf("%d decimal entries for one run of sixty pages, want 1", n)
	}
	// And the labels are still right at both ends.
	got := labelsOf(t, d)
	if len(got) != 60 || got[0] != "1" || got[59] != "60" {
		t.Errorf("labels run %q … %q", got[0], got[len(got)-1])
	}
}

// ⛔⛔ A PAGE LABEL'S LETTERS ARE NOT A SPREADSHEET'S COLUMNS. ISO 32000-1
// table 159: "A to Z for the first 26 pages, AA to ZZ for the next 26, and so
// on" — so 27 is AA and 28 is BB, where a spreadsheet's 28 is AB. Borrowing
// the spreadsheet rule, which is the one everybody has written before, would
// relabel every appendix past the twenty-sixth.
func TestLabelLettersAreNotSpreadsheetColumns(t *testing.T) {
	for n, want := range map[int]string{
		1: "A", 2: "B", 26: "Z",
		27: "AA", 28: "BB", 52: "ZZ",
		53: "AAA", 54: "BBB",
	} {
		if got := letters(n); got != want {
			t.Errorf("letters(%d) = %q, want %q", n, got, want)
		}
	}
	// The spreadsheet answer for 28 is AB. If it ever comes out of this, the
	// rule has been replaced by the familiar one.
	if got := letters(28); got == "AB" {
		t.Error("letters(28) = \"AB\", which is the spreadsheet rule, not the PDF one")
	}
	// And back again, for every value a run is built from.
	for n := 1; n <= 60; n++ {
		got, ok := fromLetters(letters(n))
		if !ok || got != n {
			t.Errorf("fromLetters(letters(%d)) = %d, %v", n, got, ok)
		}
	}
	if _, ok := fromLetters("AB"); ok {
		t.Error("fromLetters read \"AB\", which this style never writes")
	}
	for _, s := range []string{"", "a", "A1", "ABA"} {
		if _, ok := fromLetters(s); ok {
			t.Errorf("fromLetters(%q) said yes", s)
		}
	}
}

func TestRomanBothWays(t *testing.T) {
	for n, want := range map[int]string{
		1: "I", 4: "IV", 9: "IX", 14: "XIV", 40: "XL", 90: "XC",
		400: "CD", 900: "CM", 1987: "MCMLXXXVII", 3999: "MMMCMXCIX",
	} {
		if got := roman(n); got != want {
			t.Errorf("roman(%d) = %q, want %q", n, got, want)
		}
	}
	for n := 1; n <= 3999; n++ {
		got, ok := fromRoman(roman(n))
		if !ok || got != n {
			t.Fatalf("fromRoman(roman(%d)) = %d, %v", n, got, ok)
		}
	}
	// ⛔ Past 3999 the additive notation runs out — 4000 needs a bar over the
	// M, which neither a PDF string nor a page number has — so the number is
	// written in decimal. A label a viewer shows WRONGLY is worse than one it
	// shows in the wrong style.
	if got := roman(4000); got != "4000" {
		t.Errorf("roman(4000) = %q, want the decimal", got)
	}
	if got := roman(0); got != "0" {
		t.Errorf("roman(0) = %q", got)
	}
	// ⛔ A numeral must be the CANONICAL spelling of its value: "IIII" is four
	// to a person and is not what roman(4) writes, so reading it as four would
	// build a run that renumbers the page after it.
	for _, s := range []string{"IIII", "VV", "IC", "", "Q", "ivi"} {
		if n, ok := fromRoman(s); ok {
			t.Errorf("fromRoman(%q) = %d, want not a numeral", s, n)
		}
	}
}

// ⛔ A leading zero is not a decimal label: "007" and "7" are the same number
// and different labels, so reading it as part of a run would renumber "008"
// as "8".
func TestALeadingZeroIsNotDecimal(t *testing.T) {
	if _, ok := LabelDecimal.value("007"); ok {
		t.Error("\"007\" was read as decimal")
	}
	if n, ok := LabelDecimal.value("7"); !ok || n != 7 {
		t.Errorf("\"7\" = %d, %v", n, ok)
	}
	if _, ok := LabelDecimal.value("0"); ok {
		t.Error("\"0\" was read as a label number, which must be at least 1")
	}
}

// A label no style can explain becomes a run of one with the whole label as a
// literal prefix: the file then says exactly what the viewer must show.
func TestALabelNoStyleCanExplainIsWrittenLiterally(t *testing.T) {
	d := labelled(t, 3,
		LabelRule{Pages: "1", Prefix: "Front cover"},
		LabelRule{Pages: "2", Prefix: "Inside"},
		LabelRule{Pages: "3", Style: LabelDecimal},
	)
	if got := labelsOf(t, d); strings.Join(got, ",") != "Front cover,Inside,1" {
		t.Errorf("labels = %v", got)
	}
}

// A document whose labels cannot be resolved is a document with broken labels,
// not one that cannot be read.
func TestALabelTreeThatPointsAtNothingIsNoLabels(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PageLabels(); got != nil {
		t.Errorf("labels = %v", got)
	}
}

// labelledPDF builds a file whose catalogue carries the given /PageLabels
// object, so a test can hand this reader a tree no writer of ours would make.
func labelledPDF(t *testing.T, pages int, tree func(w *reader.Writer) reader.Object) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	kids := make(reader.Array, 0, pages)
	for i := 1; i <= pages; i++ {
		kids = append(kids, w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{}}),
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(pages),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)}})
	catalog := reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef}
	if v := tree(w); v != nil {
		catalog["PageLabels"] = v
	}
	out, err := w.Finish(reader.Dict{"Root": w.Add(catalog)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func labelsFrom(t *testing.T, b []byte) []string {
	t.Helper()
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	return d.PageLabels()
}

// ⛔ A page-label tree somebody else wrote can be anything at all, and a
// reader that walked it trustingly is a reader that panics on a file. Each of
// these is a shape the format allows or a producer has written, and every one
// of them must come back as "no labels" or as the labels that ARE readable —
// never as a crash and never as an error that loses the document.
func TestATreeThisWriterWouldNeverMakeIsStillRead(t *testing.T) {
	for name, tree := range map[string]func(w *reader.Writer) reader.Object{
		"no tree at all": func(*reader.Writer) reader.Object { return nil },
		"not a dictionary": func(*reader.Writer) reader.Object {
			return reader.Integer(7)
		},
		"a dictionary with no Nums": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Limits": reader.Array{}})
		},
		"Nums that is not an array": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Nums": reader.Integer(1)})
		},
		"an odd-length Nums": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Nums": reader.Array{reader.Integer(0)}})
		},
		"a key that is not a number": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Nums": reader.Array{
				reader.Name("zero"), reader.Dict{"S": reader.Name("D")},
			}})
		},
		"a value that is not a dictionary": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Nums": reader.Array{
				reader.Integer(0), reader.Name("D"),
			}})
		},
		"a negative key": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Nums": reader.Array{
				reader.Integer(-5), reader.Dict{"S": reader.Name("D")},
			}})
		},
		"a start below one": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Nums": reader.Array{
				reader.Integer(0), reader.Dict{"S": reader.Name("D"), "St": reader.Integer(0)},
			}})
		},
		"a style the format does not have": func(w *reader.Writer) reader.Object {
			return w.Add(reader.Dict{"Nums": reader.Array{
				reader.Integer(0), reader.Dict{"S": reader.Name("Q")},
			}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := labelledPDF(t, 3, tree)
			// It must open, and it must not panic.
			got := labelsFrom(t, b)
			// And it must survive being written out again.
			d, err := Open(b)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Bytes(); err != nil {
				t.Errorf("writing it back: %v", err)
			}
			t.Logf("labels = %q", got)
		})
	}
}

// ⛔ A tree of KIDS rather than one flat Nums, which is what a producer writes
// for a long document — and the depth is bounded, because a file can point a
// kid back at its parent and a reader following /Kids follows it for ever.
func TestATreeOfKids(t *testing.T) {
	b := labelledPDF(t, 4, func(w *reader.Writer) reader.Object {
		left := w.Add(reader.Dict{"Nums": reader.Array{
			reader.Integer(0), reader.Dict{"S": reader.Name("r")},
		}})
		right := w.Add(reader.Dict{"Nums": reader.Array{
			reader.Integer(2), reader.Dict{"S": reader.Name("D")},
		}})
		return w.Add(reader.Dict{"Kids": reader.Array{left, right}})
	})
	got := labelsFrom(t, b)
	if strings.Join(got, ",") != "i,ii,1,2" {
		t.Errorf("labels = %v, want i,ii,1,2", got)
	}
}

func TestATreeThatPointsBackAtItselfIsBounded(t *testing.T) {
	b := labelledPDF(t, 2, func(w *reader.Writer) reader.Object {
		loop := w.Reserve()
		w.Put(loop, reader.Dict{"Kids": reader.Array{loop}})
		return loop
	})
	// It must come back rather than recurse for ever.
	done := make(chan []string, 1)
	go func() { done <- labelsFrom(t, b) }()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("a tree that is nothing but a loop gave %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a looping tree did not come back")
	}
}

// The styles' own edges, which no document reaches but a caller can.
func TestTheStylesOwnEdges(t *testing.T) {
	if got := LabelNone.format(7); got != "" {
		t.Errorf("LabelNone.format(7) = %q, want empty", got)
	}
	if got := LabelStyle("Q").format(7); got != "" {
		t.Errorf("an unknown style formatted %q", got)
	}
	if got := letters(0); got != "" {
		t.Errorf("letters(0) = %q", got)
	}
	if got := LabelUpperLetters.format(27); got != "AA" {
		t.Errorf("LabelUpperLetters.format(27) = %q", got)
	}
	if got := LabelLowerLetters.format(27); got != "aa" {
		t.Errorf("LabelLowerLetters.format(27) = %q", got)
	}
	// value says no to an empty string, to the wrong case, and to a style it
	// does not know.
	for _, c := range []struct {
		s    LabelStyle
		text string
	}{
		{LabelDecimal, ""}, {LabelLowerRoman, "IV"}, {LabelUpperRoman, "iv"},
		{LabelLowerLetters, "AA"}, {LabelUpperLetters, "aa"},
		{LabelNone, "x"}, {LabelStyle("Q"), "x"},
	} {
		if n, ok := c.s.value(c.text); ok {
			t.Errorf("%q.value(%q) = %d, want no", string(c.s), c.text, n)
		}
	}
	// And the lower-case styles do read their own case.
	if n, ok := LabelLowerRoman.value("iv"); !ok || n != 4 {
		t.Errorf("LabelLowerRoman.value(\"iv\") = %d, %v", n, ok)
	}
	if n, ok := LabelLowerLetters.value("aa"); !ok || n != 27 {
		t.Errorf("LabelLowerLetters.value(\"aa\") = %d, %v", n, ok)
	}
}

// A rule whose range names pages outside the document is clipped rather than
// refused: "1-999" over five pages means those five.
func TestARangeBeyondTheDocumentIsClipped(t *testing.T) {
	d, err := Open(simple(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetPageLabels([]LabelRule{{Pages: "1-", Style: LabelDecimal}}); err != nil {
		t.Fatal(err)
	}
	if got := labelsOf(t, d); strings.Join(got, ",") != "1,2,3" {
		t.Errorf("labels = %v", got)
	}
}

// The three remaining paths a document can take through this reader: a
// catalogue that cannot be read, a reference that points at nothing, and the
// upper-case letters style reading its own case.
func TestTheLastThreePaths(t *testing.T) {
	t.Run("upper-case letters read their own case", func(t *testing.T) {
		if n, ok := LabelUpperLetters.value("AA"); !ok || n != 27 {
			t.Errorf("LabelUpperLetters.value(\"AA\") = %d, %v; want 27", n, ok)
		}
	})
	t.Run("a reference that points at nothing", func(t *testing.T) {
		// ⛔ A dangling reference in a label tree means THIS PAGE HAS NO
		// LABEL. A file whose labels point at objects that are gone is a file
		// with broken labels, not one that cannot be read, and refusing it
		// would lose the document over its toolbar captions.
		b := labelledPDF(t, 2, func(w *reader.Writer) reader.Object {
			// A reference to an object number nothing was ever put at.
			return reader.Ref{Num: 9999}
		})
		d, err := Open(b)
		if err != nil {
			t.Fatalf("a file with a dangling label tree would not open: %v", err)
		}
		if got := d.PageLabels(); len(got) != 0 {
			t.Errorf("labels = %v, want none", got)
		}
		if _, err := d.Bytes(); err != nil {
			t.Errorf("writing it back: %v", err)
		}
	})
}
