// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// withFile builds a document carrying one file inside it, the way a real one
// does: a name tree under the catalogue, pointing at a file specification,
// pointing at a stream.
func withFile(t *testing.T, name, desc, body string) []byte {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page 1")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": reader.Array{page},
		"Count":    reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)}})
	stream := w.Add(&reader.Stream{Dict: reader.Dict{"Type": reader.Name("EmbeddedFile")},
		Raw: []byte(body)})
	spec := reader.Dict{"Type": reader.Name("Filespec"), "F": reader.String(name),
		"EF": reader.Dict{"F": stream}}
	if desc != "" {
		spec["Desc"] = reader.String(desc)
	}
	names := w.Add(reader.Dict{"Names": reader.Array{reader.String(name), w.Add(spec)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"Names": w.Add(reader.Dict{"EmbeddedFiles": names})})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAFileInsideADocumentSurvivesAVerb(t *testing.T) {
	// Nothing on the page says it is there, so nobody notices it went until
	// the file is wanted. 45 of the 3 215 documents in the forms and scans
	// corpora carry one.
	d, err := Open(withFile(t, "source.csv", "where the figures came from", "a,b\n1,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Rotate("1", 90); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	got := back.Attachments()
	if len(got) != 1 {
		t.Fatalf("%d files came through a rotation, want 1", len(got))
	}
	if got[0].Name != "source.csv" || string(got[0].Data) != "a,b\n1,2\n" {
		t.Errorf("got %+v", got[0])
	}
	if got[0].Description != "where the figures came from" {
		t.Errorf("the description went: %q", got[0].Description)
	}
}

func TestAFileIsPutIn(t *testing.T) {
	d := New()
	d.Blank(100, 200)
	if err := d.Attach("notes.txt", []byte("hello"), "a note"); err != nil {
		t.Fatal(err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	got := back.Attachments()
	if len(got) != 1 || got[0].Name != "notes.txt" || string(got[0].Data) != "hello" {
		t.Fatalf("got %+v", got)
	}
}

func TestTwoFilesUnderOneName(t *testing.T) {
	// A document that has lost one of them. Refusing beats replacing quietly.
	d := New()
	d.Blank(100, 200)
	if err := d.Attach("a.txt", []byte("one"), ""); err != nil {
		t.Fatal(err)
	}
	err := d.Attach("a.txt", []byte("two"), "")
	if err == nil {
		t.Fatal("the second one went in")
	}
	if !strings.Contains(err.Error(), "already carries") {
		t.Errorf("it said %q", err)
	}
	if err := d.Attach("", []byte("x"), ""); err == nil {
		t.Error("a file with no name went in")
	}
}

func TestAFileIsTakenOut(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *Doc
	}{
		{"one that was put in here", func(t *testing.T) *Doc {
			d := New()
			d.Blank(100, 200)
			d.Attach("gone.txt", []byte("x"), "")
			return d
		}},
		{"one the document came in with", func(t *testing.T) *Doc {
			d, err := Open(withFile(t, "gone.txt", "", "x"))
			if err != nil {
				t.Fatal(err)
			}
			return d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.build(t)
			if !d.Detach("gone.txt") {
				t.Fatal("it said there was no such file")
			}
			out, err := d.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			back, err := Open(out)
			if err != nil {
				t.Fatal(err)
			}
			if got := back.Attachments(); len(got) != 0 {
				t.Errorf("%d files still there: %+v", len(got), got)
			}
		})
	}
}

func TestTakingOutWhatIsNotThere(t *testing.T) {
	d := New()
	d.Blank(100, 200)
	if d.Detach("never.txt") {
		t.Error("it said it removed a file that was not there")
	}
}

func TestASanitisedFileCarriesNothingInside(t *testing.T) {
	// A sanitised file leaves behind what runs and what travels. A file inside
	// a document is the thing that is meant.
	d, err := Open(withFile(t, "source.csv", "", "a,b\n"))
	if err != nil {
		t.Fatal(err)
	}
	d.Sanitize()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Attachments(); len(got) != 0 {
		t.Errorf("a sanitised file still carries %+v", got)
	}
}

func TestFilesFromSeveralDocumentsAreAllCarried(t *testing.T) {
	// Two forms cannot be merged and two catalogues cannot be chosen between,
	// but two sets of files can simply both be carried: a file belongs to no
	// page, so it cannot be in conflict with anything.
	a, err := Open(withFile(t, "one.txt", "", "1"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(withFile(t, "two.txt", "", "2"))
	if err != nil {
		t.Fatal(err)
	}
	joined := Merge(a, b)
	out, err := joined.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	got := back.Attachments()
	if len(got) != 2 {
		t.Fatalf("%d files came through a merge, want 2: %+v", len(got), got)
	}
}

func TestANameTreeAsFilesActuallyComeIn(t *testing.T) {
	// A name tree is a sorted map spread over a tree of nodes, so that a
	// reader can find a name without reading all of them. A document written
	// by another tool nests them, and one written badly puts things in that
	// are not file specifications at all.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page 1")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": reader.Array{page},
		"Count":    reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)}})

	good := w.Add(reader.Dict{"Type": reader.Name("Filespec"), "F": reader.String("good.txt"),
		"EF": reader.Dict{"F": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("kept")})}})
	// Only /UF, which is the Unicode twin and holds the same stream when both
	// are there — and sometimes the only one.
	unicodeOnly := w.Add(reader.Dict{"Type": reader.Name("Filespec"), "F": reader.String("uf.txt"),
		"EF": reader.Dict{"UF": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("also kept")})}})
	noEF := w.Add(reader.Dict{"Type": reader.Name("Filespec"), "F": reader.String("empty.txt")})
	badStream := w.Add(reader.Dict{"Type": reader.Name("Filespec"), "F": reader.String("bad.txt"),
		"EF": reader.Dict{"F": w.Add(&reader.Stream{
			Dict: reader.Dict{"Filter": reader.Name("NoSuchDecode")}, Raw: []byte("x")})}})

	// A leaf whose entries include things that are not specifications, and a
	// key that is not a string.
	leaf := w.Add(reader.Dict{"Names": reader.Array{
		reader.String("good.txt"), good,
		reader.String("uf.txt"), unicodeOnly,
		reader.String("empty.txt"), noEF,
		reader.String("bad.txt"), badStream,
		reader.String("nonsense.txt"), reader.Integer(7),
		reader.Integer(1), good,
	}})
	// Nested one level, with a kid that is not a node.
	tree := w.Add(reader.Dict{"Kids": reader.Array{leaf, reader.Integer(3)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"Names": w.Add(reader.Dict{"EmbeddedFiles": tree})})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Attachments()
	names := map[string]string{}
	for _, a := range got {
		names[a.Name] = string(a.Data)
	}
	if names["good.txt"] != "kept" {
		t.Errorf("the good one came back as %q", names["good.txt"])
	}
	if names["uf.txt"] != "also kept" {
		t.Errorf("the Unicode-only one came back as %q", names["uf.txt"])
	}
	// A specification with nowhere to point, or pointing at bytes no filter
	// reads, is not a file: it is left out rather than handed over empty.
	for _, absent := range []string{"empty.txt", "bad.txt", "nonsense.txt"} {
		if _, there := names[absent]; there {
			t.Errorf("%s came back as a file", absent)
		}
	}
}

