// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// pagesNamed builds a document whose pages say what the caller asks, so the
// order they come out in can be read rather than inferred.
func pagesNamed(t *testing.T, names ...string) *Doc {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	kids := make(reader.Array, 0, len(names))
	for _, n := range names {
		kids = append(kids, w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(n)}),
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(len(names)),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)}})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTheFrontsAndTheBacksBecomeTheDocument(t *testing.T) {
	// What a single-sided feeder leaves behind: the fronts in one file and the
	// backs in another. Merging them end to end gives every front then every
	// back; this gives the document.
	fronts := pagesNamed(t, "1", "3", "5")
	backs := pagesNamed(t, "2", "4", "6")
	if err := fronts.Interleave(backs); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, fronts), " "); got != "1 2 3 4 5 6" {
		t.Errorf("came out as %q", got)
	}
}

func TestAShorterPileSimplyRunsOut(t *testing.T) {
	// Which is what a scanner leaves when the last sheet is single-sided.
	fronts := pagesNamed(t, "1", "3", "5")
	backs := pagesNamed(t, "2", "4")
	if err := fronts.Interleave(backs); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, fronts), " "); got != "1 2 3 4 5" {
		t.Errorf("came out as %q", got)
	}
}

func TestMoreThanTwoPiles(t *testing.T) {
	a, b, c := pagesNamed(t, "1", "4"), pagesNamed(t, "2", "5"), pagesNamed(t, "3", "6")
	if err := a.Interleave(b, c); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, a), " "); got != "1 2 3 4 5 6" {
		t.Errorf("came out as %q", got)
	}
}

func TestInterleavingWithNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		build  func(t *testing.T) (*Doc, []*Doc)
		reason string
	}{
		{"a document that is not there", func(t *testing.T) (*Doc, []*Doc) {
			return pagesNamed(t, "1"), []*Doc{nil}
		}, "cannot interleave with nothing"},
		{"no pages anywhere", func(t *testing.T) (*Doc, []*Doc) {
			return New(), []*Doc{New()}
		}, "no pages to interleave"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, others := tc.build(t)
			err := d.Interleave(others...)
			if err == nil {
				t.Fatal("it went ahead anyway")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("it said %q", err)
			}
		})
	}
}

func TestInterleavingOneDocumentWithItself(t *testing.T) {
	// Nothing to interleave with is not an error: the document is already in
	// the order it is in.
	d := pagesNamed(t, "1", "2")
	if err := d.Interleave(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1 2" {
		t.Errorf("came out as %q", got)
	}
}

func TestEveryPageOnOneSheet(t *testing.T) {
	// A page is a unit of paper, not a unit of reading.
	d := pagesNamed(t, "1", "2", "3")
	if err := d.OnePage(); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if src.PageCount() != 1 {
		t.Fatalf("%d pages, want 1", src.PageCount())
	}
	page, err := src.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	box, _ := reader.ToArray(resolved(src, page.Get("MediaBox")))
	w, _ := reader.ToFloat(resolved(src, box[2]))
	h, _ := reader.ToFloat(resolved(src, box[3]))
	// As wide as the widest page, as tall as all of them stacked.
	if w != 100 || h != 600 {
		t.Errorf("the sheet is %g by %g, want 100 by 600", w, h)
	}
	// And the pages are stacked from the top down. A PDF's origin is at the
	// bottom, so the FIRST page has the largest offset and the last sits at
	// zero. Getting this backwards prints the document upside down and every
	// page is still there, which is why it is asserted rather than looked at.
	content, err := src.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"q 1 0 0 1 0 400 cm /Tile0 Do Q",
		"q 1 0 0 1 0 200 cm /Tile1 Do Q",
		"q 1 0 0 1 0 0 cm /Tile2 Do Q",
	}
	at := -1
	for _, line := range want {
		i := strings.Index(string(content), line)
		if i < 0 {
			t.Fatalf("%q is not in the sheet:\n%s", line, content)
		}
		if i < at {
			t.Errorf("%q comes out of order", line)
		}
		at = i
	}
}

func TestANarrowerPageIsCentred(t *testing.T) {
	// A column of text that jumps from side to side is harder to read than one
	// that does not.
	d := New()
	d.Blank(100, 50)
	d.Blank(60, 50)
	if err := d.OnePage(); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	content, err := src.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	// (100-60)/2 = 20 across, and below the first page.
	if !strings.Contains(string(content), "1 0 0 1 20 0 cm") {
		t.Errorf("the narrow page is not centred:\n%s", content)
	}
}

func TestStackingWhatCannotBeStacked(t *testing.T) {
	if err := New().OnePage(); err == nil {
		t.Error("an empty document was stacked")
	}
	// One page is already one page.
	d := pagesNamed(t, "1")
	if err := d.OnePage(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1" {
		t.Errorf("came out as %q", got)
	}
}

func TestStackingPagesOfNoSize(t *testing.T) {
	d := New()
	d.Blank(0, 0)
	d.Blank(0, 0)
	if err := d.OnePage(); err == nil {
		t.Error("pages of no size were stacked")
	}
}

