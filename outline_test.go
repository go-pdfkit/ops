// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-gfx/gfx/raster"
	"github.com/go-opentype/fonts"
	"github.com/go-opentype/opentype"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/render"
)

// The way outlining is judged is by looking: the page is drawn by
// go-pdfkit/render before and after, and the two pictures are compared. A
// test that read the paths back would only prove that the code agrees with
// itself.

// buildDoc is a document of pages that share one content stream and one
// resource dictionary, which build fills in. extra may add to each page.
func buildDoc(t *testing.T, pages int, content string, build func(w *reader.Writer, res reader.Dict),
	extra func(w *reader.Writer, page reader.Dict)) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	res := reader.Dict{}
	build(w, res)
	resRef := w.Add(res)
	stream := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content)})
	var kids reader.Array
	for i := 0; i < pages; i++ {
		page := reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
			"Contents": stream, "Resources": resRef}
		if extra != nil {
			extra(w, page)
		}
		kids = append(kids, w.Add(page))
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": kids,
		"Count":    reader.Integer(pages),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(200), reader.Integer(100)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func stdFont(w *reader.Writer, res reader.Dict) {
	res["Font"] = reader.Dict{"F1": w.Add(reader.Dict{"Type": reader.Name("Font"),
		"Subtype": reader.Name("Type1"), "BaseFont": reader.Name("Helvetica"),
		"Encoding": reader.Name("WinAnsiEncoding")})}
}

func widths(first, last, v int) reader.Array {
	var a reader.Array
	for i := first; i <= last; i++ {
		a = append(a, reader.Integer(v))
	}
	return a
}

