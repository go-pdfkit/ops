// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"

	"github.com/go-pdfkit/reader"
)

// A Layer is one of a document's optional content groups — what a viewer calls
// a layer, and what a drawing keeps its dimensions, its markup and its
// alternatives on.
type Layer struct {
	// Name is what the viewer shows. ⛔ It is NOT unique: a document may hold
	// two layers of the same name, and the methods here say so rather than
	// guessing which was meant.
	Name string
	// Visible is whether the document's default configuration has it on.
	Visible bool
	// Locked is whether the document asks that it not be switched: a viewer
	// shows the layer greyed, and [Doc.SetLayerVisible] refuses it.
	Locked bool
	// Depth is how deeply the layer is nested in the document's order, from
	// zero. A drawing groups its layers, and the nesting is what makes a list
	// of forty of them readable.
	Depth int
}

// a layerEdit is one change to make to the layers at write time.
type layerEdit struct {
	kind    string // "show", "hide", "add", "drop"
	name    string
	parent  string
	applied bool
}

// Layers reports the document's layers, in the order the document asks them to
// be shown and with their nesting.
//
// A document with no optional content has none.
func (d *Doc) Layers() []Layer {
	src, ok := d.singleSource()
	if !ok {
		// ⛔ Pages from several files have several sets of layers, and this
		// answers for the document as it stands rather than guessing. The
		// WRITE merges them — two layers of the same name from two drawings
		// are two layers, which is not true of two form fields of the same
		// name — but until the file is written there is no merged document to
		// report on.
		return nil
	}
	groups := sourceLayers(src)
	out := make([]Layer, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Layer)
	}
	return applyEditsToList(out, d.layerEdits)
}

// SetLayerVisible turns a layer on or off.
//
// ⛔ A LOCKED layer is refused. The lock is the document asking that this one
// not be switched — a drawing's dimension layer that everything else is
// positioned against — and a tool that switched it anyway would be ignoring
// the only thing the file says about its own layers.
func (d *Doc) SetLayerVisible(name string, visible bool) error {
	if err := d.oneLayerNamed(name); err != nil {
		return err
	}
	for _, l := range d.Layers() {
		if l.Name == name && l.Locked {
			return fmt.Errorf("ops: the layer %q is locked, and the document "+
				"asks that it not be switched", name)
		}
	}
	kind := "hide"
	if visible {
		kind = "show"
	}
	d.layerEdits = append(d.layerEdits, layerEdit{kind: kind, name: name})
	return nil
}

// AddLayer adds an empty layer, under parent when parent names one.
//
// ⛔ The layer has NO CONTENT on it, and that is what adding one means: a
// template gains a layer for whoever draws on it next. Nothing in the document
// becomes hidden or shown by adding one.
func (d *Doc) AddLayer(name, parent string) error {
	if name == "" {
		return fmt.Errorf("ops: a layer needs a name")
	}
	if parent != "" {
		if err := d.oneLayerNamed(parent); err != nil {
			return fmt.Errorf("ops: the parent: %w", err)
		}
	}
	d.layerEdits = append(d.layerEdits, layerEdit{kind: "add", name: name, parent: parent})
	return nil
}

// DropLayer removes a layer.
//
// ⛔ WHAT WAS DRAWN ON IT DOES NOT GO WITH IT. The content stays in the page
// and stops being optional, so a HIDDEN layer that is dropped becomes
// permanently visible — which is the opposite of what somebody deleting a
// layer called "internal pricing" expects. Removing the content as well would
// mean rewriting every content stream that mentions the layer, which this does
// not do; the refusal below is the honest half of that.
func (d *Doc) DropLayer(name string) error {
	if err := d.oneLayerNamed(name); err != nil {
		return err
	}
	for _, l := range d.Layers() {
		if l.Name != name {
			continue
		}
		if !l.Visible {
			return fmt.Errorf("ops: the layer %q is hidden, and dropping it "+
				"would make what is drawn on it permanently VISIBLE: "+
				"show it first if that is what you mean", name)
		}
	}
	d.layerEdits = append(d.layerEdits, layerEdit{kind: "drop", name: name})
	return nil
}

