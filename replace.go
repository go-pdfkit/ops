package ops

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/pdffont"
	"github.com/go-pdfkit/reader"
)

// Replacing the text a page already carries: not a mark over it, not a comment
// beside it, but the words themselves.
//
// ⛔⛔ THE HARD PART IS NOT THE STRING, IT IS EVERYTHING AFTER IT. A page sets
// glyphs by advancing a pen: the position of every later glyph on the line
// follows from the width of the ones before it. So a replacement of a different
// width silently moves the rest of the line, and a page that merely LOOKS
// re-flowed is the failure this verb must not have.
//
// The spec supplies the remedy. 9.4.3 says a number in a TJ array "shall be
// expressed in thousandths of a unit of text space … This amount shall be
// subtracted from the current horizontal … coordinate", so a positive number
// moves the next glyph LEFT. And 9.4.4's displacement formula multiplies BOTH
// the glyph's own width and that number by the font size — which is why the
// adjustment needed here is exactly the difference of the two raw width sums,
// in the same thousandths the /Widths array uses, with no font size in it at
// all. A test asserts that size-independence rather than taking it on trust.
//
// Everything this cannot do correctly it REFUSES BY NAME, when ReplaceText is
// called rather than when the document is written — the standard Doc.Redact
// draws, and for its reason: a caller told at once can choose other words, and
// one told at Save has already been promised an edited document by everything
// in between.
//
// ISO 32000-1:2008, 9.4.3 (text-showing operators) and 9.4.4 (text space).

// A TextEdit replaces the whole text of one text-showing operation.
//
// ⛔ IT NAMES THE OPERATION, not the words. Finding a run by its text would be
// friendlier and wrong: the same words appear twice on many pages, and a verb
// that edited "the first one" would edit whichever the scan happened to reach
// first. [extract.Run.Op] is the span a caller already has from reading the
// page, and it says exactly which drawing is meant.
type TextEdit struct {
	// Page is 1-based.
	Page int
	// At is the operation to rewrite, as extract.Run.Op reports it.
	At extract.Span
	// Now is the text to draw instead. It may not be empty: a replacement by
	// nothing is a redaction, which Doc.Redact does properly — including
	// painting over the hole.
	Now string
}

// ReplaceText rewrites the text of operations the caller names.
//
// What it refuses, each with the page and the words at issue:
//
//   - a run drawn from inside a Form XObject, whose span does not index the
//     page's own stream and which is SHARED with every other page that draws
//     it. Doc.Redact and Doc.RecolourText stop at the same wall.
//   - an operation that draws more text than the run being replaced — a TJ
//     array of several strings. Rewriting the whole of it would re-draw the
//     others from Unicode, and Unicode does not round-trip back to font codes.
//   - the operators ' and ", which also move to the next line and set the word
//     and character spacing. Re-emitting them as a TJ array would lose those
//     effects, and the page would re-flow around an edit that looked local.
//   - a composite (CID) font, where the bytes of the string are codes a CMap
//     defines and nothing can invert. A simple font's encoding CAN be walked
//     code by code, which is what makes the rest of this possible.
//   - a character the page's own font cannot draw. The replacement has to be
//     written in the codes that font understands; a rune it has no code for
//     would come out as a blank or as the wrong glyph.
//   - a replacement with a different number of spaces, because word spacing
//     (Tw) adds a displacement per space character that this cannot see from
//     the stream alone. Same count, same Tw, same answer.
func (d *Doc) ReplaceText(edits ...TextEdit) error {
	for _, e := range edits {
		if e.Page < 1 || e.Page > len(d.pages) {
			return fmt.Errorf("ops: there is no page %d whose text could be replaced", e.Page)
		}
		if e.Now == "" {
			return fmt.Errorf("ops: the replacement for page %d is empty: removing "+
				"text is a redaction, which Doc.Redact does and paints over", e.Page)
		}
		if e.At.Empty() {
			return fmt.Errorf("ops: the edit of page %d names no operation", e.Page)
		}
		p := &d.pages[e.Page-1]
		if p.src == nil {
			return fmt.Errorf("ops: page %d was made rather than read, so it "+
				"carries no text to replace", e.Page)
		}
		if len(p.redactions) > 0 {
			return fmt.Errorf("ops: page %d is already being redacted, and one "+
				"page cannot be redacted and have its text replaced in the same "+
				"write: a redaction finds its operations by scanning the ORIGINAL "+
				"stream, and a replacement changes that stream's length, so the "+
				"two would disagree about where every later operation is. Write "+
				"the document, open it again, and do the second edit to that", e.Page)
		}
		if _, err := d.plannedEdit(p, e); err != nil {
			return err
		}
		p.textEdits = append(p.textEdits, e)
	}
	return nil
}

