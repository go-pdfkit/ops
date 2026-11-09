package ops

import (
	"fmt"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/reader"
)

// ISO 32000-1, 12.5.6.23, on what a redaction must do to a picture:
//
//	If a portion of an image is contained in a redaction region, that portion
//	of the image data shall be destroyed; clipping or image masks shall not be
//	used to hide that data.
//
// So the samples under the region are overwritten and the picture is written
// back. What this release can do that to is a picture whose samples are plain —
// one the reader has already unfiltered — eight bits a component, in
// DeviceGray or DeviceRGB, drawn upright and drawn ONCE on the page. Everything
// else is refused by its own name, because a redaction that quietly did less
// than it was asked is worse than one that says it cannot.

// blankedTo is what a destroyed sample holds. Black rather than white: the
// region is painted over afterwards, and a sample left white would be
// indistinguishable from paper if the paint were ever removed.
const blankedTo = 0

// A plannedImage is one picture of a page with the samples under a redaction
// destroyed, ready to be written in place of the original.
type plannedImage struct {
	// name is what the page's resources call it.
	name reader.Name
	// dict is the picture's own dictionary, which says how to read the data.
	dict reader.Dict
	// data is its samples, with the region blanked.
	data []byte
}

// imageComponents is how many samples a pixel takes in the colour spaces this
// can blank, by the name the picture gives.
//
// ⛔ BY NAME, and nothing else. An /Indexed space's samples are indices into a
// palette, where "black" is whatever the palette says and overwriting with
// zeroes writes the palette's FIRST colour — which may be white. An /ICCBased
// one is a stream whose component count has to be read out of it. Both are
// refusals until they are implemented and tested, rather than a guess that
// destroys the wrong pixels.
var imageComponents = map[reader.Name]int{
	"DeviceGray": 1,
	"DeviceRGB":  3,
}

// plannedImages is what has to happen to the pictures of a page for the
// redactions it carries, or the reason it cannot happen.
//
// ⛔ ONE FUNCTION FOR THE CHECK AND FOR THE WRITE, as plannedEdit is for a text
// replacement. checkRedactable calls it to refuse at [Doc.Redact] — where a
// caller can still choose another region or another tool — and the writer calls
// it to get the bytes. Two functions would be two chances for the check to pass
// something the write cannot do.
func (d *Doc) plannedImages(p *Page, rs []Redaction) ([]plannedImage, error) {
	if len(rs) == 0 {
		return nil, nil
	}
	images, err := extractImages(p.src, p.number)
	if err != nil {
		return nil, fmt.Errorf("ops: the images of page %d could not be read: %w", p.number, err)
	}
	var out []plannedImage
	for _, im := range images {
		var touched []Quad
		for _, r := range rs {
			// ⛔ DrawnWidth and DrawnHeight, not Width and Height: those two are
			// the picture's own SAMPLES. Comparing a sample count with a region
			// in points would weigh a 2048-pixel thumbnail against a 72-point
			// box and refuse every page that carried a picture at all.
			if overlaps(im.X, im.Y, im.X+im.DrawnWidth, im.Y+im.DrawnHeight, r.Quads) {
				touched = append(touched, r.Quads...)
			}
		}
		if len(touched) == 0 {
			continue
		}
		planned, err := blankedImage(p.number, im, images, touched)
		if err != nil {
			return nil, err
		}
		out = append(out, planned)
	}
	return out, nil
}

