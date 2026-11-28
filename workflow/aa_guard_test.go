package workflow

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
)

// A node that would overrun the page budget is refused BEFORE it makes the
// pages: the document that arrived is left as it was.
func TestReserveRefusesBeforeTheWork(t *testing.T) {
	for _, c := range []struct {
		name string
		fn   func(*exec) ([]item, error)
		kv   []any
	}{
		{"poster", runPoster, []any{"rows", 8.0, "cols", 8.0}},
		{"divide", runDivide, nil},
		{"addblank", runAddBlank, nil},
	} {
		d := ops.New()
		for i := 0; i < 3; i++ {
			d.Blank(100, 200)
		}
		x := &exec{node: N("n", "X", c.kv...), in: map[string][]item{"pdf": {{"a.pdf", d}}}, st: &state{maxPages: 3}}
		if _, err := c.fn(x); !errors.Is(err, ErrBudget) {
			t.Fatalf("%s: %v", c.name, err)
		}
		if d.PageCount() != 3 {
			t.Fatalf("%s: the document was changed before the refusal: %d pages", c.name, d.PageCount())
		}
	}
}

func TestControlTypeErrorsAreNamed(t *testing.T) {
	for _, typ := range []string{"ExtractPagesNode", "DeletePagesNode"} {
		_, err := run(t, line(N("", typ, "pages", true)), one(t, 3), Options{})
		if err == nil || !strings.Contains(err.Error(), "expected text") {
			t.Fatalf("%s: %v", typ, err)
		}
	}
}

// Underlay draws the other document below the page, overlay above it: the
// two files differ when both documents carry something.
func TestUnderlayDiffersFromOverlay(t *testing.T) {
	mark := func(text string) []byte {
		d := ops.New()
		d.Blank(100, 200)
		if err := d.Watermark("all", text); err != nil {
			t.Fatal(err)
		}
		b, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var got [2][]byte
	for i, mode := range []string{"overlay", "underlay"} {
		ov := WF([]Node{N("in", "PDFInputNode"), N("in2", "PDFInputNode"), N("o", "OverlayNode", "mode", mode), N("out", "DownloadNode")},
			C("in", "o"), Connection{ID: "x", Source: "in2", SourceOutput: "pdf", Target: "o", TargetInput: "overlay"}, C("o", "out"))
		res, err := run(t, ov, map[string][]File{"in": {{"a.pdf", mark("A")}}, "in2": {{"b.pdf", mark("B")}}}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		got[i] = res.Files[0].Data
	}
	if bytes.Equal(got[0], got[1]) {
		t.Fatal("underlay produced what overlay does")
	}
}

// noPanic turns a crash into a failed assertion: a guard that is removed must
// show up as the test that names what the guard was for.
func noPanic(t *testing.T, what string) {
	t.Helper()
	if r := recover(); r != nil {
		t.Errorf("%s: panicked: %v", what, r)
	}
}

// Each refusal below is asserted by what it says, not by the run failing.
func TestRefusalsSayWhy(t *testing.T) {
	say := func(w *Workflow, in map[string][]File, want string) {
		t.Helper()
		defer noPanic(t, want)
		_, err := run(t, w, in, Options{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q: %v", want, err)
		}
	}
	// The number range: both edges are in, a step beyond and NaN are out.
	for _, a := range []string{"360", "-360", "0"} {
		if _, err := run(t, line(N("", "RotateNode", "angle", a)), one(t, 1), Options{}); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
	}
	for _, a := range []string{"361", "-361", "NaN"} {
		say(line(N("", "RotateNode", "angle", a)), one(t, 1), "outside")
	}
	for _, k := range []string{"rows", "cols"} {
		say(line(N("", "PosterizeNode", k, 9.0)), one(t, 1), k)
		say(line(N("", "PosterizeNode", k, 0.0)), one(t, 1), "outside")
	}
	// An input that is not a PDF names the file.
	say(line(N("", "ReversePagesNode")), in(File{"junk.pdf", []byte("nope")}), "junk.pdf")
}

func TestCloneRefusesWhatItCannotCopy(t *testing.T) {
	d := ops.New()
	d.Blank(10, 10)
	d.Encrypt(reader.Encryption{UserPassword: "pw", OwnerPassword: "pw", Permissions: reader.AllPermissions})
	out, err := clone([]item{{"a", d}}, &state{maxPages: 10, maxIn: 1 << 20, maxOut: 1 << 20})
	if err == nil || out != nil {
		t.Fatalf("%v %v", out, err)
	}
}

func TestParseNeedsBothLists(t *testing.T) {
	for _, s := range []string{`{"version":1}`, `{"nodes":[]}`, `{"connections":[]}`, `{"nodes":null,"connections":[]}`} {
		func() {
			defer noPanic(t, s)
			if _, err := Parse([]byte(s)); err == nil || !strings.Contains(err.Error(), "must both be lists") {
				t.Errorf("%s: %v", s, err)
			}
		}()
	}
}

func TestParseSizeBoundIsExact(t *testing.T) {
	if _, err := Parse(bytes.Repeat([]byte(" "), MaxFileBytes+1)); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("%v", err)
	}
	if _, err := Parse(bytes.Repeat([]byte(" "), MaxFileBytes)); err == nil || strings.Contains(err.Error(), "larger than") {
		t.Fatalf("%v", err)
	}
}
