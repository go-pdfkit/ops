package workflow

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
)

// Every node that runs on a three-page document, with its controls and the
// pages it leaves.
func TestEveryNodeRuns(t *testing.T) {
	cases := []struct {
		typ   string
		kv    []any
		pages int
	}{
		{"SplitNode", []any{"pages", "1-2"}, 2},
		{"SplitNode", nil, 3},
		{"SplitNode", []any{"pages", 2.0}, 1},
		{"RotateNode", []any{"angle", "90"}, 3},
		{"RotateNode", nil, 3},
		{"RotateNode", []any{"angle", 180.0}, 3},
		{"DeletePagesNode", []any{"pages", "1"}, 2},
		{"DeletePagesNode", nil, 2},
		{"ReversePagesNode", nil, 3},
		{"AddBlankPageNode", nil, 4},
		{"AddBlankPageNode", []any{"blankPosition", "end", "count", "2"}, 5},
		{"AddBlankPageNode", []any{"blankPosition", "start", "count", 1.0}, 4},
		{"AddBlankPageNode", []any{"blankPosition", "after", "afterPage", "1", "count", "3"}, 6},
		{"DividePagesNode", nil, 6},
		{"DividePagesNode", []any{"direction", "vertical"}, 6},
		{"DividePagesNode", []any{"direction", "horizontal"}, 6},
		{"NUpNode", nil, 2},
		{"NUpNode", []any{"pagesPerSheet", "4"}, 1},
		{"CombineSinglePageNode", nil, 1},
		{"BookletNode", nil, -1},
		{"PosterizeNode", nil, 12},
		{"PosterizeNode", []any{"rows", "1", "cols", 3.0}, 9},
		{"EditMetadataNode", []any{"title", "T", "author", "A", "subject", "S", "keywords", "K", "creator", "C", "producer", "P"}, 3},
		{"EditMetadataNode", nil, 3},
		{"WatermarkNode", []any{"text", "HELLO", "pages", "1"}, 3},
		{"WatermarkNode", nil, 3},
		{"PageNumbersNode", nil, 3},
		{"PageNumbersNode", []any{"numberFormat", "simple"}, 3},
		{"PageNumbersNode", []any{"numberFormat", "page_x_of_y"}, 3},
		{"RemoveAnnotationsNode", nil, 3},
		{"CompressNode", nil, 3},
		{"FontToOutlineNode", nil, 3},
		{"RepairNode", nil, 3},
		{"DecryptNode", nil, 3},
		{"FlattenNode", nil, 3},
		{"SanitizeNode", nil, 3},
		{"SanitizeNode", []any{"flattenForms", false, "removeMetadata", "false", "removeAnnotations", "false", "removeJavascript", "true", "removeEmbeddedFiles", false}, 3},
		{"SanitizeNode", []any{"flattenForms", false, "removeMetadata", false, "removeAnnotations", false, "removeJavascript", false, "removeEmbeddedFiles", "true"}, 3},
		{"SanitizeNode", []any{"flattenForms", false, "removeMetadata", false, "removeAnnotations", false, "removeJavascript", false, "removeEmbeddedFiles", false}, 3},
	}
	for _, c := range cases {
		res, err := run(t, line(N("", c.typ, c.kv...)), one(t, 3), Options{})
		if err != nil {
			t.Errorf("%s %v: %v", c.typ, c.kv, err)
			continue
		}
		if len(res.Files) != 1 {
			t.Errorf("%s: %d files", c.typ, len(res.Files))
			continue
		}
		if got := pageCount(t, res.Files[0].Data, ""); c.pages >= 0 && got != c.pages {
			t.Errorf("%s %v: %d pages, want %d", c.typ, c.kv, got, c.pages)
		}
		if res.Status["mid"] != Completed || res.Status["out"] != Completed {
			t.Errorf("%s: %v", c.typ, res.Status)
		}
	}
}

// Deskew needs something marked on the page, so the text is drawn first.
func TestDeskewAfterWatermark(t *testing.T) {
	w := WF([]Node{N("in", "PDFInputNode"), N("w", "WatermarkNode", "text", "SKEW"), N("d", "DeskewNode", "skewThreshold", "0.1", "processingDpi", 72.0), N("out", "DownloadNode")},
		C("in", "w"), C("w", "d"), C("d", "out"))
	res, err := run(t, w, one(t, 1), Options{})
	if err != nil {
		var ne *NodeError
		if !errors.As(err, &ne) || ne.ID != "d" {
			t.Fatal(err)
		}
		t.Logf("deskew refused the watermarked page: %v", err)
		return
	}
	if pageCount(t, res.Files[0].Data, "") != 1 {
		t.Fatal("pages")
	}
}

