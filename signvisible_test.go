package ops

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-gfx/gfx/codec"
	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

// ---- fixtures ----

func solidPNG(t testing.TB, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func solidJPEG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0, 0, 200, 255
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// blankPage is a 200 by 200 page with nothing on it and no form.
func sigBlank(t testing.TB) []byte { return formFile(t, false, nilForm) }

// inkAround counts the pixels of a page that are not paper, inside a box and
// outside it. The box is widened by a pixel each way, because an edge is
// anti-aliased and the question is whether anything is drawn AWAY from the box.
// The page is 200 points high and rendered at one pixel to the point.
func inkAround(t *testing.T, pdf []byte, sigBox [4]float64) (in, out int) {
	t.Helper()
	img := renderOf(t, pdf)
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			i := (y*img.W + x) * 4
			if blank(img.Pix[i], img.Pix[i+1], img.Pix[i+2]) {
				continue
			}
			px, py := float64(x), float64(img.H-1-y)
			if px >= sigBox[0]-1 && px <= sigBox[2]+1 && py >= sigBox[1]-1 && py <= sigBox[3]+1 {
				in++
			} else {
				out++
			}
		}
	}
	return in, out
}

func visibleOpts(t testing.TB, a SignAppearance) SignOptions {
	o := opts(t, "ecdsa")
	o.Appearance = &a
	return o
}

// widgetOf reads back the first widget of a field of a signed file.
func widgetOf(t *testing.T, b []byte, name string) reader.Dict {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	form, ok := forms.Read(d)
	if !ok {
		t.Fatal("the signed file has no form")
	}
	f, ok := form.Field(name)
	if !ok || len(f.Widgets) == 0 {
		t.Fatalf("no widget for %q", name)
	}
	return f.Widgets[0].Dict()
}

var sigBox = [4]float64{20, 20, 120, 60}

// ---- the drawing ----

// TestAVisibleSignatureIsDrawn is the oracle: go-pdfkit/render draws an
// annotation only from its /AP, so ink inside the box and none away from it
// says the appearance is there and is where it was asked to be; the same file
// signed with no Appearance draws nothing at all.
func TestAVisibleSignatureIsDrawn(t *testing.T) {
	red := SignAppearance{Rect: sigBox, Text: "Approved\nby the board", Color: [3]float64{1, 0, 0}}
	for name, a := range map[string]SignAppearance{
		"default text":    {Rect: sigBox},
		"text":            {Rect: sigBox, Text: "Approved by the board of directors"},
		"red text":        red,
		"fixed size":      {Rect: sigBox, Text: "Hello", FontSize: 14},
		"png":             {Rect: sigBox, Image: solidPNG(t, 40, 20, color.NRGBA{200, 0, 0, 255})},
		"png, tall":       {Rect: sigBox, Image: solidPNG(t, 10, 40, color.NRGBA{0, 120, 0, 255})},
		"translucent png": {Rect: sigBox, Image: solidPNG(t, 30, 30, color.NRGBA{0, 0, 200, 128})},
		"jpeg":            {Rect: sigBox, Image: solidJPEG(t, 40, 20)},
		"png and text":    {Rect: sigBox, Image: solidPNG(t, 40, 20, color.NRGBA{200, 200, 0, 255}), Text: "Seen"},
	} {
		t.Run(name, func(t *testing.T) {
			src := sigBlank(t)
			out, err := Sign(src, visibleOpts(t, a))
			if err != nil {
				t.Fatal(err)
			}
			ours(t, out, 1)
			if !bytes.HasPrefix(out, src) {
				t.Error("the original is not the start of the signed file")
			}
			in, away := inkAround(t, out, sigBox)
			if in < 100 {
				t.Errorf("%d pixels of ink in the box, want at least 100", in)
			}
			if away != 0 {
				t.Errorf("%d pixels of ink away from the box", away)
			}
			wd := widgetOf(t, out, "Signature1")
			if f, _ := reader.ToInt(wd["F"]); f != 4 {
				t.Errorf("/F is %v, want 4 (Print)", wd["F"])
			}
			// The control: the same file, the same options, no Appearance.
			o := visibleOpts(t, a)
			o.Appearance = nil
			plain, err := Sign(src, o)
			if err != nil {
				t.Fatal(err)
			}
			if in, away := inkAround(t, plain, sigBox); in+away != 0 {
				t.Errorf("the control, signed with no Appearance, draws %d pixels", in+away)
			}
		})
	}
}

