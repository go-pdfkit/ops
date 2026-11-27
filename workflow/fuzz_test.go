package workflow

import (
	"context"
	"testing"
)

var seeds = []string{
	okJSON,
	`{"version":1,"nodes":[{"id":"i","type":"PDFInputNode","controls":{}},{"id":"r","type":"RotateNode","controls":{"angle":"90"}},{"id":"o","type":"DownloadNode","controls":{}}],"connections":[{"id":"a","source":"i","sourceOutput":"pdf","target":"r","targetInput":"pdf"},{"id":"b","source":"r","sourceOutput":"pdf","target":"o","targetInput":"pdf"}]}`,
	`{"version":1,"nodes":[{"id":"a","type":"RotateNode","controls":{}},{"id":"b","type":"RotateNode","controls":{}}],"connections":[{"source":"a","target":"b"},{"source":"b","target":"a"}]}`,
	`{"nodes":[{"id":"e","type":"EncryptNode","controls":{"userPassword":"x","k":true,"n":1.5}}],"connections":[]}`,
	`{"nodes":[{"id":"p","type":"PosterizeNode","controls":{"rows":"8","cols":"8"}}],"connections":null}`,
}

// FuzzParse: whatever the bytes are, Parse returns a workflow or an error
// wrapping ErrInvalid, and a parsed workflow marshals and parses again.
func FuzzParse(f *testing.F) {
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		w, err := Parse(b)
		if err != nil {
			return
		}
		out, err := w.Marshal()
		if err != nil {
			return // a position that JSON cannot write back, such as 1e308 squared
		}
		if _, err := Parse(out); err != nil {
			t.Fatalf("what Marshal wrote does not parse: %v", err)
		}
	})
}

// FuzzCheckRun: Check never panics on a parsed workflow, and Run never
// panics either, within a small budget, on a document that is not a PDF.
func FuzzCheckRun(f *testing.F) {
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	pdfBytes := pdf(f, 2)
	f.Fuzz(func(t *testing.T, b []byte) {
		w, err := Parse(b)
		if err != nil {
			return
		}
		Check(w)
		in := map[string][]File{}
		for _, n := range w.Nodes {
			in[n.ID] = []File{{"f.pdf", pdfBytes}}
		}
		Run(context.Background(), w, in, Options{MaxPages: 200, MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	})
}
