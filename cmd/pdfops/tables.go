package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pdfkit/extract"
	"github.com/go-pdfkit/ops"
	"github.com/go-richdoc/markdown"
	"github.com/go-richdoc/richdoc"
)

// runTables finds the grids a page's text lines up into and writes them out.
//
// A page holds no table — it holds text put at places — so what this reports
// is an inference, and [extract.Tables] says what it will and will not see.
// Worth knowing before piping the output anywhere: ruled lines are not read,
// so a table held apart by rules rather than by whitespace is not found.
func runTables(c *context, args []string) error {
	fs := flags("tables")
	spec := fs.String("pages", "all", "which pages to look at")
	as := fs.String("as", "csv", "how to write them: csv, json or md")
	to := fs.String("to", "", "a directory to write one file per table into, rather than all of them together")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "<in.pdf>"); err != nil {
		return err
	}
	form, err := formNamed(*as)
	if err != nil {
		return err
	}
	src, err := c.read(fs.Arg(0))
	if err != nil {
		return err
	}
	pages, err := ops.ParseRange(*spec, src.PageCount())
	if err != nil {
		return err
	}
	var found []extract.Table
	var unread []int
	for _, page := range pages {
		tables, err := extract.Tables(src, page)
		if err != nil {
			// ⛔ Kept and said at the end rather than swallowed. A page that
			// cannot be read comes back here as no tables, which is
			// indistinguishable from a page that holds none — and a caller
			// piping this into a spreadsheet would never find out.
			unread = append(unread, page)
			continue
		}
		found = append(found, tables...)
	}
	if *to != "" {
		err = writeTablesSeparately(c, found, form, *to)
	} else {
		err = writeTablesTogether(c, found, form)
	}
	if err != nil {
		return err
	}
	if len(unread) > 0 {
		fmt.Fprintf(c.errOut, "%d page(s) could not be read, so nothing is "+
			"reported for them: %s\n", len(unread), pageList(unread))
	}
	return nil
}

// pageList writes page numbers the way somebody would read them back.
func pageList(pages []int) string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = fmt.Sprint(p)
	}
	return strings.Join(out, ", ")
}

// A tableForm is one of the ways tables can be written out: the extension a
// file of them takes, and how they are turned into bytes.
//
// Each renderer takes the writer and returns its error, rather than returning
// bytes to be checked here: a check here would be a path no test can reach,
// since nothing between a table and its bytes can fail on its own.
type tableForm struct {
	suffix string
	render func(io.Writer, []extract.Table) error
}

// formNamed is the form that name asks for, or an error saying what the
// choices are.
func formNamed(as string) (tableForm, error) {
	switch strings.ToLower(as) {
	case "csv":
		return tableForm{".csv", tablesAsCSV}, nil
	case "json":
		return tableForm{".json", tablesAsJSON}, nil
	case "md", "markdown":
		return tableForm{".md", tablesAsMarkdown}, nil
	}
	return tableForm{}, fmt.Errorf("-as %s: write csv, json or md", as)
}

// writeTablesTogether writes every table into one piece of output.
func writeTablesTogether(c *context, found []extract.Table, form tableForm) error {
	if len(found) == 0 {
		fmt.Fprintln(c.out, "no tables")
		return nil
	}
	return form.render(c.out, found)
}

// writeTablesSeparately writes one file per table, named the way the images
// verb names its own: the page it came from, and its place on that page.
func writeTablesSeparately(c *context, found []extract.Table, form tableForm, dir string) error {
	if len(found) == 0 {
		fmt.Fprintln(c.out, "no tables")
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	page, onPage := 0, 0
	for _, t := range found {
		if t.Page != page {
			page, onPage = t.Page, 0
		}
		onPage++
		var b bytes.Buffer
		if err := form.render(&b, []extract.Table{t}); err != nil {
			return err
		}
		name := fmt.Sprintf("page%03d-%02d%s", t.Page, onPage, form.suffix)
		if err := os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "%s\t%d rows\t%d columns\tat %.1f,%.1f\n",
			name, len(t.Rows), len(t.Rows[0]), t.X, t.Y)
	}
	return nil
}

// tablesAsCSV writes the rows of every table, RFC 4180 quoted, with a blank
// line between one table and the next so a reader can see where each ended.
//
// The writes are not checked one at a time because [csv.Writer] keeps the
// first error that happened and hands it over at the end, which is the same
// answer with none of the branches.
func tablesAsCSV(out io.Writer, found []extract.Table) error {
	w := csv.NewWriter(out)
	for i, t := range found {
		if i > 0 {
			w.Write(nil)
		}
		w.WriteAll(t.Rows)
	}
	w.Flush()
	return w.Error()
}

// jsonTable is one table as something that is going to parse it reads it:
// where it was found as well as what it says, because a caller pulling tables
// out of a long document needs to know which page to look at.
type jsonTable struct {
	Page   int        `json:"page"`
	X      float64    `json:"x"`
	Y      float64    `json:"y"`
	Width  float64    `json:"width"`
	Height float64    `json:"height"`
	Rows   [][]string `json:"rows"`
}

func tablesAsJSON(out io.Writer, found []extract.Table) error {
	list := make([]jsonTable, len(found))
	for i, t := range found {
		list[i] = jsonTable{t.Page, t.X, t.Y, t.Width, t.Height, t.Rows}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

// tablesAsMarkdown writes GFM pipe tables, through go-richdoc so that a cell
// holding a pipe is escaped by the code that already knew it had to be.
//
// ⛔ The first row becomes the header. A pipe table has nowhere to put a table
// with no header row, and a grid found by looking at alignment does not know
// whether its first row is one — so this picks the reading that renders, and
// says here that it picked.
func tablesAsMarkdown(out io.Writer, found []extract.Table) error {
	doc := &richdoc.Document{}
	for _, t := range found {
		doc.Blocks = append(doc.Blocks, richdoc.Table{
			Header: cellsOf(t.Rows[0]),
			Rows:   rowsOf(t.Rows[1:]),
		})
	}
	// markdown.Write returns an error for the sake of its signature and never
	// a non-nil one: every path through it builds a string and returns nil.
	// Checking it here would add a path no test can reach, so the coupling is
	// named instead — and it is a coupling to a pinned version.
	b, _ := markdown.Write(doc)
	_, err := out.Write(b)
	return err
}

// cellsOf turns a row of text into cells.
func cellsOf(row []string) []richdoc.Cell {
	out := make([]richdoc.Cell, len(row))
	for i, s := range row {
		// Inlines alone, and Blocks left nil: one line of text is one
		// paragraph, which is the case richdoc.Cell says to do this for.
		out[i] = richdoc.Cell{Inlines: []richdoc.Inline{richdoc.Text{Value: s}}}
	}
	return out
}

func rowsOf(rows [][]string) [][]richdoc.Cell {
	out := make([][]richdoc.Cell, len(rows))
	for i, row := range rows {
		out[i] = cellsOf(row)
	}
	return out
}
