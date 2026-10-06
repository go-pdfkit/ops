package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-pdfkit/extract"
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
	{"resize", "[-pages <range>] (-box <l,b,r,t> | -to <name> [-landscape]) <in.pdf> <out.pdf>", "set how big the pages are", runResize},
	{"move", "-from <page> -to <page> <in.pdf> <out.pdf>", "take a page out and put it back elsewhere", runMove},
	{"split", "-every <n> <in.pdf> <out-directory>", "cut into files of at most n pages", runSplit},
	{"nup", "-n <count> <in.pdf> <out.pdf>", "lay several pages on each sheet", runNUp},
	{"booklet", "<in.pdf> <out.pdf>", "order and lay out for saddle-stitch printing", runBooklet},
	{"interleave", "<out.pdf> <in.pdf> <in.pdf> [in.pdf …]", "take one page from each in turn", runInterleave},
	{"onepage", "<in.pdf> <out.pdf>", "put every page onto a single page", runOnePage},
	{"poster", "-across <n> -down <n> <in.pdf> <out.pdf>", "cut each page into tiles, to print larger than the paper", runPoster},
	{"overlay", "-with <mark.pdf> <in.pdf> <out.pdf>", "draw another file over these pages", runOverlay},
	{"underlay", "-with <mark.pdf> <in.pdf> <out.pdf>", "draw another file beneath these pages", runUnderlay},
	{"blank", "-before <page> <in.pdf> <out.pdf>", "insert an empty page", runBlank},
	{"watermark", "-text <words> <in.pdf> <out.pdf>", "draw pale slanted text across the pages", runWatermark},
	{"number", "-format <text> <in.pdf> <out.pdf>", "write page numbers at the foot", runNumber},
	{"bates", "-prefix <text> -start <n> <in.pdf> <out.pdf>", "stamp a running serial, exhibit style", runBates},
	{"stamp", "-text <words> -at <place> <in.pdf> <out.pdf>", "draw a line of text where you say", runStamp},
	{"info", "<in.pdf>", "print what the file says about itself", runInfo},
	{"metadata", "(-set <Key=value> … | -clear) <in.pdf> <out.pdf>", "write what the file says about itself", runMetadata},
	{"version", "-set <version> <in.pdf> <out.pdf>", "set the PDF version the file declares", runVersion},
	{"strip", "[-annotations] [-bookmarks] <in.pdf> <out.pdf>", "write the file without its metadata", runStrip},
	{"sanitize", "<in.pdf> <out.pdf>", "remove what runs rather than shows: scripts, launching, embedded files", runSanitize},
	{"outline", "(-drop | -from <toc.txt>) <in.pdf> <out.pdf>", "replace or remove the bookmarks", runOutline},
	{"flatten", "<in.pdf> <out.pdf>", "draw the annotations into the page and drop them", runFlatten},
	{"compress", "<in.pdf> <out.pdf>", "pack the objects into compressed streams", runCompress},
	{"encrypt", "-user <password> [-owner <password>] [-allow <what>] [-aes128] <in.pdf> <out.pdf>", "protect the file with a password", runEncrypt},
	{"decrypt", "<in.pdf> <out.pdf>", "write the file without its protection", runDecrypt},
	{"permissions", "<in.pdf>", "say how the file is protected and what it allows", runPermissions},
	{"text", "[-pages <range>] [-layout] [-json] <in.pdf>", "read the text off the pages", runText},
	{"images", "[-pages <range>] <in.pdf> <out-directory>", "write out the pictures the pages place", runImages},
	{"attachments", "[-to <directory>] <in.pdf>", "list the files the document carries, or write them out", runAttachments},
	{"attach", "-file <path> [-as <name>] [-description <text>] <in.pdf> <out.pdf>", "carry a file inside the document", runAttach},
	{"detach", "-name <name> <in.pdf> <out.pdf>", "drop a file the document carries", runDetach},
	{"fields", "<in.pdf>", "list what a form asks for and what it holds", runFields},
	{"fill", "-set <name>=<value> [-set ...] <in.pdf> <out.pdf>", "fill in a form and save it", runFill},
	{"xfa", "<in.pdf> <out.pdf>", "draw the form inside a file whose pages are a placeholder", runXFA},
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
	metadata := fs.Bool("metadata", true, "drop the information dictionary")
	annotations := fs.Bool("annotations", false, "drop every annotation, links included")
	bookmarks := fs.Bool("bookmarks", false, "drop the bookmarks")
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
	if *metadata {
		d.ClearInfo()
	}
	if *annotations {
		d.RemoveAnnotations()
	}
	if *bookmarks {
		d.DropOutlines()
	}
	return save(d, fs.Arg(1))
}

