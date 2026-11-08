package ops

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-pdfkit/reader"
)

// Notes and free text: the two annotations that say something of their OWN
// rather than marking what the page already says.
//
// A note is the sticky note everybody knows — "a 'sticky note' attached to a
// point in the PDF document. When closed, the annotation shall appear as an
// icon; when open, it shall display a pop-up window containing the text of the
// note" (ISO 32000-1, 12.5.6.4). A free text annotation "displays text directly
// on the page … instead of being displayed in a pop-up window, the text shall
// be always visible" (12.5.6.6).
//
// ⛔ NEITHER TOUCHES THE PAGE'S CONTENT, which is the whole reason to write one:
// the document underneath is the author's, the comment can be dropped again, and
// a reader who wants the paper as it was published gets it.
//
// ⛔⛔ AND BOTH CARRY AN APPEARANCE STREAM, for the reason the markup
// annotations do: go-pdfkit/render draws an annotation ONLY from its /AP and
// skips one that has none. A /Text with a /Name and a /FreeText with a /DA are
// both perfectly correct annotations that this fleet's own renderer shows as
// NOTHING AT ALL — so the test for either of these renders the page and looks at
// the pixels rather than at the dictionary.
//
// ISO 32000-1:2008, 12.5.6.4 (Table 172) and 12.5.6.6 (Table 174).

// A NoteIcon is the icon a closed note shows.
//
// ⛔ The seven Table 172 requires a reader to provide, and no others. The table
// says "additional names may be supported as well", which means a name outside
// this list is a note that may show up as nothing in a reader that does not know
// it. Since the appearance we write decides what it looks like anyway, the value
// of the /Name entry is fidelity to the format, and an invented one buys nothing
// and risks a blank.
type NoteIcon string

const (
	IconNote         NoteIcon = "Note"
	IconComment      NoteIcon = "Comment"
	IconKey          NoteIcon = "Key"
	IconHelp         NoteIcon = "Help"
	IconParagraph    NoteIcon = "Paragraph"
	IconNewParagraph NoteIcon = "NewParagraph"
	IconInsert       NoteIcon = "Insert"
)

// noteIcons is the set Table 172 names, which is what AddNotes accepts.
var noteIcons = map[NoteIcon]bool{
	IconNote: true, IconComment: true, IconKey: true, IconHelp: true,
	IconParagraph: true, IconNewParagraph: true, IconInsert: true,
}

// noteSide is how large a closed note's icon is, in points.
//
// ⛔ A SQUARE OF A FIXED SIZE, because a note does not scale: Table 172 says
// text annotations "shall not scale and rotate with the page; they shall behave
// as if the NoZoom and NoRotate annotation flags were always set". 20 points is
// what readers draw one at, and a caller asking for a note does not want to
// choose it.
const noteSide = 20.0

// A Note is a sticky note attached to a point on a page.
type Note struct {
	// Page is 1-based.
	Page int
	// X and Y are where the icon's bottom left corner goes, in the page's own
	// points. The icon is noteSide points square.
	X, Y float64
	// Author and Contents are /T and what the note says. Contents is required:
	// a note that says nothing is an icon in the margin with nothing behind it.
	Author, Contents string
	// Open asks for the pop-up to be showing when the document is opened.
	Open bool
	// Icon is which of Table 172's seven to name; empty means Note.
	Icon NoteIcon
	// R, G, B are the icon's colour in 0..1. The zero value is the yellow a
	// sticky note is.
	R, G, B float64
}

// AddNotes puts sticky notes into the document.
//
// ⛔ IT REFUSES BEFORE THE DOCUMENT IS WRITTEN, as Mark and AddFields do, and
// for the same reason: a note on a page the document has not is a mistake the
// caller can still do something about, while one discovered at the write is a
// document already half built.
//
// The notes are held until the write, because an annotation names the page
// OBJECT it sits on and the pages have no object numbers until then.
func (d *Doc) AddNotes(notes ...Note) error {
	for i, n := range notes {
		if n.Page < 1 || n.Page > len(d.pages) {
			return fmt.Errorf("note %d is on page %d and the document has %d",
				i+1, n.Page, len(d.pages))
		}
		if n.Contents == "" {
			return fmt.Errorf("note %d on page %d says nothing: a sticky note "+
				"with no text is an icon in the margin with nothing behind it",
				i+1, n.Page)
		}
		if n.Icon != "" && !noteIcons[n.Icon] {
			return fmt.Errorf("note %d on page %d asks for the icon %q, and "+
				"Table 172 names Comment, Key, Note, Help, NewParagraph, "+
				"Paragraph and Insert", i+1, n.Page, n.Icon)
		}
	}
	d.notes = append(d.notes, notes...)
	return nil
}