func TestTheTextIsInTheColourAskedFor(t *testing.T) {
	out, err := Sign(sigBlank(t), visibleOpts(t, SignAppearance{Rect: sigBox, Text: "RED", FontSize: 30,
		Color: [3]float64{1, 0, 0}}))
	if err != nil {
		t.Fatal(err)
	}
	img := renderOf(t, out)
	reds := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 200 && img.Pix[i+1] < 60 && img.Pix[i+2] < 60 {
			reds++
		}
	}
	if reds < 20 {
		t.Errorf("%d red pixels", reds)
	}
	// And the zero value is black.
	out, err = Sign(sigBlank(t), visibleOpts(t, SignAppearance{Rect: sigBox, Text: "BLK", FontSize: 30}))
	if err != nil {
		t.Fatal(err)
	}
	img = renderOf(t, out)
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != img.Pix[i+1] || img.Pix[i+1] != img.Pix[i+2] {
			t.Fatal("black text drew a coloured pixel")
		}
	}
}

func TestTheAppearanceIsInTheUpdateAndThePictureKeepsItsAspect(t *testing.T) {
	src := sigBlank(t)
	out, err := Sign(src, visibleOpts(t, SignAppearance{Rect: sigBox, Image: solidPNG(t, 10, 40, color.NRGBA{0, 0, 0, 255})}))
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	wd := widgetOf(t, out, "Signature1")
	ap, _ := reader.ToDict(wd["AP"])
	obj, err := d.Get(ap["N"].(reader.Ref))
	if err != nil {
		t.Fatal(err)
	}
	st, isStream := obj.(*reader.Stream)
	if !isStream {
		t.Fatalf("/AP /N is %T", obj)
	}
	if bb := reader.FormatObject(st.Dict["BBox"]); string(bb) != "[0 0 100 40]" {
		t.Errorf("BBox %s", bb)
	}
	body, _, err := d.DecodeStream(st)
	if err != nil {
		t.Fatal(err)
	}
	// 10 by 40 in a 100 by 40 box: 10 wide, 40 high, centred at x 45.
	if !strings.Contains(string(body), "10 0 0 40 45 0 cm") {
		t.Errorf("the picture is not fitted and centred:\n%s", body)
	}
	// Every object the appearance is made of is after the original file, so it
	// is in the update, and after the signature's gap, so it is signed.
	gapEnd := bytes.Index(out, []byte("/Contents<"))
	gapEnd += bytes.IndexByte(out[gapEnd:], '>') + 1
	if gapEnd <= len(src) {
		t.Fatalf("the signature's gap ends at %d, inside the original %d bytes", gapEnd, len(src))
	}
	for _, marker := range []string{"/Image", "/Form", "/PdfopsSigIm"} {
		at := bytes.LastIndex(out, []byte(marker))
		if at < gapEnd {
			t.Errorf("%s is at %d, not after the end of the signature's gap at %d", marker, at, gapEnd)
		}
	}
}

// TestAnAppearanceAlteredAfterSigningBreaksTheSignature: the appearance is in
// the update the signature covers.
func TestAnAppearanceAlteredAfterSigningBreaksTheSignature(t *testing.T) {
	out, err := Sign(sigBlank(t), visibleOpts(t, SignAppearance{Rect: sigBox}))
	if err != nil {
		t.Fatal(err)
	}
	ours(t, out, 1)
	tampered := bytes.Replace(out, []byte("Digitally signed"), []byte("Digitally Signed"), 1)
	if bytes.Equal(tampered, out) {
		t.Fatal("the text is not in the file in the clear")
	}
	d, err := reader.Open(tampered)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := signature.VerifyAll(d)
	if err != nil || len(rs) != 1 {
		t.Fatalf("%v %d", err, len(rs))
	}
	if rs[0].Err == nil {
		t.Error("an altered appearance still verifies")
	}
}