func trueTypeFont(w *reader.Writer, res reader.Dict) {
	ttf := fonts.MostLegible()
	file := w.Add(&reader.Stream{Dict: reader.Dict{"Length1": reader.Integer(len(ttf))}, Raw: ttf})
	desc := w.Add(reader.Dict{"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(32), "FontFile2": file})
	res["Font"] = reader.Dict{"F1": w.Add(reader.Dict{"Type": reader.Name("Font"),
		"Subtype": reader.Name("TrueType"), "BaseFont": reader.Name("Test"),
		"FirstChar": reader.Integer(32), "LastChar": reader.Integer(126), "Widths": widths(32, 126, 600),
		"FontDescriptor": desc, "Encoding": reader.Name("WinAnsiEncoding")})}
}

func cidTrueTypeFont(w *reader.Writer, res reader.Dict) {
	ttf := fonts.MostLegible()
	file := w.Add(&reader.Stream{Dict: reader.Dict{"Length1": reader.Integer(len(ttf))}, Raw: ttf})
	desc := w.Add(reader.Dict{"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(4), "FontFile2": file})
	kid := w.Add(reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("CIDFontType2"),
		"BaseFont": reader.Name("Test"), "FontDescriptor": desc, "DW": reader.Integer(600)})
	res["Font"] = reader.Dict{"F1": w.Add(reader.Dict{"Type": reader.Name("Font"),
		"Subtype": reader.Name("Type0"), "BaseFont": reader.Name("Test"),
		"Encoding": reader.Name("Identity-H"), "DescendantFonts": reader.Array{kid}})}
}

// glyphIDs spells a string as the two-byte codes the font above is addressed by.
func glyphIDs(t *testing.T, s string) string {
	t.Helper()
	f, err := opentype.Parse(fonts.MostLegible())
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	sb.WriteString("<")
	for _, r := range s {
		gid, ok := f.GlyphIndex(r)
		if !ok {
			t.Fatalf("the test font has no %q", r)
		}
		fmt.Fprintf(&sb, "%04x", gid)
	}
	sb.WriteString(">")
	return sb.String()
}

// cffIndexOf and cffInt lay out a CFF font the way go-pdfkit/render's own tests
// do: a font with one drawable glyph, a square, addressed by identifier 7.
func cffIndexOf(items [][]byte) []byte {
	if len(items) == 0 {
		return []byte{0, 0}
	}
	out := []byte{byte(len(items) >> 8), byte(len(items)), 4}
	at := uint32(1)
	for _, it := range items {
		out = binary.BigEndian.AppendUint32(out, at)
		at += uint32(len(it))
	}
	out = binary.BigEndian.AppendUint32(out, at)
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func cffInt(v int) []byte {
	return append([]byte{29}, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func squareCFF(cid int) []byte {
	square := []byte{139, 139, 21, 248, 136, 139, 5, 139, 248, 136, 5, 252, 136, 139, 5, 14}
	charStrings := cffIndexOf([][]byte{{14}, square})
	charset := append([]byte{0}, byte(cid>>8), byte(cid))
	name := cffIndexOf([][]byte{[]byte("T")})
	strs := cffIndexOf([][]byte{[]byte("Adobe"), []byte("Identity")})
	gsubrs := cffIndexOf(nil)
	build := func(charsetAt, charStringsAt int) []byte {
		var d []byte
		d = append(d, cffInt(391)...)
		d = append(d, cffInt(392)...)
		d = append(d, cffInt(0)...)
		d = append(d, 12, 30)
		d = append(d, cffInt(charsetAt)...)
		d = append(d, 15)
		d = append(d, cffInt(charStringsAt)...)
		d = append(d, 17)
		return d
	}
	header := []byte{1, 0, 4, 1}
	topLen := len(cffIndexOf([][]byte{build(0, 0)}))
	before := len(header) + len(name) + topLen + len(strs) + len(gsubrs)
	top := cffIndexOf([][]byte{build(before, before+len(charset))})
	out := append([]byte{}, header...)
	for _, part := range [][]byte{name, top, strs, gsubrs, charset, charStrings} {
		out = append(out, part...)
	}
	return out
}

func cffFont(w *reader.Writer, res reader.Dict) {
	file := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("CIDFontType0C")}, Raw: squareCFF(7)})
	desc := w.Add(reader.Dict{"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Test"),
		"Flags": reader.Integer(4), "FontFile3": file})
	kid := w.Add(reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("CIDFontType0"),
		"BaseFont": reader.Name("Test"), "FontDescriptor": desc, "DW": reader.Integer(600),
		"CIDSystemInfo": reader.Dict{"Registry": reader.String("Adobe"),
			"Ordering": reader.String("Identity"), "Supplement": reader.Integer(0)}})
	res["Font"] = reader.Dict{"F1": w.Add(reader.Dict{"Type": reader.Name("Font"),
		"Subtype": reader.Name("Type0"), "BaseFont": reader.Name("Test"),
		"Encoding": reader.Name("Identity-H"), "DescendantFonts": reader.Array{kid}})}
}

// A Type 1 program, written the way render's tests write one: an eexec section
// holding one glyph, a square of 400 units.
func eexec(plain []byte, key uint16, lead int) []byte {
	r := key
	buf := append(bytes.Repeat([]byte{'X'}, lead), plain...)
	out := make([]byte, 0, len(buf))
	for _, p := range buf {
		c := p ^ byte(r>>8)
		r = (uint16(c)+r)*52845 + 22719
		out = append(out, c)
	}
	return out
}

func t1num(vs ...int) []byte {
	var out []byte
	for _, v := range vs {
		switch {
		case v >= -107 && v <= 107:
			out = append(out, byte(v+139))
		case v >= 108 && v <= 1131:
			v -= 108
			out = append(out, byte(v/256+247), byte(v%256))
		default:
			out = append(out, 255, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		}
	}
	return out
}

func type1Program() []byte {
	var clear bytes.Buffer
	clear.WriteString("%!PS-AdobeFont-1.0: Synthetic 001.001\n/FontName /Synthetic def\n")
	clear.WriteString("/FontMatrix [0.001 0 0 0.001 0 0] readonly def\n/Encoding StandardEncoding def\n")
	clear.WriteString("currentdict end\ncurrentfile eexec\n")
	cs := append(t1num(0, 600), 13)
	cs = append(cs, t1num(50, 50)...)
	cs = append(cs, 21)
	cs = append(cs, t1num(400)...)
	cs = append(cs, 6)
	cs = append(cs, t1num(400)...)
	cs = append(cs, 7)
	cs = append(cs, t1num(-400)...)
	cs = append(cs, 6, 9, 14)
	enc, notdef := eexec(cs, 4330, 4), eexec([]byte{14}, 4330, 4)
	var priv bytes.Buffer
	priv.WriteString("XXXX dup /Private 8 dict dup begin\n/lenIV 4 def\n/CharStrings 2 dict dup begin\n")
	fmt.Fprintf(&priv, "/.notdef %d RD ", len(notdef))
	priv.Write(notdef)
	priv.WriteString(" ND\n")
	fmt.Fprintf(&priv, "/A %d RD ", len(enc))
	priv.Write(enc)
	priv.WriteString(" ND\nend\nend\n")
	return append(clear.Bytes(), eexec(priv.Bytes(), 55665, 4)...)
}

func type1Font(w *reader.Writer, res reader.Dict) {
	prog := type1Program()
	file := w.Add(&reader.Stream{Dict: reader.Dict{"Length1": reader.Integer(len(prog))}, Raw: prog})
	desc := w.Add(reader.Dict{"Type": reader.Name("FontDescriptor"), "FontName": reader.Name("Synthetic"),
		"Flags": reader.Integer(32), "FontFile": file})
	res["Font"] = reader.Dict{"F1": w.Add(reader.Dict{"Type": reader.Name("Font"),
		"Subtype": reader.Name("Type1"), "BaseFont": reader.Name("Synthetic"),
		"FirstChar": reader.Integer(32), "LastChar": reader.Integer(255), "Widths": widths(32, 255, 600),
		"FontDescriptor": desc, "Encoding": reader.Dict{"Type": reader.Name("Encoding"),
			"Differences": reader.Array{reader.Integer(65), reader.Name("A")}}})}
}

// type3Font has one glyph, A, whose procedure is the one given.
func type3Font(glyph string) func(w *reader.Writer, res reader.Dict) {
	return func(w *reader.Writer, res reader.Dict) {
		proc := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(glyph)})
		res["Font"] = reader.Dict{"F1": w.Add(reader.Dict{"Type": reader.Name("Font"),
			"Subtype":  reader.Name("Type3"),
			"FontBBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
			"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0),
				reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
			"CharProcs": reader.Dict{"tri": proc},
			"Encoding": reader.Dict{"Type": reader.Name("Encoding"),
				"Differences": reader.Array{reader.Integer(65), reader.Name("tri")}},
			"FirstChar": reader.Integer(65), "LastChar": reader.Integer(65),
			"Widths": reader.Array{reader.Integer(1000)}})}
	}
}

