package ops

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
)

// secretPage is a page saying two things, one of which is to be removed: a
// line at y 150 to keep and a line at y 100 to redact.
func secretPage(t *testing.T, body string, extra func(w *reader.Writer, res reader.Dict)) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	widths := make(reader.Array, 0, 224)
	for i := 32; i < 256; i++ {
		widths = append(widths, reader.Integer(500))
	}
	res := reader.Dict{"Font": reader.Dict{"F1": w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
		"BaseFont": reader.Name("Helvetica"), "FirstChar": reader.Integer(32),
		"LastChar": reader.Integer(255), "Widths": widths,
		"Encoding": reader.Name("WinAnsiEncoding"),
	})}}
	page := reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(body)}),
		"Resources": res,
	}
	if extra != nil {
		extra(w, res)
		page["Resources"] = res
	}
	added := w.Add(page)
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{added}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// twoLines is the ordinary fixture: one line to keep, one to remove.
const twoLines = "BT /F1 10 Tf 20 150 Td (public) Tj ET BT /F1 10 Tf 20 100 Td (secret) Tj ET"

// overSecret is the region the second line occupies, with paper around it.
func overSecret() Redaction {
	return Redaction{Page: 1, Quads: []Quad{QuadOf(15, 95, 85, 115)}}
}

// redacted applies a redaction to the fixture and returns the file.
func redacted(t *testing.T, body string, r Redaction) []byte {
	t.Helper()
	d, err := Open(secretPage(t, body, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(r); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ⛔⛔ THE ONLY TEST THAT DECIDES WHETHER THIS IS A REDACTION. ISO 32000-1
// §12.5.6.23 requires a conforming reader to "remove all traces of the
// specified content". A black rectangle drawn over a paragraph passes every
// test about how the page LOOKS and fails this one, because the text is still
// in the content stream for anyone's copy and paste to read back.
func TestTheRedactedStringIsGoneFromTheBytes(t *testing.T) {
	out := redacted(t, twoLines, overSecret())
	for i, content := range contentsOf(t, out) {
		if strings.Contains(content, "secret") {
			t.Errorf("page %d still holds the redacted string: %q", i+1, content)
		}
		if !strings.Contains(content, "public") {
			t.Errorf("page %d lost the text that was to be kept: %q", i+1, content)
		}
	}
	// And the document read back the way a reader would read it says nothing
	// of it either.
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	text, err := extract.Text(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "secret") {
		t.Errorf("the page still reads back as %q", text)
	}
	if !strings.Contains(text, "public") {
		t.Errorf("the page no longer says what it was to keep: %q", text)
	}
}

// ⛔ AND WHAT WAS KEPT DID NOT MOVE. Blanking an operator must not shift the
// text that stays — that is the difference between a redaction and a
// re-typesetting, and it is why the whole operation is overwritten with spaces
// of its own length rather than deleted.
func TestWhatWasKeptDidNotMove(t *testing.T) {
	before, err := reader.Open(secretPage(t, twoLines, nil))
	if err != nil {
		t.Fatal(err)
	}
	was, err := extract.Runs(before, 1)
	if err != nil {
		t.Fatal(err)
	}
	after, err := reader.Open(redacted(t, twoLines, overSecret()))
	if err != nil {
		t.Fatal(err)
	}
	now, err := extract.Runs(after, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(was) != 2 {
		t.Fatalf("the fixture draws %d run(s), want 2", len(was))
	}
	if len(now) != 1 {
		t.Fatalf("the redacted page draws %d run(s), want 1", len(now))
	}
	keep := was[0]
	if now[0].Text != keep.Text {
		t.Fatalf("what is left says %q, want %q", now[0].Text, keep.Text)
	}
	if now[0].X != keep.X || now[0].Y != keep.Y || now[0].Width != keep.Width {
		t.Errorf("the kept line was at (%g,%g) %g wide and is now at (%g,%g) %g wide",
			keep.X, keep.Y, keep.Width, now[0].X, now[0].Y, now[0].Width)
	}
}

// ⛔ THE REGION IS PAINTED OVER, because an area that was emptied and not
// marked cannot be told from one that was always blank — and a reader has to be
// able to see that something was taken out. The paint is a MARKING, not the
// redaction: the text is gone from the stream before it is drawn.
func TestTheRegionIsPaintedOver(t *testing.T) {
	r := overSecret()
	r.R, r.G, r.B = 0, 0, 0
	out := redacted(t, twoLines, r)
	// A PDF counts up from the foot of the page and a raster down from the top:
	// the region's y 95..115 on a 200pt page is rows 85..105.
	if red, green, blue := pixelAt(t, out, 50, 95); blank(red, green, blue) {
		t.Errorf("the middle of the region is bare paper (%d,%d,%d)", red, green, blue)
	}
	// The line that was kept still has its ink, and is not painted over.
	inked := false
	img := renderOf(t, out)
	for y := 40; y < 52; y++ {
		for x := 20; x < 60; x++ {
			i := (y*img.W + x) * 4
			if !blank(img.Pix[i], img.Pix[i+1], img.Pix[i+2]) {
				inked = true
			}
		}
	}
	if !inked {
		t.Error("the line that was kept has no ink left on the page")
	}
}

// ⛔⛔ A REDACTION SURVIVES EVERYTHING ELSE THE DOCUMENT IS ASKED FOR. This is a
// control for a defect the write path already carries a comment about: the
// decoration step used to take the source's content parts whenever it stamped a
// page, which threw away a rewritten stream. For a recolouring that loses a
// colour. For a redaction it would put the removed text back and then draw the
// fill over it, so the document would LOOK redacted and not be — the worst
// possible ending.
func TestARedactionSurvivesAStamp(t *testing.T) {
	d, err := Open(secretPage(t, twoLines, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(overSecret()); err != nil {
		t.Fatal(err)
	}
	if err := d.Watermark("all", "DRAFT"); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range contentsOf(t, out) {
		if strings.Contains(content, "secret") {
			t.Errorf("page %d holds the redacted string after a watermark: %q", i+1, content)
		}
	}
}

// ⛔⛔ IT REFUSES RATHER THAN HALF-REDACTS. Each of these is content a region
// can hold that this release cannot take out, and in every case the caller must
// be told — a caller who is not told believes the document safe to send.
func TestRedactingRefusesWhatItCannotRemove(t *testing.T) {
	// A Form XObject: its bytes are not the page's, and it is shared.
	t.Run("text inside a form", func(t *testing.T) {
		const body = "BT /F1 10 Tf 20 150 Td (public) Tj ET q /X1 Do Q"
		const inner = "BT /F1 10 Tf 20 100 Td (secret) Tj ET"
		pdf := secretPage(t, body, func(w *reader.Writer, res reader.Dict) {
			res["XObject"] = reader.Dict{"X1": w.Add(&reader.Stream{Dict: reader.Dict{
				"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0),
					reader.Integer(100), reader.Integer(200)},
				"Resources": res,
			}, Raw: []byte(inner)})}
		})
		assertRefused(t, pdf, overSecret(), "form XObject")
	})

	// An image: the spec says the pixels must be destroyed, not covered.
	t.Run("an image across the region", func(t *testing.T) {
		const body = "BT /F1 10 Tf 20 150 Td (public) Tj ET q 70 0 0 20 15 95 cm /Im1 Do Q"
		pdf := secretPage(t, body, func(w *reader.Writer, res reader.Dict) {
			res["XObject"] = reader.Dict{"Im1": w.Add(&reader.Stream{Dict: reader.Dict{
				"Type": reader.Name("XObject"), "Subtype": reader.Name("Image"),
				"Width": reader.Integer(2), "Height": reader.Integer(2),
				"ColorSpace": reader.Name("DeviceGray"), "BitsPerComponent": reader.Integer(8),
			}, Raw: []byte{0, 64, 128, 255}})}
		})
		assertRefused(t, pdf, overSecret(), "image")
	})

	// An annotation the file already carries: its own contents can hold the
	// words being redacted.
	t.Run("an annotation across the region", func(t *testing.T) {
		d, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Mark(Markup{Page: 1, Kind: MarkHighlight,
			Quads: []Quad{QuadOf(20, 98, 60, 112)}, Note: "the secret, again"}); err != nil {
			t.Fatal(err)
		}
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		assertRefused(t, out, overSecret(), "annotation")
	})

	// ⛔⛔ AND ONE THIS DOCUMENT HAS ADDED BUT NOT YET WRITTEN. Marks and
	// widgets are held until the write, because each names the page OBJECT it
	// sits on — so they are not in the source at all, and reading only the
	// source missed them. The workbench walked straight into it: highlight a
	// line, then decide to redact it, and the text went while the note that
	// quoted it stayed.
	t.Run("a mark this document has added", func(t *testing.T) {
		d, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Mark(Markup{Page: 1, Kind: MarkHighlight,
			Quads: []Quad{QuadOf(20, 98, 60, 112)}, Note: "the secret, again"}); err != nil {
			t.Fatal(err)
		}
		err = d.Redact(overSecret())
		if err == nil {
			t.Fatal("a redaction under a mark this document had added was accepted")
		}
		if !strings.Contains(err.Error(), "drop the mark first") {
			t.Errorf("the refusal says %q", err)
		}
		// A mark somewhere else on the page is nobody's business.
		d2, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := d2.Mark(Markup{Page: 1, Kind: MarkHighlight,
			Quads: []Quad{QuadOf(20, 148, 60, 162)}}); err != nil {
			t.Fatal(err)
		}
		if err := d2.Redact(overSecret()); err != nil {
			t.Errorf("a mark on another line stopped the redaction: %v", err)
		}
		// And a mark on another PAGE is not even looked at, however exactly it
		// sits over the region's coordinates — a page number is not a position.
		d3, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		d3.Blank(100, 200)
		if err := d3.Mark(Markup{Page: 2, Kind: MarkHighlight,
			Quads: []Quad{QuadOf(15, 95, 85, 115)}}); err != nil {
			t.Fatal(err)
		}
		if err := d3.Redact(overSecret()); err != nil {
			t.Errorf("a mark on page 2 stopped a redaction on page 1: %v", err)
		}
	})

	t.Run("a field this document has added", func(t *testing.T) {
		d, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.AddFields(forms.Field{Name: "note", Kind: forms.Text,
			Widgets: []forms.Widget{{Page: 1, Rect: [4]float64{20, 98, 60, 112}}}}); err != nil {
			t.Fatal(err)
		}
		err = d.Redact(overSecret())
		if err == nil {
			t.Fatal("a redaction under a field this document had added was accepted")
		}
		if !strings.Contains(err.Error(), "drop the field first") {
			t.Errorf("the refusal says %q", err)
		}
		// A widget on another page is not in the way, wherever it sits.
		d2, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		d2.Blank(100, 200)
		if err := d2.AddFields(forms.Field{Name: "note", Kind: forms.Text,
			Widgets: []forms.Widget{{Page: 2, Rect: [4]float64{20, 98, 60, 112}}}}); err != nil {
			t.Fatal(err)
		}
		if err := d2.Redact(overSecret()); err != nil {
			t.Errorf("a field on page 2 stopped a redaction on page 1: %v", err)
		}
	})

	// One operator drawing both what goes and what stays.
	t.Run("an operator it would half-erase", func(t *testing.T) {
		const body = "BT /F1 10 Tf 20 100 Td [(keep) -2000 (drop)] TJ ET"
		// ⛔ The region covers the second word WHOLLY and the first not at all:
		// "keep" is drawn at x 20..40, the kern moves the pen 20 points, and
		// "drop" is drawn at x 60..80. A quad that clipped "drop" instead would
		// be refused for reaching outside the region — a different rule, which
		// is what this fixture said until it was measured.
		r := Redaction{Page: 1, Quads: []Quad{QuadOf(58, 95, 85, 115)}}
		assertRefused(t, secretPage(t, body, nil), r, "would remove both")
	})

	// ⛔⛔ AND A RUN THAT REACHES OUTSIDE THE REGION. A producer that draws a
	// whole line with one Tj makes that line the run, so redacting one word of
	// it would take the line. Not a leak — the other betrayal, where more goes
	// than was asked and nothing says so.
	t.Run("text that reaches outside the region", func(t *testing.T) {
		const body = "BT /F1 10 Tf 20 100 Td (a whole line in one operation) Tj ET"
		// A region over the first few letters only.
		r := Redaction{Page: 1, Quads: []Quad{QuadOf(18, 97, 40, 113)}}
		assertRefused(t, secretPage(t, body, nil), r, "reaches outside the region")
	})
}

// ⛔ AND THE SLACK IS ENOUGH FOR A REGION THAT WAS MEASURED RATHER THAN TYPED.
// The workbench maps a lit rectangle back into page space through screen
// pixels, so its quad is a fraction of a point out in each direction. A region
// that misses the run's own box by less than that must still redact it,
// otherwise the one caller this was built for is refused every time.
func TestARegionOffByLessThanAPointStillRedacts(t *testing.T) {
	// The run is at x 20..50, y 100..110. This quad is inside that on every
	// side by a point and a half — a measured region, not a typed one.
	r := Redaction{Page: 1, Quads: []Quad{QuadOf(21.5, 101.5, 48.5, 108.5)}}
	d, err := Open(secretPage(t, "BT /F1 10 Tf 20 100 Td (secret) Tj ET", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(r); err != nil {
		t.Fatalf("a region measured off by a point was refused: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range contentsOf(t, out) {
		if strings.Contains(content, "secret") {
			t.Errorf("page %d still holds the redacted string", i+1)
		}
	}
	// And a region genuinely short of the run — by more than the slack — is
	// refused rather than quietly taking the whole run.
	far := Redaction{Page: 1, Quads: []Quad{QuadOf(26, 103, 44, 107)}}
	d2, err := Open(secretPage(t, "BT /F1 10 Tf 20 100 Td (secret) Tj ET", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Redact(far); err == nil {
		t.Error("a region well inside the run's own box took the whole run")
	}
}

// assertRefused checks that Redact says no, and says why.
func assertRefused(t *testing.T, pdf []byte, r Redaction, because string) {
	t.Helper()
	d, err := Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	err = d.Redact(r)
	if err == nil {
		t.Fatalf("the redaction was accepted although it cannot remove the %s", because)
	}
	if !strings.Contains(err.Error(), because) {
		t.Errorf("the refusal says %q, which does not mention %q", err, because)
	}
	// And nothing was recorded, so a caller that ignored the error does not
	// get a document that looks redacted.
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range contentsOf(t, out) {
		if strings.Contains(content, "re f") && strings.Contains(content, "0 0 0 rg") {
			t.Error("a refused redaction still painted its region")
		}
	}
}

// A redaction has to name a page and a region, and a colour that is a colour.
func TestARedactionMustMakeSense(t *testing.T) {
	d, err := Open(secretPage(t, twoLines, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		r    Redaction
		says string
	}{
		{"no such page", Redaction{Page: 2, Quads: []Quad{QuadOf(0, 0, 10, 10)}}, "no page 2"},
		{"page zero", Redaction{Page: 0, Quads: []Quad{QuadOf(0, 0, 10, 10)}}, "no page 0"},
		{"no region", Redaction{Page: 1}, "names no region"},
		{"a colour that is not one", Redaction{Page: 1,
			Quads: []Quad{QuadOf(0, 0, 10, 10)}, R: 2}, "from 0 to 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := d.Redact(c.r)
			if err == nil {
				t.Fatalf("%+v was accepted", c.r)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q, want it to mention %q", err, c.says)
			}
		})
	}
}

// A page this package made rather than borrowed has no text of its own, and
// saying so beats appearing to redact one.
func TestAMadePageHasNoTextToRedact(t *testing.T) {
	d, err := Open(secretPage(t, twoLines, nil))
	if err != nil {
		t.Fatal(err)
	}
	d.Blank(100, 200)
	err = d.Redact(Redaction{Page: 2, Quads: []Quad{QuadOf(0, 0, 10, 10)}})
	if err == nil {
		t.Fatal("a made page accepted a redaction")
	}
	if !strings.Contains(err.Error(), "made rather than read") {
		t.Errorf("the refusal says %q", err)
	}
}

// ⛔⛔ THE REFUSAL THAT MATTERS MOST, AND THE ONE NO FIXTURE CAN STAGE. A run
// whose operation cannot be located would be SKIPPED when the spans to blank
// are collected — left on the page, under the fill, in a document the caller
// was told was redacted. extract gives every run it draws a span today; this is
// what happens the day one of them does not, and the only way to ask is to hand
// the rule such a run directly.
func TestTextWithNoSpanIsRefusedRatherThanSkipped(t *testing.T) {
	region := []Quad{QuadOf(15, 95, 85, 115)}
	inside := extract.Run{Text: "secret", X: 20, Y: 100, Width: 30, Size: 10}
	t.Run("no span", func(t *testing.T) {
		err := unremovableText(1, []extract.Run{inside}, region)
		if err == nil {
			t.Fatal("a run this cannot locate was accepted for redaction")
		}
		if !strings.Contains(err.Error(), "cannot locate") {
			t.Errorf("the refusal says %q", err)
		}
	})
	t.Run("in a form", func(t *testing.T) {
		run := inside
		run.Op, run.InForm = extract.Span{From: 1, To: 9}, true
		err := unremovableText(1, []extract.Run{run}, region)
		if err == nil {
			t.Fatal("a run inside a form was accepted for redaction")
		}
		if !strings.Contains(err.Error(), "form XObject") {
			t.Errorf("the refusal says %q", err)
		}
	})
	// ⛔ And a long secret is not printed in full by the message that refuses
	// it: an error goes to a log, and a redaction's subject is the one string
	// that must not.
	t.Run("a long secret is shortened", func(t *testing.T) {
		run := inside
		run.Text = "a very long secret indeed, every word of it confidential"
		err := unremovableText(1, []extract.Run{run}, region)
		if err == nil {
			t.Fatal("accepted")
		}
		if strings.Contains(err.Error(), "confidential") {
			t.Errorf("the refusal printed the whole secret: %q", err)
		}
		if !strings.Contains(err.Error(), "…") {
			t.Errorf("the refusal did not say it had shortened the text: %q", err)
		}
	})
	// A run outside the region is nobody's business, span or no span.
	t.Run("outside the region", func(t *testing.T) {
		run := inside
		run.Y = 150
		if err := unremovableText(1, []extract.Run{run}, region); err != nil {
			t.Errorf("a run outside the region was refused: %v", err)
		}
	})
}

// ⛔⛔ A REDACTION THAT CANNOT BE APPLIED FAILS THE WRITE. If the page's text
// cannot be read when the document is written, no operator is blanked — and the
// fill is still painted over the region, so the file would look redacted and
// hold every word. The caller gets the failure instead of the file.
func TestARedactionThatCannotBeAppliedFailsTheWrite(t *testing.T) {
	d, err := Open(secretPage(t, twoLines, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(overSecret()); err != nil {
		t.Fatal(err)
	}
	// It was readable when Redact was called, and is not when Bytes is: a
	// document handed between the two is the case this guards.
	was := extractRuns
	extractRuns = func(*reader.Document, int) ([]extract.Run, error) { return nil, errTest }
	t.Cleanup(func() { extractRuns = was })

	out, err := d.Bytes()
	if err == nil {
		t.Fatal("a document whose redaction could not be applied was written anyway")
	}
	if out != nil {
		t.Errorf("%d bytes were handed back with the failure", len(out))
	}
	if !strings.Contains(err.Error(), "redaction cannot be applied") {
		t.Errorf("the failure says %q", err)
	}
	// And the write recovers once the page can be read again.
	extractRuns = was
	if _, err := d.Bytes(); err != nil {
		t.Errorf("the next write failed too: %v", err)
	}
}

// A page that cannot be read is a page that cannot be redacted, and the caller
// hears which reading failed.
func TestAPageThatCannotBeReadIsNotRedacted(t *testing.T) {
	for _, c := range []struct {
		name string
		arm  func(t *testing.T)
		says string
	}{
		{"its text", func(t *testing.T) {
			was := extractRuns
			extractRuns = func(*reader.Document, int) ([]extract.Run, error) {
				return nil, errTest
			}
			t.Cleanup(func() { extractRuns = was })
		}, "could not be read to redact it"},
		{"its images", func(t *testing.T) {
			was := extractImages
			extractImages = func(*reader.Document, int) ([]extract.Image, error) {
				return nil, errTest
			}
			t.Cleanup(func() { extractImages = was })
		}, "images of page 1 could not be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.arm(t)
			d, err := Open(secretPage(t, twoLines, nil))
			if err != nil {
				t.Fatal(err)
			}
			err = d.Redact(overSecret())
			if err == nil {
				t.Fatal("a page that could not be read was redacted anyway")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q, want it to mention %q", err, c.says)
			}
		})
	}
}

// errTest is the failure the readings above are made to report.
var errTest = errors.New("this page is unreadable")

// An entry of Annots that is not an annotation, and one with no rectangle, are
// stepped over rather than taken as covering the region — a document is free
// to hold either, and refusing to redact because of one would be refusing for
// no reason.
func TestNonsenseInAnnotsIsSteppedOver(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	widths := make(reader.Array, 0, 224)
	for i := 32; i < 256; i++ {
		widths = append(widths, reader.Integer(500))
	}
	font := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
		"BaseFont": reader.Name("Helvetica"), "FirstChar": reader.Integer(32),
		"LastChar": reader.Integer(255), "Widths": widths,
		"Encoding": reader.Name("WinAnsiEncoding"),
	})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents":  w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(twoLines)}),
		"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
		"Annots": reader.Array{
			reader.Integer(7), // not an annotation at all
			w.Add(reader.Dict{"Subtype": reader.Name("Text")}), // no Rect
		},
	})
	w.Put(pagesRef, reader.Dict{
		"Type": reader.Name("Pages"), "Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(200)},
	})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	pdf, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(overSecret()); err != nil {
		t.Fatalf("nonsense in Annots stopped the redaction: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range contentsOf(t, out) {
		if strings.Contains(content, "secret") {
			t.Errorf("page %d still holds the redacted string", i+1)
		}
	}
}

// A span outside the stream is ignored rather than panicking: the bytes being
// edited and the bytes the spans were read from are the same page, so this
// cannot happen — and a redaction is the wrong place to find out by crashing.
func TestASpanOutsideTheStreamIsIgnored(t *testing.T) {
	data := []byte("BT (x) Tj ET")
	for _, span := range []extract.Span{
		{From: -1, To: 4},
		{From: 2, To: len(data) + 10},
		{From: 5, To: 5},
		{From: 6, To: 3},
	} {
		out, blanked := redactContent(data, map[extract.Span]bool{span: true})
		if blanked != 0 {
			t.Errorf("%+v blanked %d operation(s)", span, blanked)
		}
		if string(out) != string(data) {
			t.Errorf("%+v changed the stream to %q", span, out)
		}
	}
	// And no spans at all is the stream itself, not a copy of it.
	if out, blanked := redactContent(data, nil); blanked != 0 || string(out) != string(data) {
		t.Errorf("no spans gave %q, %d blanked", out, blanked)
	}
}

// ⛔ A RECOLOURING IS NOT A REDACTION, and an unreadable page is the one place
// the difference shows. A page whose content stream cannot be read is, for a
// recolouring, a page left in its own colours — no worse than a colour, and
// what this did before redaction existed. For a redaction the same page is a
// document that must not be written at all, which is the test above.
func TestAnUnreadablePageIsAColourLostAndARedactionRefused(t *testing.T) {
	arm := func(t *testing.T) {
		was := pageContent
		pageContent = func(*reader.Document, int) ([]byte, error) { return nil, errTest }
		t.Cleanup(func() { pageContent = was })
	}
	t.Run("a recolouring leaves it alone", func(t *testing.T) {
		d, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.RecolourText("all", 1, 0, 0); err != nil {
			t.Fatal(err)
		}
		arm(t)
		out, err := d.Bytes()
		if err != nil {
			t.Fatalf("a page that could not be recoloured failed the write: %v", err)
		}
		// Left alone means left alone: the text is still there, in whatever
		// colour it was.
		found := false
		for _, content := range contentsOf(t, out) {
			if strings.Contains(content, "public") {
				found = true
			}
		}
		if !found {
			t.Error("the page came back empty rather than unchanged")
		}
	})
	t.Run("a redaction is refused", func(t *testing.T) {
		d, err := Open(secretPage(t, twoLines, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Redact(overSecret()); err != nil {
			t.Fatal(err)
		}
		arm(t)
		if _, err := d.Bytes(); err == nil {
			t.Fatal("a page whose stream could not be read was redacted anyway")
		}
	})
}

// A region of no area paints nothing, rather than a rectangle of no width that
// some readers draw as a hairline.
func TestARegionOfNoAreaPaintsNothing(t *testing.T) {
	for _, q := range []Quad{QuadOf(10, 10, 10, 20), QuadOf(10, 10, 20, 10)} {
		if got := redactionFill([]Redaction{{Page: 1, Quads: []Quad{q}}}); got != "" {
			t.Errorf("a quad of no area painted %q", got)
		}
	}
}

// Text inside a form that the region does not reach is left alone: the page is
// redacted, and the form — whose bytes are shared — is untouched.
func TestAFormAwayFromTheRegionIsLeftAlone(t *testing.T) {
	const body = "BT /F1 10 Tf 20 100 Td (secret) Tj ET q /X1 Do Q"
	const inner = "BT /F1 10 Tf 20 20 Td (elsewhere) Tj ET"
	pdf := secretPage(t, body, func(w *reader.Writer, res reader.Dict) {
		res["XObject"] = reader.Dict{"X1": w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(100), reader.Integer(200)},
			"Resources": res,
		}, Raw: []byte(inner)})}
	})
	d, err := Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Redact(overSecret()); err != nil {
		t.Fatalf("a form away from the region was refused: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	text, err := extract.Text(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "secret") {
		t.Errorf("the page still says %q", text)
	}
	if !strings.Contains(text, "elsewhere") {
		t.Errorf("the form's own text was lost: %q", text)
	}
}

// Redacting nothing leaves the page as it was, rather than writing a stream
// that merely looks the same.
func TestARegionWithNoTextInItChangesNoText(t *testing.T) {
	out := redacted(t, twoLines, Redaction{Page: 1, Quads: []Quad{QuadOf(0, 0, 5, 5)}})
	src, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	text, err := extract.Text(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"public", "secret"} {
		if !strings.Contains(text, want) {
			t.Errorf("a redaction over bare paper lost %q: the page reads %q", want, text)
		}
	}
}
