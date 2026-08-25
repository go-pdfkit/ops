package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