// blankedImage is one picture with the samples under those quads destroyed, or
// the reason this release cannot destroy them.
func blankedImage(page int, im extract.Image, all []extract.Image, quads []Quad) (plannedImage, error) {
	no := func(why string, args ...any) (plannedImage, error) {
		return plannedImage{}, fmt.Errorf("ops: page %d places an image across the region and "+
			"a redaction must destroy its pixels rather than cover them, which this cannot do "+
			"here: %s", page, fmt.Sprintf(why, args...))
	}
	if im.Inline {
		return no("the picture is written into the content stream rather than held as a resource")
	}
	// ⛔ No guard for a picture with no name: extract names one from the
	// resources entry it was drawn through, so the only nameless pictures are
	// the inline ones the line above has already refused. A branch for it would
	// be one no test could reach.
	// ⛔ DRAWN ONCE. Replacing the picture replaces it for every `Do` that names
	// it, so blanking it for one placement blanks it for the others — in a
	// different place on the page each time. That is removing what nobody asked
	// about, which is its own betrayal.
	drawn := 0
	for _, other := range all {
		if other.Name == im.Name && !other.Inline {
			drawn++
		}
	}
	if drawn > 1 {
		return no("the page draws %q %d times, and destroying its pixels for one of them "+
			"would destroy them for the others", im.Name, drawn)
	}
	// ⛔ UPRIGHT. b and c are zero for a picture the page did not turn or skew,
	// and stay zero for a flipped one. The box a turned picture covers looks
	// exactly like the box an upright one covers, so mapping the region into its
	// samples by that box would destroy more than was asked for.
	if im.Matrix[1] != 0 || im.Matrix[2] != 0 {
		return no("the page draws it turned or skewed, and the region cannot be mapped into " +
			"its samples by the box it covers without destroying more than was asked for")
	}
	// ⛔ And none for a picture drawn FLAT, with a zero scale: the box it covers
	// then has no width or no height, and a box with no width overlaps nothing —
	// so such a picture never reaches this function at all. The inverse below
	// would divide by that zero; it cannot be asked to.
	if im.Filter != "" {
		return no("its samples are still %s, and re-encoding them is not in this release", im.Filter)
	}
	if bools(im.Dict, "ImageMask") {
		return no("it is an image mask, whose samples are a stencil rather than a picture")
	}
	// ⛔ PRESENT IN THE DICTIONARY, not "Get answers something": Get gives a
	// null for a key that is not there, and a null is not a nil interface — so
	// testing the value refused every picture ever written, each of them for
	// carrying a soft mask it did not have. The map says what the file holds.
	for _, key := range []reader.Name{"SMask", "Mask", "Decode"} {
		if _, ok := im.Dict[key]; ok {
			return no("it carries /%s, which changes what its samples mean", key)
		}
	}
	if bits, _ := reader.ToInt(im.Dict.Get("BitsPerComponent")); bits != 8 {
		return no("its samples are %d bits each, and this blanks whole bytes", bits)
	}
	space, _ := im.Dict.Get("ColorSpace").(reader.Name)
	comps, ok := imageComponents[space]
	if !ok {
		return no("its colour space is %s, and what a destroyed sample holds there is not "+
			"something to guess at", describeSpace(im.Dict.Get("ColorSpace")))
	}
	if im.Width <= 0 || im.Height <= 0 {
		return no("it says it is %d by %d samples", im.Width, im.Height)
	}
	if want := im.Width * im.Height * comps; len(im.Data) != want {
		return no("it holds %d bytes where %d by %d in %s is %d: the samples are not all there",
			len(im.Data), im.Width, im.Height, space, want)
	}

	data := append([]byte(nil), im.Data...)
	for _, q := range quads {
		blankSamples(data, im, comps, q)
	}
	return plannedImage{name: reader.Name(im.Name), dict: im.Dict, data: data}, nil
}

// blankSamples destroys the samples of one quad's box, mapped from the page
// into the picture's own grid.
//
// ⛔ THE GRID COUNTS DOWN AND THE PAGE COUNTS UP. A picture occupies the unit
// square of the transform in force, with v running from its BOTTOM edge to its
// top, while sample row zero is its TOP row. Reading one for the other blanks
// the mirror image of what was asked for, which looks like a redaction until
// somebody turns the page over.
func blankSamples(data []byte, im extract.Image, comps int, q Quad) {
	x0, y0, x1, y1 := q.bounds()
	// The inverse of an upright transform, component by component: a flipped
	// picture has a negative scale, so the ends come back swapped and are put
	// back in order here.
	u0, u1 := (x0-im.Matrix[4])/im.Matrix[0], (x1-im.Matrix[4])/im.Matrix[0]
	v0, v1 := (y0-im.Matrix[5])/im.Matrix[3], (y1-im.Matrix[5])/im.Matrix[3]
	u0, u1 = min(u0, u1), max(u0, u1)
	v0, v1 = min(v0, v1), max(v0, v1)

	col0 := clampTo(int(float64(im.Width)*u0), im.Width)
	col1 := clampTo(ceilTo(float64(im.Width)*u1), im.Width)
	row0 := clampTo(int(float64(im.Height)*(1-v1)), im.Height)
	row1 := clampTo(ceilTo(float64(im.Height)*(1-v0)), im.Height)

	for row := row0; row < row1; row++ {
		at := (row*im.Width + col0) * comps
		for i := 0; i < (col1-col0)*comps; i++ {
			data[at+i] = blankedTo
		}
	}
}

// ceilTo is the next whole sample up, so that a region ending a third of the
// way into a sample still destroys that sample.
func ceilTo(f float64) int {
	n := int(f)
	if float64(n) < f {
		n++
	}
	return n
}

// clampTo keeps a sample index inside the picture.
func clampTo(n, size int) int {
	if n < 0 {
		return 0
	}
	if n > size {
		return size
	}
	return n
}

// bools reads a boolean entry of a dictionary, false when it is absent or is
// something else.
func bools(d reader.Dict, key reader.Name) bool {
	b, ok := d.Get(key).(reader.Bool)
	return ok && bool(b)
}

// describeSpace names a colour space for a refusal, whatever shape it is in.
func describeSpace(o reader.Object) string {
	switch v := o.(type) {
	case reader.Name:
		return "/" + string(v)
	case reader.Array:
		if len(v) > 0 {
			if first, ok := v[0].(reader.Name); ok {
				return "/" + string(first) + ", which is a family rather than a plain space"
			}
		}
		return "an array"
	default:
		return o.Kind().String()
	}
}
