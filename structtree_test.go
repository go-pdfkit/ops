package ops

import (
	"testing"

	"github.com/go-pdfkit/reader"
)

// A tagged is a document being written whose pages carry marked content and
// whose catalogue carries a structure tree over them.
type tagged struct {
	w     *reader.Writer
	pages reader.Ref
	page  []reader.Ref
	// stream is a form XObject drawn on the first page, holding a mark of its
	// own and saying under what number that mark is filed.
	stream reader.Ref
	// drawn is what the first page's resources name.
	drawn reader.Dict
	// loose is a form XObject no page draws.
	loose reader.Ref
	// annot is one annotation per page, in page order.
	annot []reader.Ref
	// notAPage is an object an element can wrongly claim to be on.
	notAPage reader.Ref
}

// marked writes a content stream with the given mark numbers in it.
func marked(w *reader.Writer, nums ...int) reader.Object {
	var raw []byte
	for _, n := range nums {
		raw = append(raw, "/P <</MCID "...)
		raw = append(raw, []byte{byte('0' + n)}...)
		raw = append(raw, ">> BDC BT ET EMC\n"...)
	}
	return w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: raw})
}

// newTagged lays out three pages, each with one mark of its own, the first of
// them also drawing a form XObject with a mark inside it, and each with one
// annotation on it. The pages are written by finish, so that a test can add
// something else for the first page to draw first.
func newTagged() *tagged {
	w := reader.NewWriter("1.7")
	g := &tagged{w: w, pages: w.Reserve(), drawn: reader.Dict{}}
	g.notAPage = w.Add(reader.Dict{"Type": reader.Name("Whatever")})
	g.stream = w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"StructParents": reader.Integer(7),
		"BBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(9), reader.Integer(9)},
	}, Raw: []byte("/P <</MCID 4>> BDC BT ET EMC\n")})
	g.loose = w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"StructParents": reader.Integer(3),
	}, Raw: []byte("/P <</MCID 5>> BDC BT ET EMC\n")})
	g.drawn["Fm0"] = g.stream
	for i := 0; i < 3; i++ {
		g.page = append(g.page, w.Reserve())
	}
	for i, ref := range g.page {
		g.annot = append(g.annot, w.Add(reader.Dict{"Type": reader.Name("Annot"),
			"Subtype": reader.Name("Link"), "P": ref,
			"StructParent": reader.Integer(int64(11 + i)),
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(9), reader.Integer(9)}}))
	}
	return g
}

// draw puts one more form XObject in the first page's resources.
func (g *tagged) draw(name reader.Name, ref reader.Ref) { g.drawn[name] = ref }

// writePages puts the three pages down.
func (g *tagged) writePages() {
	for i, ref := range g.page {
		page := reader.Dict{"Type": reader.Name("Page"), "Parent": g.pages,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(99), reader.Integer(99)},
			"Annots":        reader.Array{g.annot[i]},
			"StructParents": reader.Integer(int64(i)),
			"Contents":      marked(g.w, 0, 1),
		}
		if i == 0 {
			page["Resources"] = reader.Dict{"XObject": g.drawn}
		}
		g.w.Put(ref, page)
	}
	g.w.Put(g.pages, reader.Dict{"Type": reader.Name("Pages"),
		"Kids":  reader.Array{g.page[0], g.page[1], g.page[2]},
		"Count": reader.Integer(3)})
}

