package ops

import (
	"fmt"
	"testing"

	"github.com/go-pdfkit/reader"
)

// A rich fixture is a document carrying everything this package has to think
// about when it rebuilds a file: links between pages, actions of every
// temperament, an attachment, an appearance stream to flatten, a named
// destination, page-level actions and associated files, and a tree of
// bookmarks including one that already led nowhere.
type rich struct {
	bytes []byte
	pages int
}

// richPDF builds that document.
func richPDF(t *testing.T, pages int) rich {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRefs := make([]reader.Ref, pages)
	for i := range pageRefs {
		pageRefs[i] = w.Reserve()
	}

	// An appearance stream, for the annotation that gets flattened.
	appearance := w.Add(&reader.Stream{
		Dict: reader.Dict{
			"Type":    reader.Name("XObject"),
			"Subtype": reader.Name("Form"),
			"BBox":    reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)},
		},
		Raw: []byte("0 0 1 rg 0 0 10 10 re f"),
	})
	// A file specification with an embedded file, reached two ways.
	embedded := w.Add(&reader.Stream{
		Dict: reader.Dict{"Type": reader.Name("EmbeddedFile")},
		Raw:  []byte("secret payload"),
	})
	fileSpec := w.Add(reader.Dict{
		"Type": reader.Name("Filespec"),
		"F":    reader.String("payload.txt"),
		"EF":   reader.Dict{"F": embedded},
	})

	for i := 0; i < pages; i++ {
		content := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(fmt.Sprintf("page %d", i+1))})
		target := pageRefs[(i+1)%pages]
		annots := reader.Array{
			// A link to the next page.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("Link"),
				"Rect":    reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)},
				"Dest":    reader.Array{target, reader.Name("Fit")},
			}),
			// A link by action.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("Link"),
				"Rect":    reader.Array{reader.Integer(10), reader.Integer(0), reader.Integer(20), reader.Integer(10)},
				"A":       reader.Dict{"S": reader.Name("GoTo"), "D": reader.Array{pageRefs[0], reader.Name("Fit")}},
			}),
			// A link that runs something.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("Link"),
				"Rect":    reader.Array{reader.Integer(20), reader.Integer(0), reader.Integer(30), reader.Integer(10)},
				"A":       reader.Dict{"S": reader.Name("JavaScript"), "JS": reader.String("app.alert('hi')")},
				"AA":      reader.Dict{"E": reader.Dict{"S": reader.Name("JavaScript")}},
			}),
			// A link to the web, which is not executable and stays.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("Link"),
				"Rect":    reader.Array{reader.Integer(30), reader.Integer(0), reader.Integer(40), reader.Integer(10)},
				"A":       reader.Dict{"S": reader.Name("URI"), "URI": reader.String("https://example.invalid/")},
			}),
			// Something with an appearance, to flatten.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("Square"),
				"Rect":    reader.Array{reader.Integer(40), reader.Integer(40), reader.Integer(60), reader.Integer(60)},
				"AP":      reader.Dict{"N": appearance},
			}),
			// An attachment.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("FileAttachment"),
				"Rect":    reader.Array{reader.Integer(60), reader.Integer(0), reader.Integer(70), reader.Integer(10)},
				"FS":      fileSpec,
			}),
			// A link by named destination.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("Link"),
				"Rect":    reader.Array{reader.Integer(70), reader.Integer(0), reader.Integer(80), reader.Integer(10)},
				"Dest":    reader.String("thestart"),
			}),
			// One that is hidden, so flattening must leave it out.
			w.Add(reader.Dict{
				"Type":    reader.Name("Annot"),
				"Subtype": reader.Name("Square"),
				"F":       reader.Integer(2),
				"Rect":    reader.Array{reader.Integer(80), reader.Integer(0), reader.Integer(90), reader.Integer(10)},
				"AP":      reader.Dict{"N": appearance},
			}),
		}
		w.Put(pageRefs[i], reader.Dict{
			"Type":     reader.Name("Page"),
			"Parent":   pagesRef,
			"Contents": content,
			"Annots":   annots,
			"AA":       reader.Dict{"O": reader.Dict{"S": reader.Name("JavaScript")}},
			"AF":       reader.Array{fileSpec},
		})
	}
	kids := make(reader.Array, len(pageRefs))
	for i, r := range pageRefs {
		kids[i] = r
	}
	w.Put(pagesRef, reader.Dict{
		"Type":     reader.Name("Pages"),
		"Kids":     kids,
		"Count":    reader.Integer(pages),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)},
	})

	// Bookmarks: one per page, one of them with a child, and one that already
	// pointed at nothing.
	outlineRoot := w.Reserve()
	items := make([]reader.Ref, pages+1)
	for i := range items {
		items[i] = w.Reserve()
	}
	for i := 0; i < pages; i++ {
		node := reader.Dict{
			"Title":  reader.String(fmt.Sprintf("chapter %d", i+1)),
			"Parent": outlineRoot,
			"Dest":   reader.Array{pageRefs[i], reader.Name("Fit")},
		}
		if i > 0 {
			node["Prev"] = items[i-1]
		}
		if i+1 < pages {
			node["Next"] = items[i+1]
		} else {
			node["Next"] = items[pages]
		}
		if i == 0 {
			child := w.Add(reader.Dict{
				"Title":  reader.String("a section"),
				"Parent": items[0],
				"A":      reader.Dict{"S": reader.Name("GoTo"), "D": reader.Array{pageRefs[pages-1], reader.Name("Fit")}},
			})
			node["First"] = child
			node["Last"] = child
			node["Count"] = reader.Integer(-1)
		}
		w.Put(items[i], node)
	}
	// The broken one, last in the chain.
	w.Put(items[pages], reader.Dict{
		"Title":  reader.String("broken"),
		"Parent": outlineRoot,
		"Prev":   items[pages-1],
		"Dest":   reader.Array{reader.Null{}, reader.Name("Fit")},
	})
	w.Put(outlineRoot, reader.Dict{
		"Type":  reader.Name("Outlines"),
		"First": items[0],
		"Last":  items[pages],
		"Count": reader.Integer(pages + 1),
	})

	// A named destination, in the modern place.
	names := w.Add(reader.Dict{
		"Dests": reader.Dict{
			"Names": reader.Array{reader.String("thestart"),
				reader.Array{pageRefs[0], reader.Name("Fit")}},
		},
	})
	root := w.Add(reader.Dict{
		"Type":       reader.Name("Catalog"),
		"Pages":      pagesRef,
		"Outlines":   outlineRoot,
		"Names":      names,
		"OpenAction": reader.Dict{"S": reader.Name("JavaScript"), "JS": reader.String("app.alert(1)")},
	})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return rich{bytes: out, pages: pages}
}

