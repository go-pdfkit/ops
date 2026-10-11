// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"strings"

	"github.com/go-pdfkit/reader"
)

// A LabelStyle is how the numeric part of a page label is written. They are
// the format's own one-letter names, so that what goes in the file is what the
// caller asked for.
type LabelStyle string

const (
	LabelDecimal      LabelStyle = "D" // 1, 2, 3
	LabelLowerRoman   LabelStyle = "r" // i, ii, iii
	LabelUpperRoman   LabelStyle = "R" // I, II, III
	LabelLowerLetters LabelStyle = "a" // a, b, c … z, aa, bb
	LabelUpperLetters LabelStyle = "A" // A, B, C … Z, AA, BB
	// LabelNone writes no number at all: the label is the prefix alone, which
	// is how a run of pages is marked "Cover" or "Insert".
	LabelNone LabelStyle = ""
)

// A LabelRule labels the pages a range names.
//
// Page labels are what a VIEWER shows in its toolbar — "iv of 320" — and never
// what is printed on the paper. For a number on the page, see [Doc.PageNumbers].
type LabelRule struct {
	// Pages is a range in this package's usual form: "1-4", "7", "odd",
	// "1-9,30-40". Empty means every page.
	Pages string
	// Style is how the number is written. The zero value writes no number,
	// which is [LabelNone] and is a style rather than an omission.
	Style LabelStyle
	// Prefix goes before the number: "A-" makes A-1, A-2, A-3.
	Prefix string
	// Start is the value of the first label in the range. Zero means one.
	//
	// ⛔ It must be at least one, and that is the FORMAT's rule rather than a
	// choice here: ISO 32000-1 table 159 says /St "shall be greater than or
	// equal to 1". A zero-based numbering scheme cannot be written as a
	// numeric style at all, so Start of less than one is refused rather than
	// written out of specification — a file that opens in one viewer and
	// renumbers itself in another is worse than a refusal.
	Start int
	// Continue carries the numbering across the pieces of a DISJOINT range.
	//
	// ⛔ A page-label range in a PDF is CONTIGUOUS: the file holds the first
	// page of each run and the viewer counts forward from there, so a rule
	// over "1-9,30-40" has to become two runs. Without Continue the second
	// run starts again at Start; with it, it carries on from where the first
	// left off. "odd" over a hundred pages is fifty runs of one page, and
	// both answers are expressible only because the rule is compiled into
	// runs rather than stored as written.
	Continue bool
}

// SetPageLabels replaces the document's page labels.
//
// The rules are applied in order, so a later rule wins where two overlap. A
// page no rule names has no label, and a viewer then shows its position in the
// file — which is the right answer and is what an unlabelled document does.
//
// Passing no rules removes the labels.
func (d *Doc) SetPageLabels(rules []LabelRule) error {
	if len(rules) == 0 {
		d.labels = nil
		d.labelsSet = true
		return nil
	}
	n := len(d.pages)
	if n == 0 {
		return fmt.Errorf("ops: an empty document has no pages to label")
	}
	// labels[i] is the label of page i, or "" for a page no rule named.
	labels := make([]string, n)
	for ri, r := range rules {
		// ⛔ Zero means the default of one; anything below it is refused. The
		// format's rule, not a choice here: ISO 32000-1 table 159 says /St
		// "shall be greater than or equal to 1", so a zero-based scheme is not
		// expressible as a numeric style and writing one anyway would make a
		// file that opens in one viewer and renumbers itself in another.
		if r.Start < 0 {
			return fmt.Errorf("ops: rule %d starts at %d, and a page label's "+
				"first value must be at least 1", ri+1, r.Start)
		}
		if err := r.Style.valid(); err != nil {
			return fmt.Errorf("ops: rule %d: %w", ri+1, err)
		}
		spec := r.Pages
		if spec == "" {
			spec = "all"
		}
		nums, err := ParseRange(spec, n)
		if err != nil {
			return fmt.Errorf("ops: rule %d: %w", ri+1, err)
		}
		start := r.Start
		if start == 0 {
			start = 1
		}
		// ⛔ The runs are found BEFORE the numbers are assigned, because
		// whether the numbering restarts depends on where a run ends — which
		// is a property of the whole range and not of one page.
		at := start
		prev := -2
		// ⛔ The page numbers are known to be inside the document: that is
		// [ParseRange]'s contract — it refuses a spec that names a page the
		// document has not got rather than clipping it, which ranges.go says
		// at length. A bounds check here was the first draft and it was DEAD
		// CODE: no spec reaches it, so no test could, and a guard no test can
		// reach is not a guard. If that contract ever loosens, labels[p-1]
		// panics on the broken invariant, which is a bug report rather than a
		// document with the wrong numbers in it.
		for _, p := range nums {
			if p != prev+1 {
				// A new run. It restarts unless the rule says to carry on.
				if !r.Continue {
					at = start
				}
			}
			labels[p-1] = r.Prefix + r.Style.format(at)
			at++
			prev = p
		}
	}
	d.labels = labels
	d.labelsSet = true
	return nil
}

