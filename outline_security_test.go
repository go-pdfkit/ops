package ops

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/reader"
)

// Each test here is a finding of the security review of the outlining: the
// refusal is asserted, and so is how much was allocated before it, since a
// refusal that comes after the work has been done is no bound at all.

// allocated runs f and says how many bytes it allocated, in total.
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// outlineBounded outlines page 1 and writes, asserting the refusal and that
// no more than ceiling bytes were allocated on the way.
func outlineRefused(t *testing.T, src []byte, ceiling uint64) {
	t.Helper()
	var err error
	got := allocated(func() { err = outlineErr(t, src, "all") })
	if !errors.Is(err, ErrOutlineTooLarge) {
		t.Fatalf("err = %v, want ErrOutlineTooLarge", err)
	}
	if got > ceiling {
		t.Errorf("allocated %d MiB before refusing, ceiling %d MiB", got>>20, ceiling>>20)
	}
}

func TestTheByteBoundIsCumulativeAndRefusesEarly(t *testing.T) {
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	maxOutlineBytes = 2 << 20
	for _, mode := range []string{"0", "7"} {
		content := "BT /F1 12 Tf " + mode + " Tr " + strings.Repeat("(@) Tj ", 200000) + "ET"
		outlineRefused(t, buildDoc(t, 1, content, stdFont, nil), 256<<20)
	}
}

func TestQStackDepthIsCapped(t *testing.T) {
	src := buildDoc(t, 1, strings.Repeat("q ", 100000), stdFont, nil)
	outlineRefused(t, src, 64<<20)
	ok := buildDoc(t, 1, strings.Repeat("q ", 28)+"BT /F1 12 Tf (x) Tj ET", stdFont, nil)
	if err := outlineErr(t, ok, "1"); err != nil {
		t.Errorf("28 nested q is the limit of the standard: %v", err)
	}
}

// chainedType3 is n Type 3 fonts, each glyph of which draws four of the next.
func chainedType3(n int) func(w *reader.Writer, res reader.Dict) {
	return func(w *reader.Writer, res reader.Dict) {
		refs := make([]reader.Ref, n)
		for i := range refs {
			refs[i] = w.Reserve()
		}
		for i := range refs {
			glyph := "1000 0 d0 0 0 500 500 re f"
			var rd reader.Dict
			if i+1 < n {
				glyph = "1000 0 d0 BT /N 1000 Tf (AAAA) Tj ET"
				rd = reader.Dict{"Font": reader.Dict{"N": refs[i+1]}}
			}
			proc := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(glyph)})
			font := reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("Type3"),
				"FontBBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
				"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0),
					reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
				"CharProcs": reader.Dict{"tri": proc},
				"Encoding": reader.Dict{"Type": reader.Name("Encoding"),
					"Differences": reader.Array{reader.Integer(65), reader.Name("tri")}},
				"FirstChar": reader.Integer(65), "LastChar": reader.Integer(65),
				"Widths": reader.Array{reader.Integer(1000)}}
			if rd != nil {
				font["Resources"] = rd
			}
			w.Put(refs[i], font)
		}
		res["Font"] = reader.Dict{"F1": refs[0]}
	}
}

func TestType3FontsThatDrawEachOtherAreDoneOncePerDocument(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 12 Tf (AAAA) Tj ET", chainedType3(10), nil)
	var out []byte
	got := allocated(func() { out = outlinedBytes(t, src, "1") })
	if got > 128<<20 {
		t.Errorf("allocated %d MiB: the glyphs were drawn once per path, not once per document", got>>20)
	}
	if len(out) > 1<<20 {
		t.Errorf("%d bytes written for a 10-font chain", len(out))
	}
}

func TestType3FormResourcesAreCharged(t *testing.T) {
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	maxOutlineBytes = 1 << 20
	big := reader.Array{}
	for i := 0; i < 20000; i++ {
		big = append(big, reader.Integer(i))
	}
	build := func(w *reader.Writer, res reader.Dict) {
		fonts := reader.Dict{}
		for i := 0; i < 60; i++ {
			proc := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("1000 0 d0 0 0 5 5 re f")})
			fonts[reader.Name(fmt.Sprintf("T%d", i))] = w.Add(reader.Dict{"Type": reader.Name("Font"),
				"Subtype":  reader.Name("Type3"),
				"FontBBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
				"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0),
					reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
				"CharProcs": reader.Dict{"tri": proc},
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
		fmt.Fprintf(&content, "/T%d 12 Tf (A) Tj ", i)
	}
	content.WriteString("ET")
	outlineRefused(t, buildDoc(t, 1, content.String(), build, nil), 256<<20)
}

func TestPagesSharingAStreamAreOutlinedOnceAndChargedEach(t *testing.T) {
	content := "BT /F1 12 Tf (AAAAAAAA) Tj ET"
	src := buildDoc(t, 50, content, stdFont, nil)
	d, _ := Open(src)
	if err := d.OutlineText("all"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Bytes(); err != nil {
		t.Fatal(err)
	}
	for _, o := range d.outliners {
		if o.glyphs != 8 {
			t.Errorf("%d glyphs drawn for 50 pages of one stream, want the 8 of one", o.glyphs)
		}
	}
	defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
	maxOutlineBytes = 200
	if err := outlineErr(t, buildDoc(t, 50, strings.Repeat("0 0 1 1 re f ", 4), stdFont, nil), "all"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("a shared stream was free the second time: %v", err)
	}
}

func TestTheTextBehindTheOutlinesIsGone(t *testing.T) {
	prop := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		res["Properties"] = reader.Dict{"P1": reader.Dict{"ActualText": reader.String("secret"),
			"MCID": reader.Integer(3)}}
	}
	mask := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		g := w.Add(&reader.Stream{Dict: reader.Dict{"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":      reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)},
			"Resources": reader.Dict{"Font": res["Font"]}}, Raw: []byte("BT /F1 12 Tf (secret) Tj ET")})
		res["ExtGState"] = reader.Dict{"G1": reader.Dict{"SMask": reader.Dict{"Type": reader.Name("Mask"),
			"S": reader.Name("Luminosity"), "G": g}}}
	}
	cases := map[string]struct {
		content string
		build   func(w *reader.Writer, res reader.Dict)
	}{
		"inline ActualText": {"/Span <</ActualText (secret)>> BDC BT /F1 12 Tf (x) Tj ET EMC", stdFont},
		"inline Alt":        {"/Figure <</Alt (secret) /E (secret)>> BDC BT /F1 12 Tf (x) Tj ET EMC", stdFont},
		"properties":        {"/Span /P1 BDC BT /F1 12 Tf (x) Tj ET EMC", prop},
		"soft mask":         {"/G1 gs 0 0 10 10 re f", mask},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := outlinedBytes(t, buildDoc(t, 1, c.content, c.build, nil), "1")
			d, err := reader.Open(out)
			if err != nil {
				t.Fatal(err)
			}
			if s, _ := extract.Text(d, 1); strings.TrimSpace(s) != "" {
				t.Errorf("the extractor still reads %q", s)
			}
			if bytes.Contains(out, []byte("secret")) || bytes.Contains(out, []byte("Helvetica")) {
				t.Error("the text or its font is still in the file")
			}
			if strings.Contains(pageContentOf(t, out, 1), "ActualText") {
				t.Error("the property list is still in the content")
			}
		})
	}
}

