package ops

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"math"

	"github.com/go-pdfkit/reader"
)

// A tile places one page inside another. The matrix maps the tile's own
// coordinates — a box with its corner at the origin — into the page.
type tile struct {
	from   Page
	matrix [6]float64
}

// Blank adds an empty page of the given size at the end.
func (d *Doc) Blank(width, height float64) {
	d.pages = append(d.pages, Page{blank: true, size: [2]float64{width, height}})
}

// InsertBlank puts an empty page before the page at i, counting from one; an
// index one past the end appends. The size is taken from the page it precedes,
// or from the one before it at the end of the document.
func (d *Doc) InsertBlank(i int) error {
	if i < 1 || i > len(d.pages)+1 {
		return fmt.Errorf("ops: cannot insert before page %d of %d", i, len(d.pages))
	}
	size := [2]float64{595.276, 841.89}
	switch {
	case i <= len(d.pages):
		size = d.effectiveSize(d.pages[i-1])
	case len(d.pages) > 0:
		size = d.effectiveSize(d.pages[len(d.pages)-1])
	}
	blank := Page{blank: true, size: size}
	d.pages = append(d.pages[:i-1], append([]Page{blank}, d.pages[i-1:]...)...)
	return nil
}

// NUp lays n pages on each sheet, in reading order. The sheet keeps the size
// of the first page, and the grid is whichever arrangement of at least n cells
// gives cells closest in shape to the pages going into them.
func (d *Doc) NUp(n int) error {
	if n < 1 {
		return fmt.Errorf("ops: %d pages to a sheet makes no sense", n)
	}
	if len(d.pages) == 0 {
		return fmt.Errorf("ops: an empty document has nothing to lay out")
	}
	if n == 1 {
		return nil
	}
	sheet := d.effectiveSize(d.pages[0])
	cols, rows := bestGrid(n, sheet[0]/sheet[1], sheet[0]/sheet[1])
	var out []Page
	for i := 0; i < len(d.pages); i += n {
		end := i + n
		if end > len(d.pages) {
			end = len(d.pages)
		}
		out = append(out, d.layOut(d.pages[i:end], sheet, cols, rows))
	}
	d.pages = out
	return nil
}

// Booklet reorders the pages for saddle-stitch printing — folded in half and
// stapled through the spine — and lays them two to a sheet. Blank pages are
// added so the count is a multiple of four, which is what folding needs.
func (d *Doc) Booklet() error {
	if len(d.pages) == 0 {
		return fmt.Errorf("ops: an empty document has nothing to fold")
	}
	size := d.effectiveSize(d.pages[0])
	order := bookletOrder(len(d.pages))
	ordered := make([]Page, 0, len(order))
	for _, n := range order {
		if n > len(d.pages) {
			ordered = append(ordered, Page{blank: true, size: size})
			continue
		}
		ordered = append(ordered, d.pages[n-1])
	}
	// A booklet sheet is two pages side by side, whatever shape they are:
	sheet := [2]float64{size[0] * 2, size[1]}
	var out []Page
	for i := 0; i+1 < len(ordered); i += 2 {
		out = append(out, d.layOut(ordered[i:i+2], sheet, 2, 1))
	}
	d.pages = out
	return nil
}

// bookletOrder returns the page numbers in the order a folded booklet wants
// them, counting from one, with numbers past the end standing for blanks.
func bookletOrder(n int) []int {
	m := (n + 3) / 4 * 4
	out := make([]int, 0, m)
	for k := 0; k < m/4; k++ {
		out = append(out, m-2*k, 2*k+1, 2*k+2, m-2*k-1)
	}
	return out
}

// bestGrid picks the arrangement of at least n cells whose cells come closest
// in shape to the pages that will go in them.
func bestGrid(n int, sheetAspect, pageAspect float64) (cols, rows int) {
	best := math.Inf(1)
	cols, rows = n, 1
	for c := 1; c <= n; c++ {
		r := (n + c - 1) / c
		cell := sheetAspect * float64(r) / float64(c)
		score := math.Abs(math.Log(cell / pageAspect))
		// A grid with cells to spare is worse than a tight one of equal shape.
		score += 0.01 * float64(c*r-n)
		if score < best {
			best, cols, rows = score, c, r
		}
	}
	return cols, rows
}

