package ops

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// sweepBound outlines a document under every byte bound from 1 up to what it
// costs with room to spare, and requires of each that it either finishes or is
// refused by ErrOutlineTooLarge: the point where a charge fails is wherever
// the bound is, so every charge is made to refuse in turn, and none lets work
// past the bound unnoticed.
func sweepBound(t *testing.T, src []byte) {
	t.Helper()
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	defer func() { traceCharge = nil }()
	var charges []int
	traceCharge = func(n int) { charges = append(charges, n) }
	d, _ := Open(src)
	if err := d.OutlineText("all"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Bytes(); err != nil {
		t.Fatal(err)
	}
	all := append([]int(nil), charges...)
	wrote, read := 0, 0
	for _, o := range d.outliners {
		wrote, read = wrote+o.wrote, read+o.read
	}
	refused := 0
	for b := 1; b <= wrote+read+1; b++ {
		maxOutlineBytes = b
		charges = nil
		d, _ := Open(src)
		if err := d.OutlineText("all"); err != nil {
			t.Fatal(err)
		}
		_, err := d.Bytes()
		// Refused exactly when a total is past the bound, and nothing is charged
		// after the charge that crossed it: an ignored refusal shows as either.
		want, sum, cut := wrote > b || read > b, 0, len(all)
		for i, n := range all {
			if sum += n; sum > b {
				cut = i + 1
				break
			}
		}
		if len(charges) > cut || !slices.Equal(charges, all[:len(charges)]) {
			t.Fatalf("bound %d: charges %v, want a prefix of %v up to %d", b, charges, all, cut)
		}
		switch {
		case err == nil && !want:
		case errors.Is(err, ErrOutlineTooLarge) && want:
			refused++
		default:
			t.Fatalf("bound %d: err = %v, refusal wanted: %v", b, err, want)
		}
	}
	if refused == 0 {
		t.Error("no bound refused anything")
	}
}

func formDict(res reader.Dict) reader.Dict {
	return reader.Dict{"Subtype": reader.Name("Form"),
		"BBox":      reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)},
		"Resources": res}
}

func TestEveryChargeRefusesWhereTheBoundFalls(t *testing.T) {
	text := "BT /F1 12 Tf (xy) Tj ET"
	docs := map[string][]byte{
		"type 3": buildDoc(t, 1, "BT /F1 12 Tf (AAAA) Tj ET 0 0 1 1 re f", chainedType3(3), nil),
		"form": buildDoc(t, 1, "/Fm Do BT /F1 7 Tf (a) Tj ET", func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			res["XObject"] = reader.Dict{"Fm": w.Add(&reader.Stream{Dict: formDict(reader.Dict{"Font": res["Font"]}), Raw: []byte(text)})}
		}, nil),
		"soft mask": buildDoc(t, 1, "/G1 gs 0 0 10 10 re f", func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			g := w.Add(&reader.Stream{Dict: formDict(reader.Dict{"Font": res["Font"]}), Raw: []byte(text)})
			res["ExtGState"] = reader.Dict{"G1": reader.Dict{"SMask": reader.Dict{"S": reader.Name("Alpha"), "G": g}},
				"G2": reader.Integer(1), "G3": reader.Dict{"Font": reader.Array{}, "SMask": reader.Name("None")}}
		}, nil),
		"pattern": buildDoc(t, 1, "/Pattern cs /P scn 0 0 10 10 re f", func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			d := formDict(reader.Dict{"Font": res["Font"]})
			d["PatternType"], d["PaintType"], d["TilingType"] = reader.Integer(1), reader.Integer(1), reader.Integer(1)
			d["XStep"], d["YStep"] = reader.Integer(10), reader.Integer(10)
			res["Pattern"] = reader.Dict{"P": w.Add(&reader.Stream{Dict: d, Raw: []byte(text)})}
		}, nil),
		"properties": buildDoc(t, 1, "/Span /P1 BDC BT /F1 12 Tf (x) Tj ET EMC", func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			res["Properties"] = reader.Dict{"P1": reader.Dict{"ActualText": reader.String("s"), "K": reader.Integer(1)}}
		}, nil),
		"clip": buildDoc(t, 1, "BT /F1 12 Tf 7 Tr (a) Tj 2 0 0 2 0 0 cm (b) Tj ET", stdFont, nil),
	}
	for name, src := range docs {
		t.Run(name, func(t *testing.T) { sweepBound(t, src) })
	}
}

