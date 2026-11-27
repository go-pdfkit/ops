package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// outlineFixture is one page of Helvetica text.
func outlineFixture(t *testing.T) string {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	font := w.Add(reader.Dict{"Type": reader.Name("Font"),
		"Subtype": reader.Name("Type1"), "BaseFont": reader.Name("Helvetica")})
	kids := reader.Array{w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("BT /F1 24 Tf 10 100 Td (Hi) Tj ET\n")}),
	})}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(200)}})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "outline.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOutlineText(t *testing.T) {
	in := outlineFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("outlinetext", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	got := contentsOf(t, out)[0]
	if strings.Contains(got, "Tj") || strings.Contains(got, "Tf") {
		t.Errorf("text operators survive:\n%s", got)
	}
	if !strings.Contains(got, " f") {
		t.Errorf("no filled path:\n%s", got)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/Font") {
		t.Error("a font is left in the file")
	}
}

func TestOutlineTextRefusals(t *testing.T) {
	in := outlineFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	for _, args := range [][]string{
		{"outlinetext", in},
		{"outlinetext", "-pages", "nonsense", in, out},
		{"outlinetext", "/no/such/file.pdf", out},
		{"outlinetext", in, "/no/such/dir/out.pdf"},
		{"outlinetext", "-nonsense", in, out},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v was accepted", args[1:])
		}
	}
}

// ⛔ An annotation's appearance is not outlined, and the caller who wanted no
// font at all is told that one is left.
func TestOutlineTextSaysWhatItDidNotReach(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	font := w.Add(reader.Dict{"Type": reader.Name("Font"),
		"Subtype": reader.Name("Type1"), "BaseFont": reader.Name("Helvetica")})
	ap := w.Add(&reader.Stream{
		Dict: reader.Dict{"Subtype": reader.Name("Form"),
			"BBox":      reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(50), reader.Integer(20)},
			"Resources": reader.Dict{"Font": reader.Dict{"F1": font}}},
		Raw: []byte("BT /F1 10 Tf (x) Tj ET")})
	annot := w.Add(reader.Dict{"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"),
		"Rect": reader.Array{reader.Integer(10), reader.Integer(10), reader.Integer(60), reader.Integer(30)},
		"AP":   reader.Dict{"N": ap}})
	kids := reader.Array{w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Annots": reader.Array{annot},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("0 0 5 5 re f")}),
	})}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(200)}})
	src, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(t.TempDir(), "annot.pdf")
	if err := os.WriteFile(in, src, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, said, msg := exec("outlinetext", in, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "1 annotation appearance(s) still carry a font") {
		t.Errorf("it did not say what it left: %q", said)
	}
	// And a document without one says nothing.
	if code, said, _ := exec("outlinetext", outlineFixture(t), out); code != 0 || strings.Contains(said, "annotation") {
		t.Errorf("a plain document said %q", said)
	}
}

func TestOutlineTextSaysWhichArgumentIsWrong(t *testing.T) {
	in := outlineFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("outlinetext", in); code != 1 || !strings.Contains(msg, "<in.pdf> <out.pdf>") {
		t.Errorf("one argument: code %d, %q", code, msg)
	}
	if code, _, msg := exec("outlinetext", "/no/such/file.pdf", out); code != 1 || !strings.Contains(msg, "/no/such/file.pdf") {
		t.Errorf("missing input: code %d, %q", code, msg)
	}
}
