package ops

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// Each test here is a finding of the second security review of the outlining.

func TestPropertyListsHeldAsObjectsLoseTheirText(t *testing.T) {
	text := reader.Dict{"ActualText": reader.String("secret"), "MCID": reader.Integer(3)}
	cases := map[string]func(w *reader.Writer, res reader.Dict){
		"direct": func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			res["Properties"] = reader.Dict{"P1": text}
		},
		"the dictionary an object": func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			res["Properties"] = w.Add(reader.Dict{"P1": text})
		},
		"the list an object": func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			res["Properties"] = reader.Dict{"P1": w.Add(text)}
		},
		"both objects": func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			res["Properties"] = w.Add(reader.Dict{"P1": w.Add(text)})
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			out := outlinedBytes(t, buildDoc(t, 1, "/Span /P1 BDC BT /F1 12 Tf (x) Tj ET EMC", build, nil), "1")
			if bytes.Contains(out, []byte("secret")) {
				t.Error("the ActualText is still in the file")
			}
			if !bytes.Contains(out, []byte("/MCID 3")) {
				t.Error("the marked content's number was lost with its text")
			}
		})
	}
}

func TestAListThatNoTextWasDrawnOverIsKept(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["Properties"] = w.Add(reader.Dict{"P1": w.Add(reader.Dict{"Alt": reader.String("a figure")}),
			"P2": reader.Dict{"MCID": reader.Integer(5)}})
	}
	out := outlinedBytes(t, buildDoc(t, 1, "/Figure /P1 BDC 0 0 1 1 re f EMC", build, nil), "1")
	if !bytes.Contains(out, []byte("a figure")) {
		t.Error("the description of a figure, which no text was drawn over, was dropped")
	}
	if !bytes.Contains(out, []byte("/MCID 5")) {
		t.Error("a list with no text in it was dropped")
	}
}

func TestAStringIsChargedByItsLength(t *testing.T) {
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	maxOutlineBytes = 1 << 20
	big := reader.String(strings.Repeat("x", 20000))
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		fonts := reader.Dict{}
		for i := 0; i < 60; i++ {
			proc := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("1000 0 d0 0 0 5 5 re f")})
			fonts[reader.Name("T"+string(rune('A'+i%26))+string(rune('a'+i/26)))] = w.Add(reader.Dict{
				"Type": reader.Name("Font"), "Subtype": reader.Name("Type3"),
				"FontBBox":   reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
				"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0), reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
				"CharProcs":  reader.Dict{"tri": proc},
				"Encoding": reader.Dict{"Type": reader.Name("Encoding"),
					"Differences": reader.Array{reader.Integer(65), reader.Name("tri")}},
				"FirstChar": reader.Integer(65), "LastChar": reader.Integer(65),
				"Widths":    reader.Array{reader.Integer(1000)},
				"Resources": reader.Dict{"ProcSet": big}})
		}
		res["Font"] = fonts
	}
	var content strings.Builder
	content.WriteString("BT ")
	for i := 0; i < 60; i++ {
		content.WriteString("/T" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + " 12 Tf (A) Tj ")
	}
	content.WriteString("ET")
	outlineRefused(t, buildDoc(t, 1, content.String(), build, nil), 256<<20)
}

func TestTheStructureOfAnOutlinedPageCarriesNoWordsOfItsOwn(t *testing.T) {
	g := newTagged()
	one := g.elem("P", reader.Dict{"Pg": g.page[0], "ActualText": reader.String("secretone"),
		"Alt": reader.String("altone"), "E": reader.String("expandedone"), "Lang": reader.String("en")}, reader.Integer(0))
	two := g.elem("P", reader.Dict{"Pg": g.page[1], "ActualText": reader.String("keeptwo")}, reader.Integer(0))
	src := g.finish(t, reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": g.elem("Document", nil, one, two)})
	out := outlinedBytes(t, src, "1")
	for _, gone := range []string{"secretone", "altone", "expandedone"} {
		if bytes.Contains(out, []byte(gone)) {
			t.Errorf("%q is still in the file", gone)
		}
	}
	if !bytes.Contains(out, []byte("keeptwo")) || !bytes.Contains(out, []byte("(en)")) {
		t.Error("an element on a page that was not outlined, or what is not words, was dropped")
	}
}

func TestAStampOnOnePageDoesNotLandOnThePagesSharingItsResources(t *testing.T) {
	src := buildDoc(t, 3, "BT /F1 12 Tf (x) Tj ET", stdFont, nil)
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.OutlineText("all"); err != nil {
		t.Fatal(err)
	}
	if err := d.Stamp("1", Stamp{Text: "X", Font: HelveticaBold, Size: 12}); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	r, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	for n, want := range map[int]bool{1: true, 2: false, 3: false} {
		pg, _ := r.Page(n)
		res, _ := r.GetDict(pg, "Resources")
		if _, has := res["Font"]; has != want {
			t.Errorf("page %d: has /Font = %v, want %v", n, has, want)
		}
	}
}

// directType3 is a Type 3 font held as a direct dictionary in a font dictionary
// that is an object.
func directType3(w *reader.Writer, res reader.Dict) {
	proc := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("1000 0 d0 0 0 500 500 re f")})
	res["Font"] = w.Add(reader.Dict{"F1": reader.Dict{"Type": reader.Name("Font"),
		"Subtype":  reader.Name("Type3"),
		"FontBBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
		"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0),
			reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
		"CharProcs": reader.Dict{"tri": proc},
		"Encoding": reader.Dict{"Type": reader.Name("Encoding"),
			"Differences": reader.Array{reader.Integer(65), reader.Name("tri")}},
		"FirstChar": reader.Integer(65), "LastChar": reader.Integer(65),
		"Widths": reader.Array{reader.Integer(1000)}}})
}

