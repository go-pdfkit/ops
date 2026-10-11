// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// layerSpec describes one layer of a fixture.
type layerSpec struct {
	name    string
	hidden  bool
	locked  bool
	content bool // whether a page is marked as drawing on it
}

// layeredPDF builds a one-page document with optional content, the way a
// drawing program writes one: the layer objects in /OCProperties, the
// visibility in its default configuration, and the page's own /Resources
// /Properties naming the layer its content is marked with.
func layeredPDF(t *testing.T, specs []layerSpec, extra func(d reader.Dict)) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	var ocgs, order, on, off, locked reader.Array
	properties := reader.Dict{}
	var content strings.Builder
	for i, s := range specs {
		ref := w.Add(reader.Dict{
			"Type": reader.Name("OCG"), "Name": reader.String(s.name),
		})
		ocgs = append(ocgs, ref)
		order = append(order, ref)
		if s.hidden {
			off = append(off, ref)
		} else {
			on = append(on, ref)
		}
		if s.locked {
			locked = append(locked, ref)
		}
		if s.content {
			mark := fmt.Sprintf("MC%d", i)
			properties[reader.Name(mark)] = ref
			content.WriteString("/OC /" + mark + " BDC\n1 0 0 RG 10 10 50 50 re S\nEMC\n")
		}
	}
	stream := w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(content.String())})
	pageRef := w.Add(reader.Dict{
		"Type": reader.Name("Page"), "Parent": pagesRef, "Contents": stream,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(100)},
		"Resources": reader.Dict{"Properties": properties},
	})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	config := reader.Dict{"Order": order, "ON": on}
	if len(off) > 0 {
		config["OFF"] = off
	}
	if len(locked) > 0 {
		config["Locked"] = locked
	}
	if extra != nil {
		extra(config)
	}
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"OCProperties": reader.Dict{"OCGs": ocgs, "D": config},
	})})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// layersAfterWriting writes a document out, reads it back, and reports its
// layers. ⛔ Through the FILE: what matters is what a viewer will be told.
func layersAfterWriting(t *testing.T, d *Doc) []Layer {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	return back.Layers()
}

