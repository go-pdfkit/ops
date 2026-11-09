// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/go-pdfkit/reader"
)

// Telling two versions of a document apart.
//
// # What this is parity with
//
// BentoPDF's compare-pdfs, read at docs/tools/compare-pdfs.md: "Upload two
// PDFs and see exactly what changed between them. The tool pairs pages
// intelligently, detects differences in text, images, annotations, headers,
// and formatting". Its pairing is "automatic page pairing based on content
// similarity"; its answers are categorised, and the categories a reader can
// filter by are text, image, header/footer, annotation, formatting and
// background.
//
// What is here is the half a library can own: the PAIRING and the categories
// that can be read out of the file — text, pictures, annotations — plus the two
// categories its page pairing implies and its own list does not name, a page
// added and a page removed. Side by side and overlay are a viewer's job and
// belong in the workbench; "formatting" and "background" are a rendering
// comparison, which is a different instrument and a different cost.
//
// ⛔ THE DIFF IS GENERIC AND LIVES HERE ANYWAY, for now. A word-level diff is
// nobody's idea of a PDF operation, and the fleet has no library for one —
// `fleetfind diff` answers with rdiff and vcdiff, which are byte deltas for
// synchronising files, not Myers. It moves to a library of its own the day a
// second caller wants it; one caller is not a shared concern, and a repository
// is not free.
//
// The algorithm is Myers, E. W., "An O(ND) Difference Algorithm and Its
// Variations", Algorithmica 1 (1986) 251-266: the greedy forward pass that
// follows the furthest-reaching D-path on each diagonal, with the whole trace
// kept so the script can be walked back.

// A ChangeKind says what sort of difference was found. They are the categories
// a reader filters by, and they are strings so that a report, a command and a
// workbench panel all name a change the same way.
type ChangeKind string

// The kinds of change this can see.
const (
	// PageAdded and PageRemoved are pages the pairing could find no partner
	// for. ⛔ A page REPLACED wholesale is reported as both, because "this page
	// became that one" is a claim about intent that nothing in the file
	// supports: two pages with no words in common are two pages.
	PageAdded   ChangeKind = "page added"
	PageRemoved ChangeKind = "page removed"

	// TextAdded and TextRemoved carry the words themselves, in the order the
	// page draws them.
	TextAdded   ChangeKind = "text added"
	TextRemoved ChangeKind = "text removed"

	// PictureAdded and PictureRemoved carry a picture's size and how its
	// samples are stored. A picture whose BYTES changed is both: removed and
	// added, since there is no sense in which one bitmap "became" another.
	PictureAdded   ChangeKind = "picture added"
	PictureRemoved ChangeKind = "picture removed"

	// AnnotationAdded and AnnotationRemoved carry the annotation's subtype — a
	// note, a highlight, a link — and its own text where it has any.
	AnnotationAdded   ChangeKind = "annotation added"
	AnnotationRemoved ChangeKind = "annotation removed"
)

// A Change is one difference found on one pair of pages.
type Change struct {
	Kind ChangeKind
	// What says what changed, in the terms of the kind: the words for a text
	// change, the size and filter for a picture, the subtype and contents for
	// an annotation. Empty for a page added or removed, whose page numbers are
	// on the pair.
	What string
}

// A PagePair is one page of the earlier document set beside the page of the
// later one that answers to it, and everything that differs between them.
//
// Before and After are page numbers counting from one. One of them is zero
// when the pairing found no partner: a page only the later document has is
// {Before: 0, After: n}.
type PagePair struct {
	Before, After int
	Changes       []Change
}

// Changed reports whether anything at all differs on this pair.
func (p PagePair) Changed() bool { return len(p.Changes) > 0 }

// pairingFloor is how alike two pages must be before they are called the same
// page changed rather than one page removed and another added.
//
// ⛔ A THRESHOLD, not a best match. Without it the alignment pairs the least
// unalike pages it can find, which on a document whose middle chapter was
// replaced reports every line of both chapters as an edit of the other — a
// diff nobody can read. Half the words in common is the point at which a reader
// still recognises the page.
const pairingFloor = 0.5

// pairingCells bounds the alignment, which is a cell per pair of pages and a
// word comparison in each.
//
// ⛔ A BOUND, because the input is two files and a file can say it has a
// hundred thousand pages. Past it the pages are paired where they stand, which
// is what a reader of two versions of the same document gets anyway, and the
// report says nothing untrue — only less.
// ⛔ A var, so that a test can lower it and watch the fallback happen. A
// constant here would be a branch no test could reach without building a
// document of two thousand pages, and an unreachable guard is an untested
// claim.
var pairingCells = 4_000_000

