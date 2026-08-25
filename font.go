package ops

// The standard fonts a PDF viewer is required to have, and the character
// widths they are defined with. Nothing is embedded: these four faces are the
// ones every viewer already knows, which is what makes a watermark or a page
// number cost nothing in file size and work everywhere.
//
// The widths are Adobe's own, in thousandths of the point size. They are exact
// for the printable ASCII range; a Latin-1 letter takes the width of the
// letter it is built on, which is true of these faces, and anything else falls
// back on the width of a lower-case n.

// A Font names one of the four faces this package can draw with.
type Font string

// The faces available. Any other name reads as Helvetica.
const (
	Helvetica     Font = "Helvetica"
	HelveticaBold Font = "Helvetica-Bold"
	Courier       Font = "Courier"
	CourierBold   Font = "Courier-Bold"
)

// helveticaWidths holds the width of every printable ASCII character, indexed
// from the space at 32.
var helveticaWidths = [95]int{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
}

// helveticaBoldWidths is the same for the bold face.
var helveticaBoldWidths = [95]int{
	278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611,
	975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556,
	333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611,
	611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584,
}

// latinBase maps a Latin-1 code to the ASCII letter it is built on, so an
// accented letter takes that letter's width — which is how these faces are
// drawn.
var latinBase = map[byte]byte{
	0xC0: 'A', 0xC1: 'A', 0xC2: 'A', 0xC3: 'A', 0xC4: 'A', 0xC5: 'A',
	0xC7: 'C', 0xC8: 'E', 0xC9: 'E', 0xCA: 'E', 0xCB: 'E',
	0xCC: 'I', 0xCD: 'I', 0xCE: 'I', 0xCF: 'I', 0xD1: 'N',
	0xD2: 'O', 0xD3: 'O', 0xD4: 'O', 0xD5: 'O', 0xD6: 'O', 0xD8: 'O',
	0xD9: 'U', 0xDA: 'U', 0xDB: 'U', 0xDC: 'U', 0xDD: 'Y',
	0xE0: 'a', 0xE1: 'a', 0xE2: 'a', 0xE3: 'a', 0xE4: 'a', 0xE5: 'a',
	0xE7: 'c', 0xE8: 'e', 0xE9: 'e', 0xEA: 'e', 0xEB: 'e',
	0xEC: 'i', 0xED: 'i', 0xEE: 'i', 0xEF: 'i', 0xF1: 'n',
	0xF2: 'o', 0xF3: 'o', 0xF4: 'o', 0xF5: 'o', 0xF6: 'o', 0xF8: 'o',
	0xF9: 'u', 0xFA: 'u', 0xFB: 'u', 0xFC: 'u', 0xFD: 'y', 0xFF: 'y',
}

// charWidth reports the width of one WinAnsi character, in thousandths of the
// point size.
func (f Font) charWidth(c byte) int {
	if f == Courier || f == CourierBold {
		return 600
	}
	table := &helveticaWidths
	if f == HelveticaBold {
		table = &helveticaBoldWidths
	}
	if base, ok := latinBase[c]; ok {
		c = base
	}
	if c < 32 || c > 126 {
		return table['n'-32]
	}
	return table[c-32]
}

// Width reports how wide a string is at the given point size, once encoded.
func (f Font) Width(s string, size float64) float64 {
	total := 0
	for _, c := range encodeWinAnsi(s) {
		total += f.charWidth(c)
	}
	return float64(total) * size / 1000
}

// name returns the base font name a PDF font dictionary needs.
func (f Font) name() string {
	switch f {
	case HelveticaBold, Courier, CourierBold:
		return string(f)
	}
	return string(Helvetica)
}

// winAnsiSpecials maps the few characters WinAnsiEncoding puts in the range
// Latin-1 leaves for control codes.
var winAnsiSpecials = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87,
	'ˆ': 0x88, '‰': 0x89, 'Š': 0x8A, '‹': 0x8B, 'Œ': 0x8C, 'Ž': 0x8E,
	'‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97,
	'˜': 0x98, '™': 0x99, 'š': 0x9A, '›': 0x9B, 'œ': 0x9C, 'ž': 0x9E, 'Ÿ': 0x9F,
}

// encodeWinAnsi turns Go text into the single-byte encoding the standard faces
// are addressed with. A character the encoding has no room for is dropped
// rather than drawn as something else.
func encodeWinAnsi(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 32 && r < 127:
			out = append(out, byte(r))
		case r >= 0xA0 && r <= 0xFF:
			out = append(out, byte(r))
		default:
			if b, ok := winAnsiSpecials[r]; ok {
				out = append(out, b)
			}
		}
	}
	return out
}

// pdfString renders bytes as a PDF literal string, escaping what has to be.
func pdfString(b []byte) []byte {
	out := make([]byte, 0, len(b)+2)
	out = append(out, '(')
	for _, c := range b {
		switch {
		case c == '(' || c == ')' || c == '\\':
			out = append(out, '\\', c)
		case c < 32 || c > 126:
			out = append(out, '\\')
			out = append(out, '0'+c>>6&7, '0'+c>>3&7, '0'+c&7)
		default:
			out = append(out, c)
		}
	}
	return append(out, ')')
}
