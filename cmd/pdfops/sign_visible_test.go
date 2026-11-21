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
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

func TestParseVisible(t *testing.T) {
	good := map[string][4]float64{
		"1,2,3,4":         {1, 2, 3, 4},
		" 1 , 2.5,3,-4 ":  {1, 2.5, 3, -4},
		"1e2,0,2e2,0.5e1": {100, 0, 200, 5},
	}
	for in, want := range good {
		if got, err := parseVisible(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{
		"", "1,2,3", "1,2,3,4,5", "a,2,3,4", "1,2,3,", "NaN,0,1,1", "0,0,Inf,1", "0,0,1,-inf",
		strings.Repeat("1,", 200) + "1",
	} {
		if got, err := parseVisible(in); err == nil || got != [4]float64{} {
			t.Errorf("%q: read as %v", in, got)
		}
	}
	// The length bound is on the edge: 256 bytes pass the length check.
	edge := "1,2,3," + strings.Repeat("0", maxVisibleArg-len("1,2,3,"))
	if _, err := parseVisible(edge); err != nil {
		t.Errorf("a value of exactly %d bytes: %v", maxVisibleArg, err)
	}
	if _, err := parseVisible(edge + "0"); err == nil {
		t.Error("a value over the bound was read")
	}
}

// FuzzParseVisible: whatever the flag holds, parsing returns or refuses, and a
// box it returns is made of finite numbers. The corpus is in testdata/fuzz.
func FuzzParseVisible(f *testing.F) {
	for _, s := range []string{"1,2,3,4", "", ",,,", "NaN,1,2,3", "1e999,0,0,0", "0x1p3,1,2,3", " 1 , 2 ,3,4", "1,2,3,4,5"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := parseVisible(s)
		if err != nil {
			return
		}
		for _, v := range r {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("%q read as %v", s, r)
			}
		}
	})
}

func logoPNG(t *testing.T, dir string) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for x := 0; x < 8; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.NRGBA{200, 0, 0, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "logo.png")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSignVerbVisible(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	key, cert := credentials(t, dir, "ecdsa")
	logo := logoPNG(t, dir)
	for name, args := range map[string][]string{
		"text":          {"-visible", "20,20,180,80", "-text", "Approved"},
		"image":         {"-visible", "20,20,180,80", "-image", logo},
		"both":          {"-visible", "20,20,180,80", "-image", logo, "-text", "Approved"},
		"default text":  {"-visible", "20,20,180,80"},
		"a named field": {"-visible", "20,20,180,80", "-field", "approval", "-text", "x"},
	} {
		out := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".pdf")
		a := append([]string{"sign", "-key", key, "-cert", cert}, args...)
		if code, _, msg := exec(append(a, in, out)...); code != 0 {
			t.Errorf("%s: code %d: %s", name, code, msg)
			continue
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		d, err := reader.Open(b)
		if err != nil {
			t.Fatal(err)
		}
		rs, err := signature.VerifyAll(d)
		if err != nil || len(rs) != 1 || rs[0].Err != nil || !rs[0].Signature.CoversWholeFile {
			t.Errorf("%s: %v %+v", name, err, rs)
		}
		if !bytes.Contains(b, []byte("/AP")) {
			t.Errorf("%s: no appearance written", name)
		}
	}
}

func TestSignVerbVisibleRefusals(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	key, cert := credentials(t, dir, "ecdsa")
	logo := logoPNG(t, dir)
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, make([]byte, maxSignImage+1), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")
	for name, c := range map[string]struct {
		args []string
		say  string
	}{
		"not four numbers":       {[]string{"-visible", "1,2,3"}, "four numbers"},
		"not a number":           {[]string{"-visible", "a,2,3,4"}, "finite"},
		"an inverted box":        {[]string{"-visible", "50,50,10,10"}, "box"},
		"a box far off the page": {[]string{"-visible", "9000,9000,9010,9010"}, "outside"},
		"text without a box":     {[]string{"-text", "x"}, "-visible"},
		"image without a box":    {[]string{"-image", logo}, "-visible"},
		"a missing image":        {[]string{"-visible", "1,1,50,50", "-image", filepath.Join(dir, "none.png")}, "none.png"},
		"an image too large":     {[]string{"-visible", "1,1,50,50", "-image", big}, "larger"},
		"an image that is not":   {[]string{"-visible", "1,1,50,50", "-image", key}, "image"},
	} {
		a := append([]string{"sign", "-key", key, "-cert", cert}, c.args...)
		code, _, msg := exec(append(a, in, out)...)
		if code == 0 {
			t.Errorf("%s: signed", name)
		} else if !strings.Contains(msg, c.say) {
			t.Errorf("%s: %q does not say %q", name, msg, c.say)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused signature left a file behind")
	}
}
