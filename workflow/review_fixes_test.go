package workflow

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/go-pdfkit/ops"
)

// An empty result is refused with a named error, never a panic and never a
// run that "succeeds" with no file.
func TestEmptyResultRefused(t *testing.T) {
	defer noPanic(t, "empty result")
	w := WF([]Node{N("in", "PDFInputNode"), N("ex", "ExtractPagesNode", "pages", "even"), N("am", "AlternateMergeNode"), N("out", "DownloadNode")},
		C("in", "ex"), C("ex", "am"), C("am", "out"))
	res, err := run(t, w, one(t, 1), Options{})
	var ne *NodeError
	if !errors.As(err, &ne) || ne.ID != "ex" || !strings.Contains(err.Error(), "no document") {
		t.Fatalf("%v", err)
	}
	if res.Status["ex"] != Failed {
		t.Fatal(res.Status)
	}
	// A document with no pages is refused too.
	w = line(N("", "SplitNode", "pages", "even"))
	if _, err := run(t, w, one(t, 1), Options{}); err == nil || !strings.Contains(err.Error(), "no pages") {
		t.Fatalf("%v", err)
	}
}

func TestInterleaveOfNothing(t *testing.T) {
	defer noPanic(t, "interleave of nothing")
	if _, err := runInterleave(&exec{}); err == nil {
		t.Fatal("no error")
	}
}

// A spec that repeats pages is refused before it is built.
func TestSpecBudgetBeforeWork(t *testing.T) {
	long := strings.Repeat("*,", 120) + "*"
	w := line(N("", "SplitNode", "pages", long))
	start := time.Now()
	_, err := run(t, w, one(t, 50), Options{MaxPages: 100})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("slow: the spec was built before it was refused")
	}
	// A one-page document and the same spec still runs.
	if res, err := run(t, w, one(t, 1), Options{}); err != nil || len(res.Files) != 1 {
		t.Fatalf("%v", err)
	}
	// A spec over MaxSpecLength is refused.
	w = line(N("", "SplitNode", "pages", strings.Repeat("1,", MaxSpecLength)))
	if _, err := run(t, w, one(t, 1), Options{}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("%v", err)
	}
	// An unparsable part is refused.
	if _, err := run(t, line(N("", "SplitNode", "pages", "1,zz")), one(t, 2), Options{}); err == nil {
		t.Fatal("zz")
	}
}

// The pages of every item are added up, not each checked alone.
func TestExtractBudgetIsCumulative(t *testing.T) {
	var files []File
	for i := 0; i < 5; i++ {
		files = append(files, File{"a.pdf", pdf(t, 4)})
	}
	w := line(N("", "ExtractPagesNode", "pages", "1-4"))
	res, err := run(t, w, in(files...), Options{MaxPages: 30}) // 20 in, 20 out: over 30
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("%v %v", res, err)
	}
	if _, err := run(t, w, in(files[:1]...), Options{MaxPages: 30}); err != nil {
		t.Fatal(err)
	}
}

func TestSelectBudgetIsCumulative(t *testing.T) {
	st := &state{maxPages: 10}
	a, b := ops.New(), ops.New()
	for i := 0; i < 3; i++ {
		a.Blank(10, 10)
		b.Blank(10, 10)
	}
	x := &exec{node: N("n", "SplitNode", "pages", "1-3,1-3"), in: map[string][]item{"pdf": {{"a", a}, {"b", b}}}, st: st}
	if _, err := runSelect(x); !errors.Is(err, ErrBudget) { // 6 + 6 > 10
		t.Fatalf("%v", err)
	}
	if a.PageCount() != 3 || b.PageCount() != 3 {
		t.Fatal("a document was changed before the refusal")
	}
}

