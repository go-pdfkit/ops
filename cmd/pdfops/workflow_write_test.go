package main

import (
	stdcontext "context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An interrupt (ctx done) leaves no .pdfops-* file behind and does not touch
// the file it was to replace.
func TestWriteAtomicInterruptedLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := stdcontext.WithCancel(stdcontext.Background())
	cancel()
	err := writeAtomic(ctx, out, []byte(strings.Repeat("x", 3*writeChunk)))
	if !errors.Is(err, stdcontext.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".pdfops-*"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if b, _ := os.ReadFile(out); string(b) != "old" {
		t.Errorf("the old file was changed: %q", b)
	}
}

// A completed write leaves no temporary file either, and writes the bytes.
func TestWriteAtomicWrites(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")
	data := strings.Repeat("y", 2*writeChunk+5)
	if err := writeAtomic(stdcontext.Background(), out, []byte(data)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != data {
		t.Errorf("wrote %d bytes, want %d", len(b), len(data))
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".pdfops-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if err := writeAtomic(stdcontext.Background(), filepath.Join(dir, "no", "such", "out.pdf"), nil); err == nil {
		t.Error("a missing directory must be refused")
	}
}