func shot(t *testing.T, b []byte, page int) *raster.Image {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	img, err := render.Page(d, page, render.Options{Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// compared says how many pixels hold ink in a, and what fraction of all the
// pixels differ between a and b by more than a quarter of the range.
func compared(t *testing.T, a, b *raster.Image) (ink int, differing float64) {
	t.Helper()
	if a.W != b.W || a.H != b.H {
		t.Fatalf("the pictures are %dx%d and %dx%d", a.W, a.H, b.W, b.H)
	}
	diff := 0
	for i := 0; i < len(a.Pix); i += 4 {
		if a.Pix[i] < 200 || a.Pix[i+1] < 200 || a.Pix[i+2] < 200 {
			ink++
		}
		for c := 0; c < 3; c++ {
			d := int(a.Pix[i+c]) - int(b.Pix[i+c])
			if d > 64 || d < -64 {
				diff++
				break
			}
		}
	}
	return ink, float64(diff) / float64(a.W*a.H)
}

func outlinedBytes(t *testing.T, src []byte, spec string) []byte {
	t.Helper()
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.OutlineText(spec); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// holdsAFont says whether anything a page draws with, a form or a pattern
// among it, still has a /Font resource.
func holdsAFont(t *testing.T, b []byte, page int) bool {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := d.Page(page)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := d.GetDict(pg, "Resources")
	return resourcesHoldAFont(d, res, map[int]bool{}, 0)
}

func resourcesHoldAFont(d *reader.Document, res reader.Dict, seen map[int]bool, depth int) bool {
	if depth > 20 {
		return false
	}
	if _, ok := res["Font"]; ok {
		return true
	}
	for _, group := range []reader.Name{"XObject", "Pattern"} {
		members, _ := d.GetDict(res, group)
		for _, m := range members {
			if ref, ok := m.(reader.Ref); ok {
				if seen[ref.Num] {
					continue
				}
				seen[ref.Num] = true
			}
			s, ok := reader.ToStream(resolveObj(d, m))
			if !ok {
				continue
			}
			if sub, ok := d.GetDict(s.Dict, "Resources"); ok && resourcesHoldAFont(d, sub, seen, depth+1) {
				return true
			}
		}
	}
	states, _ := d.GetDict(res, "ExtGState")
	for _, st := range states {
		if sd, ok := reader.ToDict(resolveObj(d, st)); ok {
			if _, has := sd["Font"]; has {
				return true
			}
		}
	}
	return false
}

func TestOutlinedPagesLookAsTheyDid(t *testing.T) {
	const prose = "BT /F1 18 Tf 10 76 Td (Hello World) Tj 0 -22 Td 1 Tr 0.4 w (Stroke) Tj " +
		"0 -22 Td 2 Tr 1 0 0 rg 0 0 1 RG (Both) Tj ET"
	const spacing = "BT /F1 14 Tf 10 80 Td 2 Tc 5 Tw 110 Tz 5 Ts [(A B) -300 (C D) 120 (EF)] TJ " +
		"14 TL T* (quote) ' 3 1 (dq) \" 0 Tc 0 Tw 100 Tz 0 Ts 0 -15 TD (Last) Tj ET"
	const graphics = "q 0.5 g 0 0 200 100 re f Q 0 0 0.7 rg BT /F1 20 Tf 0.9 0.2 -0.2 0.9 20 20 Tm (Rotated) Tj ET " +
		"q 2 0 0 1 0 0 cm BT /F1 12 Tf 5 50 Td (Wide) Tj ET Q"
	cases := []struct {
		name    string
		build   func(w *reader.Writer, res reader.Dict)
		content string
	}{
		{"Helvetica, modes 0 1 2", stdFont, prose},
		{"spacing, TJ, quotes, rise", stdFont, spacing},
		{"graphics state and matrices", stdFont, graphics},
		{"embedded TrueType", trueTypeFont, "BT /F1 24 Tf 10 40 Td (Quick fox) Tj ET"},
		{"CID TrueType", cidTrueTypeFont, "BT /F1 24 Tf 10 40 Td " + glyphIDs(t, "Hag") + " Tj ET"},
		{"CFF", cffFont, "BT /F1 40 Tf 10 20 Td <0007> Tj 0 Tc <0007> Tj ET"},
		{"embedded Type 1", type1Font, "BT /F1 40 Tf 10 20 Td (AA) Tj ET"},
		{"Type 3, shape only (d1)", type3Font("1000 0 0 0 1000 1000 d1 0 0 m 1000 0 l 500 1000 l f"),
			"0 0.5 0 rg BT /F1 40 Tf 10 20 Td (AA) Tj ET"},
		{"Type 3, coloured (d0)", type3Font("1000 0 d0 1 0 0 rg 0 0 1000 1000 re f"),
			"0 0 1 rg BT /F1 40 Tf 10 20 Td (A) Tj ET"},
		{"text rendering mode 3 (invisible)", stdFont, "BT /F1 30 Tf 3 Tr 10 40 Td (hidden) Tj ET 0 0 50 50 re f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := buildDoc(t, 1, tc.content, tc.build, nil)
			if !holdsAFont(t, src, 1) {
				t.Fatal("the fixture has no font to remove")
			}
			out := outlinedBytes(t, src, "all")
			if holdsAFont(t, out, 1) {
				t.Error("a /Font resource remains")
			}
			if bytes.Contains(out, []byte("/Font")) {
				t.Error("the bytes of the file still say /Font")
			}
			ink, frac := compared(t, shot(t, src, 1), shot(t, out, 1))
			if ink == 0 {
				t.Fatal("the page the text was on is blank: nothing was judged")
			}
			t.Logf("%d inked pixels, %.4f%% differ", ink, frac*100)
			if frac > 0.0002 {
				t.Errorf("%.4f%% of the pixels differ by more than a quarter of the range (limit 0.02%%)", frac*100)
			}
		})
	}
}

func TestTheTextOfAnOutlinedPageIsGone(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 18 Tf 10 40 Td (Findable) Tj ET", stdFont, nil)
	out := outlinedBytes(t, src, "1")
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	content, err := d.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"Tj", "TJ", "BT", "Tf"} {
		if bytes.Contains(content, []byte(" "+op)) {
			t.Errorf("the content still has %s", op)
		}
	}
	if !bytes.Contains(content, []byte(" m ")) {
		t.Error("no path in the content")
	}
}

func TestOnlyTheNamedPagesAreOutlined(t *testing.T) {
	// Two pages share one font and one stream: outlining the first must not take
	// the font from the second.
	src := buildDoc(t, 2, "BT /F1 18 Tf 10 40 Td (Shared) Tj ET", stdFont, nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("page 1 still has its font")
	}
	if !holdsAFont(t, out, 2) {
		t.Error("page 2 lost its font")
	}
	_, frac := compared(t, shot(t, src, 2), shot(t, out, 2))
	if frac != 0 {
		t.Errorf("page 2 changed: %.3f%%", frac*100)
	}
}

func TestClipModesAddTheGlyphsToTheClip(t *testing.T) {
	// The renderer draws nothing for modes 4 to 7, so there is no picture to
	// match: what is asserted is that a rectangle painted after the text is cut
	// to the letters, and covers less than the rectangle.
	src := buildDoc(t, 1, "BT /F1 60 Tf 7 Tr 10 20 Td (HI) Tj ET 1 0 0 rg 0 0 200 100 re f", stdFont, nil)
	out := outlinedBytes(t, src, "1")
	img := shot(t, out, 1)
	red := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 200 && img.Pix[i+1] < 80 && img.Pix[i+2] < 80 {
			red++
		}
	}
	if red == 0 {
		t.Fatal("nothing was left of the rectangle: the clip is empty")
	}
	if red > img.W*img.H/4 {
		t.Errorf("%d of %d pixels are red: the rectangle was not clipped to the text", red, img.W*img.H)
	}
	// And mode 5 both strokes the letters and clips.
	src = buildDoc(t, 1, "BT /F1 60 Tf 5 Tr 10 20 Td (HI) Tj ET 0 1 0 rg 0 0 200 100 re f", stdFont, nil)
	img = shot(t, outlinedBytes(t, src, "1"), 1)
	green := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+1] > 200 && img.Pix[i] < 80 {
			green++
		}
	}
	if green == 0 || green > img.W*img.H/4 {
		t.Errorf("mode 5: %d green pixels", green)
	}
}

