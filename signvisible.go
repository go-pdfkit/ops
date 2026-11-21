// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
)

// A visible signature (ISO 32000-1 §12.8, §12.5.5) is the same signature with a
// widget that has a size and a picture. The signature itself is unchanged; what
// is added is the widget's /Rect, its /F (Print, 4) and an /AP /N form XObject
// holding an image and/or a block of Helvetica text. Every one of those objects
// is written in the SAME incremental update as the signature dictionary, so the
// /ByteRange covers them: an appearance edited after signing breaks the
// signature, which is what makes it part of what was signed.

// SignAppearance is how a signature shows on its page. nil in
// [SignOptions.Appearance] keeps the signature invisible.
type SignAppearance struct {
	// Rect is the box, x0 y0 x1 y1 in the page's own points (the page is
	// [SignOptions.Page]), no coordinate beyond 14400 in absolute value. When
	// signing an EXISTING empty signature field, the zero value means the field
	// widget's own /Rect, and only the field's FIRST widget gets the appearance
	// (a field shown in several places is drawn in one).
	Rect [4]float64
	// Image is an optional picture, in any format [Doc.Picture] reads. Its
	// aspect ratio is kept and it is fitted and centred in the box.
	Image []byte
	// Text is optional, drawn in Helvetica over the image; a \n starts a line
	// and long lines are wrapped. When Image and Text are both empty the text is
	// "Digitally signed by <the certificate's common name>" and a line with the
	// signing time in RFC 3339, UTC. Only characters WinAnsiEncoding has a code
	// for can be drawn: any other (Cyrillic, CJK, ...) is refused with
	// [ErrAppearanceText], in the default text too, when the common name has
	// one; pass a Text of your own then. A Unicode (Type0) font is future work.
	Text string
	// FontSize is the text's size in points: 0 means the largest, up to 24,
	// that fits the box; any other value is from 1 to 1000.
	FontSize float64
	// Color is the text's RGB, each 0..1; the zero value is black.
	Color [3]float64
	// MaxPixels is the largest Image, width times height, that will be
	// decoded; 0 means the default ceiling of [Doc.SetMaxPixels].
	MaxPixels int
}

// The refusals an appearance can meet.
var (
	// ErrAppearanceRect: the box is not finite, is inverted, has no area, or
	// has a coordinate beyond 14400 points.
	ErrAppearanceRect = errors.New("ops: the signature's box is not a box with an area, within 14400 points of the origin")
	// ErrAppearanceOffPage: the box lies outside the page's MediaBox by more
	// than the box itself.
	ErrAppearanceOffPage = errors.New("ops: the signature's box is far outside the page")
	// ErrAppearanceImage: the image is over the pixel ceiling or does not decode.
	ErrAppearanceImage = errors.New("ops: the signature's image cannot be used")
	// ErrAppearanceText: the text is longer than maxAppearanceText bytes, or
	// holds a character Helvetica in WinAnsiEncoding cannot draw (the message
	// names the first). That is also true of the default text, which is made of
	// the certificate's common name: pass a Text that can be drawn. A Unicode
	// (Type0) font appearance is future work; nothing is dropped meanwhile.
	ErrAppearanceText = errors.New("ops: the signature's text cannot be drawn")
	// ErrAppearanceFontSize: the font size is not finite, or is a non-zero
	// size outside minFontSize..maxFontSize points.
	ErrAppearanceFontSize = errors.New("ops: the signature's font size is not 0 (fitted) or a number from 1 to 1000")
	// ErrAppearanceColor: a colour component is not finite.
	ErrAppearanceColor = errors.New("ops: the signature's colour is not made of finite numbers")
	// ErrNoFieldRect: the field being signed has no usable /Rect and none was
	// given.
	ErrNoFieldRect = errors.New("ops: the signature field has no usable rectangle, and none was given")
)

// maxAppearanceText bounds the text, so that wrapping it is bounded by this
// and not by the caller.
const maxAppearanceText = 4 << 10

// The sizes the fitted text is tried at, largest first.
const (
	fitMax  = 24.0
	fitMin  = 4.0
	fitStep = 0.5
	// The font sizes a caller may ask for, in points.
	minFontSize = 1.0
	maxFontSize = 1000.0
	// maxCoord is the largest coordinate a box may have: 14400 points is the
	// page limit of ISO 32000-1 Annex C, and a number past it is not a page.
	maxCoord = 14400.0
)

// defaultPageBox is what a page that gives no readable /MediaBox is judged
// against: A4, as the rest of this package assumes.
var defaultPageBox = [4]float64{0, 0, 595.276, 841.89}

// visible is a checked appearance: where it goes and what it draws.
type visible struct {
	rect   [4]float64
	pic    *picture
	text   string
	size   float64
	colour [3]float64
	// widget is the existing field's widget, nil for a new field.
	widget *forms.Widget
}

