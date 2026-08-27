package ops

import (
	"bytes"
	"sort"

	"github.com/go-pdfkit/reader"
)

// The structure tree is the document's reading order: which run of marks on
// which page is a heading, a paragraph, a table cell, the label of a form
// field. It is what a screen reader follows, and for a government form it is
// often what the law requires. Of 1 633 real forms in the corpus, 1 021 carry
// one and 1 012 of those carry the number tree that indexes it.
//
// It is also the one thing in a catalogue that cannot be copied across. Every
// part of it points into the document: an element names the page it is on, the
// numbered marks inside that page's content, and the annotations it stands
// for, and the number tree is indexed by a key the page itself carries. So it
// is rebuilt here, element by element, around the pages that survived — and
// where a piece of it cannot be placed honestly it is left out rather than
// pointed somewhere plausible, because a reader that finds no structure falls
// back on the text, and one that finds the wrong structure does not.

// maxStructDepth bounds the walk of a structure tree. Deeper than this is a
// file playing games rather than a document with a shape.
const maxStructDepth = 64

// maxMarksPerPage bounds the array that maps a page's marks back to the
// elements that own them. The array is indexed by mark number and real files
// leave gaps in it — the corpus has two million holes and its longest array is
// 7 566 entries — so its size is decided by the largest number, and a file
// naming mark two billion would otherwise ask for the memory to match.
const maxMarksPerPage = 1 << 20

// structKeysRebuilt are the entries of a structure element this package
// decides for itself rather than copying across.
var structKeysRebuilt = map[reader.Name]bool{
	"K":  true, // its children: the ones that survived, renumbered
	"P":  true, // its parent in the tree being written
	"Pg": true, // the page it is on, which is a page of this document now

	// /Ref names other structure elements, which is a thing this rebuild
	// cannot answer while it is still deciding which of them survive; copied
	// as it stands it would drag a second copy of the source's own tree —
	// and, through it, of the source's own pages — into the file behind it.
	"Ref": true,
}

// structRootKeys are the entries of the tree's root that describe how to read
// it rather than pointing into the document, and so travel unchanged.
var structRootKeys = []reader.Name{
	"RoleMap",  // what a document's own element names stand for
	"ClassMap", // the attribute classes its elements refer to
}

// pageGone is the page of an element whose page is not in this document. It is
// told apart from naming no page at all, because a child that inherits a page
// that has gone has gone with it, while one that inherits nothing never had a
// page to lose.
const pageGone = -1

// A structRebuild is one rebuild of one structure tree.
type structRebuild struct {
	w    *reader.Writer
	src  *reader.Document
	kept *keptAnnots
	// root is the number the tree's root is given before its elements are
	// built, since each of the top ones has to point back at it.
	root reader.Ref

	// pageOf says which page of the source an object number is, counting from
	// one, so that an element's /Pg is recognised without searching.
	pageOf map[int]int
	// at is where the first output copy of each source page went, with its
	// dictionary, still open to be told which number its structure is filed
	// under.
	at map[int]builtPage
	// order is the source page numbers in the order they were written.
	order []int

	// marks[page][mark] is the element that owns one mark on one page.
	marks map[int]map[int64]reader.Ref
	// streams[key][mark] is the element that owns one mark inside a stream the
	// page draws rather than inside the page's own content, under the key that
	// stream carries.
	streams map[int64]map[int64]reader.Ref
	// floor is the first number free to be handed out to a page or an
	// annotation: see floorAbove.
	floor int64
	// reach[page] is the objects one page draws, worked out only when a mark
	// inside one of them has to be placed.
	reach map[int]map[int]bool
	// owner[annot] is the element that stands for one surviving annotation.
	owner map[reader.Ref]reader.Ref
	// ids are the identifiers of the elements that survived.
	ids []structID
	// seen is the elements already visited, since a file may point back.
	seen map[int]bool
}

// A structID is one element's identifier and where the element ended up.
type structID struct {
	id  []byte
	ref reader.Ref
}

