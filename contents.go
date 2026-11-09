package ops

import (
	"fmt"
	"strconv"
	"strings"
)

// ContentsOptions is how a table of contents is set when it is written as a
// PAGE of the document rather than as the /Outlines tree a reader navigates by.
// The zero value is the sensible one.
type ContentsOptions struct {
	// Title is the heading. Empty means "Contents".
	Title string
	// Before is the page the contents goes in front of, counting from one.
	// Zero means the front of the document.
	Before int
	// Size is the type size of an entry, in points. Zero means eleven; the
	// heading is drawn half as large again.
	Size float64
	// Margin is the room left around the text, in points. Zero means 56, which
	// is about two centimetres.
	Margin float64
}

// ContentsPage writes the entries as a table of contents PAGE — the printed
// kind, with a page number at the right of each line and dots leading to it —
// and returns how many pages it added.
//
// ⛔ THE NUMBERS IT PRINTS ARE MOVED BY THE PAGES IT ADDS. A contents page put
// in front of a document pushes every page of it down, so an entry that pointed
// at page 7 before is on page 8 after, and printing the 7 would send a reader
// one page short of every heading in the book. That is done here, once, from
// what this function itself inserted — and [ShiftBookmarks] is the same rule
// for a caller that is also writing the /Outlines tree from the same entries:
//
//	n, err := doc.ContentsPage(marks, ops.ContentsOptions{})
//	doc.SetOutline(ops.ShiftBookmarks(marks, 1, n))
//
// ⛔ AND IT RUNS ONTO AS MANY PAGES AS IT NEEDS. A table of contents quietly
// missing its last nine entries is worse than one that refuses to be written:
// the first is a document that looks finished.
func (d *Doc) ContentsPage(marks []Bookmark, opt ContentsOptions) (int, error) {
	rows := Flatten(marks)
	if len(rows) == 0 {
		return 0, fmt.Errorf("ops: a table of contents with no entries is a blank page")
	}
	before := opt.Before
	if before == 0 {
		before = 1
	}
	if before < 1 || before > len(d.pages)+1 {
		return 0, fmt.Errorf("ops: cannot put a table of contents before page %d of %d", before, len(d.pages))
	}
	for i, r := range rows {
		if r.Page < 1 || r.Page > len(d.pages) {
			return 0, fmt.Errorf("ops: entry %d (%q) points at page %d, and the document has %d",
				i+1, r.Title, r.Page, len(d.pages))
		}
	}

	size := opt.Size
	if size == 0 {
		size = 11
	}
	margin := opt.Margin
	if margin == 0 {
		margin = 56
	}
	title := opt.Title
	if title == "" {
		title = "Contents"
	}

	// The sheet the contents is printed on is the one it goes in front of — or
	// the last one, when it goes at the end — so a contents for an A4 report is
	// on A4 and one for a postcard is not on A4 with a postcard behind it.
	// InsertBlank takes its size from the same page, so this agrees with what it
	// will make by construction.
	//
	// ⛔ No arm here for a document of no pages: every entry has already been
	// checked against the page count, so an empty document was refused above and
	// a default size would be a branch no test could reach.
	sheet := d.effectiveSize(d.pages[min(before, len(d.pages))-1])

	line := size * 1.6
	headSize := size * 1.5
	// What fits on the first page, which carries the heading, and on each one
	// after it, which does not.
	room := sheet[1] - 2*margin
	first := int((room - headSize*2) / line)
	rest := int(room / line)
	if first < 1 || rest < 1 {
		return 0, fmt.Errorf("ops: a %.0f by %.0f page with a %.0f point margin has no room for %.0f point text",
			sheet[0], sheet[1], margin, size)
	}
	added := 1
	if len(rows) > first {
		added += (len(rows) - first + rest - 1) / rest
	}

	// ⛔⛔ LAID OUT BEFORE A PAGE IS INSERTED, so that a refusal leaves the
	// document exactly as it was. It also has to happen before anything is
	// drawn for a second reason: an entry whose title does not fit at all is a
	// line with a page number and nothing to read, and the first version of
	// this drew six of those on a narrow page without a word of complaint. A
	// contents that says "2" where it should say "Introduction ... 2" is the
	// document-that-looks-finished again.
	laid, err := layOutContents(rows, sheet[0], margin, size, before, added)
	if err != nil {
		return 0, err
	}

	for i := range added {
		// ⛔ The refusal is not branched on, and that is the whole of what this
		// has to say about it: InsertBlank refuses an index outside 1..n+1,
		// `before` was checked against exactly that above, and every insertion
		// here only makes the document longer. A branch would be an arm no test
		// could reach.
		_ = d.InsertBlank(before + i)
	}

	at := 0
	for i := range added {
		page := &d.pages[before+i-1]
		y := sheet[1] - margin
		fits := rest
		if i == 0 {
			page.marks = append(page.marks, stampInstance{
				stamp: Stamp{Text: title, Font: HelveticaBold, Size: headSize},
				text:  title,
				at:    &[2]float64{margin, y - headSize},
			})
			y -= headSize * 2
			fits = first
		}
		for n := 0; n < fits && at < len(laid); n++ {
			y -= line
			page.marks = append(page.marks, laid[at].marks(size, y)...)
			at++
		}
	}
	return added, nil
}

