package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
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

// duplex puts a fronts-then-backs scan back in reading order. ⛔ The assertion
// is on the ORDER: a verb that wrote the right number of pages in the wrong
// order is the entire defect, and a count cannot see it.
//
// fixture writes pages in order, so six pages read as a stack whose backs were
// scanned flipped give 1, 6, 2, 5, 3, 4 — which is what this says, rather than
// what a correctly-ordered scan would give. The fixture is a stack, not a
// document; asserting "1 2 3 4 5 6" here would be asserting the fixture.
func TestDuplex(t *testing.T) {
	in := fixture(t, 6)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("duplex", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := strings.Join(contentsOf(t, out), " "); got != "page 1 page 6 page 2 page 5 page 3 page 4" {
		t.Errorf("came out as %q", got)
	}
}

// ⛔ The DEFAULT reverses, because a stack is turned over as a stack. This
// asserts the two give different documents, so neither can be passing by
// standing in for the other.
func TestDuplexKeepBacksIsNotTheDefault(t *testing.T) {
	in := fixture(t, 6)
	dir := t.TempDir()
	def, keep := filepath.Join(dir, "d.pdf"), filepath.Join(dir, "k.pdf")
	if code, _, msg := exec("duplex", in, def); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if code, _, msg := exec("duplex", "-keep-backs", in, keep); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	a := strings.Join(contentsOf(t, def), " ")
	b := strings.Join(contentsOf(t, keep), " ")
	if a == b {
		t.Fatalf("-keep-backs changed nothing: both gave %q", a)
	}
	if b != "page 1 page 4 page 2 page 5 page 3 page 6" {
		t.Errorf("-keep-backs gave %q", b)
	}
}

func TestDuplexSplitIsHonoured(t *testing.T) {
	in := fixture(t, 6)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("duplex", "-split", "2", "-keep-backs", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	// Two fronts and four backs: the pairs collate and the rest follows.
	if got := strings.Join(contentsOf(t, out), " "); got != "page 1 page 3 page 2 page 4 page 5 page 6" {
		t.Errorf("came out as %q", got)
	}
}

func TestDuplexRefusals(t *testing.T) {
	in := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, _ := exec("duplex", in); code != 1 {
		t.Error("one argument was accepted")
	}
	if code, _, msg := exec("duplex", "-split", "9", in, out); code != 1 {
		t.Errorf("a split past the end was accepted: %s", msg)
	}
	if code, _, _ := exec("duplex", "/no/such/file.pdf", out); code != 1 {
		t.Error("a missing file was accepted")
	}
	if code, _, _ := exec("duplex", "-nonsense", in, out); code != 1 {
		t.Error("an unknown flag was accepted")
	}
}

// divide cuts each page into smaller pages of its own content. ⛔ The
// assertion is on the MEDIA BOX, because `divide` and `poster` make the same
// number of pages out of the same file and differ only in what those pages
// measure — a page count passes on either.
func TestDivide(t *testing.T) {
	in := fixture(t, 1)
	dir := t.TempDir()
	divided, postered := filepath.Join(dir, "d.pdf"), filepath.Join(dir, "p.pdf")
	if code, _, msg := exec("divide", in, divided); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if code, _, msg := exec("poster", "-across", "2", "-down", "1", in, postered); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	// The default is 2 across and 1 down: a spread cut down the middle.
	if got := len(contentsOf(t, divided)); got != 2 {
		t.Fatalf("divide made %d pages, want 2", got)
	}
	d, p := boxesOf(t, divided), boxesOf(t, postered)
	for i := range d {
		if d[i] == p[i] {
			t.Errorf("piece %d is %v either way: divide must make the pages "+
				"smaller where poster keeps them the page's size", i+1, d[i])
		}
	}
	// fixture pages are 100 by 200, so halves are 50 by 200.
	for i, b := range d {
		if b != [2]float64{50, 200} {
			t.Errorf("divide piece %d is %v, want 50x200", i+1, b)
		}
	}
}

func TestDivideRefusals(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, _ := exec("divide", in); code != 1 {
		t.Error("one argument was accepted")
	}
	if code, _, _ := exec("divide", "-across", "0", in, out); code != 1 {
		t.Error("a grid of zero was accepted")
	}
	if code, _, _ := exec("divide", "/no/such/file.pdf", out); code != 1 {
		t.Error("a missing file was accepted")
	}
	if code, _, _ := exec("divide", "-nonsense", in, out); code != 1 {
		t.Error("an unknown flag was accepted")
	}
}

// boxesOf reads back the width and height of every page of a written file, so
// a test can assert what the pages MEASURE rather than how many there are.
func boxesOf(t *testing.T, path string) [][2]float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	out := make([][2]float64, 0, d.PageCount())
	for i := 1; i <= d.PageCount(); i++ {
		page, err := d.Page(i)
		if err != nil {
			t.Fatal(err)
		}
		s := string(reader.FormatObject(page.Get("MediaBox")))
		var x0, y0, x1, y1 float64
		if _, err := fmt.Sscanf(s, "[%g %g %g %g]", &x0, &y0, &x1, &y1); err != nil {
			t.Fatalf("page %d: cannot read MediaBox %q: %v", i, s, err)
		}
		out = append(out, [2]float64{x1 - x0, y1 - y0})
	}
	return out
}

// deblank finds the pages that hold nothing and drops them.
//
// ⛔ -list is asserted FIRST and separately, because it is the dry run: a tool
// that deletes pages has to be able to say which ones it means before it does
// it, and a flag that silently wrote the file anyway would be worse than no
// flag.
func TestDeblankList(t *testing.T) {
	in := mixedFixture(t)
	code, out, msg := exec("deblank", "-list", in)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := strings.Fields(out); len(got) != 1 || got[0] != "2" {
		t.Errorf("-list said %q, want page 2", out)
	}
	// And it wrote nothing: there was nowhere to write to, and it did not ask.
	if code, _, _ := exec("deblank", "-list", in, filepath.Join(t.TempDir(), "x.pdf")); code != 1 {
		t.Error("-list accepted an output file")
	}
}

func TestDeblankDropsThem(t *testing.T) {
	in := mixedFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, said, msg := exec("deblank", in, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "dropped 1") {
		t.Errorf("said %q, want it to say one page went", said)
	}
	if got := len(contentsOf(t, out)); got != 1 {
		t.Errorf("%d pages left, want 1", got)
	}
}

func TestDeblankWithNothingToDrop(t *testing.T) {
	// ⛔ NOT fixture(t, 2): its pages carry a content stream that is not
	// drawing operators, so they render as blank paper and the detector is
	// right to say so. A fixture whose "content" is invisible is no fixture
	// for a tool that measures ink.
	in := writeDoc(t, func(d *ops.Doc) {
		for i := range 2 {
			d.Blank(595, 842)
			if err := d.Stamp(itoa(i+1), ops.Stamp{Text: "words", Font: ops.Helvetica, Size: 24}); err != nil {
				t.Fatal(err)
			}
		}
	})
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, said, msg := exec("deblank", in, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "no blank pages") {
		t.Errorf("said %q", said)
	}
	if got := len(contentsOf(t, out)); got != 2 {
		t.Errorf("%d pages, want 2", got)
	}
	code, said, _ = exec("deblank", "-list", in)
	if code != 0 || !strings.Contains(said, "no blank pages") {
		t.Errorf("-list said %q", said)
	}
}

func TestDeblankRefusals(t *testing.T) {
	in := mixedFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, _ := exec("deblank", in); code != 1 {
		t.Error("no output file was accepted")
	}
	if code, _, _ := exec("deblank", "/no/such/file.pdf", out); code != 1 {
		t.Error("a missing file was accepted")
	}
	if code, _, _ := exec("deblank", "-list", "/no/such/file.pdf"); code != 1 {
		t.Error("-list on a missing file was accepted")
	}
	if code, _, _ := exec("deblank", "-nonsense", in, out); code != 1 {
		t.Error("an unknown flag was accepted")
	}
	// -list has its own path through the detector, so its refusal needs its
	// own case: the two branches share nothing but the flag.
	if code, _, _ := exec("deblank", "-list", "-dpi", "100000", in); code != 1 {
		t.Error("-list past the pixel limit was accepted")
	}
	// And a file that cannot be written: the pages were measured, the blanks
	// were dropped, and the result had nowhere to go.
	if code, _, _ := exec("deblank", in, "/no/such/directory/out.pdf"); code != 1 {
		t.Error("an unwritable output was accepted")
	}
	// ⛔ A document of nothing but blank pages is refused rather than emptied.
	blanks := blankFixture(t)
	if code, _, said := exec("deblank", blanks, out); code != 1 {
		t.Errorf("a document of blank pages was emptied: %s", said)
	}
	// And the detector's own refusal reaches the exit status.
	if code, _, _ := exec("deblank", "-dpi", "100000", in, out); code != 1 {
		t.Error("a page past the pixel limit was accepted")
	}
}

// mixedFixture is two pages: one with content, one blank.
func mixedFixture(t *testing.T) string {
	t.Helper()
	return writeDoc(t, func(d *ops.Doc) {
		d.Blank(595, 842)
		if err := d.Stamp("1", ops.Stamp{Text: "words", Font: ops.Helvetica, Size: 24}); err != nil {
			t.Fatal(err)
		}
		d.Blank(595, 842)
	})
}

// blankFixture is two pages, both blank.
func blankFixture(t *testing.T) string {
	t.Helper()
	return writeDoc(t, func(d *ops.Doc) {
		d.Blank(595, 842)
		d.Blank(595, 842)
	})
}

func writeDoc(t *testing.T, build func(*ops.Doc)) string {
	t.Helper()
	d := ops.New()
	build(d)
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string { return strconv.Itoa(n) }

// repair writes a file back out with its structure rebuilt, and says whether
// rebuilding was needed.
//
// ⛔ Both answers are asserted, because a verb that said "rebuilt" whatever it
// was given would pass a test that only ever handed it a damaged file — and
// that is the direction this one would fail in, since the honest answer for a
// healthy file is the less interesting one.
func TestRepair(t *testing.T) {
	dir := t.TempDir()
	healthy, damaged := fixture(t, 2), damagedFixture(t)

	out := filepath.Join(dir, "healthy.pdf")
	code, said, msg := exec("repair", healthy, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "nothing needed rebuilding") {
		t.Errorf("a healthy file: said %q", said)
	}
	if got := len(contentsOf(t, out)); got != 2 {
		t.Errorf("%d pages came out, want 2", got)
	}

	out = filepath.Join(dir, "fixed.pdf")
	code, said, msg = exec("repair", damaged, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "rebuilt") || strings.Contains(said, "nothing") {
		t.Errorf("a damaged file: said %q", said)
	}
	if got := len(contentsOf(t, out)); got != 2 {
		t.Errorf("%d pages came out of the repair, want 2", got)
	}
	// ⛔ And what came out is itself clean: `repair` on the result says there
	// was nothing to do. A repair whose output still needs repairing is not
	// one.
	code, said, _ = exec("repair", "-check", out)
	if code != 0 || !strings.Contains(said, "nothing needed rebuilding") {
		t.Errorf("the repaired file still needs repairing: %q", said)
	}
}

func TestRepairCheck(t *testing.T) {
	code, said, msg := exec("repair", "-check", damagedFixture(t))
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "rebuilt") {
		t.Errorf("-check said %q", said)
	}
	// -check takes no output file, because it writes nothing.
	if code, _, _ := exec("repair", "-check", damagedFixture(t), filepath.Join(t.TempDir(), "x.pdf")); code != 1 {
		t.Error("-check accepted an output file")
	}
}

func TestRepairRefusals(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, _ := exec("repair", in); code != 1 {
		t.Error("no output file was accepted")
	}
	if code, _, _ := exec("repair", "/no/such/file.pdf", out); code != 1 {
		t.Error("a missing file was accepted")
	}
	if code, _, _ := exec("repair", "-nonsense", in, out); code != 1 {
		t.Error("an unknown flag was accepted")
	}
	// Both branches have their own save, so both need their own unwritable
	// output: a healthy file and a damaged one take different paths through
	// the verb and only one of them was covered by the first draft.
	if code, _, _ := exec("repair", in, "/no/such/directory/out.pdf"); code != 1 {
		t.Error("an unwritable output was accepted for a healthy file")
	}
	if code, _, _ := exec("repair", damagedFixture(t), "/no/such/directory/out.pdf"); code != 1 {
		t.Error("an unwritable output was accepted for a damaged file")
	}
}

// damagedFixture points a file's startxref at the wrong place, which is the
// commonest way a PDF arrives damaged: the objects are all there and the table
// that says where they are is not.
func damagedFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(fixture(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`startxref\s*\n\s*\d+`)
	broken := re.ReplaceAll(b, []byte("startxref\n999999"))
	if bytes.Equal(broken, b) {
		t.Fatal("the fixture has no startxref to break, so this proves nothing")
	}
	path := filepath.Join(t.TempDir(), "damaged.pdf")
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// labels sets what a viewer shows in its toolbar.
func TestLabels(t *testing.T) {
	in := fixture(t, 5)
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, _, msg := exec("labels",
		"-rule", "pages=1-2;style=r",
		"-rule", "pages=3-5;style=D",
		in, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	code, said, msg := exec("labels", "-list", out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	// ⛔ The POSITION and the label, both: the use of this tool is correcting
	// a document where the two disagree, so printing only the labels would
	// hide the thing being looked for.
	for _, want := range []string{"1\ti", "2\tii", "3\t1", "4\t2", "5\t3"} {
		if !strings.Contains(said, want) {
			t.Errorf("-list is missing %q:\n%s", want, said)
		}
	}
}

func TestLabelsWithAPrefixAndAStart(t *testing.T) {
	in := fixture(t, 3)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("labels", "-rule", "pages=all;style=D;prefix=A-;start=7", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	_, said, _ := exec("labels", "-list", out)
	for _, want := range []string{"1\tA-7", "2\tA-8", "3\tA-9"} {
		if !strings.Contains(said, want) {
			t.Errorf("missing %q:\n%s", want, said)
		}
	}
}

// ⛔ "continue" works with and without a value. A setting that only worked one
// of the two ways would be a trap in a shell script that built the spec.
func TestLabelsContinueBothWays(t *testing.T) {
	in := fixture(t, 6)
	dir := t.TempDir()
	for _, spec := range []string{
		"pages=1-2,5-6;style=D;continue",
		"pages=1-2,5-6;style=D;continue=true",
	} {
		out := filepath.Join(dir, "c.pdf")
		if code, _, msg := exec("labels", "-rule", spec, in, out); code != 0 {
			t.Fatalf("%s: code %d: %s", spec, code, msg)
		}
		_, said, _ := exec("labels", "-list", out)
		if !strings.Contains(said, "5\t3") {
			t.Errorf("%s did not carry the numbering:\n%s", spec, said)
		}
	}
	// And continue=false is off, so the second run starts again.
	out := filepath.Join(dir, "f.pdf")
	if code, _, msg := exec("labels", "-rule", "pages=1-2,5-6;style=D;continue=false", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	_, said, _ := exec("labels", "-list", out)
	if !strings.Contains(said, "5\t1") {
		t.Errorf("continue=false carried the numbering anyway:\n%s", said)
	}
}

func TestLabelsClear(t *testing.T) {
	in := fixture(t, 3)
	dir := t.TempDir()
	withLabels := filepath.Join(dir, "with.pdf")
	if code, _, msg := exec("labels", "-rule", "style=D", in, withLabels); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	bare := filepath.Join(dir, "bare.pdf")
	if code, _, msg := exec("labels", "-clear", withLabels, bare); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	code, said, _ := exec("labels", "-list", bare)
	if code != 0 || !strings.Contains(said, "no page labels") {
		t.Errorf("-list after -clear said %q", said)
	}
}

func TestLabelsRefusals(t *testing.T) {
	in := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, _ := exec("labels", "-rule", "style=D", in); code != 1 {
		t.Error("no output file was accepted")
	}
	if code, _, _ := exec("labels", "-list", in, out); code != 1 {
		t.Error("-list accepted an output file")
	}
	// ⛔ Neither -clear nor a rule is a mistake, and so is both: a caller who
	// said both meant one of them and the command cannot know which.
	if code, _, _ := exec("labels", in, out); code != 1 {
		t.Error("neither -clear nor -rule was accepted")
	}
	if code, _, _ := exec("labels", "-clear", "-rule", "style=D", in, out); code != 1 {
		t.Error("-clear with a rule was accepted")
	}
	for _, spec := range []string{"nonsense=1", "start=x", "style=Q", "pages=zzz"} {
		if code, _, _ := exec("labels", "-rule", spec, in, out); code != 1 {
			t.Errorf("-rule %q was accepted", spec)
		}
	}
	if code, _, _ := exec("labels", "-rule", "style=D", "/no/such/file.pdf", out); code != 1 {
		t.Error("a missing file was accepted")
	}
	if code, _, _ := exec("labels", "-list", "/no/such/file.pdf"); code != 1 {
		t.Error("-list on a missing file was accepted")
	}
	if code, _, _ := exec("labels", "-nonsense", in, out); code != 1 {
		t.Error("an unknown flag was accepted")
	}
	if code, _, _ := exec("labels", "-rule", "style=D", in, "/no/such/dir/out.pdf"); code != 1 {
		t.Error("an unwritable output was accepted")
	}
	// An empty rule spec is a rule over every page with no style: the prefix
	// alone, which is empty, so nothing is labelled. It must not crash.
	if code, _, _ := exec("labels", "-rule", "", in, out); code != 0 {
		t.Error("an empty rule was refused")
	}
}

// ⛔ A document with no labels says so rather than printing five blank lines.
func TestLabelsListOfADocumentWithNone(t *testing.T) {
	code, said, _ := exec("labels", "-list", fixture(t, 3))
	if code != 0 || !strings.Contains(said, "no page labels") {
		t.Errorf("code %d, said %q", code, said)
	}
}

// layeredFixture writes a one-page document with two layers, the second
// hidden, each with content marked as drawing on it.
func layeredFixture(t *testing.T) string {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	a := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Markup")})
	b := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Prices")})
	stream := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(
		"/OC /MC0 BDC\n1 0 0 RG 1 1 5 5 re S\nEMC\n/OC /MC1 BDC\n2 2 5 5 re S\nEMC\n")})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": stream,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(200)},
		"Resources": reader.Dict{"Properties": reader.Dict{"MC0": a, "MC1": b}}})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"OCProperties": reader.Dict{"OCGs": reader.Array{a, b},
			"D": reader.Dict{"Order": reader.Array{a, b},
				"ON": reader.Array{a}, "OFF": reader.Array{b}}},
	})})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "layered.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLayersList(t *testing.T) {
	code, said, msg := exec("layers", "-list", layeredFixture(t))
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	// ⛔ The STATE and the name, both: the use of this tool is finding the
	// layer that is off, so printing only the names would hide it.
	if !strings.Contains(said, "on") || !strings.Contains(said, "Markup") {
		t.Errorf("said %q", said)
	}
	if !strings.Contains(said, "off") || !strings.Contains(said, "Prices") {
		t.Errorf("the hidden layer is not reported as off:\n%s", said)
	}
}

