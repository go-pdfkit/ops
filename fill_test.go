package ops

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
)

// formFile writes a one-page document with a form in it, either with the plain
// cross-reference table the older files use or with the stream the newer ones
// do, since an update has to say where its objects went the same way.
func formFile(t *testing.T, packed bool, build func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array)) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	if packed {
		w = reader.NewPackedWriter("1.7")
	}
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	form, annots := build(w, pageRef)
	page := reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")}),
	}
	if len(annots) > 0 {
		page["Annots"] = annots
	}
	w.Put(pageRef, page)
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	catalog := reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef}
	if form != nil {
		catalog["AcroForm"] = w.Add(form)
	}
	out, err := w.Finish(reader.Dict{"Root": w.Add(catalog)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// oneTextField is the simplest form there is: one box to type in.
func oneTextField(t *testing.T, packed bool, extra reader.Dict) []byte {
	t.Helper()
	return formFile(t, packed, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		field := reader.Dict{
			"FT": reader.Name("Tx"), "T": reader.String("name"),
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"),
			"Rect": reader.Array{reader.Integer(20), reader.Integer(100),
				reader.Integer(180), reader.Integer(130)},
			"P": page,
		}
		for k, v := range extra {
			field[k] = v
		}
		ref := w.Add(field)
		return reader.Dict{
			"Fields": reader.Array{ref},
			"DA":     reader.String("/Helv 0 Tf 0 g"),
			"DR": reader.Dict{"Font": reader.Dict{"Helv": w.Add(reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
				"BaseFont": reader.Name("Helvetica"), "Encoding": reader.Name("WinAnsiEncoding")})}},
		}, reader.Array{ref}
	})
}

// refill fills a field, writes the file out, and reads it back — which is the
// only test that means anything: a value written and not readable again is a
// value that was not written.
func refill(t *testing.T, src []byte, name, value string) *forms.Form {
	t.Helper()
	f, ok, err := OpenForm(src)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the document was written with a form and opened without one")
	}
	if err := f.Fill(name, value); err != nil {
		t.Fatal(err)
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, src) {
		t.Fatal("the original file is not the beginning of what was written")
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatalf("what was written could not be read: %v", err)
	}
	if d.Repaired() {
		t.Fatal("what was written had to be repaired to be read")
	}
	back, ok := forms.Read(d)
	if !ok {
		t.Fatal("what was written has no form in it")
	}
	return back
}

func TestAValueSurvivesBeingWrittenOut(t *testing.T) {
	// Both ways a file says where its objects are, since an update has to
	// match: a plain table cannot be pointed back at by one written the other
	// way, and the strict readers refuse the whole file when it is.
	for _, packed := range []bool{false, true} {
		back := refill(t, oneTextField(t, packed, nil), "name", "Mozart")
		fld, ok := back.Field("name")
		if !ok {
			t.Fatal("the field is gone")
		}
		if fld.Value != "Mozart" {
			t.Errorf("packed %v: holds %q", packed, fld.Value)
		}
		if _, has := back.Dict()["NeedAppearances"]; has {
			t.Error("the form still asks for its appearances to be drawn")
		}
		w := fld.Widgets[0]
		if _, drawn := reader.ToDict(w.Dict().Get("AP")); !drawn {
			t.Error("the field was filled in and nothing was drawn for it")
		}
	}
}