// icon is the name to write, with the empty value read as Table 172's default.
func (n Note) icon() NoteIcon {
	if n.Icon == "" {
		return IconNote
	}
	return n.Icon
}

// colour is the note's colour, with the zero value read as a sticky note's
// yellow rather than as black — which would be a black square on the page.
func (n Note) colour() (r, g, b float64) {
	if n.R == 0 && n.G == 0 && n.B == 0 {
		return 1, 0.86, 0.3
	}
	return clamp01(n.R), clamp01(n.G), clamp01(n.B)
}

// addedNotes writes every note onto the page it names.
func (d *Doc) addedNotes(w *reader.Writer, dicts []reader.Dict) {
	for _, n := range d.notes {
		attach(dicts[n.Page-1], w.Add(d.noteDict(w, n)))
	}
}

// noteDict is one /Text annotation, with the appearance that makes it visible.
func (d *Doc) noteDict(w *reader.Writer, n Note) reader.Dict {
	x0, y0 := n.X, n.Y
	x1, y1 := x0+noteSide, y0+noteSide
	r, g, b := n.colour()
	dict := reader.Dict{
		"Type":    reader.Name("Annot"),
		"Subtype": reader.Name("Text"),
		"Rect":    rectArray(x0, y0, x1, y1),
		"Name":    reader.Name(n.icon()),
		"C":       reader.Array{reader.Real(r), reader.Real(g), reader.Real(b)},
		// ⛔ Printed, and neither zoomed nor rotated. The print flag is the one
		// the marks carry for the same reason — an annotation nobody can print
		// is a comment that vanishes from the paper — and the other two are what
		// Table 172 says a text annotation behaves as if it had, so writing them
		// says out loud what a reader would have done anyway.
		"F":        reader.Integer(annotPrint | annotNoZoom | annotNoRotate),
		"Contents": reader.String(n.Contents),
		"M":        reader.String(pdfDate(now())),
		"AP":       reader.Dict{"N": w.Add(noteAppearance(x0, y0, r, g, b))},
	}
	if n.Author != "" {
		dict["T"] = reader.String(n.Author)
	}
	if n.Open {
		dict["Open"] = reader.Bool(true)
	}
	return dict
}

// annotNoZoom and annotNoRotate are Table 165's bits 4 and 5.
const (
	annotNoZoom   = 1 << 3
	annotNoRotate = 1 << 4
)

// noteAppearance draws the icon: a filled speech bubble with a tail and three
// lines of writing in it.
//
// ⛔ DRAWN, not named. Table 172's /Name entry asks the reader for one of its
// own icons, and the table itself says the /AP "shall take precedence over the
// Name entry" — which this fleet's renderer turns into a requirement, because it
// draws an annotation only from its appearance. A note with a name and no
// appearance is a correct annotation that renders as nothing.
func noteAppearance(x, y, r, g, b float64) *reader.Stream {
	var body bytes.Buffer
	// The bubble, inset so the tail has room underneath it.
	bx0, by0 := x+1, y+5.5
	bx1, by1 := x+noteSide-1, y+noteSide-1
	fmt.Fprintf(&body, "%.4f %.4f %.4f rg 0.25 0.2 0.1 RG 0.6 w\n", r, g, b)
	fmt.Fprintf(&body, "%.4f %.4f %.4f %.4f re B\n", bx0, by0, bx1-bx0, by1-by0)
	// The tail, a triangle hanging off the bottom left.
	fmt.Fprintf(&body, "%.4f %.4f m %.4f %.4f l %.4f %.4f l f\n",
		bx0+3, by0, bx0+3, y+1, bx0+8, by0)
	// Three lines of writing, dark on the bubble, the last one short.
	fmt.Fprint(&body, "0.25 0.2 0.1 RG 0.7 w\n")
	for i, inset := range []float64{3, 3, 7} {
		ly := by1 - 4 - float64(i)*4
		fmt.Fprintf(&body, "%.4f %.4f m %.4f %.4f l S\n",
			bx0+2.5, ly, bx1-inset+0.5, ly)
	}
	return &reader.Stream{
		Dict: reader.Dict{
			"Type":    reader.Name("XObject"),
			"Subtype": reader.Name("Form"),
			"BBox":    rectArray(x, y, x+noteSide, y+noteSide),
		},
		Raw: body.Bytes(),
	}
}

