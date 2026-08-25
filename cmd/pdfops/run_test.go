package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
)

// fixture writes a document of n pages to a temporary file and returns its
// path; page k carries the content "page k".
func fixture(t *testing.T, n int) string {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	kids := make(reader.Array, 0, n)
	for i := 1; i <= n; i++ {
		content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(fmt.Sprintf("page %d", i))})
		kids = append(kids, w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(n),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	info := w.Add(reader.Dict{"Title": reader.String("a title")})
	out, err := w.Finish(reader.Dict{"Root": root, "Info": info})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// exec runs the tool and returns its exit code and what it printed.
func exec(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// contentsOf reads back every page of a written file.
func contentsOf(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := 1; i <= d.PageCount(); i++ {
		data, err := d.PageContent(i)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(data))
	}
	return got
}

func TestUsage(t *testing.T) {
	code, _, errOut := exec()
	if code != 2 || !strings.Contains(errOut, "pdfops —") {
		t.Errorf("code %d, output %q", code, errOut)
	}
	code, _, errOut = exec("nonsense")
	if code != 2 || !strings.Contains(errOut, "no such command") {
		t.Errorf("code %d, output %q", code, errOut)
	}
	code, _, errOut = exec("-nosuchflag")
	if code != 2 || !strings.Contains(errOut, "pdfops:") {
		t.Errorf("code %d, output %q", code, errOut)
	}
}

func TestMainCallsRun(t *testing.T) {
	old, oldArgs := osExit, os.Args
	defer func() { osExit, os.Args = old, oldArgs }()
	got := -1
	osExit = func(code int) { got = code }
	os.Args = []string{"pdfops"}
	main()
	if got != 2 {
		t.Errorf("exit code %d", got)
	}
}

func TestMerge(t *testing.T) {
	in := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("merge", out, in, in); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 4 || got[0] != "page 1" || got[2] != "page 1" {
		t.Errorf("pages = %v", got)
	}
	if code, _, _ := exec("merge", out); code != 1 {
		t.Error("too few arguments should fail")
	}
	if code, _, _ := exec("merge", out, "/no/such/file.pdf"); code != 1 {
		t.Error("a missing input should fail")
	}
	if code, _, _ := exec("merge", "-nosuchflag", out, in); code != 1 {
		t.Error("a bad flag should fail")
	}
	if code, _, _ := exec("merge", "/no/such/dir/out.pdf", in); code != 1 {
		t.Error("an unwritable output should fail")
	}
}

func TestSelectDeleteReverse(t *testing.T) {
	in := fixture(t, 4)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("select", "-pages", "3-2", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 2 || got[0] != "page 3" {
		t.Errorf("select = %v", got)
	}
	if code, _, msg := exec("delete", "-pages", "1,2", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 2 || got[0] != "page 3" {
		t.Errorf("delete = %v", got)
	}
	if code, _, msg := exec("reverse", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); got[0] != "page 4" {
		t.Errorf("reverse = %v", got)
	}

	for _, args := range [][]string{
		{"select", in},
		{"select", "-nosuchflag", in, out},
		{"select", "-pages", "9", in, out},
		{"select", "-pages", "1", "/no/such/file.pdf", out},
		{"select", "-pages", "1", in, "/no/such/dir/x.pdf"},
		{"delete", in},
		{"delete", "-nosuchflag", in, out},
		{"delete", "-pages", "9", in, out},
		{"delete", "-pages", "all", in, out},
		{"delete", "-pages", "1", "/no/such/file.pdf", out},
		{"reverse", in},
		{"reverse", "-nosuchflag", in, out},
		{"reverse", "/no/such/file.pdf", out},
		{"reverse", in, "/no/such/dir/x.pdf"},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestRotate(t *testing.T) {
	in := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("rotate", "-pages", "all", "-by", "90", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	b, _ := os.ReadFile(out)
	d, _ := ops.Open(b)
	if got, _ := d.Rotation(1); got != 90 {
		t.Errorf("rotation = %d", got)
	}
	if code, _, msg := exec("rotate", "-absolute", "-by", "180", out, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	b, _ = os.ReadFile(out)
	d, _ = ops.Open(b)
	if got, _ := d.Rotation(1); got != 180 {
		t.Errorf("absolute rotation = %d", got)
	}
	for _, args := range [][]string{
		{"rotate", in},
		{"rotate", "-nosuchflag", in, out},
		{"rotate", "-by", "45", in, out},
		{"rotate", "-absolute", "-by", "45", in, out},
		{"rotate", "-by", "90", "/no/such/file.pdf", out},
		{"rotate", "-by", "90", in, "/no/such/dir/x.pdf"},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestCrop(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("crop", "-box", "5,5,50,100", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if code, _, msg := exec("crop", "-media", "-box", "0,0,300,400", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	b, _ := os.ReadFile(out)
	src, _ := reader.Open(b)
	page, _ := src.Page(1)
	if s := string(reader.FormatObject(page.Get("MediaBox"))); s != "[0 0 300 400]" {
		t.Errorf("MediaBox = %s", s)
	}
	for _, args := range [][]string{
		{"crop", in},
		{"crop", "-nosuchflag", in, out},
		{"crop", "-box", "1,2", in, out},
		{"crop", "-box", "a,2,3,4", in, out},
		{"crop", "-box", "9,9,1,1", in, out},
		{"crop", "-media", "-box", "9,9,1,1", in, out},
		{"crop", "-box", "0,0,1,1", "/no/such/file.pdf", out},
		{"crop", "-box", "0,0,1,1", in, "/no/such/dir/x.pdf"},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestSplit(t *testing.T) {
	in := fixture(t, 3)
	dir := filepath.Join(t.TempDir(), "parts")
	code, out, msg := exec("split", "-every", "2", in, dir)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	lines := strings.Fields(strings.TrimSpace(out))
	if len(lines) != 2 {
		t.Fatalf("printed %v", lines)
	}
	if got := contentsOf(t, lines[0]); len(got) != 2 {
		t.Errorf("first part = %v", got)
	}
	for _, args := range [][]string{
		{"split", in},
		{"split", "-nosuchflag", in, dir},
		{"split", "-every", "0", in, dir},
		{"split", "-every", "1", "/no/such/file.pdf", dir},
		{"split", "-every", "1", in, filepath.Join(in, "sub")},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
	// A directory that can be made but not written into.
	readOnly := filepath.Join(t.TempDir(), "ro")
	if err := os.MkdirAll(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := exec("split", "-every", "1", in, readOnly); code != 1 {
		t.Error("an unwritable directory should fail")
	}
}

func TestInfo(t *testing.T) {
	in := fixture(t, 2)
	code, out, msg := exec("info", in)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	for _, want := range []string{"version", "pages      2", "title", "page 1", "rotate 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	for _, args := range [][]string{
		{"info"},
		{"info", "-nosuchflag", in},
		{"info", "/no/such/file.pdf"},
		{"info", filepath.Join(t.TempDir(), "notpdf")},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
	junk := filepath.Join(t.TempDir(), "junk.pdf")
	if err := os.WriteFile(junk, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := exec("info", junk); code != 1 {
		t.Error("a file that is not a PDF should fail")
	}
}

func TestStrip(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("strip", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	b, _ := os.ReadFile(out)
	d, _ := ops.Open(b)
	if d.Info() != nil {
		t.Errorf("metadata survived: %v", d.Info())
	}
	for _, args := range [][]string{
		{"strip", in},
		{"strip", "-nosuchflag", in, out},
		{"strip", "/no/such/file.pdf", out},
		{"strip", in, "/no/such/dir/x.pdf"},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestPasswordFlagReachesTheReader(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")
	// An unencrypted file ignores the password rather than refusing it.
	if code, _, msg := exec("-password", "secret", "reverse", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
}

func TestParseBox(t *testing.T) {
	if got, err := parseBox(" 1 , 2 ,3, 4 "); err != nil || got != [4]float64{1, 2, 3, 4} {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestNUpBookletOverlayBlank(t *testing.T) {
	in := fixture(t, 4)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("nup", "-n", "2", in, out); code != 0 {
		t.Fatalf("nup: code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 2 {
		t.Errorf("nup gave %d sheets", len(got))
	}
	if code, _, msg := exec("booklet", in, out); code != 0 {
		t.Fatalf("booklet: code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 2 {
		t.Errorf("booklet gave %d sheets", len(got))
	}
	if code, _, msg := exec("overlay", "-with", in, in, out); code != 0 {
		t.Fatalf("overlay: code %d: %s", code, msg)
	}
	if code, _, msg := exec("overlay", "-under", "-with", in, in, out); code != 0 {
		t.Fatalf("underlay: code %d: %s", code, msg)
	}
	if code, _, msg := exec("blank", "-before", "2", in, out); code != 0 {
		t.Fatalf("blank: code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 5 || got[1] != "" {
		t.Errorf("blank gave %q", got)
	}

	for _, args := range [][]string{
		{"nup", in},
		{"nup", "-nosuchflag", in, out},
		{"nup", "-n", "0", in, out},
		{"nup", "-n", "2", "/no/such/file.pdf", out},
		{"nup", "-n", "2", in, "/no/such/dir/x.pdf"},
		{"booklet", in},
		{"booklet", "-nosuchflag", in, out},
		{"booklet", "/no/such/file.pdf", out},
		{"booklet", in, "/no/such/dir/x.pdf"},
		{"overlay", in},
		{"overlay", "-nosuchflag", in, out},
		{"overlay", "-with", "/no/such/file.pdf", in, out},
		{"overlay", "-with", in, "/no/such/file.pdf", out},
		{"overlay", "-with", in, in, "/no/such/dir/x.pdf"},
		{"blank", in},
		{"blank", "-nosuchflag", in, out},
		{"blank", "-before", "0", in, out},
		{"blank", "-before", "1", "/no/such/file.pdf", out},
		{"blank", "-before", "1", in, "/no/such/dir/x.pdf"},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestOverlayOfAnEmptyDocumentFails(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")
	// A one-page file whose only page has been dropped cannot be drawn.
	if code, _, _ := exec("overlay", "-with", out, in, out); code != 1 {
		t.Error("an unreadable overlay should fail")
	}
	if code, _, _ := exec("overlay", "-under", "-with", "/no/such.pdf", in, out); code != 1 {
		t.Error("an unreadable underlay should fail")
	}
}

// pagelessFixture writes a file whose page tree is empty — legal, and the one
// thing several verbs have nothing to work with.
func pagelessFixture(t *testing.T) string {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{}, "Count": reader.Integer(0)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "empty.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerbsThatNeedAPage(t *testing.T) {
	empty := pagelessFixture(t)
	in := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	for _, args := range [][]string{
		{"booklet", empty, out},
		{"nup", "-n", "2", empty, out},
		{"overlay", "-with", empty, in, out},
		{"overlay", "-under", "-with", empty, in, out},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestStampingVerbs(t *testing.T) {
	in := fixture(t, 3)
	out := filepath.Join(t.TempDir(), "out.pdf")

	if code, _, msg := exec("watermark", "-text", "DRAFT", in, out); code != 0 {
		t.Fatalf("watermark: code %d: %s", code, msg)
	}
	if code, _, msg := exec("number", "-format", "{page} of {pages}", in, out); code != 0 {
		t.Fatalf("number: code %d: %s", code, msg)
	}
	if code, _, msg := exec("bates", "-prefix", "EX", "-start", "5", "-digits", "4", in, out); code != 0 {
		t.Fatalf("bates: code %d: %s", code, msg)
	}
	if code, _, msg := exec("stamp", "-text", "seen", "-at", "top-right", "-bold",
		"-size", "8", "-rotate", "30", "-opacity", "0.5", in, out); code != 0 {
		t.Fatalf("stamp: code %d: %s", code, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	content, err := d.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "(seen) Tj") {
		t.Errorf("content = %q", content)
	}

	for _, args := range [][]string{
		{"watermark", in},
		{"watermark", "-nosuchflag", in, out},
		{"watermark", "-text", "", in, out},
		{"watermark", "-text", "x", "/no/such/file.pdf", out},
		{"watermark", "-text", "x", in, "/no/such/dir/x.pdf"},
		{"number", in},
		{"number", "-nosuchflag", in, out},
		{"number", "-pages", "9", in, out},
		{"number", "/no/such/file.pdf", out},
		{"number", in, "/no/such/dir/x.pdf"},
		{"bates", in},
		{"bates", "-nosuchflag", in, out},
		{"bates", "-pages", "9", in, out},
		{"bates", "/no/such/file.pdf", out},
		{"bates", in, "/no/such/dir/x.pdf"},
		{"stamp", in},
		{"stamp", "-nosuchflag", in, out},
		{"stamp", "-text", "x", "-at", "nowhere", in, out},
		{"stamp", "-text", "", in, out},
		{"stamp", "-text", "x", "/no/such/file.pdf", out},
		{"stamp", "-text", "x", in, "/no/such/dir/x.pdf"},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestSanitizeFlattenAndStripFlags(t *testing.T) {
	in := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	for _, args := range [][]string{
		{"sanitize", in, out},
		{"flatten", in, out},
		{"strip", "-annotations", "-bookmarks", in, out},
		{"strip", "-metadata=false", in, out},
	} {
		if code, _, msg := exec(args...); code != 0 {
			t.Fatalf("%v: code %d: %s", args, code, msg)
		}
	}
	for _, args := range [][]string{
		{"sanitize", in},
		{"sanitize", "-nosuchflag", in, out},
		{"sanitize", "/no/such/file.pdf", out},
		{"sanitize", in, "/no/such/dir/x.pdf"},
		{"flatten", in},
		{"flatten", "-nosuchflag", in, out},
		{"flatten", "/no/such/file.pdf", out},
		{"flatten", in, "/no/such/dir/x.pdf"},
		{"strip", "-nosuchflag", in, out},
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}

func TestCompressVerb(t *testing.T) {
	in := fixture(t, 30)
	out := filepath.Join(t.TempDir(), "packed.pdf")
	if code, _, errOut := exec("compress", in, out); code != 0 {
		t.Fatalf("compress said %d: %s", code, errOut)
	}
	before, _ := os.Stat(in)
	after, _ := os.Stat(out)
	if after.Size() >= before.Size() {
		t.Errorf("packed is %d bytes and plain is %d", after.Size(), before.Size())
	}
	if got := contentsOf(t, out); len(got) != 30 || got[0] != "page 1" {
		t.Errorf("the packed file reads back as %v", got)
	}
	if code, _, _ := exec("compress", in); code == 0 {
		t.Error("compress with one path was accepted")
	}
	if code, _, _ := exec("compress", "nowhere.pdf", out); code == 0 {
		t.Error("compress read a file that is not there")
	}
	if code, _, _ := exec("compress", "-nope", in, out); code == 0 {
		t.Error("compress took a flag it has never heard of")
	}
}

func TestEncryptDecryptAndPermissionsVerbs(t *testing.T) {
	in := fixture(t, 3)
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked.pdf")
	if code, _, errOut := exec("encrypt", "-user", "u", "-owner", "o", "-allow", "print,copy", in, locked); code != 0 {
		t.Fatalf("encrypt said %d: %s", code, errOut)
	}
	b, err := os.ReadFile(locked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Open(b); err == nil {
		t.Error("the encrypted file opened with no password")
	}

	code, out, errOut := exec("-password", "u", "permissions", locked)
	if code != 0 {
		t.Fatalf("permissions said %d: %s", code, errOut)
	}
	for _, want := range []string{"AES-256", "the user", "print, copy"} {
		if !strings.Contains(out, want) {
			t.Errorf("permissions did not mention %q:\n%s", want, out)
		}
	}
	if _, out, _ = exec("-password", "o", "permissions", locked); !strings.Contains(out, "the owner") {
		t.Errorf("the owner password was not recognised:\n%s", out)
	}
	if _, out, _ = exec("permissions", in); !strings.Contains(out, "protection none") {
		t.Errorf("an unprotected file reported:\n%s", out)
	}

	plain := filepath.Join(dir, "plain.pdf")
	if code, _, errOut := exec("-password", "u", "decrypt", locked, plain); code != 0 {
		t.Fatalf("decrypt said %d: %s", code, errOut)
	}
	if got := contentsOf(t, plain); len(got) != 3 || got[2] != "page 3" {
		t.Errorf("the decrypted file reads back as %v", got)
	}

	// The older method, for readers from before 2008.
	old := filepath.Join(dir, "old.pdf")
	if code, _, errOut := exec("encrypt", "-user", "u", "-aes128", in, old); code != 0 {
		t.Fatalf("encrypt -aes128 said %d: %s", code, errOut)
	}
	if _, out, _ = exec("-password", "u", "permissions", old); !strings.Contains(out, "AES-128") {
		t.Errorf("-aes128 produced:\n%s", out)
	}
}

func TestTheVerbsThatProtectRefuseWhatTheyShould(t *testing.T) {
	in := fixture(t, 2)
	out := filepath.Join(t.TempDir(), "out.pdf")
	cases := []struct {
		name string
		args []string
	}{
		{"encrypt with no password at all", []string{"encrypt", in, out}},
		{"encrypt with a permission nobody has heard of", []string{"encrypt", "-user", "u", "-allow", "fly", in, out}},
		{"encrypt with one path", []string{"encrypt", "-user", "u", in}},
		{"encrypt a file that is not there", []string{"encrypt", "-user", "u", "nowhere.pdf", out}},
		{"encrypt with an unknown flag", []string{"encrypt", "-nope", in, out}},
		{"decrypt with one path", []string{"decrypt", in}},
		{"decrypt a file that is not there", []string{"decrypt", "nowhere.pdf", out}},
		{"decrypt with an unknown flag", []string{"decrypt", "-nope", in, out}},
		{"permissions with no path", []string{"permissions"}},
		{"permissions on a file that is not there", []string{"permissions", "nowhere.pdf"}},
		{"permissions with an unknown flag", []string{"permissions", "-nope", in}},
	}
	for _, c := range cases {
		if code, _, _ := exec(c.args...); code == 0 {
			t.Errorf("%s was accepted", c.name)
		}
	}
	// Asking for everything is what saying nothing means, and "none" is a
	// way of saying it out loud.
	for _, allow := range []string{"", "all", "none", "print, copy"} {
		if code, _, errOut := exec("encrypt", "-user", "u", "-allow", allow, in, out); code != 0 {
			t.Errorf("-allow %q said %d: %s", allow, code, errOut)
		}
	}
}

// pageWithText writes a document whose page carries real text and a picture,
// so that reading it back can be checked.
func pageWithText(t *testing.T) string {
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
	image := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(2), "Height": reader.Integer(2),
		"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
	}, Raw: []byte{0, 64, 128, 255}})
	jpeg := w.Add(&reader.Stream{Dict: reader.Dict{
		"Subtype": reader.Name("Image"), "Width": reader.Integer(1),
		"Height": reader.Integer(1), "Filter": reader.Name("DCTDecode"),
	}, Raw: []byte("pretend")})
	kids := make(reader.Array, 0, 2)
	for i := 1; i <= 2; i++ {
		content := fmt.Sprintf("BT /F1 12 Tf 20 100 Td (page %d says hello) Tj ET "+
			"q 20 0 0 20 10 10 cm /Im Do Q q 5 0 0 5 50 50 cm /Jp Do Q", i)
		kids = append(kids, w.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(200), reader.Integer(200)},
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)}),
			"Resources": reader.Dict{
				"Font":    reader.Dict{"F1": font},
				"XObject": reader.Dict{"Im": image, "Jp": jpeg},
			},
		}))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": kids, "Count": reader.Integer(len(kids))})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "text.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTextVerb(t *testing.T) {
	in := pageWithText(t)
	code, out, errOut := exec("text", in)
	if code != 0 {
		t.Fatalf("text said %d: %s", code, errOut)
	}
	for _, want := range []string{"page 1 says hello", "page 2 says hello"} {
		if !strings.Contains(out, want) {
			t.Errorf("the text does not hold %q:\n%s", want, out)
		}
	}
	if _, out, _ = exec("text", "-pages", "2", in); strings.Contains(out, "page 1") {
		t.Errorf("a range of one page gave:\n%s", out)
	}
	// The layout form says where every piece sits.
	code, out, errOut = exec("text", "-layout", "-pages", "1", in)
	if code != 0 {
		t.Fatalf("text -layout said %d: %s", code, errOut)
	}
	if !strings.Contains(out, "\t20.00\t100.00\t12.00\t") {
		t.Errorf("the layout does not say where the text is:\n%s", out)
	}
	for _, args := range [][]string{
		{"text"},
		{"text", "nowhere.pdf"},
		{"text", "-nope", in},
		{"text", "-pages", "nonsense", in},
	} {
		if code, _, _ := exec(args...); code == 0 {
			t.Errorf("%v was accepted", args)
		}
	}
}

func TestTextVerbSaysWhatItCouldNotRead(t *testing.T) {
	// A run nothing in the document could name is marked rather than left
	// out, and so is one drawn with no ink.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	font := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
		"FontDescriptor": w.Add(reader.Dict{"Flags": reader.Integer(4)}),
	})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{},
			Raw: []byte("BT 3 Tr /F1 12 Tf 20 100 Td (\x01\x02) Tj ET")}),
		"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "unreadable.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	_, printed, _ := exec("text", "-layout", path)
	if !strings.Contains(printed, "[invisible, part unreadable]") {
		t.Errorf("the run was not marked:\n%s", printed)
	}
}

func TestImagesVerb(t *testing.T) {
	in := pageWithText(t)
	dir := filepath.Join(t.TempDir(), "pictures")
	code, out, errOut := exec("images", in, dir)
	if code != 0 {
		t.Fatalf("images said %d: %s", code, errOut)
	}
	if !strings.Contains(out, "page001-01.samples") || !strings.Contains(out, "page001-02.jpg") {
		t.Errorf("the pictures are:\n%s", out)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Errorf("%d files were written", len(files))
	}
	jpg, err := os.ReadFile(filepath.Join(dir, "page001-02.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(jpg) != "pretend" {
		t.Errorf("the JPEG was changed on the way out: %q", jpg)
	}
	// A document with no pictures says so.
	plain := fixture(t, 1)
	if _, out, _ = exec("images", plain, filepath.Join(t.TempDir(), "none")); !strings.Contains(out, "no pictures") {
		t.Errorf("a document with no pictures said:\n%s", out)
	}
	for _, args := range [][]string{
		{"images", in},
		{"images", "nowhere.pdf", dir},
		{"images", "-nope", in, dir},
		{"images", "-pages", "nonsense", in, dir},
		{"images", in, "/dev/null/cannot"},
	} {
		if code, _, _ := exec(args...); code == 0 {
			t.Errorf("%v was accepted", args)
		}
	}
}

func TestHowARunIsMarked(t *testing.T) {
	for _, c := range []struct {
		run  extract.Run
		want string
	}{
		{extract.Run{}, ""},
		{extract.Run{Invisible: true}, "[invisible] "},
		{extract.Run{Unreadable: true}, "[part unreadable] "},
		{extract.Run{Invisible: true, Unreadable: true}, "[invisible, part unreadable] "},
	} {
		if got := marks(c.run); got != c.want {
			t.Errorf("%+v is marked %q, want %q", c.run, got, c.want)
		}
	}
}

func TestAPictureThatCannotBeWritten(t *testing.T) {
	// Somewhere to put the pictures where one of them cannot go: a directory
	// already standing where a file should be written.
	in := pageWithText(t)
	dir := filepath.Join(t.TempDir(), "pictures")
	if err := os.MkdirAll(filepath.Join(dir, "page001-01.samples"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := exec("images", in, dir); code == 0 {
		t.Error("writing over a directory was accepted")
	}
}

func TestEveryWayAPictureIsNamed(t *testing.T) {
	for _, c := range []struct {
		filter reader.Name
		want   string
	}{
		{"DCTDecode", ".jpg"},
		{"JPXDecode", ".jp2"},
		{"JBIG2Decode", ".jbig2"},
		{"", ".samples"},
	} {
		if got := imageSuffix(extract.Image{Filter: c.filter}); got != c.want {
			t.Errorf("%q is written as %q, want %q", c.filter, got, c.want)
		}
	}
}
