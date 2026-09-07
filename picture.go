// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
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
	// components is how many colour channels the stored bytes carry, which
	// decides the colour space they are declared in. It is meaningless when
	// the image was decoded instead.
	components int
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
	// A PDF carries JPEG itself, and re-encoding one would lose a little of it
	// for nothing — but only where the colour space it will be declared in is
	// the one the bytes actually hold.
	if n, ok := jpegComponents(data); ok && format == codec.JPEG && (n == 1 || n == 3) {
		return &picture{stored: data, filter: "DCTDecode", components: n,
			width: img.W, height: img.H}, nil
	}
	return &picture{pix: img, width: img.W, height: img.H}, nil
}

// pictureContent writes the image and returns the operators that draw it over
// the whole page, with the resources they need.
func (d *Doc) pictureContent(w *reader.Writer, p Page, area [4]float64) ([]byte, reader.Dict) {
	ref := writePicture(w, p.picture)
	content := fmt.Sprintf("q %g 0 0 %g %g %g cm /Pic Do Q",
		area[2]-area[0], area[3]-area[1], area[0], area[1])
	return []byte(content), reader.Dict{"XObject": reader.Dict{"Pic": ref}}
}

// writePicture puts an image into the file and returns what refers to it.
func writePicture(w *reader.Writer, pic *picture) reader.Object {
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(pic.width), "Height": reader.Integer(pic.height),
		"BitsPerComponent": reader.Integer(8),
		"ColorSpace":       reader.Name("DeviceRGB"),
	}
	if pic.stored != nil {
		if pic.components == 1 {
			dict["ColorSpace"] = reader.Name("DeviceGray")
		}
		dict["Filter"] = pic.filter
		return w.Add(&reader.Stream{Dict: dict, Raw: pic.stored})
	}
	rgb, alpha := split(pic.pix)
	if alpha != nil {
		dict["SMask"] = w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
			"Width": reader.Integer(pic.width), "Height": reader.Integer(pic.height),
			"BitsPerComponent": reader.Integer(8),
			"ColorSpace":       reader.Name("DeviceGray"),
			"Filter":           reader.Name("FlateDecode"),
		}, Raw: deflate(alpha)})
	}
	dict["Filter"] = reader.Name("FlateDecode")
	return w.Add(&reader.Stream{Dict: dict, Raw: deflate(rgb)})
}

// A placedPicture is an image drawn at a place on a page rather than over the
// whole of it, which is what a form's logo is.
type placedPicture struct {
	pic *picture
	// rect is where it goes, in the page's own coordinates: x, y of the
	// bottom left, then width and height.
	rect [4]float64
}

// placedContent draws a page's placed images, under everything else.
func placedContent(w *reader.Writer, p Page) ([]byte, reader.Dict) {
	var buf bytes.Buffer
	xobjects := reader.Dict{}
	for i, pl := range p.pictures {
		name := reader.Name(fmt.Sprintf("PdfopsIm%d", i))
		xobjects[name] = writePicture(w, pl.pic)
		fmt.Fprintf(&buf, "q %s 0 0 %s %s %s cm /%s Do Q\n",
			number(pl.rect[2]), number(pl.rect[3]),
			number(pl.rect[0]), number(pl.rect[1]), name)
	}
	return buf.Bytes(), reader.Dict{"XObject": xobjects}
}

// jpegComponents reports how many colour channels a JPEG declares, reading the
// frame header rather than trusting the file's name or its MIME type.
//
// It matters because the bytes are put into the PDF UNDECODED, so the colour
// space written beside them has to be the one they are actually in. A
// greyscale JPEG declared DeviceRGB is read three samples at a time out of a
// stream that holds one, and comes out a third of its height with its content
// crushed into the top of the box — which is what a French cerfa's scanned
// background did, at 4961 by 3508 and one component.
//
// The frame header is the only place the count is written. Markers run
// FF C0..CF, of which C4, C8 and CC are not frames; the count is the tenth
// byte of the segment, after its length, the precision and the two
// dimensions.
func jpegComponents(data []byte) (int, bool) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, false
	}
	for i := 2; i+3 < len(data); {
		if data[i] != 0xFF {
			return 0, false
		}
		marker := data[i+1]
		// FF is also the padding between segments, and it is one byte: the
		// next byte is the marker, not a length.
		if marker == 0xFF {
			i++
			continue
		}
		// The markers that carry no segment at all are two bytes and no more.
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return 0, false
		}
		if marker >= 0xC0 && marker <= 0xCF &&
			marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
			if size < 8 {
				return 0, false
			}
			return int(data[i+9]), true
		}
		// The scan begins the entropy-coded data, and the frame is behind us.
		if marker == 0xDA {
			return 0, false
		}
		i += 2 + size
	}
	return 0, false
}