// A contentsLine is one row of the printed contents, worked out: its title cut
// to what fits, the number it points at, the face it is set in, and where the
// three pieces start across the page.
type contentsLine struct {
	title, dots, num string
	face             Font
	left, dotsAt, at float64
}

// marks is that row as what gets drawn, at the height given.
//
// ⛔ The dots stop short of both, and the title was cut to leave room for them:
// two strings overlapping is a line that reads as neither.
func (l contentsLine) marks(size, y float64) []stampInstance {
	return []stampInstance{
		{stamp: Stamp{Text: l.title, Font: l.face, Size: size}, text: l.title,
			at: &[2]float64{l.left, y}},
		{stamp: Stamp{Text: l.dots, Font: Helvetica, Size: size}, text: l.dots,
			at: &[2]float64{l.dotsAt, y}},
		{stamp: Stamp{Text: l.num, Font: Helvetica, Size: size}, text: l.num,
			at: &[2]float64{l.at, y}},
	}
}

// layOutContents works every row out across the width given, and refuses the
// first whose title has no room at all.
//
// ⛔ A TITLE THAT DOES NOT FIT IS A REFUSAL AND NOT A SHORTER LINE. It was a
// shorter line for one afternoon: on a 142 by 198 point fixture with the default
// 56 point margin, every row came out as a page number with nothing to read
// beside it, and the verb reported success. A reader of that page cannot tell it
// from a document whose headings have no names.
func layOutContents(rows []Level, width, margin, size float64, before, added int) ([]contentsLine, error) {
	right := width - margin
	out := make([]contentsLine, 0, len(rows))
	for i, r := range rows {
		page := r.Page
		if page >= before {
			page += added
		}
		face := Helvetica
		if r.Depth == 0 {
			face = HelveticaBold
		}
		num := strconv.Itoa(page)
		numW := Helvetica.Width(num, size)
		left := margin + float64(r.Depth)*2*size
		// The room a title may take: what is left after the number, with a
		// space either side of where the dots go.
		title := cutToWidth(face, r.Title, size, right-numW-left-2*size)
		if title == "" {
			return nil, fmt.Errorf(
				"ops: entry %d (%q) has no room for its title on a %.0f point page at %.0f point text with a %.0f point margin",
				i+1, r.Title, width, size, margin)
		}
		titleW := face.Width(title, size)
		dotsAt := left + titleW + size*0.5
		out = append(out, contentsLine{
			title: title, num: num, face: face,
			left: left, dotsAt: dotsAt, at: right - numW,
			dots: dotsFor(size, right-numW-size*0.5-dotsAt),
		})
	}
	return out, nil
}

// cutToWidth is a string that fits, with "..." in place of what was cut. It
// gives back nothing at all when there is no room even for that, because a
// line drawn as "..." says less than a line drawn as its page number alone.
//
// ⛔ BY RUNES, not by bytes: a title cut mid-rune is a title with a broken
// character at the end of it, and the titles in a document are not ASCII.
// A room of nothing, or of less than nothing on a page too narrow for the
// indentation alone, needs no guard of its own: nothing fits in it, so the loop
// runs down to the empty string and gives back nothing, which is the answer.
func cutToWidth(f Font, s string, size, room float64) string {
	if f.Width(s, size) <= room {
		return s
	}
	r := []rune(s)
	for len(r) > 0 {
		r = r[:len(r)-1]
		cut := strings.TrimRight(string(r), " ") + "..."
		if f.Width(cut, size) <= room {
			return cut
		}
	}
	return ""
}

// dotsFor is the leader: as many dots as fit in the gap.
//
// ⛔ No guard against a gap too small for any: the title it follows was cut to
// leave room for the number and a space either side, so the gap is never less
// than that space — and a count of nought repeats to the empty string, which
// draws nothing, so the arithmetic answers the case a guard would have.
func dotsFor(size, gap float64) string {
	return strings.Repeat(".", int(gap/Helvetica.Width(".", size)))
}

// Flatten is a bookmark tree read in reading order, each entry carrying how
// deep it sits rather than what sits under it: the inverse of [Nest], and what
// a printed contents needs for its indentation.
func Flatten(marks []Bookmark) []Level {
	return flattenAt(marks, 0)
}

func flattenAt(marks []Bookmark, depth int) []Level {
	var out []Level
	for _, m := range marks {
		out = append(out, Level{Title: m.Title, Page: m.Page, Depth: depth})
		out = append(out, flattenAt(m.Children, depth+1)...)
	}
	return out
}

// ShiftBookmarks moves the entries that point at page from or later on by n
// pages, which is what inserting pages in front of them does to a document.
//
// ⛔ It is here rather than written out by each caller because the rule has an
// edge: an entry pointing at a page BEFORE the insertion has not moved, and
// shifting it too would send a reader to the wrong place in exactly the
// documents that have a preface.
func ShiftBookmarks(marks []Bookmark, from, by int) []Bookmark {
	if len(marks) == 0 {
		return nil
	}
	out := make([]Bookmark, 0, len(marks))
	for _, m := range marks {
		if m.Page >= from {
			m.Page += by
		}
		m.Children = ShiftBookmarks(m.Children, from, by)
		out = append(out, m)
	}
	return out
}