func TestWhatTheOtherDocumentsBringWithThem(t *testing.T) {
	// Interleaving two documents makes one, and it has to be a document a
	// reader of the newer of them can open: the version is the higher of the
	// two, as it is when they are merged end to end.
	older := pagesNamed(t, "1")
	newer := pagesNamed(t, "2")
	older.version, newer.version = "1.4", "2.0"
	if err := older.Interleave(newer); err != nil {
		t.Fatal(err)
	}
	if older.version != "2.0" {
		t.Errorf("the joined document says version %q", older.version)
	}

	// And a document that says nothing about itself takes what the other says,
	// rather than losing it.
	blank := pagesNamed(t, "1")
	titled := pagesNamed(t, "2")
	blank.info = nil
	titled.info = reader.Dict{"Title": reader.String("a title")}
	if err := blank.Interleave(titled); err != nil {
		t.Fatal(err)
	}
	if blank.info == nil {
		t.Error("the title was dropped on the way")
	}
}

// ⛔ Every assertion here is on the ORDER. A duplex collation that produced
// the right NUMBER of pages in the wrong order is the whole defect, and a
// count cannot see it.
func TestDuplexCollateWithTheBacksReversed(t *testing.T) {
	// Six sheets' worth: the fronts in order, then the backs as a flipped
	// stack leaves them — the last sheet's back first.
	d := pagesNamed(t, "1", "3", "5", "6", "4", "2")
	if err := d.DuplexCollate(3, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1 2 3 4 5 6" {
		t.Errorf("came out as %q", got)
	}
}

func TestDuplexCollateWithTheBacksAlreadyInOrder(t *testing.T) {
	d := pagesNamed(t, "1", "3", "5", "2", "4", "6")
	if err := d.DuplexCollate(3, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1 2 3 4 5 6" {
		t.Errorf("came out as %q", got)
	}
}

func TestDuplexCollateRefusesWhatMakesNoSense(t *testing.T) {
	if err := pagesNamed(t).DuplexCollate(0, true); err == nil {
		t.Error("an empty document was collated")
	}
	d := pagesNamed(t, "1", "2")
	err := d.DuplexCollate(3, true)
	if err == nil {
		t.Fatal("a split past the end was accepted")
	}
	// The message says how many there are, because "cannot split at 3" over a
	// file whose length the caller does not know is a riddle.
	if msg := err.Error(); !strings.Contains(msg, "2 pages") {
		t.Errorf("the refusal does not say how many pages there are: %q", msg)
	}
}

// A split AT the end is not an error: it says "all fronts, no backs", which is
// what a single-sided batch is, and handing back the document unchanged is the
// honest answer.
func TestDuplexCollateOfAllFrontsIsTheDocument(t *testing.T) {
	d := pagesNamed(t, "1", "2", "3")
	if err := d.DuplexCollate(3, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1 2 3" {
		t.Errorf("came out as %q", got)
	}
}

// ⛔ The default split is ceil(n/2), NOT n/2, and this test had to be rewritten
// to show it. The first version used a REVERSED back block — and with the backs
// reversed the output is p0, p[n-1], p1, p[n-2] … whatever the split is, because
// the reversal absorbs the difference. It passed at the ceiling AND at the
// floor, so it proved nothing about the ceiling; the negative control below is
// what said so.
//
// With the backs already in order the split is load-bearing: five pages are
// three fronts and two backs, because an odd stack has its unpaired sheet at
// the END of the fronts.
func TestTheDefaultSplitTakesTheExtraSheetAsAFront(t *testing.T) {
	d := pagesNamed(t, "1", "3", "5", "2", "4")
	if err := d.DuplexCollate(0, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1 2 3 4 5" {
		t.Errorf("came out as %q", got)
	}
}

// The negative control for the test above: the FLOOR of the same stack must
// come out wrong, or the ceiling is not what makes it pass.
func TestTheFloorOfAnOddStackIsNotTheDocument(t *testing.T) {
	d := pagesNamed(t, "1", "3", "5", "2", "4")
	if err := d.DuplexCollate(2, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got == "1 2 3 4 5" {
		t.Error("splitting an odd stack at the floor gave the document, " +
			"so the ceiling in the default is not what makes the test above pass")
	}
}

// An odd stack with the backs reversed, which is the common scan — asserted for
// what it is rather than as evidence about the split.
func TestAnOddStackWithTheBacksReversed(t *testing.T) {
	d := pagesNamed(t, "1", "3", "5", "4", "2")
	if err := d.DuplexCollate(0, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1 2 3 4 5" {
		t.Errorf("came out as %q", got)
	}
}

// ⛔ The reversal must not reorder the fronts underneath itself. fronts and
// backs are two halves of ONE array, so reversing backs in place walks over
// the fronts whenever the backs are the longer half.
func TestReversingTheBacksLeavesTheFrontsAlone(t *testing.T) {
	d := pagesNamed(t, "1", "6", "4", "2")
	if err := d.DuplexCollate(1, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written(t, d), " "); got != "1 2 4 6" {
		t.Errorf("came out as %q", got)
	}
}
