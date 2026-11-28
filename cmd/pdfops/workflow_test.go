package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pdfkit/ops"
)

const flowJSON = `{"version":1,"nodes":[
 {"id":"in","type":"PDFInputNode","position":{"x":0,"y":0},"controls":{}},
 {"id":"mid","type":"DeletePagesNode","position":{"x":1,"y":1},"controls":{"pages":"1"}},
 {"id":"out","type":"DownloadNode","position":{"x":2,"y":2},"controls":{}}],
 "connections":[
 {"id":"a","source":"in","sourceOutput":"pdf","target":"mid","targetInput":"pdf"},
 {"id":"b","source":"mid","sourceOutput":"pdf","target":"out","targetInput":"pdf"}]}`

const encFlowJSON = `{"version":1,"nodes":[
 {"id":"in","type":"PDFInputNode","controls":{}},
 {"id":"e","type":"EncryptNode","controls":{}},
 {"id":"out","type":"DownloadNode","controls":{}}],
 "connections":[
 {"id":"a","source":"in","sourceOutput":"pdf","target":"e","targetInput":"pdf"},
 {"id":"b","source":"e","sourceOutput":"pdf","target":"out","targetInput":"pdf"}]}`

const twoInputsJSON = `{"version":1,"nodes":[
 {"id":"i1","type":"PDFInputNode","controls":{}},{"id":"i2","type":"PDFInputNode","controls":{}},
 {"id":"m","type":"MergeNode","controls":{}},{"id":"out","type":"DownloadNode","controls":{}}],
 "connections":[
 {"id":"a","source":"i1","sourceOutput":"pdf","target":"m","targetInput":"pdf"},
 {"id":"b","source":"i2","sourceOutput":"pdf","target":"m","targetInput":"pdf"},
 {"id":"c","source":"m","sourceOutput":"pdf","target":"out","targetInput":"pdf"}]}`

