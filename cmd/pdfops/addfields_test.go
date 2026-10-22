package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// describe writes a description file and returns its path.
func describe(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fields.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAddFieldsVerbCreatesAFormThatFieldsCanList(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "form.pdf")
	desc := describe(t, `[
	  {"name": "applicant", "kind": "text", "required": true,
	   "widgets": [{"page": 1, "rect": [72, 700, 300, 720]}]},
	  {"name": "agree", "kind": "checkbox", "value": "Yes",
	   "widgets": [{"page": 1, "rect": [72, 670, 86, 684], "on": "Yes"}]},
	  {"name": "size", "kind": "combo",
	   "options": [{"value": "s", "label": "Small"}, {"value": "l", "label": "Large"}],
	   "widgets": [{"page": 1, "rect": [72, 630, 220, 650]}]}
	]`)

	code, stdout, errOut := exec("addfields", "-fields", desc, in, out)
	if code != 0 {
		t.Fatalf("addfields said %d: %s", code, errOut)
	}
	if !strings.Contains(stdout, "added 3 field(s)") {
		t.Errorf("it did not say what it added: %q", stdout)
	}

	// ⛔ The oracle is the verb that READS forms, not this test's idea of one.
	code, listing, errOut := exec("fields", out)
	if code != 0 {
		t.Fatalf("fields said %d on the document addfields wrote: %s", code, errOut)
	}
	for _, want := range []string{
		"applicant", "text", "required",
		"agree", "checkbox",
		"size", "combo",
	} {
		if !strings.Contains(listing, want) {
			t.Errorf("the listing does not hold %q:\n%s", want, listing)
		}
	}
}

// A document that already has a form gains the new fields and keeps its own.
func TestAddFieldsVerbAddsToAFormThatIsAlreadyThere(t *testing.T) {
	in := pageWithForm(t, true)
	out := filepath.Join(t.TempDir(), "more.pdf")
	desc := describe(t, `[{"name": "added", "kind": "text",
	   "widgets": [{"page": 1, "rect": [10, 10, 90, 26]}]}]`)

	if code, _, errOut := exec("addfields", "-fields", desc, in, out); code != 0 {
		t.Fatalf("addfields said %d: %s", code, errOut)
	}
	code, listing, errOut := exec("fields", out)
	if code != 0 {
		t.Fatalf("fields said %d: %s", code, errOut)
	}
	if !strings.Contains(listing, "added") {
		t.Errorf("the new field is not listed:\n%s", listing)
	}
	if !strings.Contains(listing, "name") {
		t.Errorf("a field the document came with was lost:\n%s", listing)
	}
}

func TestAddFieldsVerbRefusesWhatItCannotDo(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	good := `[{"name": "a", "kind": "text", "widgets": [{"page": 1, "rect": [10, 10, 90, 26]}]}]`

	for _, c := range []struct {
		why  string
		args []string
		want string
	}{
		{"no arguments at all", []string{"addfields"}, ""},
		{"no description", []string{"addfields", in, out}, "nothing to add"},
		{"a description that is not there",
			[]string{"addfields", "-fields", filepath.Join(t.TempDir(), "nope.json"), in, out}, ""},
		{"a description that is not JSON",
			[]string{"addfields", "-fields", describe(t, "not json"), in, out}, "reading"},
		{"a kind that does not exist",
			[]string{"addfields", "-fields", describe(t,
				`[{"name":"a","kind":"barcode","widgets":[{"page":1,"rect":[1,1,9,9]}]}]`), in, out}, "kinds are"},
		{"a key that is spelled wrong",
			[]string{"addfields", "-fields", describe(t,
				`[{"name":"a","kind":"text","mutliline":true,"widgets":[{"page":1,"rect":[1,1,9,9]}]}]`), in, out}, "reading"},
		{"a field with no name",
			[]string{"addfields", "-fields", describe(t,
				`[{"name":"","kind":"text","widgets":[{"page":1,"rect":[1,1,9,9]}]}]`), in, out}, "no name"},
		{"two fields of one name",
			[]string{"addfields", "-fields", describe(t,
				`[{"name":"a","kind":"text","widgets":[{"page":1,"rect":[1,1,9,9]}]},
				  {"name":"a","kind":"text","widgets":[{"page":1,"rect":[1,20,9,29]}]}]`), in, out}, "both named"},
		{"a page the document does not have",
			[]string{"addfields", "-fields", describe(t,
				`[{"name":"a","kind":"text","widgets":[{"page":99,"rect":[1,1,9,9]}]}]`), in, out}, "page 99"},
		{"a choice field with no rows",
			[]string{"addfields", "-fields", describe(t,
				`[{"name":"a","kind":"combo","widgets":[{"page":1,"rect":[1,1,9,9]}]}]`), in, out}, "no options"},
		{"a checkbox whose state is the reserved name",
			[]string{"addfields", "-fields", describe(t,
				`[{"name":"a","kind":"checkbox","widgets":[{"page":1,"rect":[1,1,9,9],"on":"Off"}]}]`), in, out}, "reserves"},
		{"an input that is not there",
			[]string{"addfields", "-fields", describe(t, good),
				filepath.Join(t.TempDir(), "nope.pdf"), out}, ""},
		{"a flag that does not exist", []string{"addfields", "-nonsense", in, out}, ""},
	} {
		code, _, errOut := exec(c.args...)
		if code == 0 {
			t.Errorf("%s was accepted", c.why)
			continue
		}
		if c.want != "" && !strings.Contains(errOut, c.want) {
			t.Errorf("%s: it said %q, which does not mention %q", c.why, errOut, c.want)
		}
	}
}