// oneLayerNamed says whether exactly one layer has this name.
func (d *Doc) oneLayerNamed(name string) error {
	n := 0
	for _, l := range d.Layers() {
		if l.Name == name {
			n++
		}
	}
	switch n {
	case 1:
		return nil
	case 0:
		return fmt.Errorf("ops: this document has no layer named %q", name)
	}
	// ⛔ Named twice is refused rather than taking the first. A layer name is
	// not an identifier, and a drawing assembled from two others often has
	// two "Markup" layers — switching one of them at random is a change
	// nobody asked for and nobody can see.
	return fmt.Errorf("ops: %d layers are named %q, so naming one does not say which", n, name)
}

// applyEditsToList is what Layers reports after the edits that have not been
// written yet.
func applyEditsToList(layers []Layer, edits []layerEdit) []Layer {
	for _, e := range edits {
		switch e.kind {
		case "show", "hide":
			for i := range layers {
				if layers[i].Name == e.name {
					layers[i].Visible = e.kind == "show"
				}
			}
		case "add":
			// ⛔ Inserted after its PARENT'S SUBTREE, not at the end. The
			// order is positional — a child follows the layer it belongs to —
			// so appending a depth-one layer after an unrelated top-level one
			// made it that one's child. The first draft did exactly that, and
			// only writing the nesting out revealed it.
			at, depth := childSlot(layers, e.parent)
			layers = append(layers, Layer{})
			copy(layers[at+1:], layers[at:])
			layers[at] = Layer{Name: e.name, Visible: true, Depth: depth}
		case "drop":
			out := layers[:0]
			for _, l := range layers {
				if l.Name != e.name {
					out = append(out, l)
				}
			}
			layers = out
		}
	}
	return layers
}

// a sourceLayer is a layer and the object it is in its own file.
type sourceLayer struct {
	Layer
	ref reader.Object
}

// sourceLayers reads a document's /OCProperties.
//
// ⛔ The ORDER and the NESTING come from /D /Order, which is a nested array,
// and the membership from /OCGs. A layer in /OCGs that /Order does not mention
// is still a layer — a viewer lists it flat — so both have to be read and
// neither is the whole answer.
func sourceLayers(src *reader.Document) []sourceLayer {
	catalog, _ := src.Catalog()
	props, ok := follow(src, catalog["OCProperties"]).(reader.Dict)
	if !ok {
		return nil
	}
	known := map[int]bool{}
	var all []sourceLayer
	add := func(obj reader.Object, depth int) {
		ref, isRef := obj.(reader.Ref)
		if !isRef {
			return
		}
		if known[ref.Num] {
			return
		}
		dict, ok := follow(src, obj).(reader.Dict)
		if !ok {
			return
		}
		known[ref.Num] = true
		name := ""
		if s, ok := follow(src, dict["Name"]).(reader.String); ok {
			name = string(s)
		}
		all = append(all, sourceLayer{Layer: Layer{Name: name, Depth: depth}, ref: ref})
	}
	config, _ := follow(src, props["D"]).(reader.Dict)
	// The order first, so that the nesting is known; then anything /OCGs names
	// that the order left out.
	var walk func(obj reader.Object, depth int)
	walked := 0
	walk = func(obj reader.Object, depth int) {
		if depth > maxLayerDepth {
			return
		}
		switch v := follow(src, obj).(type) {
		case reader.Array:
			for _, item := range v {
				// ⛔ THE COUNT IS CHECKED HERE, inside the loop, and the first
				// draft checked it only on entry to this function. A guard at
				// the door bounds the RECURSION and not the list: one flat
				// /Order of six thousand layers passed it once and added all
				// six thousand. The test that lowered the ceiling to two and
				// got six back is what found it, which is the whole argument
				// for a ceiling a test can reach.
				if walked >= maxLayers {
					return
				}
				// ⛔ A nested array is a GROUP, and its first element may be a
				// STRING that is the group's label rather than a layer — a
				// reader that took it for a layer would list a heading as
				// something switchable.
				//
				// There is no test for a string here, and that is a removal
				// rather than an omission: `add` already wants a reference
				// that resolves to a DICTIONARY, so a label — a string, or a
				// reference to one — falls out there. A mutation deleting the
				// explicit check changed no test, which is how it was found;
				// two ways of refusing the same thing is one branch nothing
				// can distinguish.
				if _, isArray := follow(src, item).(reader.Array); isArray {
					walk(item, depth+1)
					continue
				}
				walked++
				add(item, depth)
			}
		}
	}
	walk(config["Order"], 0)
	if ocgs, ok := follow(src, props["OCGs"]).(reader.Array); ok {
		for _, obj := range ocgs {
			if walked >= maxLayers {
				break
			}
			walked++
			add(obj, 0)
		}
	}
	// Visibility: everything is on unless the configuration says otherwise.
	off := refSet(src, config["OFF"])
	locked := refSet(src, config["Locked"])
	baseState, _ := follow(src, config["BaseState"]).(reader.Name)
	on := refSet(src, config["ON"])
	for i := range all {
		ref := all[i].ref.(reader.Ref)
		// ⛔ /BaseState OFF inverts the question: everything is hidden unless
		// /ON names it. A reader that only looked at /OFF would report every
		// layer of such a document as visible — and a drawing that ships with
		// all its layers off but one is exactly how an alternatives document
		// is built.
		if baseState == "OFF" {
			all[i].Visible = on[ref.Num]
		} else {
			all[i].Visible = !off[ref.Num]
		}
		all[i].Locked = locked[ref.Num]
	}
	return all
}

