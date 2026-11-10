package ops

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"testing"

	"github.com/go-pdfkit/reader"
)

// bandedJPEG is a picture of horizontal bands, each a flat colour, encoded as a
// JPEG.
//
// ⛔ THE BANDS ARE TALL. A JPEG works in blocks of eight samples and throws
// away what it likes inside them, so a picture of one-pixel rows would come
// back as something no test could state. What is asserted of it is never a
// colour anyway: the test compares the written samples against its OWN decode
// of the same file, which is the only claim this path makes.
func bandedJPEG(t *testing.T, w, h, bands int, grey bool) []byte {
	t.Helper()
	var img image.Image
	if grey {
		g := image.NewGray(image.Rect(0, 0, w, h))
		for y := range h {
			v := uint8(20 + (y*bands/h)*40)
			for x := range w {
				g.SetGray(x, y, color.Gray{Y: v})
			}
		}
		img = g
	} else {
		c := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			band := y * bands / h
			v := color.RGBA{R: uint8(20 + band*40), G: uint8(200 - band*30), B: 90, A: 255}
			for x := range w {
				c.SetRGBA(x, y, v)
			}
		}
		img = c
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// wholePage draws the picture across the whole of a 200-point page, so that a
// fraction of the page is the same fraction of the picture.
const wholePage = "q 200 0 0 200 0 0 cm /Im1 Do Q\n"

// jpegPage is picturePage's twin for a DCTDecode picture: one 200-point page
// with the JPEG on it under the same /Im1 name, so that samplesOf reads it
// back.
func jpegPage(t *testing.T, data []byte, w, h int, space reader.Name, content string, opt func(reader.Dict)) []byte {
	t.Helper()
	wr := reader.NewWriter("1.7")
	pages := wr.Reserve()
	dict := reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
		"Width": reader.Integer(w), "Height": reader.Integer(h),
		"ColorSpace": space, "BitsPerComponent": reader.Integer(8),
		"Filter": reader.Name("DCTDecode"),
	}
	if opt != nil {
		opt(dict)
	}
	pic := wr.Add(&reader.Stream{Dict: dict, Raw: data})
	body := wr.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)})
	page := wr.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pages, "Contents": body,
		"Resources": reader.Dict{"XObject": reader.Dict{"Im1": pic}},
	})
	wr.Put(pages, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)}})
	root := wr.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pages})
	out, err := wr.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// writtenPicture is the picture object of the written document's first page,
// for the tests that ask what its dictionary now says rather than what its
// samples hold.
func writtenPicture(t *testing.T, b []byte) *reader.Stream {
	t.Helper()
	doc, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	page, err := doc.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := doc.GetDict(page, "Resources")
	xo, ok := doc.GetDict(res, "XObject")
	if !ok {
		t.Fatal("the written page carries no XObject resources")
	}
	o, err := doc.Resolve(xo.Get("Im1"))
	if err != nil {
		t.Fatal(err)
	}
	st, ok := reader.ToStream(o)
	if !ok {
		t.Fatalf("Im1 is a %s, not a stream", o.Kind())
	}
	return st
}

// bottomQuarter is the bottom quarter of the 200-point page, which is the
// bottom quarter of a picture drawn across the whole of it.
var bottomQuarter = Redaction{Page: 1, Quads: []Quad{{
	TopLeftX: 0, TopLeftY: 50, TopRightX: 200, TopRightY: 50,
	BottomLeftX: 0, BottomLeftY: 0, BottomRightX: 200, BottomRightY: 0,
}}}