// pictureDPI reads the resolution an image declares, in dots per inch across
// and down. It answers 72 and 72 — one point per pixel — for an image that
// declares none, which is what a PDF assumes of a picture with nothing to say.
//
// It matters for one thing only, and that thing is not small. XFA's
// aspect="actual" means "draw it at its own size", and its own size is its
// pixels AT ITS OWN RESOLUTION: pdfium converts with XFA_UnitPx2Pt(px, dpi)
// before it does anything else (cxfa_ffwidget.cpp:55-57). Canada Revenue
// Agency forms are drawn this way, and their logos are 1200 dpi PNGs — 2617
// pixels across, which is 157.02 points, which is to a hundredth the width
// their template writes for the box. Taken at 72 the same logo is 2617 points
// wide and covers a third of the sheet in black.
//
// Every other aspect is a RATIO of the box to the picture, and a resolution
// cancels out of it.
func pictureDPI(data []byte) (dx, dy float64) {
	if d, e, ok := pngDPI(data); ok {
		return d, e
	}
	if d, e, ok := jpegDPI(data); ok {
		return d, e
	}
	return 72, 72
}

// pngDPI reads a PNG's pHYs chunk, which gives pixels per unit and says which
// unit. Only the metre is defined; anything else means the numbers are an
// aspect ratio rather than a resolution, which is not one.
func pngDPI(data []byte) (dx, dy float64, ok bool) {
	const sig = "\x89PNG\r\n\x1a\n"
	if len(data) < 8 || string(data[:8]) != sig {
		return 0, 0, false
	}
	for i := 8; i+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[i:]))
		if n < 0 || i+12+n > len(data) {
			return 0, 0, false
		}
		kind := string(data[i+4 : i+8])
		body := data[i+8 : i+8+n]
		if kind == "pHYs" && n >= 9 && body[8] == 1 {
			x := float64(binary.BigEndian.Uint32(body[0:]))
			y := float64(binary.BigEndian.Uint32(body[4:]))
			if x <= 0 || y <= 0 {
				return 0, 0, false
			}
			// A metre is 39.3700787... inches, and the round number is the
			// inch: 0.0254 metres exactly.
			return x * 0.0254, y * 0.0254, true
		}
		// The samples begin here; a resolution written after them is written
		// too late to be read.
		if kind == "IDAT" || kind == "IEND" {
			return 0, 0, false
		}
		i += 12 + n
	}
	return 0, 0, false
}

// jpegDPI reads the density out of a JFIF header, whose units are either dots
// per inch or dots per centimetre.
func jpegDPI(data []byte) (dx, dy float64, ok bool) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, 0, false
	}
	for i := 2; i+3 < len(data); {
		if data[i] != 0xFF {
			return 0, 0, false
		}
		marker := data[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return 0, 0, false
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE0 && len(seg) >= 12 && string(seg[:5]) == "JFIF\x00" {
			x := float64(binary.BigEndian.Uint16(seg[8:]))
			y := float64(binary.BigEndian.Uint16(seg[10:]))
			if x <= 0 || y <= 0 {
				return 0, 0, false
			}
			switch seg[7] {
			case 1: // dots per inch
				return x, y, true
			case 2: // dots per centimetre
				return x * 2.54, y * 2.54, true
			}
			// Unit 0 is an aspect ratio and not a resolution.
			return 0, 0, false
		}
		if marker == 0xDA {
			return 0, 0, false
		}
		i += 2 + size
	}
	return 0, 0, false
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
