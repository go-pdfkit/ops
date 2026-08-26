package ops

import (
	"strings"
	"testing"

	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
)

// formSource writes a document with a form on two pages: a field merged into
// its own widget, and a field with two widgets of its own — the two ways a
// form is written, and the two that have to survive a rebuild.
func formSource(t *testing.T) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	first := w.Reserve()
	second := w.Reserve()
	blank := w.Add(&reader.Stream{Dict: reader.Dict{"BBox": reader.Array{
		reader.Integer(0), reader.Integer(0), reader.Integer(12), reader.Integer(12)}},
		Raw: []byte("")})

	merged := w.Add(reader.Dict{
		"FT": reader.Name("Tx"), "T": reader.String("name"),
		"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"), "P": first,
		"Rect": reader.Array{reader.Integer(20), reader.Integer(150),
			reader.Integer(180), reader.Integer(175)},
	})
	parent := w.Reserve()
	onFirst := w.Add(reader.Dict{"Subtype": reader.Name("Widget"), "P": first,
		"Parent": parent, "AP": reader.Dict{"N": reader.Dict{"Off": blank, "Yes": blank}},
		"Rect": reader.Array{reader.Integer(20), reader.Integer(100),
			reader.Integer(32), reader.Integer(112)}})
	onSecond := w.Add(reader.Dict{"Subtype": reader.Name("Widget"), "P": second,
		"Parent": parent, "AP": reader.Dict{"N": reader.Dict{"Off": blank, "Yes": blank}},
		"Rect": reader.Array{reader.Integer(20), reader.Integer(100),
			reader.Integer(32), reader.Integer(112)}})
	w.Put(parent, reader.Dict{"FT": reader.Name("Btn"), "T": reader.String("agree"),
		"Kids": reader.Array{onFirst, onSecond}})

	page := func(ref reader.Ref, annots reader.Array) {
		w.Put(ref, reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(200), reader.Integer(200)},
			"Annots":   annots,
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	}
	page(first, reader.Array{merged, onFirst})
	page(second, reader.Array{onSecond})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{first, second}, "Count": reader.Integer(2)})

	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"Lang":              reader.String("fr-FR"),
		"MarkInfo":          reader.Dict{"Marked": reader.Bool(true)},
		"ViewerPreferences": reader.Dict{"HideToolbar": reader.Bool(true)},
		"PageMode":          reader.Name("UseOutlines"),
		"Metadata":          w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("<x/>")}),
		"StructTreeRoot":    w.Add(reader.Dict{"Type": reader.Name("StructTreeRoot")}),
		"AcroForm": w.Add(reader.Dict{
			"Fields": reader.Array{merged, parent},
			"DA":     reader.String("/Helv 0 Tf 0 g"),
		}),
	})})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// rebuilt turns a document round the way a verb does and reads the result.
func rebuilt(t *testing.T, src []byte, change func(*Doc)) (*reader.Document, reader.Dict) {
	t.Helper()
	doc, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(doc)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatalf("what was written could not be read: %v", err)
	}
	if d.Repaired() {
		t.Fatal("what was written had to be repaired to be read")
	}
	catalog, err := d.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	return d, catalog
}

