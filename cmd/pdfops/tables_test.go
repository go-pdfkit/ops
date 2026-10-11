package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/reader"
)

// tableFixture writes a document whose pages hold the content given, set in a
// font of even width so that a test can work out where every letter lands.
func tableFixture(t *testing.T, contents ...string) string {
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
				reader.Integer(200), reader.Integer(200)},
			"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
			"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": kids, "Count": reader.Integer(len(kids))})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tables.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// tablesOf is every table in a file, for the tests that work on the tables
// themselves rather than through the command.
func tablesOf(t *testing.T, path string) []extract.Table {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	var out []extract.Table
	for i := 1; i <= d.PageCount(); i++ {
		tables, err := extract.Tables(d, i)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tables...)
	}
	return out
}

// twoTables is a document with one two-column table on each of two pages.
func twoTables(t *testing.T) string {
	t.Helper()
	return tableFixture(t,
		"BT /F1 10 Tf 20 150 Td (Name) Tj 40 0 Td (Age) Tj ET\n"+
			"BT /F1 10 Tf 20 130 Td (Ada) Tj 40 0 Td (36) Tj ET",
		"BT /F1 10 Tf 20 150 Td (City) Tj 40 0 Td (Code) Tj ET\n"+
			"BT /F1 10 Tf 20 130 Td (Caen) Tj 40 0 Td (14000) Tj ET")
}

func TestTablesVerbWritesCSV(t *testing.T) {
	code, out, errOut := exec("tables", twoTables(t))
	if code != 0 {
		t.Fatalf("tables said %d: %s", code, errOut)
	}
	for _, want := range []string{"Name,Age", "Ada,36", "City,Code", "Caen,14000"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is not in the output:\n%s", want, out)
		}
	}
	// One blank line, between the two tables and nowhere else: it is what
	// says where one ended.
	if n := strings.Count(out, "\n\n"); n != 1 {
		t.Errorf("%d blank lines, want 1:\n%q", n, out)
	}
}

func TestTablesVerbWritesJSON(t *testing.T) {
	code, out, errOut := exec("tables", "-as", "json", twoTables(t))
	if code != 0 {
		t.Fatalf("tables said %d: %s", code, errOut)
	}
	var got []jsonTable
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("%d tables: %+v", len(got), got)
	}
	if got[0].Page != 1 || got[1].Page != 2 {
		t.Errorf("the tables say they are on pages %d and %d", got[0].Page, got[1].Page)
	}
	// Where it was found is as much the answer as what it says: a caller
	// pulling tables out of a long document needs to know where to look.
	if got[0].X != 20 || got[0].Width != 55 {
		t.Errorf("the first table is at x=%v and is %v wide", got[0].X, got[0].Width)
	}
	if want := [][]string{{"Name", "Age"}, {"Ada", "36"}}; fmt.Sprint(got[0].Rows) != fmt.Sprint(want) {
		t.Errorf("the first table reads %q", got[0].Rows)
	}
}

func TestTablesVerbWritesMarkdown(t *testing.T) {
	// md and markdown name the same thing, and the case is not part of it.
	for _, as := range []string{"md", "MARKDOWN"} {
		code, out, errOut := exec("tables", "-as", as, twoTables(t))
		if code != 0 {
			t.Fatalf("tables -as %s said %d: %s", as, code, errOut)
		}
		want := "| Name | Age |\n| --- | --- |\n| Ada | 36 |\n\n" +
			"| City | Code |\n| --- | --- |\n| Caen | 14000 |\n"
		if out != want {
			t.Errorf("-as %s wrote\n%q\nwant\n%q", as, out, want)
		}
	}
}

// BentoPDF's own documentation says the JSON is "pretty-printed with 2-space
// indentation", and something written to be read by a person as well as by a
// parser is worth keeping that way.
func TestTheJSONIsIndentedForSomebodyToRead(t *testing.T) {
	_, out, _ := exec("tables", "-as", "json", twoTables(t))
	if !strings.Contains(out, "[\n  {\n    \"page\": 1,") {
		t.Errorf("the JSON is not indented two spaces at a time:\n%s", out)
	}
}