func TestTextInAFormAndAPatternIsOutlinedInACopy(t *testing.T) {
	var formRef reader.Ref
	build := func(w *reader.Writer, res reader.Dict) {
		stdFont(w, res)
		fres := reader.Dict{"Font": res["Font"]}
		formRef = w.Add(&reader.Stream{Dict: reader.Dict{"Type": reader.Name("XObject"),
			"Subtype": reader.Name("Form"), "Resources": fres,
			"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(200), reader.Integer(100)}},
			Raw: []byte("BT /F1 20 Tf 10 60 Td (In a form) Tj ET")})
		pat := w.Add(&reader.Stream{Dict: reader.Dict{"Type": reader.Name("Pattern"),
			"PatternType": reader.Integer(1), "PaintType": reader.Integer(1), "TilingType": reader.Integer(1),
			"BBox":  reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(40), reader.Integer(20)},
			"XStep": reader.Integer(40), "YStep": reader.Integer(20), "Resources": fres},
			Raw: []byte("BT /F1 12 Tf 2 4 Td (ab) Tj ET")})
		res["XObject"] = reader.Dict{"Fm": formRef}
		res["Pattern"] = reader.Dict{"P1": pat}
		res["ColorSpace"] = reader.Dict{"Pat": reader.Array{reader.Name("Pattern")}}
	}
	src := buildDoc(t, 2, "/Fm Do /Pat cs /P1 scn 0 0 200 40 re f", build, nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("a font remains under page 1")
	}
	// The form is shared with page 2, which was not named: it keeps its font.
	if !holdsAFont(t, out, 2) {
		t.Error("the form page 2 draws lost its font")
	}
	ink, frac := compared(t, shot(t, src, 1), shot(t, out, 1))
	if ink == 0 || frac > 0.0002 {
		t.Errorf("ink %d, %.4f%% differ", ink, frac*100)
	}
}

