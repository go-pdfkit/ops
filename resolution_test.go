// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"testing"

	"github.com/go-gfx/gfx/codec"
)

// pngWith puts a chunk of its own into a PNG, just after the header, which is
// where a resolution has to be if it is to be read before the samples.
func pngWith(t *testing.T, kind string, body []byte) []byte {
	t.Helper()
	src := picBytes(t, codec.PNG, 255)
	// The signature and the IHDR, whose length is fixed at thirteen.
	const upToIHDR = 8 + 12 + 13
	if len(src) < upToIHDR {
		t.Fatal("the PNG has no header")
	}
	var chunk bytes.Buffer
	binary.Write(&chunk, binary.BigEndian, uint32(len(body)))
	chunk.WriteString(kind)
	chunk.Write(body)
	binary.Write(&chunk, binary.BigEndian,
		crc32.ChecksumIEEE(append([]byte(kind), body...)))
	out := append([]byte{}, src[:upToIHDR]...)
	out = append(out, chunk.Bytes()...)
	return append(out, src[upToIHDR:]...)
}

// phys builds a pHYs body: pixels per unit across and down, and which unit.
func phys(x, y uint32, unit byte) []byte {
	b := make([]byte, 9)
	binary.BigEndian.PutUint32(b[0:], x)
	binary.BigEndian.PutUint32(b[4:], y)
	b[8] = unit
	return b
}

func TestReadingAPNGsResolution(t *testing.T) {
	// 47 244 pixels to the metre is 1200 to the inch, which is what a Canada
	// Revenue Agency logo is drawn at.
	for _, c := range []struct {
		name   string
		in     []byte
		dx, dy float64
		ok     bool
	}{
		{"1200 dpi", pngWith(t, "pHYs", phys(47244, 47244, 1)), 1200, 1200, true},
		{"different across and down", pngWith(t, "pHYs", phys(7874, 3937, 1)), 200.0, 100.0, true},
		{"an aspect ratio rather than a resolution", pngWith(t, "pHYs", phys(1, 1, 0)), 0, 0, false},
		{"no density at all", pngWith(t, "pHYs", phys(0, 47244, 1)), 0, 0, false},
		{"a chunk too short to hold one", pngWith(t, "pHYs", []byte{1, 2}), 0, 0, false},
		{"no such chunk", picBytes(t, codec.PNG, 255), 0, 0, false},
		// A header and nothing after it: the chunks run out before anything
		// says either what the resolution is or that the samples have begun.
		{"chunks that run out", picBytes(t, codec.PNG, 255)[:8+12+13], 0, 0, false},
		{"not a PNG", picBytes(t, codec.JPEG, 255), 0, 0, false},
		{"nothing at all", nil, 0, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dx, dy, ok := pngDPI(c.in)
			if ok != c.ok {
				t.Fatalf("read=%v, wanted %v", ok, c.ok)
			}
			if ok && (round2(dx) != round2(c.dx) || round2(dy) != round2(c.dy)) {
				t.Errorf("%v by %v dpi, wanted %v by %v", dx, dy, c.dx, c.dy)
			}
		})
	}
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }

func TestAPNGWhoseChunkRunsPastTheEnd(t *testing.T) {
	good := pngWith(t, "pHYs", phys(47244, 47244, 1))
	// A length that reaches beyond the file is a truncated file, not a
	// resolution: the bytes said to be there are not.
	broken := append([]byte{}, good...)
	binary.BigEndian.PutUint32(broken[8:], 1<<20)
	if _, _, ok := pngDPI(broken); ok {
		t.Error("a chunk longer than the file was read")
	}
}

// jpegWithDensity writes a JPEG carrying a JFIF header of the given density.
// The standard library writes no such header at all, so it is put in here
// rather than patched.
func jpegWithDensity(t *testing.T, unit byte, x, y uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	app0 := []byte{0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, unit,
		byte(x >> 8), byte(x), byte(y >> 8), byte(y), 0, 0}
	out := append([]byte{}, b[:2]...)
	out = append(out, app0...)
	return append(out, b[2:]...)
}

func TestReadingAJPEGsResolution(t *testing.T) {
	for _, c := range []struct {
		name   string
		in     []byte
		dx, dy float64
		ok     bool
	}{
		{"dots per inch", jpegWithDensity(t, 1, 300, 150), 300, 150, true},
		{"dots per centimetre", jpegWithDensity(t, 2, 100, 50), 254, 127, true},
		{"an aspect ratio rather than a resolution", jpegWithDensity(t, 0, 1, 1), 0, 0, false},
		{"no density at all", jpegWithDensity(t, 1, 0, 300), 0, 0, false},
		{"not a JPEG", picBytes(t, codec.PNG, 255), 0, 0, false},
		{"nothing at all", nil, 0, 0, false},
		{"a marker that is not one", []byte{0xFF, 0xD8, 0x00, 0x01, 0x02, 0x03}, 0, 0, false},
		{"a segment longer than the file", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, 0, 0, false},
		{"a segment shorter than its length", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x01}, 0, 0, false},
		{"the scan before any header", []byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x02}, 0, 0, false},
		{"segments that run out", []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x02}, 0, 0, false},
		{"fill bytes and a restart marker", append([]byte{0xFF, 0xD8, 0xFF, 0xFF, 0xFF, 0xD0},
			jpegWithDensity(t, 1, 96, 96)[2:]...), 96, 96, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dx, dy, ok := jpegDPI(c.in)
			if ok != c.ok {
				t.Fatalf("read=%v, wanted %v", ok, c.ok)
			}
			if ok && (round2(dx) != round2(c.dx) || round2(dy) != round2(c.dy)) {
				t.Errorf("%v by %v dpi, wanted %v by %v", dx, dy, c.dx, c.dy)
			}
		})
	}
}

func TestAPictureThatDeclaresNoResolutionIsOnePointPerPixel(t *testing.T) {
	// Which is what a PDF assumes of a picture with nothing to say.
	for _, c := range []struct {
		name string
		in   []byte
	}{
		{"a PNG with no pHYs", picBytes(t, codec.PNG, 255)},
		{"a JPEG with no density", jpegWithDensity(t, 0, 1, 1)},
		{"bytes that are no picture", []byte("hello")},
	} {
		t.Run(c.name, func(t *testing.T) {
			if dx, dy := pictureDPI(c.in); dx != 72 || dy != 72 {
				t.Errorf("%v by %v", dx, dy)
			}
		})
	}
	if dx, dy := pictureDPI(pngWith(t, "pHYs", phys(7874, 7874, 1))); round2(dx) != 200 || round2(dy) != 200 {
		t.Errorf("a PNG that does declare one came to %v by %v", dx, dy)
	}
	if dx, _ := pictureDPI(jpegWithDensity(t, 1, 300, 300)); dx != 300 {
		t.Errorf("a JPEG that does declare one came to %v", dx)
	}
}
