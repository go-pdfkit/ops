package ops

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/go-gfx/gfx/codec"
	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/xfa"
)

// xfaFile writes a document whose form carries an XFA package of named parts,
// which is how every one of the corpus's dynamic forms is written.
func xfaFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	return formFile(t, false, func(w *reader.Writer, _ reader.Ref) (reader.Dict, reader.Array) {
		var arr reader.Array
		for i := 0; i+1 < len(parts); i += 2 {
			arr = append(arr, reader.String(parts[i]),
				w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(parts[i+1])}))
		}
		return reader.Dict{"Fields": reader.Array{}, "XFA": arr}, nil
	})
}

// openXFA is the whole chain a caller runs: read the file, lay the form out.
func openXFA(t *testing.T, file []byte) (*Doc, *XFAReport) {
	t.Helper()
	src, err := reader.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	d, rep, err := FromXFA(src)
	if err != nil {
		t.Fatalf("laying the form out: %v", err)
	}
	return d, rep
}

// refuseXFA is the same, for the documents this declines.
func refuseXFA(t *testing.T, file []byte) string {
	t.Helper()
	src, err := reader.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := FromXFA(src); err != nil {
		return err.Error()
	}
	t.Fatal("the form was laid out and should not have been")
	return ""
}

// A form of one sheet, one caption and one value, which is the smallest thing
// that exercises the whole chain.
const smallTemplate = `<template>
<subform name="form1" layout="tb">
  <pageSet><pageArea name="Page1">
    <medium long="792pt" short="612pt"/>
    <contentArea x="0pt" y="0pt" w="576pt" h="700pt"/>
  </pageArea></pageSet>
  <subform name="Body">
    <draw name="Title" w="300pt" h="20pt">
      <value><text>Application for a licence</text></value>
    </draw>
    <field name="Surname" w="200pt" h="20pt">
      <caption><value><text>Surname</text></value></caption>
    </field>
  </subform>
</subform>
</template>`

const smallData = `<datasets><data><form1><Body><Surname>Delavennat</Surname></Body></form1></data></datasets>`

func TestADynamicFormIsDrawnFromItsDescription(t *testing.T) {
	d, rep := openXFA(t, xfaFile(t, "template", smallTemplate, "datasets", smallData))
	if rep.Sheets != 1 || d.PageCount() != 1 {
		t.Fatalf("%d sheets in the report and %d pages in the document", rep.Sheets, d.PageCount())
	}
	if rep.Drawn != 2 {
		t.Errorf("%d elements drawn, wanted the draw and the field", rep.Drawn)
	}
	if len(rep.Unplaced) != 0 {
		t.Errorf("something was not placed: %v", rep.Unplaced)
	}
	// The source's own page is the placeholder and is not kept: what comes
	// back is the form, not the file it travelled in.
	if d.pages[0].blank != true {
		t.Error("the page was borrowed rather than drawn")
	}
	// A caption, a value and a title, and a rule around the one field.
	text := marksText(d.pages[0])
	for _, want := range []string{"Application for a licence", "Surname", "Delavennat"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not on the page; it has %q", want, text)
		}
	}
	if outlines(d.pages[0]) != 1 {
		t.Errorf("%d rules drawn, wanted one round the field", outlines(d.pages[0]))
	}
}

// marksText is every string a page's marks draw, in order.
func marksText(p Page) string {
	var b strings.Builder
	for _, m := range p.marks {
		if m.text == "" {
			continue
		}
		b.WriteString(m.text)
		b.WriteByte('\n')
	}
	return b.String()
}

// outlines counts the rules a page draws.
func outlines(p Page) int {
	n := 0
	for _, m := range p.marks {
		if m.outline != nil {
			n++
		}
	}
	return n
}

