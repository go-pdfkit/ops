// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// tiltedScan writes a one-page document of filled bars standing in for lines
// of text, already tilted by angleDeg counter-clockwise about the page centre.
//
// It is the same fixture as scanLikePDF in the ops package's own tests. A
// fixture cannot be shared across package boundaries in Go, and the thing it
// has to be is a PAGE WITH HORIZONTAL LINES ON IT — neither fixture(t, n),
// whose "content" is the literal text `page 1` and renders as blank paper, nor
// anything buildable from stamps, which anchor to one of nine positions and
// would pile up in the same place.
//
// ⛔ The tilt is built by rotating each bar's own corners, written out in
// full, and not with the matrix the code under test uses: a fixture drawn with
// that matrix would be tilted the wrong way too if the matrix were wrong, and
// every assertion would still pass.
func tiltedScan(t *testing.T, angleDeg float64) string {
	return tiltedScanAfterBlanks(t, angleDeg, 0)
}

// tiltedScanAfterBlanks puts blanks blank pages in front of the tilted one, so
// the mixed case — a separator sheet in a stack of scans — can be driven.
func tiltedScanAfterBlanks(t *testing.T, angleDeg float64, blanks int) string {
	t.Helper()
	const (
		w, h   = 612.0, 792.0
		left   = 60.0
		right  = 552.0
		bar    = 8.0
		pitch  = 20.0
		top    = 700.0
		bottom = 90.0
	)
	s, c := math.Sincos(angleDeg * math.Pi / 180)
	cx, cy := w/2, h/2
	turn := func(x, y float64) (float64, float64) {
		dx, dy := x-cx, y-cy
		return cx + dx*c - dy*s, cy + dx*s + dy*c
	}
	var content bytes.Buffer
	content.WriteString("0 g\n")
	for y := bottom; y+bar <= top; y += pitch {
		end := right - math.Mod(y*7, 90)
		x0, y0 := turn(left, y)
		x1, y1 := turn(end, y)
		x2, y2 := turn(end, y+bar)
		x3, y3 := turn(left, y+bar)
		fmt.Fprintf(&content, "%.3f %.3f m %.3f %.3f l %.3f %.3f l %.3f %.3f l h f\n",
			x0, y0, x1, y1, x2, y2, x3, y3)
	}

	wr := reader.NewWriter("1.7")
	pagesRef := wr.Reserve()
	kids := reader.Array{}
	for range blanks {
		empty := wr.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})
		kids = append(kids, wr.Add(reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": empty,
		}))
	}
	stream := wr.Add(&reader.Stream{Dict: reader.Dict{}, Raw: content.Bytes()})
	kids = append(kids, wr.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": stream,
	}))
	wr.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(len(kids)),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Real(w), reader.Real(h)}})
	root := wr.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := wr.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ⛔ -list is asserted FIRST and separately, for the reason deblank's is: it is
// the dry run, and the first thing to do with a tool that rewrites every page
// is to ask it what it thinks it has found.
func TestDeskewList(t *testing.T) {
	in := tiltedScan(t, 4)
	code, out, msg := exec("deskew", "-list", in)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(out, "+4.00°") {
		t.Errorf("-list said %q, want it to report +4.00°", out)
	}
	if !strings.Contains(out, "1 page(s), 0 corrected") {
		t.Errorf("-list said %q, want it to correct nothing", out)
	}
	// And it wrote nothing: there was nowhere to write to, and it did not ask.
	if code, _, _ := exec("deskew", "-list", in, filepath.Join(t.TempDir(), "x.pdf")); code != 1 {
		t.Error("-list accepted an output file")
	}
}

func TestDeskewStraightensIt(t *testing.T) {
	in := tiltedScan(t, -5.5)
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, said, msg := exec("deskew", in, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "corrected") || !strings.Contains(said, "-5.50°") {
		t.Errorf("said %q, want it to report -5.50° corrected", said)
	}
	if !strings.Contains(said, "1 page(s), 1 corrected") {
		t.Errorf("said %q", said)
	}
	// The file is there and still has its page.
	if got := len(contentsOf(t, out)); got != 1 {
		t.Errorf("%d pages, want 1", got)
	}
	// And straightening it again finds nothing left to do, which is the
	// property that says the correction went the right way.
	code, said, msg = exec("deskew", "-list", out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "1 page(s), 0 corrected") {
		t.Errorf("deskewing the output again said %q, want nothing left to correct", said)
	}
}

