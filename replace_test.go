package ops

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/reader"
)

// twoShowsPage is the fixture the whole file turns on: two text-showing
// operators inside ONE BT/ET, with no Td between them.
//
// ⛔⛔ THE SECOND STRING HAS NO POSITION OF ITS OWN. It starts wherever the pen
// ended after the first, which is what makes this page able to detect the defect
// these tests exist for: replace the first string with one of another width and
// the second MOVES, unless the replacement carries a kern that cancels the
// difference. A fixture that gave each string its own Td would place the second
// absolutely, and every test here would pass whatever the arithmetic did.
func twoShowsPage(t *testing.T, size float64) []byte {
	t.Helper()
	return twoShowsPageBody(t, size, "(Hello) Tj (World) Tj")
}

// twoShowsPageBody is the same page with the showing operators written out by
// the caller, for the tests that need another operator or an escaped string.
func twoShowsPageBody(t *testing.T, size float64, shows string) []byte {
	t.Helper()
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
	body := "BT /F1 " + number(size) + " Tf 20 300 Td " + shows + " ET"
	content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(body)})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
		"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(400), reader.Integer(400)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// onePhraseThen is twoShowsPage with the first string written literally as
// given, so a test can put PDF escapes into the stream. `says` is what that
// string reads back as.
func onePhraseThen(t *testing.T, literal, says string) []byte {
	t.Helper()
	page := twoShowsPageBody(t, 12, "("+literal+") Tj (World) Tj")
	if got := runSaying(t, textRunsOf(t, page), says); got.Text != says {
		t.Fatalf("the fixture reads back as %q", got.Text)
	}
	return page
}

