package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// marksOn is the annotations one page of a written file carries.
//
// ⛔ Read back out of the FILE rather than trusted from the command's own
// report. "marked 1 area(s)" is what the verb says it did; this is what the
// document holds.
func marksOn(t *testing.T, path string, page int) []reader.Dict {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	p, err := src.Page(page)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := src.Resolve(p.Get("Annots"))
	annots, _ := reader.ToArray(list)
	out := make([]reader.Dict, 0, len(annots))
	for i := range annots {
		d, ok := src.GetDict(reader.Dict{"a": annots[i]}, "a")
		if !ok {
			t.Fatalf("annotation %d of page %d is not a dictionary", i, page)
		}
		out = append(out, d)
	}
	return out
}

// ⛔⛔ THE VERB RUN, ON A FILE, WITH THE MARK READ BACK. The library's own
// tests prove Doc.Mark writes a correct annotation; what this proves is that a
// person can reach it — the state this fleet keeps losing. Measured across
// thirty repositories: four shipped a command and twenty-five did not, and the
// capability inventory had to be corrected five times because rows that read
// "missing" were libraries with no caller.
func TestMarkVerbWritesTheAnnotation(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "marked.pdf")

	code, stdout, errOut := exec("mark", "-area", "1:20,95,120,110", "-note", "look here", in, out)
	if code != 0 {
		t.Fatalf("mark said %d: %s", code, errOut)
	}
	if !strings.Contains(stdout, "marked 1 area(s) as highlight") {
		t.Errorf("it did not say what it marked: %q", stdout)
	}

	got := marksOn(t, out, 1)
	if len(got) != 1 {
		t.Fatalf("the first page carries %d annotations", len(got))
	}
	if sub, _ := reader.ToName(got[0].Get("Subtype")); sub != "Highlight" {
		t.Errorf("the annotation is a %q", sub)
	}
	if _, ok := got[0]["AP"]; !ok {
		t.Error("the annotation has no appearance, so nothing would show it")
	}
	if s, _ := reader.ToString(got[0].Get("Contents")); string(s) != "look here" {
		t.Errorf("the note reads %q", s)
	}
	// And a mark on page one leaves page two alone.
	if n := len(marksOn(t, out, 2)); n != 0 {
		t.Errorf("a mark on page 1 put %d annotations on page 2", n)
	}
}

// Several areas are several marks, one per area, on the pages they name.
func TestMarkVerbTakesMoreThanOneArea(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "marked.pdf")
	code, stdout, errOut := exec("mark",
		"-area", "1:20,95,120,110",
		"-and", "1:20,75,100,90",
		"-and", "2:20,95,120,110",
		"-as", "underline", in, out)
	if code != 0 {
		t.Fatalf("mark said %d: %s", code, errOut)
	}
	if !strings.Contains(stdout, "marked 3 area(s) as underline") {
		t.Errorf("it said %q", stdout)
	}
	for page, want := range map[int]int{1: 2, 2: 1} {
		if n := len(marksOn(t, out, page)); n != want {
			t.Errorf("page %d carries %d marks, want %d", page, n, want)
		}
	}
	if sub, _ := reader.ToName(marksOn(t, out, 2)[0].Get("Subtype")); sub != "Underline" {
		t.Errorf("the mark on page 2 is a %q", sub)
	}
}

// ⛔ What it refuses. Each of these is worth a refusal rather than a guess:
// every one of them is a mistake the person can still do something about, and
// a file written from it would look like it worked.
func TestMarkVerbRefusesWhatItCannotDo(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "marked.pdf")
	for _, c := range []struct {
		what string
		args []string
	}{
		{"no area at all", []string{in, out}},
		{"an area with no page", []string{"-area", "20,95,120,110", in, out}},
		{"a page that is not a number", []string{"-area", "one:20,95,120,110", in, out}},
		{"page zero", []string{"-area", "0:20,95,120,110", in, out}},
		{"a page the document has not", []string{"-area", "9:20,95,120,110", in, out}},
		{"three numbers", []string{"-area", "1:20,95,120", in, out}},
		{"a number that is not one", []string{"-area", "1:20,95,120,x", in, out}},
		// ⛔ A flat rectangle would mark nothing: the library refuses a markup
		// with no quadrilaterals, and this is the same mistake written
		// differently — so it is caught before the file is opened.
		{"a flat rectangle", []string{"-area", "1:20,95,120,95", in, out}},
		{"no width", []string{"-area", "1:20,95,20,110", in, out}},
		// ⛔ A kind it does not know is refused rather than read as a
		// highlight: somebody who typed "strikethrough" meant to strike text
		// out, and a yellow wash instead is a wrong answer dressed as a right
		// one.
		{"a kind it does not know", []string{"-area", "1:20,95,120,110", "-as", "strikethrough", in, out}},
		{"a colour that is not three numbers", []string{"-area", "1:20,95,120,110", "-colour", "1,0", in, out}},
		{"a colour out of range", []string{"-area", "1:20,95,120,110", "-colour", "1,0,7", in, out}},
		{"one file", []string{"-area", "1:20,95,120,110", in}},
		{"a flag it does not know", []string{"-nonsense", "-area", "1:20,95,120,110", in, out}},
		// ⛔ And the two ends of the job, which are the failures a person
		// actually meets: a file that is not there or is not a PDF, and an
		// output it cannot write. Neither must report success.
		{"a file that is not there", []string{"-area", "1:20,95,120,110",
			filepath.Join(t.TempDir(), "absent.pdf"), out}},
		{"an output it cannot write", []string{"-area", "1:20,95,120,110", in,
			filepath.Join(in, "under-a-file.pdf")}},
	} {
		if code, _, _ := exec(append([]string{"mark"}, c.args...)...); code == 0 {
			t.Errorf("%s was accepted", c.what)
		}
	}
}

// A colour the caller gives is the colour that is written.
func TestMarkVerbTakesAColour(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "marked.pdf")
	if code, _, errOut := exec("mark", "-area", "1:20,95,120,110",
		"-colour", "0.2,0.4,0.6", in, out); code != 0 {
		t.Fatalf("mark said %d: %s", code, errOut)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	col, _ := src.Resolve(marksOn(t, out, 1)[0].Get("C"))
	arr, _ := reader.ToArray(col)
	if len(arr) != 3 {
		t.Fatalf("the colour is %d numbers", len(arr))
	}
	for i, want := range []float64{0.2, 0.4, 0.6} {
		v, _ := src.Resolve(arr[i])
		got, _ := reader.ToFloat(v)
		if got < want-0.001 || got > want+0.001 {
			t.Errorf("colour %d is %v, want %v", i, got, want)
		}
	}
}

// ⛔ Every kind the help string names is a kind the verb takes. A help that
// offers a value nothing accepts is worse than no help: it is an instruction
// that fails.
func TestEveryKindTheHelpNamesIsAccepted(t *testing.T) {
	for _, name := range []string{"highlight", "underline", "strikeout", "squiggly"} {
		if _, err := markKind(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
		if _, err := markKind(strings.ToUpper(name)); err != nil {
			t.Errorf("%q in capitals: %v", name, err)
		}
	}
	// And the usage line in the verb table names them all, so the two cannot
	// drift apart unnoticed.
	var line string
	for _, c := range commands {
		if c.name == "mark" {
			line = c.usage
		}
	}
	if line == "" {
		t.Fatal("the mark verb is not in the command table")
	}
	for _, name := range []string{"highlight", "underline", "strikeout", "squiggly"} {
		if !strings.Contains(line, name) {
			t.Errorf("the usage line does not offer %q: %s", name, line)
		}
	}
}
