package ops

import (
	"fmt"

	"github.com/go-pdfkit/reader"
)

var inheritable = []reader.Name{"Resources", "MediaBox", "CropBox"}

// Bytes writes the document out as a PDF file. Every page is copied out of the
// file it came from with the attributes it inherited written onto it, so pages
// from different documents keep their own geometry and resources.
func (d *Doc) Bytes() ([]byte, error) {
	if len(d.pages) == 0 {
		return nil, fmt.Errorf("ops: a document with no pages cannot be written")
	}
	w := reader.NewWriter(d.version)
	pagesRef := w.Reserve()
	kids := make(reader.Array, 0, len(d.pages))
	for _, p := range d.pages {
		kids = append(kids, d.writePage(w, p, pagesRef))
	}
	w.Put(pagesRef, reader.Dict{
		"Type":  reader.Name("Pages"),
		"Kids":  kids,
		"Count": reader.Integer(len(kids)),
	})
	trailer := reader.Dict{"Root": w.Add(reader.Dict{
		"Type":  reader.Name("Catalog"),
		"Pages": pagesRef,
	})}
	if len(d.info) > 0 {
		trailer["Info"] = w.Add(d.info)
	}
	return w.Finish(trailer)
}

// writePage copies one page into the file being written.
func (d *Doc) writePage(w *reader.Writer, p Page, parent reader.Ref) reader.Ref {
	src, _ := p.src.Page(p.number)
	// /Parent is dropped: it would drag the page's original tree — and through
	// it every other page of that file — into this one. /Rotate is dropped
	// because this document, not the source, decides which way up a page goes.
	copied := reader.Dict{}
	for k, v := range src {
		if k == "Parent" || k == "Rotate" {
			continue
		}
		copied[k] = w.Copy(p.src, v)
	}
	copied["Type"] = reader.Name("Page")
	copied["Parent"] = parent
	if p.rotate != 0 {
		copied["Rotate"] = reader.Integer(p.rotate)
	}
	if p.media != nil {
		copied["MediaBox"] = box(p.media)
	}
	if p.crop != nil {
		copied["CropBox"] = box(p.crop)
	}
	ensureGeometry(copied)
	return w.Add(copied)
}

// ensureGeometry gives a page a media box when neither it nor its ancestors
// had one. A page with no paper size is not a page any viewer can show, and A4
// is the least surprising thing to assume.
func ensureGeometry(page reader.Dict) {
	if page.Get("MediaBox").Kind() != reader.KindNull {
		return
	}
	page["MediaBox"] = box([]float64{0, 0, 595.276, 841.89})
}

// box renders four numbers as a PDF rectangle.
func box(v []float64) reader.Array {
	return reader.Array{reader.Real(v[0]), reader.Real(v[1]), reader.Real(v[2]), reader.Real(v[3])}
}