// textRunsOf is a document's runs on page one. Not `runsOf`: fill.go already
// has one, about the runs of a form field's value.
func textRunsOf(t *testing.T, pdf []byte) []extract.Run {
	t.Helper()
	src, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := extract.Runs(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// runSaying is the run whose text is exactly this, failing if there is not one.
func runSaying(t *testing.T, runs []extract.Run, text string) extract.Run {
	t.Helper()
	for _, r := range runs {
		if r.Text == text {
			return r
		}
	}
	var said []string
	for _, r := range runs {
		said = append(said, r.Text)
	}
	t.Fatalf("no run says %q; the page says %q", text, said)
	return extract.Run{}
}

// replacedOnce is the document with one run's text replaced.
func replacedOnce(t *testing.T, pdf []byte, was, now string) []byte {
	t.Helper()
	d, err := Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	run := runSaying(t, textRunsOf(t, pdf), was)
	if err := d.ReplaceText(TextEdit{Page: 1, At: run.Op, Now: now}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ⛔⛔ THE ONE THING THIS VERB MUST NOT DO IS MOVE THE REST OF THE LINE, and the
// oracle is the page read back: the word AFTER the edit must start at the same x
// it started at before. Checking that the new text is in the file would pass for
// an edit that re-flowed everything after it.
func TestReplacingTextLeavesWhatFollowsWhereItWas(t *testing.T) {
	for _, c := range []struct {
		name string
		now  string
	}{
		{"shorter", "Hi"},
		// ⛔ No space in the longer case: the verb refuses a replacement that
		// changes the number of spaces, because word spacing adds a step per
		// space that cannot be read back from the stream. The first draft of
		// this case said "Hello there" and was refused — by the rule, correctly.
		{"longer", "Greetings"},
		{"the same length", "Howdy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := twoShowsPage(t, 12)
			wasX := runSaying(t, textRunsOf(t, before), "World").X
			after := replacedOnce(t, before, "Hello", c.now)
			runs := textRunsOf(t, after)
			// The edit happened.
			runSaying(t, runs, c.now)
			// And what follows did not move. One hundredth of a point is the
			// rounding the kern is written with, not slack.
			nowX := runSaying(t, runs, "World").X
			if diff := nowX - wasX; diff > 0.01 || diff < -0.01 {
				t.Errorf("the next word moved from x=%.3f to x=%.3f (%.3f off)",
					wasX, nowX, diff)
			}
		})
	}
}

// ⛔ AND IT HOLDS AT ANY FONT SIZE. 9.4.4's displacement formula multiplies both
// the glyph width and the TJ number by the font size, so the kern is the
// difference of the raw width sums with no size in it — which is the one part of
// this that is easy to get wrong by a factor and impossible to notice at 12pt,
// because 12 is where a mistaken scale is least visible.
func TestTheKernDoesNotDependOnTheFontSize(t *testing.T) {
	for _, size := range []float64{6, 12, 24, 48} {
		before := twoShowsPage(t, size)
		wasX := runSaying(t, textRunsOf(t, before), "World").X
		after := replacedOnce(t, before, "Hello", "Hi")
		nowX := runSaying(t, textRunsOf(t, after), "World").X
		if diff := nowX - wasX; diff > 0.01 || diff < -0.01 {
			t.Errorf("at %gpt the next word moved from x=%.3f to x=%.3f",
				size, wasX, nowX)
		}
	}
}

// ⛔ The control for the two tests above: WITHOUT the kern the next word really
// does move, by the amount the widths differ. Without this, a verb that somehow
// left the line alone for an unrelated reason would make those tests pass while
// measuring nothing.
func TestWithoutTheKernTheNextWordWouldMove(t *testing.T) {
	before := twoShowsPage(t, 12)
	wasX := runSaying(t, textRunsOf(t, before), "World").X
	// The same splice the verb does, with the adjustment left out.
	src, err := reader.Open(before)
	if err != nil {
		t.Fatal(err)
	}
	content, err := pageContent(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	run := runSaying(t, textRunsOf(t, before), "Hello")
	naive := editedContent(content, []plannedEdit{{at: run.Op, now: []byte("(Hi) Tj")}})
	if string(naive) == string(content) {
		t.Fatal("the splice changed nothing, so this measures nothing")
	}
	// "Hello" is five glyphs of 500 and "Hi" is two, so the pen ends
	// 3 * 0.5 * 12 = 18 points earlier.
	moved := spliceAndRead(t, before, naive)
	if diff := wasX - moved; diff < 17.9 || diff > 18.1 {
		t.Errorf("without a kern the next word moved by %.3f points, and the "+
			"widths differ by 18", diff)
	}
}

// spliceAndRead puts a content stream into a copy of a document and reports
// where "World" then starts.
func spliceAndRead(t *testing.T, pdf, content []byte) float64 {
	t.Helper()
	d, err := Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	// The simplest way to get these bytes onto the page is the same path the
	// verb uses, so this builds the document from the edited stream directly.
	src, err := reader.Open(pdf)
	if err != nil {
		t.Fatal(err)
	}
	page, err := src.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	res, err := src.Resolve(page.Get("Resources"))
	if err != nil {
		t.Fatal(err)
	}
	resDict, _ := reader.ToDict(res)
	fonts, err := src.Resolve(resDict.Get("Font"))
	if err != nil {
		t.Fatal(err)
	}
	fontsDict, _ := reader.ToDict(fonts)
	one, err := src.Resolve(fontsDict.Get("F1"))
	if err != nil {
		t.Fatal(err)
	}
	oneDict, _ := reader.ToDict(one)
	fontRef := w.Add(oneDict)
	stream := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: content})
	pageRef := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": stream,
		"Resources": reader.Dict{"Font": reader.Dict{"F1": fontRef}},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(400), reader.Integer(400)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	_ = d
	return runSaying(t, textRunsOf(t, out), "World").X
}

// ⛔ The replaced text is gone from the BYTES, not merely overdrawn. A reader
// that copies text out of the stream must not find the old words.
func TestTheOldTextIsGoneFromTheFile(t *testing.T) {
	after := replacedOnce(t, twoShowsPage(t, 12), "Hello", "Goodbye")
	if strings.Contains(string(after), "(Hello)") {
		t.Error("the old string is still in the content stream")
	}
	if !strings.Contains(string(after), "Goodbye") {
		t.Error("the new string is not in the content stream")
	}
}

func TestWhatReplacingTextRefuses(t *testing.T) {
	page := twoShowsPage(t, 12)
	hello := runSaying(t, textRunsOf(t, page), "Hello")
	for _, c := range []struct {
		name string
		edit TextEdit
		says string
	}{
		{"a page the document has not",
			TextEdit{Page: 2, At: hello.Op, Now: "x"}, "there is no page 2"},
		{"page zero",
			TextEdit{Page: 0, At: hello.Op, Now: "x"}, "there is no page 0"},
		{"an empty replacement",
			TextEdit{Page: 1, At: hello.Op}, "removing text is a redaction"},
		{"no operation named",
			TextEdit{Page: 1, Now: "x"}, "names no operation"},
		{"an operation the page does not draw",
			TextEdit{Page: 1, At: extract.Span{From: 1, To: 2}, Now: "x"},
			"draws no text at the operation"},
		{"a span from another document altogether",
			TextEdit{Page: 1, At: extract.Span{From: 9000, To: 9100}, Now: "x"},
			"draws no text at the operation"},
		{"a character the font cannot draw",
			TextEdit{Page: 1, At: hello.Op, Now: "Hello☃"}, "cannot draw '☃'"},
		{"a different number of spaces",
			TextEdit{Page: 1, At: hello.Op, Now: "He llo"}, "holds 0 space(s) and the replacement holds 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Open(page)
			if err != nil {
				t.Fatal(err)
			}
			err = d.ReplaceText(c.edit)
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q", err)
			}
			// ⛔ And nothing was kept: a refusal that half-added the edit would
			// apply it at the next write.
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), "(Hello)") {
				t.Error("a refused edit changed the page anyway")
			}
		})
	}
}

