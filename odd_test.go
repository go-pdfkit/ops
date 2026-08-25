package ops

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// oddPDF builds a one-page document with whatever page and catalogue entries a
// test wants, so shapes no producer should write can still be handed to the
// writer and shown not to break it.
func oddPDF(t *testing.T, build func(w *reader.Writer, page, catalog reader.Dict)) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Reserve()
	page := reader.Dict{
		"Type":     reader.Name("Page"),
		"Parent":   pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page 1")}),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)},
	}
	catalog := reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef}
	if build != nil {
		build(w, page, catalog)
	}
	w.Put(pageRef, page)
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(catalog)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// writeOdd opens an odd document, writes it, and returns the result.
func writeOdd(t *testing.T, b []byte, prepare func(*Doc)) *reader.Document {
	t.Helper()
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if prepare != nil {
		prepare(d)
	}
	back, _ := writeAndOpen(t, d)
	return back
}

func TestAnnotationsInShapesNobodyShouldWrite(t *testing.T) {
	cases := []struct {
		name   string
		annots func(w *reader.Writer) reader.Object
		want   int // how many annotations should survive
	}{
		{"not an array at all", func(w *reader.Writer) reader.Object { return reader.Integer(1) }, 0},
		{"an empty array", func(w *reader.Writer) reader.Object { return reader.Array{} }, 0},
		{"an entry that is not a dictionary", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Integer(3), reader.Name("x")}
		}, 0},
		{"an entry naming an object that is not there", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Ref{Num: 9999}}
		}, 0},
		{"one good one among them", func(w *reader.Writer) reader.Object {
			return reader.Array{reader.Integer(3), w.Add(reader.Dict{
				"Type": reader.Name("Annot"), "Subtype": reader.Name("Text"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
			})}
		}, 1},
	}
	for _, c := range cases {
		b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
			page["Annots"] = c.annots(w)
		})
		back := writeOdd(t, b, nil)
		if got := len(annotsOfPage(t, back, 1)); got != c.want {
			t.Errorf("%s: %d annotations survived, want %d", c.name, got, c.want)
		}
		// And flattening must not break either.
		writeOdd(t, b, func(d *Doc) { d.Flatten() })
	}
}

func TestDestinationsInShapesNobodyShouldWrite(t *testing.T) {
	cases := []struct {
		name string
		dest func(w *reader.Writer, pageRef reader.Ref) reader.Object
		kept bool
	}{
		{"an empty array", func(w *reader.Writer, p reader.Ref) reader.Object { return reader.Array{} }, false},
		{"a number where the page belongs", func(w *reader.Writer, p reader.Ref) reader.Object {
			return reader.Array{reader.Integer(0), reader.Name("Fit")}
		}, false},
		{"a reference to something that is not a page", func(w *reader.Writer, p reader.Ref) reader.Object {
			return reader.Array{w.Add(reader.Integer(7)), reader.Name("Fit")}
		}, false},
		{"a dictionary holding the array", func(w *reader.Writer, p reader.Ref) reader.Object {
			return reader.Dict{"D": reader.Array{p, reader.Name("Fit")}}
		}, true},
		{"a name that is not in the tree", func(w *reader.Writer, p reader.Ref) reader.Object {
			return reader.Name("nowhere")
		}, false},
		{"a string that is not in the tree", func(w *reader.Writer, p reader.Ref) reader.Object {
			return reader.String("nowhere")
		}, false},
	}
	for _, c := range cases {
		b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
			// The page's own reference is the one the writer reserved second.
			pageRef := reader.Ref{Num: 2}
			page["Annots"] = reader.Array{w.Add(reader.Dict{
				"Type": reader.Name("Annot"), "Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
				"Dest": c.dest(w, pageRef),
			})}
		})
		back := writeOdd(t, b, nil)
		annots := annotsOfPage(t, back, 1)
		if len(annots) != 1 {
			t.Fatalf("%s: the annotation itself was lost", c.name)
		}
		got := annots[0].Get("Dest").Kind() != reader.KindNull
		if got != c.kept {
			t.Errorf("%s: destination kept = %v, want %v", c.name, got, c.kept)
		}
	}
}

