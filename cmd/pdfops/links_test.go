package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/reader"
)

// linkFixture writes a two-page document whose first page carries one link to
// page 2, one to an address, and one that leads nowhere a reader can be sent.
func linkFixture(t *testing.T) string {
	t.Helper()
	w := reader.NewWriter("1.7")
	pagesRef := w.Reserve()
	refs := [2]reader.Ref{w.Reserve(), w.Reserve()}
	for i, ref := range refs {
		page := reader.Dict{
			"Type": reader.Name("Page"), "Parent": pagesRef,
			"Contents": w.Add(&reader.Stream{Dict: reader.Dict{}, Raw: []byte(fmt.Sprintf("page %d", i+1))}),
		}
		if i == 0 {
			page["Annots"] = reader.Array{
				w.Add(reader.Dict{"Subtype": reader.Name("Link"),
					"Rect": reader.Array{reader.Integer(72), reader.Integer(648),
						reader.Integer(232), reader.Integer(660)},
					"Dest": reader.Array{refs[1], reader.Name("Fit")}}),
				w.Add(reader.Dict{"Subtype": reader.Name("Link"),
					"Rect": reader.Array{reader.Integer(72), reader.Integer(600),
						reader.Integer(180), reader.Integer(612)},
					"A": reader.Dict{"S": reader.Name("URI"),
						"URI": reader.String("https://example.org/")}}),
				w.Add(reader.Dict{"Subtype": reader.Name("Link"),
					"Rect": reader.Array{reader.Integer(90), reader.Integer(120),
						reader.Integer(300), reader.Integer(132)},
					"A": reader.Dict{"S": reader.Name("Launch"), "F": reader.String("something.exe")}}),
			}
		}
		w.Put(ref, page)
	}
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"),
		"Kids": reader.Array{refs[0], refs[1]}, "Count": reader.Integer(2),
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(612), reader.Integer(792)}})
	root := w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})
	out, err := w.Finish(reader.Dict{"Root": root})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "links.pdf")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLinksListsWhereEachOneLeads(t *testing.T) {
	code, out, errOut := exec("links", linkFixture(t))
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	want := "1\t72,648,232,660\tpage 2\n" +
		"1\t72,600,180,612\thttps://example.org/\n" +
		"1\t90,120,300,132\tnowhere\n"
	if out != want {
		t.Errorf("it printed\n%q\nwant\n%q", out, want)
	}
}

// ⛔ The one that leads nowhere is in the listing. Printing only the links that
// work would answer "two" about a file carrying three, and somebody auditing a
// document for what it points at would never learn the third exists.
func TestALinkThatLeadsNowhereIsPrintedAndSaysSo(t *testing.T) {
	_, out, _ := exec("links", linkFixture(t))
	if n := strings.Count(out, "\n"); n != 3 {
		t.Fatalf("%d lines for a file carrying three links:\n%s", n, out)
	}
	if !strings.Contains(out, "nowhere") {
		t.Error("the link that goes nowhere is missing from the listing")
	}
}

func TestLinksTakesAPageRange(t *testing.T) {
	code, out, _ := exec("links", "-pages", "2", linkFixture(t))
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if out != "the file carries no links\n" {
		t.Errorf("page 2 carries none, and it printed %q", out)
	}
}

func TestLinksOnADocumentWithNone(t *testing.T) {
	code, out, _ := exec("links", fixture(t, 2))
	if code != 0 || out != "the file carries no links\n" {
		t.Errorf("code %d, out %q", code, out)
	}
}

func TestLinksRefusesWhatItCannotRead(t *testing.T) {
	for _, args := range [][]string{
		{"links"},
		{"links", "-nosuchflag", linkFixture(t)},
		{"links", "-pages", "9-12", linkFixture(t)},
		{"links", filepath.Join(t.TempDir(), "absent.pdf")},
	} {
		if code, _, _ := exec(args...); code == 0 {
			t.Errorf("%v was accepted", args)
		}
	}
}