// A Quadding is how a free text annotation's lines are justified: Table 174's
// /Q, where 0 is left, 1 centred and 2 right.
type Quadding int

const (
	AlignLeft Quadding = iota
	AlignCentre
	AlignRight
)

// A FreeText is text shown directly on the page, in a box.
type FreeText struct {
	// Page is 1-based.
	Page int
	// X0, Y0, X1, Y1 are the box, in the page's own points.
	X0, Y0, X1, Y1 float64
	// Text is what it says. Lines are wrapped to the box's width; a \n starts a
	// new line.
	Text string
	// Font and Size are the face and the point size; the zero values are
	// Helvetica at 12.
	Font Font
	Size float64
	// Align is the justification.
	Align Quadding
	// Author is /T.
	Author string
	// R, G, B are the TEXT's colour in 0..1, black by default — which is what a
	// typewriter comment is, and the one case where the zero value is right.
	R, G, B float64
}

// freeTextSize is the size a caller who did not choose one gets.
const freeTextSize = 12.0

// size and face read the zero values as the defaults.
func (f FreeText) size() float64 {
	if f.Size <= 0 {
		return freeTextSize
	}
	return f.Size
}

func (f FreeText) face() Font {
	if f.Font == "" {
		return Helvetica
	}
	return f.Font
}

// AddFreeText puts text boxes into the document.
//
// ⛔ Refused before the write, like AddNotes, and with one check of its own: a
// box of no width or no height is text nobody will ever see, because the
// appearance is clipped to it.
func (d *Doc) AddFreeText(texts ...FreeText) error {
	for i, f := range texts {
		if f.Page < 1 || f.Page > len(d.pages) {
			return fmt.Errorf("free text %d is on page %d and the document has %d",
				i+1, f.Page, len(d.pages))
		}
		if f.Text == "" {
			return fmt.Errorf("free text %d on page %d says nothing", i+1, f.Page)
		}
		if f.X1-f.X0 <= 0 || f.Y1-f.Y0 <= 0 {
			return fmt.Errorf("free text %d on page %d has a box of %g by %g "+
				"points, and its text is clipped to that box",
				i+1, f.Page, f.X1-f.X0, f.Y1-f.Y0)
		}
		if f.Align < AlignLeft || f.Align > AlignRight {
			return fmt.Errorf("free text %d on page %d asks for justification "+
				"%d, and Table 174 numbers them 0 (left), 1 (centred) and "+
				"2 (right)", i+1, f.Page, f.Align)
		}
	}
	d.freeText = append(d.freeText, texts...)
	return nil
}

// addedFreeText writes every text box onto the page it names.
func (d *Doc) addedFreeText(w *reader.Writer, dicts []reader.Dict) {
	for _, f := range d.freeText {
		attach(dicts[f.Page-1], w.Add(d.freeTextDict(w, f)))
	}
}

// freeTextDict is one /FreeText annotation.
func (d *Doc) freeTextDict(w *reader.Writer, f FreeText) reader.Dict {
	r, g, b := clamp01(f.R), clamp01(f.G), clamp01(f.B)
	dict := reader.Dict{
		"Type":    reader.Name("Annot"),
		"Subtype": reader.Name("FreeText"),
		"Rect":    rectArray(f.X0, f.Y0, f.X1, f.Y1),
		// ⛔ REQUIRED by Table 174 even though the appearance decides what is
		// drawn: "DA string (Required) The default appearance string that shall
		// be used in formatting the text". A reader that regenerates the
		// appearance — which is what a reader does when somebody EDITS the
		// comment — works from this and from nothing else, so a file written
		// without it is one that cannot be commented on further.
		"DA":       reader.String(fmt.Sprintf("/%s %s Tf %s %s %s rg", fontResourceName(f.face()), number(f.size()), number(r), number(g), number(b))),
		"Q":        reader.Integer(int(f.Align)),
		"F":        reader.Integer(annotPrint),
		"Contents": reader.String(f.Text),
		"M":        reader.String(pdfDate(now())),
		"AP":       reader.Dict{"N": w.Add(freeTextAppearance(f, r, g, b))},
	}
	if f.Author != "" {
		dict["T"] = reader.String(f.Author)
	}
	return dict
}

