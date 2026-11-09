package ops_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-pdfkit/ops"
)

func TestATableOfContentsIsReadAsATree(t *testing.T) {
	marks, err := ops.ParseOutline("Introduction\t1\n  Why\t2\n  How\t4\n\nChapter one\t7\n  Deep\t8\n")
	if err != nil {
		t.Fatalf("ParseOutline: %v", err)
	}
	if len(marks) != 2 {
		t.Fatalf("got %d top-level bookmarks, want 2: %+v", len(marks), marks)
	}
	if marks[0].Title != "Introduction" || marks[0].Page != 1 {
		t.Errorf("first bookmark is %q on page %d, want Introduction on 1", marks[0].Title, marks[0].Page)
	}
	if len(marks[0].Children) != 2 {
		t.Fatalf("Introduction has %d children, want 2", len(marks[0].Children))
	}
	if marks[0].Children[1].Title != "How" || marks[0].Children[1].Page != 4 {
		t.Errorf("second child is %q on page %d, want How on 4",
			marks[0].Children[1].Title, marks[0].Children[1].Page)
	}
	if marks[1].Title != "Chapter one" || marks[1].Page != 7 {
		t.Errorf("second bookmark is %q on page %d, want Chapter one on 7", marks[1].Title, marks[1].Page)
	}
	// A level deeper after a blank line and a new top-level entry: the stack
	// unwinds on "Chapter one" and this hangs off it, not off "Introduction".
	if len(marks[1].Children) != 1 || marks[1].Children[0].Title != "Deep" {
		t.Errorf("Chapter one's children are %+v, want one called Deep", marks[1].Children)
	}
}

func TestAnEmptyTableOfContentsIsNoBookmarksAndNoComplaint(t *testing.T) {
	marks, err := ops.ParseOutline("\n   \n")
	if err != nil {
		t.Fatalf("ParseOutline: %v", err)
	}
	if len(marks) != 0 {
		t.Errorf("got %+v, want no bookmarks", marks)
	}
}

// TestALineItCannotReadNamesTheLine is the promise of the format: every refusal
// says WHICH line, because a person fixes a file of forty lines by being told
// the one that is wrong, not by being told that something is.
func TestALineItCannotReadNamesTheLine(t *testing.T) {
	for _, c := range []struct {
		why  string
		text string
		says string
	}{
		{"no tab at all", "Introduction 1\n", "line 1: expected a title, a tab and a page number"},
		{"nothing before the tab", "Fine\t1\n  \t2\n", "line 2: the title is empty"},
		{"a page that is a word", "Introduction\tone\n", `line 1: "one" is not a page number`},
		{"a page of zero", "Introduction\t0\n", "line 1: page 0, and pages start at 1"},
		{"an indent of three", "Fine\t1\n   Why\t2\n", "line 2: indented by 3 spaces, which is not a whole number of levels of two"},
		{"a level skipped", "Fine\t1\n  Why\t2\n      Deep\t3\n", "line 3: indented 3 levels under one at 1, so a level is missing between them"},
	} {
		marks, err := ops.ParseOutline(c.text)
		if err == nil {
			t.Errorf("%s: read as %+v, want a refusal", c.why, marks)
			continue
		}
		if err.Error() != c.says {
			t.Errorf("%s: says %q, want %q", c.why, err, c.says)
		}
		if marks != nil {
			t.Errorf("%s: returned %+v beside the error; a half-read outline is worse than none", c.why, marks)
		}
	}
}

// TestBookmarksInReadingOrderBecomeATree is Nest on its own: the entry point
// for a caller that has an /Outlines tree already walked rather than a file to
// read, which is what the browser workbench has.
func TestBookmarksInReadingOrderBecomeATree(t *testing.T) {
	marks, err := ops.Nest([]ops.Level{
		{Title: "One", Page: 1},
		{Title: "Under it", Page: 2, Depth: 1},
		{Title: "Deeper still", Page: 3, Depth: 2},
		{Title: "Two", Page: 7},
	})
	if err != nil {
		t.Fatalf("Nest: %v", err)
	}
	if len(marks) != 2 {
		t.Fatalf("got %d top-level bookmarks, want 2: %+v", len(marks), marks)
	}
	if len(marks[0].Children) != 1 || marks[0].Children[0].Title != "Under it" {
		t.Fatalf("One's children are %+v", marks[0].Children)
	}
	deep := marks[0].Children[0].Children
	if len(deep) != 1 || deep[0].Title != "Deeper still" || deep[0].Page != 3 {
		t.Errorf("the third level is %+v, want Deeper still on page 3", deep)
	}
	if len(marks[1].Children) != 0 {
		t.Errorf("Two came out with children %+v; the stack did not unwind", marks[1].Children)
	}
}

func TestNestingNothingIsNoBookmarks(t *testing.T) {
	marks, err := ops.Nest(nil)
	if err != nil || marks != nil {
		t.Errorf("Nest(nil) = %+v, %v; want no bookmarks and no complaint", marks, err)
	}
}

// TestARungMissingFromTheTreeIsRefusedByItsOwnType is why the refusal is a type
// and not a sentence: Nest answers a caller holding rows, ParseOutline answers
// a person holding a file, and the same fault has to be told two ways.
func TestARungMissingFromTheTreeIsRefusedByItsOwnType(t *testing.T) {
	marks, err := ops.Nest([]ops.Level{
		{Title: "One", Page: 1},
		{Title: "Adrift", Page: 2, Depth: 2},
	})
	if marks != nil {
		t.Errorf("returned %+v beside the error; a half-built tree is worse than none", marks)
	}
	var deep *ops.DepthError
	if !errors.As(err, &deep) {
		t.Fatalf("Nest refused with %T (%v), want a *ops.DepthError", err, err)
	}
	if deep.Row != 1 || deep.Depth != 2 || deep.Under != 0 {
		t.Errorf("the error says row %d at depth %d under %d, want row 1 depth 2 under 0",
			deep.Row, deep.Depth, deep.Under)
	}
	const says = "entry 2: indented 2 levels under one at 0, so a level is missing between them"
	if deep.Error() != says {
		t.Errorf("it reads %q, want %q", deep, says)
	}
	// And the same rows read out of a file say it in lines, counting the blank
	// one that is not an entry.
	_, err = ops.ParseOutline("One\t1\n\n    Adrift\t2\n")
	if err == nil || err.Error() != "line 3: indented 2 levels under one at 0, so a level is missing between them" {
		t.Errorf("from a file it reads %v, which does not name line 3", err)
	}
}

// TestTheWorkbenchAndTheCommandReadTheSameFile holds the reason the parser was
// lifted out of cmd/pdfops: the flag's own help text names this format, so the
// text in the help and the text the library accepts have to be one thing.
func TestTheWorkbenchAndTheCommandReadTheSameFile(t *testing.T) {
	const help = "a file of bookmarks: Title, a tab, a page number, two spaces of indent per level"
	for _, word := range []string{"a tab", "two spaces of indent"} {
		if !strings.Contains(help, word) {
			t.Fatalf("the help text no longer says %q", word)
		}
	}
	if _, err := ops.ParseOutline("Title\t1\n  Nested\t2\n"); err != nil {
		t.Errorf("the format the help describes was refused: %v", err)
	}
}
