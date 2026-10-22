package ops

import (
	"strings"
	"testing"

	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
)

// readBackForm writes the document and reads its form with the package that
// reads every form in the fleet.
//
// ⛔ THE ORACLE IS THE READER, not this file's expectations. A field written
// as something the format spells differently comes back as the wrong kind or
// not at all, rather than as a passing test.
func readBackForm(t *testing.T, d *Doc) *forms.Form {
	t.Helper()
	b, err := d.Bytes()
	if err != nil {
		t.Fatalf("writing the document: %v", err)
	}
	doc, err := reader.Open(b)
	if err != nil {
		t.Fatalf("the document this wrote cannot be opened: %v", err)
	}
	f, ok := forms.Read(doc)
	if !ok {
		t.Fatal("a document that was given fields has no form in it")
	}
	return f
}

func rect(x, y, w, h float64) [4]float64 { return [4]float64{x, y, x + w, y + h} }

// pdfWithAForm is a one-page document that already carries a form, so that
// adding to it can be told apart from replacing it.
func pdfWithAForm(t *testing.T) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	field := w.Add(reader.Dict{
		"FT": reader.Name("Tx"), "T": reader.String("name"),
		"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"),
		"Rect": reader.Array{reader.Real(20), reader.Real(100), reader.Real(180), reader.Real(130)},
		"P":    pageRef,
	})
	w.Put(pageRef, reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(200), reader.Integer(200)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")}),
		"Annots":   reader.Array{field},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"AcroForm": w.Add(reader.Dict{
			"Fields": reader.Array{field},
			"DA":     reader.String("/Helv 0 Tf 0 g"),
		})})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFieldsAddedToADocumentWithNoFormCanBeReadBack(t *testing.T) {
	d, err := Open(buildPDF(t, 2, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []forms.Field{
		{Name: "name", Kind: forms.Text, Widgets: []forms.Widget{{Page: 1, Rect: rect(20, 100, 160, 20)}}},
		{Name: "agree", Kind: forms.Checkbox, Value: "Yes",
			Widgets: []forms.Widget{{Page: 1, Rect: rect(20, 60, 14, 14), On: "Yes"}}},
		{Name: "size", Kind: forms.ComboBox,
			Options: []forms.Option{{Value: "s", Label: "Small"}, {Value: "l", Label: "Large"}},
			Widgets: []forms.Widget{{Page: 2, Rect: rect(20, 100, 120, 20)}}},
	}
	if err := d.AddFields(want...); err != nil {
		t.Fatalf("AddFields refused: %v", err)
	}
	f := readBackForm(t, d)

	if got := len(f.Fields()); got != len(want) {
		t.Fatalf("added %d fields and read %d back", len(want), got)
	}
	for _, w := range want {
		got, ok := f.Field(w.Name)
		if !ok {
			t.Errorf("%q was added and cannot be found", w.Name)
			continue
		}
		if got.Kind != w.Kind {
			t.Errorf("%q was added as a %s and read back as a %s", w.Name, w.Kind, got.Kind)
		}
		if len(got.Widgets) != 1 {
			t.Errorf("%q has %d widgets, wanted one", w.Name, len(got.Widgets))
			continue
		}
		if got.Widgets[0].Page != w.Widgets[0].Page {
			t.Errorf("%q is on page %d, was put on page %d",
				w.Name, got.Widgets[0].Page, w.Widgets[0].Page)
		}
		if got.Widgets[0].Rect != w.Widgets[0].Rect {
			t.Errorf("%q is at %v, was put at %v", w.Name, got.Widgets[0].Rect, w.Widgets[0].Rect)
		}
	}
}

// ⛔ A FIELD ADDED TO A DOCUMENT THAT ALREADY HAS A FORM JOINS IT. Replacing
// the form would drop every field the file came with — and the file would open,
// with the old fields simply gone.
func TestAFieldAddedToADocumentThatHasAFormJoinsIt(t *testing.T) {
	d, err := Open(pdfWithAForm(t))
	if err != nil {
		t.Fatal(err)
	}
	before, err := Open(pdfWithAForm(t))
	if err != nil {
		t.Fatal(err)
	}
	was := len(readBackForm(t, before).Fields())
	if was == 0 {
		t.Fatal("the fixture has no form, so this proves nothing")
	}

	if err := d.AddFields(forms.Field{
		Name: "added", Kind: forms.Text,
		Widgets: []forms.Widget{{Page: 1, Rect: rect(10, 10, 80, 16)}},
	}); err != nil {
		t.Fatal(err)
	}
	f := readBackForm(t, d)
	if got := len(f.Fields()); got != was+1 {
		t.Errorf("the form had %d fields and has %d after adding one", was, got)
	}
	if _, ok := f.Field("added"); !ok {
		t.Error("the added field is not there")
	}
	if _, ok := f.Field("name"); !ok {
		t.Error("a field the document came with was lost")
	}
}

// A field showing in several places is one field with several widgets, which
// is a parent and its Kids in the file.
func TestAFieldShowingOnTwoPagesIsOneFieldWithTwoWidgets(t *testing.T) {
	d, err := Open(buildPDF(t, 2, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(forms.Field{
		Name: "initials", Kind: forms.Text,
		Widgets: []forms.Widget{
			{Page: 1, Rect: rect(10, 10, 60, 16)},
			{Page: 2, Rect: rect(10, 10, 60, 16)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	f := readBackForm(t, d)
	if got := len(f.Fields()); got != 1 {
		t.Fatalf("read %d fields, wanted one", got)
	}
	got, ok := f.Field("initials")
	if !ok {
		t.Fatal("the field is gone")
	}
	if len(got.Widgets) != 2 {
		t.Fatalf("read %d widgets, wanted two", len(got.Widgets))
	}
	if got.Widgets[0].Page == got.Widgets[1].Page {
		t.Errorf("both widgets are on page %d", got.Widgets[0].Page)
	}
}

// ⛔ A BUTTON'S DRAWING IS NOT GENERATED BY THE VIEWER. NeedAppearances covers
// text and choice fields; a checkbox shows whatever its own appearance
// dictionary holds for the state it is in, so one written without them is a
// box that never looks ticked however often it is clicked.
func TestACheckboxIsWrittenWithADrawingForEachState(t *testing.T) {
	d, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(forms.Field{
		Name: "agree", Kind: forms.Checkbox, Value: "Yes",
		Widgets: []forms.Widget{{Page: 1, Rect: rect(20, 20, 14, 14), On: "Yes"}},
	}); err != nil {
		t.Fatal(err)
	}
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	page, err := doc.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	annots, ok := reader.ToArray(mustResolve(t, doc, page.Get("Annots")))
	if !ok || len(annots) != 1 {
		t.Fatalf("the page carries %v, wanted one annotation", annots)
	}
	w, ok := reader.ToDict(mustResolve(t, doc, annots[0]))
	if !ok {
		t.Fatal("the annotation is not a dictionary")
	}
	ap, ok := reader.ToDict(mustResolve(t, doc, w.Get("AP")))
	if !ok {
		t.Fatal("the checkbox has no appearance dictionary, so it can never look ticked")
	}
	n, ok := reader.ToDict(mustResolve(t, doc, ap.Get("N")))
	if !ok {
		t.Fatal("the appearance dictionary has no normal appearance")
	}
	for _, state := range []string{"Yes", "Off"} {
		if n.Get(reader.Name(state)) == nil {
			t.Errorf("there is no drawing for the state %q", state)
		}
	}
	if got := w.Get("AS"); got != reader.Name("Yes") {
		t.Errorf("the widget is in state %v, and the field's value chose Yes", got)
	}
}

// The form a viewer is asked to draw for itself: a text field written here has
// no appearance stream, so without this the form opens with every box empty
// however it was filled in.
func TestTheFormAsksTheViewerToDrawTheAppearances(t *testing.T) {
	d, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(forms.Field{Name: "a", Kind: forms.Text,
		Widgets: []forms.Widget{{Page: 1, Rect: rect(10, 10, 80, 16)}}}); err != nil {
		t.Fatal(err)
	}
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := doc.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	form, ok := doc.GetDict(cat, "AcroForm")
	if !ok {
		t.Fatal("there is no AcroForm")
	}
	if v, _ := reader.ToBool(mustResolve(t, doc, form.Get("NeedAppearances"))); !v {
		t.Error("NeedAppearances is not set, so a filled text field shows nothing")
	}
	if form.Get("DA") == nil {
		t.Error("the form names no default appearance, so there is no font to draw a value with")
	}
}

// A field with nowhere to show is still a field: it holds a value and can be
// filled, it simply cannot be clicked. Writing it keeps the document's own
// account of its fields complete.
func TestAFieldWithNoWidgetIsStillWritten(t *testing.T) {
	d, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(forms.Field{Name: "hidden", Kind: forms.Text, Value: "kept"}); err != nil {
		t.Fatal(err)
	}
	f := readBackForm(t, d)
	got, ok := f.Field("hidden")
	if !ok {
		t.Fatal("a field with no widget was not written")
	}
	if got.Value != "kept" {
		t.Errorf("its value came back %q", got.Value)
	}
}

// A radio button's mark is a disc rather than a cross, and a box too small for
// the inset still gets one that fits.
func TestARadioButtonAndATinyBoxBothGetAMark(t *testing.T) {
	d, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(
		forms.Field{Name: "size", Kind: forms.Radio, Value: "S", Widgets: []forms.Widget{
			{Page: 1, Rect: rect(10, 10, 12, 12), On: "S"},
			{Page: 1, Rect: rect(30, 10, 2, 2), On: "L"}, // smaller than the inset
		}},
		forms.Field{Name: "tiny", Kind: forms.Checkbox,
			Widgets: []forms.Widget{{Page: 1, Rect: rect(60, 10, 4, 4), On: "Yes"}}},
	); err != nil {
		t.Fatal(err)
	}
	f := readBackForm(t, d)
	if got, ok := f.Field("size"); !ok || len(got.Widgets) != 2 {
		t.Fatalf("the radio group came back as %v", got)
	}
	if _, ok := f.Field("tiny"); !ok {
		t.Error("the small checkbox is gone")
	}
}

// A widget the document asks not to draw is written with the hidden flag, and
// the reader reports it as hidden.
func TestAHiddenWidgetIsWrittenHidden(t *testing.T) {
	d, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(forms.Field{Name: "a", Kind: forms.Text,
		Widgets: []forms.Widget{{Page: 1, Rect: rect(10, 10, 80, 16), Hidden: true}}}); err != nil {
		t.Fatal(err)
	}
	f := readBackForm(t, d)
	got, ok := f.Field("a")
	if !ok || len(got.Widgets) != 1 {
		t.Fatal("the field is gone")
	}
	if !got.Widgets[0].Hidden {
		t.Error("the widget came back visible")
	}
}

// A checkbox whose value does not name this widget's state is in Off, which is
// the other arm of the branch that chooses the state.
func TestACheckboxThatIsNotTickedIsInTheOffState(t *testing.T) {
	d, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(forms.Field{Name: "agree", Kind: forms.Checkbox,
		Widgets: []forms.Widget{{Page: 1, Rect: rect(10, 10, 14, 14), On: "Yes"}}}); err != nil {
		t.Fatal(err)
	}
	f := readBackForm(t, d)
	got, ok := f.Field("agree")
	if !ok {
		t.Fatal("the field is gone")
	}
	if got.Value != "Off" {
		t.Errorf("an unticked checkbox holds %q", got.Value)
	}
}

func TestAddFieldsRefusesWhatCannotBeWritten(t *testing.T) {
	ok := forms.Widget{Page: 1, Rect: rect(10, 10, 80, 16)}
	for _, c := range []struct {
		why    string
		fields []forms.Field
		want   string
	}{
		{"a page the document does not have",
			[]forms.Field{{Name: "a", Kind: forms.Text,
				Widgets: []forms.Widget{{Page: 9, Rect: ok.Rect}}}}, "has 2"},
		{"no name", []forms.Field{{Kind: forms.Text, Widgets: []forms.Widget{ok}}}, "no name"},
		{"two fields of one name", []forms.Field{
			{Name: "a", Kind: forms.Text, Widgets: []forms.Widget{ok}},
			{Name: "a", Kind: forms.Text, Widgets: []forms.Widget{ok}}}, "both named"},
		{"a choice with no rows",
			[]forms.Field{{Name: "a", Kind: forms.ComboBox, Widgets: []forms.Widget{ok}}}, "no options"},
	} {
		d, err := Open(buildPDF(t, 2, nil))
		if err != nil {
			t.Fatal(err)
		}
		err = d.AddFields(c.fields...)
		if err == nil {
			t.Errorf("%s was accepted", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: the refusal says %q, which does not mention %q", c.why, err, c.want)
		}
	}
}

// ⛔ A SECOND CALL IS CHECKED AGAINST THE FIRST. Two fields of one name added
// in two calls are the same silent shadowing as two in one call, and checking
// only the new ones would let it through.
func TestASecondCallCannotRepeatANameTheFirstUsed(t *testing.T) {
	d, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	f := forms.Field{Name: "a", Kind: forms.Text,
		Widgets: []forms.Widget{{Page: 1, Rect: rect(10, 10, 80, 16)}}}
	if err := d.AddFields(f); err != nil {
		t.Fatal(err)
	}
	if err := d.AddFields(f); err == nil {
		t.Fatal("a name used by an earlier call was accepted")
	}
	// And the refusal left the first call's field alone.
	if got := len(readBackForm(t, d).Fields()); got != 1 {
		t.Errorf("the document has %d fields after a refused call, wanted one", got)
	}
}

// Fields can be added to a document assembled from several files, where there
// is no source form to join and no honest way to merge two.
func TestFieldsCanBeAddedToADocumentMergedFromTwoFiles(t *testing.T) {
	a, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(buildPDF(t, 1, nil))
	if err != nil {
		t.Fatal(err)
	}
	a.Append(b)
	if err := a.AddFields(forms.Field{Name: "a", Kind: forms.Text,
		Widgets: []forms.Widget{{Page: 2, Rect: rect(10, 10, 80, 16)}}}); err != nil {
		t.Fatal(err)
	}
	f := readBackForm(t, a)
	got, ok := f.Field("a")
	if !ok {
		t.Fatal("the field is not there")
	}
	if len(got.Widgets) != 1 || got.Widgets[0].Page != 2 {
		t.Errorf("the widget came back as %v", got.Widgets)
	}
}