// checkRect says whether a box is finite and has an area, in a size that is
// itself finite.
func checkRect(r [4]float64) error {
	// A coordinate that is NaN makes w or h NaN, and the comparison false, so
	// the size test below refuses it; an infinite one is past maxCoord.
	for _, v := range r {
		if math.Abs(v) > maxCoord {
			return fmt.Errorf("%w: %v has a coordinate past %v", ErrAppearanceRect, r, maxCoord)
		}
	}
	w, h := r[2]-r[0], r[3]-r[1]
	if !(w > 0) || !(h > 0) {
		return fmt.Errorf("%w: %v", ErrAppearanceRect, r)
	}
	return nil
}

// checkAppearance refuses everything it can before anything is written, and
// works out the box: the one given, or the existing field's own.
func checkAppearance(d *reader.Document, o SignOptions, field *forms.Field) (*visible, error) {
	a := o.Appearance
	v := &visible{rect: a.Rect, size: a.FontSize, colour: a.Color}
	if math.IsNaN(a.FontSize) || (a.FontSize != 0 && (a.FontSize < minFontSize || a.FontSize > maxFontSize)) {
		return nil, fmt.Errorf("%w: %v", ErrAppearanceFontSize, a.FontSize)
	}
	for _, c := range a.Color {
		if math.IsNaN(c) || math.IsInf(c, 0) {
			return nil, fmt.Errorf("%w: %v", ErrAppearanceColor, a.Color)
		}
	}
	if len(a.Text) > maxAppearanceText {
		return nil, fmt.Errorf("%w: %d bytes, at most %d", ErrAppearanceText, len(a.Text), maxAppearanceText)
	}
	// What will be drawn: the text given, or the default one when there is
	// neither text nor image. Either is refused, not trimmed, for a character
	// WinAnsi has no code for.
	shown := a.Text
	if shown == "" && len(a.Image) == 0 {
		shown = defaultSignatureText(o, time.Time{})
	}
	if r, bad := undrawable(shown); bad {
		return nil, fmt.Errorf("%w: %q (U+%04X) has no code in WinAnsiEncoding; pass a Text that Helvetica can draw",
			ErrAppearanceText, r, r)
	}
	page := o.Page
	if page == 0 {
		page = 1
	}
	if field != nil {
		// A /Kids that is empty, or whose entries are not dictionaries,
		// leaves forms with no widget at all.
		if len(field.Widgets) == 0 {
			return nil, fmt.Errorf("%w: %q has no widget", ErrNoFieldRect, field.Name)
		}
		v.widget = &field.Widgets[0]
		if v.widget.Page > 0 {
			page = v.widget.Page
		}
		if a.Rect == [4]float64{} {
			r := v.widget.Rect
			v.rect = [4]float64{min(r[0], r[2]), min(r[1], r[3]), max(r[0], r[2]), max(r[1], r[3])}
			if err := checkRect(v.rect); err != nil {
				return nil, fmt.Errorf("%w: %q: %v", ErrNoFieldRect, field.Name, err)
			}
		}
	}
	if err := checkRect(v.rect); err != nil {
		return nil, err
	}
	if err := checkOnPage(d, page, v.rect); err != nil {
		return nil, err
	}
	if len(a.Image) > 0 {
		pic, err := readPicture(a.Image, a.MaxPixels)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAppearanceImage, err)
		}
		v.pic = pic
	}
	v.text = a.Text
	return v, nil
}

// undrawable is the first character of s that encodeWinAnsi would drop. A new
// line is not one: it starts a line.
func undrawable(s string) (rune, bool) {
	for _, r := range strings.ReplaceAll(s, "\r\n", "\n") {
		if r == '\n' {
			continue
		}
		if len(encodeWinAnsi(string(r))) == 0 {
			return r, true
		}
	}
	return 0, false
}

// pageBox reads a page box: four numbers, which may be indirect.
func pageBox(d *reader.Document, pd reader.Dict, key reader.Name) ([4]float64, bool) {
	arr, ok := reader.ToArray(followRef(d, pd.Get(key)))
	if !ok || len(arr) != 4 {
		return [4]float64{}, false
	}
	var n [4]float64
	for i, e := range arr {
		if n[i], ok = reader.ToFloat(followRef(d, e)); !ok {
			return [4]float64{}, false
		}
	}
	return [4]float64{min(n[0], n[2]), min(n[1], n[3]), max(n[0], n[2]), max(n[1], n[3])}, true
}

