package main

import (
	"bytes"
	stdcontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-pdfkit/ops/workflow"
)

// maxSecretsFile bounds the secrets file: it holds a few passwords.
const maxSecretsFile = 1 << 20

// runWorkflow is `pdfops workflow`: run a BentoPDF workflow file on PDFs, or
// check it, or list the node types it understands.
func runWorkflow(c *context, args []string) error {
	if len(args) == 0 {
		return errors.New("expected check, run or nodes")
	}
	switch args[0] {
	case "check":
		return workflowCheck(c, args[1:])
	case "run":
		return workflowRun(c, args[1:])
	case "nodes":
		return workflowNodes(c, args[1:])
	}
	return fmt.Errorf("no such workflow command %q; there is check, run and nodes", args[0])
}

// loadWorkflow reads and parses a workflow file, refusing a large one.
func loadWorkflow(path string) (*workflow.Workflow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return workflow.Read(f)
}

func workflowCheck(c *context, args []string) error {
	fs := flags("workflow check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "<workflow.json>"); err != nil {
		return err
	}
	w, err := loadWorkflow(fs.Arg(0))
	if err != nil {
		return err
	}
	issues := workflow.Check(w)
	for _, i := range issues {
		fmt.Fprintln(c.out, i)
	}
	if workflow.HasError(issues) {
		return errors.New("the workflow cannot run")
	}
	fmt.Fprintf(c.out, "ok: %d nodes, %d connections\n", len(w.Nodes), len(w.Connections))
	return nil
}

func workflowNodes(c *context, args []string) error {
	fs := flags("workflow nodes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 0, "no arguments"); err != nil {
		return err
	}
	for _, m := range workflow.Mapping() {
		if m.Verb == "" {
			fmt.Fprintf(c.out, "%-24s  (no verb here yet)\n", m.Type)
			continue
		}
		fmt.Fprintf(c.out, "%-24s  %s\n", m.Type, m.Verb)
	}
	return nil
}

// interspersed parses flags that may come before, between or after the
// positional arguments, and returns the positionals.
func interspersed(fs interface {
	Parse([]string) error
	NArg() int
	Args() []string
}, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Args()[0])
		args = fs.Args()[1:]
	}
}

func workflowRun(c *context, args []string) error {
	fs := flags("workflow run")
	out := fs.String("o", "", "where to write the result: a PDF, or a ZIP when there are several")
	secretsPath := fs.String("secrets", "", "a JSON file of passwords, {\"nodeId\":{\"userPassword\":\"…\"}}")
	pos, err := interspersed(fs, args)
	if err != nil {
		return err
	}
	if *out == "" {
		return errors.New("expected -o <out>")
	}
	if len(pos) < 2 {
		return errors.New("expected <workflow.json> <in.pdf> [in.pdf …]")
	}
	w, err := loadWorkflow(pos[0])
	if err != nil {
		return err
	}
	issues := workflow.Check(w)
	if workflow.HasError(issues) {
		for _, i := range issues {
			fmt.Fprintln(c.errOut, i)
		}
		return errors.New("the workflow cannot run")
	}
	opt := workflow.Options{Secrets: map[string]map[string]string{}}
	if *secretsPath != "" {
		if opt.Secrets, err = readSecrets(*secretsPath); err != nil {
			return err
		}
	}
	if c.password != "" {
		if opt.Secrets["*"] == nil {
			opt.Secrets["*"] = map[string]string{}
		}
		opt.Secrets["*"]["password"] = c.password
	}
	inputs, err := assignInputs(w, pos[1:])
	if err != nil {
		return err
	}
	res, err := workflow.Run(stdcontext.Background(), w, inputs, opt)
	if err != nil {
		return err
	}
	for _, i := range res.Issues {
		fmt.Fprintln(c.errOut, i)
	}
	var buf bytes.Buffer
	zipped, err := res.Encode(&buf)
	if err == nil {
		err = writeAtomic(*out, buf.Bytes())
	}
	if err != nil {
		return err
	}
	kind := "PDF"
	if zipped {
		kind = fmt.Sprintf("ZIP of %d PDFs", len(res.Files))
	}
	fmt.Fprintf(c.out, "wrote %s (%s)\n", *out, kind)
	return nil
}

// readSecrets reads the passwords file.
func readSecrets(path string) (map[string]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSecretsFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSecretsFile {
		return nil, errors.New("the secrets file is too large")
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("the secrets file: %w", err)
	}
	if m == nil {
		m = map[string]map[string]string{}
	}
	return m, nil
}

// maxInputBytes bounds the PDFs read for one run, all files together. It is a
// variable so a test can lower it. Each file is read through readBounded with
// what is left of it, so no file is read whole before it is refused.
var maxInputBytes = workflow.DefaultMaxBytes

// writeAtomic writes a file by renaming a finished temporary one over it, so
// a reader never sees half a result and a symlink at path is replaced, not
// followed to wherever it points.
func writeAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pdfops-*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// assignInputs gives the files to the input nodes: all to the one input
// node, or one each when there are as many.
func assignInputs(w *workflow.Workflow, files []string) (map[string][]workflow.File, error) {
	var ids []string
	for _, n := range w.Nodes {
		if n.Type == "PDFInputNode" {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) != 1 && len(ids) != len(files) {
		return nil, fmt.Errorf("the workflow has %d input nodes and %d files were given: give one file for each, or use a workflow with one input node", len(ids), len(files))
	}
	in := map[string][]workflow.File{}
	remaining := int64(maxInputBytes)
	for i, p := range files {
		b, err := readBounded(p, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= int64(len(b))
		id := ids[0]
		if len(ids) > 1 {
			id = ids[i]
		}
		in[id] = append(in[id], workflow.File{Name: filepath.Base(p), Data: b})
	}
	return in, nil
}
