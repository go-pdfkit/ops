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

// writeOutlines carries the bookmarks of every source document over, in the
// order the documents first contribute a page, dropping whatever pointed at a
// page that is no longer here.
func (d *Doc) writeOutlines(w *reader.Writer, where destinations) reader.Object {
	if d.dropOutlines {
		return nil
	}
	var items []outlineItem
	budget := maxOutlineItems
	for _, src := range d.sources() {
		items = append(items, d.readOutlines(w, src, where, &budget)...)
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