func TestTheDrawnFormSurvivesBeingWrittenOut(t *testing.T) {
	d, _ := openXFA(t, xfaFile(t, "template", smallTemplate, "datasets", smallData))
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := reader.Open(out)
	if err != nil {
		t.Fatalf("the file this wrote does not read again: %v", err)
	}
	if back.PageCount() != 1 {
		t.Fatalf("%d pages came back", back.PageCount())
	}
	// The words have to be in the content stream: a page that reads as a page
	// and draws nothing is the failure this is guarding against.
	page, _ := back.Page(1)
	stream, ok := reader.ToStream(resolve(back, page.Get("Contents")))
	if !ok {
		t.Fatal("the page has no content stream")
	}
	body, _, err := reader.DecodeStream(stream, back.Get)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Delavennat") {
		t.Errorf("the value is not in what was written: %q", body)
	}
	if !strings.Contains(string(body), " re S") {
		t.Error("the field's rule is not in what was written")
	}
}

func TestAFormWithNothingFilledInIsStillDrawn(t *testing.T) {
	// No datasets part at all. Every field is empty and the form still lays
	// out, because a blank form is the thing somebody prints.
	_, rep := openXFA(t, xfaFile(t, "template", smallTemplate))
	if rep.Drawn != 2 {
		t.Errorf("%d elements drawn", rep.Drawn)
	}
}

func TestDatasetsThatWillNotReadLeaveTheFormBlankRatherThanRefuseIt(t *testing.T) {
	d, rep := openXFA(t, xfaFile(t, "template", smallTemplate, "datasets", "<notdatasets/>"))
	if rep.Drawn != 2 {
		t.Errorf("%d elements drawn", rep.Drawn)
	}
	if strings.Contains(marksText(d.pages[0]), "Delavennat") {
		t.Error("a value came from datasets that do not read")
	}
}

func TestWhatIsRefusedAndWhatItSays(t *testing.T) {
	for _, c := range []struct {
		name string
		file []byte
		want string
	}{
		{"no form at all", buildPDF(t, 1, nil), "no form of any kind"},
		{"a form with no XFA", oneTextField(t, false, nil), "carries no XFA package"},
		{"a package with no template part", xfaFile(t, "config", "<config/>", "datasets", smallData),
			"no template part (it has config, datasets)"},
		{"a template that is not one", xfaFile(t, "template", "<notatemplate/>"), "not a template"},
		{"a template that does not close", xfaFile(t, "template", "<template><subform>"), "reading the template"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := refuseXFA(t, c.file); !strings.Contains(got, c.want) {
				t.Errorf("said %q, wanted it to mention %q", got, c.want)
			}
		})
	}
}

func TestAPackageWrittenAsOneStreamIsNamedForWhatItIs(t *testing.T) {
	// A single unnamed stream holds the whole XDP rather than the named
	// parts, so its root is <xdp>. Saying which of the two problems the
	// caller has is the point of the message.
	file := formFile(t, false, func(w *reader.Writer, _ reader.Ref) (reader.Dict, reader.Array) {
		return reader.Dict{"Fields": reader.Array{},
			"XFA": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("<xdp/>")})}, nil
	})
	if got := refuseXFA(t, file); !strings.Contains(got, "one unnamed stream") {
		t.Errorf("said %q", got)
	}
}

func TestAPackageHoldingNothingSaysSo(t *testing.T) {
	file := formFile(t, false, func(w *reader.Writer, _ reader.Ref) (reader.Dict, reader.Array) {
		return reader.Dict{"Fields": reader.Array{}, "XFA": reader.Array{}}, nil
	})
	// An empty array is still an XFA entry, so the document does carry a
	// package: what it has not got is a template, and that is what it says.
	if got := refuseXFA(t, file); !strings.Contains(got, "no template part (it has nothing)") {
		t.Errorf("said %q", got)
	}
}

func TestHowAPackageIsDescribedWhenTheWantedPartIsMissing(t *testing.T) {
	// A part with no name is described as one rather than as an empty string,
	// which would read as a gap in the message rather than as a part.
	for _, c := range []struct {
		name string
		in   []forms.Packet
		want string
	}{
		{"nothing at all", nil, "nothing"},
		{"named parts", []forms.Packet{{Name: "config"}, {Name: "datasets"}}, "config, datasets"},
		{"one with no name", []forms.Packet{{Name: "config"}, {}}, "config, an unnamed part"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := partNames(c.in); got != c.want {
				t.Errorf("described as %q, wanted %q", got, c.want)
			}
		})
	}
}