func wfPDF(t *testing.T, pages int) []byte {
	t.Helper()
	d := ops.New()
	for i := 0; i < pages; i++ {
		d.Blank(100, 200)
	}
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func put(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func wfRun(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestWorkflowCheckAndNodes(t *testing.T) {
	dir := t.TempDir()
	flow := put(t, dir, "f.json", []byte(flowJSON))
	if code, out, e := wfRun("workflow", "check", flow); code != 0 || !strings.Contains(out, "ok: 3 nodes, 2 connections") {
		t.Fatal(code, out, e)
	}
	bad := put(t, dir, "bad.json", []byte(strings.Replace(flowJSON, "DeletePagesNode", "CropNode", 1)))
	if code, out, e := wfRun("workflow", "check", bad); code != 1 || !strings.Contains(out, "no pdfops verb") || !strings.Contains(e, "cannot run") {
		t.Fatal(code, out, e)
	}
	if code, out, _ := wfRun("workflow", "nodes"); code != 0 || !strings.Contains(out, "RotateNode") || !strings.Contains(out, "CropNode") || !strings.Contains(out, "no verb here yet") {
		t.Fatal(code, out)
	}
	for _, args := range [][]string{
		{"workflow"}, {"workflow", "nope"}, {"workflow", "check"}, {"workflow", "check", "-x"}, {"workflow", "check", filepath.Join(dir, "missing.json")},
		{"workflow", "nodes", "extra"}, {"workflow", "nodes", "-x"},
		{"workflow", "check", put(t, dir, "junk.json", []byte("{"))},
	} {
		if code, _, e := wfRun(args...); code != 1 || e == "" {
			t.Errorf("%v: %d %q", args, code, e)
		}
	}
}

func TestWorkflowRun(t *testing.T) {
	dir := t.TempDir()
	flow := put(t, dir, "f.json", []byte(flowJSON))
	in := put(t, dir, "in.pdf", wfPDF(t, 3))
	out := filepath.Join(dir, "out.pdf")
	// Flags may follow the positionals.
	if code, o, e := wfRun("workflow", "run", flow, in, "-o", out); code != 0 || !strings.Contains(o, "wrote "+out+" (PDF)") {
		t.Fatal(code, o, e)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := ops.Open(b); err != nil || d.PageCount() != 2 {
		t.Fatal(err)
	}
	// Two files into one input node: a merge-less Download makes a ZIP.
	zout := filepath.Join(dir, "out.zip")
	if code, o, e := wfRun("workflow", "run", "-o", zout, flow, in, in); code != 0 || !strings.Contains(o, "ZIP of 2 PDFs") {
		t.Fatal(code, o, e)
	}
	zb, _ := os.ReadFile(zout)
	if zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb))); err != nil || len(zr.File) != 2 || zr.File[1].Name != "in-2.pdf" {
		t.Fatal(err)
	}
	// Warnings go to stderr, and the run goes on.
	warn := put(t, dir, "w.json", []byte(strings.Replace(flowJSON, `"pages":"1"`, `"pages":"1","bogus":1`, 1)))
	if code, _, _ := wfRun("workflow", "run", "-o", out, warn, in); code != 0 {
		t.Fatal(code)
	}
}

func TestWorkflowRunWarnsOnStderr(t *testing.T) {
	dir := t.TempDir()
	extra := strings.Replace(flowJSON, `"connections":[`, `"connections":[{"id":"z","source":"in","sourceOutput":"pdf","target":"x","targetInput":"pdf"},`, 1)
	extra = strings.Replace(extra, `{"id":"out"`, `{"id":"x","type":"RotateNode","controls":{"angle":"90"}},{"id":"out"`, 1)
	flow := put(t, dir, "x.json", []byte(extra))
	in := put(t, dir, "in.pdf", wfPDF(t, 2))
	if code, _, e := wfRun("workflow", "run", "-o", filepath.Join(dir, "o.pdf"), flow, in); code != 0 || !strings.Contains(e, "does not lead to an output") {
		t.Fatal(code, e)
	}
}

func TestWorkflowSecrets(t *testing.T) {
	dir := t.TempDir()
	flow := put(t, dir, "e.json", []byte(encFlowJSON))
	in := put(t, dir, "in.pdf", wfPDF(t, 1))
	out := filepath.Join(dir, "o.pdf")
	// No secret: Encrypt names what is missing.
	if code, _, e := wfRun("workflow", "run", "-o", out, flow, in); code != 1 || !strings.Contains(e, "no password") {
		t.Fatal(code, e)
	}
	sec := put(t, dir, "s.json", []byte(`{"e":{"userPassword":"u"}}`))
	if code, _, e := wfRun("workflow", "run", "-secrets", sec, "-o", out, flow, in); code != 0 {
		t.Fatal(code, e)
	}
	b, _ := os.ReadFile(out)
	if !bytes.Contains(b, []byte("/Encrypt")) {
		t.Fatal("not encrypted")
	}
	// The global -password feeds every input node.
	dec := strings.Replace(encFlowJSON, "EncryptNode", "DecryptNode", 1)
	dflow := put(t, dir, "d.json", []byte(dec))
	enc := put(t, dir, "enc.pdf", b)
	dout := filepath.Join(dir, "d.pdf")
	if code, _, e := wfRun("-password", "u", "workflow", "run", "-o", dout, dflow, enc); code != 0 {
		t.Fatal(code, e)
	}
	// ... alongside a secrets file, and a null secrets file is empty.
	if code, _, e := wfRun("-password", "u", "workflow", "run", "-secrets", sec, "-o", dout, dflow, enc); code != 0 {
		t.Fatal(code, e)
	}
	null := put(t, dir, "null.json", []byte("null"))
	if code, _, e := wfRun("-password", "u", "workflow", "run", "-secrets", null, "-o", dout, dflow, enc); code != 0 {
		t.Fatal(code, e)
	}
	// Refusals.
	big := put(t, dir, "big.json", bytes.Repeat([]byte(" "), maxSecretsFile+1))
	for _, s := range []string{dir, big, put(t, dir, "junk.json", []byte("[")), filepath.Join(dir, "none.json")} {
		if code, _, e := wfRun("workflow", "run", "-secrets", s, "-o", out, flow, in); code != 1 || e == "" {
			t.Errorf("%s: %d %q", s, code, e)
		}
	}
}

func TestWorkflowRunRefusals(t *testing.T) {
	dir := t.TempDir()
	flow := put(t, dir, "f.json", []byte(flowJSON))
	two := put(t, dir, "two.json", []byte(twoInputsJSON))
	bad := put(t, dir, "bad.json", []byte(strings.Replace(flowJSON, "DeletePagesNode", "NopeNode", 1)))
	junk := put(t, dir, "junk.json", []byte("{"))
	in := put(t, dir, "in.pdf", wfPDF(t, 1))
	notpdf := put(t, dir, "n.pdf", []byte("nope"))
	out := filepath.Join(dir, "o.pdf")
	for name, args := range map[string][]string{
		"no -o":       {"workflow", "run", flow, in},
		"no files":    {"workflow", "run", "-o", out, flow},
		"bad flag":    {"workflow", "run", "-x"},
		"missing wf":  {"workflow", "run", "-o", out, filepath.Join(dir, "m.json"), in},
		"junk wf":     {"workflow", "run", "-o", out, junk, in},
		"unrunnable":  {"workflow", "run", "-o", out, bad, in},
		"input count": {"workflow", "run", "-o", out, two, in},
		"missing pdf": {"workflow", "run", "-o", out, flow, filepath.Join(dir, "m.pdf")},
		"not a pdf":   {"workflow", "run", "-o", out, flow, notpdf},
		"unwritable":  {"workflow", "run", "-o", filepath.Join(dir, "no", "such", "o.pdf"), flow, put(t, dir, "two.pdf", wfPDF(t, 2))},
	} {
		if code, _, e := wfRun(args...); code != 1 || e == "" {
			t.Errorf("%s: %d %q", name, code, e)
		}
	}
	// Two input nodes take one file each.
	in2 := put(t, dir, "in2.pdf", wfPDF(t, 2))
	if code, _, e := wfRun("workflow", "run", "-o", out, two, in, in2); code != 0 {
		t.Fatal(code, e)
	}
	b, _ := os.ReadFile(out)
	if d, err := ops.Open(b); err != nil || d.PageCount() != 3 {
		t.Fatal(err)
	}
}

func TestWorkflowDroppedPasswordIsSaid(t *testing.T) {
	dir := t.TempDir()
	in := put(t, dir, "in.pdf", wfPDF(t, 2))
	flow := put(t, dir, "f.json", []byte(strings.Replace(flowJSON, `"pages":"1"`, `"pages":"1","password":"leak"`, 1)))
	out := filepath.Join(dir, "o.pdf")
	if code, _, e := wfRun("workflow", "run", "-o", out, flow, in); code != 0 || !strings.Contains(e, "password was in the file") {
		t.Fatal(code, e)
	}
}