func TestAClipWithoutETOrBehindACmKeepsItsPlace(t *testing.T) {
	plain := pageContentOf(t, outlinedBytes(t, buildDoc(t, 1, "BT /F1 40 Tf 7 Tr (A) Tj ET 0 0 50 50 re f", stdFont, nil), "1"), 1)
	for name, content := range map[string]string{
		"no ET": "BT /F1 40 Tf 7 Tr (A) Tj",
		"cm":    "BT /F1 40 Tf 7 Tr (A) Tj 2 0 0 2 5 5 cm ET",
	} {
		out := pageContentOf(t, outlinedBytes(t, buildDoc(t, 1, content, stdFont, nil), "1"), 1)
		if !strings.Contains(out, "W n") {
			t.Errorf("%s: the clip was dropped: %q", name, out)
		}
	}
	if !strings.Contains(plain, "W n") {
		t.Errorf("the ordinary clip was dropped: %q", plain)
	}
	flat := "BT /F1 40 Tf 7 Tr (A) Tj 0 0 0 0 0 0 cm ET"
	if err := outlineErr(t, buildDoc(t, 1, flat, stdFont, nil), "1"); !errors.Is(err, ErrOutlineTooLarge) {
		t.Errorf("a clip behind a matrix that cannot be undone: %v", err)
	}
}

func TestAHugeCoordinateIsRefusedNotWritten(t *testing.T) {
	for _, n := range []string{strings.Repeat("9", 300), "100000000000000000000"} {
		src := buildDoc(t, 1, "BT /F1 12 Tf 1 0 0 1 "+n+" 0 Tm (A) Tj ET", stdFont, nil)
		if err := outlineErr(t, src, "1"); !errors.Is(err, ErrOutlineTooLarge) {
			t.Errorf("%.10s...: err = %v", n, err)
		}
	}
}

func TestEveryAppearanceThatStillHoldsTextIsCounted(t *testing.T) {
	var fontRef reader.Ref
	form := func(w *reader.Writer, res reader.Dict, content string, extra reader.Dict) reader.Ref {
		d := reader.Dict{"Subtype": reader.Name("Form"),
			"BBox":      reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(50), reader.Integer(20)},
			"Resources": reader.Dict{"Font": reader.Dict{"F1": fontRef}}}
		for k, v := range extra {
			d[k] = v
		}
		return w.Add(&reader.Stream{Dict: d, Raw: []byte(content)})
	}
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		fontRef = res["Font"].(reader.Dict)["F1"].(reader.Ref)
	}
	extra := func(w *reader.Writer, page reader.Dict) {
		text := "BT /F1 9 Tf (x) Tj ET"
		rect := reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(50), reader.Integer(20)}
		inner := form(w, nil, text, nil)
		nested := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"), "BBox": rect,
			"Resources": reader.Dict{"XObject": reader.Dict{"X": inner}}}, Raw: []byte("/X Do")})
		stamp := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"), "BBox": rect},
			Raw: []byte("0 0 5 5 re f")})
		annot := func(d reader.Dict) reader.Ref {
			d["Type"], d["Rect"] = reader.Name("Annot"), rect
			return w.Add(d)
		}
		page["Annots"] = reader.Array{
			annot(reader.Dict{"Subtype": reader.Name("Stamp"),
				"AP": reader.Dict{"N": reader.Dict{"On": form(w, nil, text, nil)}}}),
			annot(reader.Dict{"Subtype": reader.Name("Stamp"), "AP": reader.Dict{"N": nested}}),
			annot(reader.Dict{"Subtype": reader.Name("FreeText"), "Contents": reader.String("secret"),
				"AP": reader.Dict{"N": stamp}}),
			annot(reader.Dict{"Subtype": reader.Name("Widget"), "FT": reader.Name("Tx"),
				"V": reader.String("secret"), "AP": reader.Dict{"N": stamp}}),
			annot(reader.Dict{"Subtype": reader.Name("Stamp"), "AP": reader.Dict{"N": stamp}}),
		}
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
	if got := d.OutlineSkipped(); got != 4 {
		t.Errorf("OutlineSkipped = %d, want 4 (a bare stamp is not one)", got)
	}
}