func TestAPageAreaWithNoMediumIsGivenASheetAndTheGuessIsNamed(t *testing.T) {
	const noMedium = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><field name="A" w="10pt" h="10pt"/></subform>
	</subform></template>`
	d, rep := openXFA(t, xfaFile(t, "template", noMedium))
	if rep.Sheets != 1 {
		t.Fatalf("%d sheets", rep.Sheets)
	}
	if d.pages[0].size != [2]float64{612, 792} {
		t.Errorf("the sheet came out %v, wanted US Letter", d.pages[0].size)
	}
}

func TestWhatTheFormHidesIsCountedAndNotDrawn(t *testing.T) {
	const hidden = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body">
	    <draw name="Seen" w="100pt" h="20pt"><value><text>seen</text></value></draw>
	    <draw name="Gone" presence="hidden" w="100pt" h="20pt"><value><text>gone</text></value></draw>
	  </subform></subform></template>`
	d, rep := openXFA(t, xfaFile(t, "template", hidden))
	if rep.Hidden != 1 || rep.Drawn != 1 {
		t.Errorf("drawn=%d hidden=%d", rep.Drawn, rep.Hidden)
	}
	if strings.Contains(marksText(d.pages[0]), "gone") {
		t.Error("what the form hides was drawn")
	}
}

func TestWhatTheLayoutCouldNotPlaceIsReportedWithItsReason(t *testing.T) {
	// A length in "px" is not an XFA length at all, and the engine reports
	// the element rather than guessing at a width for it.
	const px = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><field name="A" w="10px" h="10pt"/></subform>
	</subform></template>`
	_, rep := openXFA(t, xfaFile(t, "template", px))
	if len(rep.Unplaced) != 1 {
		t.Fatalf("%d unplaced: %v", len(rep.Unplaced), rep.Unplaced)
	}
	if !strings.Contains(rep.Unplaced[0], "form1.Body.A") {
		t.Errorf("the report does not name the element: %q", rep.Unplaced[0])
	}
}

func TestThePictureInsideAValueIsNotDrawnAsWords(t *testing.T) {
	// An <image> holds the picture, base64, inside the XML. A walk that
	// gathers every string under a <value> gathers that too, and the first
	// draw of a French cerfa came out with a kilobyte of "/9j/4AAQSkZJRg..."
	// across its title.
	const withImage = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><draw name="Logo" w="200pt" h="40pt">
	    <value><image contentType="image/jpeg">/9j/4AAQSkZJRgABAQEBLAEs</image></value>
	  </draw></subform></subform></template>`
	d, _ := openXFA(t, xfaFile(t, "template", withImage))
	if got := marksText(d.pages[0]); strings.Contains(got, "9j") {
		t.Errorf("the picture was drawn as words: %q", got)
	}
}

func TestTheSizeIsTheOneTheTemplateAsksFor(t *testing.T) {
	const sized = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body">
	    <draw name="Big" w="400pt" h="40pt"><font size="18pt"/><value><text>big</text></value></draw>
	    <draw name="Plain" w="400pt" h="40pt"><value><text>plain</text></value></draw>
	    <draw name="Odd" w="400pt" h="40pt"><font size="furlongs"/><value><text>odd</text></value></draw>
	  </subform></subform></template>`
	d, _ := openXFA(t, xfaFile(t, "template", sized))
	sizes := map[string]float64{}
	for _, m := range d.pages[0].marks {
		if m.text != "" {
			sizes[m.text] = m.stamp.Size
		}
	}
	if sizes["big"] != 18 {
		t.Errorf("the sized draw came out at %v", sizes["big"])
	}
	// Ten is the size the engine measured them at, so it is the size they are
	// drawn at: a length nobody can read is no length at all.
	if sizes["plain"] != 10 || sizes["odd"] != 10 {
		t.Errorf("plain=%v odd=%v, both wanted ten", sizes["plain"], sizes["odd"])
	}
}

func TestTextTooWideForItsBoxIsSetSmallerRatherThanCutOff(t *testing.T) {
	// The box was measured with the font the TEMPLATE names and this draws in
	// Helvetica, which is wider. At the template's size the words come to more
	// lines than fit, and the tail used to be dropped mid-sentence.
	const long = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><draw name="Note" w="120pt" h="14pt">
	    <value><text>If the total cost of all specified foreign property held at any time during the year exceeds one hundred thousand dollars</text></value>
	  </draw></subform></subform></template>`
	d, _ := openXFA(t, xfaFile(t, "template", long))
	got := marksText(d.pages[0])
	if !strings.Contains(got, "hundred thousand dollars") {
		t.Errorf("the end of the sentence was lost: %q", got)
	}
	for _, m := range d.pages[0].marks {
		if m.text != "" && m.stamp.Size >= 10 {
			t.Errorf("the text was not made smaller: %v at %v", m.text, m.stamp.Size)
		}
	}
}