// The ceilings. A layer list is a count the file chose, and /Order is a tree a
// file can nest as deep as it likes.
var (
	maxLayers     = 4096
	maxLayerDepth = 64
)

func refSet(src *reader.Document, obj reader.Object) map[int]bool {
	out := map[int]bool{}
	arr, ok := follow(src, obj).(reader.Array)
	if !ok {
		return out
	}
	for _, item := range arr {
		if ref, ok := item.(reader.Ref); ok {
			out[ref.Num] = true
		}
	}
	return out
}

// writeLayers builds the /OCProperties for the document being written.
//
// ⛔ It is CONSTRUCTED rather than copied whole. Copying the source's
// /OCProperties would be one call — and then the layer objects in it could not
// be matched by name, because after w.Copy they are references into a file
// that cannot be read back. Building it means copying each layer OBJECT, which
// w.Copy deduplicates against the copies the pages' own resources already
// made, so the lists name the same objects the content is marked with. Getting
// that wrong gives a file whose ON and OFF lists name layers nothing is drawn
// on.
func (d *Doc) writeLayers(w *reader.Writer) reader.Object {
	type entry struct {
		src    *reader.Document
		layer  Layer
		ref    reader.Object
		added  bool
		parent string
	}
	var entries []entry
	// ⛔ Every source, not just the first. keepCatalogue refuses to merge two
	// catalogues because two forms whose fields are named the same cannot
	// honestly become one — but two drawings' layers CAN: a layer is its
	// object, and two layers named "Markup" are two layers, which is exactly
	// what a viewer should show for a merge of two marked-up drawings.
	seen := map[*reader.Document]bool{}
	for _, p := range d.pages {
		if p.src == nil || seen[p.src] {
			continue
		}
		seen[p.src] = true
		for _, l := range sourceLayers(p.src) {
			entries = append(entries, entry{src: p.src, layer: l.Layer, ref: l.ref})
		}
	}
	for _, e := range d.layerEdits {
		switch e.kind {
		case "show", "hide":
			for i := range entries {
				if entries[i].layer.Name == e.name {
					entries[i].layer.Visible = e.kind == "show"
				}
			}
		case "drop":
			kept := entries[:0]
			for _, en := range entries {
				if en.layer.Name != e.name {
					kept = append(kept, en)
				}
			}
			entries = kept
		case "add":
			// ⛔ The same insertion rule as applyEditsToList, because the two
			// must agree: Layers() says what is about to be written, and a
			// pair that put a new child in two different places would make
			// the report a lie. The layers are extracted, the shared rule
			// applied, and the entries put back in the order it decided.
			flat := make([]Layer, len(entries))
			for i, en := range entries {
				flat[i] = en.layer
			}
			flat = applyEditsToList(flat, []layerEdit{e})
			rebuilt := make([]entry, 0, len(flat))
			for _, l := range flat {
				found := false
				for _, en := range entries {
					if en.layer.Name == l.Name && en.layer.Depth == l.Depth {
						en.layer = l
						rebuilt = append(rebuilt, en)
						found = true
						break
					}
				}
				if !found {
					rebuilt = append(rebuilt, entry{layer: l, added: true, parent: e.parent})
				}
			}
			entries = rebuilt
		}
	}
	if len(entries) == 0 {
		return nil
	}
	var ocgs, on, off, locked reader.Array
	refs := make([]reader.Object, len(entries))
	depths := make([]int, len(entries))
	for i, e := range entries {
		var ref reader.Object
		if e.added {
			ref = w.Add(reader.Dict{
				"Type": reader.Name("OCG"),
				"Name": reader.String(e.layer.Name),
			})
		} else {
			ref = w.Copy(e.src, e.ref)
		}
		refs[i], depths[i] = ref, e.layer.Depth
		ocgs = append(ocgs, ref)
		if e.layer.Visible {
			on = append(on, ref)
		} else {
			off = append(off, ref)
		}
		if e.layer.Locked {
			locked = append(locked, ref)
		}
	}
	// ⛔ /Order is NESTED, and the first draft wrote it flat. The nesting is
	// what makes a panel of forty layers readable, and a round-trip test is
	// what caught its loss: a child layer added under a parent came back at
	// the top level, which READING the nesting correctly did not reveal —
	// that test only ever read a file somebody else had written.
	order, _ := nestedOrder(refs, depths, 0, 0)
	config := reader.Dict{"Order": order, "ON": on}
	if len(off) > 0 {
		config["OFF"] = off
	}
	if len(locked) > 0 {
		config["Locked"] = locked
	}
	return w.Add(reader.Dict{"OCGs": ocgs, "D": w.Add(config)})
}

