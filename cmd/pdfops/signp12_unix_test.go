// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build unix

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"
)

// TestSignVerbRefusesAFIFO: opening a FIFO for reading blocks until something
// writes to it. The verb must refuse it without opening it, for -p12 and for
// the password file, and answer within seconds.
func TestSignVerbRefusesAFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	in := fixture(t, 1)
	out := filepath.Join(dir, "out.pdf")
	for name, args := range map[string][]string{
		"-p12":     {"sign", "-p12", fifo, "-p12pass", passFile(t, fixturePassword), in, out},
		"-p12pass": {"sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", fifo, in, out},
	} {
		done := make(chan string, 1)
		go func() {
			code, _, msg := exec(args...)
			if code == 0 {
				msg = "succeeded"
			}
			done <- msg
		}()
		select {
		case msg := <-done:
			if !strings.Contains(msg, "not a regular file") {
				t.Errorf("%s: %q", name, msg)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s: hung on a FIFO", name)
		}
	}
}

// TestACtrlCAtThePromptRestoresTheTerminal sends the process a real SIGINT
// while the prompt is open: the terminal is restored and the process would
// exit 130 (the exit is replaced, so the test survives).
func TestACtrlCAtThePromptRestoresTheTerminal(t *testing.T) {
	defer func(a func() bool, b func() ([]byte, error), c func(int) (*term.State, error), d func(int, *term.State) error, e func(int)) {
		stdinIsTerminal, readSecret, getState, restoreState, osExit = a, b, c, d, e
	}(stdinIsTerminal, readSecret, getState, restoreState, osExit)
	for sig, want := range map[syscall.Signal]int{syscall.SIGINT: 130, syscall.SIGQUIT: 131} {
		restored := make(chan struct{}, 1)
		exited := make(chan int, 1)
		stdinIsTerminal = func() bool { return true }
		getState = func(int) (*term.State, error) { return &term.State{}, nil }
		restoreState = func(int, *term.State) error { restored <- struct{}{}; return nil }
		osExit = func(c int) { exited <- c }
		readSecret = func() ([]byte, error) {
			if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
				t.Error(err)
			}
			select {
			case c := <-exited:
				if c != want {
					t.Errorf("%v: exit %d, want %d", sig, c, want)
				}
			case <-time.After(10 * time.Second):
				t.Errorf("no exit after %v", sig)
			}
			return nil, nil
		}
		var errOut bytes.Buffer
		if _, err := askPassword("p: ", &errOut); err != nil {
			t.Fatal(err)
		}
		if len(restored) != 1 {
			t.Errorf("%v: the terminal was not restored", sig)
		}
	}
}
