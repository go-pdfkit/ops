package ops

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/go-pdfkit/reader"
)

// A Position says where on a page something goes.
type Position uint8

// The nine places a stamp can sit.
const (
	Center Position = iota
	TopLeft
	TopCenter
	TopRight
	BottomLeft
	BottomCenter
	BottomRight
	MiddleLeft
	MiddleRight
)

// A Stamp is a line of text drawn on a page.
//
// The text may name what it is being drawn on: {page} is the page's number,
// {pages} the number of pages, and {n} a counter that starts wherever the
// stamp says and advances once per page stamped, which is what a numbering
// scheme like Bates needs.
type Stamp struct {
	Text     string
	Font     Font
	Size     float64    // in points; zero means twelve
	Colour   [3]float64 // red, green and blue, each from zero to one
	Opacity  float64    // zero means fully opaque
	Rotate   float64    // degrees, anticlockwise, about the text's own centre
	Position Position
	Margin   float64 // distance from the edge, in points; zero means 24
	Start    int     // the first value of {n}; zero means one
	Digits   int     // {n} padded to this many digits
}

// stampInstance is one stamp already resolved for a particular page.
type stampInstance struct {
	stamp Stamp
	text  string
}

// Stamp draws text on the pages a range names.
func (d *Doc) Stamp(spec string, s Stamp) error {
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	if strings.TrimSpace(s.Text) == "" {
		return fmt.Errorf("ops: a stamp with no text draws nothing")
	}
	counter := s.Start
	if counter == 0 {
		counter = 1
	}
	for _, n := range nums {
		text := expand(s.Text, n, len(d.pages), counter, s.Digits)
		d.pages[n-1].marks = append(d.pages[n-1].marks, stampInstance{stamp: s, text: text})
		counter++
	}
	return nil
}

// Watermark draws large, pale, slanted text across the pages a range names —
// the thing the word usually means.
func (d *Doc) Watermark(spec, text string) error {
	return d.Stamp(spec, Stamp{
		Text:    text,
		Font:    HelveticaBold,
		Size:    64,
		Colour:  [3]float64{0.5, 0.5, 0.5},
		Opacity: 0.25,
		Rotate:  45,
	})
}

// PageNumbers writes a number at the foot of the pages a range names. The
// format is a stamp's text, so "{page} of {pages}" and "— {page} —" both work.
func (d *Doc) PageNumbers(spec, format string) error {
	if format == "" {
		format = "{page}"
	}
	return d.Stamp(spec, Stamp{
		Text:     format,
		Font:     Helvetica,
		Size:     10,
		Position: BottomCenter,
	})
}

// Bates numbers the pages a range names with a running serial, the way a legal
// exhibit is marked: a fixed prefix and a zero-padded counter.
func (d *Doc) Bates(spec, prefix string, start, digits int) error {
	if digits < 1 {
		digits = 6
	}
	return d.Stamp(spec, Stamp{
		Text:     prefix + "{n}",
		Font:     Helvetica,
		Size:     9,
		Position: BottomRight,
		Start:    start,
		Digits:   digits,
	})
}

// expand fills in what a stamp's text says about the page it is on.
func expand(format string, page, pages, counter, digits int) string {
	n := strconv.Itoa(counter)
	for len(n) < digits {
		n = "0" + n
	}
	r := strings.NewReplacer(
		"{page}", strconv.Itoa(page),
		"{pages}", strconv.Itoa(pages),
		"{n}", n,
	)
	return r.Replace(format)
}

