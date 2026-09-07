package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// dynamicXFA writes a file of the kind this verb exists for: pages that are a
// placeholder, and a form that lives only in the XML beside them.
func dynamicXFA(t *testing.T, template string) string {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0),
			reader.Integer(200), reader.Integer(200)},
		"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte("")})})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{
		"Type": reader.Name("Catalog"), "Pages": pagesRef,
		// This is the document saying its pages are not the form, and it is
		// the only thing in the file that distinguishes the two kinds.
		"NeedsRendering": reader.Bool(true),
		"AcroForm": w.Add(reader.Dict{"Fields": reader.Array{},
			"XFA": reader.Array{
				reader.String("template"),
				w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(template)}),
			}})})})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "dynamic.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const cliTemplate = `<template><subform name="form1" layout="tb">
  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
  <subform name="Body">
    <draw name="Title" w="300pt" h="20pt"><value><text>Licence application</text></value></draw>
    <draw name="Gone" presence="hidden" w="100pt" h="20pt"><value><text>gone</text></value></draw>
    <field name="Broken" w="10px" h="10pt"/>
  </subform></subform></template>`

func TestXFAVerb(t *testing.T) {
	in := dynamicXFA(t, cliTemplate)
	out := filepath.Join(t.TempDir(), "drawn.pdf")
	code, printed, errOut := exec("xfa", in, out)
	if code != 0 {
		t.Fatalf("xfa said %d: %s", code, errOut)
	}
	// What it drew, what the form hides, and what it could not place: the
	// answer "this file is now readable" is only worth having with the size
	// of what is missing beside it.
	for _, want := range []string{"1 sheets", "1 elements drawn", "the form hides", "not drawn: form1.Body.Broken"} {
		if !strings.Contains(printed, want) {
			t.Errorf("the report does not say %q:\n%s", want, printed)
		}
	}
	if pages := contentsOf(t, out); len(pages) != 1 {
		t.Fatalf("%d pages were written", len(pages))
	} else if !strings.Contains(pages[0], "Licence application") {
		t.Errorf("the page does not hold the form's own words:\n%s", pages[0])
	}
}

func TestXFAVerbOnAFileItCannotDraw(t *testing.T) {
	in := dynamicXFA(t, cliTemplate)
	for _, c := range []struct {
		name string
		args []string
	}{
		{"no arguments", []string{"xfa"}},
		{"one argument", []string{"xfa", in}},
		{"a file that is not there", []string{"xfa", "nowhere.pdf", filepath.Join(t.TempDir(), "o.pdf")}},
		{"an option it has not got", []string{"xfa", "-nonsense", in, filepath.Join(t.TempDir(), "o.pdf")}},
		{"nowhere to write", []string{"xfa", in, filepath.Join(t.TempDir(), "no", "such", "o.pdf")}},
		{"a file with no form at all", []string{"xfa", pageWithText(t), filepath.Join(t.TempDir(), "o.pdf")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if code, _, _ := exec(c.args...); code == 0 {
				t.Error("it went through and should not have")
			}
		})
	}
}

func TestFieldsVerbPointsAtTheOtherVerbWhenThePagesAreAPlaceholder(t *testing.T) {
	// A dynamic file's standard form is nearly empty and its real one is the
	// XML, so listing the first without saying so answers the wrong question.
	_, printed, _ := exec("fields", dynamicXFA(t, cliTemplate))
	if !strings.Contains(printed, "pdfops xfa") {
		t.Errorf("it does not point at the verb that draws the real form:\n%s", printed)
	}
}

func TestXFAVerbCountsThePicturesItDrew(t *testing.T) {
	// A form's printed background is a picture, and for one of the corpus's
	// fourteen it is the whole form — so how many were drawn belongs in the
	// line that says what came out.
	img := image.NewGray(image.Rect(0, 0, 16, 16))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	template := `<template><subform name="form1" layout="tb">
	  <pageSet><pageArea name="Page1"><medium long="792pt" short="612pt"/>
	    <contentArea x="0pt" y="0pt" w="500pt" h="500pt"/></pageArea></pageSet>
	  <subform name="Body"><draw name="Logo" w="100pt" h="100pt">
	    <value><image contentType="image/jpeg">` +
		base64.StdEncoding.EncodeToString(buf.Bytes()) + `</image></value>
	  </draw></subform></subform></template>`
	code, printed, errOut := exec("xfa", dynamicXFA(t, template),
		filepath.Join(t.TempDir(), "drawn.pdf"))
	if code != 0 {
		t.Fatalf("xfa said %d: %s", code, errOut)
	}
	if !strings.Contains(printed, "1 of them pictures") {
		t.Errorf("it does not say what it drew:\n%s", printed)
	}
}
