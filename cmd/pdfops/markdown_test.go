package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// documentFixture writes a document with a title, a paragraph, a list and a
// grid, in a font of even width so a test can work out where every letter
// lands. The body is long because whether a size is a heading size is decided
// by the share of the characters it holds: a fixture of one heading and one
// short line makes that heading a fifth of the text.
func documentFixture(t *testing.T, info reader.Dict) string {
	t.Helper()
	var lines []string
	add := func(s string) { lines = append(lines, s) }
	add("BT /F1 20 Tf 20 760 Td (The Title) Tj ET")
	y := 720
	for i := 0; i < 6; i++ {
		add(fmt.Sprintf("BT /F1 10 Tf 20 %d Td (a line of ordinary body text with a number of words on it) Tj ET", y))
		y -= 12
	}
	y -= 20
	for _, s := range []string{"one", "two"} {
		add(fmt.Sprintf("BT /F1 10 Tf 20 %d Td (\\225) Tj 20 0 Td (%s) Tj ET", y, s))
		y -= 12
	}
	y -= 20
	add(fmt.Sprintf("BT /F1 10 Tf 20 %d Td (Name) Tj 60 0 Td (Age) Tj ET", y))
	add(fmt.Sprintf("BT /F1 10 Tf 20 %d Td (Ada) Tj 60 0 Td (36) Tj ET", y-12))
	return writeDocument(t, info, strings.Join(lines, "\n"), "BT /F1 10 Tf 20 700 Td (the second page) Tj ET")
}

// writeDocument writes a document of the content streams given.
func writeDocument(t *testing.T, info reader.Dict, contents ...string) string {
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
	kids := make(reader.Array, 0, len(contents))
	for _, content := range contents {
		kids = append(kids, w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(612), reader.Integer(792)},
			"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
			"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": kids, "Count": reader.Integer(len(kids))})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	trailer := reader.Dict{"Root": root}
	if info != nil {
		trailer["Info"] = w.Add(info)
	}
	out, err := w.Finish(trailer)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMarkdownVerb(t *testing.T) {
	code, out, errOut := exec("markdown", documentFixture(t, nil))
	if code != 0 {
		t.Fatalf("markdown said %d: %s", code, errOut)
	}
	for _, want := range []string{
		"# The Title",
		"a line of ordinary body text",
		"- one",
		"- two",
		"| Name | Age |",
		"| --- | --- |",
		"| Ada | 36 |",
		"the second page",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not in the output:\n%s", want, out)
		}
	}
}

// A range names pages, and the pages are the document's own.
func TestMarkdownVerbTakesARange(t *testing.T) {
	_, out, _ := exec("markdown", "-pages", "2", documentFixture(t, nil))
	if !strings.Contains(out, "the second page") {
		t.Errorf("page two is missing:\n%s", out)
	}
	if strings.Contains(out, "The Title") {
		t.Errorf("page one came too:\n%s", out)
	}
}

// ⛔ The range is applied AFTER the document is read. What counts as a heading
// is decided by the sizes the WHOLE document uses, so asking for page two
// alone must not give page two different headings from asking for both.
func TestARangeDoesNotChangeWhatAHeadingIs(t *testing.T) {
	in := documentFixture(t, nil)
	_, whole, _ := exec("markdown", in)
	_, first, _ := exec("markdown", "-pages", "1", in)
	// Page one's own text, read both ways, has to read the same.
	page1 := whole[:strings.Index(whole, "the second page")]
	if strings.TrimSpace(page1) != strings.TrimSpace(first) {
		t.Errorf("page one alone reads\n%s\nand as part of the document\n%s", first, page1)
	}
}

func TestMarkdownVerbWritesPageRecords(t *testing.T) {
	// A title written the way a real producer writes one: UTF-16 behind a
	// byte order mark.
	info := reader.Dict{
		"Title":  reader.String("\xfe\xff\x00R\x00\xe9\x00s\x00u\x00m\x00\xe9"),
		"Author": reader.String("Ada"),
	}
	code, out, errOut := exec("markdown", "-json", documentFixture(t, info))
	if code != 0 {
		t.Fatalf("markdown -json said %d: %s", code, errOut)
	}
	var got []pageRecord
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("%d records, want one per page: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Text, "# The Title") {
		t.Errorf("the first record does not hold the page's markdown: %q", got[0].Text)
	}
	// The three keys a retrieval pipeline needs, spelled the way
	// LlamaMarkdownReader spells them.
	if got[1].Metadata["page_number"] != "2" || got[1].Metadata["page_count"] != "2" {
		t.Errorf("the second record says %v", got[1].Metadata)
	}
	if !strings.HasSuffix(got[0].Metadata["file_path"], ".pdf") {
		t.Errorf("file_path is %q", got[0].Metadata["file_path"])
	}
	// ⛔ And the document's own metadata, DECODED. ToString would have put
	// "\xfe\xffR\x00é\x00…" in here.
	if got[0].Metadata["title"] != "Résumé" {
		t.Errorf("title is %q, want %q", got[0].Metadata["title"], "Résumé")
	}
	if got[0].Metadata["author"] != "Ada" {
		t.Errorf("author is %q", got[0].Metadata["author"])
	}
}

