package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The named sizes are DERIVED from their definitions, so the test checks the
// definition rather than repeating the numbers: A4 is 210 x 297 mm, and a
// point is 1/72 of an inch.
func TestPaperSizesComeFromTheirDefinitions(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h float64
	}{
		{"a3", 841.89, 1190.55},
		{"a4", 595.28, 841.89},
		{"a5", 419.53, 595.28},
		{"letter", 612, 792},
		{"legal", 612, 1008},
		{"tabloid", 792, 1224},
	} {
		got, ok := paperSizes[c.name]
		if !ok {
			t.Errorf("no size called %q", c.name)
			continue
		}
		if math.Abs(got[0]-c.w) > 0.01 || math.Abs(got[1]-c.h) > 0.01 {
			t.Errorf("%s = %.2f x %.2f, want %.2f x %.2f", c.name, got[0], got[1], c.w, c.h)
		}
	}
	// Every A size is the one above it halved along its long side, which is
	// what ISO 216 is for. ⚠ It holds only to within half a MILLIMETRE,
	// because ISO rounds each size to whole millimetres: A4 is 297 mm tall
	// and A5 is 148 mm wide, not 148.5. A tolerance of 0.01 pt asserts
	// something the standard does not say, and this test failed on it until
	// the standard was read rather than assumed.
	// ⚠ EXACTLY on the boundary, so it needs an epsilon: A4 is 297 mm and A5
	// is 148 mm, and 297/2 - 148 is 0.5 mm to the last bit. In points the
	// difference computes as 1.4173228346456693 against a threshold of
	// 1.4173228346456692 -- one unit in the last place over, and the test
	// failed on that before the epsilon was added. A property that holds with
	// equality cannot be checked with a strict comparison in floating point.
	const halfAMillimetre = 0.5/25.4*72 + 1e-9
	a3, a4, a5 := paperSizes["a3"], paperSizes["a4"], paperSizes["a5"]
	if math.Abs(a3[1]/2-a4[0]) > halfAMillimetre {
		t.Errorf("A4's width %.2f is not half A3's height %.2f", a4[0], a3[1])
	}
	if math.Abs(a4[1]/2-a5[0]) > halfAMillimetre {
		t.Errorf("A5's width %.2f is not half A4's height %.2f", a5[0], a4[1])
	}
	if names := paperNames(); len(names) != len(paperSizes) || names[0] != "a3" {
		t.Errorf("paperNames = %v", names)
	}
}

// resize sets how big a page IS, which crop does not: crop sets what of it
// shows. The assertion is on the page box that comes back out.
func TestResize(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 2)
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("resize", "-to", "a4", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if w, h := mediaBox(t, out); math.Abs(w-595.28) > 0.01 || math.Abs(h-841.89) > 0.01 {
		t.Errorf("A4 came out %.2f x %.2f", w, h)
	}

	// a name is not case-sensitive, because `info` prints sizes in neither case
	if code, _, msg := exec("resize", "-to", "A4", in, out); code != 0 {
		t.Fatalf("upper case: code %d: %s", code, msg)
	}

	// ⛔ landscape turns it on its side, and the assertion has to be on the
	// ORDER of the box. "the file contains 841.8" is true of PORTRAIT A4 as
	// well -- that is its height -- so a Contains check cannot tell the two
	// apart, and a mutation removing the swap survived every test until this
	// read the box instead.
	if code, _, msg := exec("resize", "-to", "a4", "-landscape", in, out); code != 0 {
		t.Fatalf("landscape: code %d: %s", code, msg)
	}
	if w, h := mediaBox(t, out); math.Abs(w-841.89) > 0.01 || math.Abs(h-595.28) > 0.01 {
		t.Errorf("landscape A4 came out %.2f x %.2f, want the long side first", w, h)
	}

	// or an explicit box, on chosen pages
	if code, _, msg := exec("resize", "-pages", "1", "-box", "0,0,200,400", in, out); code != 0 {
		t.Fatalf("box: code %d: %s", code, msg)
	}

	for _, c := range []struct {
		why  string
		args []string
	}{
		{"neither -box nor -to", []string{"resize", in, out}},
		{"both -box and -to", []string{"resize", "-box", "0,0,10,10", "-to", "a4", in, out}},
		{"-landscape without -to", []string{"resize", "-landscape", "-box", "0,0,10,10", in, out}},
		{"a size that is not a size", []string{"resize", "-to", "foolscap", in, out}},
		{"a box that is not a box", []string{"resize", "-box", "0,0,10", in, out}},
		{"a box with no area", []string{"resize", "-box", "10,10,10,10", in, out}},
		{"a page range the file has not got", []string{"resize", "-pages", "99", "-to", "a4", in, out}},
		{"an input that is not there", []string{"resize", "-to", "a4", "/no/such/file.pdf", out}},
		{"a missing output", []string{"resize", "-to", "a4", in}},
		{"a bad flag", []string{"resize", "-nosuchflag", "-to", "a4", in, out}},
	} {
		if code, _, _ := exec(c.args...); code != 1 {
			t.Errorf("%s should fail", c.why)
		}
	}
}