// layOut places pages into the cells of a grid on one sheet.
func (d *Doc) layOut(pages []Page, sheet [2]float64, cols, rows int) Page {
	out := Page{size: sheet}
	cw, ch := sheet[0]/float64(cols), sheet[1]/float64(rows)
	for i, p := range pages {
		col, row := i%cols, i/cols
		size := d.effectiveSize(p)
		scale := math.Min(cw/size[0], ch/size[1])
		x := float64(col)*cw + (cw-size[0]*scale)/2
		// Rows run down the sheet, so the first row is at the top.
		y := sheet[1] - float64(row+1)*ch + (ch-size[1]*scale)/2
		out.tiles = append(out.tiles, tile{from: p, matrix: [6]float64{scale, 0, 0, scale, x, y}})
	}
	return out
}

// Overlay draws each page of another document on top of the pages here, in
// order. When the other document has fewer pages its last page is not
// repeated: pages past its end are left alone.
func (d *Doc) Overlay(other *Doc) error { return d.combine(other, true) }

// Underlay draws each page of another document underneath the pages here.
func (d *Doc) Underlay(other *Doc) error { return d.combine(other, false) }

// combine puts one document's pages over or under this one's.
func (d *Doc) combine(other *Doc, onTop bool) error {
	if len(other.pages) == 0 {
		return fmt.Errorf("ops: the document to draw has no pages")
	}
	for i := range d.pages {
		if i >= len(other.pages) {
			break
		}
		base, mark := d.pages[i], other.pages[i]
		size := d.effectiveSize(base)
		markSize := d.effectiveSize(mark)
		scale := math.Min(size[0]/markSize[0], size[1]/markSize[1])
		placed := tile{from: mark, matrix: [6]float64{scale, 0, 0, scale,
			(size[0] - markSize[0]*scale) / 2, (size[1] - markSize[1]*scale) / 2}}
		whole := tile{from: base, matrix: [6]float64{1, 0, 0, 1, 0, 0}}
		page := Page{size: size}
		if onTop {
			page.tiles = []tile{whole, placed}
		} else {
			page.tiles = []tile{placed, whole}
		}
		d.pages[i] = page
	}
	return nil
}

// effectiveSize reports how wide and tall a page is once its rotation has been
// taken into account.
func (d *Doc) effectiveSize(p Page) [2]float64 {
	if p.blank || p.tiles != nil {
		if p.rotate == 90 || p.rotate == 270 {
			return [2]float64{p.size[1], p.size[0]}
		}
		return p.size
	}
	b := d.sourceBox(p)
	w, h := b[2]-b[0], b[3]-b[1]
	if p.rotate == 90 || p.rotate == 270 {
		return [2]float64{h, w}
	}
	return [2]float64{w, h}
}

// sourceBox reports the box a borrowed page occupies: its crop box where it
// has one, its media box otherwise, and A4 when it has neither.
func (d *Doc) sourceBox(p Page) [4]float64 {
	if p.crop != nil {
		return [4]float64{p.crop[0], p.crop[1], p.crop[2], p.crop[3]}
	}
	if p.media != nil {
		return [4]float64{p.media[0], p.media[1], p.media[2], p.media[3]}
	}
	page, _ := p.src.Page(p.number)
	for _, key := range []reader.Name{"CropBox", "MediaBox"} {
		if b, ok := rectangle(p.src, page.Get(key)); ok {
			return b
		}
	}
	return [4]float64{0, 0, 595.276, 841.89}
}

// rectangle reads a PDF rectangle, normalised so the first corner is the lower
// left one — files do write them the other way round.
func rectangle(src *reader.Document, o reader.Object) ([4]float64, bool) {
	var out [4]float64
	resolved := resolve(src, o)
	arr, ok := reader.ToArray(resolved)
	if !ok || len(arr) < 4 {
		return out, false
	}
	for i := 0; i < 4; i++ {
		e := resolve(src, arr[i])
		v, ok := reader.ToFloat(e)
		if !ok {
			return out, false
		}
		out[i] = v
	}
	if out[0] > out[2] {
		out[0], out[2] = out[2], out[0]
	}
	if out[1] > out[3] {
		out[1], out[3] = out[3], out[1]
	}
	if out[0] == out[2] || out[1] == out[3] {
		return out, false
	}
	return out, true
}