func TestABoxFarTooSmallForItsWordsIsClippedRatherThanShrunkToNothing(t *testing.T) {
	// Four points is the floor. Below it nothing is readable, and shrinking
	// further would hide a layout this package got wrong rather than show it.
	const tiny = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><draw name="Note" w="8pt" h="5pt">
	    <value><text>a great many words indeed that will never fit into this box</text></value>
	  </draw></subform></subform></template>`
	d, _ := openXFA(t, xfaFile(t, "template", tiny))
	for _, m := range d.pages[0].marks {
		if m.text != "" && m.stamp.Size < 4 {
			t.Errorf("the text went below the floor: %v", m.stamp.Size)
		}
	}
}

func TestWrappingAtTheLastWordThatFits(t *testing.T) {
	for _, c := range []struct {
		name  string
		text  string
		width float64
		want  int
	}{
		{"no room to break at", "one two three four", 0, 1},
		{"it fits as it is", "one two", 500, 1},
		{"a word longer than the line goes on one of its own",
			"supercalifragilisticexpialidocious and", 20, 2},
		{"nothing at all", "   ", 100, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := wrapToWidth(Helvetica, c.text, 10, c.width); len(got) != c.want {
				t.Errorf("%d lines, wanted %d: %q", len(got), c.want, got)
			}
		})
	}
}

func TestEveryLineOfAWrapFitsTheWidthItWasGiven(t *testing.T) {
	// Counting the lines would only record whatever this happens to do. What
	// is being promised is that each line fits — except one holding a single
	// word, which cannot be broken without turning an account number into two
	// of them.
	const text = "one two three four five six seven eight nine ten eleven twelve"
	for _, width := range []float64{20, 40, 80, 160, 320} {
		lines := wrapToWidth(Helvetica, text, 10, width)
		if len(lines) == 0 {
			t.Fatalf("nothing came back at width %v", width)
		}
		if got := strings.Join(lines, " "); got != text {
			t.Errorf("at width %v the words came back as %q", width, got)
		}
		for _, l := range lines {
			if Helvetica.Width(l, 10) > width && strings.Contains(l, " ") {
				t.Errorf("at width %v the line %q is %v wide", width, l, Helvetica.Width(l, 10))
			}
		}
	}
}

func TestReadingTheWordsOutOfAValue(t *testing.T) {
	if got := xfaValueText(nil, nil); got != "" {
		t.Errorf("nothing came to %q", got)
	}
	if got := xfaFontSize(nil); got != 10 {
		t.Errorf("no node came to %v points", got)
	}
	if got := xfaCaption(nil, nil); got != "" {
		t.Errorf("no node captioned %q", got)
	}
	if got := xfaContent(nil, nil); got != "" {
		t.Errorf("no node drew %q", got)
	}
	if boldIf(true) != HelveticaBold || boldIf(false) != Helvetica {
		t.Error("the faces are the wrong way round")
	}
}

func TestABoxWithNoRoomToMeasureAgainstIsDrawnAtTheSizeAsked(t *testing.T) {
	// Nothing can be made to fit a box of no width or no height, so the
	// template's own size stands and the text is drawn at it. The alternative
	// — shrinking against a zero — runs to the floor for no reason.
	const noRoom = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><draw name="Nothing" w="0pt" h="0pt">
	    <value><text>drawn all the same</text></value>
	  </draw></subform></subform></template>`
	d, _ := openXFA(t, xfaFile(t, "template", noRoom))
	var sizes []float64
	for _, m := range d.pages[0].marks {
		if m.text != "" {
			sizes = append(sizes, m.stamp.Size)
		}
	}
	if len(sizes) != 1 || sizes[0] != 10 {
		t.Errorf("the sizes came out %v, wanted the one the template asks for", sizes)
	}
}

