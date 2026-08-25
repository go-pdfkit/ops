package ops

import "github.com/go-pdfkit/reader"

// maxOutlineDepth bounds a bookmark tree, which a file can make a cycle.
const maxOutlineDepth = 32

// maxOutlineItems bounds how many bookmarks are carried over, so a file cannot
// make the writer spin.
const maxOutlineItems = 1 << 16

// An outlineItem is one bookmark, ready to be written: a title, somewhere to
// go, and whatever sits under it.
type outlineItem struct {
	title    []byte
	dest     reader.Object
	children []outlineItem
}

// DropOutlines leaves the bookmarks behind. They are kept by default: a merge
// that loses every bookmark is a poor merge.
func (d *Doc) DropOutlines() { d.dropOutlines = true }

// A Bookmark is an entry of an outline written from scratch: what it says, the
// page of this document it points at counting from one, and whatever sits
// under it.
//
// It is what a document assembled rather than merged carries — a shared
// edit, a report built out of pieces — where there is no source outline to
// carry over because the outline is the caller's own.
type Bookmark struct {
	Title    string
	Page     int
	Children []Bookmark
}

// SetOutline writes these bookmarks rather than carrying over the ones the
// sources had. An entry pointing at a page this document has not got is left
// out, and so is everything under it: a heading whose section has gone is not
// a heading any more.
//
// Passing nothing puts the sources' own bookmarks back.
func (d *Doc) SetOutline(marks []Bookmark) { d.outline = marks }

// writeOutlines writes the bookmarks the caller set, or carries over those of
// every source document, in the order the documents first contribute a page,
// dropping whatever pointed at a page that is no longer here.
func (d *Doc) writeOutlines(w *reader.Writer, where destinations, refs []reader.Ref) reader.Object {
	if d.dropOutlines {
		return nil
	}
	var items []outlineItem
	if len(d.outline) > 0 {
		budget := maxOutlineItems
		items = d.buildOutline(refs, d.outline, 0, &budget)
	} else {
		budget := maxOutlineItems
		for _, src := range d.sources() {
			items = append(items, d.readOutlines(w, src, where, &budget)...)
		}
	}
	if len(items) == 0 {
		return nil
	}
	root := w.Reserve()
	first, last, count := writeOutlineList(w, items, root)
	w.Put(root, reader.Dict{
		"Type":  reader.Name("Outlines"),
		"First": first,
		"Last":  last,
		"Count": reader.Integer(count),
	})
	return root
}

// sources lists the documents this one borrows from, in the order they first
// contribute a page.
func (d *Doc) sources() []*reader.Document {
	var out []*reader.Document
	seen := map[*reader.Document]bool{}
	for _, p := range d.pages {
		if p.src == nil || seen[p.src] {
			continue
		}
		seen[p.src] = true
		out = append(out, p.src)
	}
	return out
}

// buildOutline turns the caller's bookmarks into the ones a file carries,
// dropping any that point nowhere in this document.
func (d *Doc) buildOutline(refs []reader.Ref, marks []Bookmark, depth int, budget *int) []outlineItem {
	if depth > maxOutlineDepth {
		return nil
	}
	var out []outlineItem
	for _, m := range marks {
		if *budget <= 0 {
			return out
		}
		if m.Page < 1 || m.Page > len(refs) {
			continue
		}
		*budget--
		out = append(out, outlineItem{
			title:    []byte(m.Title),
			dest:     reader.Array{refs[m.Page-1], reader.Name("Fit")},
			children: d.buildOutline(refs, m.Children, depth+1, budget),
		})
	}
	return out
}

// readOutlines reads one document's bookmarks.
func (d *Doc) readOutlines(w *reader.Writer, src *reader.Document, where destinations, budget *int) []outlineItem {
	cat, _ := src.Catalog()
	root, ok := src.GetDict(cat, "Outlines")
	if !ok {
		return nil
	}
	return d.readOutlineChain(w, src, root.Get("First"), where, 0, budget)
}

// readOutlineChain follows a /Next chain, keeping what still leads somewhere.
func (d *Doc) readOutlineChain(w *reader.Writer, src *reader.Document, first reader.Object, where destinations, depth int, budget *int) []outlineItem {
	if depth > maxOutlineDepth {
		return nil
	}
	var out []outlineItem
	seen := map[int]bool{}
	entry := first
	for *budget > 0 {
		ref, isRef := entry.(reader.Ref)
		if isRef {
			if seen[ref.Num] {
				break
			}
			seen[ref.Num] = true
		}
		node, ok := resolveDict(src, entry)
		if !ok {
			break
		}
		children := d.readOutlineChain(w, src, node.Get("First"), where, depth+1, budget)
		dest, status := d.outlineDestination(w, src, node, where)
		// A bookmark whose target was removed goes with it. One that led
		// nowhere in the source stays, without a target: a file with broken
		// bookmarks keeps its shape rather than losing it.
		if status == destPageGone && len(children) == 0 {
			entry = node.Get("Next")
			if entry.Kind() == reader.KindNull {
				break
			}
			continue
		}
		if status != destMapped {
			dest = nil
		}
		title, _ := reader.ToString(resolve(src, node.Get("Title")))
		out = append(out, outlineItem{title: title, dest: dest, children: children})
		entry = node.Get("Next")
		if entry.Kind() == reader.KindNull {
			break
		}
	}
	return out
}

// outlineDestination reads where a bookmark goes, from either of the two
// places a file may put it.
func (d *Doc) outlineDestination(w *reader.Writer, src *reader.Document, node reader.Dict, where destinations) (reader.Object, destStatus) {
	if dest, status := d.remapDestination(w, src, node.Get("Dest"), where); status != destUnusable {
		return dest, status
	}
	dict, ok := resolveDict(src, node.Get("A"))
	if !ok {
		return nil, destUnusable
	}
	if kind, _ := reader.ToName(dict.Get("S")); kind != "GoTo" {
		return nil, destUnusable
	}
	return d.remapDestination(w, src, dict.Get("D"), where)
}

// writeOutlineList writes a list of bookmarks as the linked list a PDF
// outline is, and reports its ends and how many entries it holds in all.
func writeOutlineList(w *reader.Writer, items []outlineItem, parent reader.Ref) (first, last reader.Ref, count int) {
	refs := make([]reader.Ref, len(items))
	for i := range items {
		refs[i] = w.Reserve()
	}
	for i, item := range items {
		node := reader.Dict{
			"Title":  reader.String(item.title),
			"Parent": parent,
		}
		if item.dest != nil {
			node["Dest"] = item.dest
		}
		if i > 0 {
			node["Prev"] = refs[i-1]
		}
		if i+1 < len(refs) {
			node["Next"] = refs[i+1]
		}
		if len(item.children) > 0 {
			childFirst, childLast, childCount := writeOutlineList(w, item.children, refs[i])
			node["First"] = childFirst
			node["Last"] = childLast
			// A negative count means the entry is written closed, which is
			// what a reader expects of a bookmark it did not open.
			node["Count"] = reader.Integer(-childCount)
			count += childCount
		}
		w.Put(refs[i], node)
		count++
	}
	return refs[0], refs[len(refs)-1], count
}
