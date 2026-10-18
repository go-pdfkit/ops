// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// redSquare is a picture whose colour says it is the placed one and nothing
// else on the page: 20 by 10 pixels, so a test can tell width from height and
// an aspect ratio of two from one of a half.
func redSquare(t *testing.T) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for y := range 10 {
		for x := range 20 {
			m.Set(x, y, color.NRGBA{R: 0xff, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// transparentPicture is half opaque red and half nothing at all, which is what
// a drawn signature is: ink on a transparent background.
func transparentPicture(t *testing.T) []byte {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for y := range 10 {
		for x := range 20 {
			a := byte(0)
			if x < 10 {
				a = 0xff
			}
			m.Set(x, y, color.NRGBA{R: 0xff, A: a})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// placedRects reads back where the pictures went, from the content stream this
// package wrote: each placement is a `w 0 0 h x y cm /name Do`.
func placedRects(t *testing.T, pdf []byte) [][4]float64 {
	t.Helper()
	content := pageContentOf(t, pdf, 1)
	var out [][4]float64
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, "cm /PdfopsIm") {
			continue
		}
		f := strings.Fields(line)
		// … w 0 0 h x y cm /name Do
		for i, tok := range f {
			if tok == "cm" && i >= 6 {
				var r [4]float64
				if _, err := fmt.Sscan(f[i-6], &r[2]); err != nil {
					t.Fatalf("width %q: %v", f[i-6], err)
				}
				if _, err := fmt.Sscan(f[i-3], &r[3]); err != nil {
					t.Fatalf("height %q: %v", f[i-3], err)
				}
				if _, err := fmt.Sscan(f[i-2], &r[0]); err != nil {
					t.Fatalf("x %q: %v", f[i-2], err)
				}
				if _, err := fmt.Sscan(f[i-1], &r[1]); err != nil {
					t.Fatalf("y %q: %v", f[i-1], err)
				}
				out = append(out, r)
			}
		}
	}
	return out
}

// onePage is a blank A5-ish page from this package, as a file.
func onePage(t *testing.T, w, h float64) []byte {
	t.Helper()
	d := New()
	d.Blank(w, h)
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ⛔ THE DEFECT THIS WHOLE CHANGE IS ABOUT: a BORROWED page — one read from a
// file, which is every page a caller of this package has — did not honour a
// placed picture at all. The machinery existed, the XFA renderer used it, and
// the conditions that decide whether a source page is wrapped counted marks
// and not pictures: the picture was simply absent, with nothing failing.
func TestAPictureReachesABorrowedPage(t *testing.T) {
	d, err := Open(onePage(t, 200, 100))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Place("all", Placement{Data: redSquare(t), Width: 40}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	rects := placedRects(t, out)
	if len(rects) != 1 {
		t.Fatalf("%d pictures drawn, want one — the page was borrowed, which is the case that was not wired", len(rects))
	}
	if !bytes.Contains(out, []byte("/PdfopsIm0")) {
		t.Error("the picture's XObject is not referenced anywhere in the file")
	}
}

// ⛔ Giving ONE of width and height keeps the proportions: a signature scaled
// into a box is a signature nobody recognises.
func TestSizing(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Placement
		w, h float64
	}{
		{"width alone keeps the ratio", Placement{Width: 40}, 40, 20},
		{"height alone keeps the ratio", Placement{Height: 40}, 80, 40},
		{"both may distort, and that is the caller's business", Placement{Width: 40, Height: 40}, 40, 40},
		{"neither means its own pixels at 72 dpi", Placement{}, 20, 10},
		{"dpi sizes it from its pixels", Placement{DPI: 144}, 10, 5},
		{"width wins over dpi, because it is the more specific answer", Placement{Width: 40, DPI: 300}, 40, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Open(onePage(t, 400, 400))
			if err != nil {
				t.Fatal(err)
			}
			p := tc.p
			p.Data = redSquare(t)
			if err := d.Place("1", p); err != nil {
				t.Fatal(err)
			}
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			rects := placedRects(t, out)
			if len(rects) != 1 {
				t.Fatalf("%d pictures, want one", len(rects))
			}
			if got := rects[0][2]; !close(got, tc.w) {
				t.Errorf("width = %g, want %g", got, tc.w)
			}
			if got := rects[0][3]; !close(got, tc.h) {
				t.Errorf("height = %g, want %g", got, tc.h)
			}
		})
	}
}

// The nine positions, measured against the page's own box — the same box a
// stamp is positioned in, so "bottom right" means one thing on a page.
func TestPositions(t *testing.T) {
	const pw, ph, margin = 200.0, 100.0, 10.0
	const w, h = 40.0, 20.0
	for _, tc := range []struct {
		pos  Position
		x, y float64
	}{
		{Center, (pw - w) / 2, (ph - h) / 2},
		{TopLeft, margin, ph - margin - h},
		{TopCenter, (pw - w) / 2, ph - margin - h},
		{TopRight, pw - margin - w, ph - margin - h},
		{BottomLeft, margin, margin},
		{BottomCenter, (pw - w) / 2, margin},
		{BottomRight, pw - margin - w, margin},
		{MiddleLeft, margin, (ph - h) / 2},
		{MiddleRight, pw - margin - w, (ph - h) / 2},
	} {
		d, err := Open(onePage(t, pw, ph))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Place("1", Placement{
			Data: redSquare(t), Width: w, Position: tc.pos, Margin: margin,
		}); err != nil {
			t.Fatal(err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		r := placedRects(t, out)[0]
		if !close(r[0], tc.x) || !close(r[1], tc.y) {
			t.Errorf("position %d went to (%g,%g), want (%g,%g)", tc.pos, r[0], r[1], tc.x, tc.y)
		}
	}
}

// ⛔ An explicit corner is an INSTRUCTION, not a hint: it is not clamped into
// the page, because a signature may deliberately bleed off the edge of a
// scanned form and a viewer clips what falls outside perfectly well.
func TestAnAnchoredCornerIsTakenAsGiven(t *testing.T) {
	d, err := Open(onePage(t, 200, 100))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Place("1", Placement{
		Data: redSquare(t), Width: 40, Anchored: true, At: [2]float64{180, 90},
		Position: TopLeft, // ignored, because a corner was given
	}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	r := placedRects(t, out)[0]
	if !close(r[0], 180) || !close(r[1], 90) {
		t.Errorf("the picture went to (%g,%g), want the corner it was given, (180,90)", r[0], r[1])
	}
	// And the nine positions DO clamp, which is the other half of the rule.
	d, _ = Open(onePage(t, 20, 20))
	if err := d.Place("1", Placement{Data: redSquare(t), Width: 40, Position: TopRight, Margin: 10}); err != nil {
		t.Fatal(err)
	}
	out, _ = d.Bytes()
	r = placedRects(t, out)[0]
	if r[0] < 0 || r[1] < 0 {
		t.Errorf("a picture wider than its page went to (%g,%g), want it clamped to the page", r[0], r[1])
	}
}

func TestPageRange(t *testing.T) {
	d := New()
	for range 4 {
		d.Blank(100, 100)
	}
	src, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Place("2,4", Placement{Data: redSquare(t), Width: 20}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 4; page++ {
		content := pageContentOf(t, out, page)
		has := strings.Contains(content, "/PdfopsIm0")
		want := page == 2 || page == 4
		if has != want {
			t.Errorf("page %d has a picture = %v, want %v", page, has, want)
		}
	}
}

// ⛔ Opacity means what a Stamp's Opacity means — the alpha, with zero the
// unset default — and shares its ExtGState NAME, so a pale picture and pale
// text of the same alpha carry one state between them. The first version of
// the field was the other way round, which would have put two meanings of one
// word in one package.
func TestOpacityIsAStampsOpacity(t *testing.T) {
	d, err := Open(onePage(t, 200, 100))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Place("1", Placement{Data: redSquare(t), Width: 40, Opacity: 0.25}); err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("1", Stamp{Text: "draft", Size: 12, Opacity: 0.25}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(out, []byte("PdfopsA250")); n == 0 {
		t.Fatal("no alpha state named for 0.25 anywhere in the file")
	}
	// One state dictionary, shared: the name says the alpha, so the two
	// cannot disagree about what 0.25 means.
	if n := bytes.Count(out, []byte("/ExtGState")); n > 2 {
		t.Errorf("%d ExtGState dictionaries, want the picture and the text to share one per page", n)
	}
	// An opaque placement writes no state at all.
	d2, _ := Open(onePage(t, 200, 100))
	if err := d2.Place("1", Placement{Data: redSquare(t), Width: 40}); err != nil {
		t.Fatal(err)
	}
	out2, _ := d2.Bytes()
	if bytes.Contains(out2, []byte("PdfopsA")) {
		t.Error("an opaque picture wrote an alpha state")
	}
}

// ⛔ A drawn signature is ink on a TRANSPARENT background, so the picture's
// alpha has to survive as a soft mask. Without one it arrives as ink on a
// black rectangle, which is the single most visible way this feature can fail.
func TestAPlacedPicturesTransparencyBecomesASoftMask(t *testing.T) {
	d, err := Open(onePage(t, 200, 100))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Place("1", Placement{Data: transparentPicture(t), Width: 40}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("/SMask")) {
		t.Error("no soft mask: a signature with a transparent background would come out on a black box")
	}
}

func TestWhatPlaceRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
		p    Placement
		says string
	}{
		{"no picture", "all", Placement{}, "no picture"},
		{"a page that is not there", "9", Placement{Data: []byte{1}}, "page"},
		{"a negative width", "all", Placement{Data: []byte{1}, Width: -1}, "-1 by 0 points"},
		{"an opacity above one", "all", Placement{Data: []byte{1}, Opacity: 1.5}, "outside zero to one"},
		{"an opacity below zero", "all", Placement{Data: []byte{1}, Opacity: -0.5}, "outside zero to one"},
		{"bytes that are not a picture", "all", Placement{Data: []byte("no picture here")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Open(onePage(t, 100, 100))
			if err != nil {
				t.Fatal(err)
			}
			err = d.Place(tc.spec, tc.p)
			if err == nil {
				t.Fatal("accepted")
			}
			if tc.says != "" && !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %v, want it to say %q", err, tc.says)
			}
		})
	}
	// ⛔ AND A VERY HIGH DPI IS ACCEPTED, which is the opposite of what this
	// test first asserted. I expected a size that "rounds to nothing" to be
	// refused; measured, the writer keeps full precision — 1.44e-6 points
	// comes out as "0.00000144" — so nothing rounds to zero and there is no
	// invisible-but-looks-fine case to guard. A sub-pixel picture is a
	// caller's instruction, and the code was right.
	d, err := Open(onePage(t, 100, 100))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Place("all", Placement{Data: redSquare(t), DPI: 1e9}); err != nil {
		t.Errorf("a very high dpi was refused: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if r := placedRects(t, out)[0]; r[2] <= 0 || r[2] > 0.001 {
		t.Errorf("width = %g, want a tiny positive size", r[2])
	}

	// What IS refused is a size that really is zero, which an infinite dpi
	// reaches: 72 divided by infinity is nought.
	d2, err := Open(onePage(t, 100, 100))
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Place("all", Placement{Data: redSquare(t), DPI: math.Inf(1)}); err == nil {
		t.Error("a picture of zero size was accepted")
	} else if !strings.Contains(err.Error(), "draws nothing") {
		t.Errorf("error = %v, want it to say the picture draws nothing", err)
	}
}

// close compares two point measurements, which go through a decimal string on
// their way into the file.
func close(a, b float64) bool { return a-b < 0.01 && b-a < 0.01 }

// ⛔ POPPLER LOOKS AT THE PAGE, because a package that agrees with itself about
// where it put something has proved nothing. The picture is red, the page is
// white, and the test asserts which PIXELS are red — which is the only
// statement that cannot be satisfied by arithmetic that is wrong in the same
// way twice.
func TestAPlacedPictureJudgedByPoppler(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("pdftoppm is not installed, and CI is meant to have it")
		}
		t.Skip("pdftoppm is not installed")
	}
	d, err := Open(onePage(t, 200, 100))
	if err != nil {
		t.Fatal(err)
	}
	// Bottom left, 40 by 20 points, ten points in from each edge: so the red
	// runs from x=10..50 and y=10..30 in PDF coordinates, which is
	// y=70..90 counting down from the top of a 100-point page.
	if err := d.Place("1", Placement{
		Data: redSquare(t), Width: 40, Position: BottomLeft, Margin: 10,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "placed.pdf")
	if err := os.WriteFile(file, out, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("pdftoppm", "-png", "-r", "72", "-cropbox", file, filepath.Join(dir, "page"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v: %s", err, b)
	}
	f, err := os.Open(filepath.Join(dir, "page-1.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("rendered %dx%d, want the page's own 200x100", b.Dx(), b.Dy())
	}
	isRed := func(x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		return r > 0xc000 && g < 0x4000 && b < 0x4000
	}
	for _, p := range [][2]int{{30, 80}, {12, 72}, {48, 88}} {
		if !isRed(p[0], p[1]) {
			r, g, b, _ := img.At(p[0], p[1]).RGBA()
			t.Errorf("(%d,%d) is %04x%04x%04x, want the picture's red", p[0], p[1], r, g, b)
		}
	}
	for _, p := range [][2]int{{5, 80}, {30, 95}, {30, 60}, {60, 80}, {150, 20}} {
		if isRed(p[0], p[1]) {
			t.Errorf("(%d,%d) is red, and the picture should not reach it", p[0], p[1])
		}
	}
}

// The helper that reads a written page's content back is pageContentOf, in
// recolour_test.go: one per package, not one per feature.

// ⛔ A page this package MADE, placed on without a round trip through a file.
// It is a different path — madeContent rather than borrowedExtras — and the
// coverage profile is what said no test took it: every other test here opens a
// file first, because that is what a caller of this package usually has.
func TestAPictureOnAPageThisPackageMade(t *testing.T) {
	d := New()
	d.Blank(200, 100)
	if err := d.Place("1", Placement{
		Data: redSquare(t), Width: 40, Position: BottomLeft, Margin: 10,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	r := placedRects(t, out)
	if len(r) != 1 {
		t.Fatalf("%d pictures, want one", len(r))
	}
	if !close(r[0][0], 10) || !close(r[0][1], 10) {
		t.Errorf("the picture went to (%g,%g), want the bottom-left margin (10,10)", r[0][0], r[0][1])
	}

	// And on a made page drawn INSIDE another, which is the form path: the
	// wrapper has to carry the picture, not the tile.
	d2 := New()
	d2.Blank(200, 100)
	if err := d2.Place("1", Placement{Data: redSquare(t), Width: 40}); err != nil {
		t.Fatal(err)
	}
	if err := d2.NUp(2); err != nil {
		t.Fatal(err)
	}
	out2, err := d2.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out2, []byte("/PdfopsIm0")) {
		t.Error("a page composed onto a sheet lost its placed picture")
	}
}