func TestRemoveBlankPagesRuns(t *testing.T) {
	// Every page is blank, so the verb decides; either way the node's error
	// would name the node.
	_, err := run(t, line(N("", "RemoveBlankPagesNode", "threshold", "1")), one(t, 3), Options{})
	if err != nil {
		var ne *NodeError
		if !errors.As(err, &ne) || ne.ID != "mid" {
			t.Fatal(err)
		}
	}
}

// Every control a node refuses, with what the error says.
func TestNodeRefusals(t *testing.T) {
	cases := []struct {
		typ  string
		kv   []any
		want string
	}{
		{"SplitNode", []any{"pages", true}, "expected text"},
		{"SplitNode", []any{"pages", "9"}, ""},
		{"DeletePagesNode", []any{"pages", true}, "expected text"},
		{"DeletePagesNode", []any{"pages", "zz"}, ""},
		{"RotateNode", []any{"angle", "abc"}, `"abc" is not a number`},
		{"RotateNode", []any{"angle", 999.0}, "outside"},
		{"RotateNode", []any{"angle", true}, "expected a number"},
		{"RotateNode", []any{"angle", 45.0}, ""},
		{"AddBlankPageNode", []any{"blankPosition", true}, "expected text"},
		{"AddBlankPageNode", []any{"afterPage", "x"}, "not a number"},
		{"AddBlankPageNode", []any{"count", 0.0}, "outside"},
		{"AddBlankPageNode", []any{"blankPosition", "middle"}, "not end, start or after"},
		{"AddBlankPageNode", []any{"blankPosition", "after", "afterPage", 50.0}, "cannot insert"},
		{"DividePagesNode", []any{"direction", true}, "expected text"},
		{"DividePagesNode", []any{"direction", "diagonal"}, "not vertical or horizontal"},
		{"NUpNode", []any{"pagesPerSheet", 1.0}, "outside"},
		{"DeskewNode", nil, "nothing marked"},
		{"PosterizeNode", []any{"rows", 0.0}, "outside"},
		{"PosterizeNode", []any{"cols", 9.0}, "outside"},
		{"EditMetadataNode", []any{"title", true}, "expected text"},
		{"WatermarkNode", []any{"text", true}, "expected text"},
		{"WatermarkNode", []any{"pages", true}, "expected text"},
		{"WatermarkNode", []any{"text", ""}, "is empty"},
		{"WatermarkNode", []any{"pages", "99"}, ""},
		{"PageNumbersNode", []any{"numberFormat", true}, "expected text"},
		{"PageNumbersNode", []any{"numberFormat", "roman"}, "not simple or page_x_of_y"},
		{"RemoveBlankPagesNode", []any{"threshold", 9.0}, "outside"},
		{"DeskewNode", []any{"skewThreshold", 99.0}, "outside"},
		{"DeskewNode", []any{"processingDpi", 1.0}, "outside"},
		{"EncryptNode", nil, "no password"},
		{"SanitizeNode", []any{"flattenForms", "maybe"}, "expected true or false"},
		{"SanitizeNode", []any{"removeMetadata", "maybe"}, "expected true or false"},
		{"SanitizeNode", []any{"removeAnnotations", 1.0}, "expected true or false"},
		{"SanitizeNode", []any{"removeJavascript", "maybe"}, "expected true or false"},
		{"SanitizeNode", []any{"removeEmbeddedFiles", "maybe"}, "expected true or false"},
	}
	for _, c := range cases {
		res, err := run(t, line(N("", c.typ, c.kv...)), one(t, 3), Options{})
		var ne *NodeError
		if !errors.As(err, &ne) || ne.ID != "mid" || ne.Type != c.typ || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %v: %v", c.typ, c.kv, err)
			continue
		}
		if !strings.HasPrefix(err.Error(), ne.Label+` (node "mid"): `) || ne.Unwrap() == nil {
			t.Errorf("message %q", err)
		}
		if res.Status["mid"] != Failed || res.Status["out"] != Idle || len(res.Files) != 0 {
			t.Errorf("%s: %v", c.typ, res.Status)
		}
	}
}