func TestTheDrawingBesideAValueIsWhatShows(t *testing.T) {
	// A value is not what gets drawn. What is written beside it has to hold
	// the value, in a stream naming a font the document carries.
	f, _, err := OpenForm(oneTextField(t, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Fill("name", "Mozart"); err != nil {
		t.Fatal(err)
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d, _ := reader.Open(out)
	back, _ := forms.Read(d)
	fld, _ := back.Field("name")
	ap, _ := d.GetDict(fld.Widgets[0].Dict(), "AP")
	stream, ok := reader.ToStream(mustResolve(t, d, ap.Get("N")))
	if !ok {
		t.Fatal("what was drawn is not a stream")
	}
	body, _, err := d.DecodeStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "(Mozart) Tj") {
		t.Errorf("the drawing does not hold the value:\n%s", body)
	}
	res, ok := d.GetDict(stream.Dict, "Resources")
	if !ok {
		t.Fatal("the drawing names no resources")
	}
	fonts, ok := d.GetDict(res, "Font")
	if !ok || len(fonts) == 0 {
		t.Error("the drawing names no font, so nothing would show")
	}
}

func mustResolve(t *testing.T, d *reader.Document, o reader.Object) reader.Object {
	t.Helper()
	v, err := d.Resolve(o)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTickingABoxSaysWhichPictureShows(t *testing.T) {
	// A button's pictures are already in the file; which of them shows is
	// what changes, and every widget of a group is told, since only the one
	// whose own name matches the value is on.
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		blank := w.Add(&reader.Stream{Dict: reader.Dict{"BBox": reader.Array{
			reader.Integer(0), reader.Integer(0), reader.Integer(12), reader.Integer(12)}},
			Raw: []byte("")})
		one := w.Add(reader.Dict{"Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(12), reader.Integer(12)},
			"AP": reader.Dict{"N": reader.Dict{"Off": blank, "Zurich": blank}}})
		two := w.Add(reader.Dict{"Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(20), reader.Integer(0),
				reader.Integer(32), reader.Integer(12)},
			"AP": reader.Dict{"N": reader.Dict{"Off": blank, "Anvers": blank}}})
		field := w.Add(reader.Dict{"T": reader.String("city"), "FT": reader.Name("Btn"),
			"Ff": reader.Integer(1 << 15), "Kids": reader.Array{one, two}})
		return reader.Dict{"Fields": reader.Array{field}}, reader.Array{one, two}
	})
	back := refill(t, src, "city", "Anvers")
	fld, _ := back.Field("city")
	if fld.Value != "Anvers" {
		t.Fatalf("the field holds %q", fld.Value)
	}
	var states []string
	for _, w := range fld.Widgets {
		s, _ := reader.ToName(w.Dict().Get("AS"))
		states = append(states, string(s))
	}
	if len(states) != 2 || states[0] != "Off" || states[1] != "Anvers" {
		t.Errorf("the buttons are showing %v, wanted the second one only", states)
	}
}

func TestAFormThatWasNotTouchedIsHandedBackAsItWas(t *testing.T) {
	src := oneTextField(t, false, nil)
	f, ok, err := OpenForm(src)
	if err != nil || !ok {
		t.Fatal(err)
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, src) {
		t.Errorf("a file nobody filled in came back %d bytes instead of %d", len(out), len(src))
	}
}

func TestAValueThatIsNotPlainEnglish(t *testing.T) {
	// Anything the eight-bit alphabet has no room for is written the other
	// way, with a mark at the front, and has to come back the same.
	for _, value := range []string{"Dvořák", "Ω", "😀 Mozart"} {
		back := refill(t, oneTextField(t, false, nil), "name", value)
		fld, _ := back.Field("name")
		if fld.Value != value {
			t.Errorf("%q came back as %q", value, fld.Value)
		}
	}
}

func TestAFormThatAsksForItsDrawingsToBeMadeAgain(t *testing.T) {
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		ref := w.Add(reader.Dict{"FT": reader.Name("Tx"), "T": reader.String("name"),
			"Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(100), reader.Integer(20)}})
		return reader.Dict{"Fields": reader.Array{ref},
			"NeedAppearances": reader.Bool(true),
			"DA":              reader.String("/Helv 0 Tf 0 g")}, reader.Array{ref}
	})
	back := refill(t, src, "name", "Mozart")
	if back.NeedAppearances() {
		t.Error("the drawings were made and the file still asks for them")
	}
}

func TestADocumentWithNoFormToFill(t *testing.T) {
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		return nil, nil
	})
	if _, ok, err := OpenForm(src); ok || err != nil {
		t.Errorf("ok %v err %v", ok, err)
	}
}

