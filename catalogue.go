package ops

import "github.com/go-pdfkit/reader"

// A document is more than its pages. Its catalogue says what language it is
// in, whether its structure has been marked up for a screen reader, how a
// viewer should open it — and, if it has one, where its form is.
//
// Every verb here rebuilds the document around the pages it kept, and the
// catalogue used to be rebuilt as two entries: the page tree and the word
// Catalog. Everything else was dropped. That is not a tidy-up. Rotating a tax
// return kept all 199 of its widget annotations on the pages and threw away
// the field list that gives them meaning — not a form with something missing
// but **half a form**, which is worse than none — and it threw away the
// language the document is in and the structure a screen reader needs, which
// for a government form is not merely untidy.
//
// So what can be carried is carried. What cannot is named here, with the
// reason, rather than disappearing quietly.

// documentKeys are the catalogue entries that describe the document rather
// than point into it, and so can be copied across unchanged.
var documentKeys = []reader.Name{
	"Lang",              // what language the words are in
	"MarkInfo",          // whether the structure has been marked up
	"ViewerPreferences", // how the document asks to be shown
	"PageLayout",        // one page at a time, or two
	"PageMode",          // whether to open with the bookmarks showing
	"Metadata",          // the XMP packet
	"Extensions",        // which extensions to the format the file uses
}

// sensitiveKeys are entries a sanitised file does not keep: the XMP packet
// says who wrote the document, on what machine, and when.
var sensitiveKeys = map[reader.Name]bool{"Metadata": true}

// What is still not carried, and why. Each of these points into the document
// rather than describing it, so copying one across a rebuild would leave it
// naming objects that are no longer there.
//
//   - /Names /Dests, where named destinations point at pages this may have
//     reordered or removed, and /Names /JavaScript, which is where a document
//     keeps code that runs. /Names /EmbeddedFiles IS carried: see attach.go —
//     a file inside a document belongs to no page, so nothing this does can
//     invalidate it, and dropping it loses something no page shows is there.
//   - /Perms, which records what a signature allows. Every verb here rewrites
//     the bytes the signature was taken over, so the signature is void and the
//     permission it granted with it.
//   - /OpenAction and /AA, which run when the document is opened.
//
// /StructTreeRoot, the marked-up structure a screen reader follows, is
// rebuilt: see structtree.go. Three parts of it are left out, and each is left
// out because it cannot be placed rather than because it is awkward.
//
//   - A mark inside a stream that no surviving page draws. Such a mark is
//     numbered within its own stream and filed under a key that stream
//     carries, so it can only be carried when the stream is still drawn — and
//     109 of the corpus's 215 such marks named a stream that no page of the
//     source drew either.
//   - A structure element's /Ref, which names other structure elements. It
//     cannot be answered while the rebuild is still deciding which of them
//     survive, and copied as it stands it would drag a second copy of the
//     source's tree — and of the source's pages behind it — into the file. No
//     file in the corpus has one; it is PDF 2.0.
//   - The structure of pages from more than one file. Two files have two
//     trees, and two /RoleMap and /ClassMap dictionaries in which the same
//     name may stand for two different things; a merged tree read through
//     either one of them would describe the other file's pages wrongly, and
//     there is no honest way to choose. Such a document keeps its pages and
//     nothing above them, as it already did for the catalogue and the form.

// keepCatalogue carries across what the source document said about itself.
func (d *Doc) keepCatalogue(w *reader.Writer, catalog reader.Dict, kept *keptAnnots, built []builtPage) {
	src, ok := d.singleSource()
	if !ok {
		// Pages from several files have several catalogues, and there is no
		// honest way to choose between them or to merge two forms whose
		// fields may be named the same. Such a document keeps its pages and
		// nothing above them — except the files it was handed, which belong to
		// no page and so cannot be in conflict.
		d.keepAttachments(w, catalog)
		return
	}
	// A document that opened has a catalogue; one that somehow came back
	// empty simply has nothing in it to carry.
	source, _ := src.Catalog()
	for _, key := range documentKeys {
		if d.sanitize && sensitiveKeys[key] {
			continue
		}
		if v, named := source[key]; named {
			catalog[key] = w.Copy(src, v)
		}
	}
	if form := d.keepForm(w, src, source, kept); form != nil {
		catalog["AcroForm"] = w.Add(form)
	}
	if tree := d.keepStructure(w, src, source, kept, built); tree != nil {
		catalog["StructTreeRoot"] = tree
	}
	d.keepAttachments(w, catalog)
}