func TestATypeThreeGlyphMayUseTextOfItsOwn(t *testing.T) {
	// The glyph draws a Helvetica letter: the form made for it has to be
	// outlined too, and the result still has no font.
	build := func(w *reader.Writer, res reader.Dict) {
		face := w.Add(reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("Type1"),
			"BaseFont": reader.Name("Helvetica")})
		res["Font"] = reader.Dict{"F1": w.Add(reader.Dict{"Type": reader.Name("Font"),
			"Subtype":  reader.Name("Type3"),
			"FontBBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(1000), reader.Integer(1000)},
			"FontMatrix": reader.Array{reader.Real(0.001), reader.Integer(0), reader.Integer(0),
				reader.Real(0.001), reader.Integer(0), reader.Integer(0)},
			"CharProcs": reader.Dict{"tri": w.Add(&reader.Stream{Dict: reader.Dict{},
				Raw: []byte("1000 0 d0 BT /G 800 Tf 100 100 Td (x) Tj ET")})},
			"Encoding": reader.Dict{"Type": reader.Name("Encoding"),
				"Differences": reader.Array{reader.Integer(65), reader.Name("tri")}},
			"FirstChar": reader.Integer(65), "LastChar": reader.Integer(65),
			"Widths":    reader.Array{reader.Integer(1000)},
			"Resources": reader.Dict{"Font": reader.Dict{"G": face}}})}
	}
	src := buildDoc(t, 1, "BT /F1 60 Tf 10 20 Td (A) Tj ET", build, nil)
	out := outlinedBytes(t, src, "1")
	if holdsAFont(t, out, 1) {
		t.Error("a font remains")
	}
	ink, frac := compared(t, shot(t, src, 1), shot(t, out, 1))
	if ink == 0 || frac > 0.0002 {
		t.Errorf("ink %d, %.4f%% differ", ink, frac*100)
	}
}

