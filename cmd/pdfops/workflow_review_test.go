package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -o is written by renaming a finished file over it: a symlink there is
// replaced, and what it pointed at is left alone.
func TestWorkflowOutputIsAtomic(t *testing.T) {
	dir := t.TempDir()
	flow := put(t, dir, "f.json", []byte(flowJSON))
	in := put(t, dir, "in.pdf", wfPDF(t, 3))
	victim := put(t, dir, "victim.txt", []byte("keep me"))
	out := filepath.Join(dir, "out.pdf")
	if err := os.Symlink(victim, out); err != nil {
		t.Skip(err)
	}
	if code, o, e := wfRun("workflow", "run", flow, in, "-o", out); code != 0 {
		t.Fatal(code, o, e)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Fatalf("the symlink was followed: %q", b)
	}
	if st, err := os.Lstat(out); err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o644 {
		t.Fatal(st, err)
	}
	// A failure leaves no temporary file behind.
	bad := filepath.Join(dir, "adir")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := wfRun("workflow", "run", flow, in, "-o", bad); code != 1 {
		t.Fatal(code)
	}
	if code, _, _ := wfRun("workflow", "run", flow, in, "-o", filepath.Join(dir, "no", "such", "out.pdf")); code != 1 {
		t.Fatal(code)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".pdfops-*"))
	if len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
}

// The input files are bounded together, and refused without being read whole.
func TestWorkflowInputBound(t *testing.T) {
	defer func(n int) { maxInputBytes = n }(maxInputBytes)
	dir := t.TempDir()
	flow := put(t, dir, "f.json", []byte(flowJSON))
	a := put(t, dir, "a.pdf", wfPDF(t, 2))
	st, _ := os.Stat(a)
	maxInputBytes = int(st.Size()) + 10 // one file fits, two do not
	out := filepath.Join(dir, "o.pdf")
	if code, o, e := wfRun("workflow", "run", flow, a, "-o", out); code != 0 {
		t.Fatal(code, o, e)
	}
	code, _, e := wfRun("workflow", "run", flow, a, a, "-o", out)
	if code != 1 || !strings.Contains(e, "larger than") {
		t.Fatal(code, e)
	}
	// A device has no end to be refused at.
	if code, _, e := wfRun("workflow", "run", flow, "/dev/zero", "-o", out); code != 1 || !strings.Contains(e, "not a regular file") {
		t.Fatal(code, e)
	}
}

// A workflow file whose keys differ by case from the format's is refused.
func TestWorkflowStrictKeys(t *testing.T) {
	dir := t.TempDir()
	f := put(t, dir, "f.json", []byte(strings.Replace(flowJSON, `"nodes"`, `"NODES"`, 1)))
	if code, _, e := wfRun("workflow", "check", f); code != 1 || e == "" {
		t.Fatal(code, e)
	}
}