// refusingWriter is a writer that will not take anything, which is what a
// closed pipe or a full disk looks like from here.
type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, errRefused }

var errRefused = fmt.Errorf("this writer takes nothing")

// ⛔ Every renderer has to hand back the writer's error rather than finishing
// quietly. `pdfops tables … | head` closes the pipe, and a tool that returns
// nought from a write that did not happen reports success for an empty file.
func TestEveryFormSaysWhenTheWriterRefusesIt(t *testing.T) {
	tables := tablesOf(t, twoTables(t))
	for _, as := range []string{"csv", "json", "md"} {
		form, err := formNamed(as)
		if err != nil {
			t.Fatal(err)
		}
		if err := form.render(refusingWriter{}, tables); err == nil {
			t.Errorf("-as %s wrote nothing and said nothing was wrong", as)
		}
	}
}

// ⛔ A cell holding a pipe has to be escaped or it becomes two cells, and the
// escaping belongs to the code that already knew it had to do it.
func TestACellHoldingAPipe(t *testing.T) {
	in := tableFixture(t,
		"BT /F1 10 Tf 20 150 Td (a|b) Tj 40 0 Td (Age) Tj ET\n"+
			"BT /F1 10 Tf 20 130 Td (Ada) Tj 40 0 Td (36) Tj ET")
	_, out, _ := exec("tables", "-as", "md", in)
	if !strings.Contains(out, `a\|b`) {
		t.Errorf("the pipe was not escaped:\n%s", out)
	}
}

func TestTablesVerbWritesOneFilePerTable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tables")
	code, out, errOut := exec("tables", "-to", dir, twoTables(t))
	if code != 0 {
		t.Fatalf("tables said %d: %s", code, errOut)
	}
	for _, name := range []string{"page001-01.csv", "page002-01.csv"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s is not in the listing:\n%s", name, out)
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s was not written: %v", name, err)
		}
		if len(b) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
	if !strings.Contains(out, "2 rows\t2 columns") {
		t.Errorf("the listing does not say the shape:\n%s", out)
	}
}

// Two tables on one page are numbered within it, and the numbering starts
// again on the next page.
func TestTwoTablesOnOnePageAreNumberedWithinIt(t *testing.T) {
	in := tableFixture(t,
		"BT /F1 10 Tf 20 180 Td (Name) Tj 40 0 Td (Age) Tj ET\n"+
			"BT /F1 10 Tf 20 160 Td (Ada) Tj 40 0 Td (36) Tj ET\n"+
			"BT /F1 10 Tf 20 140 Td (CCCCCCCCCC) Tj 80 0 Td (D) Tj ET\n"+
			"BT /F1 10 Tf 20 120 Td (GGGGGGGGGG) Tj 80 0 Td (H) Tj ET",
		"BT /F1 10 Tf 20 150 Td (City) Tj 40 0 Td (Code) Tj ET\n"+
			"BT /F1 10 Tf 20 130 Td (Caen) Tj 40 0 Td (14000) Tj ET")
	dir := filepath.Join(t.TempDir(), "tables")
	_, out, _ := exec("tables", "-to", dir, in)
	for _, name := range []string{"page001-01.csv", "page001-02.csv", "page002-01.csv"} {
		if !strings.Contains(out, name) {
			t.Errorf("%s is not in the listing:\n%s", name, out)
		}
	}
}

func TestTablesVerbWhenThereAreNone(t *testing.T) {
	in := fixture(t, 1)
	_, out, _ := exec("tables", in)
	if !strings.Contains(out, "no tables") {
		t.Errorf("it did not say there were none:\n%s", out)
	}
	// And nothing is written, not even the directory: a tool that makes an
	// empty directory has told you it found something.
	dir := filepath.Join(t.TempDir(), "tables")
	_, out, _ = exec("tables", "-to", dir, in)
	if !strings.Contains(out, "no tables") {
		t.Errorf("it did not say there were none:\n%s", out)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("it made a directory for no tables")
	}
}

