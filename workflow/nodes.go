package workflow

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
)

// Category is BentoPDF's NodeCategory (types.ts).
type Category string

// The six categories.
const (
	Input    Category = "Input"
	Edit     Category = "Edit & Annotate"
	Organize Category = "Organize & Manage"
	Optimize Category = "Optimize & Repair"
	Secure   Category = "Secure PDF"
	Output   Category = "Output"
)

// Socket is a socket type (sockets.ts): PDF, MultiPDF and Image.
type Socket string

// The three socket types.
const (
	PDF      Socket = "PDF"
	MultiPDF Socket = "MultiPDF"
	Image    Socket = "Image"
)

// compatible says whether an output of one socket type may feed an input of
// another: a PDF and a bundle of PDFs are both lists of documents here, and an
// image is only ever an image.
func compatible(from, to Socket) bool {
	if from == Image || to == Image {
		return from == to
	}
	return true
}

// port is one input or output socket of a node.
type port struct {
	name   string
	socket Socket
	multi  bool // an input that takes several connections
}

var (
	pdfIn   = []port{{"pdf", PDF, false}}
	multiIn = []port{{"pdf", PDF, true}}
	pdfOut  = []port{{"pdf", PDF, false}}
)

// spec describes one node type: where it sits, what it plugs into, which
// BentoPDF controls it cannot honour, and the code that runs it.
type spec struct {
	label    string
	category Category
	in, out  []port
	ignored  []string
	verb     string
	run      func(x *exec) ([]item, error)
}

