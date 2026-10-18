// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// signatureFile writes a picture to a file, which is how the verb takes one.
func signatureFile(t *testing.T, dir string) string {
	t.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			a := byte(0)
			if y > 5 && y < 15 {
				a = 0xff // a band of ink on a transparent background
			}
			m.Set(x, y, color.NRGBA{B: 0x80, A: a})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "signature.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPlaceVerb(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 3)
	out := filepath.Join(dir, "out.pdf")
	sig := signatureFile(t, dir)

	if code, _, msg := exec("place", "-image", sig, "-at", "bottom-right",
		"-width", "80", "-margin", "18", "-opacity", "0.9", in, out); code != 0 {
		t.Fatalf("place: code %d: %s", code, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	content, err := d.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "/PdfopsIm0 Do") {
		t.Errorf("the picture is not drawn on page one: %q", content)
	}

	// ⛔ -x and -y are an ANCHOR: the picture goes exactly there, and -at is
	// ignored rather than combined with it.
	anchored := filepath.Join(dir, "anchored.pdf")
	if code, _, msg := exec("place", "-image", sig, "-x", "12", "-y", "24",
		"-at", "top-left", "-dpi", "300", in, anchored); code != 0 {
		t.Fatalf("place -x -y: code %d: %s", code, msg)
	}
	b, err = os.ReadFile(anchored)
	if err != nil {
		t.Fatal(err)
	}
	d, err = reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	content, err = d.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), " 12 24 cm") {
		t.Errorf("the picture did not go to the corner it was given: %q", content)
	}

	// A range, and the page it does not name.
	ranged := filepath.Join(dir, "ranged.pdf")
	if code, _, msg := exec("place", "-image", sig, "-pages", "2", "-height", "30", in, ranged); code != 0 {
		t.Fatalf("place -pages: code %d: %s", code, msg)
	}
	b, _ = os.ReadFile(ranged)
	d, _ = reader.Open(b)
	first, _ := d.PageContent(1)
	second, _ := d.PageContent(2)
	if strings.Contains(string(first), "/PdfopsIm0") {
		t.Error("page one was drawn on, and the range named page two")
	}
	if !strings.Contains(string(second), "/PdfopsIm0") {
		t.Error("page two was not drawn on")
	}

	for _, args := range [][]string{
		{"place", in},
		{"place", "-nosuchflag", in, out},
		{"place", in, out}, // no -image
		{"place", "-image", sig, "-at", "nowhere", in, out},         // not a place
		{"place", "-image", "/no/such/picture.png", in, out},        // no picture
		{"place", "-image", sig, "-pages", "9", in, out},            // no such page
		{"place", "-image", sig, "-opacity", "2", in, out},          // outside zero to one
		{"place", "-image", sig, "/no/such/file.pdf", out},          // no such input
		{"place", "-image", sig, in, "/no/such/dir/x.pdf"},          // nowhere to write
		{"place", "-image", filepath.Join(dir, "out.pdf"), in, out}, // a PDF is not a picture
	} {
		if code, _, _ := exec(args...); code != 1 {
			t.Errorf("%v should fail", args)
		}
	}
}