// keepStructure rebuilds the structure a screen reader follows, and reports
// where the tree's root went, or nil when nothing of it survived.
func (d *Doc) keepStructure(w *reader.Writer, src *reader.Document, catalog reader.Dict, kept *keptAnnots, built []builtPage) reader.Object {
	root, ok := src.GetDict(catalog, "StructTreeRoot")
	if !ok {
		return nil
	}
	s := &structRebuild{
		w: w, src: src, kept: kept, root: w.Reserve(),
		pageOf:  map[int]int{},
		at:      map[int]builtPage{},
		marks:   map[int]map[int64]reader.Ref{},
		streams: map[int64]map[int64]reader.Ref{},
		reach:   map[int]map[int]bool{},
		owner:   map[reader.Ref]reader.Ref{},
		seen:    map[int]bool{},
	}
	for i := 1; i <= src.PageCount(); i++ {
		ref, _ := src.PageRef(i)
		s.pageOf[ref.Num] = i
	}
	for _, p := range built {
		if _, already := s.at[p.num]; already {
			// The same page of the source, written twice. An element says
			// which single page it is on, so the first copy is the one the
			// structure describes and the others carry none: a page a reader
			// finds no structure on is read as it stands, which is what an
			// unmarked page has always been.
			continue
		}
		s.at[p.num] = p
		s.order = append(s.order, p.num)
	}
	s.floorAbove()
	kids := s.top(root)
	if len(kids) == 0 {
		return nil
	}
	out := reader.Dict{"Type": reader.Name("StructTreeRoot"), "K": kids}
	for _, key := range structRootKeys {
		if v, named := root[key]; named {
			out[key] = w.Copy(src, v)
		}
	}
	if nums, next := s.parentTree(); len(nums) > 0 {
		out["ParentTree"] = w.Add(reader.Dict{"Nums": nums})
		// Where an editor adding to this tree should carry on numbering.
		out["ParentTreeNextKey"] = reader.Integer(next)
	}
	if _, named := root["IDTree"]; named {
		if names := s.idTree(); len(names) > 0 {
			out["IDTree"] = w.Add(reader.Dict{"Names": names})
		}
	}
	w.Put(s.root, out)
	return s.root
}

// top rebuilds the children of the tree's root, which are elements and
// nothing else.
func (s *structRebuild) top(root reader.Dict) reader.Array {
	entry := root.Get("K")
	list, ok := resolveArray(s.src, entry)
	if !ok {
		list = reader.Array{entry}
	}
	var out reader.Array
	for _, kid := range list {
		if ref, _, ok := s.element(kid, s.root, 0, 0); ok {
			out = append(out, ref)
		}
	}
	return out
}

// element rebuilds one structure element and reports whether anything of it
// survived, and which page of the source what is left of it is on. page is the
// page it is on coming in, which it may have inherited from an element above
// it.
func (s *structRebuild) element(o reader.Object, parent reader.Ref, page, depth int) (reader.Ref, int, bool) {
	if depth > maxStructDepth {
		return reader.Ref{}, 0, false
	}
	if ref, ok := o.(reader.Ref); ok {
		if s.seen[ref.Num] {
			return reader.Ref{}, 0, false
		}
		s.seen[ref.Num] = true
	}
	elem, ok := resolveDict(s.src, o)
	if !ok {
		return reader.Ref{}, 0, false
	}
	switch kind, _ := reader.ToName(resolve(s.src, elem.Get("Type"))); kind {
	case "MCR", "OBJR":
		// A mark, or a reference to an annotation, at the top of a tree: there
		// is no element there for it to belong to.
		return reader.Ref{}, 0, false
	}
	own, named := s.pageAt(elem)
	if named {
		page = own
	}
	// The element is given its number before its children are rebuilt, since
	// each of them has to point back at it.
	ref := s.w.Reserve()
	kids, had, at := s.children(elem, ref, page, depth)
	if len(kids) == 0 && (had > 0 || page == pageGone) {
		// An element whose children have all gone describes nothing. One that
		// never had any is the shape of the document rather than a claim about
		// its content — an empty table cell, of which this corpus has 30 345 —
		// and is kept, as long as the page it sits on is still here.
		return reader.Ref{}, 0, false
	}
	out := reader.Dict{}
	for k, v := range elem {
		if structKeysRebuilt[k] {
			continue
		}
		out[k] = s.w.Copy(s.src, v)
	}
	out["P"] = parent
	if len(kids) > 0 {
		out["K"] = kids
	}
	switch {
	case named && page > 0:
		out["Pg"] = s.at[page].ref
	case named && at > 0:
		// Its own page has gone but some of its content is still here, on
		// another one. Saying nothing would leave it inheriting the page of
		// whatever it sits under — a page it is not on, stated as confidently
		// as the right one would have been.
		out["Pg"] = s.at[at].ref
	}
	if id, ok := reader.ToString(resolve(s.src, elem.Get("ID"))); ok {
		s.ids = append(s.ids, structID{id, ref})
	}
	s.w.Put(ref, out)
	if page > 0 {
		return ref, page, true
	}
	return ref, at, true
}