// nodes is THE mapping table: BentoPDF node type name (registry.ts,
// nodeRegistry) -> the verb that runs it here.
var nodes = map[string]*spec{
	"PDFInputNode": {label: "PDF Input", category: Input, out: pdfOut, verb: "open", run: runInput},

	"MergeNode":          {label: "Merge PDFs", category: Organize, in: multiIn, out: pdfOut, ignored: []string{"retainPageLabels"}, verb: "ops.Merge", run: runMerge},
	"AlternateMergeNode": {label: "Alternate Merge", category: Organize, in: multiIn, out: pdfOut, ignored: []string{"retainPageLabels"}, verb: "Doc.Interleave", run: runInterleave},
	"SplitNode":          {label: "Split PDF", category: Organize, in: pdfIn, out: pdfOut, verb: "Doc.Select", run: runSelect},
	"ExtractPagesNode":   {label: "Extract Pages", category: Organize, in: pdfIn, out: pdfOut, verb: "ops.Merge + Doc.Select, one file per page", run: runExtract},
	"RotateNode":         {label: "Rotate", category: Organize, in: pdfIn, out: pdfOut, verb: "Doc.Rotate", run: runRotate},
	"DeletePagesNode":    {label: "Delete Pages", category: Organize, in: pdfIn, out: pdfOut, verb: "Doc.Delete", run: runDelete},
	"ReversePagesNode":   {label: "Reverse Pages", category: Organize, in: pdfIn, out: pdfOut, verb: "Doc.Reverse", run: runReverse},
	"AddBlankPageNode":   {label: "Add Blank Page", category: Organize, in: pdfIn, out: pdfOut, verb: "Doc.InsertBlank", run: runAddBlank},
	"DividePagesNode":    {label: "Divide Pages", category: Organize, in: pdfIn, out: pdfOut, ignored: []string{"pages"}, verb: "Doc.Divide", run: runDivide},
	"NUpNode":            {label: "N-Up", category: Organize, in: pdfIn, out: pdfOut, ignored: []string{"orientation", "margins", "border", "borderColor"}, verb: "Doc.NUp", run: runNUp},
	"CombineSinglePageNode": {label: "Combine to Single Page", category: Organize, in: pdfIn, out: pdfOut,
		ignored: []string{"orientation", "spacing", "backgroundColor", "separator", "separatorThickness", "separatorColor"}, verb: "Doc.OnePage", run: runOnePage},
	"BookletNode":      {label: "Booklet", category: Organize, in: pdfIn, out: pdfOut, ignored: []string{"gridMode", "paperSize", "orientation"}, verb: "Doc.Booklet", run: runBooklet},
	"OverlayNode":      {label: "Overlay", category: Organize, in: []port{{"pdf", PDF, false}, {"overlay", PDF, false}}, out: pdfOut, verb: "Doc.Overlay / Doc.Underlay", run: runOverlay},
	"PosterizeNode":    {label: "Posterize", category: Organize, in: pdfIn, out: pdfOut, verb: "Doc.Poster", run: runPoster},
	"EditMetadataNode": {label: "Edit Metadata", category: Organize, in: pdfIn, out: pdfOut, verb: "Doc.SetInfo", run: runMetadata},

	"WatermarkNode": {label: "Watermark", category: Edit, in: pdfIn, out: pdfOut,
		ignored: []string{"fontSize", "color", "opacity", "angle", "position", "tile", "tileGapX", "tileGapY", "flatten"}, verb: "Doc.Watermark", run: runWatermark},
	"PageNumbersNode":       {label: "Page Numbers", category: Edit, in: pdfIn, out: pdfOut, ignored: []string{"position", "fontSize", "color"}, verb: "Doc.PageNumbers", run: runNumbers},
	"RemoveBlankPagesNode":  {label: "Remove Blank Pages", category: Edit, in: pdfIn, out: pdfOut, verb: "Doc.DropBlank", run: runDeblank},
	"RemoveAnnotationsNode": {label: "Remove Annotations", category: Edit, in: pdfIn, out: pdfOut, verb: "Doc.RemoveAnnotations", run: runStripAnnots},

	"CompressNode":      {label: "Compress", category: Optimize, in: pdfIn, out: pdfOut, ignored: []string{"compressionLevel", "targetKilobytes", "convertToGrayscale"}, verb: "Doc.Compress", run: runCompress},
	"DeskewNode":        {label: "Deskew", category: Optimize, in: pdfIn, out: pdfOut, verb: "Doc.Deskew", run: runDeskew},
	"FontToOutlineNode": {label: "Font to Outline", category: Optimize, in: pdfIn, out: pdfOut, verb: "Doc.OutlineText", run: runOutline},
	"RepairNode":        {label: "Repair", category: Optimize, in: pdfIn, out: pdfOut, verb: "open (the reader repairs on open)", run: runPass},

	"EncryptNode": {label: "Encrypt", category: Secure, in: pdfIn, out: pdfOut, verb: "Doc.Encrypt", run: runEncrypt},
	"DecryptNode": {label: "Decrypt", category: Secure, in: pdfIn, out: pdfOut, verb: "Doc.Decrypt", run: runDecrypt},
	"SanitizeNode": {label: "Sanitize", category: Secure, in: pdfIn, out: pdfOut,
		ignored: []string{"removeLayers", "removeLinks", "removeStructureTree", "removeMarkInfo", "removeFonts"}, verb: "Doc.Sanitize + Flatten + RemoveAnnotations + ClearInfo", run: runSanitize},
	"FlattenNode": {label: "Flatten", category: Secure, in: pdfIn, out: pdfOut, verb: "Doc.Flatten", run: runFlatten},

	"DownloadNode":    {label: "Download", category: Output, in: multiIn, verb: "write one PDF, or a ZIP", run: runDownload},
	"DownloadPDFNode": {label: "Download", category: Output, in: multiIn, verb: "legacy name of DownloadNode", run: runDownload},
	"DownloadZipNode": {label: "Download", category: Output, in: multiIn, verb: "legacy name of DownloadNode", run: runDownload},
}

// unsupported is every other node type in BentoPDF's registry. They are known,
// so a file that uses one is told it has no verb here, not that it is garbage.
var unsupported = map[string]Category{
	"ImageInputNode": Input, "WordToPdfNode": Input, "ExcelToPdfNode": Input, "PowerPointToPdfNode": Input,
	"TextToPdfNode": Input, "SvgToPdfNode": Input, "EpubToPdfNode": Input, "EmailToPdfNode": Input,
	"XpsToPdfNode": Input, "MobiToPdfNode": Input, "Fb2ToPdfNode": Input, "CbzToPdfNode": Input,
	"MarkdownToPdfNode": Input, "JsonToPdfNode": Input, "XmlToPdfNode": Input, "WpdToPdfNode": Input,
	"WpsToPdfNode": Input, "PagesToPdfNode": Input, "OdgToPdfNode": Input, "PubToPdfNode": Input,
	"VsdToPdfNode": Input,

	"TableOfContentsNode": Organize, "OCRNode": Organize, "FixPageSizeNode": Organize,

	"CropNode": Edit, "GreyscaleNode": Edit, "InvertColorsNode": Edit, "ScannerEffectNode": Edit,
	"AdjustColorsNode": Edit, "BackgroundColorNode": Edit, "HeaderFooterNode": Edit,

	"RasterizeNode": Optimize, "LinearizeNode": Optimize, "PdfToPdfANode": Optimize,

	"DigitalSignNode": Secure, "TimestampNode": Secure, "RedactNode": Secure,

	"PdfToImagesNode": Output, "PdfToTextNode": Output, "PdfToDocxNode": Output, "PdfToXlsxNode": Output,
	"PdfToCsvNode": Output, "PdfToSvgNode": Output, "PdfToMarkdownNode": Output, "ExtractImagesNode": Output,
}

