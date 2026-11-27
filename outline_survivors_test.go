package ops

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// What these assert is what mutation of the guards found nothing holding: a
// value on each side of the guard, not only that something is refused.

func TestAResourceIsCostedInFull(t *testing.T) {
	deep := reader.Array{}
	for i := 0; i < 40; i++ {
		deep = reader.Array{deep}
	}
	// Every node is counted, however deep: nothing below a depth is free.
	if got, want := objCost(deep), 41*outlineNodeCost; got != want {
		t.Errorf("objCost = %d, want %d", got, want)
	}
	for name, c := range map[string]struct {
		o    reader.Object
		want int
	}{
		"dictionary": {reader.Dict{"ab": reader.Integer(1)}, 2*outlineNodeCost + 2},
		"string":     {reader.String("abcde"), outlineNodeCost + 5},
		"name":       {reader.Name("xyz"), outlineNodeCost + 3},
		"array":      {reader.Array{reader.String("ab"), reader.Name("c")}, 3*outlineNodeCost + 3},
	} {
		if got := objCost(c.o); got != c.want {
			t.Errorf("%s: objCost = %d, want %d", name, got, c.want)
		}
	}
}

func TestASoftMaskThatIsNotADictionaryIsKeptAsItIs(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["ExtGState"] = reader.Dict{"G": reader.Dict{"SMask": reader.Name("None"), "LW": reader.Integer(2)}}
	}
	out := outlinedBytes(t, buildDoc(t, 1, "/G gs BT /F1 12 Tf (x) Tj ET", build, nil), "1")
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	pg, _ := d.Page(1)
	res, _ := d.GetDict(pg, "Resources")
	gs, _ := d.GetDict(res, "ExtGState")
	g, _ := d.GetDict(gs, "G")
	if n, ok := reader.ToName(g["SMask"]); !ok || n != "None" {
		t.Errorf("SMask = %v, want /None", g["SMask"])
	}
}

func TestAFontSharedByTwoFormsIsWrittenOnce(t *testing.T) {
	build := func(w *reader.Writer, res reader.Dict) {
		chainedType3(1)(w, res)
		form := func() reader.Object {
			return w.Add(&reader.Stream{Dict: formDict(reader.Dict{"Font": res["Font"]}), Raw: []byte("BT /F1 12 Tf (A) Tj ET")})
		}
		res["XObject"] = reader.Dict{"Fm1": form(), "Fm2": form()}
	}
	defer func() { traceCharge = nil }()
	forms := 0
	traceCharge = func(n int) {
		if n == outlineFormCost {
			forms++
		}
	}
	outlinedBytes(t, buildDoc(t, 1, "/Fm1 Do /Fm2 Do", build, nil), "1")
	// Two forms and one glyph: the second form finds the glyph written.
	if forms != 3 {
		t.Errorf("%d forms charged, want 3", forms)
	}
}

func TestAClipWhoseUndoCannotBeInvertedIsRefused(t *testing.T) {
	huge, tiny := "1"+strings.Repeat("0", 200), "0."+strings.Repeat("0", 149)+"1"
	content := "BT /F1 12 Tf 7 Tr " + tiny + " 0 0 " + tiny + " 0 0 cm (a) Tj " + huge + " 0 0 " + huge + " 0 0 cm ET"
	if err := outlineErr(t, buildDoc(t, 1, content, stdFont, nil), "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("err = %v", err)
	}
}
