// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"compress/zlib"
	"fmt"

	"github.com/go-gfx/gfx/codec"
	"github.com/go-gfx/gfx/raster"
	"github.com/go-pdfkit/reader"
)

// picture is one image made into a page.
type picture struct {
	// stored is the image as the file held it, when that is a format a PDF
	// can carry as it stands. filter names it.
	stored []byte
	filter reader.Name
	// pix is the decoded image, for everything else.
	pix           *raster.Image
	width, height int
}

// Picture adds a page holding one image, the size of the image.
//
// This is the other half of drawing a page: a scanner, a camera and a
// screenshot all produce a picture, and what people want of a PDF toolkit is
// to be handed one back with the picture in it.
//
// Every format go-gfx/gfx reads is accepted, which is more than a PDF can
// carry. A JPEG is put in as it stands, because a PDF carries JPEG and
// re-encoding one loses a little of it for nothing; everything else is decoded
// and written as samples, compressed. An image with any transparency in it
// gets a soft mask, so a PNG drawn over the page's white does not come out
// with black behind it.
//
// dpi says how many of the image's pixels go into an inch of paper; 0 means
// 72, one point per pixel. A photograph from a telephone at 72 is a page the
// size of a wall, and at 300 it is a photograph.
func (d *Doc) Picture(data []byte, dpi float64) error {
	if dpi <= 0 {
		dpi = 72
	}
	p, err := readPicture(data)
	if err != nil {
		return err
	}
	scale := 72 / dpi
	d.pages = append(d.pages, Page{
		picture: p,
		size:    [2]float64{float64(p.width) * scale, float64(p.height) * scale},
	})
	return nil
}

// readPicture works out how an image will be carried.
//
// Every format is decoded, even the one that is then carried undecoded, because
// the page has to be the size of the picture and only the picture says what
// that is.
func readPicture(data []byte) (*picture, error) {
	format := codec.Sniff(data)
	if format == codec.Unknown {
		return nil, fmt.Errorf("ops: these bytes are not a picture in any format that can be read")
	}
	img, err := codec.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("ops: this picture cannot be read: %w", err)
	}
	// A picture of no size is a page of no size. GIF, BMP and JPEG all decode
	// one without complaining — they are not malformed files, they are empty
	// ones — so the check is here rather than left to the decoders.
	if img.W <= 0 || img.H <= 0 {
		return nil, fmt.Errorf("ops: a picture of %d by %d is not one", img.W, img.H)
	}
	if format == codec.JPEG {
		// A PDF carries JPEG itself, and re-encoding one would lose a little
		// of it for nothing.
		return &picture{stored: data, filter: "DCTDecode",
			width: img.W, height: img.H}, nil
	}
	return &picture{pix: img, width: img.W, height: img.H}, nil
}

// pictureContent writes the image and returns the operators that draw it over
// the whole page, with the resources they need.
func (d *Doc) pictureContent(w *reader.Writer, p Page, area [4]float64) ([]byte, reader.Dict) {
	pic := p.picture
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(pic.width), "Height": reader.Integer(pic.height),
		"BitsPerComponent": reader.Integer(8),
		"ColorSpace":       reader.Name("DeviceRGB"),
	}
	var ref reader.Object
	if pic.stored != nil {
		dict["Filter"] = pic.filter
		ref = w.Add(&reader.Stream{Dict: dict, Raw: pic.stored})
	} else {
		rgb, alpha := split(pic.pix)
		if alpha != nil {
			ref = w.Add(&reader.Stream{Dict: reader.Dict{
				"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
				"Width": reader.Integer(pic.width), "Height": reader.Integer(pic.height),
				"BitsPerComponent": reader.Integer(8),
				"ColorSpace":       reader.Name("DeviceGray"),
				"Filter":           reader.Name("FlateDecode"),
			}, Raw: deflate(alpha)})
			dict["SMask"] = ref
		}
		dict["Filter"] = reader.Name("FlateDecode")
		ref = w.Add(&reader.Stream{Dict: dict, Raw: deflate(rgb)})
	}
	content := fmt.Sprintf("q %g 0 0 %g %g %g cm /Pic Do Q",
		area[2]-area[0], area[3]-area[1], area[0], area[1])
	return []byte(content), reader.Dict{"XObject": reader.Dict{"Pic": ref}}
}

// split separates an image into its colours and its transparency. The alpha is
// nil when every pixel is opaque, which is most pictures and saves carrying a
// mask that says nothing.
func split(img *raster.Image) (rgb, alpha []byte) {
	n := img.W * img.H
	rgb = make([]byte, n*3)
	opaque := true
	for i := 0; i < n; i++ {
		rgb[i*3], rgb[i*3+1], rgb[i*3+2] = img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2]
		if img.Pix[i*4+3] != 255 {
			opaque = false
		}
	}
	if opaque {
		return rgb, nil
	}
	alpha = make([]byte, n)
	for i := 0; i < n; i++ {
		alpha[i] = img.Pix[i*4+3]
	}
	return rgb, alpha
}

// deflate compresses samples the way a PDF carries them.
func deflate(data []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	// A bytes.Buffer never fails to take bytes, and Close only flushes.
	zw.Write(data)
	zw.Close()
	return buf.Bytes()
}
