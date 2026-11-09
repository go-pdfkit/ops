package ops

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A Level is one entry of a table of contents in reading order, carrying how
// deep it sits rather than what sits under it.
//
// It is what a reader of a table of contents has BEFORE it has a tree: a text
// file's indentation, or the /Outlines tree of a document already open, walked
// in reading order. Nest turns a run of them into the Bookmarks SetOutline
// takes.
type Level struct {
	Title string
	Page  int
	// Depth is zero for a top-level entry and one more for each level of
	// nesting under it.
	Depth int
}

// A DepthError is a row sitting more than one level under the row before it,
// which is a tree with a rung missing: a section heading under nothing, or a
// sub-section under a sub-section that was never written.
//
// It is a type rather than a sentence so that a caller who knows where its rows
// came from can say so — ParseOutline reports the line of the file, because the
// person it answers is looking at a file and not at a slice.
type DepthError struct {
	// Row is which entry, counting from zero.
	Row int
	// Depth is how deep it asked to sit, and Under how deep the entry before it
	// sits.
	Depth, Under int
}

func (e *DepthError) Error() string {
	return fmt.Sprintf("entry %d: indented %d levels under one at %d, so a level is missing between them",
		e.Row+1, e.Depth, e.Under)
}

// Nest turns bookmarks in reading order into the tree SetOutline takes.
//
// ⛔ A row deeper than one level below the row before it is a *DepthError
// naming the row, because a level missing in the middle is a tree nobody meant
// to ask for and every way of guessing one is a different document.
//
// ⛔⛔ IT IS ONE ALGORITHM AND NOT TWO. A table of contents read as text and an
// /Outlines tree already in a document arrive the same way — a title, a page
// and a depth, in reading order — so the stack that turns depths into children
// lives here, once, and both ParseOutline and the browser workbench go through
// it rather than each keeping a copy.
func Nest(rows []Level) ([]Bookmark, error) {
	type level struct {
		depth int
		marks *[]Bookmark
	}
	var root []Bookmark
	stack := []level{{depth: -1, marks: &root}}

	for i, row := range rows {
		for len(stack) > 1 && stack[len(stack)-1].depth >= row.Depth {
			stack = stack[:len(stack)-1]
		}
		if row.Depth > stack[len(stack)-1].depth+1 {
			return nil, &DepthError{Row: i, Depth: row.Depth, Under: stack[len(stack)-1].depth}
		}
		parent := stack[len(stack)-1].marks
		*parent = append(*parent, Bookmark{Title: row.Title, Page: row.Page})
		stack = append(stack, level{depth: row.Depth, marks: &(*parent)[len(*parent)-1].Children})
	}
	return root, nil
}

// ParseOutline reads a table of contents written as text: one bookmark a line,
// a title, a TAB and a page number, with two spaces of indent for each level of
// nesting.
//
//	Introduction→1
//	  Why→2
//	Chapter one→7
//
// ⛔ A LINE IT CANNOT READ IS AN ERROR NAMING THE LINE, because a table of
// contents quietly missing an entry is worse than one that refuses to be
// written: the first is a document that looks finished and sends a reader to
// the wrong page. Blank lines are not entries and are not counted as any.
//
// ⛔⛔ IT LIVES HERE, IN THE LIBRARY, AND NOT IN THE COMMAND. It was written
// inside cmd/pdfops, where nothing else could reach it — so the browser
// workbench, asked for the same thing, would have had to carry a second parser
// for the same text. Two implementations of one published format are two things
// to keep in step, and the one that drifts is whichever has fewer users. The
// command now calls this, so the workbench and `pdfops outline -from` accept
// exactly the same file, by construction rather than by care.
func ParseOutline(text string) ([]Bookmark, error) {
	var rows []Level
	// Which line each row came from: blank lines are skipped, so the row's
	// index is not its line, and the person reading the refusal is looking at
	// the file.
	var lines []int

	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		if strings.TrimSpace(line) == "" {
			continue
		}
		body := strings.TrimLeft(line, " ")
		indent := len(line) - len(body)
		if indent%2 != 0 {
			return nil, fmt.Errorf("line %d: indented by %d spaces, which is not a whole number of levels of two", n, indent)
		}

		title, page, ok := strings.Cut(body, "\t")
		if !ok {
			return nil, fmt.Errorf("line %d: expected a title, a tab and a page number", n)
		}
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, fmt.Errorf("line %d: the title is empty", n)
		}
		p, err := strconv.Atoi(strings.TrimSpace(page))
		if err != nil {
			return nil, fmt.Errorf("line %d: %q is not a page number", n, strings.TrimSpace(page))
		}
		if p < 1 {
			return nil, fmt.Errorf("line %d: page %d, and pages start at 1", n, p)
		}
		rows = append(rows, Level{Title: title, Page: p, Depth: indent / 2})
		lines = append(lines, n)
	}

	marks, err := Nest(rows)
	var deep *DepthError
	if errors.As(err, &deep) {
		// The same complaint, told in what the caller is looking at.
		return nil, fmt.Errorf("line %d: indented %d levels under one at %d, so a level is missing between them",
			lines[deep.Row], deep.Depth, deep.Under)
	}
	return marks, err
}