// firstRun is the page's first run, for the fixtures whose text is not worth
// naming.
func firstRun(t *testing.T, pdf []byte) extract.Run {
	t.Helper()
	runs := textRunsOf(t, pdf)
	if len(runs) == 0 {
		t.Fatal("the page draws no text")
	}
	return runs[0]
}

// ⛔ The refusals that need a page of their own: an operator drawing several
// strings, one drawn from inside a form, one drawn with an operator that also
// moves the line, and one set in a composite font. Each is a condition under
// which a rewrite would be wrong rather than merely hard, so each must be
// refused BY NAME — and a refusal nobody has seen fire is a refusal nobody
// knows the wording of.
func TestTheRefusalsThatNeedAPageOfTheirOwn(t *testing.T) {
	t.Run("an operator drawing several strings", func(t *testing.T) {
		page := twoShowsPageBody(t, 12, "[(Hel) (lo)] TJ (World) Tj")
		d, err := Open(page)
		if err != nil {
			t.Fatal(err)
		}
		at := firstRun(t, page).Op
		runs := 0
		for _, r := range textRunsOf(t, page) {
			if r.Op == at {
				runs++
			}
		}
		if runs < 2 {
			t.Skipf("this fixture gives %d run(s) for one operator", runs)
		}
		err = d.ReplaceText(TextEdit{Page: 1, At: at, Now: "xy"})
		if err == nil {
			t.Fatal("it was accepted")
		}
		if !strings.Contains(err.Error(), "pieces of text") {
			t.Errorf("the refusal says %q", err)
		}
	})

	t.Run("an operator that also moves the line", func(t *testing.T) {
		page := twoShowsPageBody(t, 12, "(Hello) ' (World) Tj")
		d, err := Open(page)
		if err != nil {
			t.Fatal(err)
		}
		at := runSaying(t, textRunsOf(t, page), "Hello").Op
		err = d.ReplaceText(TextEdit{Page: 1, At: at, Now: "xy"})
		if err == nil {
			t.Fatal("it was accepted")
		}
		if !strings.Contains(err.Error(), "moves to the next line") {
			t.Errorf("the refusal says %q", err)
		}
	})

	t.Run("a composite font", func(t *testing.T) {
		page := compositeFontPage(t)
		d, err := Open(page)
		if err != nil {
			t.Fatal(err)
		}
		at := firstRun(t, page).Op
		err = d.ReplaceText(TextEdit{Page: 1, At: at, Now: "xy"})
		if err == nil {
			t.Fatal("it was accepted")
		}
		if !strings.Contains(err.Error(), "composite font") {
			t.Errorf("the refusal says %q", err)
		}
	})
}

