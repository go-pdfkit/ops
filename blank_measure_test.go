package ops

import (
	"testing"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

func renderOne(t *testing.T, text string, size, dpi float64) (int, int, float64) {
	t.Helper()
	d := New()
	d.Blank(595, 842) // A4, so the figures are the ones a real page gives
	if text != "" {
		if err := d.Stamp("all", Stamp{Text: text, Font: Helvetica, Size: size}); err != nil {
			t.Fatal(err)
		}
	}
	pdf, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	img, err := render.Page(src, 1, render.Options{DPI: dpi})
	if err != nil {
		t.Fatal(err)
	}
	return img.W, img.H, inkFraction(img, defaultTolerance)
}

// TestMeasureTheInkLadder prints what a page of each kind actually measures,
// at each resolution worth considering. It is how the defaults were chosen.
func TestMeasureTheInkLadder(t *testing.T) {
	for _, dpi := range []float64{50, 100, 150, 300} {
		w, h, _ := renderOne(t, "", 0, dpi)
		onePixel := 1 / float64(w*h)
		t.Logf("--- %g DPI: %d x %d, one pixel = %.3e of the page", dpi, w, h, onePixel)
		for _, r := range []struct {
			name string
			text string
			size float64
		}{
			{"nothing", "", 0},
			{"a full stop, 6pt", ".", 6},
			{"a full stop, 8pt", ".", 8},
			{"a comma, 8pt", ",", 8},
			{"one word, 12pt", "word", 12},
		} {
			_, _, ink := renderOne(t, r.text, r.size, dpi)
			px := ink / onePixel
			t.Logf("    %-18s ink=%.3e  (%.0f pixels)", r.name, ink, px)
		}
	}
}

// TestMeasureAFaintMark says what a nearly-white mark actually measures, which
// is what the Tolerance default is for. A mutation that set the default
// tolerance to zero SURVIVED the first version of this suite: every fixture
// rendered to exactly white, so the field was inert as far as any test could
// see, and the doc comment's claim was unproven.
func TestMeasureAFaintMark(t *testing.T) {
	for _, grey := range []float64{1.0, 0.99, 0.97, 0.95, 0.9} {
		d := New()
		d.Blank(595, 842)
		if err := d.Stamp("all", Stamp{
			Text: "faint", Font: Helvetica, Size: 48,
			Colour: [3]float64{grey, grey, grey},
		}); err != nil {
			t.Fatal(err)
		}
		pdf, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		src, err := reader.Open(pdf)
		if err != nil {
			t.Fatal(err)
		}
		img, err := render.Page(src, 1, render.Options{DPI: defaultBlankDPI})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("grey %.2f (%3.0f/255): ink at tol 0 = %.3e, at tol 12 = %.3e",
			grey, grey*255, inkFraction(img, 0), inkFraction(img, defaultTolerance))
	}
}