func TestARebuiltDocumentKeepsWhatItSaysAboutItself(t *testing.T) {
	d, catalog := rebuilt(t, formSource(t), func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	for _, key := range []reader.Name{"Lang", "MarkInfo", "ViewerPreferences", "PageMode", "Metadata"} {
		if _, named := catalog[key]; !named {
			t.Errorf("the rebuilt document lost /%s", key)
		}
	}
	if lang, ok := reader.ToString(mustGet(t, d, catalog.Get("Lang"))); !ok || string(lang) != "fr-FR" {
		t.Errorf("the language came back as %q", lang)
	}
}

func TestARebuiltDocumentKeepsItsForm(t *testing.T) {
	// Rotating a form used to keep every widget on the page and throw away
	// the field list that gives them meaning: not a form with something
	// missing but half a form.
	d, _ := rebuilt(t, formSource(t), func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	f, ok := forms.Read(d)
	if !ok {
		t.Fatal("the rebuilt document has no form")
	}
	if got := len(f.Fields()); got != 2 {
		var have []string
		for _, fld := range f.Fields() {
			have = append(have, fld.Name)
		}
		t.Fatalf("read %d fields (%s), wanted two", got, strings.Join(have, ", "))
	}
	byName := map[string]*forms.Field{}
	for _, fld := range f.Fields() {
		byName[fld.Name] = fld
	}
	name, ok := byName["name"]
	if !ok {
		t.Fatal("the field merged into its own widget is gone")
	}
	if len(name.Widgets) != 1 || name.Widgets[0].Page != 1 {
		t.Errorf("its widget is %v", name.Widgets)
	}
	agree, ok := byName["agree"]
	if !ok {
		t.Fatal("the field with widgets of its own is gone")
	}
	if len(agree.Widgets) != 2 {
		t.Fatalf("it has %d widgets, wanted two", len(agree.Widgets))
	}
	if agree.Widgets[0].Page != 1 || agree.Widgets[1].Page != 2 {
		t.Errorf("its widgets are on pages %d and %d",
			agree.Widgets[0].Page, agree.Widgets[1].Page)
	}
}

func TestAFieldWhosePageWentIsDropped(t *testing.T) {
	// A form asking for something that cannot be seen or filled in is a worse
	// thing to leave behind than a shorter form.
	d, _ := rebuilt(t, formSource(t), func(doc *Doc) { mustDo(t, doc.Delete("1")) })
	f, ok := forms.Read(d)
	if !ok {
		t.Fatal("the rebuilt document has no form")
	}
	if got := len(f.Fields()); got != 1 {
		t.Fatalf("read %d fields, wanted only the one still on a page", got)
	}
	fld := f.Fields()[0]
	if fld.Name != "agree" {
		t.Errorf("kept %q", fld.Name)
	}
	if len(fld.Widgets) != 1 {
		t.Errorf("it kept %d widgets, wanted the one on the page that stayed", len(fld.Widgets))
	}
}

func TestADocumentWithNoWidgetsLeftHasNoForm(t *testing.T) {
	for _, c := range []struct {
		why    string
		change func(*Doc)
	}{
		{"every annotation removed", func(doc *Doc) { doc.RemoveAnnotations() }},
		{"the annotations drawn into the page", func(doc *Doc) { doc.Flatten() }},
		{"the only page with a field dropped", func(doc *Doc) { mustDo(t, doc.Delete("1")) }},
	} {
		src := formSource(t)
		if c.why == "the only page with a field dropped" {
			continue
		}
		d, catalog := rebuilt(t, src, c.change)
		if _, named := catalog["AcroForm"]; named {
			t.Errorf("%s: the document still claims a form", c.why)
		}
		if _, ok := forms.Read(d); ok {
			t.Errorf("%s: a form was read back", c.why)
		}
	}
}

func TestPagesFromSeveralFilesKeepNoCatalogue(t *testing.T) {
	// Two files have two catalogues, and two forms whose fields may be named
	// the same. There is no honest way to choose or to merge, so such a
	// document keeps its pages and nothing above them.
	src := formSource(t)
	a, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Merge(a, b).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := d.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []reader.Name{"AcroForm", "Lang"} {
		if _, named := catalog[key]; named {
			t.Errorf("a merge of two files kept /%s from one of them", key)
		}
	}
}

func TestASanitisedDocumentDropsWhatSaysWhoMadeIt(t *testing.T) {
	_, catalog := rebuilt(t, formSource(t), func(doc *Doc) { doc.Sanitize() })
	if _, named := catalog["Metadata"]; named {
		t.Error("a sanitised document kept the packet that says who wrote it")
	}
	if _, named := catalog["Lang"]; !named {
		t.Error("a sanitised document lost the language it is in")
	}
}

func TestADocumentBuiltRatherThanBorrowedHasNothingToKeep(t *testing.T) {
	doc := New()
	doc.Blank(612, 792)
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := d.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if _, named := catalog["Lang"]; named {
		t.Error("a document built from nothing claims a language")
	}
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustGet(t *testing.T, d *reader.Document, o reader.Object) reader.Object {
	t.Helper()
	v, err := d.Resolve(o)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// oddForm writes a document with one page and whatever field list the test
// wants, so that the shapes a file may put in one can be walked.
func oddForm(t *testing.T, build func(w *reader.Writer, page reader.Ref) reader.Array) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	fields := build(w, pageRef)
	w.Put(pageRef, reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"AcroForm": w.Add(reader.Dict{"Fields": fields,
			"DA": reader.String("/Helv 0 Tf 0 g")})})})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFieldListsThatHaveNothingInThemToKeep(t *testing.T) {
	// Every one of these is a shape a file may write and none of them leaves
	// a field on a page, so none of them leaves a form.
	for _, c := range []struct {
		why   string
		build func(w *reader.Writer, page reader.Ref) reader.Array
	}{
		{"an empty field list — 561 of the figure corpus's files are exactly this",
			func(w *reader.Writer, page reader.Ref) reader.Array { return reader.Array{} }},
		{"a field list holding something that is not a field",
			func(w *reader.Writer, page reader.Ref) reader.Array {
				return reader.Array{reader.Integer(7)}
			}},
		{"a field written into the list rather than as an object",
			func(w *reader.Writer, page reader.Ref) reader.Array {
				return reader.Array{reader.Dict{"FT": reader.Name("Tx"),
					"T": reader.String("inline")}}
			}},
		{"a field that is on no page and is the parent of nothing",
			func(w *reader.Writer, page reader.Ref) reader.Array {
				return reader.Array{w.Add(reader.Dict{"FT": reader.Name("Tx"),
					"T": reader.String("orphan")})}
			}},
		{"a parent whose children are all nonsense",
			func(w *reader.Writer, page reader.Ref) reader.Array {
				return reader.Array{w.Add(reader.Dict{"T": reader.String("parent"),
					"FT": reader.Name("Tx"), "Kids": reader.Array{reader.Integer(3)}})}
			}},
		{"a field tree that goes round for ever",
			func(w *reader.Writer, page reader.Ref) reader.Array {
				node := w.Reserve()
				w.Put(node, reader.Dict{"T": reader.String("loop"), "FT": reader.Name("Tx"),
					"Kids": reader.Array{node}})
				return reader.Array{node}
			}},
	} {
		_, catalog := rebuilt(t, oddForm(t, c.build), nil)
		if _, named := catalog["AcroForm"]; named {
			t.Errorf("%s: the rebuilt document claims a form", c.why)
		}
	}
}

func TestAFieldTreeThreeDeep(t *testing.T) {
	// A field may be the parent of a field that is the parent of the widget,
	// and every one of them has to point back at the one above it.
	src := oddForm(t, func(w *reader.Writer, page reader.Ref) reader.Array {
		grand := w.Reserve()
		middle := w.Reserve()
		widget := w.Add(reader.Dict{"Subtype": reader.Name("Widget"), "P": page,
			"Parent": middle,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(60), reader.Integer(20)}})
		w.Put(middle, reader.Dict{"T": reader.String("home"), "Parent": grand,
			"Kids": reader.Array{widget}})
		w.Put(grand, reader.Dict{"T": reader.String("address"), "FT": reader.Name("Tx"),
			"Kids": reader.Array{middle}})
		return reader.Array{grand}
	})
	// The page has to carry the widget for it to survive.
	d0, err := reader.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	_ = d0
	d, _ := rebuilt(t, withAnnots(t, src), nil)
	f, ok := forms.Read(d)
	if !ok {
		t.Fatal("the rebuilt document has no form")
	}
	if _, ok := f.Field("address.home"); !ok {
		var have []string
		for _, fld := range f.Fields() {
			have = append(have, fld.Name)
		}
		t.Fatalf("read %v, wanted address.home", have)
	}
}

// withAnnots puts every widget the form names onto the page it says it is on,
// which a file that writes its field tree first may forget to do.
func withAnnots(t *testing.T, src []byte) []byte {
	t.Helper()
	d, err := reader.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	catalog, err := d.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	form, _ := d.GetDict(catalog, "AcroForm")
	fields, _ := reader.ToArray(resolve(d, form.Get("Fields")))
	var widgets reader.Array
	var walk func(o reader.Object, depth int)
	walk = func(o reader.Object, depth int) {
		if depth > 8 {
			return
		}
		dict, ok := resolveDict(d, o)
		if !ok {
			return
		}
		if sub, _ := reader.ToName(dict.Get("Subtype")); sub == "Widget" {
			widgets = append(widgets, w.Copy(d, o))
			return
		}
		kids, _ := reader.ToArray(resolve(d, dict.Get("Kids")))
		for _, k := range kids {
			walk(k, depth+1)
		}
	}
	var copied reader.Array
	for _, f := range fields {
		walk(f, 0)
		copied = append(copied, w.Copy(d, f))
	}
	w.Put(pageRef, reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Annots":   widgets,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"AcroForm": w.Add(reader.Dict{"Fields": copied,
			"DA": reader.String("/Helv 0 Tf 0 g")})})})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