// a plannedEdit is one rewrite worked out and ready to splice: the span it
// replaces and the bytes to put there.
type plannedEdit struct {
	at  extract.Span
	now []byte
}

// plannedEdit works out what an edit would write, or why it cannot be done.
//
// ⛔ IT IS THE SAME FUNCTION FOR THE CHECK AND FOR THE WRITE. The refusal at
// call time and the bytes at write time must agree about every one of the
// conditions above; two functions that had to agree would be two chances to
// disagree, and the way that failure shows is an edit accepted and then
// silently skipped.
func (d *Doc) plannedEdit(p *Page, e TextEdit) (plannedEdit, error) {
	content, err := pageContent(p.src, p.number)
	if err != nil {
		return plannedEdit{}, fmt.Errorf("ops: the content of page %d could not "+
			"be read: %w", e.Page, err)
	}
	// ⛔⛔ THE ORDER OF WHAT FOLLOWS IS CHOSEN BY THE MESSAGE IT PRODUCES, and it
	// was wrong once in a way worth recording. A bounds check on the span used to
	// come first, which looked like good hygiene and ruined the answer: a run
	// drawn inside a FORM carries a span into the form's stream, not the page's,
	// so the bounds check fired on every such run and said "the edit names bytes
	// 23 to 33 of a stream 6 long" — true, useless, and hiding the real reason,
	// which is that the form is shared with every page that draws it.
	//
	// So the run is found first and the form refused by name. After that the span
	// cannot be out of range at all: it belongs to a run of THIS page's own
	// stream. spanBytes slices safely anyway, so a future mistake in extract
	// becomes a wrong refusal rather than a panic.
	runs, err := extractRuns(p.src, p.number)
	if err != nil {
		return plannedEdit{}, fmt.Errorf("ops: page %d could not be read to "+
			"replace its text: %w", e.Page, err)
	}
	var mine []extract.Run
	for _, r := range runs {
		if r.Op == e.At {
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		return plannedEdit{}, fmt.Errorf("ops: page %d draws no text at the "+
			"operation named by the edit", e.Page)
	}
	if len(mine) > 1 {
		return plannedEdit{}, fmt.Errorf("ops: the operation named on page %d "+
			"draws %d pieces of text (%s): rewriting it would have to re-draw "+
			"the others from their characters, and a font's codes cannot be "+
			"recovered from characters", e.Page, len(mine), summarise(mine[0].Text))
	}
	run := mine[0]
	if run.InForm {
		return plannedEdit{}, fmt.Errorf("ops: %s on page %d is drawn from "+
			"inside a form, which is shared with every page that draws it",
			summarise(run.Text), e.Page)
	}
	op := operatorOf(spanBytes(content, e.At))
	if op != "Tj" && op != "TJ" {
		return plannedEdit{}, fmt.Errorf("ops: %s on page %d is drawn with %q, "+
			"which also moves to the next line or sets the spacing: rewriting it "+
			"would lose that and re-flow the page", summarise(run.Text), e.Page, op)
	}
	font, err := d.runFont(p, run)
	if err != nil {
		return plannedEdit{}, err
	}
	if font.Kind() != pdffont.Simple {
		return plannedEdit{}, fmt.Errorf("ops: %s on page %d is set in a "+
			"composite font, whose byte codes are defined by a map this cannot "+
			"invert", summarise(run.Text), e.Page)
	}
	if was, now := strings.Count(run.Text, " "), strings.Count(e.Now, " "); was != now {
		return plannedEdit{}, fmt.Errorf("ops: %s on page %d holds %d space(s) "+
			"and the replacement holds %d: word spacing adds a step per space "+
			"that cannot be read back from the stream, so the line would move",
			summarise(run.Text), e.Page, was, now)
	}
	// ⛔⛔ BOTH WIDTHS COME FROM THE SAME ENCODER, which is a simplification
	// with a reason rather than a shortcut. The first draft measured the OLD
	// string by finding the parentheses in the stream and undoing PDF's string
	// escaping — a parser, with its own branches for \n, \( and octal triples,
	// every one of them a place to be wrong about a width. Encoding the run's
	// own text through the same `encodeIn` the replacement goes through gives
	// the old codes without parsing anything, and it makes the two sides of the
	// subtraction the same function.
	//
	// ⛔ It also turns an unreadable run into an honest refusal. A run whose
	// codes said nothing about their characters (extract.Run.Unreadable) cannot
	// be re-encoded, and this now says so instead of measuring a string it
	// guessed at.
	//
	// ⛔ And NOT run.Width, which is in POINTS — already multiplied by the font
	// size — while the adjustment is in the font's own thousandths. Mixing the
	// two is how a kern comes out a factor of the size wrong.
	wasCodes, err := encodeIn(font, run.Text)
	if err != nil {
		return plannedEdit{}, fmt.Errorf("ops: page %d draws %s with codes that "+
			"cannot be read back as characters, so the width it occupies cannot "+
			"be measured: %w", e.Page, summarise(run.Text), err)
	}
	codes, err := encodeIn(font, e.Now)
	if err != nil {
		return plannedEdit{}, fmt.Errorf("ops: page %d: %w", e.Page, err)
	}
	delta := widthOf(font, codes) - widthOf(font, wasCodes)
	return plannedEdit{at: e.At, now: showOperator(codes, delta)}, nil
}

// runFont is the font a run is set in, read from the page's resources.
//
// ⛔ ONE GUARD FOR THE WHOLE WALK, not one per step. Getting here means
// following page → Resources → Font → the run's name, and the first draft
// checked an error after each: six branches, six messages, and all six saying
// the same thing in the end — this page does not describe that font. A caller
// can do nothing different with "the resources could not be read" than with
// "the font could not be read", and six arms are six tests to write for one
// fact. The errors are dropped deliberately: whatever went wrong on the way,
// what decides is whether a font dictionary came out, and the one message names
// the page, the words and the font.
func (d *Doc) runFont(p *Page, run extract.Run) (*pdffont.Font, error) {
	page, _ := p.src.Page(p.number)
	res, _ := p.src.Resolve(page.Get("Resources"))
	dict, _ := reader.ToDict(res)
	fonts, _ := p.src.Resolve(dict.Get("Font"))
	all, _ := reader.ToDict(fonts)
	one, _ := p.src.Resolve(all.Get(reader.Name(run.Font)))
	fd, ok := reader.ToDict(one)
	if !ok {
		return nil, fmt.Errorf("ops: page %d draws %s with the font %q, which its "+
			"resources do not describe", p.number, summarise(run.Text), run.Font)
	}
	return pdffont.Read(p.src, fd), nil
}

// encodeIn turns text into the byte codes a simple font draws it with, or says
// which character it cannot draw.
//
// ⛔ BY WALKING THE FONT'S OWN ENCODING, not by assuming one. A simple font's
// 256 codes each stand for some text — pdffont.Text answers for whichever
// encoding, differences and glyph names the document actually gave — so the map
// from text back to code is that walk reversed. Assuming WinAnsi would be right
// for most files and silently wrong for the ones that matter: a font with a
// /Differences array exists precisely because its author wanted other glyphs.
func encodeIn(font *pdffont.Font, text string) ([]byte, error) {
	back := map[string]byte{}
	for code := 255; code >= 0; code-- {
		if !font.HasWidth(code) {
			continue
		}
		if s, ok := font.Text(code); ok && s != "" {
			back[s] = byte(code)
		}
	}
	out := make([]byte, 0, len(text))
	for _, r := range text {
		code, ok := back[string(r)]
		if !ok {
			return nil, fmt.Errorf("the font cannot draw %q", r)
		}
		out = append(out, code)
	}
	return out, nil
}

// widthOf is the total width of these codes, in the thousandths of text space
// that a TJ adjustment is written in.
//
// ⛔ THE THOUSAND IS NOT DECORATION. pdffont.Width answers in TEXT SPACE — 0.5
// for a /Widths entry of 500, the glyph space the font declares divided by its
// 1000 units — and 9.4.3 says a TJ number is "expressed in thousandths of a
// unit of text space". Leaving the factor out writes a kern a thousand times too
// small, which is invisible on the page and exactly what the first draft did:
// the test for "what follows did not move" caught it moving by 17.982 points of
// the 18 it should have cancelled.
func widthOf(font *pdffont.Font, codes []byte) float64 {
	total := 0.0
	for _, c := range codes {
		total += font.Width(int(c))
	}
	return total * 1000
}

// spanBytes is the stretch of a stream a span names, or nothing when the span
// does not fit in it.
//
// ⛔ It never panics and it never truncates: a span that does not fit is a span
// about another stream, and half of one would be read as an operator it is not.
func spanBytes(data []byte, s extract.Span) []byte {
	if s.From < 0 || s.To > len(data) || s.To <= s.From {
		return nil
	}
	return data[s.From:s.To]
}

// operatorOf is the operator name at the end of an operation's bytes.
func operatorOf(op []byte) string {
	fields := strings.Fields(string(op))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// showOperator is the replacement operation: the new codes, and the kern that
// keeps everything after them where it was.
//
// ⛔ A TJ ARRAY EVEN WHEN THE WIDTH DID NOT CHANGE, because the one case where
// it would matter is the one where a Tj is tempting: a replacement of exactly
// the same width still has to be the same SHAPE of operator for the rest of the
// stream to be unaffected by the splice, and one form for both means one path
// to test. The kern is left out when it is zero, which keeps the common case
// readable in the file.
func showOperator(codes []byte, delta float64) []byte {
	var out bytes.Buffer
	out.WriteByte('[')
	out.Write(pdfString(codes))
	if r := roundTo(delta, 1e-3); r != 0 {
		// Positive moves the next glyph LEFT (9.4.3), which is what cancels a
		// replacement that came out wider.
		fmt.Fprintf(&out, " %s", number(r))
	}
	out.WriteString("] TJ")
	return out.Bytes()
}

// roundTo rounds to a multiple, so a kern of 1e-14 does not reach the file.
func roundTo(v, unit float64) float64 {
	n := v / unit
	if n < 0 {
		n -= 0.5
	} else {
		n += 0.5
	}
	return float64(int64(n)) * unit
}

// editedContent is a page's stream with every edit spliced in.
//
// ⛔ APPLIED FROM THE END BACKWARDS. A replacement is a different length from
// what it replaces, so every offset after it moves — unlike the redaction
// beside it, which writes spaces over the same number of bytes and so needs no
// ordering at all. Taking the edits in descending order of their start means no
// offset is ever used after the bytes under it have shifted.
func editedContent(data []byte, edits []plannedEdit) []byte {
	if len(edits) == 0 {
		return data
	}
	sorted := make([]plannedEdit, len(edits))
	copy(sorted, edits)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].at.From > sorted[b].at.From })
	out := make([]byte, len(data))
	copy(out, data)
	for _, e := range sorted {
		if e.at.From < 0 || e.at.To > len(out) || e.at.To <= e.at.From {
			continue
		}
		next := make([]byte, 0, len(out)-(e.at.To-e.at.From)+len(e.now))
		next = append(next, out[:e.at.From]...)
		next = append(next, e.now...)
		next = append(next, out[e.at.To:]...)
		out = next
	}
	return out
}

// plannedEdits is every edit of a page worked out, or the error that must stop
// the write.
func (d *Doc) plannedEdits(p Page) ([]plannedEdit, error) {
	if len(p.textEdits) == 0 {
		return nil, nil
	}
	page := p
	out := make([]plannedEdit, 0, len(p.textEdits))
	for _, e := range p.textEdits {
		got, err := d.plannedEdit(&page, e)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}