// pageAt reports which page of the source an element names, and whether it
// named one at all.
func (s *structRebuild) pageAt(elem reader.Dict) (int, bool) {
	entry := elem.Get("Pg")
	if entry.Kind() == reader.KindNull {
		return 0, false
	}
	ref, ok := entry.(reader.Ref)
	if !ok {
		// A page written inside the element rather than referred to is not a
		// page of the document: nothing else could point at it.
		return pageGone, true
	}
	num, ok := s.pageOf[ref.Num]
	if !ok {
		return pageGone, true
	}
	if _, kept := s.at[num]; !kept {
		return pageGone, true
	}
	return num, true
}

// children rebuilds an element's children. It reports how many the source gave
// it, so that an element that never had any can be told from one whose own have
// all gone, and the first page any of what is left is on.
func (s *structRebuild) children(elem reader.Dict, ref reader.Ref, page, depth int) (reader.Array, int, int) {
	entry := elem.Get("K")
	list, ok := resolveArray(s.src, entry)
	if !ok {
		if resolve(s.src, entry).Kind() == reader.KindNull {
			return nil, 0, 0
		}
		// One child, written on its own rather than in an array, which is how
		// 803 of the corpus's 1 021 trees write the root's.
		list = reader.Array{entry}
	}
	var out reader.Array
	at := 0
	for _, kid := range list {
		got, on, ok := s.child(kid, ref, page, depth)
		if !ok {
			continue
		}
		out = append(out, got)
		if at == 0 {
			at = on
		}
	}
	return out, len(list), at
}

// child rebuilds one child of a structure element: another element, an integer
// naming a mark in the page's own content, a marked-content reference, or a
// reference to an annotation.
func (s *structRebuild) child(o reader.Object, parent reader.Ref, page, depth int) (reader.Object, int, bool) {
	resolved := resolve(s.src, o)
	if n, ok := reader.ToInt(resolved); ok {
		return s.mark(n, parent, page)
	}
	kid, ok := reader.ToDict(resolved)
	if !ok {
		return nil, 0, false
	}
	switch kind, _ := reader.ToName(resolve(s.src, kid.Get("Type"))); kind {
	case "MCR":
		return s.markRef(kid, parent, page)
	case "OBJR":
		return s.objectRef(kid, parent, page)
	}
	ref, on, ok := s.element(o, parent, page, depth+1)
	return ref, on, ok
}

// mark records one mark of one page as belonging to an element, and reports
// the child to write in its place. The number is left exactly as it was: a
// page's content is copied byte for byte, so the marks inside it still carry
// the numbers they carried, and renumbering them here would be inventing a
// disagreement with the content.
func (s *structRebuild) mark(n int64, parent reader.Ref, page int) (reader.Object, int, bool) {
	if page <= 0 || n < 0 || n >= maxMarksPerPage {
		return nil, 0, false
	}
	at, ok := s.marks[page]
	if !ok {
		at = map[int64]reader.Ref{}
		s.marks[page] = at
	}
	// Two elements claiming one mark is a file contradicting itself, and the
	// number tree can only name one of them; the later one is taken, which is
	// what a reader reading the file in order would have been left with.
	at[n] = parent
	return reader.Integer(n), page, true
}

// markRef rebuilds a marked-content reference, which is the long way of naming
// a mark: it may say which page the mark is on rather than leave it to be
// inherited, and it may say the mark is inside a stream the page draws rather
// than inside the page's own content.
func (s *structRebuild) markRef(kid reader.Dict, parent reader.Ref, page int) (reader.Object, int, bool) {
	n, ok := reader.ToInt(resolve(s.src, kid.Get("MCID")))
	if !ok {
		return nil, 0, false
	}
	own, named := s.pageAt(kid)
	if named {
		page = own
	}
	if stm := kid.Get("Stm"); stm.Kind() != reader.KindNull {
		return s.streamMark(stm, n, parent, page, named)
	}
	if _, _, ok := s.mark(n, parent, page); !ok {
		return nil, 0, false
	}
	out := reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(n)}
	if named {
		out["Pg"] = s.at[page].ref
	}
	return out, page, true
}