// stampContent draws every stamp on a page, and reports which of the standard
// faces it used and whether any of them needed transparency.
func (d *Doc) stampContent(p Page, area [4]float64) (content []byte, fonts map[Font]bool, alpha map[string]float64) {
	fonts = map[Font]bool{}
	alpha = map[string]float64{}
	var buf bytes.Buffer
	for _, m := range p.marks {
		s := m.stamp
		if s.Size == 0 {
			s.Size = 12
		}
		if s.Margin == 0 {
			s.Margin = 24
		}
		fonts[s.Font] = true
		width := s.Font.Width(m.text, s.Size)
		// A line of text sits on its baseline; these two are close enough to
		// the ascent and descent of the standard faces to centre by.
		ascent, descent := s.Size*0.72, s.Size*0.21
		x, y := place(s.Position, area, width, ascent+descent, s.Margin)

		buf.WriteString("q\n")
		if s.Opacity > 0 && s.Opacity < 1 {
			name := fmt.Sprintf("PdfopsA%d", int(math.Round(s.Opacity*1000)))
			alpha[name] = s.Opacity
			fmt.Fprintf(&buf, "/%s gs\n", name)
		}
		fmt.Fprintf(&buf, "%s %s %s rg\n",
			number(s.Colour[0]), number(s.Colour[1]), number(s.Colour[2]))
		// Turn about the middle of the text rather than its corner, so a
		// slanted watermark stays where it was put.
		cx, cy := x+width/2, y+(ascent-descent)/2
		if s.Rotate != 0 {
			rad := s.Rotate * math.Pi / 180
			cos, sin := math.Cos(rad), math.Sin(rad)
			fmt.Fprintf(&buf, "1 0 0 1 %s %s cm\n", number(cx), number(cy))
			fmt.Fprintf(&buf, "%s %s %s %s 0 0 cm\n", number(cos), number(sin), number(-sin), number(cos))
			fmt.Fprintf(&buf, "1 0 0 1 %s %s cm\n", number(-cx), number(-cy))
		}
		fmt.Fprintf(&buf, "BT /%s %s Tf %s %s Td ", fontResourceName(s.Font), number(s.Size), number(x), number(y))
		buf.Write(pdfString(encodeWinAnsi(m.text)))
		buf.WriteString(" Tj ET\nQ\n")
	}
	return buf.Bytes(), fonts, alpha
}

// place works out where the left end of the baseline goes.
func place(pos Position, area [4]float64, width, height, margin float64) (x, y float64) {
	size := [2]float64{area[2] - area[0], area[3] - area[1]}
	switch pos {
	case TopLeft, MiddleLeft, BottomLeft:
		x = area[0] + margin
	case TopRight, MiddleRight, BottomRight:
		x = area[0] + size[0] - margin - width
	default:
		x = area[0] + (size[0]-width)/2
	}
	switch pos {
	case TopLeft, TopCenter, TopRight:
		y = area[1] + size[1] - margin - height
	case BottomLeft, BottomCenter, BottomRight:
		y = area[1] + margin
	default:
		y = area[1] + (size[1]-height)/2
	}
	// A page smaller than the margin still gets its text, at the edge.
	x = math.Max(area[0], math.Min(x, area[0]+size[0]-width))
	y = math.Max(area[1], math.Min(y, area[1]+size[1]-height))
	return x, y
}

// number renders a coordinate the way a content stream spells one.
func number(v float64) string { return string(reader.FormatObject(reader.Real(v))) }

// fontResourceName is the name a stamp's face is given in the resources.
func fontResourceName(f Font) string {
	switch f {
	case HelveticaBold:
		return "PdfopsFB"
	case Courier:
		return "PdfopsFC"
	case CourierBold:
		return "PdfopsFCB"
	}
	return "PdfopsF"
}

// stampResources builds the resource entries a page's stamps need: one font
// dictionary per face used, and one graphics state per level of transparency.
func stampResources(fonts map[Font]bool, alpha map[string]float64) reader.Dict {
	out := reader.Dict{}
	if len(fonts) > 0 {
		fd := reader.Dict{}
		for f := range fonts {
			fd[reader.Name(fontResourceName(f))] = reader.Dict{
				"Type":     reader.Name("Font"),
				"Subtype":  reader.Name("Type1"),
				"BaseFont": reader.Name(f.name()),
				"Encoding": reader.Name("WinAnsiEncoding"),
			}
		}
		out["Font"] = fd
	}
	if len(alpha) > 0 {
		gs := reader.Dict{}
		for name, v := range alpha {
			gs[reader.Name(name)] = reader.Dict{
				"Type": reader.Name("ExtGState"),
				"ca":   reader.Real(v),
				"CA":   reader.Real(v),
			}
		}
		out["ExtGState"] = gs
	}
	return out
}

// mergeResources adds entries to a resources dictionary without disturbing
// what is already in it, sub-dictionary by sub-dictionary.
func mergeResources(into, extra reader.Dict) reader.Dict {
	if into == nil {
		into = reader.Dict{}
	}
	for key, add := range extra {
		addDict, _ := reader.ToDict(add)
		existing, ok := reader.ToDict(into.Get(key))
		if !ok {
			into[key] = addDict
			continue
		}
		merged := reader.Dict{}
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range addDict {
			merged[k] = v
		}
		into[key] = merged
	}
	return into
}