// finish writes the file with the given structure tree over the pages.
func (g *tagged) finish(t *testing.T, root reader.Dict) []byte {
	t.Helper()
	g.writePages()
	catalog := reader.Dict{"Type": reader.Name("Catalog"), "Pages": g.pages,
		"MarkInfo": reader.Dict{"Marked": reader.Bool(true)}}
	if root != nil {
		catalog["StructTreeRoot"] = g.w.Add(root)
	}
	out, err := g.w.Finish(reader.Dict{"Root": g.w.Add(catalog)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// elem writes one structure element.
func (g *tagged) elem(kind reader.Name, extra reader.Dict, kids ...reader.Object) reader.Ref {
	d := reader.Dict{"Type": reader.Name("StructElem"), "S": kind}
	for k, v := range extra {
		d[k] = v
	}
	switch len(kids) {
	case 0:
	case 1:
		d["K"] = kids[0]
	default:
		d["K"] = reader.Array(kids)
	}
	return g.w.Add(d)
}

// wholeTree is the structure tree the tests measure against: every shape a
// real one has, over three pages.
func wholeTree(g *tagged) reader.Dict {
	sect := g.elem("Sect", reader.Dict{"Pg": g.page[0]},
		reader.Integer(0),
		reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(1)},
		reader.Dict{"Type": reader.Name("OBJR"), "Obj": g.annot[0], "Pg": g.page[0]},
	)
	// A mark inside the form XObject the first page draws.
	inStream := g.elem("H1", reader.Dict{"Pg": g.page[0], "ID": reader.String("aaa")},
		reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(4),
			"Stm": g.stream, "Pg": g.page[0]})
	// An empty table cell: the shape of the document, not a claim about it.
	empty := g.elem("TD", reader.Dict{"Pg": g.page[0]})
	second := g.elem("P", reader.Dict{"Pg": g.page[1], "ID": reader.String("bbb")},
		reader.Integer(0),
		reader.Dict{"Type": reader.Name("OBJR"), "Obj": g.annot[1]},
	)
	emptyGone := g.elem("TD", reader.Dict{"Pg": g.page[1]})
	// An element with no page of its own, whose child has one.
	third := g.elem("Div", nil,
		g.elem("P", reader.Dict{"Pg": g.page[2]}, reader.Integer(1)))
	return reader.Dict{
		"Type":     reader.Name("StructTreeRoot"),
		"RoleMap":  reader.Dict{"Sect": reader.Name("Div")},
		"ClassMap": reader.Dict{"warm": reader.Dict{"O": reader.Name("Layout")}},
		"IDTree":   reader.Dict{"Names": reader.Array{}},
		"K": g.elem("Document", nil,
			sect, inStream, empty, second, emptyGone, third),
	}
}

// treeOf reads back the structure tree of a rebuilt document.
func treeOf(t *testing.T, d *reader.Document, catalog reader.Dict) reader.Dict {
	t.Helper()
	root, ok := d.GetDict(catalog, "StructTreeRoot")
	if !ok {
		t.Fatal("the rebuilt document has no structure tree")
	}
	return root
}

// topOf lists the elements at the top of a rebuilt tree.
func topOf(t *testing.T, d *reader.Document, root reader.Dict) []reader.Object {
	t.Helper()
	kids := kidsOf(t, d, root)
	if len(kids) != 1 {
		t.Fatalf("the tree has %d elements at the top, wanted one", len(kids))
	}
	doc, ok := reader.ToDict(kids[0])
	if !ok {
		t.Fatalf("the top of the tree is %v", kids[0])
	}
	return kidsOf(t, d, doc)
}

// namesOf lists the kinds of a handful of elements, for a failure message.
func namesOf(list []reader.Object) []string {
	var out []string
	for _, k := range list {
		e, _ := reader.ToDict(k)
		s, _ := reader.ToName(e.Get("S"))
		out = append(out, string(s))
	}
	return out
}

// kidsOf lists one element's children, resolved.
func kidsOf(t *testing.T, d *reader.Document, elem reader.Dict) []reader.Object {
	t.Helper()
	entry, _ := d.Resolve(elem.Get("K"))
	if arr, ok := reader.ToArray(entry); ok {
		out := make([]reader.Object, 0, len(arr))
		for _, e := range arr {
			got, _ := d.Resolve(e)
			out = append(out, got)
		}
		return out
	}
	return []reader.Object{entry}
}

// numsOf reads a rebuilt parent tree into a map.
func numsOf(t *testing.T, d *reader.Document, root reader.Dict) map[int64]reader.Object {
	t.Helper()
	out := map[int64]reader.Object{}
	pt, ok := d.GetDict(root, "ParentTree")
	if !ok {
		return out
	}
	arr, ok := reader.ToArray(pt.Get("Nums"))
	if !ok {
		t.Fatal("the parent tree has no /Nums")
	}
	last := int64(-1)
	for i := 0; i+1 < len(arr); i += 2 {
		key, ok := reader.ToInt(arr[i])
		if !ok {
			t.Fatalf("the parent tree is keyed by %v", arr[i])
		}
		if key <= last {
			t.Errorf("the parent tree's keys are out of order: %d after %d", key, last)
		}
		last = key
		got, _ := d.Resolve(arr[i+1])
		out[key] = got
	}
	return out
}

// marksOn lists the mark numbers in one page's content, so that what the tree
// claims can be held against what the page actually draws.
func marksOn(t *testing.T, d *reader.Document, page int) map[int64]bool {
	t.Helper()
	ops, err := d.PageOperations(page)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]bool{}
	for _, op := range ops {
		if op.Operator != "BDC" || len(op.Operands) < 2 {
			continue
		}
		props, ok := reader.ToDict(op.Operands[1])
		if !ok {
			continue
		}
		if n, ok := reader.ToInt(props.Get("MCID")); ok {
			out[n] = true
		}
	}
	return out
}