// Compare says what changed between two documents, page by page.
//
// The pages are paired by how much text they share rather than by their
// numbers, so a page inserted in the middle moves nothing after it: the pages
// that follow are still paired with the pages they came from, and the
// insertion is one PageAdded.
func Compare(before, after []byte) ([]PagePair, error) {
	was, err := reader.Open(before)
	if err != nil {
		return nil, fmt.Errorf("ops: the earlier document could not be read: %w", err)
	}
	now, err := reader.Open(after)
	if err != nil {
		return nil, fmt.Errorf("ops: the later document could not be read: %w", err)
	}
	wasWords, err := wordsOfEveryPage(was, "earlier")
	if err != nil {
		return nil, err
	}
	nowWords, err := wordsOfEveryPage(now, "later")
	if err != nil {
		return nil, err
	}

	var out []PagePair
	for _, p := range alignPages(wasWords, nowWords) {
		pair := PagePair{Before: p.before, After: p.after}
		switch {
		case pair.Before == 0:
			pair.Changes = append(pair.Changes, Change{Kind: PageAdded})
		case pair.After == 0:
			pair.Changes = append(pair.Changes, Change{Kind: PageRemoved})
		default:
			added, removed := diffWords(wasWords[pair.Before-1], nowWords[pair.After-1])
			for _, run := range removed {
				pair.Changes = append(pair.Changes, Change{Kind: TextRemoved, What: strings.Join(run, " ")})
			}
			for _, run := range added {
				pair.Changes = append(pair.Changes, Change{Kind: TextAdded, What: strings.Join(run, " ")})
			}
			more, err := thingsOnThePages(was, now, pair.Before, pair.After)
			if err != nil {
				return nil, err
			}
			pair.Changes = append(pair.Changes, more...)
		}
		out = append(out, pair)
	}
	return out, nil
}

// wordsOfEveryPage reads back what each page says, as words.
//
// ⛔ WORDS, which is a choice with a limit worth saying out loud: a script that
// does not put spaces between words — Chinese, Japanese — gives one "word" a
// line or a page, so a change inside it is reported as the whole of it
// changing. Splitting those correctly is a segmentation problem with its own
// dictionaries, and guessing at it would report changes that are not there.
func wordsOfEveryPage(d *reader.Document, which string) ([][]string, error) {
	out := make([][]string, 0, d.PageCount())
	for i := 1; i <= d.PageCount(); i++ {
		text, err := extractText(d, i)
		if err != nil {
			return nil, fmt.Errorf("ops: page %d of the %s document could not be read: %w", i, which, err)
		}
		out = append(out, strings.Fields(text))
	}
	return out, nil
}

// aligned is one row of the pairing: page numbers counting from one, zero for
// a page with no partner.
type aligned struct{ before, after int }

// alignPages decides which page of the earlier document each page of the later
// one answers to.
//
// It is a longest-common-subsequence alignment over the pages, scoring a pair
// by how much text the two share and refusing any pair under [pairingFloor] —
// so a page can be matched out of its position but never to a page nobody would
// call the same page.
func alignPages(was, now [][]string) []aligned {
	if len(was)*len(now) > pairingCells {
		// ⛔ Past the bound, pair where they stand. Said in the shape of the
		// answer rather than as an error, because a report of the pages that
		// line up is worth more than a refusal to look at a long document.
		return pairedInOrder(len(was), len(now))
	}
	// best[i][j] is the most similarity reachable using the first i pages of
	// the earlier document and the first j of the later one.
	best := make([][]float64, len(was)+1)
	for i := range best {
		best[i] = make([]float64, len(now)+1)
	}
	for i := 1; i <= len(was); i++ {
		for j := 1; j <= len(now); j++ {
			skip := max(best[i-1][j], best[i][j-1])
			take := 0.0
			if s := sharedWords(was[i-1], now[j-1]); s >= pairingFloor {
				take = best[i-1][j-1] + s
			}
			best[i][j] = max(skip, take)
		}
	}
	// Walk it back, which gives the pairs in reverse order.
	var rev []aligned
	i, j := len(was), len(now)
	for i > 0 && j > 0 {
		s := sharedWords(was[i-1], now[j-1])
		if s >= pairingFloor && best[i][j] == best[i-1][j-1]+s {
			rev = append(rev, aligned{before: i, after: j})
			i, j = i-1, j-1
			continue
		}
		// ⛔ The LATER document's unpaired page is taken first, so that after
		// the reversal a page replaced wholesale reads "page removed" and then
		// "page added": the document that came first is read first, which is
		// the order somebody comparing two drafts has them in.
		if best[i][j-1] >= best[i-1][j] {
			rev = append(rev, aligned{after: j})
			j--
			continue
		}
		rev = append(rev, aligned{before: i})
		i--
	}
	for ; i > 0; i-- {
		rev = append(rev, aligned{before: i})
	}
	for ; j > 0; j-- {
		rev = append(rev, aligned{after: j})
	}
	out := make([]aligned, 0, len(rev))
	for k := len(rev) - 1; k >= 0; k-- {
		out = append(out, rev[k])
	}
	return out
}

