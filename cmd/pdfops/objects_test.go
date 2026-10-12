package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// graphOf runs the verb and parses what it wrote.
func graphOf(t *testing.T, args ...string) jsonGraph {
	t.Helper()
	code, out, errOut := exec(append([]string{"objects"}, args...)...)
	if code != 0 {
		t.Fatalf("objects said %d: %s", code, errOut)
	}
	var g jsonGraph
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, out)
	}
	return g
}

func TestObjectsVerb(t *testing.T) {
	g := graphOf(t, documentFixture(t, reader.Dict{"Title": reader.String("a title")}))
	if g.Version == "" || g.Pages != 2 {
		t.Errorf("it says version %q and %d pages", g.Version, g.Pages)
	}
	if g.Encrypted || g.Repaired {
		t.Errorf("it says encrypted=%v repaired=%v", g.Encrypted, g.Repaired)
	}
	// The trailer points at the catalogue, and the catalogue is among the
	// objects: the walk follows what it finds rather than guessing names.
	trailer, ok := g.Trailer.(map[string]any)
	if !ok {
		t.Fatalf("the trailer is a %T", g.Trailer)
	}
	root, ok := trailer["Root"].(map[string]any)
	if !ok {
		t.Fatalf("the trailer's Root is %#v", trailer["Root"])
	}
	key, _ := root["ref"].(string)
	if key == "" {
		t.Fatalf("Root is not a reference: %#v", root)
	}
	catalog, ok := g.Objects[key].(map[string]any)
	if !ok {
		t.Fatalf("the catalogue at %q is %#v", key, g.Objects[key])
	}
	// ⛔ A name keeps its slash and a string does not get one, which is how
	// the two are told apart: a PDF string may be exactly "/Catalog".
	if catalog["Type"] != "/Catalog" {
		t.Errorf("the catalogue's Type is %#v", catalog["Type"])
	}
	if len(g.Unread) != 0 {
		t.Errorf("it could not read %v", g.Unread)
	}
}

// ⛔ A stream's bytes are NOT here, and its length is. They are the bulk of
// most files and they are binary, so including them would mean base64 — a
// 35 MB file becoming a 47 MB JSON nobody reads.
func TestAStreamsBytesAreNotInTheGraph(t *testing.T) {
	_, out, _ := exec("objects", documentFixture(t, nil))
	g := graphOf(t, documentFixture(t, nil))
	found := false
	for key, o := range g.Objects {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		length, hasLength := m["length"]
		if !hasLength {
			continue
		}
		found = true
		if _, hasDict := m["dict"]; !hasDict {
			t.Errorf("the stream at %q has no dictionary: %#v", key, m)
		}
		if n, _ := length.(float64); n <= 0 {
			t.Errorf("the stream at %q is %v bytes", key, length)
		}
	}
	if !found {
		t.Fatalf("no stream at all in a document with pages:\n%.300s", out)
	}
	// The content stream of the fixture says "The Title"; the graph must not.
	if strings.Contains(out, "The Title") {
		t.Errorf("a stream's bytes are in the output:\n%.400s", out)
	}
}

// ⛔ A text string is DECODED and a string that is not text is hex. The bytes
// of a text string are not text: a title in UTF-16 behind a byte order mark
// reads as "\xfe\xffL\x00a\x00…" raw.
func TestATextStringAndOneThatIsNot(t *testing.T) {
	info := reader.Dict{
		"Title":    reader.String("\xfe\xff\x00R\x00\xe9\x00s\x00u\x00m\x00\xe9"),
		"Producer": reader.String("plain"),
		// A digest: valid bytes, no text in them.
		"Custom": reader.String("\x00\x01\x02\x03\xff\xfe"),
	}
	g := graphOf(t, documentFixture(t, info))
	var seen map[string]any
	for _, o := range g.Objects {
		m, ok := o.(map[string]any)
		if ok && m["Producer"] != nil {
			seen = m
			break
		}
	}
	if seen == nil {
		t.Fatal("the information dictionary is not in the graph")
	}
	text := func(key string) map[string]any {
		m, ok := seen[key].(map[string]any)
		if !ok {
			t.Fatalf("%s is %#v", key, seen[key])
		}
		return m
	}
	if got := text("Title")["text"]; got != "Résumé" {
		t.Errorf("the title is %#v, want Résumé", got)
	}
	if got := text("Producer")["text"]; got != "plain" {
		t.Errorf("the producer is %#v", got)
	}
	if got := text("Custom")["hex"]; got != "000102 03fffe" && got != "00010203fffe" {
		t.Errorf("the digest is %#v, want it in hex", got)
	}
}