func TestARebuiltDocumentKeepsItsStructure(t *testing.T) {
	g := newTagged()
	src := g.finish(t, wholeTree(g))
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	root := treeOf(t, d, catalog)

	for _, key := range []reader.Name{"RoleMap", "ClassMap", "ParentTree", "IDTree"} {
		if root.Get(key).Kind() == reader.KindNull {
			t.Errorf("the rebuilt tree lost /%s", key)
		}
	}
	kids := topOf(t, d, root)
	if len(kids) != 6 {
		t.Fatalf("the document element has %d children (%v), wanted six", len(kids), namesOf(kids))
	}
	// The first element's own page must be a page of this document, and its
	// marks must be marks the page really draws.
	sect, _ := reader.ToDict(kids[0])
	first, _ := d.PageRef(1)
	if pg, ok := sect.Get("Pg").(reader.Ref); !ok || pg != first {
		t.Errorf("the first element is on %v, wanted page one at %v", sect.Get("Pg"), first)
	}
	drawn := marksOn(t, d, 1)
	for _, kid := range kidsOf(t, d, sect) {
		if n, ok := reader.ToInt(kid); ok && !drawn[n] {
			t.Errorf("the tree names mark %d, which page one does not draw", n)
		}
	}
	// The page says under what number its marks are filed, and the number
	// tree, looked up under it, says which element owns each mark.
	nums := numsOf(t, d, root)
	page, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := reader.ToInt(page.Get("StructParents"))
	if !ok {
		t.Fatal("page one does not say where its structure is filed")
	}
	arr, ok := reader.ToArray(nums[key])
	if !ok {
		t.Fatalf("the number tree holds %v under page one's key %d", nums[key], key)
	}
	if len(arr) < 2 {
		t.Fatalf("page one's entry has %d places, wanted at least two", len(arr))
	}
	for _, mark := range []int64{0, 1} {
		if _, ok := arr[mark].(reader.Ref); !ok {
			t.Errorf("mark %d of page one is owned by %v", mark, arr[mark])
		}
	}
	// The annotation is filed under a number of its own, and that entry names
	// one element rather than an array.
	annots, _ := reader.ToArray(page.Get("Annots"))
	if len(annots) != 1 {
		t.Fatalf("page one has %d annotations", len(annots))
	}
	annot, _ := d.GetDict(page, "Annots")
	_ = annot
	first0, _ := reader.ToDict(mustGet(t, d, annots[0]))
	akey, ok := reader.ToInt(first0.Get("StructParent"))
	if !ok {
		t.Fatal("the annotation does not say where it is filed")
	}
	if akey == key {
		t.Errorf("the annotation and the page are filed under the same number %d", akey)
	}
	if _, ok := nums[akey].(reader.Dict); !ok {
		if _, isRef := nums[akey].(reader.Ref); !isRef {
			t.Errorf("the annotation's entry is %v, wanted an element", nums[akey])
		}
	}
	if next, ok := reader.ToInt(root.Get("ParentTreeNextKey")); !ok || next <= akey {
		t.Errorf("the next free number is %v, and %d is taken", root.Get("ParentTreeNextKey"), akey)
	}
	// The identifiers of the elements that survived, in order.
	ids, ok := d.GetDict(root, "IDTree")
	if !ok {
		t.Fatal("the rebuilt tree has no /IDTree")
	}
	names, ok := reader.ToArray(ids.Get("Names"))
	if !ok || len(names) != 4 {
		t.Fatalf("the identifier tree holds %v", ids.Get("Names"))
	}
	if s, _ := reader.ToString(names[0]); string(s) != "aaa" {
		t.Errorf("the first identifier is %q", s)
	}
	if s, _ := reader.ToString(names[2]); string(s) != "bbb" {
		t.Errorf("the second identifier is %q", s)
	}
}