// pairedInOrder is the pairing of last resort: page one with page one, and the
// tail of the longer document unpaired.
func pairedInOrder(was, now int) []aligned {
	var out []aligned
	for i := 1; i <= was || i <= now; i++ {
		var p aligned
		if i <= was {
			p.before = i
		}
		if i <= now {
			p.after = i
		}
		out = append(out, p)
	}
	return out
}

// sharedWords is how much of two pages' text is the same, counting repeats: the
// size of the multiset intersection over the size of the larger page.
//
// Two pages with nothing on them are the same page; a page with nothing on it
// and a page with something on it share nothing.
func sharedWords(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	left := make(map[string]int, len(a))
	for _, w := range a {
		left[w]++
	}
	common := 0
	for _, w := range b {
		if left[w] > 0 {
			left[w]--
			common++
		}
	}
	return float64(common) / float64(max(len(a), len(b)))
}

// diffEdits bounds the Myers pass, which keeps a trace of D rows of up to
// 2D+1 diagonals: the memory is quadratic in the number of differences, so a
// pair of pages with nothing in common must not be allowed to ask for it.
// A var for the same reason as [pairingCells]: two pages that differ by two
// thousand words are easy to describe and tiresome to build.
var diffEdits = 2000

// diffWords is the words added and the words removed between two pages, as
// runs in the order the pages draw them.
//
// ⛔ RUNS, not single words. "the first chapter" becoming "the second chapter"
// is one word removed and one added, and reporting them as two separate
// changes next to each other reads as two edits. A reader wants the phrase.
func diffWords(was, now []string) (added, removed [][]string) {
	script, ok := myers(was, now)
	if !ok {
		// Past the bound the honest answer is the whole of both pages: saying
		// "these words changed" when the walk was abandoned half way would be
		// a diff of the part that happened to be cheap.
		if len(was) > 0 {
			removed = [][]string{was}
		}
		if len(now) > 0 {
			added = [][]string{now}
		}
		return added, removed
	}
	var run []string
	var kind byte
	flush := func() {
		if len(run) == 0 {
			return
		}
		if kind == '-' {
			removed = append(removed, run)
		} else {
			added = append(added, run)
		}
		run = nil
	}
	for _, e := range script {
		if e.kind == '=' {
			flush()
			kind = 0
			continue
		}
		if e.kind != kind {
			flush()
			kind = e.kind
		}
		run = append(run, e.word)
	}
	flush()
	return added, removed
}

// an edit is one word of the script: kept, removed or added.
type edit struct {
	kind byte // '=', '-' or '+'
	word string
}

// myers is the greedy forward pass of Myers' algorithm, returning the edit
// script and whether it finished inside [diffEdits] differences.
//
// v holds, for each diagonal k = x - y, the furthest x a path of d differences
// has reached. Each round extends every diagonal by one difference and then
// slides as far as the two pages agree, which is the whole idea: the sliding is
// free, so the work is proportional to the differences and not to the length.
//
// ⛔ THE WHOLE OF v IS KEPT EACH ROUND, not the diagonals this round touched.
// The first version stored a row of 2d+1 and filled it as it went — so a round
// that finished early left the diagonals after it as zeroes, and the walk back
// read those zeroes as real furthest points. It reported words removed and
// added that both pages had, and panicked on an index of −1 where the zeroes
// put it. The trace is the state, and half the state is not the state.
func myers(was, now []string) ([]edit, bool) {
	n, m := len(was), len(now)
	limit := min(n+m, diffEdits)
	offset := limit + 1
	v := make([]int, 2*limit+3)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		// Kept BEFORE the round, so trace[d] is what the walk back needs when
		// it undoes the dth difference: where the paths of d-1 had got to.
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && was[x] == now[y] {
				x, y = x+1, y+1
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return walkBack(was, now, trace, offset), true
			}
		}
	}
	return nil, false
}