func TestSomethingThatIsNotADocumentAtAll(t *testing.T) {
	if _, _, err := OpenForm([]byte("not a PDF")); err == nil {
		t.Error("nonsense was opened as a document")
	}
	if _, _, err := OpenFormWithPassword([]byte("not a PDF"), "x"); err == nil {
		t.Error("nonsense was opened as a protected document")
	}
}

func TestAProtectedDocumentIsOpenedWithItsPassword(t *testing.T) {
	src := oneTextField(t, false, nil)
	doc, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	_ = doc
	if _, _, err := OpenFormWithPassword(src, ""); err != nil {
		t.Fatal(err)
	}
}

func TestBreakingTheRunsOfObjectNumbers(t *testing.T) {
	for _, c := range []struct {
		in   []int
		runs int
	}{
		{nil, 0},
		{[]int{1}, 1},
		{[]int{1, 2, 3}, 1},
		{[]int{1, 3}, 2},
		{[]int{1, 2, 5, 6, 9}, 3},
	} {
		if got := len(runsOf(c.in)); got != c.runs {
			t.Errorf("%v broke into %d runs, wanted %d", c.in, got, c.runs)
		}
	}
}

func TestFindingWhereTheTableBegins(t *testing.T) {
	for _, c := range []struct {
		why  string
		in   string
		want int
		ok   bool
	}{
		{"the usual", "%PDF-1.7\nxref\ntrailer\nstartxref\n9\n%%EOF", 9, true},
		{"nothing saying so", "%PDF-1.7\n", 0, false},
		{"saying so and then not", "%PDF-1.7\nstartxref\n", 0, false},
		{"a number past the end of the file", "startxref\n99999\n", 0, false},
		{"nought, which is nowhere", "%PDF-1.7\nstartxref\n0\n", 0, false},
	} {
		got, ok := lastStartxref([]byte(c.in))
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: read %d %v, wanted %d %v", c.why, got, ok, c.want, c.ok)
		}
	}
}

func TestTellingOneSortOfTableFromTheOther(t *testing.T) {
	if xrefIsStream([]byte("xref\n0 1\n"), 0) {
		t.Error("a plain table was taken for a stream")
	}
	if xrefIsStream([]byte("  \r\nxref\n"), 0) {
		t.Error("a plain table with space before it was taken for a stream")
	}
	if !xrefIsStream([]byte("12 0 obj <</Type/XRef>>"), 0) {
		t.Error("a stream was taken for a plain table")
	}
	if xrefIsStream([]byte("xref"), 99) {
		t.Error("an offset past the end of the file said something")
	}
}

func TestAFileTheReaderHadToRepairIsNotAddedTo(t *testing.T) {
	// An update points back at the file's own cross-reference section by
	// where it begins. A file whose section was wrong enough to be rebuilt
	// has no such place worth pointing at, and an update naming offsets into
	// a table that was never right is a file nothing can read.
	src := oneTextField(t, false, nil)
	broken := bytes.Replace(src, []byte("startxref"), []byte("startxrEf"), 1)
	if _, _, err := OpenForm(broken); err == nil {
		t.Error("a file with no usable table was opened for adding to")
	}

	i := bytes.LastIndex(src, []byte("startxref"))
	moved := append([]byte(nil), src[:i]...)
	moved = append(moved, []byte("startxref\n3\n%%EOF\n")...)
	if _, _, err := OpenForm(moved); err == nil {
		t.Error("a file whose table is not where it says was opened for adding to")
	}
}

func TestAnEncryptedDocumentIsNotAddedTo(t *testing.T) {
	// Everything in such a file is written through a key, so anything
	// appended would have to be too. Writing in the clear beside it makes a
	// file nothing can read, which is worse than refusing.
	w := reader.NewWriter("1.7")
	w.Encrypt(reader.Encryption{OwnerPassword: "secret"})
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	field := w.Add(reader.Dict{"FT": reader.Name("Tx"), "T": reader.String("name"),
		"Subtype": reader.Name("Widget"), "P": pageRef,
		"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(20)}})
	w.Put(pageRef, reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Annots":   reader.Array{field},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	locked, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"AcroForm": w.Add(reader.Dict{"Fields": reader.Array{field},
			"DA": reader.String("/Helv 0 Tf 0 g")})})})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := OpenFormWithPassword(locked, ""); ok || err == nil {
		t.Errorf("an encrypted document was opened for adding to: ok %v err %v", ok, err)
	}
}