func TestStructureFollowsThePagesThatSurvive(t *testing.T) {
	g := newTagged()
	src := g.finish(t, wholeTree(g))
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Select("1,3")) })
	root := treeOf(t, d, catalog)
	// The element on the dropped page goes, and so does the empty cell that
	// sat on it: four of the six are left.
	kids := topOf(t, d, root)
	if len(kids) != 4 {
		t.Fatalf("%d children left (%v), wanted four", len(kids), namesOf(kids))
	}
	// Every page named by what is left is a page of this document, and the
	// numbers have been handed out afresh.
	nums := numsOf(t, d, root)
	for i := 1; i <= d.PageCount(); i++ {
		page, err := d.Page(i)
		if err != nil {
			t.Fatal(err)
		}
		key, ok := reader.ToInt(page.Get("StructParents"))
		if !ok {
			t.Fatalf("page %d says nothing about where its structure is filed", i)
		}
		arr, ok := reader.ToArray(nums[key])
		if !ok {
			t.Fatalf("page %d's key %d holds %v", i, key, nums[key])
		}
		drawn := marksOn(t, d, i)
		for mark, owner := range arr {
			if owner.Kind() == reader.KindNull {
				continue
			}
			if !drawn[int64(mark)] {
				t.Errorf("page %d is filed as owning mark %d, which it does not draw", i, mark)
			}
		}
	}
	// The identifier of the element that went is not carried: it would name
	// nothing.
	ids, _ := d.GetDict(root, "IDTree")
	names, _ := reader.ToArray(ids.Get("Names"))
	if len(names) != 2 {
		t.Fatalf("the identifier tree holds %v, wanted only the surviving one", names)
	}
}

func TestADuplicatedPageCarriesNoStructure(t *testing.T) {
	// An element names one page, so only the first copy can be the one the
	// structure describes. The second must not be left carrying a number that
	// would send a reader to the first copy's elements.
	g := newTagged()
	src := g.finish(t, wholeTree(g))
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Select("1,1,2,3")) })
	treeOf(t, d, catalog)
	first, err := d.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Get("StructParents").Kind() == reader.KindNull {
		t.Error("the first copy of the page carries no structure")
	}
	second, err := d.Page(2)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Get("StructParents"); got.Kind() != reader.KindNull {
		t.Errorf("the second copy of the page is filed under %v", got)
	}
}

func TestStructureIsNotCarriedAcrossFiles(t *testing.T) {
	g := newTagged()
	src := g.finish(t, wholeTree(g))
	one, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	one.Append(two)
	out, err := one.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := d.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.Get("StructTreeRoot"); got.Kind() != reader.KindNull {
		t.Errorf("two files were given one structure tree: %v", got)
	}
	// And no page may be left saying it is filed in a tree that is not there.
	for i := 1; i <= d.PageCount(); i++ {
		page, err := d.Page(i)
		if err != nil {
			t.Fatal(err)
		}
		if got := page.Get("StructParents"); got.Kind() != reader.KindNull {
			t.Errorf("page %d still says it is filed under %v", i, got)
		}
	}
}

func TestStructureGoesWithTheAnnotationsWhenTheyGo(t *testing.T) {
	g := newTagged()
	src := g.finish(t, wholeTree(g))
	for _, c := range []struct {
		name string
		do   func(*Doc)
	}{
		{"flattened", func(doc *Doc) { doc.Flatten() }},
		{"without annotations", func(doc *Doc) { doc.RemoveAnnotations() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, catalog := rebuilt(t, src, c.do)
			root := treeOf(t, d, catalog)
			for _, kid := range topOf(t, d, root) {
				elem, _ := reader.ToDict(kid)
				for _, own := range kidsOf(t, d, elem) {
					if o, ok := reader.ToDict(own); ok {
						if kind, _ := reader.ToName(o.Get("Type")); kind == "OBJR" {
							t.Errorf("the tree still points at an annotation: %v", o)
						}
					}
				}
			}
		})
	}
}

