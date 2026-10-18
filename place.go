// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import "fmt"

// A Placement is a picture drawn at a place on a page: a signature, a logo, a
// stamp that is an image rather than a line of text.
//
// ⛔ Its shape deliberately mirrors [Stamp]'s, because the two answer the same
// question about a page and a reader of one should recognise the other:
// Position and Margin mean exactly what they mean there. What differs is that
// a picture has a size of its own, so it has three ways of being sized and a
// stamp has one.
type Placement struct {
	// Data is the picture, in any format go-gfx/gfx reads — which is more
	// than a PDF can carry, and more than this package needs to care about:
	// a JPEG goes in as it stands and everything else is decoded and
	// recompressed, exactly as [Doc.Picture] does it.
	Data []byte

	// Position says which of the nine places on the page it goes, and Margin
	// how far from the edge, in points; zero means 24, as a stamp's does.
	Position Position
	Margin   float64

	// At, when Anchored is true, is the bottom left corner in the page's own
	// points, and Position is then ignored.
	//
	// ⛔ A separate flag rather than "At is zero means unset": the bottom left
	// corner of a page IS (0,0), and a caller placing a signature in the
	// corner of a scanned form would silently get it centred.
	At       [2]float64
	Anchored bool

	// Width and Height are the size on the page, in points. Giving one of
	// them keeps the picture's own proportions; giving both may distort it,
	// which is the caller's business and not this package's.
	Width, Height float64

	// DPI sizes it from its own pixels instead: 300 means a 300-pixel-wide
	// picture is an inch across. Zero means 72, one point per pixel, which is
	// what [Doc.Picture] means by it too.
	//
	// It is ignored when Width or Height is given.
	DPI float64

	// Opacity means exactly what [Stamp.Opacity] means: the alpha the picture
	// is drawn with, where zero is the unset default and therefore fully
	// opaque, and 0.25 is pale.
	//
	// ⛔ The first version of this field was the other way round — "how much
	// of the page shows through" — which would have put TWO meanings of one
	// word in one package: a watermark at 0.25 is pale and a placement at
	// 0.25 would have been nearly solid. Nothing would have failed; the
	// picture would simply have come out wrong for everyone who read the
	// other field first.
	Opacity float64
}

// Place draws a picture on the pages a range names.
//
// ⛔ The drawing itself was already here and reachable by nothing: the
// machinery that puts an image at a rectangle on a page (placedPicture,
// placedContent) was written for the XFA renderer and only XFA fed it, so a
// capability the package HAD could not be asked for. A borrowed page did not
// even honour it — the conditions that decide whether a source page is wrapped
// or cached counted marks and not pictures — which is the shape a capability
// takes when it exists and is not wired: present, tested through one caller,
// and absent from the outside.
//
// What it serves, beyond a logo: a drawn or typed signature placed on a page,
// which is what every "sign a PDF" tool means by signing — a picture, not
// cryptography. The cryptographic kind is a different job with a different
// name, and conflating the two is how a drawn squiggle gets called a digital
// signature.
func (d *Doc) Place(spec string, p Placement) error {
	nums, err := ParseRange(spec, len(d.pages))
	if err != nil {
		return err
	}
	if len(p.Data) == 0 {
		return fmt.Errorf("ops: a placement with no picture draws nothing")
	}
	if p.Width < 0 || p.Height < 0 {
		return fmt.Errorf("ops: a placement %g by %g points", p.Width, p.Height)
	}
	// ⛔ One is allowed and means opaque, as it does for a stamp: the command
	// line defaults that flag to 1, so refusing it here would refuse the
	// commonest call there is.
	if p.Opacity < 0 || p.Opacity > 1 {
		return fmt.Errorf("ops: an opacity of %g, which is outside zero to one", p.Opacity)
	}
	pic, err := readPicture(p.Data)
	if err != nil {
		return err
	}
	w, h := p.sizeOf(pic)
	if w <= 0 || h <= 0 {
		return fmt.Errorf("ops: a picture %g by %g points, which draws nothing", w, h)
	}
	for _, n := range nums {
		page := &d.pages[n-1]
		area := d.pageArea(*page)
		x, y := p.cornerIn(area, w, h)
		page.pictures = append(page.pictures, placedPicture{
			pic:     pic,
			rect:    [4]float64{x, y, w, h},
			opacity: p.Opacity,
		})
	}
	return nil
}

// sizeOf works out how big the picture goes on the page, in points.
//
// ⛔ Giving ONE of width and height keeps the proportions. A signature scaled
// to a box is a signature nobody recognises, and the commonest way to ask for
// one is to say how wide it should be and let the height follow.
func (p Placement) sizeOf(pic *picture) (w, h float64) {
	pw, ph := float64(pic.width), float64(pic.height)
	switch {
	case p.Width > 0 && p.Height > 0:
		return p.Width, p.Height
	case p.Width > 0:
		return p.Width, p.Width * ph / pw
	case p.Height > 0:
		return p.Height * pw / ph, p.Height
	}
	dpi := p.DPI
	if dpi <= 0 {
		dpi = 72
	}
	scale := 72 / dpi
	return pw * scale, ph * scale
}

// cornerIn is where the bottom left of the picture goes.
func (p Placement) cornerIn(area [4]float64, w, h float64) (x, y float64) {
	if p.Anchored {
		// ⛔ Taken as given, and not clamped into the page. A caller who says
		// where may be placing a signature that deliberately bleeds off the
		// edge of a scanned form, and a viewer clips what falls outside the
		// media box perfectly well. The nine positions clamp; an explicit
		// corner is an instruction.
		return area[0] + p.At[0], area[1] + p.At[1]
	}
	margin := p.Margin
	if margin == 0 {
		margin = 24
	}
	return place(p.Position, area, w, h, margin)
}

// pageArea is the box a placement is positioned within: the same one a stamp
// uses, so "bottom right" means the same thing for a picture and for a line of
// text on the same page.
func (d *Doc) pageArea(p Page) [4]float64 {
	if p.src == nil {
		return [4]float64{0, 0, p.size[0], p.size[1]}
	}
	return d.sourceBox(p)
}