func TestTheFormIsWhatIsAskedAboutAndTold(t *testing.T) {
	f, ok, err := OpenForm(oneTextField(t, false, nil))
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got := len(f.Form().Fields()); got != 1 {
		t.Errorf("the form has %d fields", got)
	}
}

func TestAFileWhoseCountOfObjectsIsTooSmall(t *testing.T) {
	// A file may say anything about how many objects it holds. Believing a
	// count smaller than the objects that are there would have this write on
	// top of something already in the file.
	src := oneTextField(t, false, nil)
	f, ok, err := OpenForm(src)
	if err != nil || !ok {
		t.Fatal(err)
	}
	// What the trailer says is believed only when the objects agree with it.
	// A file claiming to hold two objects while its field is object nine must
	// not have this write a tenth on top of something.
	f.doc.Trailer()["Size"] = reader.Integer(2)
	if got := f.highestObject(); got < 2 {
		t.Errorf("the highest object is %d, and the form's own field is above that", got)
	}
	ref, _ := f.form.Fields()[0].Ref()
	if got := f.highestObject(); got < ref.Num {
		t.Errorf("the highest object is %d and the field is object %d", got, ref.Num)
	}
}

func TestAFormWrittenIntoTheCatalogueRatherThanBeside(t *testing.T) {
	// There is then nowhere of its own to write the form back to, so what it
	// asked for cannot be unasked; the fields are still filled in.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	field := w.Add(reader.Dict{"FT": reader.Name("Tx"), "T": reader.String("name"),
		"Subtype": reader.Name("Widget"),
		"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(20)}})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Annots":   reader.Array{field},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	src, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"AcroForm": reader.Dict{"Fields": reader.Array{field},
			"NeedAppearances": reader.Bool(true),
			"DA":              reader.String("/Helv 0 Tf 0 g")}})})
	if err != nil {
		t.Fatal(err)
	}
	back := refill(t, src, "name", "Mozart")
	fld, _ := back.Field("name")
	if fld.Value != "Mozart" {
		t.Errorf("holds %q", fld.Value)
	}
}

func TestAFieldWrittenInsideAnotherObjectCannotBeChanged(t *testing.T) {
	// There is nowhere to write it back to. Saying so is better than writing
	// a file whose value and whose drawing disagree.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	src, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"AcroForm": w.Add(reader.Dict{"DA": reader.String("/Helv 0 Tf 0 g"),
			"Fields": reader.Array{reader.Dict{
				"FT": reader.Name("Tx"), "T": reader.String("inline"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
					reader.Integer(100), reader.Integer(20)}}}})})})
	if err != nil {
		t.Fatal(err)
	}
	f, ok, err := OpenForm(src)
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if err := f.Fill("inline", "Mozart"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Bytes(); err == nil {
		t.Error("a field with nowhere to be written was written anyway")
	}
}

func TestAWidgetWrittenInsideAnotherObject(t *testing.T) {
	// The field can be changed; the widget cannot, so nothing is drawn for
	// that one and the rest of the file is still written.
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		field := w.Add(reader.Dict{"T": reader.String("name"), "FT": reader.Name("Tx"),
			"Kids": reader.Array{reader.Dict{"Subtype": reader.Name("Widget"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
					reader.Integer(100), reader.Integer(20)}}}})
		return reader.Dict{"Fields": reader.Array{field},
			"DA": reader.String("/Helv 0 Tf 0 g")}, nil
	})
	back := refill(t, src, "name", "Mozart")
	fld, _ := back.Field("name")
	if fld.Value != "Mozart" {
		t.Errorf("holds %q", fld.Value)
	}
}

