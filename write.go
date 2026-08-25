package ops

import (
	"fmt"

	"github.com/go-pdfkit/reader"
)

// Bytes writes the document out as a PDF file. Every borrowed page is copied
// out of the file it came from with the attributes it inherited written onto
// it, so pages from different documents keep their own geometry and resources.
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

// writePage writes one page: copied out of its source file, built out of other
// pages, or empty.
func (d *Doc) writePage(w *reader.Writer, p Page, parent reader.Ref) reader.Ref {
	if p.blank || p.tiles != nil {
		return d.writeMadePage(w, p, parent)
	}
	return d.writeBorrowedPage(w, p, parent)
}

// writeMadePage writes a page this package built rather than borrowed.
func (d *Doc) writeMadePage(w *reader.Writer, p Page, parent reader.Ref) reader.Ref {
	area := [4]float64{0, 0, p.size[0], p.size[1]}
	page := reader.Dict{
		"Type":     reader.Name("Page"),
		"Parent":   parent,
		"MediaBox": boxArray(area),
	}
	if p.rotate != 0 {
		page["Rotate"] = reader.Integer(p.rotate)
	}
	content, resources := d.madeContent(w, p, area)
	if len(content) > 0 {
		page["Contents"] = w.Add(contentStream(content, nil))
		page["Resources"] = resources
	}
	if p.crop != nil {
		page["CropBox"] = box(p.crop)
	}
	return w.Add(page)
}

// writeBorrowedPage copies a page out of the file it came from.
func (d *Doc) writeBorrowedPage(w *reader.Writer, p Page, parent reader.Ref) reader.Ref {
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
	if len(p.marks) > 0 {
		d.stampBorrowedPage(w, p, src, copied)
	}
	return w.Add(copied)
}

// stampBorrowedPage adds a page's text to a copy of it. The page's own content
// is left exactly as it was, wrapped in a save and a restore so that whatever
// state it leaves behind cannot reach the text, and the text is drawn inside
// the page's own box — so on a page carrying a rotation the text turns with
// the page, which is what the page's own layout does too.
func (d *Doc) stampBorrowedPage(w *reader.Writer, p Page, src, copied reader.Dict) {
	stamp, fonts, alpha := d.stampContent(p, d.sourceBox(p))
	before := w.Add(contentStream([]byte("q\n"), nil))
	after := w.Add(contentStream(append([]byte("Q\n"), stamp...), nil))
	list := append(reader.Array{before}, contentParts(w, p.src, src)...)
	copied["Contents"] = append(list, after)

	// The resources are rebuilt as a dictionary of this file's own, because
	// the page may be sharing them with pages that are not being stamped.
	direct := reader.Dict{}
	if res, ok := p.src.GetDict(src, "Resources"); ok {
		for k, v := range res {
			direct[k] = w.Copy(p.src, v)
		}
	}
	copied["Resources"] = mergeResources(direct, stampResources(fonts, alpha))
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

// contentParts returns a page's content streams as references in the file
// being written, whatever shape the source kept them in — a stream, an array
// of streams, or a reference to such an array, which producers do write.
func contentParts(w *reader.Writer, src *reader.Document, page reader.Dict) reader.Array {
	entry := page.Get("Contents")
	resolved, _ := src.Resolve(entry)
	if arr, ok := reader.ToArray(resolved); ok {
		out := make(reader.Array, 0, len(arr))
		for _, e := range arr {
			out = append(out, w.Copy(src, e))
		}
		return out
	}
	if _, ok := reader.ToStream(resolved); ok {
		return reader.Array{w.Copy(src, entry)}
	}
	return nil
}