// streamMark carries a mark that lives inside a stream the page draws rather
// than inside the page's own content — a form XObject, in every one of the 215
// the corpus has. Such a mark is numbered within that stream, and filed under
// a key the stream itself carries: the four UK power-of-attorney forms keep
// their whole content this way, on pages that carry no key at all.
//
// The stream is followed only when the page it belongs to still draws it. A
// mark in a stream no surviving page reaches is a mark nobody sees, and 109 of
// the corpus's 215 were already in that state in the file they came from.
func (s *structRebuild) streamMark(stm reader.Object, n int64, parent reader.Ref, page int, named bool) (reader.Object, int, bool) {
	if page <= 0 || n < 0 || n >= maxMarksPerPage {
		return nil, 0, false
	}
	ref, ok := stm.(reader.Ref)
	if !ok || !s.reaches(page, ref.Num) {
		return nil, 0, false
	}
	stream, ok := reader.ToStream(resolve(s.src, ref))
	if !ok {
		return nil, 0, false
	}
	key, ok := reader.ToInt(resolve(s.src, stream.Dict.Get("StructParents")))
	if !ok || key < 0 {
		// The stream does not say where its marks are filed, and this package
		// cannot tell it: the copy of it has already been written.
		return nil, 0, false
	}
	at, ok := s.streams[key]
	if !ok {
		at = map[int64]reader.Ref{}
		s.streams[key] = at
	}
	at[n] = parent
	out := reader.Dict{"Type": reader.Name("MCR"), "MCID": reader.Integer(n),
		"Stm": s.w.Copy(s.src, ref)}
	if named {
		out["Pg"] = s.at[page].ref
	}
	return out, page, true
}

// reaches reports whether a page draws the object with the given number: its
// content, and everything the resources it draws with name.
func (s *structRebuild) reaches(page, num int) bool {
	set, ok := s.reach[page]
	if !ok {
		set = map[int]bool{}
		src, _ := s.src.Page(page)
		s.follow(src.Get("Contents"), set, 0)
		s.follow(src.Get("Resources"), set, 0)
		s.reach[page] = set
	}
	return set[num]
}

// floorAbove settles the first number free to be handed out to a page or an
// annotation here.
//
// A stream the page draws may hold marks of its own, filed under a key the
// stream carries rather than under the page's. That is not a guess: of the
// 1 021 tagged forms in the corpus, 991 file every mark under a page, 14 file
// some under a page and some under a form XObject drawn on it, and 3 — the UK
// power-of-attorney forms — carry no key on any page at all and file
// everything under the XObjects, whose stream dictionaries say
// /StructParents 0, 1, 2 and 3 while every page of the file says nothing.
//
// The copy of such a stream in this file keeps the key, because a stream is
// written before the structure above it is rebuilt and cannot be given a new
// one. So the numbers handed out below start above every key any object a
// surviving page draws already carries. Numbering from zero instead would
// eventually hand a page the number a form XObject on it is filed under, and a
// reader looking up a mark in that XObject would be told, with every
// confidence, about the page's own elements.
func (s *structRebuild) floorAbove() {
	walked := map[int]bool{}
	for _, num := range s.order {
		page, _ := s.src.Page(num)
		for _, key := range structDrawn {
			s.follow(page.Get(key), walked, 0)
		}
	}
}

// structDrawn are the entries of a page that lead to what it draws.
var structDrawn = []reader.Name{"Contents", "Resources", "Annots"}

// structKeyed are the entries under which an object says where its own marks
// are filed in the parent tree.
var structKeyed = []reader.Name{"StructParents", "StructParent"}

// follow walks what a page draws, collecting the objects it reaches and
// raising the floor above every parent-tree key they carry.
func (s *structRebuild) follow(o reader.Object, into map[int]bool, depth int) {
	if depth > maxStructDepth {
		return
	}
	if ref, ok := o.(reader.Ref); ok {
		if into[ref.Num] {
			return
		}
		into[ref.Num] = true
	}
	switch v := resolve(s.src, o).(type) {
	case reader.Array:
		for _, e := range v {
			s.follow(e, into, depth+1)
		}
	case reader.Dict:
		s.raise(v)
		for _, e := range v {
			s.follow(e, into, depth+1)
		}
	case *reader.Stream:
		s.raise(v.Dict)
		for _, e := range v.Dict {
			s.follow(e, into, depth+1)
		}
	}
}