func TestVerticalWritingIsRefused(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 18 Tf 10 40 Td <0001> Tj ET", func(w *reader.Writer, res reader.Dict) {
		cidTrueTypeFont(w, res)
		fonts := res["Font"].(reader.Dict)
		f, _ := fonts["F1"].(reader.Ref)
		// Rewrite the font with a vertical encoding: a second font object.
		kid := w.Add(reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("CIDFontType2"),
			"BaseFont": reader.Name("Test"), "DW": reader.Integer(1000)})
		_ = f
		fonts["F1"] = w.Add(reader.Dict{"Type": reader.Name("Font"), "Subtype": reader.Name("Type0"),
			"BaseFont": reader.Name("Test"), "Encoding": reader.Name("Identity-V"),
			"DescendantFonts": reader.Array{kid}})
	}, nil)
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.OutlineText("1"); err != nil {
		t.Fatal(err)
	}
	if out, err := d.Bytes(); !errors.Is(err, ErrOutlineVertical) || out != nil {
		t.Fatalf("Bytes() = %d bytes, %v; want ErrOutlineVertical", len(out), err)
	}
}

func TestOutliningIsBounded(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 10 Tf 10 40 Td (abcdef) Tj ET", stdFont, nil)
	t.Run("glyphs", func(t *testing.T) {
		defer func(v int) { maxOutlineGlyphs = v }(maxOutlineGlyphs)
		maxOutlineGlyphs = 5
		d, _ := Open(src)
		_ = d.OutlineText("1")
		if _, err := d.Bytes(); !errors.Is(err, ErrOutlineTooLarge) {
			t.Errorf("6 glyphs against a limit of 5: %v", err)
		}
	})
	t.Run("glyphs at the limit are fine", func(t *testing.T) {
		defer func(v int) { maxOutlineGlyphs = v }(maxOutlineGlyphs)
		maxOutlineGlyphs = 6
		d, _ := Open(src)
		_ = d.OutlineText("1")
		if _, err := d.Bytes(); err != nil {
			t.Errorf("6 glyphs against a limit of 6: %v", err)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		defer func(v int) { maxOutlineBytes = v }(maxOutlineBytes)
		maxOutlineBytes = 40
		d, _ := Open(src)
		_ = d.OutlineText("1")
		if _, err := d.Bytes(); !errors.Is(err, ErrOutlineTooLarge) {
			t.Errorf("paths past 40 bytes: %v", err)
		}
	})
	t.Run("nesting", func(t *testing.T) {
		defer func(v int) { maxOutlineNesting = v }(maxOutlineNesting)
		maxOutlineNesting = 0
		withForm := buildDoc(t, 1, "/Fm Do", func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			res["XObject"] = reader.Dict{"Fm": w.Add(&reader.Stream{Dict: reader.Dict{
				"Subtype": reader.Name("Form"), "Resources": reader.Dict{"Font": res["Font"]},
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(9), reader.Integer(9)}},
				Raw: []byte("BT /F1 9 Tf (a) Tj ET")})}
		}, nil)
		d, _ := Open(withForm)
		_ = d.OutlineText("1")
		if _, err := d.Bytes(); !errors.Is(err, ErrOutlineTooLarge) {
			t.Errorf("a form nested past the limit: %v", err)
		}
	})
	t.Run("a form that draws itself ends", func(t *testing.T) {
		loop := buildDoc(t, 1, "/Fm Do", func(w *reader.Writer, res reader.Dict) {
			stdFont(w, res)
			self := w.Reserve()
			fres := reader.Dict{"Font": res["Font"], "XObject": reader.Dict{"Fm": self}}
			w.Put(self, &reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"), "Resources": fres,
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(9), reader.Integer(9)}},
				Raw: []byte("BT /F1 9 Tf (a) Tj ET /Fm Do")})
			res["XObject"] = reader.Dict{"Fm": self}
		}, nil)
		if out := outlinedBytes(t, loop, "1"); holdsAFont(t, out, 1) {
			t.Error("a font remains")
		}
	})
}