func TestNamedDestinationsInBothPlaces(t *testing.T) {
	// The old place: a plain dictionary under the catalogue's /Dests.
	b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		pageRef := reader.Ref{Num: 2}
		catalog["Dests"] = reader.Dict{"here": reader.Array{pageRef, reader.Name("Fit")}}
		page["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Link"),
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
			"Dest": reader.Name("here"),
		})}
	})
	back := writeOdd(t, b, nil)
	if annots := annotsOfPage(t, back, 1); annots[0].Get("Dest").Kind() == reader.KindNull {
		t.Error("a destination named in the old place was not found")
	}

	// The modern place, with the entry a level down in the tree.
	b = oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		pageRef := reader.Ref{Num: 2}
		leaf := w.Add(reader.Dict{
			"Names": reader.Array{reader.String("deep"), reader.Array{pageRef, reader.Name("Fit")}},
		})
		catalog["Names"] = reader.Dict{"Dests": reader.Dict{
			"Kids": reader.Array{reader.Integer(3), leaf},
		}}
		page["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Link"),
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
			"Dest": reader.String("deep"),
		})}
	})
	back = writeOdd(t, b, nil)
	if annots := annotsOfPage(t, back, 1); annots[0].Get("Dest").Kind() == reader.KindNull {
		t.Error("a destination a level down in the tree was not found")
	}
}

func TestNameTreesInShapesNobodyShouldWrite(t *testing.T) {
	trees := []reader.Object{
		reader.Dict{},                                                            // nothing at all
		reader.Dict{"Names": reader.Integer(1)},                                  // not an array
		reader.Dict{"Names": reader.Array{reader.String("x")}},                   // an odd number of entries
		reader.Dict{"Names": reader.Array{reader.Integer(1), reader.Integer(2)}}, // a key that is not a string
		reader.Dict{"Kids": reader.Integer(1)},                                   // kids that are not a list
		reader.Dict{"Kids": reader.Array{reader.Integer(1)}},                     // a kid that is not a node
	}
	for i, tree := range trees {
		b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
			catalog["Names"] = reader.Dict{"Dests": tree}
			page["Annots"] = reader.Array{w.Add(reader.Dict{
				"Type": reader.Name("Annot"), "Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
				"Dest": reader.String("wanted"),
			})}
		})
		back := writeOdd(t, b, nil)
		annots := annotsOfPage(t, back, 1)
		if len(annots) != 1 {
			t.Fatalf("tree %d: the annotation was lost", i)
		}
		if annots[0].Get("Dest").Kind() != reader.KindNull {
			t.Errorf("tree %d: a destination was invented", i)
		}
	}
}

func TestActionsInShapesNobodyShouldWrite(t *testing.T) {
	cases := []struct {
		name   string
		action reader.Object
		kept   bool
	}{
		{"not a dictionary", reader.Integer(1), false},
		{"no kind at all", reader.Dict{"X": reader.Integer(1)}, true},
		{"a kind nobody has heard of", reader.Dict{"S": reader.Name("Wat")}, true},
		{"a jump to nowhere", reader.Dict{"S": reader.Name("GoTo")}, false},
		{"an action with a next", reader.Dict{"S": reader.Name("URI"), "Next": reader.Integer(1)}, true},
	}
	for _, c := range cases {
		b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
			page["Annots"] = reader.Array{w.Add(reader.Dict{
				"Type": reader.Name("Annot"), "Subtype": reader.Name("Link"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
				"A":    c.action,
			})}
		})
		back := writeOdd(t, b, nil)
		annots := annotsOfPage(t, back, 1)
		if len(annots) != 1 {
			t.Fatalf("%s: the annotation was lost", c.name)
		}
		got := annots[0].Get("A").Kind() != reader.KindNull
		if got != c.kept {
			t.Errorf("%s: action kept = %v, want %v", c.name, got, c.kept)
		}
		if c.name == "an action with a next" {
			action, _ := back.GetDict(annots[0], "A")
			if action.Get("Next").Kind() != reader.KindNull {
				t.Error("a chained action survived")
			}
		}
	}
}

