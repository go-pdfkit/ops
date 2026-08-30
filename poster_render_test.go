// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-pdfkit/reader"
)

// This file checks a poster the way a person would: it prints one and looks at
// the sheets. poppler does the printing and the looking, because a package
// that agrees with itself about where it put something has proved nothing. It
// is skipped where pdftoppm is not installed.

// posterPalette gives each cell of a three-by-three page a colour of its own,
// so that a rendered sheet says which cell it came from.
var posterPalette = [9][3]float64{
	{1, 0, 0}, {0, 0.6, 0}, {0, 0, 1},
	{1, 0.6, 0}, {0.6, 0, 0.6}, {0, 0.6, 0.6},
	{0.4, 0.2, 0}, {1, 0, 1}, {0.3, 0.3, 0.3},
}

// markedPage builds a page 300 by 600 points divided into a three-by-three
// grid of 100 by 200 cells. Each cell carries a block of its own colour,
// centred, and a small black pip tucked into the cell's top-left corner. The
// colour says which cell a sheet shows; the pip says which way up it is.
func markedPage(t *testing.T) []byte {
	t.Helper()
	var c bytes.Buffer
	for row := 0; row < 3; row++ {
		for col := 0; col < 3; col++ {
			x, top := float64(col)*100, float64(3-row)*200
			p := posterPalette[row*3+col]
			fmt.Fprintf(&c, "%g %g %g rg %g %g 40 80 re f\n", p[0], p[1], p[2], x+30, top-140)
			fmt.Fprintf(&c, "0 0 0 rg %g %g 12 12 re f\n", x+4, top-16)
		}
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: c.Bytes()})
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content})
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(300), reader.Integer(600)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// inkBox reports the bounding box of the pixels a predicate accepts, and how
// many there were.
func inkBox(img image.Image, want func(r, g, b uint32) bool) (box [4]int, n int) {
	b := img.Bounds()
	box = [4]int{b.Max.X, b.Max.Y, b.Min.X, b.Min.Y}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			if !want(r>>8, g>>8, bb>>8) {
				continue
			}
			n++
			box[0], box[1] = min(box[0], x), min(box[1], y)
			box[2], box[3] = max(box[2], x), max(box[3], y)
		}
	}
	return box, n
}

// closeTo accepts pixels within a channel or so of a colour, which is what an
// anti-aliased renderer leaves in the middle of a solid block.
func closeTo(want [3]uint32) func(r, g, b uint32) bool {
	off := func(a, b uint32) uint32 {
		if a > b {
			return a - b
		}
		return b - a
	}
	return func(r, g, b uint32) bool {
		return off(r, want[0]) < 12 && off(g, want[1]) < 12 && off(b, want[2]) < 12
	}
}

// boxNear compares two boxes, allowing a pixel either way: which pixel a
// renderer calls the last one of an edge is its own business.
func boxNear(got, want [4]int) bool {
	for i := range got {
		if got[i] < want[i]-1 || got[i] > want[i]+1 {
			return false
		}
	}
	return true
}

func eightBit(c [3]float64) [3]uint32 {
	return [3]uint32{uint32(c[0]*255 + 0.5), uint32(c[1]*255 + 0.5), uint32(c[2]*255 + 0.5)}
}

func TestPosterJudgedByPoppler(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		// Skipping is right on a machine that has no poppler, and wrong in CI,
		// where the workflow installs one: a judge that quietly absents itself
		// is no better than no judge, and this is the only thing here that
		// reads the output rather than the arithmetic that produced it.
		if os.Getenv("CI") != "" {
			t.Fatal("pdftoppm is not installed, and CI is meant to have it")
		}
		t.Skip("pdftoppm is not installed")
	}
	d, err := Open(markedPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Poster(3, 3); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "poster.pdf")
	if err := os.WriteFile(file, out, 0o600); err != nil {
		t.Fatal(err)
	}
	// A point to the pixel, so a coordinate in the file is a coordinate in the
	// picture and the sums below can be read.
	cmd := exec.Command("pdftoppm", "-png", "-r", "72", "-cropbox", file, filepath.Join(dir, "sheet"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v: %s", err, b)
	}
	for i := 0; i < 9; i++ {
		f, err := os.Open(filepath.Join(dir, fmt.Sprintf("sheet-%d.png", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != 300 || b.Dy() != 600 {
			t.Fatalf("sheet %d rendered %dx%d, want the page's own 300x600", i+1, b.Dx(), b.Dy())
		}
		want := eightBit(posterPalette[i])

		// Sheet i shows cell i, three times life size: the block that was 40
		// by 80 in the middle of a cell is 120 by 240 in the middle of a
		// sheet. Reading order is the claim being tested — that this is cell
		// i's colour and not some other cell's.
		box, n := inkBox(img, closeTo(want))
		if n == 0 {
			t.Errorf("sheet %d shows nothing of cell %d's colour %v", i+1, i+1, want)
			continue
		}
		if !boxNear(box, [4]int{90, 180, 209, 419}) {
			t.Errorf("sheet %d: block at %v, want (90,180)-(209,419)", i+1, box)
		}
		for j, q := range posterPalette {
			other := eightBit(q)
			if j == i || other == want {
				continue
			}
			// Anti-aliasing blends an edge towards white and can pass close to
			// another entry of the palette, so a handful of pixels is not
			// evidence. A block is twenty-eight thousand of them.
			if _, m := inkBox(img, closeTo(other)); m > 500 {
				t.Errorf("sheet %d also shows cell %d's colour %v, in %d pixels", i+1, j+1, other, m)
			}
		}

		// The pip sat in its cell's top-left corner, so it must sit in the
		// sheet's. A poster laid out from the bottom has every piece present
		// and every one of them in the wrong place; this is what says so.
		pip, n := inkBox(img, func(r, g, b uint32) bool { return r < 40 && g < 40 && b < 40 })
		if n == 0 {
			t.Errorf("sheet %d has no pip", i+1)
			continue
		}
		if !boxNear(pip, [4]int{12, 12, 47, 47}) {
			t.Errorf("sheet %d: pip at %v, want (12,12)-(47,47) — the top-left corner", i+1, pip)
		}
	}
}
