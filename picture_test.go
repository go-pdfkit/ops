// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-gfx/gfx/codec"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// picBytes writes a picture in the given format: the left half red and opaque,
// the right half whatever alpha is asked for.
func picBytes(t *testing.T, f codec.Format, alpha uint8) []byte {
	t.Helper()
	const w, h = 32, 16
	img := raster.New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			if x < w/2 {
				img.Pix[i], img.Pix[i+3] = 255, 255
			} else {
				img.Pix[i+1], img.Pix[i+3] = 255, alpha
			}
		}
	}
	var buf bytes.Buffer
	if err := codec.Encode(&buf, img, f); err != nil {
		t.Fatalf("writing a %s to make a page of: %v", f, err)
	}
	return buf.Bytes()
}

// emptyPicture writes a picture with no pixels in it. GIF, BMP and JPEG all
// accept one and hand it back without complaining: it is not a malformed file,
// it is an empty one, and a page cannot be made of it.
func emptyPicture(t *testing.T, f codec.Format) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := codec.Encode(&buf, raster.New(0, 0), f); err != nil {
		t.Fatalf("writing an empty %s: %v", f, err)
	}
	return buf.Bytes()
}

// resolved is the object a reference names, or Null.
func resolved(d *reader.Document, v reader.Object) reader.Object {
	o, _ := d.Resolve(v)
	return o
}

// pageImage finds the one image a document's first page draws, decoded.
func pageImage(t *testing.T, out []byte) (reader.Dict, *reader.Stream, *reader.Document) {
	t.Helper()
	d, err := reader.Open(out)
	if err != nil {
		t.Fatalf("what was written cannot be read back: %v", err)
	}
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := d.Resolve(page.Get("Resources"))
	rd, ok := reader.ToDict(res)
	if !ok {
		t.Fatal("the page names no resources")
	}
	xo, _ := d.Resolve(rd.Get("XObject"))
	xd, ok := reader.ToDict(xo)
	if !ok || len(xd) != 1 {
		t.Fatalf("the page names %d XObjects", len(xd))
	}
	for _, v := range xd {
		o, _ := d.Resolve(v)
		st, ok := reader.ToStream(o)
		if !ok {
			t.Fatal("what it names is not a stream")
		}
		return page, st, d
	}
	return nil, nil, nil
}

func TestAPictureBecomesAPage(t *testing.T) {
	// Every format the fleet reads is accepted, which is more than a PDF can
	// carry: what a PDF cannot hold is decoded and written as samples.
	for _, f := range []codec.Format{codec.PNG, codec.JPEG, codec.GIF, codec.TIFF, codec.BMP} {
		t.Run(f.String(), func(t *testing.T) {
			d := New()
			if err := d.Picture(picBytes(t, f, 255), 0); err != nil {
				t.Fatal(err)
			}
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			page, st, doc := pageImage(t, out)
			// The page is the size of the picture at 72 to the inch.
			box, _ := reader.ToArray(resolved(doc, page.Get("MediaBox")))
			if len(box) != 4 {
				t.Fatalf("the page has no box")
			}
			w, _ := reader.ToFloat(resolved(doc, box[2]))
			h, _ := reader.ToFloat(resolved(doc, box[3]))
			if w != 32 || h != 16 {
				t.Errorf("the page came out %g by %g points", w, h)
			}
			wid, _ := reader.ToInt(resolved(doc, st.Dict.Get("Width")))
			hei, _ := reader.ToInt(resolved(doc, st.Dict.Get("Height")))
			if wid != 32 || hei != 16 {
				t.Errorf("the picture came out %d by %d", wid, hei)
			}
			// A JPEG goes in as it stands, because a PDF carries JPEG and
			// re-encoding one loses a little of it for nothing.
			filter, _ := reader.ToName(resolved(doc, st.Dict.Get("Filter")))
			want := reader.Name("FlateDecode")
			if f == codec.JPEG {
				want = "DCTDecode"
			}
			if filter != want {
				t.Errorf("it was carried as %s, want %s", filter, want)
			}
		})
	}
}