func show(layers []Layer) string {
	var parts []string
	for _, l := range layers {
		s := l.Name
		if !l.Visible {
			s += "(off)"
		}
		if l.Locked {
			s += "(locked)"
		}
		if l.Depth > 0 {
			s = strings.Repeat(">", l.Depth) + s
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}

// ⛔⛔ THE WITNESS FOR THE WHOLE CHANGE, and the measurement that prompted it.
// Before this, a one-page drawing with a layer switched OFF came back from
// `pdfops watermark` with NO /OCProperties at all: the content still marked as
// belonging to a layer, the layer object still reachable through the page's own
// resources, and nothing left saying which layer was hidden. So the hidden
// layer SHOWED — a drawing distributed with an "internal pricing" layer turned
// off revealed it to any verb at all.
//
// The assertion is on the VISIBILITY and not on the presence of the layers: a
// carry that kept both layers and lost which one was off would pass a test
// that only counted them, and would have exactly the same consequence.
func TestAHiddenLayerStaysHiddenThroughAVerb(t *testing.T) {
	b := layeredPDF(t, []layerSpec{
		{name: "Markup", content: true},
		{name: "Internal pricing", hidden: true, content: true},
	}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := show(d.Layers()); got != "Markup,Internal pricing(off)" {
		t.Fatalf("the fixture itself reads as %q, so this proves nothing", got)
	}
	if err := d.Watermark("all", "DRAFT"); err != nil {
		t.Fatal(err)
	}
	if got := show(layersAfterWriting(t, d)); got != "Markup,Internal pricing(off)" {
		t.Errorf("after a watermark the layers are %q, want the hidden one still off", got)
	}
}

// And through the verbs that move the pages, where there is no page index to
// get wrong — layers belong to the document, not to a page.
func TestLayersSurviveTheVerbsThatMovePages(t *testing.T) {
	b := layeredPDF(t, []layerSpec{
		{name: "A", content: true}, {name: "B", hidden: true, content: true},
	}, nil)
	for name, verb := range map[string]func(*Doc) error{
		"reverse":  func(d *Doc) error { d.Reverse(); return nil },
		"select":   func(d *Doc) error { return d.Select("1") },
		"rotate":   func(d *Doc) error { return d.Rotate("all", 90) },
		"compress": func(d *Doc) error { d.Compress(); return nil },
	} {
		t.Run(name, func(t *testing.T) {
			d, err := Open(b)
			if err != nil {
				t.Fatal(err)
			}
			if err := verb(d); err != nil {
				t.Fatal(err)
			}
			if got := show(layersAfterWriting(t, d)); got != "A,B(off)" {
				t.Errorf("layers = %q, want A,B(off)", got)
			}
		})
	}
}

// ⛔ /BaseState OFF inverts the question: everything is hidden unless /ON names
// it. A reader that only looked at /OFF would report every layer of such a
// document as visible — and a drawing that ships with all its layers off but
// one is exactly how an alternatives document is built.
func TestBaseStateOffMeansHiddenUnlessNamed(t *testing.T) {
	b := layeredPDF(t, []layerSpec{
		{name: "Shown", content: true}, {name: "Hidden", content: true},
	}, func(config reader.Dict) {
		config["BaseState"] = reader.Name("OFF")
		// Only the first is named ON.
		if on, ok := config["ON"].(reader.Array); ok && len(on) > 0 {
			config["ON"] = reader.Array{on[0]}
		}
	})
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := show(d.Layers()); got != "Shown,Hidden(off)" {
		t.Errorf("with BaseState OFF the layers read as %q", got)
	}
}

// The nesting comes from /D /Order, which is a nested array — and a string
// inside it is the GROUP's label rather than a layer, which a reader that took
// it for one would list as something switchable.
func TestTheNestingAndTheGroupLabels(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	top := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Plan")})
	childA := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Walls")})
	childB := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Doors")})
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(100), reader.Integer(100)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{}})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"OCProperties": reader.Dict{
			"OCGs": reader.Array{top, childA, childB},
			"D": reader.Dict{"Order": reader.Array{
				top,
				reader.Array{reader.String("the drawing"), childA, childB},
			}, "ON": reader.Array{top, childA, childB}},
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := show(d.Layers()); got != "Plan,>Walls,>Doors" {
		t.Errorf("layers = %q, want Plan,>Walls,>Doors", got)
	}
	// The group's label is not a layer, so it cannot be switched.
	if err := d.SetLayerVisible("the drawing", false); err == nil {
		t.Error("a group's label was treated as a layer")
	}
}

func TestSetLayerVisible(t *testing.T) {
	b := layeredPDF(t, []layerSpec{
		{name: "A", content: true}, {name: "B", hidden: true, content: true},
	}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetLayerVisible("B", true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetLayerVisible("A", false); err != nil {
		t.Fatal(err)
	}
	if got := show(layersAfterWriting(t, d)); got != "A(off),B" {
		t.Errorf("layers = %q, want A(off),B", got)
	}
}

// ⛔ A LOCKED layer is refused. The lock is the document asking that this one
// not be switched — a drawing's dimension layer that everything else is
// positioned against — and a tool that switched it anyway would be ignoring
// the only thing the file says about its own layers.
func TestALockedLayerIsRefused(t *testing.T) {
	b := layeredPDF(t, []layerSpec{{name: "Grid", locked: true, content: true}}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := show(d.Layers()); got != "Grid(locked)" {
		t.Fatalf("the fixture reads as %q", got)
	}
	err = d.SetLayerVisible("Grid", false)
	if err == nil {
		t.Fatal("a locked layer was switched")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	// And the lock survives the rewrite, or the refusal is good for one pass.
	if got := show(layersAfterWriting(t, d)); got != "Grid(locked)" {
		t.Errorf("after writing, the lock is %q", got)
	}
}

func TestAddLayer(t *testing.T) {
	b := layeredPDF(t, []layerSpec{{name: "Plan", content: true}}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddLayer("Notes", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.AddLayer("Mine", "Plan"); err != nil {
		t.Fatal(err)
	}
	if got := show(layersAfterWriting(t, d)); got != "Plan,>Mine,Notes" {
		t.Errorf("layers = %q, want Plan,>Mine,Notes: a child goes after its PARENT", got)
	}
	if err := d.AddLayer("", ""); err == nil {
		t.Error("a layer with no name was added")
	}
	if err := d.AddLayer("x", "no such parent"); err == nil {
		t.Error("a layer under a parent that is not there was added")
	}
}

// ⛔ Dropping a HIDDEN layer is refused, because what was drawn on it does not
// go with it: the content stays in the page and stops being optional, so the
// hidden drawing becomes permanently VISIBLE — the opposite of what somebody
// deleting a layer called "internal pricing" expects.
func TestDroppingAHiddenLayerIsRefused(t *testing.T) {
	b := layeredPDF(t, []layerSpec{
		{name: "Plan", content: true},
		{name: "Internal pricing", hidden: true, content: true},
	}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	err = d.DropLayer("Internal pricing")
	if err == nil {
		t.Fatal("a hidden layer was dropped")
	}
	if !strings.Contains(err.Error(), "VISIBLE") {
		t.Errorf("the refusal does not say what would happen: %v", err)
	}
	// A visible one drops, because nothing changes about what is seen.
	if err := d.DropLayer("Plan"); err != nil {
		t.Fatal(err)
	}
	if got := show(layersAfterWriting(t, d)); got != "Internal pricing(off)" {
		t.Errorf("layers = %q", got)
	}
}

// ⛔ A name is not an identifier. A drawing assembled from two others often has
// two "Markup" layers, and switching one of them at random is a change nobody
// asked for and nobody can see.
func TestANameTwoLayersShareIsRefused(t *testing.T) {
	b := layeredPDF(t, []layerSpec{
		{name: "Markup", content: true}, {name: "Markup", content: true},
	}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d.Layers()); n != 2 {
		t.Fatalf("%d layers, want 2", n)
	}
	err = d.SetLayerVisible("Markup", false)
	if err == nil {
		t.Fatal("an ambiguous name was accepted")
	}
	if !strings.Contains(err.Error(), "2 layers") {
		t.Errorf("the refusal does not say how many: %v", err)
	}
	if err := d.DropLayer("Markup"); err == nil {
		t.Error("an ambiguous name was dropped")
	}
	if err := d.SetLayerVisible("no such layer", false); err == nil {
		t.Error("a layer that is not there was switched")
	}
}

// ⛔ A MERGE keeps every source's layers. keepCatalogue refuses to merge two
// catalogues because two forms whose fields are named the same cannot honestly
// become one — but two drawings' layers can: a layer is its object, and two
// layers named "Markup" are two layers.
func TestAMergeKeepsEverySourcesLayers(t *testing.T) {
	a, err := Open(layeredPDF(t, []layerSpec{{name: "First", content: true}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(layeredPDF(t, []layerSpec{
		{name: "Second", hidden: true, content: true},
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	a.Append(b)
	if got := show(layersAfterWriting(t, a)); got != "First,Second(off)" {
		t.Errorf("layers = %q, want First,Second(off)", got)
	}
}

// ⛔ A document with NO optional content gets none. Without this, everything
// above would pass on an implementation that wrote an empty /OCProperties into
// every file — which a viewer shows as an empty layers panel where there
// should be no panel at all.
func TestADocumentWithNoLayersGetsNone(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Layers(); len(got) != 0 {
		t.Errorf("layers = %v", got)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "OCProperties") {
		t.Error("an /OCProperties was written for a document that has none")
	}
}

// ⛔ The reported list reflects edits that have NOT been written yet, so a
// caller can ask what it is about to do. Dropping is the one that changes the
// list's length, and the one whose in-place rewrite could lose a layer it
// should have kept.
func TestLayersReportsPendingEdits(t *testing.T) {
	b := layeredPDF(t, []layerSpec{
		{name: "A", content: true}, {name: "B", content: true}, {name: "C", content: true},
	}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DropLayer("B"); err != nil {
		t.Fatal(err)
	}
	if got := show(d.Layers()); got != "A,C" {
		t.Errorf("before writing, Layers() = %q, want A,C", got)
	}
	if err := d.SetLayerVisible("C", false); err != nil {
		t.Fatal(err)
	}
	if err := d.AddLayer("D", "A"); err != nil {
		t.Fatal(err)
	}
	if got := show(d.Layers()); got != "A,>D,C(off)" {
		t.Errorf("Layers() = %q, want A,>D,C(off)", got)
	}
	// And the file agrees with what was reported.
	if got := show(layersAfterWriting(t, d)); got != "A,>D,C(off)" {
		t.Errorf("after writing = %q", got)
	}
}

// Pages from several files have several sets of layers, and Layers answers for
// the document as it stands rather than guessing — the WRITE merges them.
func TestLayersOfAMergedDocumentAreNotReportedUntilItIsWritten(t *testing.T) {
	a, err := Open(layeredPDF(t, []layerSpec{{name: "First", content: true}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(layeredPDF(t, []layerSpec{{name: "Second", content: true}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	a.Append(b)
	if got := a.Layers(); got != nil {
		t.Errorf("Layers() of a merge = %v, want nothing until it is written", got)
	}
	// ⛔ And the write has them both, which is what makes the nil above an
	// answer about timing rather than a loss.
	if got := show(layersAfterWriting(t, a)); got != "First,Second" {
		t.Errorf("after writing = %q", got)
	}
}

// A catalogue with no /OCProperties, and one whose /OCProperties is not a
// dictionary: both are "no layers" and neither is an error.
func TestALayerTreeThatIsNotOneIsNoLayers(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(10), reader.Integer(10)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{}})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"OCProperties": reader.Integer(7),
	})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Layers(); len(got) != 0 {
		t.Errorf("layers = %v", got)
	}
}

// ⛔ The ceilings, lowered so a test can reach them. A layer list is a count
// the file chose and /Order is a tree it can nest as deep as it likes; a
// guard against either that no test exercises is not a guard.
func TestTheLayerCeilings(t *testing.T) {
	t.Run("how many", func(t *testing.T) {
		defer func(n int) { maxLayers = n }(maxLayers)
		maxLayers = 2
		specs := make([]layerSpec, 6)
		for i := range specs {
			specs[i] = layerSpec{name: fmt.Sprintf("L%d", i)}
		}
		d, err := Open(layeredPDF(t, specs, nil))
		if err != nil {
			t.Fatal(err)
		}
		// ⛔ Asserted against a LITERAL: the ceiling is two, so at most three
		// come back (the walk stops once it is past). Writing maxLayers here
		// would move the judge with the subject.
		if got := len(d.Layers()); got > 2 {
			t.Errorf("%d layers came back under a ceiling of 2", got)
		}
	})
	t.Run("how deep", func(t *testing.T) {
		defer func(n int) { maxLayerDepth = n }(maxLayerDepth)
		maxLayerDepth = 1
		w := reader.NewWriter("1.7")
		pagesRef := w.Reserve()
		deep := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Deep")})
		pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
				reader.Integer(10), reader.Integer(10)},
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{}})})
		w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
			"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
		// Nested four deep, under a ceiling of one.
		order := reader.Array{reader.Array{reader.Array{reader.Array{deep}}}}
		out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
			"Type": reader.Name("Catalog"), "Pages": pagesRef,
			"OCProperties": reader.Dict{"OCGs": reader.Array{},
				"D": reader.Dict{"Order": order}},
		})})
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(out)
		if err != nil {
			t.Fatal(err)
		}
		// The walk stopped, so the layer buried past the ceiling is not listed
		// at its depth — and the document still opens and still writes.
		for _, l := range d.Layers() {
			if l.Depth > 2 {
				t.Errorf("a layer came back at depth %d under a ceiling of 1", l.Depth)
			}
		}
		if _, err := d.Bytes(); err != nil {
			t.Errorf("writing it: %v", err)
		}
	})
}

// ⛔ The nesting has to ROUND-TRIP, which is a different claim from reading it.
// The first draft read a nested /Order correctly and wrote a FLAT one, so every
// hierarchy was lost on the way out — and the test that read a file somebody
// else had written could not see it.
func TestTheNestingSurvivesBeingWritten(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	mk := func(name string) reader.Object {
		return w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String(name)})
	}
	plan, walls, doors, notes := mk("Plan"), mk("Walls"), mk("Doors"), mk("Notes")
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(10), reader.Integer(10)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{}})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"OCProperties": reader.Dict{
			"OCGs": reader.Array{plan, walls, doors, notes},
			"D": reader.Dict{
				"Order": reader.Array{plan, reader.Array{walls, doors}, notes},
				"ON":    reader.Array{plan, walls, doors, notes},
			},
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	const want = "Plan,>Walls,>Doors,Notes"
	if got := show(d.Layers()); got != want {
		t.Fatalf("reading it gives %q, want %q", got, want)
	}
	// ⛔ And again after a round trip through the file, which is the claim.
	if got := show(layersAfterWriting(t, d)); got != want {
		t.Errorf("after writing it gives %q, want %q — the nesting was lost", got, want)
	}
	// Twice, because a nesting that survives one pass and flattens on the
	// second is a nesting that flattens.
	again, err := Open(mustBytes(t, d))
	if err != nil {
		t.Fatal(err)
	}
	if got := show(layersAfterWriting(t, again)); got != want {
		t.Errorf("after a second pass: %q", got)
	}
}

func mustBytes(t *testing.T, d *Doc) []byte {
	t.Helper()
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ⛔ A reference in /Order that resolves to something that is not a dictionary
// — which a file can say and no drawing program writes. It is not a layer, and
// a reader that took it for one would list an integer as switchable.
func TestAReferenceInTheOrderThatIsNotALayer(t *testing.T) {
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	real := w.Add(reader.Dict{"Type": reader.Name("OCG"), "Name": reader.String("Real")})
	notALayer := w.Add(reader.Integer(7))
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(10), reader.Integer(10)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte{}})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"OCProperties": reader.Dict{
			"OCGs": reader.Array{real, notALayer},
			"D":    reader.Dict{"Order": reader.Array{real, notALayer}, "ON": reader.Array{real}},
		},
	})})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := show(d.Layers()); got != "Real" {
		t.Errorf("layers = %q, want just Real", got)
	}
}

// ⛔ A second child goes after the FIRST one, not between the parent and it.
// childSlot walks past everything already nested under the parent, and a
// version that stopped at the parent would reverse the children each time one
// was added.
func TestASecondChildGoesAfterTheFirst(t *testing.T) {
	b := layeredPDF(t, []layerSpec{{name: "Plan", content: true}}, nil)
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddLayer("Walls", "Plan"); err != nil {
		t.Fatal(err)
	}
	if err := d.AddLayer("Doors", "Plan"); err != nil {
		t.Fatal(err)
	}
	const want = "Plan,>Walls,>Doors"
	if got := show(d.Layers()); got != want {
		t.Errorf("Layers() = %q, want %q", got, want)
	}
	if got := show(layersAfterWriting(t, d)); got != want {
		t.Errorf("after writing = %q, want %q", got, want)
	}
}
