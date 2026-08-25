package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
)

// A command is one verb of the tool.
type command struct {
	name  string
	usage string
	about string
	run   func(c *context, args []string) error
}

// A context carries what every command needs: where to write, and the shared
// flags.
type context struct {
	out      io.Writer
	errOut   io.Writer
	password string
}

// commands lists the verbs in the order the help prints them.
var commands = []command{
	{"merge", "<out.pdf> <in.pdf> [in.pdf …]", "join files, in the order given", runMerge},
	{"select", "-pages <range> <in.pdf> <out.pdf>", "keep the pages a range names, in its order", runSelect},
	{"delete", "-pages <range> <in.pdf> <out.pdf>", "drop the pages a range names", runDelete},
	{"reverse", "<in.pdf> <out.pdf>", "put the pages in the opposite order", runReverse},
	{"rotate", "-pages <range> -by <degrees> <in.pdf> <out.pdf>", "turn pages by a multiple of ninety", runRotate},
	{"crop", "-pages <range> -box <l,b,r,t> <in.pdf> <out.pdf>", "set the visible area, in points", runCrop},
	{"split", "-every <n> <in.pdf> <out-directory>", "cut into files of at most n pages", runSplit},
	{"info", "<in.pdf>", "print what the file says about itself", runInfo},
	{"strip", "<in.pdf> <out.pdf>", "write the file without its metadata", runStrip},
}

// run is the whole program, so that the tests can drive it.
func run(args []string, out, errOut io.Writer) int {
	c := &context{out: out, errOut: errOut}
	rest, err := globalFlags(c, args, errOut)
	if err != nil {
		return usage(errOut, err)
	}
	if len(rest) == 0 {
		return usage(errOut, nil)
	}
	for _, cmd := range commands {
		if cmd.name != rest[0] {
			continue
		}
		if err := cmd.run(c, rest[1:]); err != nil {
			fmt.Fprintf(errOut, "pdfops %s: %v\n", cmd.name, err)
			return 1
		}
		return 0
	}
	return usage(errOut, fmt.Errorf("no such command %q", rest[0]))
}

// globalFlags reads the options every command shares.
func globalFlags(c *context, args []string, errOut io.Writer) ([]string, error) {
	fs := flag.NewFlagSet("pdfops", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.password, "password", "", "the password of an encrypted file")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

// usage prints what the tool can do.
func usage(w io.Writer, err error) int {
	if err != nil {
		fmt.Fprintf(w, "pdfops: %v\n\n", err)
	}
	fmt.Fprintln(w, "pdfops — do something to a PDF you already have.")
	fmt.Fprint(w, "\nusage: pdfops [-password <password>] <command> [options]\n\n")
	width := 0
	for _, c := range commands {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	for _, c := range commands {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.name, c.about)
		fmt.Fprintf(w, "  %-*s    pdfops %s %s\n", width, "", c.name, c.usage)
	}
	fmt.Fprintln(w, "\nA page range is written 1-3,7,10- and may say all, even, odd or last.")
	if err != nil {
		return 2
	}
	return 2
}

// open reads a document from disk.
func (c *context) open(path string) (*ops.Doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ops.OpenWithPassword(b, c.password)
}

// save writes a document to disk.
func save(d *ops.Doc, path string) error {
	out, err := d.Bytes()
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// flags builds a flag set that reports its errors rather than printing them.
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// wantArgs checks the number of positional arguments left.
func wantArgs(fs *flag.FlagSet, n int, shape string) error {
	if fs.NArg() != n {
		return fmt.Errorf("expected %s", shape)
	}
	return nil
}

func runMerge(c *context, args []string) error {
	fs := flags("merge")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("expected <out.pdf> <in.pdf> [in.pdf …]")
	}
	var docs []*ops.Doc
	for _, in := range fs.Args()[1:] {
		d, err := c.open(in)
		if err != nil {
			return err
		}
		docs = append(docs, d)
	}
	return save(ops.Merge(docs...), fs.Arg(0))
}

func runSelect(c *context, args []string) error {
	fs := flags("select")
	pages := fs.String("pages", "all", "the pages to keep")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.Select(*pages); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runDelete(c *context, args []string) error {
	fs := flags("delete")
	pages := fs.String("pages", "", "the pages to drop")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.Delete(*pages); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runReverse(c *context, args []string) error {
	fs := flags("reverse")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	d.Reverse()
	return save(d, fs.Arg(1))
}

func runRotate(c *context, args []string) error {
	fs := flags("rotate")
	pages := fs.String("pages", "all", "the pages to turn")
	by := fs.Int("by", 90, "degrees, a multiple of ninety")
	absolute := fs.Bool("absolute", false, "set the angle instead of adding to it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if *absolute {
		err = d.SetRotation(*pages, *by)
	} else {
		err = d.Rotate(*pages, *by)
	}
	if err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runCrop(c *context, args []string) error {
	fs := flags("crop")
	pages := fs.String("pages", "all", "the pages to crop")
	boxSpec := fs.String("box", "", "left,bottom,right,top in points")
	media := fs.Bool("media", false, "set the paper size rather than the visible area")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	box, err := parseBox(*boxSpec)
	if err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if *media {
		err = d.Resize(*pages, box)
	} else {
		err = d.Crop(*pages, box)
	}
	if err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// parseBox reads "l,b,r,t".
func parseBox(s string) ([4]float64, error) {
	var box [4]float64
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return box, fmt.Errorf("a box is four numbers, left,bottom,right,top")
	}
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return box, fmt.Errorf("%q is not a number", p)
		}
		box[i] = v
	}
	return box, nil
}

func runSplit(c *context, args []string) error {
	fs := flags("split")
	every := fs.Int("every", 1, "how many pages per file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out-directory>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	parts, err := d.Split(*every)
	if err != nil {
		return err
	}
	dir := fs.Arg(1)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	base := strings.TrimSuffix(filepath.Base(fs.Arg(0)), filepath.Ext(fs.Arg(0)))
	for i, part := range parts {
		name := filepath.Join(dir, fmt.Sprintf("%s-%03d.pdf", base, i+1))
		if err := save(part, name); err != nil {
			return err
		}
		fmt.Fprintln(c.out, name)
	}
	return nil
}

func runInfo(c *context, args []string) error {
	fs := flags("info")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "<in.pdf>"); err != nil {
		return err
	}
	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	src, err := reader.OpenWithPassword(b, c.password)
	if err != nil {
		return err
	}
	d := ops.FromDocument(src)
	fmt.Fprintf(c.out, "version    %s\n", src.Version())
	fmt.Fprintf(c.out, "pages      %d\n", d.PageCount())
	fmt.Fprintf(c.out, "encrypted  %v\n", src.Encrypted())
	fmt.Fprintf(c.out, "repaired   %v\n", src.Repaired())
	info := d.Info()
	keys := make([]string, 0, len(info))
	for k := range info {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := reader.ToString(info[reader.Name(k)]); ok {
			fmt.Fprintf(c.out, "%-10s %s\n", strings.ToLower(k), s)
		}
	}
	for i := 1; i <= d.PageCount(); i++ {
		// Every page number here comes from the document itself, so neither
		// of these can fail.
		rot, _ := d.Rotation(i)
		page, _ := src.Page(i)
		mb, _ := src.Resolve(page.Get("MediaBox"))
		fmt.Fprintf(c.out, "page %-5d %s rotate %d\n", i, reader.FormatObject(mb), rot)
	}
	return nil
}

func runStrip(c *context, args []string) error {
	fs := flags("strip")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	d.ClearInfo()
	return save(d, fs.Arg(1))
}