func TestStrictJSON(t *testing.T) {
	node := `{"id":"a","type":"RotateNode","controls":{}}`
	for name, src := range map[string]string{
		"duplicate nodes":           `{"nodes":[` + node + `],"nodes":[],"connections":[]}`,
		"case nodes":                `{"nodes":[` + node + `],"NODES":[],"connections":[]}`,
		"case node key":             `{"nodes":[{"ID":"a","type":"RotateNode"}],"connections":[]}`,
		"duplicate node key":        `{"nodes":[{"id":"a","id":"b","type":"RotateNode"}],"connections":[]}`,
		"case connection":           `{"nodes":[],"connections":[{"Id":"c"}]}`,
		"dup connection":            `{"nodes":[],"connections":[{"id":"c","id":"d"}]}`,
		"dup controls":              `{"nodes":[{"id":"a","type":"RotateNode","controls":{"angle":1,"angle":2}}],"connections":[]}`,
		"null node":                 `{"nodes":[null],"connections":[]}`,
		"null file":                 `null`,
		"node is a list":            `{"nodes":[[]],"connections":[]}`,
		"node id is a number":       `{"nodes":[{"id":1}],"connections":[]}`,
		"connection id is a number": `{"nodes":[],"connections":[{"id":1}]}`,
	} {
		if _, err := Parse([]byte(src)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Elements are not decoded when there are too many.
	many := `{"nodes":[` + strings.Repeat("0,", MaxNodes) + `0],"connections":[]}`
	if _, err := Parse([]byte(many)); err == nil || !strings.Contains(err.Error(), "nodes; at most") {
		t.Fatalf("%v", err)
	}
	if _, err := Parse([]byte(`{"nodes":[],"connections":[` + strings.Repeat("0,", MaxConnections) + `0]}`)); err == nil || !strings.Contains(err.Error(), "connections; at most") {
		t.Fatalf("%v", err)
	}
	// null controls are fine.
	if _, err := Parse([]byte(`{"nodes":[{"id":"a","type":"RotateNode","controls":null}],"connections":[]}`)); err != nil {
		t.Fatal(err)
	}
}

// An id cannot forge a line of output.
func TestIDsAreQuotedAndPrintable(t *testing.T) {
	id := "x\nok: 3 nodes\x1b[2K"
	s := Issue{Error, id, "hm"}.String()
	if strings.ContainsAny(s, "\n\x1b") {
		t.Fatalf("%q", s)
	}
	w := WF([]Node{N("in", "PDFInputNode"), N(id, "RotateNode"), N("o", "DownloadNode")}, C("in", id), C(id, id), C(id, "o"))
	for _, i := range Check(w) {
		if strings.ContainsAny(i.String(), "\n\x1b") {
			t.Fatalf("%q", i.String())
		}
	}
	ne := &NodeError{ID: id, Label: "L", Err: errors.New("e")}
	if strings.ContainsAny(ne.Error(), "\n\x1b") {
		t.Fatal(ne.Error())
	}
	for name, src := range map[string]string{
		"node id":     `{"nodes":[{"id":"a\nb","type":"RotateNode"}],"connections":[]}`,
		"node type":   `{"nodes":[{"id":"a","type":"R\u001bx"}],"connections":[]}`,
		"control":     `{"nodes":[{"id":"a","type":"RotateNode","controls":{"a\nb":1}}],"connections":[]}`,
		"connection":  `{"nodes":[],"connections":[{"id":"c","source":"a\u001b","sourceOutput":"","target":"","targetInput":""}]}`,
		"long source": `{"nodes":[],"connections":[{"id":"c","source":"` + strings.Repeat("a", MaxIDLength+1) + `"}]}`,
	} {
		if _, err := Parse([]byte(src)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// An unsupported node type is quoted.
	for _, i := range Check(WF([]Node{N("a", "OCRNode")})) {
		if strings.Contains(i.Message, "OCRNode has") {
			t.Fatal(i.Message)
		}
	}
}

func TestOutputNames(t *testing.T) {
	for in, want := range map[string]string{
		"C:x.pdf":   "C_x.pdf",
		"CON":       "_CON.pdf",
		"nul.pdf":   "_nul.pdf",
		"COM1":      "_COM1.pdf",
		"lpt9.x":    "_lpt9.x.pdf",
		"a.pdf ":    "a.pdf",
		"a. . ":     "a.pdf",
		"CON .pdf":  "_CON.pdf",
		"COM0":      "COM0.pdf",
		"console":   "console.pdf",
		"a<b>c.pdf": "a_b_c.pdf",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	// Three bytes a character, so the cut falls inside one.
	long := cleanName(strings.Repeat("€", 70000))
	if len(long) > MaxNameBytes || !strings.HasSuffix(long, ".pdf") || !utf8.ValidString(long) {
		t.Fatalf("%d", len(long))
	}
	r := &Result{Files: []File{{Name: "A.pdf"}, {Name: "a.pdf"}, {Name: "a.PDF"}, {Name: strings.Repeat("x", 200)}, {Name: strings.Repeat("x", 200)}}}
	r.name()
	seen := map[string]bool{}
	for _, f := range r.Files {
		l := strings.ToLower(f.Name)
		if seen[l] || len(f.Name) > MaxNameBytes {
			t.Fatalf("%v", r.Files)
		}
		seen[l] = true
	}
}

// Superscript digits and the console devices are reserved, and a name written
// precomposed or decomposed is one name.
func TestOutputNamesReservedAndNormalised(t *testing.T) {
	for in, want := range map[string]string{
		"COM¹":    "_COM¹.pdf",
		"lpt².x":  "_lpt².x.pdf",
		"COM³":    "_COM³.pdf",
		"CONIN$":  "_CONIN$.pdf",
		"conout$": "_conout$.pdf",
		"COM⁴":    "COM⁴.pdf",
		"1":       "1.pdf",
		"¹":       "¹.pdf",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	r := &Result{Files: []File{{Name: "é.pdf"}, {Name: "é.pdf"}}}
	r.name()
	if norm.NFC.String(strings.ToLower(r.Files[0].Name)) == norm.NFC.String(strings.ToLower(r.Files[1].Name)) {
		t.Errorf("NFC and NFD names collide: %q", []string{r.Files[0].Name, r.Files[1].Name})
	}
}

// Many equal names are numbered in linear time.
func TestOutputNamesLinear(t *testing.T) {
	r := &Result{}
	for i := 0; i < 20000; i++ {
		r.Files = append(r.Files, File{Name: "a.pdf"})
	}
	start := time.Now()
	r.name()
	if time.Since(start) > 5*time.Second {
		t.Fatal("quadratic")
	}
	if r.Files[19999].Name != "a-20000.pdf" {
		t.Fatal(r.Files[19999].Name)
	}
}

// The refusals themselves, without the run around them that could answer for
// them.
func TestRefusalsAlone(t *testing.T) {
	for in, want := range map[string]string{"ab": "ab", "abc": "abc", "€€": "€", "abcd": "abc"} {
		n := 3
		if got := cut(in, n); got != want {
			t.Errorf("cut(%q, %d) = %q, want %q", in, n, got, want)
		}
	}
	if err := nonEmpty([]item{{"a.pdf", ops.New()}}); err == nil || !strings.Contains(err.Error(), "no pages") {
		t.Fatalf("%v", err)
	}
	x := &exec{node: N("n", "X"), st: &state{maxPages: 5}}
	if _, err := x.count("zz", 3); err == nil || !strings.Contains(err.Error(), "not a page number") {
		t.Fatalf("%v", err)
	}
	if n, err := x.count("1-3,2", 3); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	// Each part fits alone; the sum does not.
	if _, err := x.count("1-3,1-3", 3); !errors.Is(err, ErrBudget) {
		t.Fatalf("%v", err)
	}
	d := ops.New()
	for i := 0; i < 3; i++ {
		d.Blank(10, 10)
	}
	e := &exec{node: N("n", "X", "pages", "1-3"), in: map[string][]item{"pdf": {{"a.pdf", d}}}, st: &state{maxPages: 2}}
	if out, err := runExtract(e); !errors.Is(err, ErrBudget) || out != nil {
		t.Fatalf("%v %v", out, err)
	}
	e.node = N("n", "X", "pages", "zz")
	if out, err := runExtract(e); err == nil || !strings.Contains(err.Error(), "not a page number") || out != nil {
		t.Fatalf("%v %v", out, err)
	}
}

func TestStateDoesNotWrap(t *testing.T) {
	s := &state{maxPages: 1<<31 - 1, pages: 1<<31 - 2}
	if s.charge(5) == nil {
		t.Fatal("charge wrapped")
	}
	s = &state{maxIn: 10, in: 9}
	if s.input(-1) == nil || s.input(2) == nil || s.output(1) == nil {
		t.Fatal("not refused")
	}
	if s.check(-1, 1) == nil || s.check(1, 0) == nil {
		t.Fatal("check")
	}
	if r := (&state{maxPages: 1, pending: 5}).room(); r != 0 {
		t.Fatal(r)
	}
}