func TestTheDefaultTextNamesTheSignerAndTheTime(t *testing.T) {
	// Wide and small, so that no line is wrapped into fragments.
	o := visibleOpts(t, SignAppearance{Rect: [4]float64{0, 0, 200, 60}, FontSize: 6})
	o.Time = time.Date(2026, 11, 20, 21, 0, 0, 0, time.FixedZone("CET", 3600))
	out, err := Sign(sigBlank(t), o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Digitally signed by go-pdfkit/ops test signer ecdsa", "2026-11-20T20:00:00Z"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("%q is not in the appearance", want)
		}
	}
	// A certificate with no common name falls back on its whole subject.
	_, signer, _ := ed25519.GenerateKey(rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{Organization: []string{"Acme"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	o.Signer, o.Certificate = signer, cert
	if out, err = Sign(sigBlank(t), o); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("Digitally signed by O=Acme")) {
		t.Error("no subject fallback")
	}
	// Text of one's own replaces it, and an image alone has none.
	for _, a := range []SignAppearance{
		{Rect: sigBox, Text: "mine"},
		{Rect: sigBox, Image: solidPNG(t, 4, 4, color.NRGBA{0, 0, 0, 255})},
	} {
		if out, err = Sign(sigBlank(t), visibleOpts(t, a)); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out, []byte("Digitally signed")) {
			t.Errorf("%+v: the default text is there", a.Text)
		}
	}
}

func TestTheFittedSizeShrinksToTheBox(t *testing.T) {
	if got := fitSize("Hi", 100, 40); got != fitMax {
		t.Errorf("a short word in a roomy box: %g, want %g", got, float64(fitMax))
	}
	long := strings.Repeat("word ", 40)
	mid := fitSize(long, 100, 40)
	if mid <= fitMin || mid >= fitMax {
		t.Errorf("a paragraph in a small box: %g", mid)
	}
	// A word that is wider than the box at every size, and a box that holds no
	// line at any.
	if got := fitSize(strings.Repeat("W", 100), 20, 40); got != fitMin {
		t.Errorf("an unbreakable word: %g", got)
	}
	if got := fitSize("a\nb\nc\nd\ne\nf", 100, 3); got != fitMin {
		t.Errorf("a box too short for any line: %g", got)
	}
}

// ---- existing fields ----

func TestAnExistingFieldIsSignedInItsOwnBox(t *testing.T) {
	for _, packed := range []bool{false, true} {
		src := sigFieldFile(t, packed)
		o := opts(t, "ecdsa")
		o.Field = "approval"
		o.Appearance = &SignAppearance{}
		out, err := Sign(src, o)
		if err != nil {
			t.Fatal(err)
		}
		ours(t, out, 1)
		if in, away := inkAround(t, out, [4]float64{20, 20, 120, 60}); in < 100 || away != 0 {
			t.Errorf("packed=%v: %d in the field's own box, %d away", packed, in, away)
		}
		// A box of one's own wins over the field's.
		o.Appearance = &SignAppearance{Rect: [4]float64{120, 120, 190, 160}}
		out, err = Sign(src, o)
		if err != nil {
			t.Fatal(err)
		}
		ours(t, out, 1)
		if in, away := inkAround(t, out, [4]float64{120, 120, 190, 160}); in < 100 || away != 0 {
			t.Errorf("packed=%v: %d in the given box, %d away", packed, in, away)
		}
	}
}

// sigWithWidget builds a form whose signature field has a separate widget.
func sigWithWidget(t *testing.T, rect reader.Object, inline bool) []byte {
	return formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		wd := reader.Dict{"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"), "P": page}
		if rect != nil {
			wd["Rect"] = rect
		}
		field := reader.Dict{"FT": reader.Name("Sig"), "T": reader.String("approval")}
		if inline {
			field["Kids"] = reader.Array{wd}
			return reader.Dict{"Fields": reader.Array{w.Add(field)}}, nil
		}
		fref := w.Reserve()
		wd["Parent"] = fref
		wref := w.Add(wd)
		field["Kids"] = reader.Array{wref}
		w.Put(fref, field)
		return reader.Dict{"Fields": reader.Array{fref}}, reader.Array{wref}
	})
}