func TestDeskewLeavesALevelPage(t *testing.T) {
	in := tiltedScan(t, 0)
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, said, msg := exec("deskew", in, out)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	// ⛔ "corrected" alone is the wrong thing to look for: the summary line
	// ends in "0 corrected", so an assertion on the word matched the very
	// output it was meant to rule out. The page's own line is what says
	// whether it was turned.
	if strings.Contains(said, "° corrected") || !strings.Contains(said, "1 page(s), 0 corrected") {
		t.Errorf("said %q, want nothing corrected", said)
	}
}

func TestDeskewThresholdAndDPI(t *testing.T) {
	in := tiltedScan(t, 1.5)
	out := filepath.Join(t.TempDir(), "out.pdf")
	// Above the threshold at the default, below it at two degrees.
	if code, said, _ := exec("deskew", in, out); code != 0 || !strings.Contains(said, "1 corrected") {
		t.Errorf("at the default threshold: code %d, said %q", code, said)
	}
	if code, said, _ := exec("deskew", "-threshold", "2", in, out); code != 0 || !strings.Contains(said, "0 corrected") {
		t.Errorf("at a 2° threshold: code %d, said %q", code, said)
	}
	// Every DPI BentoPDF offers finds the same page.
	for _, dpi := range []string{"100", "150", "200", "300"} {
		code, said, msg := exec("deskew", "-list", "-dpi", dpi, in)
		if code != 0 {
			t.Fatalf("%s DPI: code %d: %s", dpi, code, msg)
		}
		if !strings.Contains(said, "+1.50°") {
			t.Errorf("%s DPI said %q, want +1.50°", dpi, said)
		}
	}
}

func TestDeskewReportsAPageItCannotMeasure(t *testing.T) {
	// A page with nothing on it has no skew. It is named in the report and the
	// command still succeeds, because one blank separator sheet must not sink
	// a stack of scans.
	in := tiltedScanAfterBlanks(t, 4, 1)
	code, said, msg := exec("deskew", "-list", in)
	if code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(said, "page 1:") || !strings.Contains(said, "no skew") {
		t.Errorf("said %q, want it to name the blank page and why", said)
	}
	if !strings.Contains(said, "page 2: +4.00°") {
		t.Errorf("said %q, want the other page measured anyway", said)
	}
	if !strings.Contains(said, "2 page(s), 0 corrected") {
		t.Errorf("said %q", said)
	}
}

func TestDeskewRefusesADocumentItCannotMeasureAtAll(t *testing.T) {
	// ⛔ ONE PAGE FAILING IS A REPORT; EVERY PAGE FAILING IS AN ERROR. Found
	// by the refusals test below: asked for a DPI no renderer will accept,
	// this printed a failure for every page, wrote the file out unchanged and
	// exited 0 — telling anyone who checks the exit status, which is how a
	// command is normally driven, that the deskewing had been done.
	in := fixture(t, 2) // "content" that is not drawing operators: blank paper
	code, _, msg := exec("deskew", "-list", in)
	if code != 1 {
		t.Errorf("a document whose every page is blank exited %d, want 1", code)
	}
	if !strings.Contains(msg, "not one of the 2 pages") {
		t.Errorf("said %q, want it to say the whole document failed", msg)
	}
}

func TestDeskewRefusals(t *testing.T) {
	in := tiltedScan(t, 3)
	out := filepath.Join(t.TempDir(), "out.pdf")
	for _, tc := range []struct {
		why  string
		args []string
	}{
		{"no output file", []string{"deskew", in}},
		{"a file that is not there", []string{"deskew", "/no/such/file.pdf", out}},
		{"a file that is not there, listing", []string{"deskew", "-list", "/no/such/file.pdf"}},
		{"a flag that does not exist", []string{"deskew", "-nonsense", in, out}},
		{"a directory that is not there", []string{"deskew", in, "/no/such/directory/out.pdf"}},
		{"a DPI no renderer will accept", []string{"deskew", "-dpi", "100000", in, out}},
		{"a DPI no renderer will accept, listing", []string{"deskew", "-list", "-dpi", "100000", in}},
	} {
		if code, _, _ := exec(tc.args...); code != 1 {
			t.Errorf("%s: exit code %d, want 1", tc.why, code)
		}
	}
}