func TestOutliningCannotBeCombinedWithAnEditOfTheText(t *testing.T) {
	src := buildDoc(t, 1, "BT /F1 18 Tf 10 40 Td (Words) Tj ET", stdFont, nil)
	d, _ := Open(src)
	if err := d.RecolourText("1", 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.OutlineText("1"); err == nil {
		t.Error("outlining a page already to be recoloured was accepted")
	}
	// The other order is only seen when the document is written.
	d, _ = Open(src)
	if err := d.OutlineText("1"); err != nil {
		t.Fatal(err)
	}
	if err := d.RecolourText("1", 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Bytes(); err == nil {
		t.Error("a page both outlined and recoloured was written")
	}
}

func TestOutlineTextRefusesWhatItCannotName(t *testing.T) {
	d, _ := Open(buildDoc(t, 1, "", stdFont, nil))
	if err := d.OutlineText("nonsense"); err == nil {
		t.Error("a range that is not one")
	}
	if err := New().OutlineText("all"); err == nil {
		t.Error("an empty document has no pages to outline")
	}
}

func TestAnnotationsWithFontsAreCounted(t *testing.T) {
	extra := func(w *reader.Writer, page reader.Dict) {
		ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
			"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(9), reader.Integer(9)},
			"Resources": reader.Dict{"Font": reader.Dict{"F": w.Add(reader.Dict{"Type": reader.Name("Font"),
				"Subtype": reader.Name("Type1"), "BaseFont": reader.Name("Helvetica")})}}},
			Raw: []byte("BT /F 9 Tf (a) Tj ET")})
		bare := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
			"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(9), reader.Integer(9)}}})
		rect := reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(9), reader.Integer(9)}
		page["Annots"] = reader.Array{
			w.Add(reader.Dict{"Type": reader.Name("Annot"), "Subtype": reader.Name("Stamp"), "Rect": rect,
				"AP": reader.Dict{"N": ap}}),
			w.Add(reader.Dict{"Type": reader.Name("Annot"), "Subtype": reader.Name("Stamp"), "Rect": rect,
				"AP": reader.Dict{"N": bare}}),
			reader.Integer(3),
			w.Add(reader.Dict{"Type": reader.Name("Annot"), "Subtype": reader.Name("Text"), "Rect": rect}),
		}
	}
	d, _ := Open(buildDoc(t, 1, "BT /F1 18 Tf 10 40 Td (x) Tj ET", stdFont, extra))
	if err := d.OutlineText("1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Bytes(); err != nil {
		t.Fatal(err)
	}
	if got := d.OutlineSkipped(); got != 1 {
		t.Errorf("OutlineSkipped() = %d, want 1", got)
	}
}