func TestAStructureTreeWithNothingInItIsNotWritten(t *testing.T) {
	for _, c := range []struct {
		name string
		root func(*tagged) reader.Dict
	}{
		{"no tree at all", func(*tagged) reader.Dict { return nil }},
		{"a root with no children", func(*tagged) reader.Dict {
			return reader.Dict{"Type": reader.Name("StructTreeRoot"),
				"RoleMap": reader.Dict{"Sect": reader.Name("Div")}}
		}},
		{"a root whose only child is a mark", func(g *tagged) reader.Dict {
			return reader.Dict{"Type": reader.Name("StructTreeRoot"),
				"K": reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(0)}}
		}},
		{"a root whose only child is an annotation", func(g *tagged) reader.Dict {
			return reader.Dict{"Type": reader.Name("StructTreeRoot"),
				"K": reader.Dict{"Type": reader.Name("OBJR"), "Obj": g.annot[0]}}
		}},
		{"a root whose children are not elements", func(g *tagged) reader.Dict {
			return reader.Dict{"Type": reader.Name("StructTreeRoot"),
				"K": reader.Array{reader.Name("nonsense"), reader.Integer(3)}}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newTagged()
			src := g.finish(t, c.root(g))
			d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
			if got := catalog.Get("StructTreeRoot"); got.Kind() != reader.KindNull {
				t.Errorf("a tree with nothing in it was written as %v", got)
			}
			for i := 1; i <= d.PageCount(); i++ {
				page, _ := d.Page(i)
				if got := page.Get("StructParents"); got.Kind() != reader.KindNull {
					t.Errorf("page %d says it is filed under %v", i, got)
				}
			}
		})
	}
}

func TestAnElementThatNamesNothingIsDropped(t *testing.T) {
	g := newTagged()
	// Each of these describes nothing that is still there, and each must go
	// rather than be written naming something that is not.
	loop := g.w.Reserve()
	g.w.Put(loop, reader.Dict{"Type": reader.Name("StructElem"), "S": reader.Name("Loop"),
		"Pg": g.page[0], "K": loop})
	deep := g.page[0]
	chain := reader.Object(reader.Integer(0))
	for i := 0; i < maxStructDepth+4; i++ {
		chain = g.elem("Deep", reader.Dict{"Pg": deep}, chain)
	}
	root := reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": reader.Array{
			// A page written into the element rather than referred to.
			g.elem("A", reader.Dict{"Pg": reader.Dict{"Type": reader.Name("Page")}},
				reader.Integer(0)),
			// A page that is not a page of this document.
			g.elem("B", reader.Dict{"Pg": g.notAPage}, reader.Integer(0)),
			// An empty element on a page written into it, which cannot be
			// placed and so is not the shape of anything.
			g.elem("C", reader.Dict{"Pg": g.notAPage}),
			// A mark with no page anywhere above it.
			g.elem("D", nil, reader.Integer(0)),
			// Marks no page could have.
			g.elem("E", reader.Dict{"Pg": g.page[0]},
				reader.Integer(-1), reader.Integer(maxMarksPerPage)),
			// A child that is neither a mark nor a dictionary.
			g.elem("F", reader.Dict{"Pg": g.page[0]}, reader.Name("nonsense")),
			// A marked-content reference with no mark number.
			g.elem("G", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("MCR")}),
			// A reference to an annotation this document does not have.
			g.elem("H", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("OBJR"), "Obj": g.notAPage}),
			// An element that is its own child.
			loop,
			// A chain deeper than anything a document has.
			chain,
			// One that does survive, so that there is a tree to look at.
			g.elem("Keeper", reader.Dict{"Pg": g.page[0]}, reader.Integer(1)),
		}}
	src := g.finish(t, root)
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	tree := treeOf(t, d, catalog)
	kids := kidsOf(t, d, tree)
	if len(kids) != 1 || namesOf(kids)[0] != "Keeper" {
		t.Fatalf("%d elements survived (%v), wanted only the keeper", len(kids), namesOf(kids))
	}
}

