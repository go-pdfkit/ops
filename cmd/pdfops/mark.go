package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-pdfkit/ops"
)

// runMark writes text markup annotations over the pages: a highlight, an
// underline, a strikeout or a squiggly underline.
//
// ⛔ THE VERB IS WHAT MAKES THE CAPABILITY REACHABLE. Doc.Mark shipped in the
// library with nothing a person can run calling it, which is a state this
// fleet has already paid for: measured across thirty repositories, four
// shipped a command and twenty-five did not, and the capability inventory had
// to be corrected five times because rows that read "missing" were libraries
// with no caller. A library nothing calls is also a library nothing holds to
// its word.
func runMark(c *context, args []string) error {
	fs := flags("mark")
	area := fs.String("area", "", "the rectangle to mark, as page:x0,y0,x1,y1 in points; repeat for more")
	kind := fs.String("as", "highlight", "highlight, underline, strikeout or squiggly")
	colour := fs.String("colour", "", "the mark's colour as r,g,b in 0..1; the default is a highlighter's yellow")
	note := fs.String("note", "", "what the mark says, shown when it is opened")
	author := fs.String("author", "", "who made the mark")
	var areas multiFlag
	fs.Var(&areas, "and", "another rectangle, as page:x0,y0,x1,y1")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "-area <page:x0,y0,x1,y1> [-and ...] [-as <kind>] [-colour r,g,b] [-note <text>] [-author <name>] <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	if *area == "" {
		return fmt.Errorf("pdfops mark: -area says what to mark, as page:x0,y0,x1,y1 in points")
	}
	k, err := markKind(*kind)
	if err != nil {
		return err
	}
	r, g, b, err := markColour(*colour)
	if err != nil {
		return err
	}

	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	// ⛔ One annotation PER AREA, not one annotation holding every area. A
	// markup annotation may carry several quadrilaterals, and that is for the
	// several LINES of one selection — "each quadrilateral shall encompass a
	// word or group of contiguous words" (ISO 32000-1, Table 179). Two
	// unrelated marks in one annotation would share a note, a colour and a
	// date, and come off together.
	for _, spec := range append([]string{*area}, areas...) {
		page, quad, err := parseArea(spec)
		if err != nil {
			return err
		}
		m := ops.Markup{
			Page: page, Kind: k, Quads: []ops.Quad{quad},
			R: r, G: g, B: b, Note: *note, Author: *author,
		}
		if err := d.Mark(m); err != nil {
			return err
		}
	}
	if err := save(d, fs.Arg(1)); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "marked %d area(s) as %s\n", 1+len(areas), *kind)
	return nil
}

// A multiFlag collects a flag given more than once.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, " ") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// markKind reads the name of a mark.
//
// ⛔ It refuses a name it does not know rather than falling back to a
// highlight: somebody who typed "strikethrough" meant to strike text out, and
// a yellow wash appearing instead is a wrong answer dressed as a right one.
func markKind(s string) (ops.MarkupKind, error) {
	switch strings.ToLower(s) {
	case "highlight":
		return ops.MarkHighlight, nil
	case "underline":
		return ops.MarkUnderline, nil
	case "strikeout":
		return ops.MarkStrikeOut, nil
	case "squiggly":
		return ops.MarkSquiggly, nil
	}
	return 0, fmt.Errorf("pdfops mark: %q is not a kind of mark; it reads "+
		"highlight, underline, strikeout or squiggly", s)
}

// markColour reads an r,g,b triple in 0..1, or says there is none.
func markColour(s string) (r, g, b float64, err error) {
	if strings.TrimSpace(s) == "" {
		return 0, 0, 0, nil // the library reads this as its own default
	}
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("pdfops mark: %q is not a colour; it reads r,g,b in 0..1", s)
	}
	out := make([]float64, 3)
	for i, p := range parts {
		v, e := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if e != nil || v < 0 || v > 1 {
			return 0, 0, 0, fmt.Errorf("pdfops mark: %q is not a number in 0..1", strings.TrimSpace(p))
		}
		out[i] = v
	}
	return out[0], out[1], out[2], nil
}

// parseArea reads page:x0,y0,x1,y1.
//
// ⛔ In POINTS and from the foot of the page, which is the space a PDF is
// written in and the space the library takes. Saying so in the flag's help is
// the difference between a mark where somebody meant it and a mark near the
// opposite edge.
func parseArea(s string) (int, ops.Quad, error) {
	page, rest, found := strings.Cut(s, ":")
	if !found {
		return 0, ops.Quad{}, fmt.Errorf("pdfops mark: %q has no page; an area reads page:x0,y0,x1,y1", s)
	}
	n, err := strconv.Atoi(strings.TrimSpace(page))
	if err != nil || n < 1 {
		return 0, ops.Quad{}, fmt.Errorf("pdfops mark: %q is not a page number", strings.TrimSpace(page))
	}
	parts := strings.Split(rest, ",")
	if len(parts) != 4 {
		return 0, ops.Quad{}, fmt.Errorf("pdfops mark: %q is not a rectangle; it reads x0,y0,x1,y1 in points", rest)
	}
	v := make([]float64, 4)
	for i, p := range parts {
		f, e := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if e != nil {
			return 0, ops.Quad{}, fmt.Errorf("pdfops mark: %q is not a number", strings.TrimSpace(p))
		}
		v[i] = f
	}
	if v[0] == v[2] || v[1] == v[3] {
		return 0, ops.Quad{}, fmt.Errorf("pdfops mark: %q has no area, so it would mark nothing", rest)
	}
	return n, ops.QuadOf(v[0], v[1], v[2], v[3]), nil
}
