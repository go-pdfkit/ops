// Package ops is the verb layer of go-pdfkit: what people actually do to a PDF
// they already have. Merge two files, pull out pages three to seven, turn a
// page the right way up, drop the metadata, split a report into chapters.
//
// A document here is an ordered list of pages, each borrowed from a source
// file, plus the document-level pieces. Every operation rearranges that list or
// annotates its entries, and nothing is applied until [Doc.Bytes] is called.
// An operation therefore costs nothing until it has to, pages from several
// files mix freely, and the list stays a plain value.
package ops

import (
	"fmt"

	"github.com/go-pdfkit/reader"
)

// A Doc is a document being assembled: pages in the order they will be
// written, and the trailer's information dictionary.
type Doc struct {
	pages   []Page
	info    reader.Dict
	version string
}

// A Page is one page of a document, borrowed from the file it came from. The
// rotation is what will be written, not what the source said.
type Page struct {
	src    *reader.Document
	number int // the page's number in its source, counting from one
	rotate int // a multiple of 90, normalised to 0, 90, 180 or 270
	media  []float64
	crop   []float64
}

// Open reads a PDF file held in memory, with the empty password.
func Open(b []byte) (*Doc, error) { return OpenWithPassword(b, "") }

// OpenWithPassword reads a PDF file that may be encrypted.
func OpenWithPassword(b []byte, password string) (*Doc, error) {
	src, err := reader.OpenWithPassword(b, password)
	if err != nil {
		return nil, err
	}
	return FromDocument(src), nil
}

// FromDocument wraps an already-parsed document. Every page the document
// reports can be read — the page tree walk has already established that — so
// there is nothing here that can fail.
func FromDocument(src *reader.Document) *Doc {
	d := &Doc{version: src.Version()}
	for i := 1; i <= src.PageCount(); i++ {
		page, _ := src.Page(i)
		d.pages = append(d.pages, Page{src: src, number: i, rotate: rotationOf(src, page)})
	}
	if info, ok := src.GetDict(src.Trailer(), "Info"); ok {
		d.info = reader.Dict{}
		for k, v := range info {
			resolved, _ := src.Resolve(v)
			d.info[k] = resolved
		}
	}
	return d
}

// New returns an empty document, for merging into.
func New() *Doc { return &Doc{version: "1.7"} }

// rotationOf reads a page's /Rotate, normalised.
func rotationOf(src *reader.Document, page reader.Dict) int {
	// A page the tree produced always reads; an entry that is not a number
	// simply is not a rotation.
	o, _ := src.Resolve(page.Get("Rotate"))
	n, ok := reader.ToInt(o)
	if !ok {
		return 0
	}
	return normaliseRotation(int(n))
}

// normaliseRotation folds any multiple of ninety degrees into 0, 90, 180 or
// 270. A value that is not a multiple of ninety is not a rotation the format
// allows, and reads as none.
func normaliseRotation(deg int) int {
	if deg%90 != 0 {
		return 0
	}
	deg %= 360
	if deg < 0 {
		deg += 360
	}
	return deg
}

// PageCount reports how many pages the document has.
func (d *Doc) PageCount() int { return len(d.pages) }

// Version reports the PDF version that will be written.
func (d *Doc) Version() string { return d.version }

// SetVersion sets the version written in the header.
func (d *Doc) SetVersion(v string) { d.version = v }

// Info returns the document information dictionary, which may be nil.
func (d *Doc) Info() reader.Dict { return d.info }

// SetInfo replaces one entry of the information dictionary. An empty value
// removes it.
func (d *Doc) SetInfo(key reader.Name, value string) {
	if value == "" {
		delete(d.info, key)
		return
	}
	if d.info == nil {
		d.info = reader.Dict{}
	}
	d.info[key] = reader.String(value)
}

// ClearInfo drops the whole information dictionary, which is what "remove the
// metadata" means.
func (d *Doc) ClearInfo() { d.info = nil }

// Rotation reports the rotation of the i'th page, counting from one.
func (d *Doc) Rotation(i int) (int, error) {
	if err := d.check(i); err != nil {
		return 0, err
	}
	return d.pages[i-1].rotate, nil
}

// check reports whether a page number is in range.
func (d *Doc) check(i int) error {
	if i < 1 || i > len(d.pages) {
		return fmt.Errorf("ops: page %d is out of range (the document has %d)", i, len(d.pages))
	}
	return nil
}