func TestAFieldWhoseWidgetIsAnotherObject(t *testing.T) {
	r := reader.Array{reader.Integer(120), reader.Integer(60), reader.Integer(20), reader.Integer(20)}
	src := sigWithWidget(t, r, false)
	o := opts(t, "ecdsa")
	o.Field = "approval"
	o.Appearance = &SignAppearance{}
	out, err := Sign(src, o)
	if err != nil {
		t.Fatal(err)
	}
	ours(t, out, 1)
	// The rectangle was written the other way round, and is the same box.
	if in, away := inkAround(t, out, [4]float64{20, 20, 120, 60}); in < 100 || away != 0 {
		t.Errorf("%d in the box, %d away", in, away)
	}
	if _, err := Sign(sigWithWidget(t, r, true), o); err == nil || !strings.Contains(err.Error(), "inside another object") {
		t.Errorf("a widget written inside its field: %v", err)
	}
}

func TestAFieldWithNoUsableRect(t *testing.T) {
	o := opts(t, "ecdsa")
	o.Field = "approval"
	zero := reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(0), reader.Integer(0)}
	flat := reader.Array{reader.Integer(5), reader.Integer(5), reader.Integer(50), reader.Integer(5)}
	for name, src := range map[string][]byte{
		"no /Rect":   sigWithWidget(t, nil, false),
		"zero /Rect": sigWithWidget(t, zero, false),
		"flat /Rect": sigWithWidget(t, flat, false),
		"no widget": formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
			return reader.Dict{"Fields": reader.Array{w.Add(reader.Dict{"FT": reader.Name("Sig"), "T": reader.String("approval")})}}, nil
		}),
	} {
		o.Appearance = &SignAppearance{}
		if _, err := Sign(src, o); !errors.Is(err, ErrNoFieldRect) {
			t.Errorf("%s, no Rect given: %v", name, err)
		}
		if name == "no widget" {
			continue
		}
		o.Appearance = &SignAppearance{Rect: sigBox}
		out, err := Sign(src, o)
		if err != nil {
			t.Errorf("%s, a Rect given: %v", name, err)
			continue
		}
		ours(t, out, 1)
	}
	// An invisible signature in such a field is as it ever was.
	o.Appearance = nil
	if _, err := Sign(sigWithWidget(t, nil, false), o); err != nil {
		t.Error(err)
	}
}

func TestAVisibleSignatureOnAnotherPage(t *testing.T) {
	src := simple(t, 2)
	o := visibleOpts(t, SignAppearance{Rect: [4]float64{10, 10, 90, 40}})
	o.Page = 2
	out, err := Sign(src, o)
	if err != nil {
		t.Fatal(err)
	}
	ours(t, out, 1)
	d, _ := reader.Open(out)
	form, _ := forms.Read(d)
	f, _ := form.Field("Signature1")
	if f.Widgets[0].Page != 2 {
		t.Errorf("the widget is on page %d", f.Widgets[0].Page)
	}
}

// ---- refusals, each tested on both sides ----