func TestAMarkInsideAStreamThePageDraws(t *testing.T) {
	g := newTagged()
	notAStream := g.w.Add(reader.Dict{"Type": reader.Name("Whatever")})
	keyless := g.w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form")},
		Raw: []byte("/P <</MCID 4>> BDC BT ET EMC\n")})
	root := reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": reader.Array{
			// Inside the form XObject the first page draws: carried, and filed
			// under the number that stream carries.
			g.elem("Kept", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(4),
					"Stm": g.stream}),
			// Inside a stream no page draws.
			g.elem("Loose", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(5),
					"Stm": g.loose}),
			// A stream written into the reference rather than referred to.
			g.elem("Direct", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(4),
					"Stm": reader.Dict{}}),
			// Something that is not a stream at all.
			g.elem("NotAStream", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(4),
					"Stm": notAStream}),
			// A stream that says nothing about where its marks are filed.
			g.elem("Keyless", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(4),
					"Stm": keyless}),
			// A mark inside a stream, with no page anywhere above it.
			g.elem("Unplaced", nil,
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(4),
					"Stm": g.stream}),
		}}
	// The first page has to draw the keyless stream too, or it would be
	// refused for that rather than for having no key.
	g.draw("Fm1", keyless)
	src := g.finish(t, root)
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	tree := treeOf(t, d, catalog)
	kids := kidsOf(t, d, tree)
	if len(kids) != 1 {
		t.Fatalf("%d elements survived (%v), wanted only the one whose stream the page draws",
			len(kids), namesOf(kids))
	}
	kept, _ := reader.ToDict(kids[0])
	mcr, ok := reader.ToDict(kidsOf(t, d, kept)[0])
	if !ok {
		t.Fatal("the mark inside the stream is gone")
	}
	stm, ok := mcr.Get("Stm").(reader.Ref)
	if !ok {
		t.Fatalf("the reference names %v rather than a stream of this file", mcr.Get("Stm"))
	}
	// The copy of the stream keeps the number it was filed under, so the
	// numbers handed to the pages must start above it.
	stream, ok := reader.ToStream(mustGet(t, d, stm))
	if !ok {
		t.Fatal("the stream the mark is in was not carried")
	}
	key, ok := reader.ToInt(stream.Dict.Get("StructParents"))
	if !ok {
		t.Fatal("the copied stream says nothing about where its marks are filed")
	}
	nums := numsOf(t, d, tree)
	arr, ok := reader.ToArray(nums[key])
	if !ok || len(arr) != 5 {
		t.Fatalf("the stream's key %d holds %v", key, nums[key])
	}
	if _, ok := arr[4].(reader.Ref); !ok {
		t.Errorf("mark four of the stream is owned by %v", arr[4])
	}
	for i := 1; i <= d.PageCount(); i++ {
		page, _ := d.Page(i)
		if got, ok := reader.ToInt(page.Get("StructParents")); ok && got <= key {
			t.Errorf("page %d was handed number %d, which the stream already holds", i, got)
		}
	}
}

func TestAnElementWhoseOwnPageGoesButWhoseContentStays(t *testing.T) {
	// Its own page is gone and some of its content is not. Saying nothing
	// about a page would leave it inheriting whatever is above it, which is a
	// page it is not on — the mistake worth avoiding, since a reader trusts it.
	g := newTagged()
	root := reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": reader.Array{
			g.elem("Displaced", reader.Dict{"Pg": g.page[1]},
				g.elem("P", reader.Dict{"Pg": g.page[0]}, reader.Integer(0))),
			// One whose only content is an annotation on a page that stays.
			g.elem("Standing", reader.Dict{"Pg": g.page[1]},
				reader.Dict{"Type": reader.Name("OBJR"), "Obj": g.annot[0]}),
		}}
	src := g.finish(t, root)
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Select("1")) })
	tree := treeOf(t, d, catalog)
	kids := kidsOf(t, d, tree)
	if len(kids) != 2 {
		t.Fatalf("%d elements survived (%v), wanted both", len(kids), namesOf(kids))
	}
	only, _ := d.PageRef(1)
	displaced, _ := reader.ToDict(kids[0])
	if pg, ok := displaced.Get("Pg").(reader.Ref); !ok || pg != only {
		t.Errorf("the displaced element says it is on %v, wanted the one page left at %v",
			displaced.Get("Pg"), only)
	}
	standing, _ := reader.ToDict(kids[1])
	if got := standing.Get("Pg"); got.Kind() != reader.KindNull {
		t.Errorf("the element standing for an annotation claims page %v of its own", got)
	}
}