// ⛔ A PDF is a GRAPH and not a tree: a page points at its parent, which
// points back at the page. Without a visited set the walk never ends, and
// this fixture has exactly that cycle in it.
func TestACycleTerminates(t *testing.T) {
	g := graphOf(t, documentFixture(t, nil))
	// Every page's Parent is a reference, and the pages node's Kids point
	// back. If the walk terminated, both are in the graph once.
	if len(g.Objects) == 0 {
		t.Fatal("no objects at all")
	}
	for key, o := range g.Objects {
		if o == nil {
			t.Errorf("the object at %q was left as a placeholder, so the walk did not finish it", key)
		}
	}
}

// A reference that leads nowhere is NAMED in the output rather than dropped:
// "the graph, and these edges lead nowhere" is what somebody debugging a
// damaged file needs.
func TestAReferenceThatLeadsNowhere(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		// Nothing was ever written at object 9999.
		"Contents": reader.Ref{Num: 9999, Gen: 0},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{page}, "Count": reader.Integer(1)})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dangling.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	g := graphOf(t, path)
	// Either the reference is named as unread, or the reader resolved it to
	// the null object — both are answers, and silence is not.
	if len(g.Unread) == 0 {
		if _, there := g.Objects["9999 0"]; !there {
			t.Errorf("a reference to nothing left no trace: unread=%v", g.Unread)
		}
	}
}

func TestTheObjectsVerbRefusesWhatItCannotDo(t *testing.T) {
	in := documentFixture(t, nil)
	cases := []struct {
		name, says string
		args       []string
	}{
		{"no file", "expected <in.pdf>", []string{"objects"}},
		{"two files", "expected <in.pdf>", []string{"objects", in, in}},
		{"a flag it does not have", "flag provided but not defined", []string{"objects", "-nope", in}},
		{"a file that is not there", "gone.pdf", []string{"objects", filepath.Join(t.TempDir(), "gone.pdf")}},
	}
	for _, c := range cases {
		code, _, errOut := exec(c.args...)
		if code == 0 {
			t.Errorf("%s: it said nothing was wrong", c.name)
			continue
		}
		if !strings.Contains(errOut, c.says) {
			t.Errorf("%s: it said %q, which does not mention %q", c.name, errOut, c.says)
		}
	}
}

// Nothing unread is `[]`, never `null`.
func TestNothingUnreadIsAnEmptyList(t *testing.T) {
	_, out, _ := exec("objects", documentFixture(t, nil))
	if !strings.Contains(out, `"unread": []`) {
		t.Errorf("unread is not an empty list:\n%s", out[max(0, len(out)-200):])
	}
}

// Every leaf kind the format has, in one dictionary: a null that is PRESENT
// and says nothing, a boolean, a real, a name and an integer.
func TestEveryLeafKind(t *testing.T) {
	info := reader.Dict{
		"Nothing": reader.Null{},
		"Yes":     reader.Bool(true),
		"No":      reader.Bool(false),
		"Ratio":   reader.Real(1.5),
		"Count":   reader.Integer(7),
		"Kind":    reader.Name("Custom"),
	}
	g := graphOf(t, documentFixture(t, info))
	var seen map[string]any
	for _, o := range g.Objects {
		m, ok := o.(map[string]any)
		if ok {
			if _, there := m["Ratio"]; there {
				seen = m
				break
			}
		}
	}
	if seen == nil {
		t.Fatal("the information dictionary is not in the graph")
	}
	if v, there := seen["Nothing"]; !there || v != nil {
		// ⛔ PRESENT and null. A dictionary entry whose value is the null
		// object is not a missing entry, and the format gives that its own
		// meaning in a few places.
		t.Errorf("the null entry is %#v, there=%v", v, there)
	}
	if seen["Yes"] != true || seen["No"] != false {
		t.Errorf("the booleans are %#v and %#v", seen["Yes"], seen["No"])
	}
	if seen["Ratio"] != 1.5 {
		t.Errorf("the real is %#v", seen["Ratio"])
	}
	if seen["Count"] != float64(7) {
		t.Errorf("the integer is %#v", seen["Count"])
	}
	if seen["Kind"] != "/Custom" {
		t.Errorf("the name is %#v, want it to keep its slash", seen["Kind"])
	}
}