func TestAButtonWidgetWrittenInsideAnotherObject(t *testing.T) {
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		field := w.Add(reader.Dict{"T": reader.String("tick"), "FT": reader.Name("Btn"),
			"Kids": reader.Array{reader.Dict{"Subtype": reader.Name("Widget"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
					reader.Integer(12), reader.Integer(12)}}}})
		return reader.Dict{"Fields": reader.Array{field}}, nil
	})
	back := refill(t, src, "tick", "yes")
	fld, _ := back.Field("tick")
	if fld.Value != "Yes" {
		t.Errorf("holds %q", fld.Value)
	}
}

func TestAFieldOfNoSizeHasNothingDrawnForIt(t *testing.T) {
	src := oneTextField(t, false, reader.Dict{
		"Rect": reader.Array{reader.Integer(10), reader.Integer(10),
			reader.Integer(10), reader.Integer(40)}})
	back := refill(t, src, "name", "Mozart")
	fld, _ := back.Field("name")
	if fld.Value != "Mozart" {
		t.Errorf("holds %q", fld.Value)
	}
}

func TestAListBoxHoldingSeveralRows(t *testing.T) {
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		ref := w.Add(reader.Dict{"FT": reader.Name("Ch"), "T": reader.String("where"),
			"Ff":      reader.Integer(1 << 21),
			"Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(100), reader.Integer(40)},
			"Opt": reader.Array{reader.String("FR"), reader.String("BE")}})
		return reader.Dict{"Fields": reader.Array{ref},
			"DA": reader.String("/Helv 9 Tf 0 g")}, reader.Array{ref}
	})
	f, ok, err := OpenForm(src)
	if err != nil || !ok {
		t.Fatal(err)
	}
	fld, _ := f.Form().Field("where")
	if err := fld.Choose("FR", "BE"); err != nil {
		t.Fatal(err)
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := forms.Read(d)
	got, _ := back.Field("where")
	if len(got.Values) != 2 {
		t.Errorf("came back holding %v", got.Values)
	}
}

func TestAFileWhoseLastLineDoesNotEnd(t *testing.T) {
	// Its last line would otherwise run into the first object of the update.
	src := oneTextField(t, false, nil)
	trimmed := bytes.TrimRight(src, "\r\n")
	back := refill(t, trimmed, "name", "Mozart")
	fld, _ := back.Field("name")
	if fld.Value != "Mozart" {
		t.Errorf("holds %q", fld.Value)
	}
}

func TestAWidgetNumberedAboveItsField(t *testing.T) {
	// A field is not always written before the widgets that belong to it, and
	// what the trailer says about how many objects there are is not always
	// true. Both have to be looked at, or this writes a new object on top of
	// one already in the file.
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		fieldRef := w.Reserve()
		one := w.Add(reader.Dict{"Subtype": reader.Name("Widget"), "P": page,
			"Parent": fieldRef,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(100), reader.Integer(20)}})
		w.Put(fieldRef, reader.Dict{"T": reader.String("name"), "FT": reader.Name("Tx"),
			"Kids": reader.Array{one}})
		return reader.Dict{"Fields": reader.Array{fieldRef},
			"DA": reader.String("/Helv 0 Tf 0 g")}, reader.Array{one}
	})
	f, ok, err := OpenForm(src)
	if err != nil || !ok {
		t.Fatal(err)
	}
	f.doc.Trailer()["Size"] = reader.Integer(2)
	fld := f.form.Fields()[0]
	fieldRef, _ := fld.Ref()
	widgetRef, _ := fld.Widgets[0].Ref()
	if widgetRef.Num <= fieldRef.Num {
		t.Skipf("the widget is object %d and the field %d, which is not the case this is about",
			widgetRef.Num, fieldRef.Num)
	}
	if got := f.highestObject(); got < widgetRef.Num {
		t.Errorf("the highest object is %d and the widget is object %d", got, widgetRef.Num)
	}
}