// compositeFontPage draws text with a Type0 font: two-byte codes a CMap
// defines, which is the case ReplaceText must refuse rather than guess at. A
// /ToUnicode map is included so extract can still read the page — without it
// the run would be unreadable and the refusal would be about that instead.
func compositeFontPage(t *testing.T) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	toUnicode := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(
		`/CIDInit /ProcSet findresource begin 12 dict begin begincmap
/CMapName /A def /CMapType 2 def
1 begincodespacerange <0000> <FFFF> endcodespacerange
2 beginbfchar <0048> <0048> <0065> <0065> endbfchar
endcmap CMapName currentdict /CMap defineresource pop end end`)})
	fd := w.Add(reader.Dict{
		"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Nothing"),
		"Flags": reader.Integer(4), "ItalicAngle": reader.Integer(0),
		"Ascent": reader.Integer(800), "Descent": reader.Integer(-200),
		"CapHeight": reader.Integer(700), "StemV": reader.Integer(80),
		"FontBBox": reader.Array{reader.Integer(0), reader.Integer(-200),
			reader.Integer(1000), reader.Integer(800)},
	})
	cid := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("CIDFontType2"),
		"BaseFont": reader.Name("Nothing"), "DW": reader.Integer(500),
		"CIDSystemInfo": reader.Dict{
			"Registry": reader.String("Adobe"), "Ordering": reader.String("Identity"),
			"Supplement": reader.Integer(0),
		},
		"FontDescriptor": fd,
	})
	font := w.Add(reader.Dict{
		"Type": reader.Name("Font"), "Subtype": reader.Name("Type0"),
		"BaseFont": reader.Name("Nothing"), "Encoding": reader.Name("Identity-H"),
		"DescendantFonts": reader.Array{cid}, "ToUnicode": toUnicode,
	})
	content := w.Add(&reader.Stream{Dict: reader.Dict{},
		Raw: []byte("BT /F1 12 Tf 20 300 Td <00480065> Tj ET")})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
		"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(400), reader.Integer(400)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ⛔⛔ A PAGE MAY NOT BE BOTH REDACTED AND REWRITTEN IN ONE WRITE, and the
// refusal is made at BOTH doors. A rule enforced at one of its two entrances is
// a rule the caller meets or misses depending on the order they happened to
// call things in — and the wrong answer here is not a crash but a redaction
// landing on the wrong bytes, which is a document that looks redacted and is
// not.
func TestAPageIsNotBothRedactedAndRewritten(t *testing.T) {
	page := twoShowsPage(t, 12)
	hello := runSaying(t, textRunsOf(t, page), "Hello")
	region := []Quad{QuadOf(15, 295, 200, 315)}

	t.Run("replace then redact", func(t *testing.T) {
		d, err := Open(page)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.ReplaceText(TextEdit{Page: 1, At: hello.Op, Now: "Hi"}); err != nil {
			t.Fatal(err)
		}
		err = d.Redact(Redaction{Page: 1, Quads: region})
		if err == nil {
			t.Fatal("the redaction was accepted")
		}
		if !strings.Contains(err.Error(), "already being replaced") {
			t.Errorf("the refusal says %q", err)
		}
	})

	t.Run("redact then replace", func(t *testing.T) {
		d, err := Open(page)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Redact(Redaction{Page: 1, Quads: region}); err != nil {
			t.Skipf("this region cannot be redacted on this fixture: %v", err)
		}
		err = d.ReplaceText(TextEdit{Page: 1, At: hello.Op, Now: "Hi"})
		if err == nil {
			t.Fatal("the replacement was accepted")
		}
		if !strings.Contains(err.Error(), "already being redacted") {
			t.Errorf("the refusal says %q", err)
		}
	})
}

// ⛔ A page whose stream or whose text cannot be read is refused, not guessed
// at — through the same seams the redaction uses, because the condition is a
// damaged file and no fixture can be reliably damaged in just that way.
func TestAPageThatCannotBeReadIsNotRewritten(t *testing.T) {
	page := twoShowsPage(t, 12)
	at := runSaying(t, textRunsOf(t, page), "Hello").Op
	for _, c := range []struct {
		name string
		fail func(t *testing.T)
		says string
	}{
		{
			name: "its content stream will not come out",
			fail: func(t *testing.T) {
				was := pageContent
				t.Cleanup(func() { pageContent = was })
				pageContent = func(*reader.Document, int) ([]byte, error) {
					return nil, errors.New("the stream is a ruin")
				}
			},
			says: "the content of page 1 could not be read",
		},
		{
			name: "its text will not come out",
			fail: func(t *testing.T) {
				was := extractRuns
				t.Cleanup(func() { extractRuns = was })
				extractRuns = func(*reader.Document, int) ([]extract.Run, error) {
					return nil, errors.New("the text is a ruin")
				}
			},
			says: "could not be read to replace its text",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Open(page)
			if err != nil {
				t.Fatal(err)
			}
			c.fail(t)
			err = d.ReplaceText(TextEdit{Page: 1, At: at, Now: "xy"})
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q", err)
			}
		})
	}
}