// keepAttachments puts the files the document carries back into the catalogue.
//
// It is called for a document with one source and for one with several, unlike
// everything else here: two documents' forms cannot be merged and two
// catalogues cannot be chosen between, but two sets of files can simply both
// be carried. What cannot be carried is two files under one name, and Attach
// refuses that.
func (d *Doc) keepAttachments(w *reader.Writer, catalog reader.Dict) {
	if tree := d.writeAttachments(w); tree != nil {
		catalog["Names"] = w.Add(reader.Dict{"EmbeddedFiles": tree})
	}
}

// singleSource is the one document every page was borrowed from, when there is
// one. A document built here rather than borrowed has none.
func (d *Doc) singleSource() (*reader.Document, bool) {
	var only *reader.Document
	for _, p := range d.pages {
		if p.src == nil {
			return nil, false
		}
		if only == nil {
			only = p.src
			continue
		}
		if p.src != only {
			return nil, false
		}
	}
	return only, only != nil
}

// keepForm rebuilds the document's form around the widgets that survived.
//
// A field is kept when at least one of the places it shows on a page is still
// there. A field whose every widget went with a page that was dropped is
// dropped too: a form asking for something that cannot be seen or filled in is
// a worse thing to leave behind than a shorter form.
func (d *Doc) keepForm(w *reader.Writer, src *reader.Document, catalog reader.Dict, kept *keptAnnots) reader.Dict {
	if d.dropAnnots || d.flatten {
		// The widgets are gone, so the fields have nothing to point at.
		return nil
	}
	form, ok := src.GetDict(catalog, "AcroForm")
	if !ok {
		return nil
	}
	fields, ok := reader.ToArray(resolve(src, form.Get("Fields")))
	if !ok || len(fields) == 0 {
		return nil
	}
	out := reader.Dict{}
	for k, v := range form {
		if k == "Fields" {
			continue
		}
		out[k] = w.Copy(src, v)
	}
	var list reader.Array
	for _, entry := range fields {
		if ref, ok := d.keepField(w, src, entry, kept, reader.Ref{}, 0); ok {
			list = append(list, ref)
		}
	}
	if len(list) == 0 {
		return nil
	}
	out["Fields"] = list
	return out
}

// maxFieldDepth is how far down a field tree this will go. Deeper than this is
// a file playing games rather than a form.
const maxFieldDepth = 32

// keepField rebuilds one field, and reports whether anything of it survived.
//
// A field that is itself a widget on a page — which is how nearly every field
// with one place on the page is written — is already in the output, so what is
// wanted is the number it was given, not a second copy of it.
func (d *Doc) keepField(w *reader.Writer, src *reader.Document, entry reader.Object, kept *keptAnnots, parent reader.Ref, depth int) (reader.Ref, bool) {
	if depth > maxFieldDepth {
		return reader.Ref{}, false
	}
	if ref, ok := kept.find(src, entry); ok {
		if parent != (reader.Ref{}) {
			kept.dict[ref]["Parent"] = parent
		} else {
			delete(kept.dict[ref], "Parent")
		}
		return ref, true
	}
	field, ok := resolveDict(src, entry)
	if !ok {
		return reader.Ref{}, false
	}
	kids, hasKids := reader.ToArray(resolve(src, field.Get("Kids")))
	if !hasKids {
		// A field that is neither on a page nor a parent of anything has
		// nothing left to show for itself.
		return reader.Ref{}, false
	}
	// The field is given its number before its children are rebuilt, since
	// each of them has to point back at it.
	ref := w.Reserve()
	var list reader.Array
	for _, kid := range kids {
		if got, ok := d.keepField(w, src, kid, kept, ref, depth+1); ok {
			list = append(list, got)
		}
	}
	if len(list) == 0 {
		return reader.Ref{}, false
	}
	out := reader.Dict{}
	for k, v := range field {
		if k == "Kids" || k == "Parent" {
			continue
		}
		out[k] = w.Copy(src, v)
	}
	out["Kids"] = list
	if parent != (reader.Ref{}) {
		out["Parent"] = parent
	}
	w.Put(ref, out)
	return ref, true
}
