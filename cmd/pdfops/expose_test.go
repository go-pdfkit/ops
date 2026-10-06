package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// interleave takes one page from each document in turn, so two two-page files
// come out 1, 1, 2, 2 rather than 1, 2, 1, 2 -- the assertion is on the ORDER,
// because a verb that produced the right number of pages in the wrong order
// would pass a count.
func TestInterleave(t *testing.T) {
	a := fixture(t, 2)
	b := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")

	if code, _, msg := exec("interleave", out, a, b); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	got := contentsOf(t, out)
	if len(got) != 4 {
		t.Fatalf("pages = %v, want four", got)
	}
	want := []string{"page 1", "page 1", "page 2", "page 2"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("page %d = %q, want %q (order = %v)", i+1, got[i], want[i], got)
		}
	}

	if code, _, _ := exec("interleave", out, a); code != 1 {
		t.Error("one input is not an interleave and should fail")
	}
	if code, _, _ := exec("interleave", out, a, "/no/such/file.pdf"); code != 1 {
		t.Error("a missing second input should fail")
	}
	if code, _, _ := exec("interleave", out, "/no/such/file.pdf", b); code != 1 {
		t.Error("a missing first input should fail")
	}
	if code, _, _ := exec("interleave", "-nosuchflag", out, a, b); code != 1 {
		t.Error("a bad flag should fail")
	}
}

