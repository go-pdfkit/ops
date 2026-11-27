package workflow

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/go-pdfkit/ops"
)

// File is a PDF going into or coming out of a run.
type File struct {
	Name string
	Data []byte
}

// Options bound a run and carry what a workflow file must not hold.
type Options struct {
	// Secrets are the passwords nodes need, by node id and control name
	// ("password" for an input, "userPassword" and "ownerPassword" for
	// Encrypt). The id "*" applies to every node that has no entry of its own
	// for that name.
	Secrets map[string]map[string]string

	// MaxPages bounds the pages the whole run may produce, counted per node
	// output, so a Poster feeding a Poster cannot multiply without end.
	// Zero means DefaultMaxPages.
	MaxPages int
	// MaxInputBytes and MaxOutputBytes bound the bytes read in and written
	// out. Zero means DefaultMaxBytes.
	MaxInputBytes  int
	MaxOutputBytes int
}

// The default bounds of a run.
const (
	DefaultMaxPages = 20000
	DefaultMaxBytes = 512 << 20
)

// Status is how a node ended.
type Status string

// A node is idle until it runs, then completed or error; a node that cannot
// reach an output stays idle. (BentoPDF's own node states.)
const (
	Idle      Status = "idle"
	Completed Status = "completed"
	Failed    Status = "error"
)

// Result is what a run produced.
type Result struct {
	Files  []File
	Status map[string]Status // by node id
	Issues []Issue           // what Check had to say that was not an error
}

// NodeError is the failure of one node. The run stops there.
type NodeError struct {
	ID, Type, Label string
	Err             error
}

func (e *NodeError) Error() string {
	return fmt.Sprintf("%s (node %s): %v", e.Label, e.ID, e.Err)
}

func (e *NodeError) Unwrap() error { return e.Err }

// ErrRefused is wrapped by the error of a workflow Check found wrong.
var ErrRefused = errors.New("workflow: refused")

// ErrBudget is wrapped by the error of a run that went over a bound.
var ErrBudget = errors.New("workflow: over budget")

// state is the work a run has done, against its bounds.
type state struct {
	maxPages, maxIn, maxOut int
	pages, in, out          int
}

func (s *state) budget(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBudget, fmt.Sprintf(format, a...))
}

// input charges bytes read.
func (s *state) input(n int) error {
	s.in += n
	if s.in > s.maxIn {
		return s.budget("more than %d bytes of input", s.maxIn)
	}
	return nil
}

// output charges bytes written.
func (s *state) output(n int) error {
	s.out += n
	if s.out > s.maxOut {
		return s.budget("more than %d bytes of output", s.maxOut)
	}
	return nil
}

// reserve refuses, before the work is done, a node whose output would be
// pages*times pages and would not fit in what is left.
func (s *state) reserve(pages, times int) error {
	if pages > (s.maxPages-s.pages)/times {
		return s.budget("this node would make %d pages, %d allowed in all", pages*times, s.maxPages)
	}
	return nil
}

// charge counts the pages a node produced.
func (s *state) charge(n int) error {
	s.pages += n
	if s.pages > s.maxPages {
		return s.budget("more than %d pages in all", s.maxPages)
	}
	return nil
}

func orDefault(n, d int) int {
	if n <= 0 {
		return d
	}
	return n
}