// ⛔ A PDF is a graph and the visited set stops a CYCLE, but depth that is
// real rather than cyclic needs its own guard: a thousand nested arrays is a
// legal file, and a stack overflow is not a diagnosis.
func TestNestingDeeperThanTheLimit(t *testing.T) {
	deep := reader.Object(reader.Integer(1))
	for i := 0; i < maxGraphDepth+10; i++ {
		deep = reader.Array{deep}
	}
	g := graphOf(t, documentFixture(t, reader.Dict{"Deep": deep}))
	// It terminated, said so, and the rest of the document is still there.
	out, _ := json.Marshal(g)
	if !strings.Contains(string(out), "deeper than") {
		t.Errorf("it did not say where it stopped")
	}
	if g.Pages != 2 {
		t.Errorf("the rest of the document was lost: %d pages", g.Pages)
	}
}

// ⛔ A reference that cannot be read is NAMED. reader.Get is forgiving by
// design — a reference to an object that was never written resolves to Null —
// so no file reaches this, which is why the walker holds a getter.
func TestAReferenceThatCannotBeRead(t *testing.T) {
	w := &graphWriter{
		get: func(reader.Ref) (reader.Object, error) {
			return nil, errRefused
		},
		objects: map[string]any{},
		unread:  []string{},
	}
	got := w.value(reader.Dict{"Root": reader.Ref{Num: 12, Gen: 0}}, 0)
	if len(w.unread) != 1 || w.unread[0] != "12 0" {
		t.Errorf("unread is %v, want [12 0]", w.unread)
	}
	// ⛔ And no placeholder is left behind: a key whose value is null would
	// read as "object 12 is the null object", which is a different fact.
	if _, there := w.objects["12 0"]; there {
		t.Errorf("a placeholder was left: %#v", w.objects)
	}
	// The reference is still in the graph, pointing at what could not be read.
	m, _ := got.(map[string]any)
	ref, _ := m["Root"].(map[string]any)
	if ref["ref"] != "12 0" {
		t.Errorf("the reference itself was lost: %#v", got)
	}
}

// A type that is not one of the format's. reader.Object is an interface with
// an exported method, so the set is open and this is reachable — saying what
// arrived beats writing null and losing it.
type strangeObject struct{}

func (strangeObject) Kind() reader.Kind { return reader.Kind(0) }

func TestAnObjectKindThatIsNotTheFormats(t *testing.T) {
	w := &graphWriter{objects: map[string]any{}, unread: []string{}}
	got, _ := w.value(strangeObject{}, 0).(map[string]any)
	if got["unknown"] == nil || !strings.Contains(got["unknown"].(string), "strangeObject") {
		t.Errorf("it said %#v", got)
	}
}

// ⛔ ONE READ PER OBJECT, which is what says the cycle guard works.
//
// A PDF is a graph: object 1 is the page tree, its Kids point at the pages,
// and every page's Parent points back at object 1. Without marking an object
// as seen BEFORE reading it, that cycle walks forever — except that the depth
// guard cuts it off at 256, so the answer comes back finite, enormous, and
// wrong. The two guards mask each other, and "it terminated" cannot tell them
// apart. Counting the reads can.
func TestEachObjectIsReadOnce(t *testing.T) {
	b, err := os.ReadFile(documentFixture(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	w := &graphWriter{
		get: func(r reader.Ref) (reader.Object, error) {
			reads++
			return d.Get(r)
		},
		objects: map[string]any{},
		unread:  []string{},
	}
	w.value(d.Trailer(), 0)
	if reads != len(w.objects) {
		t.Errorf("%d reads for %d objects: the graph was walked more than once",
			reads, len(w.objects))
	}
	// And the fixture really does hold the cycle this is about.
	pages, ok := w.objects["1 0"].(map[string]any)
	if !ok {
		t.Fatalf("object 1 0 is %#v", w.objects["1 0"])
	}
	kids, _ := pages["Kids"].([]any)
	if len(kids) == 0 {
		t.Fatal("the page tree has no Kids, so there is no cycle to guard against")
	}
	first, _ := kids[0].(map[string]any)
	page, _ := w.objects[first["ref"].(string)].(map[string]any)
	parent, _ := page["Parent"].(map[string]any)
	if parent["ref"] != "1 0" {
		t.Fatalf("the page's Parent is %#v, so the fixture has no cycle", page["Parent"])
	}
}