func TestAnAppearanceThatCannotBeDrawnIsRefused(t *testing.T) {
	png10 := solidPNG(t, 10, 10, color.NRGBA{0, 0, 0, 255})
	nan, inf := math.NaN(), math.Inf(1)
	cases := []struct {
		name      string
		bad, good SignAppearance
		want      error
	}{
		{"nan in the box", SignAppearance{Rect: [4]float64{nan, 0, 10, 10}}, SignAppearance{Rect: [4]float64{0, 0, 10, 10}}, ErrAppearanceRect},
		{"infinity in the box", SignAppearance{Rect: [4]float64{0, 0, inf, 10}}, SignAppearance{Rect: [4]float64{0, 0, 10, 10}}, ErrAppearanceRect},
		{"inverted in x", SignAppearance{Rect: [4]float64{10, 0, 0, 10}}, SignAppearance{Rect: [4]float64{0, 0, 10, 10}}, ErrAppearanceRect},
		{"inverted in y", SignAppearance{Rect: [4]float64{0, 10, 10, 0}}, SignAppearance{Rect: [4]float64{0, 0, 10, 10}}, ErrAppearanceRect},
		{"no width", SignAppearance{Rect: [4]float64{5, 0, 5, 10}}, SignAppearance{Rect: [4]float64{5, 0, 6, 10}}, ErrAppearanceRect},
		{"no height", SignAppearance{Rect: [4]float64{0, 5, 10, 5}}, SignAppearance{Rect: [4]float64{0, 5, 10, 6}}, ErrAppearanceRect},
		{"no box at all", SignAppearance{}, SignAppearance{Rect: [4]float64{0, 0, 10, 10}}, ErrAppearanceRect},
		{"a box whose size overflows", SignAppearance{Rect: [4]float64{-1.7e308, 0, 1.7e308, 10}}, SignAppearance{Rect: [4]float64{0, 0, 10, 10}}, ErrAppearanceRect},
		{"far to the right", SignAppearance{Rect: [4]float64{231, 0, 251, 20}}, SignAppearance{Rect: [4]float64{220, 0, 240, 20}}, ErrAppearanceOffPage},
		{"far to the left", SignAppearance{Rect: [4]float64{-41, 0, -21, 20}}, SignAppearance{Rect: [4]float64{-40, 0, -20, 20}}, ErrAppearanceOffPage},
		{"far above", SignAppearance{Rect: [4]float64{0, 221, 20, 241}}, SignAppearance{Rect: [4]float64{0, 220, 20, 240}}, ErrAppearanceOffPage},
		{"far below", SignAppearance{Rect: [4]float64{0, -41, 20, -21}}, SignAppearance{Rect: [4]float64{0, -40, 20, -20}}, ErrAppearanceOffPage},
		{"an image that is not one", SignAppearance{Rect: sigBox, Image: []byte("not a picture")}, SignAppearance{Rect: sigBox, Image: png10}, ErrAppearanceImage},
		{"a damaged image", SignAppearance{Rect: sigBox, Image: png10[:len(png10)/2]}, SignAppearance{Rect: sigBox, Image: png10}, ErrAppearanceImage},
		{"an image over the pixel ceiling", SignAppearance{Rect: sigBox, Image: png10, MaxPixels: 99}, SignAppearance{Rect: sigBox, Image: png10, MaxPixels: 100}, ErrAppearanceImage},
		{"text over its bound", SignAppearance{Rect: sigBox, Text: strings.Repeat("a", maxAppearanceText+1)}, SignAppearance{Rect: sigBox, Text: strings.Repeat("a", maxAppearanceText)}, ErrAppearanceText},
		{"a font size that is nan", SignAppearance{Rect: sigBox, FontSize: nan}, SignAppearance{Rect: sigBox, FontSize: 9}, ErrAppearanceFontSize},
		{"a font size that is infinite", SignAppearance{Rect: sigBox, FontSize: inf}, SignAppearance{Rect: sigBox, FontSize: 9}, ErrAppearanceFontSize},
		{"a negative font size", SignAppearance{Rect: sigBox, FontSize: -1}, SignAppearance{Rect: sigBox, FontSize: 0}, ErrAppearanceFontSize},
		{"a colour that is nan", SignAppearance{Rect: sigBox, Color: [3]float64{0, nan, 0}}, SignAppearance{Rect: sigBox, Color: [3]float64{0, 1, 0}}, ErrAppearanceColor},
		{"a colour that is infinite", SignAppearance{Rect: sigBox, Color: [3]float64{inf, 0, 0}}, SignAppearance{Rect: sigBox, Color: [3]float64{1, 0, 0}}, ErrAppearanceColor},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := sigBlank(t)
			if out, err := signOrFail(t, src, visibleOpts(t, c.bad)); !errors.Is(err, c.want) {
				t.Errorf("refused with %v, want %v", err, c.want)
			} else if out != nil {
				t.Error("a refusal returned a file")
			}
			out, err := signOrFail(t, src, visibleOpts(t, c.good))
			if err != nil {
				t.Fatalf("the good side: %v", err)
			}
			ours(t, out, 1)
		})
	}
	// A picture over the ceiling says why, too.
	_, err := Sign(sigBlank(t), visibleOpts(t, SignAppearance{Rect: sigBox, Image: png10, MaxPixels: 50}))
	if !errors.Is(err, codec.ErrTooLarge) {
		t.Errorf("not codec.ErrTooLarge: %v", err)
	}
}

