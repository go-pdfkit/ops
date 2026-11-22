// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"
)

// TestSignVerbReadsRegularFilesOnly: a directory (or a FIFO, a device) given
// for any file the verb reads is refused by name, not opened: opening a FIFO
// waits for a writer for ever. A directory stands for them all on every
// platform; signp12_unix_test.go has the FIFO.
func TestSignVerbReadsRegularFilesOnly(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	out := filepath.Join(dir, "out.pdf")
	key, cert := credentials(t, dir, "ecdsa")
	ec, good := p12Path(t, "ec.p12"), passFile(t, fixturePassword)
	for name, args := range map[string][]string{
		"-p12":     {"sign", "-p12", dir, "-p12pass", good, in, out},
		"-p12pass": {"sign", "-p12", ec, "-p12pass", dir, in, out},
		"-key":     {"sign", "-key", dir, "-cert", cert, in, out},
		"-cert":    {"sign", "-key", key, "-cert", dir, in, out},
		"-chain":   {"sign", "-key", key, "-cert", cert, "-chain", dir, in, out},
	} {
		code, _, msg := exec(args...)
		if code == 0 || !strings.Contains(msg, "is a directory") {
			t.Errorf("%s: code %d: %q", name, code, msg)
		}
	}
}

// TestSignVerbUnusablePassword: a password pkcs12 cannot use (not UTF-8) is
// said to be unusable, not to be a damaged file.
func TestSignVerbUnusablePassword(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", passFile(t, "\xff\xfe\xfd"), fixture(t, 1), out)
	if code == 0 || !strings.Contains(msg, "cannot be used") || strings.Contains(msg, "not a usable") {
		t.Errorf("code %d: %q", code, msg)
	}
}

// TestReadBoundedFailures: a regular file that cannot be opened or read is an
// error, not an empty file.
func TestReadBoundedFailures(t *testing.T) {
	defer func(a func(string) (*os.File, error), b func(io.Reader) ([]byte, error)) { openFile, readAll = a, b }(openFile, readAll)
	p := passFile(t, "x")
	openFile = func(string) (*os.File, error) { return nil, errors.New("no permission") }
	if _, err := readBounded(p, 10); err == nil || !strings.Contains(err.Error(), "no permission") {
		t.Errorf("open: %v", err)
	}
	openFile = os.Open
	readAll = func(io.Reader) ([]byte, error) { return nil, errors.New("disk fell off") }
	if _, err := readBounded(p, 10); err == nil || !strings.Contains(err.Error(), "disk fell off") {
		t.Errorf("read: %v", err)
	}
}

func TestSaveTerminal(t *testing.T) {
	defer func(a func(int) (*term.State, error), b func(int, *term.State) error) { getState, restoreState = a, b }(getState, restoreState)
	st := &term.State{}
	var got struct {
		fd int
		st *term.State
	}
	getState = func(fd int) (*term.State, error) { return st, nil }
	restoreState = func(fd int, s *term.State) error { got.fd, got.st = fd, s; return nil }
	restore, err := saveTerminal(7)
	if err != nil {
		t.Fatal(err)
	}
	restore()
	if got.fd != 7 || got.st != st {
		t.Errorf("restored %+v", got)
	}
	getState = func(int) (*term.State, error) { return nil, errors.New("not a terminal") }
	if _, err := saveTerminal(7); err == nil {
		t.Error("no state, no error")
	}
}

// TestGuardPromptRestoresOnASignal: Ctrl-C and a kill put the terminal back
// before the process ends, and exit with the shell's code for the signal.
func TestGuardPromptRestoresOnASignal(t *testing.T) {
	for sig, want := range map[os.Signal]int{os.Interrupt: 130, syscall.SIGTERM: 143, syscall.SIGQUIT: 131} {
		sigs := make(chan os.Signal, 1)
		restored := make(chan struct{}, 1)
		exited := make(chan int, 1)
		stop := guardPrompt(func() { restored <- struct{}{} }, sigs, func(c int) { exited <- c })
		sigs <- sig
		select {
		case c := <-exited:
			if c != want {
				t.Errorf("%v: exit %d, want %d", sig, c, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%v: no exit", sig)
		}
		if len(restored) != 1 {
			t.Errorf("%v: the terminal was not restored before exit", sig)
		}
		stop()
	}
}

// TestGuardPromptStopsQuietly: when the prompt ends by itself the guard goes
// away and restores nothing.
func TestGuardPromptStopsQuietly(t *testing.T) {
	sigs := make(chan os.Signal, 1)
	stop := guardPrompt(func() { t.Error("restored without a signal") }, sigs, func(int) { t.Error("exited without a signal") })
	stop()
}

// TestThePromptIsGuardedOnATerminal: with a terminal state to save, the prompt
// still returns what was typed and leaves the terminal alone.
func TestThePromptIsGuardedOnATerminal(t *testing.T) {
	defer func(a func() bool, b func() ([]byte, error), c func(int) (*term.State, error), d func(int, *term.State) error) {
		stdinIsTerminal, readSecret, getState, restoreState = a, b, c, d
	}(stdinIsTerminal, readSecret, getState, restoreState)
	stdinIsTerminal = func() bool { return true }
	readSecret = func() ([]byte, error) { return []byte("typed"), nil }
	getState = func(int) (*term.State, error) { return &term.State{}, nil }
	restoreState = func(int, *term.State) error { t.Error("restored without a signal"); return nil }
	var errOut bytes.Buffer
	got, err := askPassword("p: ", &errOut)
	if err != nil || string(got) != "typed" {
		t.Errorf("%q %v", got, err)
	}
}