// walkBack turns Myers' trace into the script, from the end to the beginning.
//
// At each depth the diagonal the path arrived on says which neighbour it came
// from — the same choice the forward pass made, read out of the state it made
// it from — and that says whether the step was a word added or a word removed.
// The diagonal run before the step is the words both pages share.
func walkBack(was, now []string, trace [][]int, offset int) []edit {
	x, y := len(was), len(now)
	var rev []edit
	for d := len(trace) - 1; d > 0; d-- {
		v, k := trace[d], x-y
		prevK := k - 1
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x, y = x-1, y-1
			rev = append(rev, edit{kind: '=', word: was[x]})
		}
		if x == prevX {
			y--
			rev = append(rev, edit{kind: '+', word: now[y]})
		} else {
			x--
			rev = append(rev, edit{kind: '-', word: was[x]})
		}
	}
	// What is left is the run the two pages agreed on before the first
	// difference, which is on diagonal zero: x and y are equal here.
	//
	// ⛔ AND NOTHING FOLLOWS IT. Two more loops stood here, emitting the words
	// of whichever page still had some — and neither could ever run. Undoing
	// every difference lands on the furthest point a path of NO differences
	// reached, which is the common prefix, so x == y by construction. An
	// unreachable guard is an untested claim, and the test that would have
	// covered these was never going to be written.
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		rev = append(rev, edit{kind: '=', word: was[x]})
	}
	out := make([]edit, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out
}

// thingsOnThePages compares what is on a pair of pages besides its words: the
// pictures it draws and the annotations it carries.
func thingsOnThePages(was, now *reader.Document, before, after int) ([]Change, error) {
	wasPics, err := pictureMarks(was, before)
	if err != nil {
		return nil, fmt.Errorf("ops: the pictures of page %d of the earlier document: %w", before, err)
	}
	nowPics, err := pictureMarks(now, after)
	if err != nil {
		return nil, fmt.Errorf("ops: the pictures of page %d of the later document: %w", after, err)
	}
	out := whatWentAndWhatCame(wasPics, nowPics, PictureRemoved, PictureAdded)

	wasNotes, err := annotationMarks(was, before)
	if err != nil {
		return nil, fmt.Errorf("ops: the annotations of page %d of the earlier document: %w", before, err)
	}
	nowNotes, err := annotationMarks(now, after)
	if err != nil {
		return nil, fmt.Errorf("ops: the annotations of page %d of the later document: %w", after, err)
	}
	return append(out, whatWentAndWhatCame(wasNotes, nowNotes, AnnotationRemoved, AnnotationAdded)...), nil
}

// whatWentAndWhatCame is the multiset difference of two lists of descriptions,
// both ways round. Repeats count: a page that drew one logo and now draws two
// has gained one.
func whatWentAndWhatCame(was, now []string, gone, came ChangeKind) []Change {
	var out []Change
	for _, what := range missingFrom(was, now) {
		out = append(out, Change{Kind: gone, What: what})
	}
	for _, what := range missingFrom(now, was) {
		out = append(out, Change{Kind: came, What: what})
	}
	return out
}

// missingFrom is what is in a and not in b, counting repeats, in a's order.
func missingFrom(a, b []string) []string {
	have := make(map[string]int, len(b))
	for _, s := range b {
		have[s]++
	}
	var out []string
	for _, s := range a {
		if have[s] > 0 {
			have[s]--
			continue
		}
		out = append(out, s)
	}
	return out
}

// pictureMarks describes each picture a page draws, closely enough that two
// different pictures are told apart and the same picture drawn twice is not.
//
// ⛔ THE SAMPLES ARE HASHED, not compared. Two pictures of the same size in the
// same format are not the same picture, and holding both documents' bitmaps in
// memory to find that out would be a way of turning a comparison into an
// exhausted machine.
func pictureMarks(d *reader.Document, page int) ([]string, error) {
	images, err := extractImages(d, page)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(images))
	for _, im := range images {
		filter := string(im.Filter)
		if filter == "" {
			filter = "plain"
		}
		sum := sha256.Sum256(im.Data)
		out = append(out, fmt.Sprintf("%d by %d, %s, %x", im.Width, im.Height, filter, sum[:6]))
	}
	return out, nil
}

// annotationMarks describes each annotation on a page by what it is and what it
// says, which is what a reader comparing two drafts is looking for.
func annotationMarks(d *reader.Document, page int) ([]string, error) {
	// ⛔ The error is dropped on purpose: the page number comes from the
	// alignment of this document's own pages, so there is no page here the
	// document does not have. A guard for it would be a branch no test could
	// reach. A dictionary that did not arrive is empty, and an empty page
	// carries no annotations — which is the same answer.
	dict, _ := d.Page(page)
	annots, err := d.Resolve(dict.Get("Annots"))
	if err != nil {
		return nil, err
	}
	list, ok := reader.ToArray(annots)
	if !ok {
		return nil, nil
	}
	var out []string
	for _, ref := range list {
		o, err := d.Resolve(ref)
		if err != nil {
			return nil, err
		}
		a, ok := o.(reader.Dict)
		if !ok {
			continue
		}
		what := "an annotation"
		if sub, ok := a.Get("Subtype").(reader.Name); ok {
			what = "/" + string(sub)
		}
		if says, ok := reader.TextString(a.Get("Contents")); ok && says != "" {
			what += " saying " + says
		}
		out = append(out, what)
	}
	return out, nil
}