// metadata writes what `info` reads, and the round trip is the assertion.
func TestMetadata(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("metadata", "-set", "Title=The Report", "-set", "/Author=Someone", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	code, listing, msg := exec("info", out)
	if code != 0 {
		t.Fatalf("info: code %d: %s", code, msg)
	}
	if !strings.Contains(listing, "The Report") {
		t.Errorf("info does not show the title: %q", listing)
	}
	// a leading slash is accepted, because `info` prints the name without one
	// and a person types what they saw
	if !strings.Contains(listing, "Someone") {
		t.Errorf("info does not show the author: %q", listing)
	}

	cleared := filepath.Join(dir, "cleared.pdf")
	if code, _, msg := exec("metadata", "-clear", out, cleared); code != 0 {
		t.Fatalf("clear: code %d: %s", code, msg)
	}
	if _, listing, _ := exec("info", cleared); strings.Contains(listing, "The Report") {
		t.Errorf("clearing left the title behind: %q", listing)
	}

	for _, c := range []struct {
		why  string
		args []string
	}{
		{"neither -set nor -clear", []string{"metadata", in, out}},
		{"both", []string{"metadata", "-clear", "-set", "Title=x", in, out}},
		{"no = in the value", []string{"metadata", "-set", "Title", in, out}},
		{"no key before the =", []string{"metadata", "-set", "=x", in, out}},
		{"only a slash before the =", []string{"metadata", "-set", "/=x", in, out}},
		{"an input that is not there", []string{"metadata", "-set", "Title=x", "/no/such/file.pdf", out}},
		{"a missing output", []string{"metadata", "-set", "Title=x", in}},
		{"a bad flag", []string{"metadata", "-nosuchflag", "-set", "Title=x", in, out}},
	} {
		if code, _, _ := exec(c.args...); code != 1 {
			t.Errorf("%s should fail", c.why)
		}
	}
}

// move takes a page out and puts it back somewhere else, so the ORDER is the
// assertion.
func TestMove(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 3)
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("move", "-from", "1", "-to", "3", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	got := contentsOf(t, out)
	want := []string{"page 2", "page 3", "page 1"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("page %d = %q, want %q (order = %v)", i+1, got[i], want[i], got)
		}
	}

	for _, c := range []struct {
		why  string
		args []string
	}{
		{"no -from", []string{"move", "-to", "2", in, out}},
		{"no -to", []string{"move", "-from", "1", in, out}},
		{"a page the file has not got", []string{"move", "-from", "9", "-to", "1", in, out}},
		{"an input that is not there", []string{"move", "-from", "1", "-to", "2", "/no/such/file.pdf", out}},
		{"a missing output", []string{"move", "-from", "1", "-to", "2", in}},
		{"a bad flag", []string{"move", "-nosuchflag", "-from", "1", "-to", "2", in, out}},
	} {
		if code, _, _ := exec(c.args...); code != 1 {
			t.Errorf("%s should fail", c.why)
		}
	}
}

