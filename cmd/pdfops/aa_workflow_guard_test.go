package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Every refusal of `pdfops workflow` is asserted by what it says: a command
// that fails for another reason must not pass for this one.
func TestWorkflowRefusalsSayWhy(t *testing.T) {
	dir := t.TempDir()
	flow := put(t, dir, "f.json", []byte(flowJSON))
	two := put(t, dir, "two.json", []byte(twoInputsJSON))
	bad := put(t, dir, "bad.json", []byte(strings.Replace(flowJSON, "DeletePagesNode", "NopeNode", 1)))
	junk := put(t, dir, "junk.json", []byte("{"))
	in := put(t, dir, "in.pdf", wfPDF(t, 2))
	notpdf := put(t, dir, "n.pdf", []byte("nope"))
	out := filepath.Join(dir, "o.pdf")
	missing := filepath.Join(dir, "missing")
	big := put(t, dir, "big.json", bytes.Repeat([]byte(" "), maxSecretsFile+1))
	sjunk := put(t, dir, "sjunk.json", []byte("["))

	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no subcommand":      {[]string{"workflow"}, "expected check, run or nodes"},
		"unknown subcommand": {[]string{"workflow", "nope"}, `no such workflow command "nope"`},
		"check no file":      {[]string{"workflow", "check"}, "<workflow.json>"},
		"check bad flag":     {[]string{"workflow", "check", "-x"}, "-x"},
		"check missing":      {[]string{"workflow", "check", missing}, "no such file"},
		"check junk":         {[]string{"workflow", "check", junk}, "invalid"},
		"check unrunnable":   {[]string{"workflow", "check", bad}, "the workflow cannot run"},
		"nodes extra":        {[]string{"workflow", "nodes", "extra"}, "no arguments"},
		"nodes bad flag":     {[]string{"workflow", "nodes", "-x"}, "-x"},
		"run bad flag":       {[]string{"workflow", "run", "-x"}, "-x"},
		"run no -o":          {[]string{"workflow", "run", flow, in}, "expected -o"},
		"run no files":       {[]string{"workflow", "run", "-o", out, flow}, "expected <workflow.json> <in.pdf>"},
		"run missing wf":     {[]string{"workflow", "run", "-o", out, missing, in}, "no such file"},
		"run junk wf":        {[]string{"workflow", "run", "-o", out, junk, in}, "invalid"},
		"run unrunnable":     {[]string{"workflow", "run", "-o", out, bad, in}, "the workflow cannot run"},
		"input count":        {[]string{"workflow", "run", "-o", out, two, in}, "2 input nodes and 1 files"},
		"missing pdf":        {[]string{"workflow", "run", "-o", out, flow, missing}, "no such file"},
		"missing pdf (2)":    {[]string{"workflow", "run", "-o", out, two, in, missing}, "no such file"},
		"not a pdf":          {[]string{"workflow", "run", "-o", out, flow, notpdf}, "n.pdf"},
		"unwritable":         {[]string{"workflow", "run", "-o", filepath.Join(dir, "no", "such", "o.pdf"), flow, in}, "no such file"},
		"secrets dir":        {[]string{"workflow", "run", "-secrets", dir, "-o", out, flow, in}, "is a directory"},
		"secrets missing":    {[]string{"workflow", "run", "-secrets", missing, "-o", out, flow, in}, "no such file"},
		"secrets big":        {[]string{"workflow", "run", "-secrets", big, "-o", out, flow, in}, "too large"},
		"secrets junk":       {[]string{"workflow", "run", "-secrets", sjunk, "-o", out, flow, in}, "the secrets file"},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panicked: %v", name, r)
				}
			}()
			code, _, e := wfRun(c.args...)
			if code != 1 || !strings.Contains(e, c.want) {
				t.Errorf("%s: %d %q, want %q", name, code, e, c.want)
			}
		}()
	}
	// The secrets file is read to its end: one byte under the cap is read.
	edge := put(t, dir, "edge.json", append([]byte("{}"), bytes.Repeat([]byte(" "), maxSecretsFile-2)...))
	if code, _, e := wfRun("workflow", "run", "-secrets", edge, "-o", out, flow, in); code != 0 {
		t.Fatalf("%d %q", code, e)
	}
}