// TestARedactionOverAJPEGWritesWhatAReaderWouldHaveSeen is ISO 32000-1
// 12.5.6.23 for the half that was a refusal until now: the pixels under the
// region are destroyed, and the rest are written down unchanged.
//
// ⛔⛔ THE KEPT SAMPLES ARE COMPARED AGAINST THIS TEST'S OWN DECODE OF THE
// ORIGINAL JPEG, BYTE FOR BYTE. That is the whole claim the lossless path
// makes. Asserting a colour instead would be asserting what an encoder does
// with it, which is a different subject and a moving one; and asserting "it
// still looks about the same" would pass for a picture re-encoded into mud —
// which is what re-encoding as a JPEG would have done to every block of it,
// not only the blanked ones.
func TestARedactionOverAJPEGWritesWhatAReaderWouldHaveSeen(t *testing.T) {
	const w, h = 64, 64
	raw := bandedJPEG(t, w, h, 4, false)
	want, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}

	d, err := Open(jpegPage(t, raw, w, h, "DeviceRGB", wholePage, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(bottomQuarter); err != nil {
		t.Fatalf("Redact refused a JPEG it can blank: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ NOT "NO FILTER AT ALL": the writer deflates a plain stream on its way
	// out, as it does every other one, so what this asks is that the picture is
	// no longer a JPEG. A test written against the absence of a filter would
	// fail the day compression was turned on and say nothing about redaction.
	st := writtenPicture(t, out)
	if f, _ := st.Dict.Get("Filter").(reader.Name); f == "DCTDecode" {
		t.Error("the picture is still DCTDecode: the samples were not written back")
	}
	got := samplesOf(t, out)
	if len(got) != w*h*3 {
		t.Fatalf("the picture holds %d bytes, want %d", len(got), w*h*3)
	}

	// ⛔ The sample grid counts DOWN from the top and the page counts up, so
	// the bottom quarter of the page is the LAST quarter of the rows. Reading
	// it the other way blanks the mirror image, which looks like a redaction
	// until somebody compares it with the page.
	const kept = h * 3 / 4
	for y := range h {
		for x := range w {
			at := (y*w + x) * 3
			have := [3]byte{got[at], got[at+1], got[at+2]}
			if y >= kept {
				if have != [3]byte{0, 0, 0} {
					t.Fatalf("row %d is %v and the redaction covered it", y, have)
				}
				continue
			}
			r, g, b, _ := want.At(x, y).RGBA()
			was := [3]byte{byte(r >> 8), byte(g >> 8), byte(b >> 8)}
			if have != was {
				t.Fatalf("row %d, column %d came back %v where a reader of the JPEG would have "+
					"seen %v: the pixels nobody asked about were changed", y, x, have, was)
			}
		}
	}

	// ⛔⛔ AND THE JPEG ITSELF IS GONE FROM THE FILE. Writing the blanked
	// samples while the original stream stayed, referenced by nothing, is the
	// leak #74 was about: the document would read as redacted and still hold
	// every destroyed pixel, in a form any tool can decode.
	if anyObjectHolds(t, out, raw[:64]) {
		t.Error("the original JPEG is still in the file, in some object of it")
	}
}

// TestAGreyJPEGComesBackGrey: turning it into RGB would treble a scan to
// destroy a corner of it, and would say /DeviceRGB about samples carrying no
// colour.
func TestAGreyJPEGComesBackGrey(t *testing.T) {
	const w, h = 32, 32
	raw := bandedJPEG(t, w, h, 4, true)
	want, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(jpegPage(t, raw, w, h, "DeviceGray", wholePage, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(bottomQuarter); err != nil {
		t.Fatalf("Redact refused a grey JPEG: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if space, _ := writtenPicture(t, out).Dict.Get("ColorSpace").(reader.Name); space != "DeviceGray" {
		t.Errorf("its colour space is now %s", space)
	}
	got := samplesOf(t, out)
	if len(got) != w*h {
		t.Fatalf("a grey picture of %d samples came back as %d bytes", w*h, len(got))
	}
	const kept = h * 3 / 4
	for y := range h {
		for x := range w {
			have := got[y*w+x]
			if y >= kept {
				if have != 0 {
					t.Fatalf("row %d holds %d and the redaction covered it", y, have)
				}
				continue
			}
			g, _, _, _ := want.At(x, y).RGBA()
			if was := byte(g >> 8); have != was {
				t.Fatalf("row %d, column %d came back %d where a reader would have seen %d",
					y, x, have, was)
			}
		}
	}
}

// TestARedactionThatReachesNoJPEGLeavesItAlone: the rewrite is the price of
// destroying pixels, and a picture nobody redacted over must not pay it — a
// decoded and re-deflated photograph is several times the size of the JPEG it
// replaced.
func TestARedactionThatReachesNoJPEGLeavesItAlone(t *testing.T) {
	const w, h = 32, 32
	raw := bandedJPEG(t, w, h, 4, false)
	// Here the picture is 100 points square at (50,50), so there is room on
	// the page that is not picture.
	d, err := Open(jpegPage(t, raw, w, h, "DeviceRGB", "q 100 0 0 100 50 50 cm /Im1 Do Q", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(Redaction{Page: 1, Quads: []Quad{{
		TopLeftX: 0, TopLeftY: 40, TopRightX: 40, TopRightY: 40,
		BottomLeftX: 0, BottomLeftY: 0, BottomRightX: 40, BottomRightY: 0,
	}}}); err != nil {
		t.Fatalf("Redact refused a region that touches no picture: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	st := writtenPicture(t, out)
	if f, _ := st.Dict.Get("Filter").(reader.Name); f != "DCTDecode" {
		t.Errorf("the picture came back filtered with %q: it was decoded although nothing was "+
			"redacted over it", f)
	}
	if !bytes.Equal(st.Raw, raw) {
		t.Errorf("the picture's %d JPEG bytes became %d", len(raw), len(st.Raw))
	}
}

// ⛔ THE SIZE IS READ FROM THE HEADER AND REFUSED BEFORE ANYTHING IS DECODED.
// A JPEG says how big it is in a handful of bytes and the decoder then
// allocates width × height × 3, so checking after the decode is checking after
// the allocation — which is the whole of what the bound is for. The seam here
// does not stand in for the decoder: it WATCHES whether the decoder was
// reached, which is the claim.
func TestAJPEGPastTheCeilingIsRefusedBeforeItIsDecoded(t *testing.T) {
	// ⛔ A REAL BOMB AND THE REAL BOUND. Thirty-nine bytes that declare a
	// picture of four hundred megapixels, and not one sample of it: a JFIF
	// header, then a baseline frame header saying 8 bits, 20000 by 20000,
	// three components at 4:4:4. Decoding it would ask for 1.2 GB. Lowering a
	// knob to zero instead would test an arrangement made for the occasion and
	// leave the hundred that callers actually get unrun.
	//
	// ⛔ THE APP0 SEGMENT IS NOT DECORATION: image/jpeg returns from a
	// header-only read at the frame header only once it has seen a JFIF
	// marker, so without it this fixture is an unexpected EOF and the test
	// proves the "cannot read" arm instead of the ceiling.
	bomb := []byte{
		0xFF, 0xD8, // SOI
		0xFF, 0xE0, 0x00, 0x10, // APP0, 16 bytes
		'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, // version 1.1
		0x00,       // no density units
		0x00, 0x01, // 1 across
		0x00, 0x01, // 1 down
		0x00, 0x00, // no thumbnail
		0xFF, 0xC0, 0x00, 0x11, // SOF0, 17 bytes
		0x08,       // 8 bits a sample
		0x4E, 0x20, // 20000 high
		0x4E, 0x20, // 20000 wide
		0x03,             // three components
		0x01, 0x11, 0x00, // Y,  1x1, table 0
		0x02, 0x11, 0x01, // Cb, 1x1, table 1
		0x03, 0x11, 0x01, // Cr, 1x1, table 1
	}
	// The header has to be one the reader really reads, or this test proves
	// the "cannot read" arm instead of the ceiling.
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(bomb))
	if err != nil {
		t.Fatalf("the fixture is not a JPEG header at all: %v", err)
	}
	if cfg.Width*cfg.Height <= jpegMegapixels*1e6 {
		t.Fatalf("the fixture claims %d by %d, which is inside the bound it is meant to break",
			cfg.Width, cfg.Height)
	}

	decoded := false
	was := decodeJPEG
	t.Cleanup(func() { decodeJPEG = was })
	decodeJPEG = func(r io.Reader) (image.Image, error) {
		decoded = true
		return was(r)
	}

	d, err := Open(jpegPage(t, bomb, cfg.Width, cfg.Height, "DeviceRGB", wholePage, nil))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Redact(bottomQuarter)
	if err == nil {
		t.Fatal("a picture past the ceiling was accepted")
	}
	if !contains(err.Error(), "past the 100 megapixels this will decode") {
		t.Errorf("the refusal says %q", err)
	}
	// ⛔ AND THE DECODER WAS NEVER REACHED. The message alone would read the
	// same whether the check ran before the decode or after it, and after it
	// is after the allocation — which is the whole of what the bound is for.
	if decoded {
		t.Error("it was decoded anyway, which is the allocation the ceiling exists to refuse")
	}
}

// FuzzRedactOverAJPEG feeds arbitrary bytes to the new decoder arm: whatever
// they are, Redact answers — a picture blanked or a reason it was not — and
// never panics or runs away with the machine.
//
// ⛔ THE CEILING IS PART OF WHAT THIS FUZZES. A JPEG header is cheap to
// mutate into one claiming a gigantic picture, which is the input that turns a
// decoder into a denial of service; the seed above is one by hand, and the
// fuzzer finds the others.
func FuzzRedactOverAJPEG(f *testing.F) {
	f.Add(bandedJPEG(&testing.T{}, 16, 16, 2, false))
	f.Add([]byte{
		0xFF, 0xD8,
		0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00,
		0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
		0xFF, 0xC0, 0x00, 0x11, 0x08, 0x4E, 0x20, 0x4E, 0x20,
		0x03, 0x01, 0x11, 0x00, 0x02, 0x11, 0x01, 0x03, 0x11, 0x01})
	f.Add([]byte("not a JPEG at all"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Open(jpegPage(t, data, 16, 16, "DeviceRGB", wholePage, nil))
		if err != nil {
			t.Skip()
		}
		if err := d.Redact(bottomQuarter); err != nil {
			return
		}
		if _, err := d.Bytes(); err != nil {
			t.Fatalf("Redact accepted the picture and the write then refused it: %v", err)
		}
	})
}

// TestAJPEGThatStopsHalfWayIsRefused is the decoder's own arm: a header this
// reads and a scan it does not.
func TestAJPEGThatStopsHalfWayIsRefused(t *testing.T) {
	const w, h = 64, 64
	whole := bandedJPEG(t, w, h, 4, false)
	d, err := Open(jpegPage(t, whole[:len(whole)/2], w, h, "DeviceRGB", wholePage, nil))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Redact(bottomQuarter)
	if err == nil {
		t.Fatal("half a JPEG was accepted")
	}
	if !contains(err.Error(), "a JPEG this cannot read") {
		t.Errorf("the refusal says %q", err)
	}
	// ⛔ AND IT SAYS WHY IT MATTERS, like every other arm of this refusal: a
	// caller told here can still choose another region or another tool.
	if !contains(err.Error(), "must destroy its pixels") {
		t.Errorf("the refusal does not say why it matters: %q", err)
	}
}

// ⛔ A dictionary that disagrees with the picture inside it is refused rather
// than averaged: every region is mapped into the samples using the
// DICTIONARY's size, so blanking against the other one would destroy the wrong
// rows.
func TestAJPEGWhoseDictionaryDisagreesWithIt(t *testing.T) {
	const w, h = 32, 32
	in := jpegPage(t, bandedJPEG(t, w, h, 4, false), w, h, "DeviceRGB", wholePage, func(d reader.Dict) {
		d["Height"] = reader.Integer(h * 2)
	})
	d, err := Open(in)
	if err != nil {
		t.Fatal(err)
	}
	err = d.Redact(bottomQuarter)
	if err == nil {
		t.Fatal("a dictionary that disagrees with its picture was accepted")
	}
	if !contains(err.Error(), "dictionary says 32 by 64 and the JPEG in it is 32 by 32") {
		t.Errorf("the refusal says %q", err)
	}
}

// ⛔ A CMYK JPEG is refused rather than converted: there is no faithful CMYK to
// RGB without the output profile the file does not carry, so a conversion here
// would change every colour on the page in order to destroy a corner of it.
//
// The seam is why this can be tested at all — image/jpeg writes Gray, YCbCr and
// RGBA and has no way to write a CMYK JPEG, so without it the refusal would be
// a claim no test could reach.
func TestACMYKJPEGIsRefusedRatherThanConverted(t *testing.T) {
	const w, h = 32, 32
	was := decodeJPEG
	t.Cleanup(func() { decodeJPEG = was })
	decodeJPEG = func(io.Reader) (image.Image, error) {
		return image.NewCMYK(image.Rect(0, 0, w, h)), nil
	}
	d, err := Open(jpegPage(t, bandedJPEG(t, w, h, 4, false), w, h, "DeviceRGB", wholePage, nil))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Redact(bottomQuarter)
	if err == nil {
		t.Fatal("a CMYK JPEG was accepted")
	}
	if !contains(err.Error(), "it is a CMYK JPEG") {
		t.Errorf("the refusal says %q", err)
	}
}

// TestADecoderThatRefusesAfterTheHeader is the other half of that call, reached
// by a picture no fixture of ours can be: a decoder that reads the header and
// then gives up.
func TestADecoderThatRefusesAfterTheHeader(t *testing.T) {
	const w, h = 32, 32
	was := decodeJPEG
	t.Cleanup(func() { decodeJPEG = was })
	decodeJPEG = func(io.Reader) (image.Image, error) {
		return nil, errors.New("the scan ended early")
	}
	d, err := Open(jpegPage(t, bandedJPEG(t, w, h, 4, false), w, h, "DeviceRGB", wholePage, nil))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Redact(bottomQuarter)
	if err == nil || !contains(err.Error(), "the scan ended early") {
		t.Errorf("the refusal is %v", err)
	}
}
