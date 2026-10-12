package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-pdfkit/document"
	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
	"github.com/go-richdoc/markdown"
	"github.com/go-richdoc/richdoc"
)

// runMarkdown reads the pages back as a document and writes them as Markdown,
// or as one record per page for something that is going to index them.
//
// ⛔ A PDF has no headings. What this writes is an inference from how big the
// glyphs were and where they sat, and [document] says what it will and will
// not see. The one thing worth knowing before piping it anywhere: a page laid
// out in columns, or held together by ruled lines, is not read as either.
func runMarkdown(c *context, args []string) error {
	fs := flags("markdown")
	spec := fs.String("pages", "all", "which pages to read")
	asJSON := fs.Bool("json", false,
		"write one record per page, with the document's own metadata, for something that is going to index it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "<in.pdf>"); err != nil {
		return err
	}
	src, err := c.read(fs.Arg(0))
	if err != nil {
		return err
	}
	wanted, err := ops.ParseRange(*spec, src.PageCount())
	if err != nil {
		return err
	}
	pages := chosen(document.Pages(src), wanted)
	if *asJSON {
		return writePageRecords(c, src, fs.Arg(0), pages)
	}
	return writeMarkdown(c, pages)
}

// chosen keeps the pages a range named, in the document's order.
//
// ⛔ The range is applied AFTER the document is read, never before. What counts
// as a heading is decided by the sizes the whole document uses, so reading
// page nine alone would give it different headings from reading all of them —
// and a caller doing a document a page at a time would get a different answer
// for every page.
func chosen(pages []document.Page, wanted []int) []document.Page {
	keep := make(map[int]bool, len(wanted))
	for _, n := range wanted {
		keep[n] = true
	}
	var out []document.Page
	for _, p := range pages {
		if keep[p.Number] {
			out = append(out, p)
		}
	}
	return out
}

// writeMarkdown writes the pages as one document, which is what somebody
// reading it wants: a page boundary in the middle of a sentence is an artefact
// of printing, not of the text.
func writeMarkdown(c *context, pages []document.Page) error {
	doc := &richdoc.Document{}
	for _, p := range pages {
		doc.Blocks = append(doc.Blocks, p.Blocks...)
	}
	// markdown.Write returns an error for the sake of its signature and never
	// a non-nil one: every path through it builds a string and returns nil.
	// Checking it here would add a path no test can reach, so the coupling is
	// named instead — and it is a coupling to a pinned version.
	b, _ := markdown.Write(doc)
	_, err := c.out.Write(b)
	return err
}

// A pageRecord is one page as something that is going to index it reads it:
// the text, and where the text came from.
//
// ⛔ The shape is the one a retrieval pipeline already expects — text plus a
// metadata map, which is LlamaIndex's document schema and what PyMuPDF4LLM's
// LlamaMarkdownReader produces. Inventing a shape here would mean every caller
// writing an adapter for it.
type pageRecord struct {
	Text     string            `json:"text"`
	Metadata map[string]string `json:"metadata"`
}

func writePageRecords(c *context, src *reader.Document, path string, pages []document.Page) error {
	// An empty result is `[]`, never `null`: a caller testing the length of
	// what came back should not have to test for nothing first.
	out := []pageRecord{}
	info := documentMetadata(src)
	for _, p := range pages {
		b, _ := markdown.Write(&richdoc.Document{Blocks: p.Blocks})
		meta := map[string]string{
			// file_path, page_count and page_number are the three keys
			// LlamaMarkdownReader adds to a document's own metadata, spelled
			// the way it spells them.
			"file_path":   filepath.Base(path),
			"page_count":  fmt.Sprint(src.PageCount()),
			"page_number": fmt.Sprint(p.Number),
		}
		for k, v := range info {
			meta[k] = v
		}
		out = append(out, pageRecord{Text: string(b), Metadata: meta})
	}
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// documentMetadata is what the file says about itself, with the keys lowered
// so that a caller does not have to know that PDF spells them /Title and
// /CreationDate.
//
// An entry that is not text is left out rather than rendered: the information
// dictionary is the one place a document is free to put anything at all, and
// "author: 4" is worse than no author.
func documentMetadata(src *reader.Document) map[string]string {
	out := map[string]string{}
	d := ops.FromDocument(src)
	info := d.Info()
	keys := make([]string, 0, len(info))
	for k := range info {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	for _, k := range keys {
		// ⛔ TextString and not ToString: the bytes of a text string are not
		// text. A title written in UTF-16 — which is most of what a modern
		// producer writes — comes out of ToString as "\xfe\xffL\x00a\x00…".
		if s, ok := reader.TextString(info[reader.Name(k)]); ok && s != "" {
			out[strings.ToLower(k)] = s
		}
	}
	return out
}