// Mapped is one row of the mapping table, for a listing.
type Mapped struct {
	Type     string
	Label    string
	Category Category
	Verb     string // empty when the node has no verb here
	Ignored  []string
}

// Mapping lists every BentoPDF node type, sorted by name: the ones that run
// here with their verb, and the ones that do not with an empty Verb.
func Mapping() []Mapped {
	out := make([]Mapped, 0, len(nodes)+len(unsupported))
	for t, s := range nodes {
		out = append(out, Mapped{t, s.label, s.category, s.verb, s.ignored})
	}
	for t, c := range unsupported {
		out = append(out, Mapped{Type: t, Category: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// item is a document travelling along a connection, with the file name it
// will have if it reaches a Download.
type item struct {
	name string
	doc  *ops.Doc
}

// exec is what a node's code sees of its place in a run.
type exec struct {
	node  Node
	in    map[string][]item
	st    *state
	files []File            // for an input node
	sec   map[string]string // the secrets the caller supplied for this node
	res   *Result           // for an output node
}

func (x *exec) fail(key, format string, a ...any) error {
	return fmt.Errorf("control %q: %s", key, fmt.Sprintf(format, a...))
}

// text reads a control as text. A number is written the way BentoPDF's own
// selects hold it ("90"), a missing control is the default.
func (x *exec) text(key, def string) (string, error) {
	v, ok := x.node.Controls[key]
	if !ok {
		return def, nil
	}
	switch v := v.(type) {
	case string:
		return v, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	}
	return "", x.fail(key, "expected text, got %v", v)
}

// number reads a control as a number in [lo, hi]; BentoPDF keeps selects as
// strings, so "90" is read as 90.
func (x *exec) number(key string, def, lo, hi float64) (float64, error) {
	v, ok := x.node.Controls[key]
	if !ok {
		return def, nil
	}
	var f float64
	switch v := v.(type) {
	case float64:
		f = v
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil {
			return 0, x.fail(key, "%q is not a number", v)
		}
	default:
		return 0, x.fail(key, "expected a number, got %v", v)
	}
	if !(f >= lo && f <= hi) {
		return 0, x.fail(key, "%v is outside %v..%v", f, lo, hi)
	}
	return f, nil
}

// flag reads a boolean control, which BentoPDF's own controls hold as the
// strings "true" and "false".
func (x *exec) flag(key string, def bool) (bool, error) {
	v, ok := x.node.Controls[key]
	if !ok {
		return def, nil
	}
	switch v {
	case true, "true":
		return true, nil
	case false, "false":
		return false, nil
	}
	return false, x.fail(key, "expected true or false, got %v", v)
}

// each applies a verb to every document that arrives: a node fed several PDFs
// treats each in turn (BentoPDF's processBatch).
func (x *exec) each(fn func(d *ops.Doc) error) ([]item, error) {
	items := x.in["pdf"]
	for _, it := range items {
		if err := fn(it.doc); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// pagesIn is the number of pages that arrived.
func (x *exec) pagesIn() int {
	n := 0
	for _, it := range x.in["pdf"] {
		n += it.doc.PageCount()
	}
	return n
}

// ---- the nodes ----

func runInput(x *exec) ([]item, error) {
	var out []item
	for _, f := range x.files {
		if err := x.st.input(len(f.Data)); err != nil {
			return nil, err
		}
		pw := x.sec["password"]
		d, err := ops.OpenWithPassword(f.Data, pw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		out = append(out, item{f.Name, d})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no PDF was given to this input")
	}
	return out, nil
}

func runMerge(x *exec) ([]item, error) {
	docs := make([]*ops.Doc, 0, len(x.in["pdf"]))
	for _, it := range x.in["pdf"] {
		docs = append(docs, it.doc)
	}
	return []item{{"merged.pdf", ops.Merge(docs...)}}, nil
}

func runInterleave(x *exec) ([]item, error) {
	in := x.in["pdf"]
	if len(in) == 1 {
		return in, nil
	}
	rest := make([]*ops.Doc, 0, len(in)-1)
	for _, it := range in[1:] {
		rest = append(rest, it.doc)
	}
	if err := in[0].doc.Interleave(rest...); err != nil {
		return nil, err
	}
	return []item{{"merged.pdf", in[0].doc}}, nil
}

func runSelect(x *exec) ([]item, error) {
	spec, err := x.text("pages", "1-3")
	if err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error { return d.Select(spec) })
}

func runExtract(x *exec) ([]item, error) {
	spec, err := x.text("pages", "1,2,3")
	if err != nil {
		return nil, err
	}
	var out []item
	for _, it := range x.in["pdf"] {
		nums, err := ops.ParseRange(spec, it.doc.PageCount())
		if err != nil {
			return nil, err
		}
		if err := x.st.reserve(len(nums), 1); err != nil {
			return nil, err
		}
		base := strings.TrimSuffix(it.name, ".pdf")
		for _, n := range nums {
			d := ops.Merge(it.doc)
			if err := d.Select(strconv.Itoa(n)); err != nil {
				return nil, err
			}
			out = append(out, item{fmt.Sprintf("%s-p%d.pdf", base, n), d})
		}
	}
	return out, nil
}

func runRotate(x *exec) ([]item, error) {
	a, err := x.number("angle", 90, -360, 360)
	if err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error { return d.Rotate("all", int(a)) })
}

func runDelete(x *exec) ([]item, error) {
	spec, err := x.text("pages", "1")
	if err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error { return d.Delete(spec) })
}

func runReverse(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { d.Reverse(); return nil })
}

func runAddBlank(x *exec) ([]item, error) {
	where, err := x.text("blankPosition", "end")
	if err != nil {
		return nil, err
	}
	after, err := x.number("afterPage", 1, 1, 100000)
	if err != nil {
		return nil, err
	}
	count, err := x.number("count", 1, 1, 100)
	if err != nil {
		return nil, err
	}
	if where != "end" && where != "start" && where != "after" {
		return nil, x.fail("blankPosition", "%q is not end, start or after", where)
	}
	if err := x.st.reserve(x.pagesIn()+int(count)*len(x.in["pdf"]), 1); err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error {
		at := 1
		switch where {
		case "end":
			at = d.PageCount() + 1
		case "after":
			at = int(after) + 1
		}
		for i := 0; i < int(count); i++ {
			if err := d.InsertBlank(at); err != nil {
				return err
			}
		}
		return nil
	})
}

func runDivide(x *exec) ([]item, error) {
	dir, err := x.text("direction", "vertical")
	if err != nil {
		return nil, err
	}
	across, down := 2, 1
	switch dir {
	case "vertical":
	case "horizontal":
		across, down = 1, 2
	default:
		return nil, x.fail("direction", "%q is not vertical or horizontal", dir)
	}
	if err := x.st.reserve(x.pagesIn(), across*down); err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error { return d.Divide(across, down) })
}

func runNUp(x *exec) ([]item, error) {
	n, err := x.number("pagesPerSheet", 2, 2, 16)
	if err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error { return d.NUp(int(n)) })
}