// checkOnPage refuses a box that lies outside the page, by a gap larger than
// the box's own width (to its left or right) or height (above or below). The
// page is its /CropBox when it has one (inherited, as the reader gives it),
// else its /MediaBox, else defaultPageBox when neither can be read.
func checkOnPage(d *reader.Document, page int, r [4]float64) error {
	mb := defaultPageBox
	// A page that cannot be read has no boxes and gives the default.
	pd, _ := d.Page(page)
	if b, ok := pageBox(d, pd, "CropBox"); ok {
		mb = b
	} else if b, ok = pageBox(d, pd, "MediaBox"); ok {
		mb = b
	}
	w, h := r[2]-r[0], r[3]-r[1]
	if r[0] > mb[2]+w || r[2] < mb[0]-w || r[1] > mb[3]+h || r[3] < mb[1]-h {
		return fmt.Errorf("%w: %v against the page's %v", ErrAppearanceOffPage, r, mb)
	}
	return nil
}

// followRef follows a reference; one that cannot be followed is null, which
// is not a number and not an array.
func followRef(d *reader.Document, o reader.Object) reader.Object {
	r, _ := d.Resolve(o)
	return r
}

// defaultSignatureText is what a signature with neither image nor text says.
func defaultSignatureText(o SignOptions, when time.Time) string {
	who := o.Certificate.Subject.CommonName
	if who == "" {
		who = o.Certificate.Subject.String()
	}
	return "Digitally signed by " + who + "\n" + when.UTC().Format(time.RFC3339)
}

// addStream puts a stream into the update and gives its reference.
func (s *signing) addStream(st *reader.Stream) reader.Ref {
	st.Dict["Length"] = reader.Integer(len(st.Raw))
	ref := s.alloc()
	s.written[ref.Num] = st
	return ref
}

// draw gives the widget its box, its /F and its /AP, and writes the objects the
// appearance is made of into the update, beside the signature.
func (s *signing) draw(widget reader.Dict, v *visible, o SignOptions, when time.Time) {
	text := v.text
	if text == "" && v.pic == nil {
		text = defaultSignatureText(o, when)
	}
	w, h := v.rect[2]-v.rect[0], v.rect[3]-v.rect[1]
	resources := reader.Dict{}
	var body bytes.Buffer
	fmt.Fprintf(&body, "q 0 0 %s %s re W n\n", number(w), number(h))
	if v.pic != nil {
		ref := pictureObjects(v.pic, s.addStream)
		scale := min(w/float64(v.pic.width), h/float64(v.pic.height))
		iw, ih := float64(v.pic.width)*scale, float64(v.pic.height)*scale
		fmt.Fprintf(&body, "q %s 0 0 %s %s %s cm /PdfopsSigIm Do Q\n",
			number(iw), number(ih), number((w-iw)/2), number((h-ih)/2))
		resources["XObject"] = reader.Dict{"PdfopsSigIm": ref}
	}
	if text != "" {
		resources["Font"] = stampResources(map[Font]bool{Helvetica: true}, nil)["Font"]
		pad := min(2, w/4, h/4)
		// The box is within maxCoord of the origin, so h is a small float and
		// the number of lines it holds is a small int.
		tw, th := w-2*pad, h-2*pad
		size := v.size
		if size == 0 {
			size = fitSize(text, tw, th)
		}
		fmt.Fprintf(&body, "%s %s %s rg BT /%s %s Tf\n",
			number(clamp01(v.colour[0])), number(clamp01(v.colour[1])), number(clamp01(v.colour[2])),
			fontResourceName(Helvetica), number(size))
		y := h - pad - size
		for _, line := range freeTextLines(text, Helvetica, size, tw, linesThatFit(th, size)) {
			fmt.Fprintf(&body, "1 0 0 1 %s %s Tm ", number(pad), number(y))
			body.Write(pdfString(encodeWinAnsi(line)))
			body.WriteString(" Tj\n")
			y -= size * lineSpacing
		}
		body.WriteString("ET\n")
	}
	body.WriteString("Q\n")
	form := s.addStream(&reader.Stream{
		Dict: reader.Dict{
			"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
			"BBox":      rectArray(0, 0, w, h),
			"Resources": resources,
		},
		Raw: body.Bytes(),
	})
	widget["Rect"] = rectArray(v.rect[0], v.rect[1], v.rect[2], v.rect[3])
	widget["F"] = reader.Integer(annotPrint)
	widget["AP"] = reader.Dict{"N": form}
}

// fitSize is the largest size, from fitMax down, at which the text wrapped to
// the width fits the height with its longest line inside the width; fitMin
// when none does (the box clips it).
func fitSize(text string, w, h float64) float64 {
	for size := fitMax; size > fitMin; size -= fitStep {
		room := int(h / (size * lineSpacing))
		lines := freeTextLines(text, Helvetica, size, w, room+1)
		if len(lines) > room {
			continue
		}
		widest := 0.0
		for _, l := range lines {
			widest = max(widest, Helvetica.Width(l, size))
		}
		if widest <= w {
			return size
		}
	}
	return fitMin
}
