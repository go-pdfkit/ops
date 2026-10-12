package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-ooxml/ooxml"
	"github.com/go-ooxml/opc"
	"github.com/go-richdoc/richdoc"
)

// ⛔ THE CONTROL IS go-ooxml's READER, not a string of expected XML. What
// these verbs owe is a file something else can open, and ParseWord and
// ParseWorkbook were measured against files Word and LibreOffice produced.
func TestWordVerb(t *testing.T) {
	in := documentFixture(t, nil)
	out := filepath.Join(t.TempDir(), "out.docx")
	code, printed, errOut := exec("word", in, out)
	if code != 0 {
		t.Fatalf("word said %d: %s", code, errOut)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// ⛔ What it SAYS it wrote has to be what is on disk. "0 bytes" beside a
	// four-kilobyte file is a tool reporting on something else, and
	// "it mentioned bytes" could not tell the two apart.
	if want := fmt.Sprintf("out.docx\t%d bytes", len(b)); !strings.Contains(printed, want) {
		t.Errorf("it said %q, want it to say %q", printed, want)
	}
	doc, err := ooxml.ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatalf("what it wrote could not be read: %v", err)
	}
	text := richdoc.PlainText(doc)
	for _, want := range []string{"The Title", "one", "two", "Name", "Age", "Ada", "36"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not in the .docx:\n%s", want, text)
		}
	}
	// The heading is a heading, which is the whole reason a styles part is
	// written — not just text that happens to be first.
	var levels []int
	for _, blk := range doc.Blocks {
		if h, ok := blk.(richdoc.Heading); ok {
			levels = append(levels, h.Level)
		}
	}
	if len(levels) == 0 {
		t.Errorf("no headings came back:\n%s", text)
	}
}

func TestExcelVerb(t *testing.T) {
	in := documentFixture(t, nil)
	out := filepath.Join(t.TempDir(), "out.xlsx")
	code, _, errOut := exec("excel", in, out)
	if code != 0 {
		t.Fatalf("excel said %d: %s", code, errOut)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ooxml.ParseWorkbook(b, opc.Limits{})
	if err != nil {
		t.Fatalf("what it wrote could not be read: %v", err)
	}
	text := richdoc.PlainText(doc)
	for _, want := range []string{"Name", "Age", "Ada", "36"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not in the .xlsx:\n%s", want, text)
		}
	}
	// ⛔ And the prose is NOT: a spreadsheet is a grid of cells, and putting
	// the headings in column A would make a file that looks like a document
	// and works as neither.
	if strings.Contains(text, "The Title") {
		t.Errorf("the document's prose went into the sheet:\n%s", text)
	}
}

// A range names pages, and means the same thing in both verbs.
func TestTheOfficeVerbsTakeARange(t *testing.T) {
	in := documentFixture(t, nil)
	// ⛔ Page two and NOT page one. "it wrote some bytes" is true of a range
	// the verb ignored and of a range it got wrong, so the test has to say
	// what came back and what did not.
	out := filepath.Join(t.TempDir(), "p2.docx")
	if code, _, errOut := exec("word", "-pages", "2", in, out); code != 0 {
		t.Fatalf("word -pages 2 said %d: %s", code, errOut)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ooxml.ParseWord(b, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	text := richdoc.PlainText(doc)
	if !strings.Contains(text, "the second page") {
		t.Errorf("page two is missing:\n%s", text)
	}
	if strings.Contains(text, "The Title") {
		t.Errorf("page one came too:\n%s", text)
	}
	// Page two of the fixture holds no table, so the workbook of it is the
	// one empty sheet a workbook has to have rather than an error.
	book := filepath.Join(t.TempDir(), "p2.xlsx")
	if code, _, errOut := exec("excel", "-pages", "2", in, book); code != 0 {
		t.Fatalf("a page with no tables was refused: %s", errOut)
	}
	raw, err := os.ReadFile(book)
	if err != nil {
		t.Fatal(err)
	}
	p, err := opc.Open(raw, opc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has("xl/worksheets/sheet1.xml") {
		t.Errorf("no sheet at all: %v", p.Parts())
	}
}

func TestTheOfficeVerbsRefuseWhatTheyCannotDo(t *testing.T) {
	in := documentFixture(t, nil)
	dir := t.TempDir()
	for _, verb := range []string{"word", "excel"} {
		cases := []struct {
			name string
			args []string
			says string
		}{
			{"no arguments", []string{verb}, "expected <in.pdf> <out>"},
			{"no output", []string{verb, in}, "expected <in.pdf> <out>"},
			{"a flag it does not have", []string{verb, "-nope", in, "x"}, "flag provided but not defined"},
			{"a range it cannot read", []string{verb, "-pages", "nope", in, "x"}, "nope"},
			{"a file that is not there", []string{verb, filepath.Join(dir, "gone.pdf"), "x"}, "gone.pdf"},
			// A directory where a file should go: the write is the last thing
			// to fail and the one nothing else checks.
			{"an output it cannot write", []string{verb, in, dir}, dir},
		}
		for _, c := range cases {
			code, _, errOut := exec(c.args...)
			if code == 0 {
				t.Errorf("%s/%s: it said nothing was wrong", verb, c.name)
				continue
			}
			if !strings.Contains(errOut, c.says) {
				t.Errorf("%s/%s: it said %q, which does not mention %q", verb, c.name, errOut, c.says)
			}
		}
	}
}

// ⛔ A writer that refuses. go-ooxml v0.3.0 fails only for a nil document and
// the document here is never nil, so nothing a caller can type reaches this —
// but the check stays, because a writer gaining a failure mode later must not
// mean a truncated file written and reported as bytes.
//
// runOffice takes the writer as a parameter because both verbs share it, and
// that seam is what makes this testable rather than a branch nobody can see.
func TestAWriterThatRefuses(t *testing.T) {
	in := documentFixture(t, nil)
	out := filepath.Join(t.TempDir(), "out.docx")
	c := &context{out: &strings.Builder{}, errOut: &strings.Builder{}}
	err := runOffice(c, []string{in, out}, "word",
		func(*richdoc.Document) ([]byte, error) { return nil, errRefused })
	if err == nil {
		t.Fatal("a writer that refused was reported as a success")
	}
	if !strings.Contains(err.Error(), "takes nothing") {
		t.Errorf("it said %q", err)
	}
	// And nothing was written: a file that exists is a file somebody opens.
	if _, err := os.Stat(out); err == nil {
		t.Error("it wrote a file anyway")
	}
}