// annotsOfPage lists the annotations of one page of a written file.
func annotsOfPage(t *testing.T, d *reader.Document, page int) []reader.Dict {
	t.Helper()
	p, err := d.Page(page)
	if err != nil {
		t.Fatal(err)
	}
	o, err := d.Resolve(p.Get("Annots"))
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := reader.ToArray(o)
	if !ok {
		return nil
	}
	out := make([]reader.Dict, 0, len(arr))
	for _, e := range arr {
		r, err := d.Resolve(e)
		if err != nil {
			continue
		}
		if dict, ok := reader.ToDict(r); ok {
			out = append(out, dict)
		}
	}
	return out
}

// outlineTitles lists the bookmarks of a written file, depth first.
func outlineTitles(t *testing.T, d *reader.Document) []string {
	t.Helper()
	cat, err := d.Catalog()
	if err != nil {
		return nil
	}
	root, ok := d.GetDict(cat, "Outlines")
	if !ok {
		return nil
	}
	return walkOutline(t, d, root.Get("First"), 0)
}

func walkOutline(t *testing.T, d *reader.Document, entry reader.Object, depth int) []string {
	t.Helper()
	if depth > 8 {
		return nil
	}
	var out []string
	seen := map[int]bool{}
	for i := 0; i < 64; i++ {
		if ref, ok := entry.(reader.Ref); ok {
			if seen[ref.Num] {
				break
			}
			seen[ref.Num] = true
		}
		o, err := d.Resolve(entry)
		if err != nil {
			break
		}
		node, ok := reader.ToDict(o)
		if !ok {
			break
		}
		title, _ := reader.ToString(node.Get("Title"))
		out = append(out, string(title))
		out = append(out, walkOutline(t, d, node.Get("First"), depth+1)...)
		entry = node.Get("Next")
		if entry.Kind() == reader.KindNull {
			break
		}
	}
	return out
}