func runOnePage(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { return d.OnePage() })
}

func runBooklet(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { return d.Booklet() })
}

func runOverlay(x *exec) ([]item, error) {
	mode, err := x.text("mode", "overlay")
	if err != nil {
		return nil, err
	}
	over := x.in["overlay"]
	if len(over) == 0 {
		return nil, fmt.Errorf("no PDF to draw reached the overlay input")
	}
	return x.each(func(d *ops.Doc) error {
		if mode == "underlay" {
			return d.Underlay(over[0].doc)
		}
		return d.Overlay(over[0].doc)
	})
}

func runPoster(x *exec) ([]item, error) {
	rows, err := x.number("rows", 2, 1, 8)
	if err != nil {
		return nil, err
	}
	cols, err := x.number("cols", 2, 1, 8)
	if err != nil {
		return nil, err
	}
	if err := x.st.reserve(x.pagesIn(), int(rows)*int(cols)); err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error { return d.Poster(int(cols), int(rows)) })
}

var infoKeys = []struct{ control, key string }{
	{"title", "Title"}, {"author", "Author"}, {"subject", "Subject"},
	{"keywords", "Keywords"}, {"creator", "Creator"}, {"producer", "Producer"},
}

func runMetadata(x *exec) ([]item, error) {
	set := map[string]string{}
	for _, k := range infoKeys {
		v, err := x.text(k.control, "")
		if err != nil {
			return nil, err
		}
		if v != "" {
			set[k.key] = v
		}
	}
	return x.each(func(d *ops.Doc) error {
		for _, k := range infoKeys {
			if v, ok := set[k.key]; ok {
				d.SetInfo(reader.Name(k.key), v)
			}
		}
		return nil
	})
}