// raise lifts the floor above the parent-tree keys one object carries.
func (s *structRebuild) raise(d reader.Dict) {
	for _, key := range structKeyed {
		if n, ok := reader.ToInt(resolve(s.src, d.Get(key))); ok && n >= s.floor {
			s.floor = n + 1
		}
	}
}

// objectRef rebuilds a reference to something outside the content, which in
// practice is an annotation: a link, or the widget through which a form field
// is filled in. It survives exactly as long as the annotation does — 104 379
// of the corpus's 104 521 point at one, and the other 142 pointed at nothing
// on a page in the source either.
//
// No page is written on it: the annotation says which page it is on, and so
// does the element above it.
func (s *structRebuild) objectRef(kid reader.Dict, parent reader.Ref, page int) (reader.Object, int, bool) {
	to, ok := s.kept.find(s.src, kid.Get("Obj"))
	if !ok {
		return nil, 0, false
	}
	s.owner[to] = parent
	if own, named := s.pageAt(kid); named {
		page = own
	}
	if page < 0 {
		page = 0
	}
	return reader.Dict{"Type": reader.Name("OBJR"), "Obj": to}, page, true
}

// parentTree maps each page, each drawn stream and each annotation the
// structure points at back to the elements on it, under the number that page,
// stream or annotation carries, and reports the next number free after it.
//
// A stream keeps the number it had, since the copy of it in this file has
// already been written carrying that number. A page and an annotation are
// given theirs afresh, in the order they were written: a page that kept the
// number it had in a file it is no longer part of is a page a reader would look
// up and be told about somebody else's. The entries come out in order of their
// number, which is what makes a number tree a tree.
func (s *structRebuild) parentTree() (reader.Array, int64) {
	var nums reader.Array
	filed := make([]int64, 0, len(s.streams))
	for key := range s.streams {
		filed = append(filed, key)
	}
	sort.Slice(filed, func(i, j int) bool { return filed[i] < filed[j] })
	for _, key := range filed {
		nums = append(nums, reader.Integer(key), s.w.Add(markArray(s.streams[key])))
	}
	key := s.floor
	for _, num := range s.order {
		at, ok := s.marks[num]
		if !ok {
			continue
		}
		nums = append(nums, reader.Integer(key), s.w.Add(markArray(at)))
		s.at[num].dict["StructParents"] = reader.Integer(key)
		key++
	}
	// An annotation is filed under a number of its own, and its entry is the
	// one element that stands for it rather than an array.
	for _, ref := range s.kept.order {
		owner, ok := s.owner[ref]
		if !ok {
			continue
		}
		nums = append(nums, reader.Integer(key), owner)
		s.kept.dict[ref]["StructParent"] = reader.Integer(key)
		key++
	}
	return nums, key
}

// markArray lays a page's marks out as an array indexed by mark number, with
// the gaps a real file leaves in it written as null.
func markArray(at map[int64]reader.Ref) reader.Array {
	high := int64(0)
	for n := range at {
		if n > high {
			high = n
		}
	}
	out := make(reader.Array, high+1)
	for i := range out {
		out[i] = reader.Null{}
	}
	for n, ref := range at {
		out[n] = ref
	}
	return out
}

// idTree lists the identifiers of the elements that survived. A name tree is
// its keys in order, so that a reader can find one by halving; the identifier
// of an element that has gone is not carried, since it would name nothing.
func (s *structRebuild) idTree() reader.Array {
	sort.SliceStable(s.ids, func(i, j int) bool { return bytes.Compare(s.ids[i].id, s.ids[j].id) < 0 })
	var out reader.Array
	seen := map[string]bool{}
	for _, e := range s.ids {
		if seen[string(e.id)] {
			// One identifier naming two elements is a file contradicting
			// itself, and a name tree has one entry per key.
			continue
		}
		seen[string(e.id)] = true
		out = append(out, reader.String(e.id), e.ref)
	}
	return out
}
