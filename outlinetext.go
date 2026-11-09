package ops

import (
	"fmt"
	"strconv"
	"strings"
)

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
// the wrong page.
//
// ⛔⛔ IT LIVES HERE, IN THE LIBRARY, AND NOT IN THE COMMAND. It was written
// inside cmd/pdfops, where nothing else could reach it — so the browser
// workbench, asked for the same thing, would have had to carry a second parser
// for the same text. Two implementations of one published format are two things
// to keep in step, and the one that drifts is whichever has fewer users. The
// command now calls this, so the workbench and `pdfops outline -from` accept
// exactly the same file, by construction rather than by care.
func ParseOutline(text string) ([]Bookmark, error) {
	type level struct {
		depth int
		marks *[]Bookmark
	}
	var root []Bookmark
	stack := []level{{depth: -1, marks: &root}}

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
		depth := indent / 2

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

		for len(stack) > 1 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		if depth > stack[len(stack)-1].depth+1 {
			return nil, fmt.Errorf("line %d: indented %d levels under one at %d, so a level is missing between them",
				n, depth, stack[len(stack)-1].depth)
		}
		parent := stack[len(stack)-1].marks
		*parent = append(*parent, Bookmark{Title: title, Page: p})
		stack = append(stack, level{depth: depth, marks: &(*parent)[len(*parent)-1].Children})
	}
	return root, nil
}
