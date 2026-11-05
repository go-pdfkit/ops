package ops

import "github.com/go-pdfkit/reader"

// A Link is one link annotation of the document as it now stands: where it
// sits, and where it leads.
//
// Page and Target are numbered in THIS document, not in the files its pages
// came from, so a link survives having its pages reordered and reports Target 0
// once the page it pointed at has been dropped.
//
// Rect is in PDF user space — origin bottom-left, y upwards — and normalised so
// that Rect[0] <= Rect[2] and Rect[1] <= Rect[3]. A file is free to write a
// rectangle from either pair of corners, and a caller hit-testing an
// un-normalised one finds nothing at all, which looks exactly like a page with
// no links on it.
type Link struct {
	Page   int
	Rect   [4]float64
	Target int
	URI    string
}

// Leads reports whether this link goes somewhere that can be named: a page of
// this document, or an address.
//
// ⛔ A link that leads NOWHERE THIS CAN FOLLOW is still in the list, with
// Target 0 and URI "". Dropping it would have a caller counting links
// under-report, and "twelve links, one of which goes somewhere I cannot follow"
// is a different fact from "eleven links". It happens for an honest reason as
// well as a dishonest one: a page this document no longer has, a GoToR into
// another file, a Launch, a destination name nothing defines.
func (l Link) Leads() bool { return l.Target != 0 || l.URI != "" }

// Links is every link annotation of the document, in page order and, within a
// page, in the order the page lists them.
//
// It reads the files the pages came from: a link added by this package's own
// verbs is not among them, because none of them adds one.
//
// # What it follows
//
//	/A with /S /GoTo    a page of this document
//	/A with /S /URI     an address
//	/Dest               a page of this document, through a name or a name tree
//
// ⛔ /A IS READ FIRST. A link is not meant to carry both /A and /Dest — the
// link-annotation clause of the PDF specification (ISO 32000-1 §12.5.6.5,
// Table 173) makes /Dest "not permitted if an A entry is present", as three
// independent implementations' documentation records; the clause's own wording
// was not opened here. A file carrying both is therefore malformed, and the
// action is what a reader acts on, so it is what this reports.
//
// Everything else — GoToR into another file, Launch, Named, JavaScript, a
// chain of /Next actions — is a link this does not follow. It is still listed.
func (d *Doc) Links() []Link {
	here := d.pagesBySource()
	pages := map[*reader.Document]map[int]int{}
	var out []Link
	for i, p := range d.pages {
		if p.src == nil {
			continue
		}
		// ⛔ THE ERROR IS NOT BRANCHED ON, and that is a decision rather than an
		// oversight. d.pages was built from the source's own page list, and
		// reader only puts a page in that list once it has RESOLVED it to a
		// dictionary — walkPages drops a kid that is anything else — so Page
		// cannot fail for a number that came from there. A branch on it would
		// be a guard no test can reach, which is a claim nobody checks and a
		// hole in the coverage gate at the same time. Were it ever to fail,
		// dict is nil, Get on a nil Dict is Null, and the page is passed over
		// exactly as a page carrying no annotations is.
		dict, _ := p.src.Page(p.number)
		list, ok := resolveArray(p.src, dict.Get("Annots"))
		if !ok {
			continue
		}
		for _, o := range list {
			annot, ok := resolveDict(p.src, o)
			if !ok {
				continue
			}
			if name, _ := reader.ToName(annot.Get("Subtype")); name != "Link" {
				continue
			}
			l := Link{Page: i + 1, Rect: annotRect(p.src, annot.Get("Rect"))}
			l.Target, l.URI = leadsTo(p.src, annot, here, pages)
			out = append(out, l)
		}
	}
	return out
}

// pagesBySource says, for each page of each file the document borrowed from,
// which page of THIS document it became.
//
// A selection may ask for the same source page twice; the first copy is the one
// anything pointing at that page is pointed at, which is the choice the
// destination map and the structure tree already make.
func (d *Doc) pagesBySource() map[pageKey]int {
	out := make(map[pageKey]int, len(d.pages))
	for i, p := range d.pages {
		if p.src == nil {
			continue
		}
		k := pageKey{p.src, p.number}
		if _, seen := out[k]; !seen {
			out[k] = i + 1
		}
	}
	return out
}

// leadsTo follows one link annotation.
func leadsTo(src *reader.Document, annot reader.Dict, here map[pageKey]int, pages map[*reader.Document]map[int]int) (int, string) {
	if action, ok := resolveDict(src, annot.Get("A")); ok {
		switch kind, _ := reader.ToName(action.Get("S")); kind {
		case "URI":
			s, _ := reader.ToString(resolve(src, action.Get("URI")))
			return 0, string(s)
		case "GoTo":
			return destinationPage(src, action.Get("D"), here, pages), ""
		}
		return 0, ""
	}
	return destinationPage(src, annot.Get("Dest"), here, pages), ""
}

// destinationPage follows a destination to the page of THIS document it names,
// or 0.
func destinationPage(src *reader.Document, o reader.Object, here map[pageKey]int, pages map[*reader.Document]map[int]int) int {
	arr, ok := resolveDestination(src, o)
	if !ok {
		return 0
	}
	// A destination names its page by reference, except in a remote one, where
	// it is an index into a file this document is not.
	ref, ok := arr[0].(reader.Ref)
	if !ok {
		return 0
	}
	number := pageIndex(src, pages)[ref.Num]
	if number == 0 {
		return 0
	}
	return here[pageKey{src, number}]
}

// pageIndex is a source's pages by object number, built once and kept for the
// rest of the call.
//
// ⛔ NOT pageNumberOf, WHICH WALKS EVERY PAGE TO ANSWER ONE QUESTION. Called
// once per link that is links × pages: a file carrying 50 000 links over 5 000
// pages would spend 250 million comparisons producing a list, and nothing about
// the list says it should have been slow. Both numbers are the file's to
// choose, which makes it a bound worth holding rather than a tidiness.
func pageIndex(src *reader.Document, cache map[*reader.Document]map[int]int) map[int]int {
	if idx, ok := cache[src]; ok {
		return idx
	}
	idx := make(map[int]int, src.PageCount())
	for i := 1; i <= src.PageCount(); i++ {
		if r, ok := src.PageRef(i); ok {
			if _, seen := idx[r.Num]; !seen {
				idx[r.Num] = i
			}
		}
	}
	cache[src] = idx
	return idx
}

// annotRect reads an annotation's rectangle and normalises its corners.
func annotRect(src *reader.Document, o reader.Object) [4]float64 {
	arr, ok := resolveArray(src, o)
	if !ok || len(arr) < 4 {
		return [4]float64{}
	}
	var r [4]float64
	for i := range r {
		r[i], _ = resolveFloat(src, arr[i])
	}
	if r[0] > r[2] {
		r[0], r[2] = r[2], r[0]
	}
	if r[1] > r[3] {
		r[1], r[3] = r[3], r[1]
	}
	return r
}