func TestARichTextsWordsAreReadInTheOrderItsMarkupWritesThem(t *testing.T) {
	// The characters of a rich text sit in its elements rather than under the
	// value, and the elements between them carry none of their own. Reading
	// it has to descend past the empty ones and put a space between the
	// pieces it finds, or two paragraphs come back as one word.
	const rich = `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><draw name="Note" w="400pt" h="40pt">
	    <value><exData contentType="text/html"><body><p>first line</p><p>second line</p></body></exData></value>
	  </draw></subform></subform></template>`
	d, _ := openXFA(t, xfaFile(t, "template", rich))
	got := marksText(d.pages[0])
	if !strings.Contains(got, "first line") || !strings.Contains(got, "second line") {
		t.Errorf("the markup's words came out as %q", got)
	}
	if strings.Contains(got, "linesecond") {
		t.Errorf("two paragraphs were run together: %q", got)
	}
}

// imageTemplate is a one-sheet form whose only element is a draw carrying a
// picture, with whatever attributes the case under test wants on it.
func imageTemplate(attrs, payload string) string {
	return `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><draw name="Logo" w="200pt" h="100pt">
	    <value><image ` + attrs + `>` + payload + `</image></value>
	  </draw></subform></subform></template>`
}

func TestAPictureInAFormIsDrawn(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(picBytes(t, codec.JPEG, 255))
	d, rep := openXFA(t, xfaFile(t, "template", imageTemplate(`contentType="image/jpeg"`, payload)))
	if rep.Pictures != 1 {
		t.Fatalf("%d pictures drawn, and %v", rep.Pictures, rep.Unplaced)
	}
	if len(d.pages[0].pictures) != 1 {
		t.Fatalf("%d pictures on the page", len(d.pages[0].pictures))
	}
	// The picture is 32 by 16 in a box 200 by 100. "fit" is the default, so
	// it is scaled by the smaller ratio — 200/32 = 6.25 against 100/16 = 6.25,
	// which are equal here — and anchored at the top left of the box.
	got := d.pages[0].pictures[0].rect
	if got[2] != 200 || got[3] != 100 {
		t.Errorf("it was drawn %v by %v", got[2], got[3])
	}
	// The sheet is 792 tall and the box begins at the top of the content area,
	// so the picture's bottom is 792 - 0 - 100.
	if got[1] != 692 {
		t.Errorf("its bottom is at %v", got[1])
	}
}

func TestAPictureSurvivesBeingWrittenOut(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(picBytes(t, codec.JPEG, 255))
	d, _ := openXFA(t, xfaFile(t, "template", imageTemplate(`contentType="image/jpeg"`, payload)))
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, st, _ := pageImage(t, out)
	if got := st.Dict.Get("Filter"); got != reader.Name("DCTDecode") {
		t.Errorf("the picture went in as %v", got)
	}
}