func TestMarksNamedTheLongWay(t *testing.T) {
	g := newTagged()
	root := reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": reader.Array{
			// A reference that says which page the mark is on itself.
			g.elem("Kept", nil,
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(1),
					"Pg": g.page[0]}),
			// One naming a mark no page could have.
			g.elem("Gone", nil,
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(-1),
					"Pg": g.page[0]}),
		}}
	src := g.finish(t, root)
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	tree := treeOf(t, d, catalog)
	kids := kidsOf(t, d, tree)
	if len(kids) != 1 || namesOf(kids)[0] != "Kept" {
		t.Fatalf("%d elements survived (%v), wanted only the one naming a real mark",
			len(kids), namesOf(kids))
	}
	kept, _ := reader.ToDict(kids[0])
	mcr, ok := reader.ToDict(kidsOf(t, d, kept)[0])
	if !ok {
		t.Fatal("the reference is gone")
	}
	first, _ := d.PageRef(1)
	if pg, ok := mcr.Get("Pg").(reader.Ref); !ok || pg != first {
		t.Errorf("the reference says the mark is on %v, wanted page one at %v", mcr.Get("Pg"), first)
	}
}

func TestTwoDrawnStreamsAndOneIdentifierTwice(t *testing.T) {
	g := newTagged()
	other := g.w.Add(&reader.Stream{Dict: reader.Dict{
		"Type": reader.Name("XObject"), "Subtype": reader.Name("Form"),
		"StructParents": reader.Integer(2),
	}, Raw: []byte("/P <</MCID 1>> BDC BT ET EMC\n")})
	g.draw("Fm1", other)
	// Something the page draws that is not a stream at all.
	g.draw("Fm2", g.notAPage)
	root := reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"IDTree": reader.Dict{"Names": reader.Array{}},
		"K": reader.Array{
			g.elem("A", reader.Dict{"Pg": g.page[0], "ID": reader.String("aaa")},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(4),
					"Stm": g.stream}),
			g.elem("B", reader.Dict{"Pg": g.page[0], "ID": reader.String("aaa")},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(1),
					"Stm": other}),
			// A reference into something the page draws that is not a stream.
			g.elem("C", reader.Dict{"Pg": g.page[0]},
				reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(1),
					"Stm": g.notAPage}),
		}}
	src := g.finish(t, root)
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	tree := treeOf(t, d, catalog)
	kids := kidsOf(t, d, tree)
	if len(kids) != 2 {
		t.Fatalf("%d elements survived (%v), wanted the two whose streams are streams",
			len(kids), namesOf(kids))
	}
	// Two streams, each keeping its own number, and both below the numbers the
	// pages were handed.
	nums := numsOf(t, d, tree)
	for _, key := range []int64{2, 7} {
		if _, ok := reader.ToArray(nums[key]); !ok {
			t.Errorf("the number tree holds %v under the stream's own key %d", nums[key], key)
		}
	}
	for i := 1; i <= d.PageCount(); i++ {
		page, _ := d.Page(i)
		if got, ok := reader.ToInt(page.Get("StructParents")); ok && got <= 7 {
			t.Errorf("page %d was handed number %d, which a stream already holds", i, got)
		}
	}
	// One identifier naming two elements: a name tree has one entry per key.
	ids, _ := d.GetDict(tree, "IDTree")
	names, _ := reader.ToArray(ids.Get("Names"))
	if len(names) != 2 {
		t.Errorf("the identifier tree holds %v, wanted one entry", names)
	}
}

func TestResourcesNestedDeeperThanAnythingReal(t *testing.T) {
	// The walk that settles which numbers are already taken has to stop
	// somewhere, and a file can nest a dictionary without end.
	g := newTagged()
	nest := reader.Object(reader.Dict{"StructParents": reader.Integer(4)})
	for i := 0; i < maxStructDepth+4; i++ {
		nest = reader.Dict{"Deeper": nest}
	}
	g.drawn["Nest"] = nest
	root := reader.Dict{"Type": reader.Name("StructTreeRoot"),
		"K": g.elem("Keeper", reader.Dict{"Pg": g.page[0]}, reader.Integer(0))}
	src := g.finish(t, root)
	d, catalog := rebuilt(t, src, func(doc *Doc) { mustDo(t, doc.Rotate("all", 90)) })
	tree := treeOf(t, d, catalog)
	if kids := kidsOf(t, d, tree); len(kids) != 1 {
		t.Fatalf("%d elements survived (%v)", len(kids), namesOf(kids))
	}
}