// ⛔⛔ AND IF THE READ FAILS BETWEEN THE CHECK AND THE WRITE, THE WRITE FAILS.
// The edit was accepted on a page that could be read; by the time the document
// is written the stream will not come out. Writing the page unchanged would hand
// back a document the caller was told had been edited — the same shape of lie as
// a redaction that did not happen, so it is the same answer: no document at all.
func TestAWriteFailsIfTheTextCannotBeReplacedAfterAll(t *testing.T) {
	d, err := Open(twoShowsPage(t, 12))
	if err != nil {
		t.Fatal(err)
	}
	at := runSaying(t, textRunsOf(t, twoShowsPage(t, 12)), "Hello").Op
	if err := d.ReplaceText(TextEdit{Page: 1, At: at, Now: "Hi"}); err != nil {
		t.Fatal(err)
	}
	was := pageContent
	t.Cleanup(func() { pageContent = was })
	pageContent = func(*reader.Document, int) ([]byte, error) {
		return nil, errors.New("the stream is a ruin")
	}
	out, err := d.Bytes()
	if err == nil {
		t.Fatalf("a document of %d bytes was written anyway", len(out))
	}
	if !strings.Contains(err.Error(), "replacement cannot be applied") {
		t.Errorf("the failure says %q", err)
	}
}

// ⛔ Text drawn from inside a form is refused: its span does not index the
// page's own stream, and the form is SHARED with every page that draws it.
func TestTextInsideAFormIsNotRewritten(t *testing.T) {
	page := formTextPage(t)
	d, err := Open(page)
	if err != nil {
		t.Fatal(err)
	}
	run := firstRun(t, page)
	if !run.InForm {
		t.Skipf("this fixture's run is not in a form")
	}
	err = d.ReplaceText(TextEdit{Page: 1, At: run.Op, Now: "xy"})
	if err == nil {
		t.Fatal("it was accepted")
	}
	if !strings.Contains(err.Error(), "inside a form") {
		t.Errorf("the refusal says %q", err)
	}
}

// formTextPage draws its text from a Form XObject rather than from the page.
func formTextPage(t *testing.T) []byte {
	t.Helper()
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
	form := w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"BBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(400), reader.Integer(400)},
		"Resources": reader.Dict{"Font": reader.Dict{"F1": font}},
	}, Raw: []byte("BT /F1 12 Tf 20 300 Td (Hello) Tj ET")})
	content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("/X1 Do")})
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": content,
		"Resources": reader.Dict{"XObject": reader.Dict{"X1": form}},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(400), reader.Integer(400)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ⛔ Two refusals about the FONT, driven through the extractRuns seam because a
// real file cannot easily produce either: extract does not hand back a run whose
// font the page's resources never described, nor one whose text it could not
// read. Both are states a damaged or unusual document can be in, and in both the
// width cannot be measured — so a kern guessed from nothing would move the line
// this verb exists to hold still.
func TestTheTwoRefusalsAboutTheFont(t *testing.T) {
	page := twoShowsPage(t, 12)
	real := runSaying(t, textRunsOf(t, page), "Hello")
	for _, c := range []struct {
		name string
		run  extract.Run
		says string
	}{
		{
			name: "a font the resources do not describe",
			run:  extract.Run{Text: "Hello", Font: "F9", Op: real.Op},
			says: "resources do not describe",
		},
		{
			name: "text the page's codes could not be read as",
			run:  extract.Run{Text: "Hell☃", Font: "F1", Op: real.Op},
			says: "cannot be read back as characters",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := Open(page)
			if err != nil {
				t.Fatal(err)
			}
			was := extractRuns
			t.Cleanup(func() { extractRuns = was })
			extractRuns = func(*reader.Document, int) ([]extract.Run, error) {
				return []extract.Run{c.run}, nil
			}
			err = d.ReplaceText(TextEdit{Page: 1, At: real.Op, Now: "xy"})
			if err == nil {
				t.Fatal("it was accepted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the refusal says %q", err)
			}
		})
	}
}