func TestOutlineNumbersHaveNoExponent(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 1: "1", -0.00001: "0", 1e-9: "0", 12.34567: "12.3457",
		1e8: "100000000", -2.5: "-2.5",
		// Past the bound a number is not written: 1e300 was 300 digits.
		1e15: "0", 1e300: "0"} {
		if got := olNum(v); got != want {
			t.Errorf("olNum(%v) = %q, want %q", v, got, want)
		}
	}
	if got := olNum(math.NaN()); got != "0" {
		t.Errorf("olNum(NaN) = %q", got)
	}
	if got := olNum(math.Inf(1)); got != "0" {
		t.Errorf("olNum(+Inf) = %q", got)
	}
}

// FuzzOutlineContent runs arbitrary content streams over a page with one font
// and requires the outlining to finish, and what it writes to be a document
// that reads.
func FuzzOutlineContent(f *testing.F) {
	for _, seed := range []string{
		"BT /F1 12 Tf (a) Tj ET",
		"BT /F1 12 Tf 1 Tr [(a) -50 (b)] TJ ET q Q Q",
		"BT /F1 1e9 Tf 100000 Tz (aaaa) Tj ' \" ET",
		"BT 7 Tr /F1 12 Tf 0 0 0 0 0 0 Tm (a) Tj",
		"/Fm Do BT /Nope 5 Tf (x) Tj ET",
		"BT /T3 12 Tf (AAAA) Tj ET",
		"/Fm Do /Fm Do q /Fm Do Q BT /F1 9 Tf /T3 9 Tf (AA) Tj ET",
		"/Span <</ActualText (s)>> BDC BT /F1 12 Tf 7 Tr (a) Tj ET EMC",
		"/Span /P1 BDC BT /F1 12 Tf (a) Tj ET /Fm Do EMC /G1 gs",
		"BT /F1 12 Tf 7 Tr (a) Tj 2 0 0 2 5 5 cm 0 0 0 0 0 0 cm ET",
	} {
		f.Add([]byte(seed))
	}
	// The page the content is run over: a standard font, a Type 3 font whose
	// glyph draws the next one, a form holding text, a property list and a
	// soft mask.
	build := func(w *reader.Writer, res reader.Dict) {
		chainedType3(3)(w, res)
		t3 := res["Font"].(reader.Dict)["F1"]
		stdFont(w, res)
		res["Font"].(reader.Dict)["T3"] = t3
		fm := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
			"BBox":      reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(100)},
			"Resources": reader.Dict{"Font": res["Font"]}}, Raw: []byte("BT /F1 12 Tf (form) Tj /T3 9 Tf (A) Tj ET")})
		res["XObject"] = reader.Dict{"Fm": fm}
		res["Properties"] = reader.Dict{"P1": reader.Dict{"ActualText": reader.String("p")}}
		res["ExtGState"] = reader.Dict{"G1": reader.Dict{"SMask": reader.Dict{"Type": reader.Name("Mask"),
			"S": reader.Name("Alpha"), "G": fm}}}
	}
	f.Fuzz(func(t *testing.T, content []byte) {
		if len(content) > 4096 {
			return
		}
		start := time.Now()
		var m0 runtime.MemStats
		runtime.ReadMemStats(&m0)
		defer func() {
			var m1 runtime.MemStats
			runtime.ReadMemStats(&m1)
			if d := time.Since(start); d > time.Second || m1.TotalAlloc-m0.TotalAlloc > 256<<20 {
				t.Errorf("%d bytes of content took %v and %d MiB", len(content), d, (m1.TotalAlloc-m0.TotalAlloc)>>20)
			}
		}()
		src := buildDoc(t, 1, string(content), build, nil)
		d, err := Open(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.OutlineText("1"); err != nil {
			t.Fatal(err)
		}
		out, err := d.Bytes()
		if err != nil {
			return // refused: a bound, or a page that cannot be read
		}
		if _, err := reader.Open(out); err != nil {
			t.Fatalf("the output does not open: %v", err)
		}
	})
}