func TestAPageWithAMediaBoxOfItsOwn(t *testing.T) {
	// Each page below says its /MediaBox some way the check has to read, or
	// cannot, and falls back on A4: so a box at 500,700 is on every one.
	onA4 := SignAppearance{Rect: [4]float64{500, 700, 560, 740}}
	for name, mb := range map[string]reader.Object{
		"reversed corners":    reader.Array{reader.Integer(600), reader.Integer(800), reader.Integer(0), reader.Integer(0)},
		"not an array":        reader.Integer(1),
		"three numbers":       reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(600)},
		"a name in the array": reader.Array{reader.Integer(0), reader.Integer(0), reader.Name("x"), reader.Integer(800)},
	} {
		src := buildPDF(t, 1, func(_ int, d reader.Dict) { d["MediaBox"] = mb })
		if _, err := Sign(src, visibleOpts(t, onA4)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A page that is 100 by 200 (the fixture's inherited box) is not A4.
	src := simple(t, 1)
	if _, err := Sign(src, visibleOpts(t, SignAppearance{Rect: [4]float64{500, 700, 520, 720}})); !errors.Is(err, ErrAppearanceOffPage) {
		t.Errorf("a box off the inherited MediaBox: %v", err)
	}
	if _, err := Sign(src, visibleOpts(t, SignAppearance{Rect: [4]float64{90, 190, 110, 210}})); err != nil {
		t.Errorf("a box hanging off the inherited MediaBox: %v", err)
	}
	// A page the options do not name, on an existing field, is not judged.
	o := visibleOpts(t, SignAppearance{Rect: sigBox})
	o.Field, o.Page = "approval", 9
	if _, err := Sign(sigFieldFile(t, false), o); err != nil {
		t.Error(err)
	}
}

// ---- the other oracles ----

// TestVisibleSignaturesAgreeWithTheOracles: poppler's pdfsig calls the
// signature valid and the whole document signed, and qpdf --check adds no
// warning, whichever way the file keeps its cross-reference section.
func TestVisibleSignaturesAgreeWithTheOracles(t *testing.T) {
	pdfsig := tool(t, "pdfsig", "PDFSIG_REQUIRE")
	qpdf := tool(t, "qpdf", "REQUIRE_QPDF")
	dir := t.TempDir()
	img := solidPNG(t, 40, 20, color.NRGBA{200, 0, 0, 255})
	for _, in := range inputs(t) {
		for name, a := range map[string]SignAppearance{
			"default": {Rect: sigBox},
			"image":   {Rect: sigBox, Image: img, Text: "Seen"},
		} {
			label := in.name + "-" + name
			o := opts(t, "rsa")
			o.Field = in.field
			o.Appearance = &a
			out, err := Sign(in.b, o)
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			ours(t, out, 1)
			p := filepath.Join(dir, label+".pdf")
			if err := os.WriteFile(p, out, 0o644); err != nil {
				t.Fatal(err)
			}
			vs, raw := pdfsigVerdicts(t, pdfsig, p)
			if len(vs) != 1 || !vs[0].valid || !vs[0].whole {
				t.Errorf("%s: pdfsig says %+v\n%s", label, vs, raw)
			}
			if !qpdfAgrees(t, qpdf, dir, label, in.b, out) {
				t.Errorf("%s: qpdf --check disagrees", label)
			}
		}
	}
}

// A line exactly as wide as the box fits it; one a hair narrower does not.
func TestAFittedLineExactlyAsWideAsTheBoxFits(t *testing.T) {
	w := Helvetica.Width("Signed", fitMax)
	if got := fitSize("Signed", w, 1000); got != fitMax {
		t.Errorf("a box exactly as wide as the text: %v, want %v", got, fitMax)
	}
	if got := fitSize("Signed", w-0.01, 1000); got >= fitMax {
		t.Errorf("a box a hair too narrow: %v", got)
	}
}
