// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withStdin(t *testing.T, r io.Reader) {
	t.Helper()
	old := passStdin
	passStdin = r
	t.Cleanup(func() { passStdin = old })
}

// TestSignVerbReadsThePasswordFromStandardInput: -p12pass - takes the password
// from standard input, one trailing line ending (either convention) not part of
// it, as -p12pass <file> does.
func TestSignVerbReadsThePasswordFromStandardInput(t *testing.T) {
	in := fixture(t, 1)
	for _, eol := range []string{"", "\n", "\r\n"} {
		withStdin(t, strings.NewReader(fixturePassword+eol))
		out := filepath.Join(t.TempDir(), "out.pdf")
		code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", "-", in, out)
		if code != 0 {
			t.Fatalf("%q: code %d: %s", eol, code, msg)
		}
		verifiedOnce(t, out)
	}
	// Only ONE line ending is stripped: a second one is part of the password.
	withStdin(t, strings.NewReader(fixturePassword+"\n\n"))
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", "-", in, out); code == 0 || !strings.Contains(msg, "wrong password") {
		t.Errorf("two line endings: code %d: %q", code, msg)
	}
}

// TestStandardInputPasswordIsBounded: more than the bound is refused, not read
// whole; exactly the bound is read (and then judged by pkcs12 like any other).
func TestStandardInputPasswordIsBounded(t *testing.T) {
	in := fixture(t, 1)
	out := filepath.Join(t.TempDir(), "out.pdf")
	withStdin(t, strings.NewReader(strings.Repeat("a", maxPasswordFile+1)))
	code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", "-", in, out)
	if code == 0 || !strings.Contains(msg, "standard input is longer than the 4098 bytes") {
		t.Errorf("over the bound: code %d: %q", code, msg)
	}
	withStdin(t, strings.NewReader(strings.Repeat("a", maxPasswordFile)))
	code, _, msg = exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", "-", in, out)
	if code == 0 || strings.Contains(msg, "longer than the") || !strings.Contains(msg, "cannot be used") {
		t.Errorf("at the bound: code %d: %q", code, msg)
	}
}

// TestStandardInputPasswordFailures: a read error is an error, not an empty
// password.
func TestStandardInputPasswordFailures(t *testing.T) {
	defer func(b func(io.Reader) ([]byte, error)) { readAll = b }(readAll)
	readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("pipe broke") }
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", "-", fixture(t, 1), out)
	if code == 0 || !strings.Contains(msg, "pipe broke") {
		t.Errorf("code %d: %q", code, msg)
	}
}

// TestStandardInputCannotBeBothPasswordAndDocument: stdin is the password's, so
// it cannot also be the PDF.
func TestStandardInputCannotBeBothPasswordAndDocument(t *testing.T) {
	withStdin(t, strings.NewReader(fixturePassword))
	out := filepath.Join(t.TempDir(), "out.pdf")
	for _, in := range []string{"-", "/dev/stdin"} {
		code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", "-", in, out)
		if code == 0 || !strings.Contains(msg, "cannot also be") {
			t.Errorf("%s: code %d: %q", in, code, msg)
		}
	}
}

// TestReadBoundedLooksAtWhatItOpened: the file is examined after it is opened,
// so a file that cannot be examined is an error, and one that was swapped for
// something else between a path check and the open is refused by what it is.
func TestReadBoundedLooksAtWhatItOpened(t *testing.T) {
	defer func(a func(string) (*os.File, error)) { openFile = a }(openFile)
	openFile = func(p string) (*os.File, error) {
		f, err := os.Open(p)
		if err == nil {
			f.Close() // Stat on a closed file fails
		}
		return f, err
	}
	func() {
		// Without the check the code would go on to use a result that is not
		// there; say so as a failed assertion, not as a crash.
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("a file that cannot be examined was used: %v", r)
			}
		}()
		if _, err := readBounded(passFile(t, "x"), 10); err == nil {
			t.Error("a file that cannot be examined was read")
		}
	}()
	openFile = func(string) (*os.File, error) { return os.Open(os.DevNull) }
	if _, err := readBounded("whatever", 10); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a device that replaced the file: %v", err)
	}
}
