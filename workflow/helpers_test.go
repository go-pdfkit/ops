package workflow

import (
	"context"
	"testing"

	"github.com/go-pdfkit/ops"
)

// pdf builds a document of n blank pages.
func pdf(t testing.TB, n int) []byte {
	t.Helper()
	d := ops.New()
	for i := 0; i < n; i++ {
		d.Blank(100, 200)
	}
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// pageCount reads the pages of a produced file.
func pageCount(t testing.TB, b []byte, password string) int {
	t.Helper()
	d, err := ops.OpenWithPassword(b, password)
	if err != nil {
		t.Fatal(err)
	}
	return d.PageCount()
}

// N builds a node; kv are control name and value pairs.
func N(id, typ string, kv ...any) Node {
	c := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		c[kv[i].(string)] = kv[i+1]
	}
	return Node{ID: id, Type: typ, Controls: c}
}

// C connects the pdf output of one node to the pdf input of another.
func C(src, dst string) Connection {
	return Connection{ID: src + ">" + dst, Source: src, SourceOutput: "pdf", Target: dst, TargetInput: "pdf"}
}

// WF builds a workflow.
func WF(ns []Node, cs ...Connection) *Workflow {
	return &Workflow{Version: Version, Nodes: ns, Connections: cs}
}

// line is input -> mid -> download.
func line(mid Node) *Workflow {
	mid.ID = "mid"
	return WF([]Node{N("in", "PDFInputNode"), mid, N("out", "DownloadNode")}, C("in", "mid"), C("mid", "out"))
}

func in(files ...File) map[string][]File { return map[string][]File{"in": files} }

func one(t testing.TB, pages int) map[string][]File {
	return in(File{"a.pdf", pdf(t, pages)})
}

func run(t testing.TB, w *Workflow, inputs map[string][]File, opt Options) (*Result, error) {
	t.Helper()
	return Run(context.Background(), w, inputs, opt)
}