func TestAClipThatCannotBePutBackIsRefused(t *testing.T) {
	huge, tiny := "1"+strings.Repeat("0", 200), "0."+strings.Repeat("0", 199)+"1"
	for name, content := range map[string]string{
		"left open":   "BT /F1 12 Tf 7 Tr (a) Tj 0 0 0 0 0 0 cm",
		"overflow":    "BT /F1 12 Tf 7 Tr " + huge + " 0 0 " + huge + " 0 0 cm (a) Tj " + tiny + " 0 0 " + tiny + " 0 0 cm ET",
		"coordinates": "BT /F1 12 Tf 7 Tr (a) Tj 1 0 0 1 2000000000 0 cm ET",
	} {
		if err := outlineErr(t, buildDoc(t, 1, content, stdFont, nil), "1"); !errors.Is(err, ErrOutlineTooLarge) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestMarkedContentIsBoundedAndTagsAreSanitised(t *testing.T) {
	if err := outlineErr(t, buildDoc(t, 1, strings.Repeat("/Span BMC ", 300), stdFont, nil), "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("300 nested sequences: %v", err)
	}
	for name, tag := range map[string]string{"empty": "/#20", "long": "/" + strings.Repeat("a", 70), "odd": "/a#20b"} {
		content := tag + " <</ActualText (secret)>> BDC BT /F1 12 Tf (x) Tj ET EMC /Span <</MCID 1>> BDC EMC 5 Do"
		out := pageContentOf(t, outlinedBytes(t, buildDoc(t, 1, content, stdFont, nil), "1"), 1)
		if !strings.Contains(out, "/Span BMC") || strings.Contains(out, "secret") {
			t.Errorf("%s: %q", name, out)
		}
	}
}

func TestTheNumbersOfAType3GlyphAreBounded(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 12 Tf 1 0 0 1 2000000000 0 Tm (A) Tj ET", type3Font("1000 0 d0 0 0 5 5 re f"), nil)
	if err := outlineErr(t, src, "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("err = %v", err)
	}
	deep := reader.Array{}
	for i := 0; i < 40; i++ {
		deep = reader.Array{deep}
	}
	src = buildDoc(t, 1, "0 0 1 1 re f", func(w *reader.Writer, res reader.Dict) {
		res["ProcSet"] = deep
	}, nil)
	if err := outlineErr(t, src, "1"); err != nil {
		t.Errorf("a deep resource: %v", err)
	}
}

func TestAnAppearanceThatDrawsFormsIsLookedIntoAsFarAsIsBounded(t *testing.T) {
	var fontRef reader.Ref
	rect := reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(50), reader.Integer(20)}
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		fontRef = res["Font"].(reader.Dict)["F1"].(reader.Ref)
	}
	count := func(t *testing.T, ap func(w *reader.Writer) reader.Object) int {
		t.Helper()
		extra := func(w *reader.Writer, page reader.Dict) {
			page["Annots"] = reader.Array{w.Add(reader.Dict{"Type": reader.Name("Annot"),
				"Subtype": reader.Name("Stamp"), "Rect": rect, "AP": reader.Dict{"N": ap(w)}})}
		}
		d, err := Open(buildDoc(t, 1, "0 0 1 1 re f", build, extra))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.OutlineText("1"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Bytes(); err != nil {
			t.Fatal(err)
		}
		return d.OutlineSkipped()
	}
	form := func(w *reader.Writer, xo reader.Dict, font bool) reader.Ref {
		res := reader.Dict{"XObject": xo}
		if font {
			res["Font"] = reader.Dict{"F1": fontRef}
		}
		return w.Add(&reader.Stream{Dict: formDict(res), Raw: []byte("0 0 1 1 re f")})
	}
	// Two annotations drawing the same form with a font: the second is told so
	// by the verdict already reached.
	{
		extra := func(w *reader.Writer, page reader.Dict) {
			shared := w.Add(&reader.Stream{Dict: formDict(reader.Dict{"Font": reader.Dict{"F1": fontRef}}), Raw: []byte("0 0 1 1 re f")})
			outer := func() reader.Ref {
				return w.Add(&reader.Stream{Dict: formDict(reader.Dict{"XObject": reader.Dict{"S": shared}}), Raw: []byte("/S Do")})
			}
			page["Annots"] = reader.Array{
				w.Add(reader.Dict{"Subtype": reader.Name("Stamp"), "AP": reader.Dict{"N": outer()}}),
				w.Add(reader.Dict{"Subtype": reader.Name("Stamp"), "AP": reader.Dict{"N": outer()}}),
			}
		}
		d, _ := Open(buildDoc(t, 1, "0 0 1 1 re f", build, extra))
		if err := d.OutlineText("1"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Bytes(); err != nil {
			t.Fatal(err)
		}
		if got := d.OutlineSkipped(); got != 2 {
			t.Errorf("a shared form with a font: %d, want 2", got)
		}
	}
	// Forms drawn twice, one holding a font and one not, and a member that is no stream.
	if got := count(t, func(w *reader.Writer) reader.Object {
		bare, texty := form(w, nil, false), form(w, nil, true)
		return form(w, reader.Dict{"A": bare, "B": bare, "C": texty, "D": texty, "E": reader.Integer(1)}, false)
	}); got != 1 {
		t.Errorf("a form drawing one with a font: %d, want 1", got)
	}
	// The same form without a font reached twice is looked at once.
	if got := count(t, func(w *reader.Writer) reader.Object {
		bare := form(w, nil, false)
		return form(w, reader.Dict{"A": bare, "B": bare}, false)
	}); got != 0 {
		t.Errorf("a form reached twice: %d, want 0", got)
	}
	// A form drawing only forms without one is no text.
	if got := count(t, func(w *reader.Writer) reader.Object {
		return form(w, reader.Dict{"A": form(w, nil, false), "B": reader.Integer(1)}, false)
	}); got != 0 {
		t.Errorf("forms without a font: %d, want 0", got)
	}
	// Nested too deep to be looked at: the worst is assumed.
	if got := count(t, func(w *reader.Writer) reader.Object {
		inner := form(w, nil, false)
		for i := 0; i < maxOutlineNesting+3; i++ {
			inner = form(w, reader.Dict{"X": inner}, false)
		}
		return inner
	}); got != 1 {
		t.Errorf("a form nested too deep: %d, want 1", got)
	}
}