// ⛔ A page that cannot be read gives no tables, which is exactly what a page
// holding none gives. Whoever is piping this into a spreadsheet has to be
// told, so it is said on the error stream where it cannot corrupt the rows.
func TestTablesVerbSaysWhichPagesItCouldNotRead(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	broken := w.Add(&reader.Stream{
		Dict: reader.Dict{"Filter": reader.Name("FlateDecode")},
		Raw:  []byte("this is not deflated at all"),
	})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Contents": broken,
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "broken.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	code, printed, errOut := exec("tables", path)
	if code != 0 {
		t.Fatalf("tables said %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "could not be read") || !strings.Contains(errOut, "1") {
		t.Errorf("it did not say which page it could not read: %q", errOut)
	}
	if strings.Contains(printed, "could not be read") {
		t.Errorf("the warning went into the rows:\n%s", printed)
	}
}

func TestTablesVerbRefusesWhatItCannotDo(t *testing.T) {
	in := twoTables(t)
	cases := []struct {
		name string
		args []string
		says string
	}{
		{"a form it does not know", []string{"tables", "-as", "xml", in}, "csv, json or md"},
		{"no file at all", []string{"tables"}, "expected <in.pdf>"},
		{"two files", []string{"tables", in, in}, "expected <in.pdf>"},
		{"a flag it does not have", []string{"tables", "-nope", in}, "flag provided but not defined"},
		{"a range it cannot read", []string{"tables", "-pages", "nope", in}, "nope"},
		{"a file that is not there", []string{"tables", filepath.Join(t.TempDir(), "gone.pdf")}, "gone.pdf"},
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

// ⛔ A document is free to say a word sits 10^308 points along, and a table
// with one word at minus that and another at plus it is infinitely wide. JSON
// has no spelling for an infinity, so this has to come back as an error:
// emitting `"width": +Inf` would hand whatever parses it something it will
// reject, halfway through a file it has already started reading.
//
// The number is written out in full because a PDF real has no exponent — `1e308`
// in a content stream is not a number at all, and a fixture written that way
// quietly tests nothing.
func TestATableWhoseWidthIsAnInfinity(t *testing.T) {
	big := "1" + strings.Repeat("0", 308)
	in := tableFixture(t,
		"BT /F1 10 Tf -"+big+" 150 Td (A) Tj ET\n"+
			"BT /F1 10 Tf 20 150 Td (Name) Tj 40 0 Td (Age) Tj ET\n"+
			"BT /F1 10 Tf "+big+" 150 Td (Z) Tj ET\n"+
			"BT /F1 10 Tf 20 130 Td (Ada) Tj 40 0 Td (36) Tj ET")
	// CSV says what the cells say and never mentions the box, so it is fine.
	if code, _, errOut := exec("tables", in); code != 0 {
		t.Errorf("csv refused a table it could write: %s", errOut)
	}
	code, _, errOut := exec("tables", "-as", "json", in)
	if code == 0 {
		t.Fatalf("it wrote JSON holding an infinity")
	}
	if !strings.Contains(errOut, "Inf") {
		t.Errorf("it did not say what it could not write: %q", errOut)
	}
	// The same on the way to a file, which is a different path through the
	// same renderer.
	code, _, errOut = exec("tables", "-as", "json", "-to", filepath.Join(t.TempDir(), "x"), in)
	if code == 0 {
		t.Errorf("it wrote a file holding an infinity: %s", errOut)
	}
}

func TestTablesVerbWhenTheDirectoryCannotBeMade(t *testing.T) {
	// A file where a directory would have to go.
	blocker := filepath.Join(t.TempDir(), "in-the-way")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := exec("tables", "-to", filepath.Join(blocker, "tables"), twoTables(t))
	if code == 0 {
		t.Errorf("it wrote into a path under a file: %s", errOut)
	}
}

func TestTablesVerbWhenTheFileCannotBeWritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tables")
	// The name the first table wants, already taken by a directory.
	if err := os.MkdirAll(filepath.Join(dir, "page001-01.csv"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := exec("tables", "-to", dir, twoTables(t))
	if code == 0 {
		t.Errorf("it wrote a file over a directory: %s", errOut)
	}
}
