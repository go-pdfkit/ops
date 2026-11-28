//go:build unix

package main

import (
	stdcontext "context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The result keeps the mode of the file it replaces, and a new one gets 0666
// less the umask, as os.Create does.
func TestWriteAtomicMode(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := t.TempDir()

	fresh := filepath.Join(dir, "fresh.pdf")
	if err := writeAtomic(stdcontext.Background(), fresh, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o600 {
		t.Errorf("new file mode %v, want 0600 under umask 077", fi.Mode().Perm())
	}

	kept := filepath.Join(dir, "kept.pdf")
	if err := os.WriteFile(kept, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(kept, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(stdcontext.Background(), kept, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(kept); fi.Mode().Perm() != 0o640 {
		t.Errorf("replaced file mode %v, want 0640 (the old one's)", fi.Mode().Perm())
	}
}