func TestWhatStopsAPictureBeingDrawn(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(picBytes(t, codec.JPEG, 255))
	for _, c := range []struct {
		name  string
		attrs string
		body  string
		want  string
	}{
		// pdf.js: "we don't get remote data and use what we have in the pdf
		// itself, so no picture for non null href" (template.js:3414-3419).
		{"a file outside the document", `href="C:\logo.png"`, "", "outside the document"},
		{"an encoding that is not base64", `transferEncoding="package"`, good, "rather than base64"},
		{"a format the reference does not list", `contentType="image/svg+xml"`, good, "says it is image/svg+xml"},
		{"base64 that is not", `contentType="image/jpeg"`, "not base64 at all!!", "not the base64 it says it is"},
		{"bytes that are not a picture", `contentType="image/jpeg"`, "aGVsbG8gd29ybGQ=", "cannot be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, rep := openXFA(t, xfaFile(t, "template", imageTemplate(c.attrs, c.body)))
			if rep.Pictures != 0 {
				t.Fatalf("%d pictures were drawn", rep.Pictures)
			}
			if len(rep.Unplaced) != 1 || !strings.Contains(rep.Unplaced[0], c.want) {
				t.Errorf("the report says %v, wanted it to mention %q", rep.Unplaced, c.want)
			}
		})
	}
}

func TestHowBigAPictureIsDrawnInTheBoxMeasuredForIt(t *testing.T) {
	// The arithmetic is pdfium's (xfa/fxfa/cxfa_ffwidget.cpp:55-86). The box
	// is 100 by 50 and the picture 40 by 40, so the ratios are 2.5 across and
	// 1.25 down.
	for _, c := range []struct {
		aspect string
		w, h   float64
	}{
		{"", 50, 50},         // fit: the smaller ratio, 1.25, both ways
		{"fit", 50, 50},      //
		{"furlongs", 50, 50}, // anything the template writes that is not one of the five
		{"none", 100, 50},    // exactly the box, proportions be damned
		{"actual", 40, 40},   // its own size
		{"height", 50, 50},   // as tall as the box, scaled across by 1.25
		{"width", 100, 100},  // as wide as the box, scaled down by 2.5
	} {
		t.Run("aspect "+c.aspect, func(t *testing.T) {
			w, h := xfaFitPicture(c.aspect, 100, 50, 40, 40)
			if w != c.w || h != c.h {
				t.Errorf("came to %v by %v, wanted %v by %v", w, h, c.w, c.h)
			}
		})
	}
}

func TestAPictureWithNothingToFitAgainst(t *testing.T) {
	// A box with no width or no height is nothing to fit against, and a
	// picture of no size is nothing to fit.
	if w, h := xfaFitPicture("fit", 0, 50, 40, 40); w != 40 || h != 40 {
		t.Errorf("a box with no width gave %v by %v", w, h)
	}
	if w, h := xfaFitPicture("fit", 100, 50, 0, 40); w != 100 || h != 50 {
		t.Errorf("a picture with no width gave %v by %v", w, h)
	}
}

func TestFitIsBoundedByWhicheverSideRunsOutFirst(t *testing.T) {
	// The table above has the height running out first. A box that is tall and
	// narrow is bounded by its width instead, and taking the wrong one of the
	// two is how a picture comes out drawn past the edge of its box.
	if w, h := xfaFitPicture("fit", 50, 100, 40, 40); w != 50 || h != 50 {
		t.Errorf("a tall narrow box gave %v by %v, wanted 50 by 50", w, h)
	}
}

func TestAskingAnElementThatIsNotThereForAPicture(t *testing.T) {
	if pic, why := xfaPicture(xfa.Box{}, 0); pic != nil || why != "" {
		t.Errorf("an empty box gave %v, %q", pic, why)
	}
	if pic, why := xfaPicture(xfa.Box{Node: &xfa.FormNode{}}, 0); pic != nil || why != "" {
		t.Errorf("a node with no template gave %v, %q", pic, why)
	}
}