func TestThePictureIsTheOneThatWasGiven(t *testing.T) {
	// Not merely that a picture arrived: the left half is red and the right
	// half is green, so a page built from a mirrored or a flat picture is
	// caught.
	d := New()
	if err := d.Picture(picBytes(t, codec.PNG, 255), 0); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, st, doc := pageImage(t, out)
	data, _, err := reader.DecodeStream(st, doc.Get)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 32*16*3 {
		t.Fatalf("%d bytes of samples for a 32 by 16 picture in three components", len(data))
	}
	// Row 8, well inside each half.
	left := data[(8*32+4)*3 : (8*32+4)*3+3]
	right := data[(8*32+28)*3 : (8*32+28)*3+3]
	if left[0] < 200 || left[1] > 60 {
		t.Errorf("the left half came out %v", left)
	}
	if right[1] < 200 || right[0] > 60 {
		t.Errorf("the right half came out %v", right)
	}
}

func TestTransparencyBecomesASoftMask(t *testing.T) {
	// A PNG drawn over the page's white must not come out with black behind
	// it, which is what dropping the alpha channel does.
	d := New()
	if err := d.Picture(picBytes(t, codec.PNG, 0), 0); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, st, doc := pageImage(t, out)
	ms, ok := reader.ToStream(resolved(doc, st.Dict.Get("SMask")))
	if !ok {
		t.Fatal("a picture with transparency in it carries no soft mask")
	}
	alpha, _, err := reader.DecodeStream(ms, doc.Get)
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 32*16 {
		t.Fatalf("%d bytes of mask for a 32 by 16 picture", len(alpha))
	}
	if alpha[8*32+4] != 255 {
		t.Errorf("the opaque half has alpha %d", alpha[8*32+4])
	}
	if alpha[8*32+28] != 0 {
		t.Errorf("the transparent half has alpha %d", alpha[8*32+28])
	}
}

func TestAPictureWithNoTransparencyCarriesNoMask(t *testing.T) {
	// Most pictures are opaque, and a mask that says nothing is bytes for
	// nothing.
	d := New()
	if err := d.Picture(picBytes(t, codec.PNG, 255), 0); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, st, doc := pageImage(t, out)
	if _, ok := reader.ToStream(resolved(doc, st.Dict.Get("SMask"))); ok {
		t.Error("an opaque picture was given a soft mask")
	}
}

func TestHowManyPixelsGoIntoAnInch(t *testing.T) {
	// A photograph from a telephone at 72 to the inch is a page the size of a
	// wall, and at 300 it is a photograph.
	for _, tc := range []struct{ dpi, want float64 }{
		{0, 32}, {72, 32}, {144, 16}, {300, 7.68},
	} {
		d := New()
		if err := d.Picture(picBytes(t, codec.PNG, 255), tc.dpi); err != nil {
			t.Fatal(err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		page, _, doc := pageImage(t, out)
		box, _ := reader.ToArray(resolved(doc, page.Get("MediaBox")))
		w, _ := reader.ToFloat(resolved(doc, box[2]))
		if w != tc.want {
			t.Errorf("at %g to the inch the page is %g points wide, want %g", tc.dpi, w, tc.want)
		}
	}
}

func TestWhatIsNotAPicture(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"nothing at all", nil, "not a picture"},
		{"bytes of no format", []byte("hello, this is not an image"), "not a picture"},
		{"a PNG that stops part way", picBytes(t, codec.PNG, 255)[:20], "cannot be read"},
		{"a JPEG that stops part way", picBytes(t, codec.JPEG, 255)[:20], "cannot be read"},
		{"a picture of no size", emptyPicture(t, codec.GIF), "is not one"},
		{"a JPEG of no size", emptyPicture(t, codec.JPEG), "is not one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			err := d.Picture(tc.data, 0)
			if err == nil {
				t.Fatal("it was made into a page anyway")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("it said %q", err)
			}
			if d.PageCount() != 0 {
				t.Errorf("%d pages came of it", d.PageCount())
			}
		})
	}
}
