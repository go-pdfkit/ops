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
	// A one-page file through "even" selects nothing, and what follows must
	// refuse, not panic.
	`{"version":1,"nodes":[{"id":"i","type":"PDFInputNode"},{"id":"x","type":"ExtractPagesNode","controls":{"pages":"even"}},{"id":"m","type":"AlternateMergeNode"},{"id":"o","type":"DownloadNode"}],"connections":[{"id":"a","source":"i","sourceOutput":"pdf","target":"x","targetInput":"pdf"},{"id":"b","source":"x","sourceOutput":"pdf","target":"m","targetInput":"pdf"},{"id":"c","source":"m","sourceOutput":"pdf","target":"o","targetInput":"pdf"}]}`,
	// Several items, and a spec that repeats pages.
	`{"version":1,"nodes":[{"id":"i","type":"PDFInputNode"},{"id":"s","type":"SplitNode","controls":{"pages":"*,*,*,*,*,*"}},{"id":"m","type":"AlternateMergeNode"},{"id":"o","type":"DownloadNode"}],"connections":[{"id":"a","source":"i","sourceOutput":"pdf","target":"s","targetInput":"pdf"},{"id":"b","source":"s","sourceOutput":"pdf","target":"m","targetInput":"pdf"},{"id":"c","source":"m","sourceOutput":"pdf","target":"o","targetInput":"pdf"}]}`,
	`{"nodes":[{"id":"a\nb","type":"RotateNode"}],"NODES":[],"connections":[]}`,
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
	// Several files of one, two and three pages: one page is where "even"
	// selects nothing, and several items are where a node that expects one
	// breaks.
	files := []File{{"1.pdf", pdf(f, 1)}, {"2.pdf", pdf(f, 2)}, {"3.pdf", pdf(f, 3)}}
	f.Fuzz(func(t *testing.T, b []byte) {
		w, err := Parse(b)
		if err != nil {
			return
		}
		Check(w)
		in := map[string][]File{}
		for _, n := range w.Nodes {
			in[n.ID] = files
		}
		Run(context.Background(), w, in, Options{MaxPages: 200, MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	})
}