// numberedTemplate is a form whose footer asks the layout which sheet it is
// on, the way every one in the corpus that numbers its pages does: rich text
// holding a floating field that names a hidden field by its id, whose script
// is one line asking xfa.layout.
//
// The content area holds one draw, so the two in the body make two sheets.
func numberedTemplate(script1, script2, embed1, embed2 string) string {
	return `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="25pt"/>
	    <draw name="Footer" x="0pt" y="700pt" w="200pt" h="20pt"><value>
	      <exData contentType="text/html"><body xmlns="http://www.w3.org/1999/xhtml"
	        xmlns:xfa="http://www.xfa.org/schema/xfa-data/1.0/"
	        ><p>Page<span xfa:embed="` + embed1 + `"/> of<span xfa:embed="` + embed2 + `"/></p></body>
	      </exData></value></draw>
	  </pageArea></pageSet>
	  <subform name="Body" layout="tb">
	    <field name="Where" id="ff1" presence="hidden" w="1pt" h="1pt">
	      <calculate><script>` + script1 + `</script></calculate></field>
	    <field name="HowMany" id="ff2" presence="hidden" w="1pt" h="1pt">
	      <calculate><script>` + script2 + `</script></calculate></field>
	    <draw name="A" w="100pt" h="20pt"><value><text>first</text></value></draw>
	    <draw name="B" w="100pt" h="20pt"><value><text>second</text></value></draw>
	  </subform></subform></template>`
}

const askPage, askCount = "this.rawValue = xfa.layout.page(this);", "this.rawValue = xfa.layout.pageCount();"

func TestAFooterIsToldWhichSheetItIsOn(t *testing.T) {
	d, rep := openXFA(t, xfaFile(t, "template",
		numberedTemplate(askPage, askCount, "#ff1", "#ff2")))
	if rep.Sheets != 2 {
		t.Fatalf("%d sheets", rep.Sheets)
	}
	for i, want := range []string{"Page 1 of 2", "Page 2 of 2"} {
		got := strings.Join(strings.Fields(marksText(d.pages[i])), " ")
		if !strings.Contains(got, want) {
			t.Errorf("sheet %d says %q, wanted it to hold %q", i+1, got, want)
		}
	}
}

func TestAFloatingFieldThisDoesNotUnderstandIsLeftAsAGap(t *testing.T) {
	// A wrong number that looks right is worse than a gap. The four cerfa
	// fields carry a static default beside their script, and printing THAT
	// would put "Page 1 of 1" on every sheet of a four-sheet form.
	for _, c := range []struct {
		name           string
		s1, s2, e1, e2 string
	}{
		{"a script asking something else", "this.rawValue = 42;", askCount, "#ff1", "#ff2"},
		{"an id nothing carries", askPage, askCount, "#nosuchthing", "#ff2"},
		{"no script at all", "", "", "#ff1", "#ff2"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, _ := openXFA(t, xfaFile(t, "template",
				numberedTemplate(c.s1, c.s2, c.e1, c.e2)))
			got := strings.Join(strings.Fields(marksText(d.pages[0])), " ")
			if strings.Contains(got, "Page 1 of 2") {
				t.Errorf("it answered a question it does not understand: %q", got)
			}
			if !strings.Contains(got, "Page") {
				t.Errorf("the footer lost its own words too: %q", got)
			}
		})
	}
}

func TestTheCountIsRecognisedBeforeThePage(t *testing.T) {
	// "xfa.layout.page" is a prefix of "xfa.layout.pageCount", so the looser
	// test taken first would answer both with the sheet number and a
	// nine-sheet form would say "Page 3 of 3".
	if got := questionAsked(scriptNode(t, askCount)); got != asksCount {
		t.Errorf("pageCount was read as %q", got)
	}
	if got := questionAsked(scriptNode(t, askPage)); got != asksPage {
		t.Errorf("page was read as %q", got)
	}
	if got := questionAsked(scriptNode(t, "this.rawValue = 1;")); got != "" {
		t.Errorf("an ordinary script was read as %q", got)
	}
}

// scriptNode is a field carrying one script, for asking what it asks.
func scriptNode(t *testing.T, src string) *xfa.Node {
	t.Helper()
	n, err := xfa.ParseTemplate(strings.NewReader(
		`<template><subform><field><calculate><script>` + src +
			`</script></calculate></field></subform></template>`))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAskingNothingForAnAnswer(t *testing.T) {
	var none *sheetNumbers
	if got := none.answer("#whatever"); got != "" {
		t.Errorf("nothing answered %q", got)
	}
}