func TestADirectType3FontIsWrittenOncePerDocument(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		directType3(w, res)
		form := func() reader.Object {
			return w.Add(&reader.Stream{Dict: formDict(reader.Dict{"Font": res["Font"]}), Raw: []byte("BT /F1 12 Tf (A) Tj ET")})
		}
		res["XObject"] = reader.Dict{"Fm1": form(), "Fm2": form(), "Fm3": form()}
	}
	defer func() { traceCharge = nil }()
	forms := 0
	traceCharge = func(n int) {
		if n == outlineFormCost {
			forms++
		}
	}
	outlinedBytes(t, buildDoc(t, 1, "/Fm1 Do /Fm2 Do /Fm3 Do", build, nil), "1")
	// Three forms and one glyph.
	if forms != 4 {
		t.Errorf("%d forms charged, want 4", forms)
	}
}

func TestAClipIsPutBackInTheSpaceQAndQLeave(t *testing.T) {
	content := "BT /F1 12 Tf 7 Tr (a) Tj q 2 0 0 2 0 0 cm Q (b) Tj ET"
	out := pageContentOf(t, outlinedBytes(t, buildDoc(t, 1, content, stdFont, nil), "1"), 1)
	if strings.Contains(out, " cm\n") {
		t.Errorf("the clip was written under a matrix that q and Q had undone: %q", out)
	}
	inside := "BT /F1 12 Tf 7 Tr (a) Tj q 2 0 0 2 0 0 cm (b) Tj Q ET"
	out = pageContentOf(t, outlinedBytes(t, buildDoc(t, 1, inside, stdFont, nil), "1"), 1)
	if !strings.Contains(out, " cm\n") {
		t.Errorf("a glyph drawn under a cm was put back as if there had been none: %q", out)
	}
}

func TestAnnotationsWithTextOfTheirOwnAreCounted(t *testing.T) {
	extra := func(w *reader.Writer, page reader.Dict) {
		rect := reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(9), reader.Integer(9)}
		annot := func(sub reader.Name, more reader.Dict) reader.Object {
			d := reader.Dict{"Type": reader.Name("Annot"), "Subtype": sub, "Rect": rect}
			for k, v := range more {
				d[k] = v
			}
			return w.Add(d)
		}
		field := w.Add(reader.Dict{"FT": reader.Name("Tx"), "V": reader.String("inherited")})
		loop := w.Reserve()
		w.Put(loop, reader.Dict{"FT": reader.Name("Tx"), "Parent": loop})
		page["Annots"] = reader.Array{
			annot("Text", reader.Dict{"Contents": reader.String("a note")}),
			annot("Highlight", reader.Dict{"Contents": reader.String("")}),
			annot("Widget", reader.Dict{"Parent": field}),
			annot("Widget", reader.Dict{"Parent": loop}),
			annot("Widget", reader.Dict{"V": reader.Name("Yes")}),
		}
	}
	d, _ := Open(buildDoc(t, 1, "BT /F1 18 Tf (x) Tj ET", stdFont, extra))
	if err := d.OutlineText("1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Bytes(); err != nil {
		t.Fatal(err)
	}
	if got := d.OutlineSkipped(); got != 2 {
		t.Errorf("OutlineSkipped() = %d, want 2: a note and an inherited value", got)
	}
}
