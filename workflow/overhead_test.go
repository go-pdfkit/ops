package workflow

import (
	"context"
	"testing"

	"github.com/go-pdfkit/ops"
)

// The same three verbs, by hand and through the graph.
func byHand(b []byte) ([]byte, error) {
	d, err := ops.Open(b)
	if err != nil {
		return nil, err
	}
	if err := d.Rotate("all", 90); err != nil {
		return nil, err
	}
	if err := d.Delete("1"); err != nil {
		return nil, err
	}
	d.Reverse()
	return d.Bytes()
}

func overheadFlow() *Workflow {
	return WF([]Node{N("in", "PDFInputNode"), N("a", "RotateNode", "angle", "90"), N("b", "DeletePagesNode", "pages", "1"), N("c", "ReversePagesNode"), N("out", "DownloadNode")},
		C("in", "a"), C("a", "b"), C("b", "c"), C("c", "out"))
}

// TestOverheadAllocations reports the allocations of the graph against the
// same verbs run one by one. Allocation counts are exact under load; the
// times (the benchmarks below) are not, and are taken on a quiet machine.
func TestOverheadAllocations(t *testing.T) {
	data := pdf(t, 20)
	w := overheadFlow()
	in := map[string][]File{"in": {{"a.pdf", data}}}
	hand := testing.AllocsPerRun(20, func() {
		if _, err := byHand(data); err != nil {
			t.Fatal(err)
		}
	})
	flow := testing.AllocsPerRun(20, func() {
		if _, err := Run(context.Background(), w, in, Options{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocations per run: by hand %.0f, workflow %.0f, overhead %.0f (%.2f%%)", hand, flow, flow-hand, 100*(flow-hand)/hand)
}

func BenchmarkByHand(b *testing.B) {
	data := pdf(b, 20)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := byHand(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWorkflow(b *testing.B) {
	data := pdf(b, 20)
	w := overheadFlow()
	in := map[string][]File{"in": {{"a.pdf", data}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Run(context.Background(), w, in, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