// PageLabels reports what each page of the document is labelled, in order, as
// a viewer would show it. A page with no label comes back as "".
//
// It answers for the document as it stands: the labels a caller set, or the
// ones carried over from the files the pages came from.
func (d *Doc) PageLabels() []string {
	if d.labelsSet {
		out := make([]string, len(d.pages))
		copy(out, d.labels)
		return out
	}
	return d.carriedLabels()
}

// carriedLabels is what the pages are labelled in the files they came from.
//
// ⛔ Read per PAGE and not copied as a tree. A /PageLabels tree is keyed by
// page INDEX, so carrying it unchanged across a rebuild that reordered,
// removed or merged pages would leave every label on the wrong page — and
// dropping it instead, which is what this package did until now, loses the
// labels of a document that was only stamped. Asking each page what it was
// called in its own file is right for both.
func (d *Doc) carriedLabels() []string {
	out := make([]string, len(d.pages))
	any := false
	cache := map[*reader.Document][]string{}
	for i, p := range d.pages {
		if p.src == nil {
			continue
		}
		labels, seen := cache[p.src]
		if !seen {
			labels = sourceLabels(p.src)
			cache[p.src] = labels
		}
		if p.number >= 1 && p.number <= len(labels) && labels[p.number-1] != "" {
			out[i] = labels[p.number-1]
			any = true
		}
	}
	if !any {
		return nil
	}
	return out
}

// sourceLabels reads a document's /PageLabels into one label per page.
func sourceLabels(src *reader.Document) []string {
	// ⛔ The error is dropped WITHOUT a branch, deliberately. A document with
	// no readable catalogue has no page labels, and that is the same answer as
	// a document with a catalogue and no /PageLabels in it — so `if err !=
	// nil { return nil }` was a second way of saying the one thing, and a way
	// no test could reach, since a document whose catalogue cannot be read
	// does not get past Open. Indexing a nil Dict is nothing, which is the
	// answer.
	catalog, _ := src.Catalog()
	nums := numberTree(src, catalog["PageLabels"], 0)
	if len(nums) == 0 {
		return nil
	}
	count := src.PageCount()
	out := make([]string, count)
	// The runs are in key order: each one labels from its own key until the
	// next, so the ends have to be known before the labels can be written.
	for i, run := range nums {
		end := count
		if i+1 < len(nums) {
			end = nums[i+1].at
		}
		style := LabelStyle(run.style)
		at := run.start
		for p := run.at; p < end && p < count; p++ {
			if p < 0 {
				continue
			}
			out[p] = run.prefix + style.format(at)
			at++
		}
	}
	return out
}

// A labelRun is one entry of a /PageLabels tree: where it starts and how it
// numbers from there.
type labelRun struct {
	at     int // the page index the run begins at, counting from zero
	style  string
	prefix string
	start  int
}

// numberTree walks a PDF number tree, which may be one node or a tree of
// them, and returns its entries in key order.
//
// ⛔ depth is bounded. A number tree is a tree of /Kids, and a file can point
// one of them back at its parent — a loop that a reader following /Kids
// follows for ever.
func numberTree(src *reader.Document, node reader.Object, depth int) []labelRun {
	const maxTreeDepth = 32
	if depth > maxTreeDepth {
		return nil
	}
	dict, ok := follow(src, node).(reader.Dict)
	if !ok {
		return nil
	}
	var out []labelRun
	if nums, ok := follow(src, dict["Nums"]).(reader.Array); ok {
		// Nums is a flat array of alternating key and value.
		for i := 0; i+1 < len(nums); i += 2 {
			key, ok := follow(src, nums[i]).(reader.Integer)
			if !ok {
				continue
			}
			value, ok := follow(src, nums[i+1]).(reader.Dict)
			if !ok {
				continue
			}
			run := labelRun{at: int(key), start: 1}
			if s, ok := follow(src, value["S"]).(reader.Name); ok {
				run.style = string(s)
			}
			if p, ok := follow(src, value["P"]).(reader.String); ok {
				run.prefix = string(p)
			}
			if st, ok := follow(src, value["St"]).(reader.Integer); ok && st >= 1 {
				run.start = int(st)
			}
			out = append(out, run)
		}
	}
	if kids, ok := follow(src, dict["Kids"]).(reader.Array); ok {
		for _, kid := range kids {
			out = append(out, numberTree(src, kid, depth+1)...)
		}
	}
	return out
}