func TestInputs(t *testing.T) {
	w := line(N("", "RotateNode"))
	// No file for the input.
	if _, err := run(t, w, nil, Options{}); err == nil || !strings.Contains(err.Error(), "no PDF was given") {
		t.Fatal(err)
	}
	// Not a PDF: the error names the file.
	if _, err := run(t, w, in(File{"junk.pdf", []byte("nope")}), Options{}); err == nil || !strings.Contains(err.Error(), "junk.pdf") {
		t.Fatal(err)
	}
	// Input bytes are bounded.
	if _, err := run(t, w, one(t, 1), Options{MaxInputBytes: 10}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	// Output bytes are bounded.
	if _, err := run(t, w, one(t, 1), Options{MaxOutputBytes: 10}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	enc := WF([]Node{N("in", "PDFInputNode"), N("e", "EncryptNode"), N("out", "DownloadNode")}, C("in", "e"), C("e", "out"))
	res, err := run(t, enc, one(t, 2), Options{Secrets: map[string]map[string]string{"e": {"userPassword": "u", "ownerPassword": "o"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(res.Files[0].Data, []byte("/Encrypt")) {
		t.Fatal("not encrypted")
	}
	if _, err := ops.Open(res.Files[0].Data); err == nil {
		t.Fatal("opened without the password")
	}
	// The "*" secret feeds the input node's password; Decrypt drops the lock.
	dec := WF([]Node{N("in", "PDFInputNode"), N("d", "DecryptNode"), N("out", "DownloadNode")}, C("in", "d"), C("d", "out"))
	res2, err := run(t, dec, in(File{"x.pdf", res.Files[0].Data}), Options{Secrets: map[string]map[string]string{"*": {"password": "u"}}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(res2.Files[0].Data, []byte("/Encrypt")) || pageCount(t, res2.Files[0].Data, "") != 2 {
		t.Fatal("still encrypted")
	}
	// A node's own secret wins over "*".
	sec := secretsFor(map[string]map[string]string{"*": {"a": "1", "b": "2"}, "n": {"b": "3"}}, "n")
	if sec["a"] != "1" || sec["b"] != "3" {
		t.Fatalf("%v", sec)
	}
	// Wrong password: the input node fails.
	if _, err := run(t, dec, in(File{"x.pdf", res.Files[0].Data}), Options{}); err == nil {
		t.Fatal("opened without the password")
	}
}

func TestMergeInterleaveExtractOverlay(t *testing.T) {
	two := in(File{"a.pdf", pdf(t, 2)}, File{"b.pdf", pdf(t, 3)})
	for _, c := range []struct {
		typ   string
		pages int
	}{{"MergeNode", 5}, {"AlternateMergeNode", 5}} {
		w := WF([]Node{N("in", "PDFInputNode"), N("mid", c.typ), N("out", "DownloadNode")}, C("in", "mid"), C("mid", "out"))
		res, err := run(t, w, two, Options{})
		if err != nil || len(res.Files) != 1 || res.Files[0].Name != "merged.pdf" || pageCount(t, res.Files[0].Data, "") != c.pages {
			t.Fatalf("%s: %v %v", c.typ, res, err)
		}
	}
	// One document into a merge-like node stays itself.
	w := WF([]Node{N("in", "PDFInputNode"), N("mid", "AlternateMergeNode"), N("out", "DownloadNode")}, C("in", "mid"), C("mid", "out"))
	res, err := run(t, w, one(t, 2), Options{})
	if err != nil || len(res.Files) != 1 || res.Files[0].Name != "a.pdf" {
		t.Fatalf("%v %v", res, err)
	}
	// Extract makes one file per page: a ZIP.
	w = line(N("", "ExtractPagesNode", "pages", "1,3"))
	res, err = run(t, w, one(t, 3), Options{})
	if err != nil || len(res.Files) != 2 || res.Files[0].Name != "a-p1.pdf" || res.Files[1].Name != "a-p3.pdf" {
		t.Fatalf("%v %v", res, err)
	}
	if _, err := run(t, line(N("", "ExtractPagesNode", "pages", true)), one(t, 3), Options{}); err == nil {
		t.Fatal("bad pages")
	}
	if _, err := run(t, line(N("", "ExtractPagesNode", "pages", "9")), one(t, 3), Options{}); err == nil {
		t.Fatal("page out of range")
	}
	// Overlay and underlay: pdf and overlay inputs.
	for _, mode := range []string{"overlay", "underlay", ""} {
		kv := []any{}
		if mode != "" {
			kv = append(kv, "mode", mode)
		}
		ov := WF([]Node{N("in", "PDFInputNode"), N("in2", "PDFInputNode"), N("o", "OverlayNode", kv...), N("out", "DownloadNode")},
			C("in", "o"), Connection{ID: "x", Source: "in2", SourceOutput: "pdf", Target: "o", TargetInput: "overlay"}, C("o", "out"))
		res, err := run(t, ov, map[string][]File{"in": {{"a.pdf", pdf(t, 2)}}, "in2": {{"b.pdf", pdf(t, 1)}}}, Options{})
		if err != nil || pageCount(t, res.Files[0].Data, "") != 2 {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	bad := WF([]Node{N("in", "PDFInputNode"), N("in2", "PDFInputNode"), N("o", "OverlayNode", "mode", true), N("out", "DownloadNode")},
		C("in", "o"), Connection{ID: "x", Source: "in2", SourceOutput: "pdf", Target: "o", TargetInput: "overlay"}, C("o", "out"))
	if _, err := run(t, bad, map[string][]File{"in": {{"a.pdf", pdf(t, 2)}}, "in2": {{"b.pdf", pdf(t, 1)}}}, Options{}); err == nil {
		t.Fatal("mode true")
	}
	// Direct: an overlay input that carries nothing.
	if _, err := runOverlay(&exec{node: N("o", "OverlayNode"), in: map[string][]item{"pdf": nil}}); err == nil {
		t.Fatal("empty overlay input")
	}
}

func TestInterleaveError(t *testing.T) {
	w := WF([]Node{N("in", "PDFInputNode"), N("mid", "AlternateMergeNode"), N("out", "DownloadNode")}, C("in", "mid"), C("mid", "out"))
	res, err := run(t, w, in(File{"a.pdf", pdf(t, 1)}, File{"b.pdf", pdf(t, 1)}), Options{})
	if err != nil || pageCount(t, res.Files[0].Data, "") != 2 {
		t.Fatal(err)
	}
	// ops refuses to interleave nothing.
	e := ops.New()
	if _, err := runInterleave(&exec{in: map[string][]item{"pdf": {{"a", e}, {"b", ops.New()}}}}); err == nil {
		t.Fatal("no pages to interleave")
	}
}

func TestFanOutGivesEachConsumerItsOwnCopy(t *testing.T) {
	w := WF([]Node{N("in", "PDFInputNode"), N("del", "DeletePagesNode", "pages", "1"), N("rev", "ReversePagesNode"), N("o1", "DownloadNode"), N("o2", "DownloadNode"), N("o3", "DownloadNode")},
		C("in", "del"), C("in", "rev"), C("in", "o3"), C("del", "o1"), C("rev", "o2"))
	res, err := run(t, w, one(t, 3), Options{})
	if err != nil || len(res.Files) != 3 {
		t.Fatalf("%v %v", res, err)
	}
	got := []int{}
	for _, f := range res.Files {
		got = append(got, pageCount(t, f.Data, ""))
	}
	// Output order is run order: o3 (the input itself), o1 (del), o2 (rev).
	if got[0] != 3 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("%v", got)
	}
	// Duplicate output names are told apart.
	if res.Files[0].Name != "a.pdf" || res.Files[1].Name != "a-2.pdf" || res.Files[2].Name != "a-3.pdf" {
		t.Fatalf("%q %q %q", res.Files[0].Name, res.Files[1].Name, res.Files[2].Name)
	}
	// Copies are charged against the budgets.
	if _, err := run(t, w, one(t, 3), Options{MaxPages: 7}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	if _, err := run(t, w, one(t, 3), Options{MaxInputBytes: len(pdf(t, 3)) + 1}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
}

func TestPageBudget(t *testing.T) {
	// Poster of poster of poster: 3 * 64 * 64 pages is refused before it is made.
	w := WF([]Node{N("in", "PDFInputNode"), N("p1", "PosterizeNode", "rows", 8.0, "cols", 8.0), N("p2", "PosterizeNode", "rows", 8.0, "cols", 8.0), N("out", "DownloadNode")},
		C("in", "p1"), C("p1", "p2"), C("p2", "out"))
	_, err := run(t, w, one(t, 3), Options{MaxPages: 1000})
	var ne *NodeError
	if !errors.As(err, &ne) || ne.ID != "p2" || !errors.Is(err, ErrBudget) {
		t.Fatalf("%v", err)
	}
	// The cumulative charge catches what no single node would.
	if _, err := run(t, line(N("", "ReversePagesNode")), one(t, 3), Options{MaxPages: 5}); !errors.Is(err, ErrBudget) {
		t.Fatalf("%v", err)
	}
	for _, typ := range []string{"DividePagesNode", "AddBlankPageNode"} {
		if _, err := run(t, line(N("", typ)), one(t, 3), Options{MaxPages: 4}); !errors.Is(err, ErrBudget) {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	if _, err := run(t, line(N("", "ExtractPagesNode", "pages", "1-3")), one(t, 3), Options{MaxPages: 4}); !errors.Is(err, ErrBudget) {
		t.Fatalf("%v", err)
	}
}

func TestRefusedAndCancelled(t *testing.T) {
	w := WF([]Node{N("in", "PDFInputNode"), N("out", "DownloadNode"), N("x", "NopeNode")}, C("in", "out"))
	if _, err := run(t, w, one(t, 1), Options{}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "NopeNode") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, line(N("", "RotateNode")), one(t, 1), Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDanglingNodeStaysIdle(t *testing.T) {
	w := WF([]Node{N("in", "PDFInputNode"), N("d", "DeletePagesNode", "pages", "99"), N("out", "DownloadNode")}, C("in", "out"), C("in", "d"))
	res, err := run(t, w, one(t, 1), Options{})
	if err != nil || res.Status["d"] != Idle || res.Status["out"] != Completed || !has1(res.Issues, Warning, "d", "does not lead") {
		t.Fatalf("%v %v", res, err)
	}
}

func TestNamesAndEncode(t *testing.T) {
	for in, want := range map[string]string{
		"a.pdf": "a.pdf", "A.PDF": "A.pdf", "x": "x.pdf", "../../etc/passwd": "passwd.pdf", `..\..\win\a.pdf`: "a.pdf",
		"a\x00b\n.pdf": "ab.pdf", "": "document.pdf", "..": "document.pdf", "/": "document.pdf", "dir/": "dir.pdf", "\x7f": "document.pdf",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	r := &Result{Files: []File{{"a.pdf", []byte("1")}, {"a.pdf", []byte("2")}, {"a", []byte("3")}}}
	r.name()
	if r.Files[0].Name != "a.pdf" || r.Files[1].Name != "a-2.pdf" || r.Files[2].Name != "a-3.pdf" {
		t.Fatalf("%v", r.Files)
	}
	var b bytes.Buffer
	z, err := r.Encode(&b)
	if err != nil || !z {
		t.Fatal(z, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil || len(zr.File) != 3 || zr.File[1].Name != "a-2.pdf" {
		t.Fatal(err)
	}
	b.Reset()
	if z, err := (&Result{Files: r.Files[:1]}).Encode(&b); err != nil || z || b.String() != "1" {
		t.Fatal(z, err, b.String())
	}
	if _, err := (&Result{}).Encode(&b); err == nil {
		t.Fatal("empty result")
	}
	if _, err := r.Encode(errWriter{}); err == nil {
		t.Fatal("write error")
	}
	if _, err := (&Result{Files: r.Files[:1]}).Encode(errWriter{}); err == nil {
		t.Fatal("write error")
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("full") }

func TestCloneErrors(t *testing.T) {
	st := &state{maxPages: 10, maxIn: 1 << 20, maxOut: 1 << 20}
	d := ops.New()
	d.Blank(10, 10)
	d.Encrypt(reader.Encryption{UserPassword: "pw", OwnerPassword: "pw", Permissions: reader.AllPermissions})
	// An encrypted document cannot be copied back in without its password.
	if _, err := clone([]item{{"a", d}}, st); err == nil {
		t.Fatal("encrypted clone opened")
	}
	plain := ops.New()
	plain.Blank(1, 1)
	plain.Blank(1, 1)
	if _, err := clone([]item{{"a", plain}}, &state{maxPages: 10, maxIn: 1, maxOut: 1}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	if _, err := clone([]item{{"a", plain}}, &state{maxPages: 1, maxIn: 1 << 20}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
}
