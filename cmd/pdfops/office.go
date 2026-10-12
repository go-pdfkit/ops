package main

import (
	"fmt"
	"os"

	"github.com/go-ooxml/ooxml"
	"github.com/go-pdfkit/document"
	"github.com/go-pdfkit/ops"
	"github.com/go-richdoc/richdoc"
)

// runWord writes the pages back out as a Word document.
//
// ⛔ What comes out is the DOCUMENT this read off the pages — headings,
// paragraphs, lists and tables — and not the pages themselves. The fonts, the
// margins, the line breaks and the page breaks are the typesetter's work, and
// a .docx that reproduced those would be a picture of a PDF rather than
// something anybody can edit. [document] says what it reads and what it does
// not.
func runWord(c *context, args []string) error {
	return runOffice(c, args, "word", ooxml.WriteWord)
}

// runExcel writes the pages' tables back out as a workbook.
//
// ⛔ Only the tables, one sheet each. A spreadsheet is a grid of cells and a
// document is not, so the prose around a table has nowhere to go.
func runExcel(c *context, args []string) error {
	return runOffice(c, args, "excel", ooxml.WriteWorkbook)
}

// runOffice is both of the above: the same reading, written by a different
// writer. Keeping them one function is why -pages means the same thing in
// both, and why a defect found in one is fixed in both.
func runOffice(c *context, args []string, verb string, write func(*richdoc.Document) ([]byte, error)) error {
	fs := flags(verb)
	spec := fs.String("pages", "all", "which pages to read")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out>"); err != nil {
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
	// The range is applied AFTER the document is read, for the reason
	// [chosen] gives: what counts as a heading is decided by the sizes the
	// whole document uses.
	doc := &richdoc.Document{}
	for _, p := range chosen(document.Pages(src), wanted) {
		doc.Blocks = append(doc.Blocks, p.Blocks...)
	}
	out, err := write(doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(fs.Arg(1), out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s\t%d bytes\n", fs.Arg(1), len(out))
	return nil
}