func TestAppearancesInShapesNobodyShouldWrite(t *testing.T) {
	cases := []struct {
		name  string
		annot func(w *reader.Writer) reader.Dict
		draws int
	}{
		{"no appearance", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Square"),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}}
		}, 0},
		{"an appearance that is not a dictionary", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Square"), "AP": reader.Integer(1),
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}}
		}, 0},
		{"a normal appearance that is neither stream nor states", func(w *reader.Writer) reader.Dict {
			return reader.Dict{"Subtype": reader.Name("Square"), "AP": reader.Dict{"N": reader.Integer(1)},
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}}
		}, 0},
		{"an appearance with no bounding box", func(w *reader.Writer) reader.Dict {
			ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form")}, Raw: []byte("x")})
			return reader.Dict{"Subtype": reader.Name("Square"), "AP": reader.Dict{"N": ap},
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}}
		}, 0},
		{"no rectangle", func(w *reader.Writer) reader.Dict {
			ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}},
				Raw: []byte("x")})
			return reader.Dict{"Subtype": reader.Name("Square"), "AP": reader.Dict{"N": ap}}
		}, 0},
		{"a matrix that is not numbers", func(w *reader.Writer) reader.Dict {
			ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
				"BBox":   reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
				"Matrix": reader.Array{reader.Name("x"), reader.Integer(0), reader.Integer(0), reader.Integer(1), reader.Integer(0), reader.Integer(0)}},
				Raw: []byte("x")})
			return reader.Dict{"Subtype": reader.Name("Square"), "AP": reader.Dict{"N": ap},
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}}
		}, 0},
		{"a matrix that turns it", func(w *reader.Writer) reader.Dict {
			ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
				"BBox":   reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(5)},
				"Matrix": reader.Array{reader.Integer(0), reader.Integer(1), reader.Integer(-1), reader.Integer(0), reader.Integer(0), reader.Integer(0)}},
				Raw: []byte("x")})
			return reader.Dict{"Subtype": reader.Name("Square"), "AP": reader.Dict{"N": ap},
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(10)}}
		}, 1},
		{"a box with no width", func(w *reader.Writer) reader.Dict {
			ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}},
				Raw: []byte("x")})
			return reader.Dict{"Subtype": reader.Name("Square"), "AP": reader.Dict{"N": ap},
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(0), reader.Integer(5)}}
		}, 0},
		{"a flag that is not a number", func(w *reader.Writer) reader.Dict {
			ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
				"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}},
				Raw: []byte("x")})
			return reader.Dict{"Subtype": reader.Name("Square"), "F": reader.Name("x"),
				"AP":   reader.Dict{"N": ap},
				"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)}}
		}, 1},
	}
	for _, c := range cases {
		b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
			page["Annots"] = reader.Array{w.Add(c.annot(w))}
		})
		back := writeOdd(t, b, func(d *Doc) { d.Flatten() })
		content, err := back.PageContent(1)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := strings.Count(string(content), " Do"); got != c.draws {
			t.Errorf("%s: draws %d, want %d", c.name, got, c.draws)
		}
	}
}

func TestBookmarksInShapesNobodyShouldWrite(t *testing.T) {
	cases := []struct {
		name  string
		build func(w *reader.Writer, catalog reader.Dict)
		want  int
	}{
		{"an outline root with nothing under it", func(w *reader.Writer, catalog reader.Dict) {
			catalog["Outlines"] = reader.Dict{"Type": reader.Name("Outlines")}
		}, 0},
		{"an outline root that is not a dictionary", func(w *reader.Writer, catalog reader.Dict) {
			catalog["Outlines"] = reader.Integer(1)
		}, 0},
		{"an entry that is not a dictionary", func(w *reader.Writer, catalog reader.Dict) {
			catalog["Outlines"] = reader.Dict{"First": reader.Integer(1)}
		}, 0},
		{"an entry with no title", func(w *reader.Writer, catalog reader.Dict) {
			catalog["Outlines"] = reader.Dict{"First": w.Add(reader.Dict{
				"Dest": reader.Array{reader.Ref{Num: 2}, reader.Name("Fit")}})}
		}, 1},
		{"an action that is not a jump", func(w *reader.Writer, catalog reader.Dict) {
			catalog["Outlines"] = reader.Dict{"First": w.Add(reader.Dict{
				"Title": reader.String("x"),
				"A":     reader.Dict{"S": reader.Name("URI"), "URI": reader.String("https://example.invalid/")}})}
		}, 1},
		{"an action that is not a dictionary", func(w *reader.Writer, catalog reader.Dict) {
			catalog["Outlines"] = reader.Dict{"First": w.Add(reader.Dict{
				"Title": reader.String("x"), "A": reader.Integer(1)})}
		}, 1},
		{"a chain that loops back on itself", func(w *reader.Writer, catalog reader.Dict) {
			node := w.Reserve()
			w.Put(node, reader.Dict{"Title": reader.String("x"), "Next": node,
				"Dest": reader.Array{reader.Ref{Num: 2}, reader.Name("Fit")}})
			catalog["Outlines"] = reader.Dict{"First": node}
		}, 1},
	}
	for _, c := range cases {
		b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) { c.build(w, catalog) })
		back := writeOdd(t, b, nil)
		if got := len(outlineTitles(t, back)); got != c.want {
			t.Errorf("%s: %d bookmarks, want %d", c.name, got, c.want)
		}
	}
}