// Run checks the workflow and runs it. inputs gives the files of each input
// node by id. A refused workflow returns an error wrapping ErrRefused, naming
// every error; a failing node stops the run with a *NodeError and its Status
// is Failed in the Result returned beside it.
func Run(ctx context.Context, w *Workflow, inputs map[string][]File, opt Options) (*Result, error) {
	issues := Check(w)
	if HasError(issues) {
		var msgs []string
		for _, i := range issues {
			if i.Level == Error {
				msgs = append(msgs, i.String())
			}
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, strings.Join(msgs, "; "))
	}
	g := graph{byID: map[string]int{}}
	for _, n := range w.Nodes {
		g.byID[n.ID] = len(g.nodes)
		g.nodes = append(g.nodes, n)
	}
	g.conns = w.Connections
	order, _ := g.order()
	reach := g.reaching()

	res := &Result{Status: map[string]Status{}, Issues: issues}
	for _, n := range g.nodes {
		res.Status[n.ID] = Idle
	}
	st := &state{
		maxPages: orDefault(opt.MaxPages, DefaultMaxPages),
		maxIn:    orDefault(opt.MaxInputBytes, DefaultMaxBytes),
		maxOut:   orDefault(opt.MaxOutputBytes, DefaultMaxBytes),
	}
	consumers := map[string]int{} // how many connections read each node
	for _, c := range g.conns {
		if reach[c.Target] {
			consumers[c.Source]++
		}
	}
	outs := map[string][]item{}
	for _, id := range order {
		if !reach[id] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		n := g.nodes[g.byID[id]]
		s := nodes[n.Type]
		in := map[string][]item{}
		for _, c := range g.conns {
			if c.Target != id {
				continue
			}
			got := outs[c.Source]
			if consumers[c.Source] > 1 && s.category != Output {
				// Another node reads this too, and this one will change what
				// it gets: it gets its own copy.
				var err error
				if got, err = clone(got, st); err != nil {
					return fail(res, n, s, err)
				}
			}
			in[c.TargetInput] = append(in[c.TargetInput], got...)
		}
		x := &exec{node: n, in: in, st: st, files: inputs[id], sec: secretsFor(opt.Secrets, id), res: res}
		made, err := s.run(x)
		if err == nil {
			err = st.charge(pagesOf(made))
		}
		if err != nil {
			return fail(res, n, s, err)
		}
		outs[id] = made
		res.Status[id] = Completed
	}
	res.name()
	return res, nil
}

func fail(res *Result, n Node, s *spec, err error) (*Result, error) {
	res.Status[n.ID] = Failed
	return res, &NodeError{ID: n.ID, Type: n.Type, Label: s.label, Err: err}
}

func pagesOf(items []item) int {
	n := 0
	for _, it := range items {
		n += it.doc.PageCount()
	}
	return n
}

// clone copies documents by writing and reading them again, which is the one
// way a Doc is duplicated that shares nothing.
func clone(items []item, st *state) ([]item, error) {
	out := make([]item, len(items))
	for i, it := range items {
		if err := st.reserve(it.doc.PageCount(), 1); err != nil {
			return nil, err
		}
		b, err := it.doc.Bytes()
		if err == nil {
			err = st.input(len(b))
		}
		var d *ops.Doc
		if err == nil {
			d, err = ops.Open(b)
		}
		if err != nil {
			return nil, err
		}
		out[i] = item{it.name, d}
	}
	return out, nil
}

// secretsFor merges the "*" secrets under the node's own.
func secretsFor(all map[string]map[string]string, id string) map[string]string {
	m := map[string]string{}
	for k, v := range all["*"] {
		m[k] = v
	}
	for k, v := range all[id] {
		m[k] = v
	}
	return m
}

// name gives every output a safe, unique file name ending in .pdf.
func (r *Result) name() {
	seen := map[string]bool{}
	for i := range r.Files {
		base := cleanName(r.Files[i].Name)
		n := base
		for k := 2; seen[n]; k++ {
			n = fmt.Sprintf("%s-%d.pdf", strings.TrimSuffix(base, ".pdf"), k)
		}
		seen[n] = true
		r.Files[i].Name = n
	}
}

// cleanName keeps the last element of a name, without control characters or
// a path, and gives it a .pdf suffix. A name is data from a file name or a
// workflow, and ends up in an archive: it must not climb out of it.
func cleanName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '\\' {
			r = '/'
		}
		if r >= ' ' && r != 0x7f {
			b.WriteRune(r)
		}
	}
	base := path.Base(b.String())
	if base == "." || base == "/" || base == ".." {
		base = "document"
	}
	if !strings.HasSuffix(strings.ToLower(base), ".pdf") {
		base += ".pdf"
	}
	return base
}

// Encode writes the outputs the way BentoPDF's Download does: the PDF itself
// when there is one, a ZIP of them when there are several. It reports whether
// what it wrote is a ZIP.
func (r *Result) Encode(w io.Writer) (zipped bool, err error) {
	switch len(r.Files) {
	case 0:
		return false, errors.New("workflow: the run produced no file")
	case 1:
		_, err = w.Write(r.Files[0].Data)
		return false, err
	}
	zw := zip.NewWriter(w)
	for _, f := range r.Files {
		if err == nil {
			var fw io.Writer
			if fw, err = zw.Create(f.Name); err == nil {
				_, err = fw.Write(f.Data)
			}
		}
	}
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	return true, err
}