// ⛔⛔ THE WITNESS, through the command: a hidden layer must still be hidden
// after any verb at all. Before this change `pdfops watermark` lost the
// optional content entirely and the hidden layer showed.
func TestAHiddenLayerSurvivesAVerbThroughTheCommand(t *testing.T) {
	in := layeredFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("watermark", "-text", "DRAFT", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	code, said, _ := exec("layers", "-list", out)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(said, "Prices") {
		t.Errorf("the hidden layer is gone after a watermark:\n%s", said)
	}
	if !strings.Contains(said, "off") {
		t.Errorf("the hidden layer came back VISIBLE after a watermark:\n%s", said)
	}
}

func TestLayersShowHideAddDrop(t *testing.T) {
	in := layeredFixture(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")
	if code, _, msg := exec("layers", "-show", "Prices", "-hide", "Markup",
		"-add", "Notes", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	_, said, _ := exec("layers", "-list", out)
	for _, want := range []string{"Notes", "Prices", "Markup"} {
		if !strings.Contains(said, want) {
			t.Errorf("missing %q:\n%s", want, said)
		}
	}
	// A child, by PARENT/NAME.
	child := filepath.Join(dir, "child.pdf")
	if code, _, msg := exec("layers", "-add", "Markup/Mine", in, child); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	_, said, _ = exec("layers", "-list", child)
	if !strings.Contains(said, "  on         Mine") {
		t.Errorf("the child is not shown nested:\n%s", said)
	}
	// Dropping a visible layer.
	dropped := filepath.Join(dir, "dropped.pdf")
	if code, _, msg := exec("layers", "-drop", "Markup", in, dropped); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	_, said, _ = exec("layers", "-list", dropped)
	if strings.Contains(said, "Markup") {
		t.Errorf("the layer was not dropped:\n%s", said)
	}
}

func TestLayersRefusals(t *testing.T) {
	in := layeredFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, _ := exec("layers", "-show", "Markup", in); code != 1 {
		t.Error("no output file was accepted")
	}
	if code, _, _ := exec("layers", "-list", in, out); code != 1 {
		t.Error("-list accepted an output file")
	}
	if code, _, _ := exec("layers", in, out); code != 1 {
		t.Error("neither -list nor a change was accepted")
	}
	if code, _, _ := exec("layers", "-list", "-show", "Markup", in); code != 1 {
		t.Error("-list with a change was accepted")
	}
	// ⛔ Dropping a HIDDEN layer is refused: its content stays and stops being
	// optional, so the hidden drawing becomes permanently visible.
	if code, _, said := exec("layers", "-drop", "Prices", in, out); code != 1 {
		t.Errorf("a hidden layer was dropped: %s", said)
	}
	for _, args := range [][]string{
		{"layers", "-show", "no such layer", in, out},
		{"layers", "-hide", "no such layer", in, out},
		{"layers", "-drop", "no such layer", in, out},
		{"layers", "-add", "x/y", in, out},
		{"layers", "-add", "", in, out},
		{"layers", "-nonsense", in, out},
		{"layers", "-show", "Markup", "/no/such/file.pdf", out},
		{"layers", "-list", "/no/such/file.pdf"},
		{"layers", "-show", "Markup", in, "/no/such/dir/out.pdf"},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v was accepted", args[1:])
		}
	}
}

func TestLayersListOfADocumentWithNone(t *testing.T) {
	code, said, _ := exec("layers", "-list", fixture(t, 1))
	if code != 0 || !strings.Contains(said, "no layers") {
		t.Errorf("code %d, said %q", code, said)
	}
}

// A locked layer cannot be switched, and the command says so.
func TestLayersRefusesALockedOne(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	grid := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Grid")})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(10), reader.Integer(10)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{}})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"OCProperties": reader.Dict{"OCGs": reader.Array{grid},
			"D": reader.Dict{"Order": reader.Array{grid}, "ON": reader.Array{grid},
				"Locked": reader.Array{grid}}},
	})})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "locked.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	code, said, _ := exec("layers", "-list", path)
	if code != 0 || !strings.Contains(said, "locked") {
		t.Errorf("-list does not report the lock: %q", said)
	}
	if code, _, _ := exec("layers", "-hide", "Grid", path,
		filepath.Join(t.TempDir(), "x.pdf")); code != 1 {
		t.Error("a locked layer was switched")
	}
}