// nestedOrder builds a /Order array from a flat list of layers and their
// depths, putting each layer's children in an array just after it.
//
// It returns the array for the layers at `depth` starting at `i`, and the
// index it stopped at. ⛔ A child array FOLLOWS its parent rather than
// containing it, which is the shape the format asks for:
//
//	[ plan [ walls doors ] notes ]
//
// A layer whose depth jumps by more than one — which a file can say and a
// viewer shows flat — is taken at the depth it is reached, because the
// alternative is inventing parents that do not exist.
func nestedOrder(refs []reader.Object, depths []int, i, depth int) (reader.Array, int) {
	var out reader.Array
	for i < len(refs) {
		switch {
		case depths[i] < depth:
			return out, i
		case depths[i] > depth:
			// The children of the layer just written.
			kids, next := nestedOrder(refs, depths, i, depth+1)
			if len(kids) > 0 {
				out = append(out, kids)
			}
			i = next
		default:
			out = append(out, refs[i])
			i++
		}
	}
	return out, i
}

// childSlot says where a new child of parent goes and at what depth: just
// past the parent and everything already nested under it.
//
// ⛔ The final return serves BOTH "there is no parent" and "the parent is not
// there", which is what makes it reachable. The first draft had two helpers
// with a not-found return each, and [Doc.AddLayer] has already refused a
// parent that is not there — so both were dead code a coverage gap found.
// One shared answer, used by the case that really happens.
func childSlot(layers []Layer, parent string) (at, depth int) {
	for i, l := range layers {
		if parent == "" || l.Name != parent {
			continue
		}
		j := i + 1
		for j < len(layers) && layers[j].Depth > l.Depth {
			j++
		}
		return j, l.Depth + 1
	}
	return len(layers), 0
}