// freeTextAppearance draws the text in its box.
//
// ⛔ CLIPPED TO THE BOX. A free text annotation whose text is longer than its
// rectangle is the normal case — somebody typed more than they measured — and
// 12.5.6.6 puts the text inside the rectangle. Without the clip the overflow
// paints across whatever the page says underneath, which looks exactly like a
// corrupted page rather than like a comment that needs resizing.
func freeTextAppearance(f FreeText, r, g, b float64) *reader.Stream {
	size, face := f.size(), f.face()
	var body bytes.Buffer
	fmt.Fprintf(&body, "q %s %s %s %s re W n\n",
		number(f.X0), number(f.Y0), number(f.X1-f.X0), number(f.Y1-f.Y0))
	fmt.Fprintf(&body, "%s %s %s rg BT /%s %s Tf\n",
		number(r), number(g), number(b), fontResourceName(face), number(size))
	// The first baseline sits one line below the top edge, which is where a
	// reader of a text box expects the first line to start.
	y := f.Y1 - size
	fit := linesThatFit(f.Y1-f.Y0, size)
	for _, line := range freeTextLines(f.Text, face, size, f.X1-f.X0, fit) {
		x := f.X0
		switch f.Align {
		case AlignCentre:
			x = f.X0 + (f.X1-f.X0-face.Width(line, size))/2
		case AlignRight:
			x = f.X1 - face.Width(line, size)
		}
		fmt.Fprintf(&body, "1 0 0 1 %s %s Tm ", number(max(x, f.X0)), number(y))
		body.Write(pdfString(encodeWinAnsi(line)))
		fmt.Fprint(&body, " Tj\n")
		y -= size * lineSpacing
	}
	fmt.Fprint(&body, "ET Q\n")
	return &reader.Stream{
		Dict: reader.Dict{
			"Type":      reader.Name("XObject"),
			"Subtype":   reader.Name("Form"),
			"BBox":      rectArray(f.X0, f.Y0, f.X1, f.Y1),
			"Resources": stampResources(map[Font]bool{face: true}, nil),
		},
		Raw: body.Bytes(),
	}
}

// freeTextLines breaks the comment into the lines that will be drawn.
//
// ⛔ IT WRAPS THROUGH THE wrapToWidth xfa.go ALREADY HAD, one paragraph at a
// time, rather than growing a second word-wrapper beside it. The one difference
// it needs is the one that function deliberately does not make: `strings.Fields`
// collapses every blank INCLUDING a newline, which is right for a form field
// whose value happens to contain one and wrong for a comment somebody typed on
// three lines. So the newlines are honoured here and the words are wrapped
// there.
//
// A word longer than the box is not cut into pieces — wrapToWidth puts it on a
// line of its own and the clip takes what does not fit. Breaking inside a word
// would invent a hyphen nobody typed, and a comment reading "concatena tion" is
// worse than one whose long word is visibly cropped.
// ⛔ AND IT STOPS AT `limit` LINES, which is a resource bound and not a
// nicety. The appearance is clipped to the box, so lines past its foot are
// never seen — but wrapping the whole text first means a one-line box holding a
// pasted megabyte measures and allocates a megabyte of lines to throw them all
// away. The limit is how many lines the box can show, so the work is bounded by
// the BOX and not by the input. The annotation's /Contents still carries every
// byte the writer typed, because that is the comment; only the drawing stops.
func freeTextLines(text string, face Font, size, width float64, limit int) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if len(out) >= limit {
			return out[:limit]
		}
		wrapped := wrapToWidth(face, para, size, width)
		if len(wrapped) == 0 {
			// An empty line between two paragraphs is a line the writer typed.
			out = append(out, "")
			continue
		}
		out = append(out, wrapped...)
	}
	if len(out) > limit {
		return out[:limit]
	}
	return out
}

// linesThatFit is how many lines of this size the box can show, and at least
// one: a box shorter than its own type still draws the first line, clipped,
// which is visibly a box too small rather than a comment that vanished.
func linesThatFit(height, size float64) int {
	return max(int(height/(size*lineSpacing))+1, 1)
}

// lineSpacing is how far apart the baselines of a comment sit, as a multiple of
// the point size.
const lineSpacing = 1.2
