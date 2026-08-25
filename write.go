package ops

import (
	"fmt"

	"github.com/go-pdfkit/reader"
)

// A destination map says where each source page ended up in the file being
// written, so that a link or a bookmark pointing at it can be pointed at the
// right place instead of at nothing — or, worse, at the source file's own page
// tree, which would drag every page of it along.
type destinations map[pageKey]reader.Ref

// A pageKey names one page of one source document.
type pageKey struct {
	src *reader.Document
	num int
}

// Bytes writes the document out as a PDF file. Every borrowed page is copied
// out of the file it came from with the attributes it inherited written onto
// it, so pages from different documents keep their own geometry and resources.
func (d *Doc) Bytes() ([]byte, error) {
	if len(d.pages) == 0 {
		return nil, fmt.Errorf("ops: a document with no pages cannot be written")
	}
	w := reader.NewWriter(d.version)
	if d.packed {
		w = reader.NewPackedWriter(d.version)
	}
	if d.protect != nil {
		// Before anything is written: a file cannot be protected after the
		// fact, because the key is what everything in it is written through.
		w.Encrypt(*d.protect)
	}
	pagesRef := w.Reserve()

	// Pages are numbered first and written last: what goes on one of them may
	// need to name another, and a link cannot be written before its target has
	// a number.
	refs := make([]reader.Ref, len(d.pages))
	dicts := make([]reader.Dict, len(d.pages))
	where := destinations{}
	for i, p := range d.pages {
		refs[i] = w.Reserve()
		if p.src != nil {
			if _, ok := where[pageKey{p.src, p.number}]; !ok {
				where[pageKey{p.src, p.number}] = refs[i]
			}
		}
	}
	for i, p := range d.pages {
		dicts[i] = d.buildPage(w, p, pagesRef, where)
	}
	kids := make(reader.Array, 0, len(d.pages))
	for i := range d.pages {
		w.Put(refs[i], dicts[i])
		kids = append(kids, refs[i])
	}
	w.Put(pagesRef, reader.Dict{
		"Type":  reader.Name("Pages"),
		"Kids":  kids,
		"Count": reader.Integer(len(kids)),
	})

	catalog := reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef}
	if outlines := d.writeOutlines(w, where); outlines != nil {
		catalog["Outlines"] = outlines
	}
	trailer := reader.Dict{"Root": w.Add(catalog)}
	if len(d.info) > 0 {
		trailer["Info"] = w.Add(d.info)
	}
	return w.Finish(trailer)
}

// buildPage assembles one page's dictionary without writing it.
func (d *Doc) buildPage(w *reader.Writer, p Page, parent reader.Ref, where destinations) reader.Dict {
	if p.blank || p.tiles != nil {
		return d.buildMadePage(w, p, parent)
	}
	return d.buildBorrowedPage(w, p, parent, where)
}

// buildMadePage assembles a page this package built rather than borrowed.
func (d *Doc) buildMadePage(w *reader.Writer, p Page, parent reader.Ref) reader.Dict {
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
	return page
}

// pageKeys lists the entries of a source page that this package rebuilds
// rather than copies, or drops outright.
var rebuiltPageKeys = map[reader.Name]bool{
	"Parent": true, // the new tree decides who the parent is
	"Rotate": true, // this document decides which way up a page goes
	"Annots": true, // rebuilt so that links can be remapped
	"AA":     true, // a page's own actions run without anyone asking
}

// sanitisedPageKeys are the entries of a page that only a sanitised file
// leaves behind. /AF names files travelling with the page — the PDF 2.0 way
// of attaching one — and is how an embedded file survives a catalogue this
// package rebuilt from nothing.
var sanitisedPageKeys = map[reader.Name]bool{"AF": true}

// buildBorrowedPage copies a page out of the file it came from.
func (d *Doc) buildBorrowedPage(w *reader.Writer, p Page, parent reader.Ref, where destinations) reader.Dict {
	src, _ := p.src.Page(p.number)
	copied := reader.Dict{}
	for k, v := range src {
		if rebuiltPageKeys[k] || (d.sanitize && sanitisedPageKeys[k]) {
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

	extra, resources := d.borrowedExtras(w, p, src)
	if len(extra) > 0 {
		d.decorate(w, p, src, copied, extra, resources)
	}
	if annots := d.copyAnnots(w, p, src, where); len(annots) > 0 {
		copied["Annots"] = annots
	}
	return copied
}

// borrowedExtras builds whatever this package draws on top of a borrowed
// page: the annotations it was asked to flatten, then the text stamped on it.
func (d *Doc) borrowedExtras(w *reader.Writer, p Page, src reader.Dict) ([]byte, reader.Dict) {
	var content []byte
	resources := reader.Dict{}
	if d.flatten {
		flat, res := d.flattenAnnots(w, p, src)
		content = append(content, flat...)
		resources = mergeResources(resources, res)
	}
	if len(p.marks) > 0 {
		stamp, fonts, alpha := d.stampContent(p, d.sourceBox(p))
		content = append(content, stamp...)
		resources = mergeResources(resources, stampResources(fonts, alpha))
	}
	return content, resources
}

// decorate adds generated content to a copy of a borrowed page. The page's own
// content is left exactly as it was, wrapped in a save and a restore so that
// whatever state it leaves behind cannot reach what comes after it.
func (d *Doc) decorate(w *reader.Writer, p Page, src, copied reader.Dict, extra []byte, resources reader.Dict) {
	before := w.Add(contentStream([]byte("q\n"), nil))
	after := w.Add(contentStream(append([]byte("Q\n"), extra...), nil))
	list := append(reader.Array{before}, contentParts(w, p.src, src)...)
	copied["Contents"] = append(list, after)

	// The resources are rebuilt as a dictionary of this file's own, because
	// the page may be sharing them with pages that are not being decorated.
	direct := reader.Dict{}
	if res, ok := p.src.GetDict(src, "Resources"); ok {
		for k, v := range res {
			direct[k] = w.Copy(p.src, v)
		}
	}
	copied["Resources"] = mergeResources(direct, resources)
}

// contentParts returns a page's content streams as references in the file
// being written, whatever shape the source kept them in — a stream, an array
// of streams, or a reference to such an array, which producers do write.
func contentParts(w *reader.Writer, src *reader.Document, page reader.Dict) reader.Array {
	entry := page.Get("Contents")
	resolved := resolve(src, entry)
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