// ⛔ The splicing on its own: nothing to do, several edits at once, and a span
// that cannot be spliced. The several-at-once case is the one that matters —
// each replacement changes the length, so they go in from the END BACKWARDS, and
// a forward pass would put the second one at an offset the first had moved.
func TestHowTheSplicingBehaves(t *testing.T) {
	const data = "one two three"
	t.Run("nothing to do", func(t *testing.T) {
		if got := string(editedContent([]byte(data), nil)); got != data {
			t.Errorf("no edits gave %q", got)
		}
	})
	t.Run("two edits at once", func(t *testing.T) {
		got := string(editedContent([]byte(data), []plannedEdit{
			{at: extract.Span{From: 0, To: 3}, now: []byte("ONE!")},
			{at: extract.Span{From: 8, To: 13}, now: []byte("3")},
		}))
		if want := "ONE! two 3"; got != want {
			t.Errorf("two edits gave %q, want %q", got, want)
		}
	})
	t.Run("a span that cannot be spliced", func(t *testing.T) {
		for _, bad := range []extract.Span{
			{From: -1, To: 4}, {From: 0, To: 99}, {From: 5, To: 5}, {From: 6, To: 2},
		} {
			if got := string(editedContent([]byte(data), []plannedEdit{
				{at: bad, now: []byte("X")},
			})); got != data {
				t.Errorf("the span %+v changed the bytes to %q", bad, got)
			}
		}
	})
}

// ⛔ The small pure answers, each of which has a case the rest of the file
// cannot reach through a document.
func TestTheSmallAnswers(t *testing.T) {
	if got := operatorOf(nil); got != "" {
		t.Errorf("the operator of nothing is %q", got)
	}
	// A span that does not fit its stream reads as nothing rather than as half
	// an operator — the case a run drawn inside a form would otherwise take.
	for _, bad := range []extract.Span{
		{From: -1, To: 2}, {From: 0, To: 99}, {From: 3, To: 3}, {From: 4, To: 1},
	} {
		if got := spanBytes([]byte("abcde"), bad); got != nil {
			t.Errorf("the span %+v gave %q", bad, got)
		}
	}
	if got := string(spanBytes([]byte("abcde"), extract.Span{From: 1, To: 3})); got != "bc" {
		t.Errorf("a span that fits gave %q", got)
	}
	if got := operatorOf([]byte("  (x) Tj  ")); got != "Tj" {
		t.Errorf("the operator of %q is %q", "(x) Tj", got)
	}
	// A kern of a hair is no kern: it would reach the file as noise.
	if got := roundTo(1e-14, 1e-3); got != 0 {
		t.Errorf("a kern of 1e-14 rounded to %v", got)
	}
	if got := roundTo(-1500.0004, 1e-3); got != -1500 {
		t.Errorf("-1500.0004 rounded to %v", got)
	}
	// A page with no edits plans none, which is what rewrittenContent relies on
	// when it is called for a recolouring alone.
	d := New()
	d.Blank(10, 10)
	got, err := d.plannedEdits(d.pages[0])
	if err != nil || got != nil {
		t.Errorf("a page with no edits planned %v, %v", got, err)
	}
}

// ⛔ A page made rather than read has no text to replace, and says so instead of
// reaching into a stream that does not exist.
func TestAMadePageHasNoTextToReplace(t *testing.T) {
	d := New()
	d.Blank(200, 200)
	err := d.ReplaceText(TextEdit{Page: 1, At: extract.Span{From: 0, To: 4}, Now: "x"})
	if err == nil {
		t.Fatal("it was accepted")
	}
	if !strings.Contains(err.Error(), "made rather than read") {
		t.Errorf("the refusal says %q", err)
	}
}

// ⛔ A string whose bytes the stream ESCAPED is measured by its CODES. The old
// width used to be found by locating the parentheses and undoing PDF's string
// escaping, which meant a parser of its own — and a stream that wrote a code as
// \( or as an octal triple holds more bytes than codes, so counting bytes made
// the kern wrong by a glyph per escape. Both widths now come from the same
// encoder, over the run's own text, and this asserts that a page whose string
// needs escaping is still measured right.
func TestAnEscapedStringIsMeasuredByItsCodesAndNotItsBytes(t *testing.T) {
	// "a(b" is written (a\(b) in the stream: four bytes for three codes.
	page := onePhraseThen(t, `a\(b`, "a(b")
	wasX := runSaying(t, textRunsOf(t, page), "World").X
	after := replacedOnce(t, page, "a(b", "xy")
	nowX := runSaying(t, textRunsOf(t, after), "World").X
	if diff := nowX - wasX; diff > 0.01 || diff < -0.01 {
		t.Errorf("after replacing an escaped string the next word moved from "+
			"x=%.3f to x=%.3f: the old width was measured in bytes, not codes",
			wasX, nowX)
	}
}
