// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"sort"

	"github.com/go-pdfkit/reader"
)

// An Attachment is a file travelling inside the document.
type Attachment struct {
	// Name is what the document calls it.
	Name string
	// Description is what it says the file is, when it says anything.
	Description string
	// Data is the file.
	Data []byte
}

// Attachments are the files the document carries, in the order it names them.
//
// A PDF can hold whole files inside it — the spreadsheet a report was drawn
// from, the XML a form was filled from, the source of a figure. They are not
// drawn and nothing on the page says they are there, which is why a tool that
// rewrites a document can drop them without anyone noticing until the file is
// wanted.
func (d *Doc) Attachments() []Attachment {
	out := append([]Attachment(nil), d.attached...)
	// Every document the pages came from, not just the one. A merged document
	// has several sources and each may carry files; unlike a form or a
	// catalogue, two sets of files cannot conflict, because a file belongs to
	// no page.
	seen := map[*reader.Document]bool{}
	for _, p := range d.pages {
		if p.src == nil || seen[p.src] {
			continue
		}
		seen[p.src] = true
		out = append(out, readAttachments(p.src)...)
	}
	return out
}

// Attach puts a file inside the document, under a name.
//
// The name is what a reader shows and what another tool looks it up by. Two
// files under one name is a document that has lost one of them, so a name
// already used is refused rather than quietly replacing what is there.
func (d *Doc) Attach(name string, data []byte, description string) error {
	if name == "" {
		return fmt.Errorf("ops: a file inside a document needs a name")
	}
	for _, a := range d.Attachments() {
		if a.Name == name {
			return fmt.Errorf("ops: this document already carries a file called %q", name)
		}
	}
	d.attached = append(d.attached, Attachment{Name: name, Description: description, Data: data})
	return nil
}

// Detach removes the file of that name, and says whether there was one.
func (d *Doc) Detach(name string) bool {
	for i, a := range d.attached {
		if a.Name == name {
			d.attached = append(d.attached[:i], d.attached[i+1:]...)
			return true
		}
	}
	// A file that came in with the document is dropped by being left out of
	// what is written, which is what dropped is set for.
	for _, a := range d.Attachments() {
		if a.Name == name {
			d.dropped = append(d.dropped, name)
			return true
		}
	}
	return false
}

// readAttachments walks the /EmbeddedFiles name tree of a source document.
func readAttachments(src *reader.Document) []Attachment {
	// A document that opened has a catalogue; one that somehow has none simply
	// has nothing in it to look under, which the next line asks anyway.
	cat, _ := src.Catalog()
	names, ok := src.GetDict(cat, "Names")
	if !ok {
		return nil
	}
	tree, ok := src.GetDict(names, "EmbeddedFiles")
	if !ok {
		return nil
	}
	var out []Attachment
	walkNameTree(src, tree, 0, func(name string, value reader.Object) {
		spec, ok := reader.ToDict(resolve(src, value))
		if !ok {
			return
		}
		a := Attachment{Name: name}
		if s, ok := reader.ToString(resolve(src, spec.Get("Desc"))); ok {
			a.Description = string(s)
		}
		ef, ok := reader.ToDict(resolve(src, spec.Get("EF")))
		if !ok {
			return
		}
		// /F is the usual place; /UF is its Unicode twin and holds the same
		// stream when both are there.
		read := false
		for _, key := range []reader.Name{"F", "UF"} {
			st, ok := reader.ToStream(resolve(src, ef.Get(key)))
			if !ok {
				continue
			}
			data, filter, err := reader.DecodeStream(st, src.Get)
			if err != nil {
				continue
			}
			// A chain that stopped at a filter nothing here reads hands back
			// the bytes as they were STORED, and names what they are still in.
			// Handing those over as the file gives somebody a spreadsheet that
			// is not one, which is worse than saying there is no file.
			if filter != "" {
				continue
			}
			a.Data, read = data, true
			break
		}
		// A specification pointing at nothing that can be read is not a file.
		// Handing it over with no bytes says the document carries something it
		// does not, and whoever asked would save an empty file believing it.
		if !read {
			return
		}
		out = append(out, a)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// walkNameTree visits the entries of a PDF name tree, which is a sorted map
// spread over a tree of nodes so that a reader can find a name without reading
// all of them.
func walkNameTree(src *reader.Document, node reader.Dict, depth int, visit func(string, reader.Object)) {
	if depth > 16 {
		// A tree that holds itself is a tree with no leaves, and following it
		// is a way of never coming back.
		return
	}
	if arr, ok := reader.ToArray(resolve(src, node.Get("Names"))); ok {
		for i := 0; i+1 < len(arr); i += 2 {
			key, ok := reader.ToString(resolve(src, arr[i]))
			if !ok {
				continue
			}
			visit(string(key), arr[i+1])
		}
	}
	if kids, ok := reader.ToArray(resolve(src, node.Get("Kids"))); ok {
		for _, k := range kids {
			if kd, ok := reader.ToDict(resolve(src, k)); ok {
				walkNameTree(src, kd, depth+1, visit)
			}
		}
	}
}

// writeAttachments builds the /Names /EmbeddedFiles tree the catalogue points
// at, and returns nil when there is nothing to point at.
//
// The catalogue's /Names used to be dropped whole, on the ground that a name
// tree points into the document rather than describing it. That is true of
// /Dests, whose names point at pages this may have reordered or removed, and
// of /JavaScript, which a sanitised file has no business keeping. It is not
// true of /EmbeddedFiles: a file inside a document is not attached to any page,
// so it survives every verb here — and was being thrown away by all of them.
//
// 45 of the 3 215 documents in the forms and scans corpora carry one, 50 files
// between them. Nothing on the page says they are there, so nobody notices
// until the file is wanted.
func (d *Doc) writeAttachments(w *reader.Writer) reader.Object {
	if d.sanitize {
		// A sanitised file leaves behind what runs and what travels: a file
		// inside a document is the thing this is for.
		return nil
	}
	all := d.Attachments()
	dropped := map[string]bool{}
	for _, n := range d.dropped {
		dropped[n] = true
	}
	var names reader.Array
	for _, a := range all {
		if dropped[a.Name] {
			continue
		}
		stream := w.Add(&reader.Stream{Dict: reader.Dict{
			"Type": reader.Name("EmbeddedFile"),
			"Params": reader.Dict{
				"Size": reader.Integer(len(a.Data)),
			},
		}, Raw: a.Data})
		spec := reader.Dict{
			"Type": reader.Name("Filespec"),
			"F":    reader.String(a.Name),
			"UF":   reader.String(a.Name),
			"EF":   reader.Dict{"F": stream, "UF": stream},
		}
		if a.Description != "" {
			spec["Desc"] = reader.String(a.Description)
		}
		names = append(names, reader.String(a.Name), w.Add(spec))
	}
	if len(names) == 0 {
		return nil
	}
	// One leaf, sorted: a name tree may be a single node, and the entries have
	// to be in order for a reader that searches it rather than reading it all.
	return w.Add(reader.Dict{"Names": names})
}