func TestATreeThatGoesOnForever(t *testing.T) {
	// A tree that holds itself is a tree with no leaves, and following it is a
	// way of never coming back.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page 1")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": reader.Array{page},
		"Count":    reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)}})
	loop := w.Reserve()
	w.Put(loop, reader.Dict{"Kids": reader.Array{loop}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"Names": w.Add(reader.Dict{"EmbeddedFiles": loop})})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Attachments(); len(got) != 0 {
		t.Errorf("a tree with no leaves yielded %d files", len(got))
	}
}

func TestADocumentWithNoCatalogueToRead(t *testing.T) {
	// readAttachments is given whatever the pages came from, and a document
	// can be missing every step of the way to its files.
	d, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Attachments(); len(got) != 0 {
		t.Errorf("a document with no files yielded %+v", got)
	}
}

func TestAFileStoredInAFormatThisDoesNotUnpack(t *testing.T) {
	// A chain that stops at a filter nothing here reads hands back the bytes as
	// they were STORED and names what they are still in. Handing those over as
	// the file gives somebody a spreadsheet that is not one, which is worse
	// than saying there is no file.
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	page := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("page 1")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": reader.Array{page},
		"Count":    reader.Integer(1),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(100), reader.Integer(200)}})
	spec := w.Add(reader.Dict{"Type": reader.Name("Filespec"), "F": reader.String("photo.jpg"),
		"EF": reader.Dict{"F": w.Add(&reader.Stream{
			Dict: reader.Dict{"Filter": reader.Name("DCTDecode")}, Raw: []byte{0xff, 0xd8, 0xff}})}})
	tree := w.Add(reader.Dict{"Names": reader.Array{reader.String("photo.jpg"), spec}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef,
		"Names": w.Add(reader.Dict{"EmbeddedFiles": tree})})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Attachments(); len(got) != 0 {
		t.Errorf("bytes still in a filter were handed over as a file: %+v", got)
	}
}