func runWatermark(x *exec) ([]item, error) {
	text, err := x.text("text", "DRAFT")
	if err != nil {
		return nil, err
	}
	pages, err := x.text("pages", "all")
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, x.fail("text", "is empty")
	}
	return x.each(func(d *ops.Doc) error { return d.Watermark(pages, text) })
}

func runNumbers(x *exec) ([]item, error) {
	f, err := x.text("numberFormat", "simple")
	if err != nil {
		return nil, err
	}
	var format string
	switch f {
	case "simple":
		format = "{page}"
	case "page_x_of_y":
		format = "Page {page} of {pages}"
	default:
		return nil, x.fail("numberFormat", "%q is not simple or page_x_of_y", f)
	}
	return x.each(func(d *ops.Doc) error { return d.PageNumbers("all", format) })
}

func runDeblank(x *exec) ([]item, error) {
	t, err := x.number("threshold", 0.1, 0.1, 5)
	if err != nil {
		return nil, err
	}
	// BentoPDF's threshold is a percentage of the page that may be ink.
	return x.each(func(d *ops.Doc) error {
		_, err := d.DropBlank(ops.BlankOptions{MaxInk: t / 100})
		return err
	})
}

func runStripAnnots(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { d.RemoveAnnotations(); return nil })
}

func runCompress(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { d.Compress(); return nil })
}

func runDeskew(x *exec) ([]item, error) {
	th, err := x.number("skewThreshold", 0.5, 0, 45)
	if err != nil {
		return nil, err
	}
	dpi, err := x.number("processingDpi", 150, 36, 600)
	if err != nil {
		return nil, err
	}
	return x.each(func(d *ops.Doc) error {
		_, err := d.Deskew(ops.DeskewOptions{Threshold: th, DPI: dpi})
		return err
	})
}

func runOutline(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { return d.OutlineText("all") })
}

func runPass(x *exec) ([]item, error) { return x.in["pdf"], nil }

func runEncrypt(x *exec) ([]item, error) {
	user, owner := x.sec["userPassword"], x.sec["ownerPassword"]
	if user == "" && owner == "" {
		return nil, fmt.Errorf("no password: a workflow file never holds one, so give userPassword or ownerPassword in the secrets")
	}
	return x.each(func(d *ops.Doc) error {
		d.Encrypt(reader.Encryption{UserPassword: user, OwnerPassword: owner, Permissions: reader.AllPermissions})
		return nil
	})
}

func runDecrypt(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { d.Decrypt(); return nil })
}

func runSanitize(x *exec) ([]item, error) {
	var on [5]bool
	for i, k := range []string{"flattenForms", "removeMetadata", "removeAnnotations", "removeJavascript", "removeEmbeddedFiles"} {
		v, err := x.flag(k, true)
		if err != nil {
			return nil, err
		}
		on[i] = v
	}
	return x.each(func(d *ops.Doc) error {
		if on[0] {
			d.Flatten()
		}
		if on[1] {
			d.ClearInfo()
		}
		if on[2] {
			d.RemoveAnnotations()
		}
		if on[3] || on[4] {
			d.Sanitize()
		}
		return nil
	})
}

func runFlatten(x *exec) ([]item, error) {
	return x.each(func(d *ops.Doc) error { d.Flatten(); return nil })
}

func runDownload(x *exec) ([]item, error) {
	for _, it := range x.in["pdf"] {
		b, err := it.doc.Bytes()
		if err != nil {
			return nil, err
		}
		if err := x.st.output(len(b)); err != nil {
			return nil, err
		}
		x.res.Files = append(x.res.Files, File{Name: it.name, Data: b})
	}
	return nil, nil
}
