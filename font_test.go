package ops

import (
	"math"
	"testing"
)

func TestFontWidths(t *testing.T) {
	// Adobe's own numbers, in thousandths of the point size.
	cases := []struct {
		f    Font
		s    string
		size float64
		want float64
	}{
		{Helvetica, "A", 1000, 667},
		{Helvetica, " ", 1000, 278},
		{Helvetica, "iiii", 1000, 888},
		{HelveticaBold, "A", 1000, 722},
		{HelveticaBold, "i", 1000, 278},
		{Courier, "AiW", 1000, 1800},
		{CourierBold, "x", 1000, 600},
		// An unknown face reads as Helvetica.
		{Font("Nonesuch"), "A", 1000, 667},
		// An accented letter takes the width of the letter it is built on.
		{Helvetica, "é", 1000, 556},
		{Helvetica, "É", 1000, 667},
		// Anything else falls back on a lower-case n.
		{Helvetica, "•", 1000, 556},
		{Helvetica, "", 1000, 0},
	}
	for _, c := range cases {
		if got := c.f.Width(c.s, c.size); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s.Width(%q) = %g, want %g", c.f, c.s, got, c.want)
		}
	}
}

func TestFontNames(t *testing.T) {
	for f, want := range map[Font]string{
		Helvetica:       "Helvetica",
		HelveticaBold:   "Helvetica-Bold",
		Courier:         "Courier",
		CourierBold:     "Courier-Bold",
		Font("Unknown"): "Helvetica",
	} {
		if got := f.name(); got != want {
			t.Errorf("%s.name() = %q", f, got)
		}
	}
}

func TestEncodeWinAnsi(t *testing.T) {
	cases := []struct {
		in   string
		want []byte
	}{
		{"abc", []byte("abc")},
		{"é", []byte{0xE9}},
		{"€", []byte{0x80}},
		{"’", []byte{0x92}},
		// Outside the encoding altogether: dropped rather than drawn wrong.
		{"a中b", []byte("ab")},
		{"\x01", nil},
	}
	for _, c := range cases {
		got := encodeWinAnsi(c.in)
		if string(got) != string(c.want) {
			t.Errorf("encodeWinAnsi(%q) = % x, want % x", c.in, got, c.want)
		}
	}
}

func TestPDFString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "(plain)"},
		{"a(b)c", `(a\(b\)c)`},
		{`back\slash`, `(back\\slash)`},
		{"\xe9", `(\351)`},
		{"", "()"},
	}
	for _, c := range cases {
		if got := string(pdfString([]byte(c.in))); got != c.want {
			t.Errorf("pdfString(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestCharWidthOutOfRange(t *testing.T) {
	if got := Helvetica.charWidth(0x01); got != 556 {
		t.Errorf("a control character = %d", got)
	}
	if got := Helvetica.charWidth(0xFF); got != 500 {
		t.Errorf("y with a diaeresis = %d", got)
	}
	if got := Helvetica.charWidth(0xA9); got != 556 {
		t.Errorf("an unmapped Latin-1 character = %d", got)
	}
}