// writePageLabels builds the /PageLabels value for the document being written,
// or nil when there is nothing to say.
//
// ⛔ The runs are COMPRESSED. A document of three hundred pages labelled
// decimally is one entry, not three hundred: a viewer counts forward from a
// run's start, so a label that is the previous one plus one in the same style
// and prefix belongs to the run already open. Writing one entry per page would
// be correct and would make a file a reader has to walk three hundred nodes of
// to answer "what is page two called".
func (d *Doc) writePageLabels(w *reader.Writer) reader.Object {
	labels := d.PageLabels()
	if len(labels) == 0 {
		return nil
	}
	var nums reader.Array
	for i := 0; i < len(labels); {
		if labels[i] == "" {
			// ⛔ A PAGE-LABEL TREE HAS NO GAPS, and this is the format rather
			// than this writer: an entry labels from its own key until the
			// NEXT entry, so a run that opened at page one reaches page four
			// whether or not page four was meant to have a label. Skipping
			// unlabelled pages — which is what the first draft did — came
			// back from the file as 1, 2, 3, 4 where 1, 2 and then nothing
			// was asked for.
			//
			// So an unlabelled page after a labelled one is said OUT LOUD,
			// with an entry carrying neither a style nor a prefix: the label
			// it computes is the empty string, which is what was meant. Only
			// a document that starts unlabelled needs no entry, because
			// there is no run open yet to close.
			if len(nums) > 0 {
				nums = append(nums, reader.Integer(i), reader.Dict{})
			}
			for i < len(labels) && labels[i] == "" {
				i++
			}
			continue
		}
		style, prefix, start, run := runAt(labels, i)
		entry := reader.Dict{}
		if style != LabelNone {
			entry["S"] = reader.Name(style)
		}
		if prefix != "" {
			entry["P"] = reader.String(prefix)
		}
		if start != 1 {
			entry["St"] = reader.Integer(start)
		}
		nums = append(nums, reader.Integer(i), entry)
		i += run
	}
	if len(nums) == 0 {
		return nil
	}
	return w.Add(reader.Dict{"Nums": nums})
}

// runAt works out how long a run of labels starting at i is, and what it is
// made of.
//
// A label is only part of a numbered run when the style can reproduce it and
// the next label is what that style writes for one more. Anything this cannot
// explain becomes a run of ONE with the label as a literal prefix — which is
// the honest fallback: the file then says exactly what the viewer must show,
// at the cost of an entry.
func runAt(labels []string, i int) (style LabelStyle, prefix string, start, run int) {
	for _, s := range []LabelStyle{LabelDecimal, LabelLowerRoman, LabelUpperRoman,
		LabelLowerLetters, LabelUpperLetters} {
		p, n, ok := s.parse(labels[i])
		if !ok {
			continue
		}
		length := 1
		for j := i + 1; j < len(labels); j++ {
			if labels[j] != p+s.format(n+length) {
				break
			}
			length++
		}
		if length > 1 || s == LabelDecimal {
			// A single label is written as a literal unless it is decimal,
			// where the style is the one a viewer expects and costs nothing.
			return s, p, n, length
		}
	}
	return LabelNone, labels[i], 1, 1
}

// valid says whether a style is one the format has.
func (s LabelStyle) valid() error {
	switch s {
	case LabelNone, LabelDecimal, LabelLowerRoman, LabelUpperRoman,
		LabelLowerLetters, LabelUpperLetters:
		return nil
	}
	return fmt.Errorf("%q is not a label style: they are D, r, R, a, A, or empty", string(s))
}

// format writes n in this style.
func (s LabelStyle) format(n int) string {
	switch s {
	case LabelDecimal:
		return fmt.Sprint(n)
	case LabelLowerRoman:
		return strings.ToLower(roman(n))
	case LabelUpperRoman:
		return roman(n)
	case LabelLowerLetters:
		return strings.ToLower(letters(n))
	case LabelUpperLetters:
		return letters(n)
	}
	return ""
}

// parse is format backwards: it reads a label as a prefix and a number in this
// style, for working out which runs a list of labels is made of.
func (s LabelStyle) parse(label string) (prefix string, n int, ok bool) {
	// The number is at the END, so the prefix is everything before the
	// longest suffix this style can read.
	for i := 0; i < len(label); i++ {
		if v, ok := s.value(label[i:]); ok {
			return label[:i], v, true
		}
	}
	return "", 0, false
}