// underlay draws beneath, where overlay draws over.
func TestUnderlay(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 2)
	mark := fixture(t, 1)
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("underlay", "-with", mark, in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if got := contentsOf(t, out); len(got) != 2 {
		t.Errorf("pages = %d, want the two it started with", len(got))
	}

	for _, c := range []struct {
		why  string
		args []string
	}{
		{"no -with", []string{"underlay", in, out}},
		{"a mark that is not there", []string{"underlay", "-with", "/no/such/mark.pdf", in, out}},
		{"an input that is not there", []string{"underlay", "-with", mark, "/no/such/file.pdf", out}},
		{"a mark with no pages", []string{"underlay", "-with", pagelessFixture(t), in, out}},
		{"a missing output", []string{"underlay", "-with", mark, in}},
		{"a bad flag", []string{"underlay", "-nosuchflag", "-with", mark, in, out}},
	} {
		if code, _, _ := exec(c.args...); code != 1 {
			t.Errorf("%s should fail", c.why)
		}
	}
}

// version sets what the file declares it is.
func TestVersion(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	out := filepath.Join(dir, "out.pdf")

	if code, _, msg := exec("version", "-set", "1.7", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if _, listing, _ := exec("info", out); !strings.Contains(listing, "1.7") {
		t.Errorf("info does not report the version that was set: %q", listing)
	}

	for _, c := range []struct {
		why  string
		args []string
	}{
		{"no -set", []string{"version", in, out}},
		{"an input that is not there", []string{"version", "-set", "1.7", "/no/such/file.pdf", out}},
		{"a missing output", []string{"version", "-set", "1.7", in}},
		{"a bad flag", []string{"version", "-nosuchflag", "-set", "1.7", in, out}},
	} {
		if code, _, _ := exec(c.args...); code != 1 {
			t.Errorf("%s should fail", c.why)
		}
	}
}

// -json writes the same reading as the lines, in a shape something else can
// parse. The assertion is that it PARSES and says the same thing, not that it
// contains a brace.
func TestTextAsJSON(t *testing.T) {
	// pageWithText, not fixture: fixture's content stream says "page 1" but
	// carries no font, so extract reads nothing off it. contentsOf reads the
	// stream; this verb reads the TEXT, and they are not the same question.
	in := pageWithText(t)

	code, out, msg := exec("text", "-json", in)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	var plain []jsonRun
	if err := json.Unmarshal([]byte(out), &plain); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(plain) != 2 {
		t.Fatalf("entries = %d, want one a page", len(plain))
	}
	if plain[0].Page != 1 || !strings.Contains(plain[0].Text, "page 1") {
		t.Errorf("first entry = %+v", plain[0])
	}
	// without -layout there is no position to report, and a zero is not a
	// position: the fields are absent rather than nought.
	if plain[0].X != nil || plain[0].Size != nil || plain[0].Invisible != nil {
		t.Errorf("a reading with no layout reported a position: %+v", plain[0])
	}

	code, out, msg = exec("text", "-layout", "-json", in)
	if code != 0 {
		t.Fatalf("layout: code %d: %s", code, msg)
	}
	var runs []jsonRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(runs) == 0 {
		t.Fatal("no runs")
	}
	r := runs[0]
	if r.X == nil || r.Y == nil || r.Size == nil || r.Invisible == nil || r.Unreadable == nil {
		t.Fatalf("a layout run is missing its fields: %+v", r)
	}
	// ⛔ false must be WRITTEN, not omitted. A reader that cannot tell "this
	// run is readable" from "this tool did not say" is reading a guess.
	if *r.Invisible || *r.Unreadable {
		t.Errorf("the fixture's text is neither invisible nor unreadable: %+v", r)
	}
	if !strings.Contains(out, `"invisible": false`) {
		t.Errorf("false was omitted rather than written:\n%s", out)
	}

	// an empty reading is [], never null
	code, out, _ = exec("text", "-json", "-pages", "all", pagelessFixture(t))
	if code != 0 {
		t.Fatalf("pageless: code %d", code)
	}
	if strings.Contains(out, "null") {
		t.Errorf("an empty reading came back as null: %q", out)
	}
}

// mediaBox reads the width and height of the first page's box out of the
// written file, so a test can say which way round a page is rather than only
// that a number appears somewhere in it.
func mediaBox(t *testing.T, path string) (w, h float64) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`/MediaBox\s*\[\s*([-0-9.]+)\s+([-0-9.]+)\s+([-0-9.]+)\s+([-0-9.]+)\s*\]`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("no /MediaBox in %s", path)
	}
	num := func(i int) float64 {
		v, err := strconv.ParseFloat(string(m[i]), 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	return num(3) - num(1), num(4) - num(2)
}