func TestSanitizeDropsFilesTravellingWithAnAnnotation(t *testing.T) {
	// An annotation that is not one of the dangerous kinds but still carries
	// an embedded file, and one carrying an associated file.
	b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		embedded := w.Add(&reader.Stream{
			Dict: reader.Dict{"Type": reader.Name("EmbeddedFile")},
			Raw:  []byte("payload"),
		})
		page["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Square"),
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
			"EF":   reader.Dict{"F": embedded},
			"AF":   reader.Array{embedded},
		})}
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	d.Sanitize()
	back, out := writeAndOpen(t, d)
	if bytes.Contains(out, []byte("/EmbeddedFile")) {
		t.Error("the embedded file survived")
	}
	annots := annotsOfPage(t, back, 1)
	if len(annots) != 1 {
		t.Fatalf("the annotation itself was dropped")
	}
	for _, key := range []reader.Name{"EF", "AF"} {
		if annots[0].Get(key).Kind() != reader.KindNull {
			t.Errorf("/%s survived", key)
		}
	}
}

func TestANameTreeThatLoopsBackOnItself(t *testing.T) {
	b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		node := w.Reserve()
		w.Put(node, reader.Dict{"Kids": reader.Array{node}})
		catalog["Names"] = reader.Dict{"Dests": reader.Dict{"Kids": reader.Array{node}}}
		page["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Link"),
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
			"Dest": reader.String("wanted"),
		})}
	})
	back := writeOdd(t, b, nil)
	annots := annotsOfPage(t, back, 1)
	if len(annots) != 1 || annots[0].Get("Dest").Kind() != reader.KindNull {
		t.Errorf("a cycle in the name tree produced %v", annots)
	}
}

func TestAnAppearanceWithNoHeight(t *testing.T) {
	b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
			"BBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(0)}},
			Raw: []byte("x")})
		page["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Square"),
			"AP":   reader.Dict{"N": ap},
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
		})}
	})
	back := writeOdd(t, b, func(d *Doc) { d.Flatten() })
	content, err := back.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), " Do") {
		t.Error("a box with no height was drawn anyway")
	}
}

func TestAnAppearanceWithNoWidth(t *testing.T) {
	b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		// A box with no width once its own matrix has flattened it.
		ap := w.Add(&reader.Stream{Dict: reader.Dict{"Subtype": reader.Name("Form"),
			"BBox":   reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
			"Matrix": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(0), reader.Integer(1), reader.Integer(0), reader.Integer(0)}},
			Raw: []byte("x")})
		page["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Square"),
			"AP":   reader.Dict{"N": ap},
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
		})}
	})
	back := writeOdd(t, b, func(d *Doc) { d.Flatten() })
	content, err := back.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), " Do") {
		t.Error("a box with no width was drawn anyway")
	}
}

func TestBookmarksNestedDeeperThanTheWalkGoes(t *testing.T) {
	b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		pageRef := reader.Ref{Num: 2}
		// A chain of parents each holding the next, deeper than the bound.
		refs := make([]reader.Ref, maxOutlineDepth+4)
		for i := range refs {
			refs[i] = w.Reserve()
		}
		for i, r := range refs {
			node := reader.Dict{
				"Title": reader.String(fmt.Sprintf("level %d", i)),
				"Dest":  reader.Array{pageRef, reader.Name("Fit")},
			}
			if i+1 < len(refs) {
				node["First"] = refs[i+1]
			}
			w.Put(r, node)
		}
		catalog["Outlines"] = reader.Dict{"First": refs[0]}
	})
	back := writeOdd(t, b, nil)
	got := len(outlineTitles(t, back))
	if got == 0 || got > maxOutlineDepth+1 {
		t.Errorf("%d bookmarks came through a tree %d deep", got, maxOutlineDepth+4)
	}
}

func TestAnAppearanceStateThatIsNotAStream(t *testing.T) {
	// The state dictionary is only checked for the name; what it holds may be
	// anything at all.
	b := oddPDF(t, func(w *reader.Writer, page, catalog reader.Dict) {
		page["Annots"] = reader.Array{w.Add(reader.Dict{
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"),
			"AS":   reader.Name("On"),
			"AP":   reader.Dict{"N": reader.Dict{"On": reader.Integer(7)}},
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(5), reader.Integer(5)},
		})}
	})
	back := writeOdd(t, b, func(d *Doc) { d.Flatten() })
	content, err := back.PageContent(1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), " Do") {
		t.Error("something that is not a stream was drawn")
	}
}