func runSanitize(c *context, args []string) error {
	fs := flags("sanitize")
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
	d.Sanitize()
	return save(d, fs.Arg(1))
}

func runFlatten(c *context, args []string) error {
	fs := flags("flatten")
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
	d.Flatten()
	return save(d, fs.Arg(1))
}

func runNUp(c *context, args []string) error {
	fs := flags("nup")
	n := fs.Int("n", 2, "how many pages to a sheet")
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
	if err := d.NUp(*n); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runBooklet(c *context, args []string) error {
	fs := flags("booklet")
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
	if err := d.Booklet(); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runOverlay(c *context, args []string) error {
	fs := flags("overlay")
	with := fs.String("with", "", "the file to draw")
	under := fs.Bool("under", false, "draw it underneath rather than on top")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	mark, err := c.open(*with)
	if err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if *under {
		err = d.Underlay(mark)
	} else {
		err = d.Overlay(mark)
	}
	if err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runBlank(c *context, args []string) error {
	fs := flags("blank")
	before := fs.Int("before", 1, "the page to insert before; one past the end appends")
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
	if err := d.InsertBlank(*before); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// positions maps what a person types to where the text goes.
var positions = map[string]ops.Position{
	"center": ops.Center, "centre": ops.Center,
	"top-left": ops.TopLeft, "top": ops.TopCenter, "top-center": ops.TopCenter,
	"top-right": ops.TopRight, "bottom-left": ops.BottomLeft,
	"bottom": ops.BottomCenter, "bottom-center": ops.BottomCenter,
	"bottom-right": ops.BottomRight, "left": ops.MiddleLeft, "right": ops.MiddleRight,
}

// positionNames lists them for the error message.
func positionNames() string {
	names := make([]string, 0, len(positions))
	for k := range positions {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func runWatermark(c *context, args []string) error {
	fs := flags("watermark")
	pages := fs.String("pages", "all", "the pages to mark")
	text := fs.String("text", "", "the words to draw")
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
	if err := d.Watermark(*pages, *text); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runNumber(c *context, args []string) error {
	fs := flags("number")
	pages := fs.String("pages", "all", "the pages to number")
	format := fs.String("format", "{page}", "the text, where {page} and {pages} are filled in")
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
	if err := d.PageNumbers(*pages, *format); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runBates(c *context, args []string) error {
	fs := flags("bates")
	pages := fs.String("pages", "all", "the pages to stamp")
	prefix := fs.String("prefix", "", "what comes before the number")
	start := fs.Int("start", 1, "the first number")
	digits := fs.Int("digits", 6, "how many digits to pad to")
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
	if err := d.Bates(*pages, *prefix, *start, *digits); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

func runStamp(c *context, args []string) error {
	fs := flags("stamp")
	pages := fs.String("pages", "all", "the pages to stamp")
	text := fs.String("text", "", "the words to draw")
	at := fs.String("at", "center", "where to put them: "+positionNames())
	size := fs.Float64("size", 12, "the point size")
	rotate := fs.Float64("rotate", 0, "degrees anticlockwise")
	opacity := fs.Float64("opacity", 1, "from zero to one")
	bold := fs.Bool("bold", false, "use the bold face")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	pos, ok := positions[strings.ToLower(*at)]
	if !ok {
		return fmt.Errorf("%q is not a place; try one of %s", *at, positionNames())
	}
	font := ops.Helvetica
	if *bold {
		font = ops.HelveticaBold
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.Stamp(*pages, ops.Stamp{
		Text: *text, Font: font, Size: *size, Rotate: *rotate,
		Opacity: *opacity, Position: pos,
	}); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// runCompress packs the file's objects into compressed streams.
func runCompress(c *context, args []string) error {
	fs := flags("compress")
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
	d.Compress()
	return save(d, fs.Arg(1))
}

// permissionFlags names each permission the way a person would ask for it.
var permissionFlags = []struct {
	name string
	bit  reader.Permissions
}{
	{"print", reader.PermPrint},
	{"print-faithful", reader.PermPrintFaithful},
	{"modify", reader.PermModify},
	{"assemble", reader.PermAssemble},
	{"copy", reader.PermCopy},
	{"extract", reader.PermExtract},
	{"annotate", reader.PermAnnotate},
	{"fill-forms", reader.PermFillForms},
	{"all", reader.AllPermissions},
	{"none", 0},
}

// parsePermissions reads a comma-separated list of what a reader may do.
func parsePermissions(spec string) (reader.Permissions, error) {
	if spec == "" {
		return reader.AllPermissions, nil
	}
	var out reader.Permissions
	for _, word := range strings.Split(spec, ",") {
		word = strings.TrimSpace(word)
		found := false
		for _, p := range permissionFlags {
			if p.name == word {
				out |= p.bit
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("no such permission %q; there is %s", word, permissionList())
		}
	}
	return out, nil
}

// permissionList is every permission name, for an error message.
func permissionList() string {
	names := make([]string, 0, len(permissionFlags))
	for _, p := range permissionFlags {
		names = append(names, p.name)
	}
	return strings.Join(names, ", ")
}

// runEncrypt protects a file with a password.
func runEncrypt(c *context, args []string) error {
	fs := flags("encrypt")
	user := fs.String("user", "", "the password that opens the file, subject to the permissions")
	owner := fs.String("owner", "", "the password that opens it subject to nothing")
	allow := fs.String("allow", "", "what a reader may do: "+permissionList())
	aes128 := fs.Bool("aes128", false, "use the older 128-bit method, for readers from before 2008")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	if *user == "" && *owner == "" {
		return fmt.Errorf("encrypt needs a -user or an -owner password")
	}
	perms, err := parsePermissions(*allow)
	if err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	d.Encrypt(reader.Encryption{
		UserPassword:  *user,
		OwnerPassword: *owner,
		Permissions:   perms,
		AES128:        *aes128,
	})
	return save(d, fs.Arg(1))
}

// runDecrypt writes the file without its protection. The password to open it
// with is the global -password.
func runDecrypt(c *context, args []string) error {
	fs := flags("decrypt")
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
	d.Decrypt()
	return save(d, fs.Arg(1))
}

// runPermissions says how a file is protected and what it allows.
func runPermissions(c *context, args []string) error {
	fs := flags("permissions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "<in.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	p, ok := d.Protection()
	if !ok {
		fmt.Fprintln(c.out, "protection none")
		return nil
	}
	fmt.Fprintf(c.out, "protection %s, revision %d\n", p.Method, p.Revision)
	fmt.Fprintf(c.out, "opened as  %s\n", openedAs(p.Owner))
	fmt.Fprintf(c.out, "allows     %s\n", p.Permissions)
	return nil
}

// openedAs says which password the file was opened with.
func openedAs(owner bool) string {
	if owner {
		return "the owner, so the permissions do not apply"
	}
	return "the user"
}

// runText reads the text off a document and prints it.
func runText(c *context, args []string) error {
	fs := flags("text")
	spec := fs.String("pages", "all", "which pages to read")
	layout := fs.Bool("layout", false, "print where each piece of text sits as well as what it says")
	asJSON := fs.Bool("json", false, "write JSON rather than lines, for something that is going to parse it")
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
	pages, err := ops.ParseRange(*spec, src.PageCount())
	if err != nil {
		return err
	}
	if *asJSON {
		return writeTextJSON(c, src, pages, *layout)
	}
	for _, page := range pages {
		// Every page here came from the range, so it is one the document
		// has; neither of these can fail.
		if *layout {
			runs, _ := extract.Runs(src, page)
			for _, r := range runs {
				fmt.Fprintf(c.out, "%d\t%.2f\t%.2f\t%.2f\t%s%s\n",
					page, r.X, r.Y, r.Size, marks(r), r.Text)
			}
			continue
		}
		text, _ := extract.Text(src, page)
		fmt.Fprintln(c.out, text)
	}
	return nil
}

// jsonRun is one piece of text, with where it sits when -layout asked for it.
//
// Invisible and Unreadable are NOT omitted when false. A reader that has to
// tell "this run is readable" from "this tool did not say" would be reading a
// guess, and the whole point of the layout output is that what could not be
// read comes back marked rather than guessed at.
type jsonRun struct {
	Page       int      `json:"page"`
	X          *float64 `json:"x,omitempty"`
	Y          *float64 `json:"y,omitempty"`
	Size       *float64 `json:"size,omitempty"`
	Text       string   `json:"text"`
	Invisible  *bool    `json:"invisible,omitempty"`
	Unreadable *bool    `json:"unreadable,omitempty"`
}

// writeTextJSON writes the same reading as the lines above, in a shape
// something else can parse without splitting on tabs.
func writeTextJSON(c *context, src *reader.Document, pages []int, layout bool) error {
	// An empty result is `[]`, never `null`: a caller testing the length of
	// what came back should not have to test for nothing first.
	out := []jsonRun{}
	for _, page := range pages {
		if layout {
			runs, _ := extract.Runs(src, page)
			for _, r := range runs {
				x, y, size := r.X, r.Y, r.Size
				invisible, unreadable := r.Invisible, r.Unreadable
				out = append(out, jsonRun{
					Page: page, X: &x, Y: &y, Size: &size, Text: r.Text,
					Invisible: &invisible, Unreadable: &unreadable,
				})
			}
			continue
		}
		text, _ := extract.Text(src, page)
		out = append(out, jsonRun{Page: page, Text: text})
	}
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// marks says what is unusual about a run, in front of what it says.
func marks(r extract.Run) string {
	switch {
	case r.Invisible && r.Unreadable:
		return "[invisible, part unreadable] "
	case r.Invisible:
		return "[invisible] "
	case r.Unreadable:
		return "[part unreadable] "
	}
	return ""
}

// runImages writes out the pictures a document places.
func runImages(c *context, args []string) error {
	fs := flags("images")
	spec := fs.String("pages", "all", "which pages to take pictures from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out-directory>"); err != nil {
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
	dir := fs.Arg(1)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	n := 0
	for _, page := range pages {
		// The range only names pages the document has.
		images, _ := extract.Images(src, page)
		for i, im := range images {
			name := fmt.Sprintf("page%03d-%02d%s", page, i+1, imageSuffix(im))
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, im.Data, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(c.out, "%s\t%dx%d\tat %.1f,%.1f\tdrawn %.1fx%.1f\n",
				name, im.Width, im.Height, im.X, im.Y, im.DrawnWidth, im.DrawnHeight)
			n++
		}
	}
	if n == 0 {
		fmt.Fprintln(c.out, "no pictures")
	}
	return nil
}

// imageSuffix names a picture by what it holds. A JPEG is written out as one;
// anything this has unfiltered into plain samples is written as it stands,
// since turning samples into pixels means reading a colour space and that is
// the renderer's work.
func imageSuffix(im extract.Image) string {
	switch im.Filter {
	case "DCTDecode":
		return ".jpg"
	case "JPXDecode":
		return ".jp2"
	case "JBIG2Decode":
		return ".jbig2"
	}
	return ".samples"
}

// read opens a document rather than a document being assembled, which is what
// reading a page back needs.
func (c *context) read(path string) (*reader.Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return reader.OpenWithPassword(b, c.password)
}

// runFields lists a form's fields: what each is called, what sort of thing it
// is, and what it holds. A name is what fill takes, so this is how anybody
// finds out what to type.
func runFields(c *context, args []string) error {
	fs := flags("fields")
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
	filling, ok, err := ops.OpenFormWithPassword(b, c.password)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(c.out, "the file has no form in it")
		return nil
	}
	form := filling.Form()
	if form.HasXFA() {
		if form.Dynamic() {
			fmt.Fprintln(c.out, "note: this file's pages are a placeholder and its real form is XFA;")
			fmt.Fprintln(c.out, "      \"pdfops xfa\" draws that one. What follows is the standard form beside it.")
		} else {
			fmt.Fprintln(c.out, "note: the file also carries an XFA form, which is not read; the standard one is.")
		}
	}
	for _, f := range form.Fields() {
		marks := ""
		if f.ReadOnly {
			marks += " read-only"
		}
		if f.Required {
			marks += " required"
		}
		if f.MaxLen > 0 {
			marks += fmt.Sprintf(" max=%d", f.MaxLen)
		}
		fmt.Fprintf(c.out, "%-40s %-9s %q%s\n", f.Name, f.Kind, f.Value, marks)
		for _, o := range f.Options {
			fmt.Fprintf(c.out, "%-40s   row %q\n", "", o.Value)
		}
		if len(f.States()) > 0 {
			fmt.Fprintf(c.out, "%-40s   buttons %v\n", "", f.States())
		}
	}
	return nil
}

// runFill fills a form in and writes the result.
//
// The file it writes is the one it read with the changes appended after it,
// which is how everything that saves a form saves one: nothing already in the
// file is rewritten, so whatever this does not understand survives.
func runFill(c *context, args []string) error {
	fs := flags("fill")
	var set stringList
	fs.Var(&set, "set", "a field to fill, as <name>=<value>; may be given more than once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	if len(set) == 0 {
		return fmt.Errorf("nothing to fill in: give at least one -set <name>=<value>")
	}
	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	filling, ok, err := ops.OpenFormWithPassword(b, c.password)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s has no form in it", fs.Arg(0))
	}
	for _, pair := range set {
		name, value, found := strings.Cut(pair, "=")
		if !found {
			return fmt.Errorf("-set wants <name>=<value>, not %q", pair)
		}
		if err := filling.Fill(name, value); err != nil {
			return err
		}
	}
	out, err := filling.Bytes()
	if err != nil {
		return err
	}
	return os.WriteFile(fs.Arg(1), out, 0o644)
}

// A stringList is a flag that may be given more than once.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// runXFA lays out the form inside a document whose pages are a placeholder,
// and writes those pages drawn.
//
// It says on the way out what it drew and what it could not, because the
// answer "this file is now readable" is only worth having with the size of
// what is missing beside it.
func runXFA(c *context, args []string) error {
	fs := flags("xfa")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	src, err := c.read(fs.Arg(0))
	if err != nil {
		return err
	}
	d, rep, err := ops.FromXFA(src)
	if err != nil {
		return err
	}
	if err := save(d, fs.Arg(1)); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%d sheets, %d elements drawn", rep.Sheets, rep.Drawn)
	if rep.Pictures > 0 {
		fmt.Fprintf(c.out, ", %d of them pictures", rep.Pictures)
	}
	if rep.Hidden > 0 {
		fmt.Fprintf(c.out, ", %d the form hides", rep.Hidden)
	}
	fmt.Fprintln(c.out)
	for _, u := range rep.Unplaced {
		fmt.Fprintf(c.out, "  not drawn: %s\n", u)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Verbs over library functions that already existed and nothing exposed.
//
// ops.Interleave, ops.OnePage, ops.Poster, ops.Attach/Detach/Attachments and
// ops.SetOutline/DropOutlines were written, tested and reachable from Go, and
// a person with a PDF and a terminal could not get at any of them. Each of
// these is a thin wrapper: the work, and the tests that prove it, are in the
// package.
// ---------------------------------------------------------------------------

// runInterleave takes one page from each document in turn.
//
// Shaped like merge — the output first — because it is the same kind of verb,
// several files in and one out, and two different argument orders for that
// shape is a way to lose a file.
func runInterleave(c *context, args []string) error {
	fs := flags("interleave")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 3 {
		return fmt.Errorf("expected <out.pdf> <in.pdf> <in.pdf> [in.pdf …]")
	}
	first, err := c.open(fs.Arg(1))
	if err != nil {
		return err
	}
	var rest []*ops.Doc
	for _, in := range fs.Args()[2:] {
		d, err := c.open(in)
		if err != nil {
			return err
		}
		rest = append(rest, d)
	}
	if err := first.Interleave(rest...); err != nil {
		return err
	}
	return save(first, fs.Arg(0))
}

// runOnePage puts every page of a document onto a single page.
func runOnePage(c *context, args []string) error {
	fs := flags("onepage")
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
	if err := d.OnePage(); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// runPoster cuts each page into tiles, so a page can be printed larger than
// the paper and assembled.
func runPoster(c *context, args []string) error {
	fs := flags("poster")
	across := fs.Int("across", 2, "how many tiles across each page becomes")
	down := fs.Int("down", 2, "how many tiles down each page becomes")
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
	if err := d.Poster(*across, *down); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// runAttachments lists the files a document carries, or writes them out.
//
// Listing is the default because that is the question somebody has first, and
// because writing files to disk should be asked for rather than assumed.
func runAttachments(c *context, args []string) error {
	fs := flags("attachments")
	to := fs.String("to", "", "write the files into this directory instead of listing them")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "[-to <directory>] <in.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	carried := d.Attachments()
	if len(carried) == 0 {
		fmt.Fprintln(c.out, "the file carries nothing")
		return nil
	}
	if *to == "" {
		for _, a := range carried {
			if a.Description != "" {
				fmt.Fprintf(c.out, "%s\t%d bytes\t%s\n", a.Name, len(a.Data), a.Description)
				continue
			}
			fmt.Fprintf(c.out, "%s\t%d bytes\n", a.Name, len(a.Data))
		}
		return nil
	}
	if err := os.MkdirAll(*to, 0o755); err != nil {
		return err
	}
	for _, a := range carried {
		// The name comes out of the document, so it is not ours to trust: a
		// name holding a separator or `..` would write outside the directory
		// that was asked for. Take the last element and nothing else.
		name := filepath.Base(filepath.FromSlash(a.Name))
		if name == "." || name == string(filepath.Separator) || name == ".." {
			return fmt.Errorf("attachment %q has no usable file name", a.Name)
		}
		if err := os.WriteFile(filepath.Join(*to, name), a.Data, 0o644); err != nil {
			return err
		}
		fmt.Fprintln(c.out, filepath.Join(*to, name))
	}
	return nil
}

// runAttach carries a file inside the document.
func runAttach(c *context, args []string) error {
	fs := flags("attach")
	file := fs.String("file", "", "the file to carry")
	as := fs.String("as", "", "what the document should call it (default: the file's own name)")
	description := fs.String("description", "", "what the file is")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("expected -file <path>")
	}
	if err := wantArgs(fs, 2, "-file <path> [-as <name>] [-description <text>] <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	name := *as
	if name == "" {
		name = filepath.Base(*file)
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.Attach(name, data, *description); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// runDetach drops a file the document carries.
func runDetach(c *context, args []string) error {
	fs := flags("detach")
	name := fs.String("name", "", "the attachment to drop")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("expected -name <name>")
	}
	if err := wantArgs(fs, 2, "-name <name> <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	// ⛔ Refuse rather than write an unchanged copy. `detach -name typo` that
	// succeeds and removes nothing is the shape of a mistake somebody only
	// finds out about later, when the file they meant to remove is still in
	// what they sent.
	if !d.Detach(*name) {
		return fmt.Errorf("the file carries nothing called %q", *name)
	}
	return save(d, fs.Arg(1))
}

// runOutline replaces or removes a document's bookmarks.
func runOutline(c *context, args []string) error {
	fs := flags("outline")
	drop := fs.Bool("drop", false, "remove the bookmarks instead of replacing them")
	from := fs.String("from", "", "a file of bookmarks: Title, a tab, a page number, two spaces of indent per level")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *drop && *from != "":
		return fmt.Errorf("expected one of -drop or -from, not both")
	case !*drop && *from == "":
		return fmt.Errorf("expected -drop or -from <toc.txt>")
	}
	if err := wantArgs(fs, 2, "(-drop | -from <toc.txt>) <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if *drop {
		d.DropOutlines()
		return save(d, fs.Arg(1))
	}
	text, err := os.ReadFile(*from)
	if err != nil {
		return err
	}
	marks, err := parseOutline(string(text))
	if err != nil {
		return err
	}
	d.SetOutline(marks)
	return save(d, fs.Arg(1))
}

// parseOutline reads a bookmark file: one bookmark a line, a title, a tab and
// a page number, with two spaces of indent for each level of nesting.
//
//	Introduction→1
//	  Why→2
//	Chapter one→7
//
// A line it cannot read is an error naming the line, because a table of
// contents quietly missing an entry is worse than one that refuses to be
// written.
func parseOutline(text string) ([]ops.Bookmark, error) {
	type level struct {
		depth int
		marks *[]ops.Bookmark
	}
	var root []ops.Bookmark
	stack := []level{{depth: -1, marks: &root}}

	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		if strings.TrimSpace(line) == "" {
			continue
		}
		body := strings.TrimLeft(line, " ")
		indent := len(line) - len(body)
		if indent%2 != 0 {
			return nil, fmt.Errorf("line %d: indented by %d spaces, which is not a whole number of levels of two", n, indent)
		}
		depth := indent / 2

		title, page, ok := strings.Cut(body, "\t")
		if !ok {
			return nil, fmt.Errorf("line %d: expected a title, a tab and a page number", n)
		}
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, fmt.Errorf("line %d: the title is empty", n)
		}
		p, err := strconv.Atoi(strings.TrimSpace(page))
		if err != nil {
			return nil, fmt.Errorf("line %d: %q is not a page number", n, strings.TrimSpace(page))
		}
		if p < 1 {
			return nil, fmt.Errorf("line %d: page %d, and pages start at 1", n, p)
		}

		for len(stack) > 1 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		if depth > stack[len(stack)-1].depth+1 {
			return nil, fmt.Errorf("line %d: indented %d levels under one at %d, so a level is missing between them",
				n, depth, stack[len(stack)-1].depth)
		}
		parent := stack[len(stack)-1].marks
		*parent = append(*parent, ops.Bookmark{Title: title, Page: p})
		stack = append(stack, level{depth: depth, marks: &(*parent)[len(*parent)-1].Children})
	}
	return root, nil
}

// paperSizes are the page sizes a person names rather than measures, in
// points, DERIVED from their definitions rather than typed: ISO 216 fixes the
// A series in millimetres, and a point is 1/72 of an inch.
//
// Typing 595.28 x 841.89 would be right until somebody wanted A2.
var paperSizes = func() map[string][2]float64 {
	mm := func(w, h float64) [2]float64 { return [2]float64{w / 25.4 * 72, h / 25.4 * 72} }
	in := func(w, h float64) [2]float64 { return [2]float64{w * 72, h * 72} }
	return map[string][2]float64{
		"a3":      mm(297, 420),
		"a4":      mm(210, 297),
		"a5":      mm(148, 210),
		"letter":  in(8.5, 11),
		"legal":   in(8.5, 14),
		"tabloid": in(11, 17),
	}
}()

// paperNames lists the sizes in a fixed order, so the error that names them
// reads the same every time.
func paperNames() []string {
	names := make([]string, 0, len(paperSizes))
	for n := range paperSizes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// runResize sets the page size: a box in points, or a size by name.
//
// `crop` sets what of the page SHOWS; this sets how big the page IS. Two
// verbs because they are two questions, and a flag on one of them would make
// both harder to read.
func runResize(c *context, args []string) error {
	fs := flags("resize")
	spec := fs.String("pages", "all", "which pages to resize")
	box := fs.String("box", "", "the new page box, in points: l,b,r,t")
	to := fs.String("to", "", "a size by name: "+strings.Join(paperNames(), ", "))
	landscape := fs.Bool("landscape", false, "with -to, turn the named size on its side")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *box != "" && *to != "":
		return fmt.Errorf("expected one of -box or -to, not both")
	case *box == "" && *to == "":
		return fmt.Errorf("expected -box <l,b,r,t> or -to <%s>", strings.Join(paperNames(), "|"))
	case *landscape && *to == "":
		return fmt.Errorf("-landscape says which way round a named size goes, so it needs -to")
	}
	if err := wantArgs(fs, 2, "(-box <l,b,r,t> | -to <name>) <in.pdf> <out.pdf>"); err != nil {
		return err
	}

	var rect [4]float64
	if *box != "" {
		var err error
		rect, err = parseBox(*box)
		if err != nil {
			return err
		}
	} else {
		size, ok := paperSizes[strings.ToLower(*to)]
		if !ok {
			return fmt.Errorf("no size called %q; the ones there are: %s", *to, strings.Join(paperNames(), ", "))
		}
		w, h := size[0], size[1]
		if *landscape {
			w, h = h, w
		}
		rect = [4]float64{0, 0, w, h}
	}

	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.Resize(*spec, rect); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// runMetadata writes what a document says about itself.
//
// `info` already prints it; this is the other half. A key is given the way the
// PDF names it, with or without the slash, because a person reading `info`
// sees `Title` and should be able to type what they saw.
func runMetadata(c *context, args []string) error {
	fs := flags("metadata")
	var sets stringList
	fs.Var(&sets, "set", "an entry to write, as Key=value; may be given more than once")
	clear := fs.Bool("clear", false, "remove everything the document says about itself")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *clear && len(sets) > 0:
		return fmt.Errorf("expected -clear or -set, not both")
	case !*clear && len(sets) == 0:
		return fmt.Errorf("expected -set <Key=value> or -clear")
	}
	if err := wantArgs(fs, 2, "(-set <Key=value> … | -clear) <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if *clear {
		d.ClearInfo()
		return save(d, fs.Arg(1))
	}
	for _, s := range sets {
		key, value, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("expected Key=value, got %q", s)
		}
		key = strings.TrimPrefix(strings.TrimSpace(key), "/")
		if key == "" {
			return fmt.Errorf("expected a key before the = in %q", s)
		}
		d.SetInfo(reader.Name(key), value)
	}
	return save(d, fs.Arg(1))
}

// runMove takes one page out and puts it back somewhere else.
func runMove(c *context, args []string) error {
	fs := flags("move")
	from := fs.Int("from", 0, "the page to move")
	to := fs.Int("to", 0, "where it should end up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == 0 || *to == 0 {
		return fmt.Errorf("expected -from <page> -to <page>, counting from 1")
	}
	if err := wantArgs(fs, 2, "-from <page> -to <page> <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.Move(*from, *to); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// runUnderlay draws another file BENEATH these pages, where overlay draws it
// over. A watermark that belongs behind the text rather than across it is the
// difference, and it is not a flag on overlay because "overlay -under" is a
// sentence nobody should have to read.
func runUnderlay(c *context, args []string) error {
	fs := flags("underlay")
	with := fs.String("with", "", "the file to draw underneath")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *with == "" {
		return fmt.Errorf("expected -with <mark.pdf>")
	}
	if err := wantArgs(fs, 2, "-with <mark.pdf> <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	mark, err := c.open(*with)
	if err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := d.Underlay(mark); err != nil {
		return err
	}
	return save(d, fs.Arg(1))
}

// runVersion sets the PDF version a document declares.
func runVersion(c *context, args []string) error {
	fs := flags("version")
	set := fs.String("set", "", "the version to declare, such as 1.7")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *set == "" {
		return fmt.Errorf("expected -set <version>")
	}
	if err := wantArgs(fs, 2, "-set <version> <in.pdf> <out.pdf>"); err != nil {
		return err
	}
	d, err := c.open(fs.Arg(0))
	if err != nil {
		return err
	}
	d.SetVersion(*set)
	return save(d, fs.Arg(1))
}