// onepage puts every page onto one.
func TestOnePage(t *testing.T) {
	in := fixture(t, 4)
	out := filepath.Join(t.TempDir(), "out.pdf")

	if code, _, msg := exec("onepage", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 1 {
		t.Errorf("pages = %d, want one", len(got))
	}
	if code, _, _ := exec("onepage", in); code != 1 {
		t.Error("a missing output argument should fail")
	}
	if code, _, _ := exec("onepage", "/no/such/file.pdf", out); code != 1 {
		t.Error("a missing input should fail")
	}
	if code, _, _ := exec("onepage", "-nosuchflag", in, out); code != 1 {
		t.Error("a bad flag should fail")
	}
}

// poster cuts each page into across x down tiles, so one page becomes four.
func TestPoster(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")

	if code, _, msg := exec("poster", "-across", "2", "-down", "2", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 4 {
		t.Errorf("pages = %d, want four tiles", len(got))
	}
	// The default is 2x2, so leaving the flags off must give the same count.
	if code, _, msg := exec("poster", in, out); code != 0 {
		t.Fatalf("defaults: code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 4 {
		t.Errorf("with defaults, pages = %d, want four", len(got))
	}
	if code, _, _ := exec("poster", "-across", "0", in, out); code != 1 {
		t.Error("a zero tile count should fail rather than produce nothing")
	}
	if code, _, _ := exec("poster", "-across", "two", in, out); code != 1 {
		t.Error("a non-numeric count should fail")
	}
	if code, _, _ := exec("poster", in); code != 1 {
		t.Error("a missing output argument should fail")
	}
}

// A file carried inside a document: put one in, see it listed, take it out
// again, and drop it. The round trip is the assertion -- "exit 0" would pass
// on a verb that wrote the attachment nowhere.
func TestAttachAttachmentsDetach(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	carried := filepath.Join(dir, "figures.csv")
	if err := os.WriteFile(carried, []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFile := filepath.Join(dir, "with.pdf")

	if code, _, msg := exec("attach", "-file", carried, "-description", "the figures", in, withFile); code != 0 {
		t.Fatalf("attach: code %d: %s", code, msg)
	}
	code, out, msg := exec("attachments", withFile)
	if code != 0 {
		t.Fatalf("attachments: code %d: %s", code, msg)
	}
	if !strings.Contains(out, "figures.csv") {
		t.Errorf("listing does not name the file: %q", out)
	}
	if !strings.Contains(out, "the figures") {
		t.Errorf("listing drops the description: %q", out)
	}
	if !strings.Contains(out, "8 bytes") {
		t.Errorf("listing does not give the size: %q", out)
	}

	// and written back out, byte for byte
	to := filepath.Join(dir, "out")
	if code, _, msg := exec("attachments", "-to", to, withFile); code != 0 {
		t.Fatalf("attachments -to: code %d: %s", code, msg)
	}
	back, err := os.ReadFile(filepath.Join(to, "figures.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != "a,b\n1,2\n" {
		t.Errorf("written back as %q", back)
	}

	// a document carrying nothing says so rather than printing an empty list
	if _, out, _ := exec("attachments", in); !strings.Contains(out, "carries nothing") {
		t.Errorf("a document with no attachments printed %q", out)
	}

	// detaching
	without := filepath.Join(dir, "without.pdf")
	if code, _, msg := exec("detach", "-name", "figures.csv", withFile, without); code != 0 {
		t.Fatalf("detach: code %d: %s", code, msg)
	}
	if _, out, _ := exec("attachments", without); !strings.Contains(out, "carries nothing") {
		t.Errorf("after detaching, the file still lists %q", out)
	}

	// ⛔ and detaching something that is not there must REFUSE, not write an
	// unchanged copy somebody then sends believing it was removed.
	if code, _, _ := exec("detach", "-name", "notthere.csv", withFile, without); code != 1 {
		t.Error("detaching a name the file does not carry should fail")
	}
}

// The ways these three verbs can be asked for wrongly.
func TestAttachmentVerbsRefuse(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	out := filepath.Join(dir, "out.pdf")

	if code, _, _ := exec("attach", in, out); code != 1 {
		t.Error("attach with no -file should fail")
	}
	if code, _, _ := exec("attach", "-file", "/no/such/file", in, out); code != 1 {
		t.Error("attach of a file that is not there should fail")
	}
	if code, _, _ := exec("attach", "-file", in, in); code != 1 {
		t.Error("attach with no output should fail")
	}
	if code, _, _ := exec("detach", in, out); code != 1 {
		t.Error("detach with no -name should fail")
	}
	if code, _, _ := exec("detach", "-name", "x", in); code != 1 {
		t.Error("detach with no output should fail")
	}
	if code, _, _ := exec("attachments"); code != 1 {
		t.Error("attachments with no input should fail")
	}
	if code, _, _ := exec("attachments", "/no/such/file.pdf"); code != 1 {
		t.Error("attachments of a missing file should fail")
	}
	if code, _, _ := exec("attachments", "-to", "/no/such/dir/under/a/file", "/no/such/file.pdf"); code != 1 {
		t.Error("an unopenable input should fail before anything is written")
	}
}

// -as renames what the document calls the file, which is the whole of
// bento's "edit attachments" beyond adding and removing.
func TestAttachUnderAnotherName(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	src := filepath.Join(dir, "scratch.bin")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")
	if code, _, msg := exec("attach", "-file", src, "-as", "report.csv", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	_, listing, _ := exec("attachments", out)
	if !strings.Contains(listing, "report.csv") {
		t.Errorf("listing = %q, want the -as name", listing)
	}
	if strings.Contains(listing, "scratch.bin") {
		t.Errorf("listing = %q, still the file's own name", listing)
	}
}

// outline, both ways round.
func TestOutlineFromAndDrop(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 4)
	toc := filepath.Join(dir, "toc.txt")
	if err := os.WriteFile(toc, []byte("Introduction\t1\n  Why\t2\nChapter one\t3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("outline", "-from", toc, in, out); code != 0 {
		t.Fatalf("outline -from: code %d: %s", code, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Introduction") {
		t.Error("the written file does not carry the bookmark titles")
	}

	dropped := filepath.Join(dir, "dropped.pdf")
	if code, _, msg := exec("outline", "-drop", out, dropped); code != 0 {
		t.Fatalf("outline -drop: code %d: %s", code, msg)
	}
	b2, err := os.ReadFile(dropped)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), "Introduction") {
		t.Error("dropping left the bookmark titles in the file")
	}

	// asked for wrongly
	if code, _, _ := exec("outline", in, out); code != 1 {
		t.Error("neither -drop nor -from should fail")
	}
	if code, _, _ := exec("outline", "-drop", "-from", toc, in, out); code != 1 {
		t.Error("both -drop and -from should fail")
	}
	if code, _, _ := exec("outline", "-from", "/no/such/toc.txt", in, out); code != 1 {
		t.Error("a missing bookmark file should fail")
	}
	if code, _, _ := exec("outline", "-drop", in); code != 1 {
		t.Error("a missing output argument should fail")
	}
}

// The bookmark file's grammar, and every way it can be wrong. A table of
// contents quietly missing an entry is worse than one that refuses to be
// written, so each of these is an error naming the line rather than a skip.
func TestParseOutline(t *testing.T) {
	marks, err := parseOutline("Introduction\t1\n  Why\t2\n  How\t3\n    Deeper\t4\nChapter one\t7\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 2 {
		t.Fatalf("top level = %d, want two", len(marks))
	}
	if marks[0].Title != "Introduction" || marks[0].Page != 1 {
		t.Errorf("first = %+v", marks[0])
	}
	if len(marks[0].Children) != 2 {
		t.Fatalf("children of the first = %d, want two", len(marks[0].Children))
	}
	if marks[0].Children[1].Title != "How" {
		t.Errorf("second child = %+v", marks[0].Children[1])
	}
	if len(marks[0].Children[1].Children) != 1 || marks[0].Children[1].Children[0].Title != "Deeper" {
		t.Errorf("third level = %+v", marks[0].Children[1].Children)
	}
	if marks[1].Title != "Chapter one" || marks[1].Page != 7 {
		t.Errorf("second top-level = %+v", marks[1])
	}

	for _, bad := range []struct{ name, text, says string }{
		{"no tab", "Introduction 1\n", "a tab"},
		{"odd indent", "A\t1\n   B\t2\n", "levels of two"},
		{"a level skipped", "A\t1\n    B\t2\n", "missing"},
		{"empty title", "\t3\n", "empty"},
		{"page is not a number", "A\tlater\n", "not a page number"},
		{"page zero", "A\t0\n", "pages start at 1"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			_, err := parseOutline(bad.text)
			if err == nil {
				t.Fatalf("%q was accepted", bad.text)
			}
			if !strings.Contains(err.Error(), bad.says) {
				t.Errorf("error %q does not say %q", err, bad.says)
			}
			if !strings.Contains(err.Error(), "line ") {
				t.Errorf("error %q does not name the line", err)
			}
		})
	}

	// An empty file is an empty outline, not an error: SetOutline with nothing
	// puts the sources' own bookmarks back, which is a thing to be able to ask
	// for.
	if got, err := parseOutline("\n\n  \n"); err != nil || len(got) != 0 {
		t.Errorf("blank file = %v, %v", got, err)
	}
}

// The refusals the library itself makes, reached through the verbs: the gate
// here is exact 100 % statement coverage, and an error path nothing reaches is
// an error path nobody has read.
func TestTheNewVerbsPassTheLibrarysRefusalsOn(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")
	empty := pagelessFixture(t)
	in := fixture(t, 1)

	// ops.Interleave and ops.OnePage both refuse a document with no pages.
	if code, _, _ := exec("interleave", out, empty, empty); code != 1 {
		t.Error("interleaving documents with no pages should fail")
	}
	if code, _, _ := exec("onepage", empty, out); code != 1 {
		t.Error("stacking a document with no pages should fail")
	}

	// ops.Attach refuses a name the document already carries.
	src := filepath.Join(dir, "once.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	withOne := filepath.Join(dir, "one.pdf")
	if code, _, msg := exec("attach", "-file", src, in, withOne); code != 0 {
		t.Fatalf("first attach: code %d: %s", code, msg)
	}
	if code, _, _ := exec("attach", "-file", src, withOne, out); code != 1 {
		t.Error("attaching the same name twice should fail")
	}
}

// Every one of these verbs opens its input, and every one of them parses
// flags; both can fail, and a verb that ignored either would write something
// from nothing.
func TestTheNewVerbsRefuseABadFlagAndAMissingInput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")
	in := fixture(t, 1)
	missing := "/no/such/file.pdf"

	for _, c := range []struct {
		name string
		args []string
	}{
		{"attachments", []string{"attachments", "-nosuchflag", in}},
		{"attach", []string{"attach", "-nosuchflag", "-file", in, in, out}},
		{"detach", []string{"detach", "-nosuchflag", "-name", "x", in, out}},
		{"outline", []string{"outline", "-nosuchflag", "-drop", in, out}},
	} {
		if code, _, _ := exec(c.args...); code != 1 {
			t.Errorf("%s: a bad flag should fail", c.name)
		}
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"poster", []string{"poster", missing, out}},
		{"attach", []string{"attach", "-file", in, missing, out}},
		{"detach", []string{"detach", "-name", "x", missing, out}},
		{"outline", []string{"outline", "-drop", missing, out}},
	} {
		if code, _, _ := exec(c.args...); code != 1 {
			t.Errorf("%s: an input that is not there should fail", c.name)
		}
	}
}

// A bookmark file that is there but cannot be read is a different failure from
// one that is absent, and the verb has to carry both back.
func TestOutlineRefusesAMalformedBookmarkFile(t *testing.T) {
	dir := t.TempDir()
	toc := filepath.Join(dir, "toc.txt")
	if err := os.WriteFile(toc, []byte("Introduction but no tab and no page\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, msg := exec("outline", "-from", toc, fixture(t, 1), filepath.Join(dir, "out.pdf"))
	if code != 1 {
		t.Fatalf("code %d, want a refusal", code)
	}
	if !strings.Contains(msg, "line 1") {
		t.Errorf("the message %q does not name the line", msg)
	}
}

// ⛔ The name of an attachment comes out of the document, so it is not ours to
// trust. `-to` must not write outside the directory it was given, and must
// fail loudly rather than silently skipping, because a person who asked for
// the files out and got some of them has no way to tell.
func TestAnAttachmentNameCannotEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "doc.pdf")
	if code, _, msg := exec("attach", "-file", src, "-as", "..", fixture(t, 1), doc); code != 0 {
		t.Fatalf("attach: code %d: %s", code, msg)
	}
	code, _, msg := exec("attachments", "-to", filepath.Join(dir, "out"), doc)
	if code != 1 {
		t.Fatalf("code %d, want a refusal for a name with no usable last element", code)
	}
	if !strings.Contains(msg, "no usable file name") {
		t.Errorf("message = %q", msg)
	}
}

// Writing the files out can fail at the directory and at a file inside it, and
// both have to come back rather than be counted as written.
func TestAttachmentsReportsWhatItCouldNotWrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sub")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "doc.pdf")
	if code, _, msg := exec("attach", "-file", src, fixture(t, 1), doc); code != 0 {
		t.Fatalf("attach: code %d: %s", code, msg)
	}

	// the directory itself cannot be made, because its parent is a file
	notADir := filepath.Join(dir, "sub", "under", "it")
	if code, _, _ := exec("attachments", "-to", notADir, doc); code != 1 {
		t.Error("a directory that cannot be made should fail")
	}

	// and the file inside it cannot be written, because that name is a
	// directory already
	to := filepath.Join(dir, "out")
	if err := os.MkdirAll(filepath.Join(to, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := exec("attachments", "-to", to, doc); code != 1 {
		t.Error("an attachment that cannot be written should fail")
	}
}

// ⛔ A name carrying a path escapes the directory that was asked for, and
// `..` is only the most obvious shape of it. `-as "../escaped.txt"` is written
// by a document, not by the person running the command, and the file has to
// land INSIDE the directory they named.
//
// This test exists because mutation said so: removing filepath.Base from the
// verb left every other test passing, at 100 % statement coverage. A covered
// line is not a checked one.
func TestAnAttachmentNameIsStrippedToItsLastElement(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload")
	if err := os.WriteFile(src, []byte("inside please"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "doc.pdf")
	if code, _, msg := exec("attach", "-file", src, "-as", "../escaped.txt", fixture(t, 1), doc); code != 0 {
		t.Fatalf("attach: code %d: %s", code, msg)
	}

	to := filepath.Join(dir, "out")
	if code, _, msg := exec("attachments", "-to", to, doc); code != 0 {
		t.Fatalf("attachments -to: code %d: %s", code, msg)
	}

	inside := filepath.Join(to, "escaped.txt")
	if b, err := os.ReadFile(inside); err != nil || string(b) != "inside please" {
		t.Errorf("not written inside the directory asked for: %v / %q", err, b)
	}
	// and nothing beside it
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Error("a file was written OUTSIDE the directory that was asked for")
	}
}