// value reads a number written in this style, or says it is not one.
func (s LabelStyle) value(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	switch s {
	case LabelDecimal:
		n := 0
		for i := 0; i < len(text); i++ {
			if text[i] < '0' || text[i] > '9' {
				return 0, false
			}
			n = n*10 + int(text[i]-'0')
		}
		// ⛔ A leading zero is not decimal: "007" and "7" are the same number
		// and different labels, so reading it as a run would renumber page
		// "008" as "8".
		if len(text) > 1 && text[0] == '0' {
			return 0, false
		}
		return n, n >= 1
	case LabelLowerRoman:
		if text != strings.ToLower(text) {
			return 0, false
		}
		return fromRoman(strings.ToUpper(text))
	case LabelUpperRoman:
		if text != strings.ToUpper(text) {
			return 0, false
		}
		return fromRoman(text)
	case LabelLowerLetters:
		if text != strings.ToLower(text) {
			return 0, false
		}
		return fromLetters(strings.ToUpper(text))
	case LabelUpperLetters:
		if text != strings.ToUpper(text) {
			return 0, false
		}
		return fromLetters(text)
	}
	return 0, false
}

// roman writes a number the way a page label does.
//
// ⛔ It is bounded at 3999, where the additive notation runs out: 4000 needs a
// bar over the M that neither a PDF string nor a page number has. Past it the
// number is written in decimal, because a label a viewer shows wrongly is
// worse than one it shows in the wrong style.
func roman(n int) string {
	if n < 1 || n > 3999 {
		return fmt.Sprint(n)
	}
	var sb strings.Builder
	for _, r := range []struct {
		v int
		s string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	} {
		for n >= r.v {
			sb.WriteString(r.s)
			n -= r.v
		}
	}
	return sb.String()
}

// fromRoman reads a roman numeral, and insists it is the CANONICAL spelling of
// its value: "IIII" is four to a person and is not what roman(4) writes, so
// reading it as four would make a run that renumbers the page after it.
func fromRoman(text string) (int, bool) {
	values := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	n, prev := 0, 0
	for i := len(text) - 1; i >= 0; i-- {
		v, ok := values[text[i]]
		if !ok {
			return 0, false
		}
		if v < prev {
			n -= v
		} else {
			n += v
			prev = v
		}
	}
	if n < 1 || n > 3999 || roman(n) != text {
		return 0, false
	}
	return n, true
}

// letters writes a number as a page label's letters: A..Z, then AA..ZZ.
//
// ⛔ This is NOT a spreadsheet column. ISO 32000-1 table 159 says the letters
// run "A to Z for the first 26, AA to ZZ for the next 26" — so 27 is AA and
// 28 is BB, where a spreadsheet's 28 is AB. Borrowing the spreadsheet rule
// would relabel every appendix past the twenty-sixth.
func letters(n int) string {
	if n < 1 {
		return ""
	}
	round := (n-1)/26 + 1
	letter := byte('A' + (n-1)%26)
	return strings.Repeat(string(letter), round)
}

// fromLetters reads what letters writes.
func fromLetters(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < 'A' || text[i] > 'Z' || text[i] != text[0] {
			return 0, false
		}
	}
	// ⛔ No round-trip check here, and that is a removal rather than an
	// omission: the loop above has already established the only shape
	// [letters] writes — one letter, A to Z, repeated. So letters(n) == text
	// for every text that reaches this line, which made `if letters(n) !=
	// text` dead code that no test could reach. The canonicity is proved by
	// the loop, not by writing the answer out again.
	return (len(text)-1)*26 + int(text[0]-'A') + 1, true
}

// follow resolves a reference and hands back nothing when it cannot.
//
// ⛔ The error is deliberately dropped, and only here. In a page-label tree an
// object that cannot be resolved means THIS PAGE HAS NO LABEL — a file whose
// labels point at objects that are gone is a file with broken labels, not a
// file that cannot be read, and refusing to open it would lose the document
// over its toolbar captions.
// It drops the error WITHOUT a branch. A reference that cannot be followed and
// one that resolves to nothing are the same answer here, and the format agrees:
// ISO 32000-1 says a reference to an object that is not there IS the null
// object. So `if err != nil { return nil }` said the one thing twice, and said
// it in a branch no test could reach — a dangling reference comes back as nil
// with no error at all.
func follow(src *reader.Document, obj reader.Object) reader.Object {
	v, _ := src.Resolve(obj)
	return v
}