// ⛔ A DESCRIPTION FILE IS INPUT. A list is as long as the file says, so a
// small file can ask for an enormous document; both bounds are far above any
// real form.
func TestAddFieldsVerbRefusesADescriptionThatIsTooLarge(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "out.pdf")

	var b strings.Builder
	b.WriteString(`[{"name":"a","kind":"combo","widgets":[{"page":1,"rect":[1,1,9,9]}],"options":[`)
	for i := 0; i < 10001; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"value":"x"}`)
	}
	b.WriteString(`]}]`)

	code, _, errOut := exec("addfields", "-fields", describe(t, b.String()), in, out)
	if code == 0 {
		t.Fatal("a choice field with ten thousand and one rows was accepted")
	}
	if !strings.Contains(errOut, "the most is") {
		t.Errorf("it said %q, which does not say what the limit is", errOut)
	}
}

// The paths that are not about the description: a file that is not a PDF, and
// a place the result cannot be written.
func TestAddFieldsVerbOnAFileItCannotUse(t *testing.T) {
	good := describe(t, `[{"name":"a","kind":"text","widgets":[{"page":1,"rect":[10,10,90,26]}]}]`)
	dir := t.TempDir()

	notAPDF := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notAPDF, []byte("not a pdf at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := exec("addfields", "-fields", good, notAPDF, filepath.Join(dir, "out.pdf")); code == 0 {
		t.Error("a file that is not a PDF was accepted")
	}

	// A destination inside a directory that is not there.
	nowhere := filepath.Join(dir, "no", "such", "place", "out.pdf")
	if code, _, _ := exec("addfields", "-fields", good, pageWithText(t), nowhere); code == 0 {
		t.Error("a destination that cannot be written was accepted")
	}
}

// ⛔ The other two bounds on a description file, each a small file asking for
// an enormous document.
func TestAddFieldsVerbRefusesTooManyFieldsOrWidgets(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "out.pdf")

	var many strings.Builder
	many.WriteByte('[')
	for i := 0; i < maxFields+1; i++ {
		if i > 0 {
			many.WriteByte(',')
		}
		fmt.Fprintf(&many, `{"name":"f%d","kind":"text","widgets":[{"page":1,"rect":[1,1,9,9]}]}`, i)
	}
	many.WriteByte(']')
	code, _, errOut := exec("addfields", "-fields", describe(t, many.String()), in, out)
	if code == 0 || !strings.Contains(errOut, "the most this adds at once") {
		t.Errorf("a description of %d fields was answered with %d: %s", maxFields+1, code, errOut)
	}

	var wide strings.Builder
	wide.WriteString(`[{"name":"a","kind":"text","widgets":[`)
	for i := 0; i < maxWidgets+1; i++ {
		if i > 0 {
			wide.WriteByte(',')
		}
		wide.WriteString(`{"page":1,"rect":[1,1,9,9]}`)
	}
	wide.WriteString(`]}]`)
	code, _, errOut = exec("addfields", "-fields", describe(t, wide.String()), in, out)
	if code == 0 || !strings.Contains(errOut, "shows in") {
		t.Errorf("a field with %d widgets was answered with %d: %s", maxWidgets+1, code, errOut)
	}
}

// A refusal leaves no half-written document behind.
func TestARefusedAddFieldsWritesNothing(t *testing.T) {
	in := pageWithText(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	desc := describe(t, `[{"name":"","kind":"text","widgets":[{"page":1,"rect":[1,1,9,9]}]}]`)

	if code, _, _ := exec("addfields", "-fields", desc, in, out); code == 0 {
		t.Fatal("it was accepted")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused run left a document behind")
	}
}