// composeContent builds the content stream that draws a composed page's tiles.
func (d *Doc) composeContent(w *reader.Writer, p Page) ([]byte, reader.Dict) {
	xobjects := reader.Dict{}
	var content bytes.Buffer
	for i, t := range p.tiles {
		name := reader.Name(fmt.Sprintf("Tile%d", i))
		xobjects[name] = d.asForm(w, t.from)
		content.WriteString("q ")
		writeMatrix(&content, t.matrix)
		fmt.Fprintf(&content, " cm /%s Do Q\n", name)
	}
	return content.Bytes(), reader.Dict{"XObject": xobjects}
}

// writeMatrix renders six numbers the way a content stream spells a matrix.
func writeMatrix(w *bytes.Buffer, m [6]float64) {
	for i, v := range m {
		if i > 0 {
			w.WriteByte(' ')
		}
		w.Write(reader.FormatObject(reader.Real(v)))
	}
}

// asForm turns any page into a form that can be drawn inside another, with
// its own coordinates running from the origin to its effective size, so
// whatever places it need only scale and translate. A page's rotation is
// baked into the form's matrix here, exactly as a viewer would apply it.
func (d *Doc) asForm(w *reader.Writer, p Page) reader.Ref {
	if len(p.marks) > 0 && !p.blank && p.tiles == nil {
		inner := p
		inner.marks = nil
		size := d.effectiveSize(p)
		return d.asForm(w, Page{
			size:  size,
			marks: p.marks,
			tiles: []tile{{from: inner, matrix: [6]float64{1, 0, 0, 1, 0, 0}}},
		})
	}
	var content []byte
	extra := reader.Dict{
		"Type":    reader.Name("XObject"),
		"Subtype": reader.Name("Form"),
	}
	box := [4]float64{0, 0, p.size[0], p.size[1]}
	switch {
	case p.blank:
	case p.tiles != nil || len(p.marks) > 0:
		var res reader.Dict
		content, res = d.madeContent(w, p, [4]float64{0, 0, p.size[0], p.size[1]})
		extra["Resources"] = res
	default:
		content, _ = p.src.PageContent(p.number)
		page, _ := p.src.Page(p.number)
		extra["Resources"] = w.Copy(p.src, page.Get("Resources"))
		box = d.sourceBox(p)
	}
	extra["BBox"] = boxArray(box)
	extra["Matrix"] = matrixArray(rotationMatrix(p.rotate, box))
	return w.Add(contentStream(content, extra))
}

// rotationMatrix maps a page's own box onto a box of its effective size with a
// corner at the origin, turning it by the page's rotation on the way.
func rotationMatrix(rotate int, b [4]float64) [6]float64 {
	switch rotate {
	case 90:
		return [6]float64{0, -1, 1, 0, -b[1], b[2]}
	case 180:
		return [6]float64{-1, 0, 0, -1, b[2], b[3]}
	case 270:
		return [6]float64{0, 1, -1, 0, b[3], -b[0]}
	}
	return [6]float64{1, 0, 0, 1, -b[0], -b[1]}
}

// matrixArray renders six numbers as a PDF array.
func matrixArray(m [6]float64) reader.Array {
	out := make(reader.Array, 6)
	for i, v := range m {
		out[i] = reader.Real(v)
	}
	return out
}

// boxArray renders a rectangle as a PDF array.
func boxArray(b [4]float64) reader.Array {
	return reader.Array{reader.Real(b[0]), reader.Real(b[1]), reader.Real(b[2]), reader.Real(b[3])}
}

// contentStream makes a stream out of content this package generated,
// compressed, since it was decoded to get here and would otherwise go back out
// several times its size.
func contentStream(data []byte, extra reader.Dict) *reader.Stream {
	d := reader.Dict{}
	for k, v := range extra {
		d[k] = v
	}
	if len(data) == 0 {
		return &reader.Stream{Dict: d}
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	// A bytes.Buffer never fails to take bytes, and Close only flushes.
	zw.Write(data)
	zw.Close()
	d["Filter"] = reader.Name("FlateDecode")
	return &reader.Stream{Dict: d, Raw: buf.Bytes()}
}

// madeContent builds the content stream and resources of a page this package
// made: the tiles it draws, then the text stamped on top of them.
func (d *Doc) madeContent(w *reader.Writer, p Page, area [4]float64) ([]byte, reader.Dict) {
	var content []byte
	resources := reader.Dict{}
	if p.tiles != nil {
		content, resources = d.composeContent(w, p)
	}
	if len(p.marks) > 0 {
		stamp, fonts, alpha := d.stampContent(p, area)
		content = append(content, stamp...)
		resources = mergeResources(resources, stampResources(fonts, alpha))
	}
	return content, resources
}