// A document that says nothing about itself still gets the three keys that
// describe where the text came from.
func TestPageRecordsOfADocumentWithNoMetadata(t *testing.T) {
	_, out, _ := exec("markdown", "-json", documentFixture(t, nil))
	var got []pageRecord
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no records")
	}
	if got[0].Metadata["page_number"] != "1" {
		t.Errorf("metadata is %v", got[0].Metadata)
	}
}

// An entry that is not text is left out rather than rendered: the information
// dictionary is the one place a document may put anything at all, and
// "author: 4" is worse than no author.
func TestAMetadataEntryThatIsNotText(t *testing.T) {
	info := reader.Dict{"Author": reader.Integer(4), "Title": reader.String("ok")}
	_, out, _ := exec("markdown", "-json", documentFixture(t, info))
	var got []pageRecord
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, there := got[0].Metadata["author"]; there {
		t.Errorf("a number was rendered as an author: %v", got[0].Metadata)
	}
	if got[0].Metadata["title"] != "ok" {
		t.Errorf("metadata is %v", got[0].Metadata)
	}
}

// An empty entry is left out too: a key whose value is nothing says less than
// no key at all, and a pipeline filtering on "has a title" would be wrong.
func TestAnEmptyMetadataEntry(t *testing.T) {
	info := reader.Dict{"Title": reader.String("")}
	_, out, _ := exec("markdown", "-json", documentFixture(t, info))
	var got []pageRecord
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, there := got[0].Metadata["title"]; there {
		t.Errorf("an empty title was kept: %v", got[0].Metadata)
	}
}

// No pages gives `[]`, never `null`: a caller testing the length of what came
// back should not have to test for nothing first.
func TestNoPagesIsAnEmptyListAndNotNull(t *testing.T) {
	code, out, errOut := exec("markdown", "-json", writeDocument(t, nil))
	if code != 0 {
		t.Fatalf("a document with no pages said %d: %s", code, errOut)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("it wrote %q, want []", strings.TrimSpace(out))
	}
}

// The JSON is indented for somebody to read as well as for a parser, which is
// what the tools this answers for do.
func TestThePageRecordsAreIndented(t *testing.T) {
	_, out, _ := exec("markdown", "-json", documentFixture(t, nil))
	if !strings.Contains(out, "[\n  {\n    \"text\":") {
		t.Errorf("the JSON is not indented two spaces at a time:\n%.120s", out)
	}
}

func TestMarkdownVerbRefusesWhatItCannotDo(t *testing.T) {
	in := documentFixture(t, nil)
	cases := []struct {
		name string
		args []string
		says string
	}{
		{"no file at all", []string{"markdown"}, "expected <in.pdf>"},
		{"two files", []string{"markdown", in, in}, "expected <in.pdf>"},
		{"a flag it does not have", []string{"markdown", "-nope", in}, "flag provided but not defined"},
		{"a range it cannot read", []string{"markdown", "-pages", "nope", in}, "nope"},
		{"a file that is not there", []string{"markdown", filepath.Join(t.TempDir(), "gone.pdf")}, "gone.pdf"},
	}
	for _, c := range cases {
		code, _, errOut := exec(c.args...)
		if code == 0 {
			t.Errorf("%s: it said nothing was wrong", c.name)
			continue
		}
		if !strings.Contains(errOut, c.says) {
			t.Errorf("%s: it said %q, which does not mention %q", c.name, errOut, c.says)
		}
	}
}

// ⛔ Both shapes have to hand back the writer's error rather than finishing
// quietly: `pdfops markdown … | head` closes the pipe.
func TestMarkdownVerbSaysWhenTheWriterRefusesIt(t *testing.T) {
	in := documentFixture(t, nil)
	for _, args := range [][]string{{"markdown", in}, {"markdown", "-json", in}} {
		c := &context{out: refusingWriter{}, errOut: refusingWriter{}}
		if err := runMarkdown(c, args[1:]); err == nil {
			t.Errorf("%v wrote nothing and said nothing was wrong", args)
		}
	}
}
