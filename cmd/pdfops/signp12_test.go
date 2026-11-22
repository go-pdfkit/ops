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
	"testing"

	"github.com/go-pdfkit/ops"
	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

// The fixtures are the library's: testdata/p12 at the module root, written by
// OpenSSL 4.1, password "fixture-only password".
const fixturePassword = "fixture-only password"

func p12Path(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "p12", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// passFile writes a password file, with the line ending an editor adds.
func passFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pass.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func verifiedOnce(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := signature.VerifyAll(d)
	if err != nil || len(rs) != 1 || rs[0].Err != nil || !rs[0].Signature.CoversWholeFile {
		t.Fatalf("%v %+v", err, rs)
	}
}

// TestSignVerbWithAP12 is the round trip through the command: sign with a
// .p12, verify with the fleet's verifier. The password file ends with a line
// ending, as one written by an editor does, with either convention.
func TestSignVerbWithAP12(t *testing.T) {
	in := fixture(t, 1)
	for _, file := range []string{"rsa-chain.p12", "ec.p12"} {
		for _, eol := range []string{"", "\n", "\r\n"} {
			out := filepath.Join(t.TempDir(), "out.pdf")
			code, _, msg := exec("sign", "-p12", p12Path(t, file), "-p12pass", passFile(t, fixturePassword+eol),
				"-reason", "I agree", in, out)
			if code != 0 {
				t.Fatalf("%s %q: code %d: %s", file, eol, code, msg)
			}
			verifiedOnce(t, out)
		}
	}
}

func TestSignVerbAsksForThePassword(t *testing.T) {
	defer func(f func(string, io.Writer) ([]byte, error)) { askPassword = f }(askPassword)
	var asked string
	typed := []byte(fixturePassword)
	askPassword = func(prompt string, _ io.Writer) ([]byte, error) {
		asked = prompt
		return typed, nil
	}
	out := filepath.Join(t.TempDir(), "out.pdf")
	if code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), fixture(t, 1), out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	if !strings.Contains(asked, "password") {
		t.Errorf("the prompt was %q", asked)
	}
	verifiedOnce(t, out)
	if !bytes.Equal(typed, make([]byte, len(typed))) {
		t.Errorf("the typed password was left in its buffer: %q", typed)
	}

	askPassword = func(string, io.Writer) ([]byte, error) { return nil, errors.New("the terminal went away") }
	if code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), fixture(t, 1), out); code == 0 || !strings.Contains(msg, "terminal went away") {
		t.Errorf("a failed prompt: code %d %q", code, msg)
	}
}

// TestThePromptReadsWithoutEcho: on a terminal the prompt goes to the error
// stream, the answer is read through the no-echo reader, and a line break
// follows what the person typed (which was not echoed).
func TestThePromptReadsWithoutEcho(t *testing.T) {
	defer func(a func() bool, b func() ([]byte, error)) { stdinIsTerminal, readSecret = a, b }(stdinIsTerminal, readSecret)
	stdinIsTerminal = func() bool { return true }
	readSecret = func() ([]byte, error) { return []byte("typed"), nil }
	var errOut bytes.Buffer
	got, err := askPassword("p: ", &errOut)
	if err != nil || string(got) != "typed" || errOut.String() != "p: \n" {
		t.Errorf("%q %v %q", got, err, errOut.String())
	}
	readSecret = func() ([]byte, error) { return nil, errors.New("interrupted") }
	if _, err := askPassword("p: ", &errOut); err == nil {
		t.Error("an interrupted prompt gave a password")
	}
}

// TestThePasswordPromptNeedsATerminal: with no file and no terminal (go test
// has none on standard input) the command says what to do instead of reading
// a password from a pipe, the environment or the command line.
func TestThePasswordPromptNeedsATerminal(t *testing.T) {
	var errOut bytes.Buffer
	if _, err := askPassword("p: ", &errOut); err == nil || !strings.Contains(err.Error(), "-p12pass") {
		t.Errorf("error %v", err)
	}
}

func TestSignVerbP12Refusals(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	out := filepath.Join(dir, "out.pdf")
	key, cert := credentials(t, dir, "ecdsa")
	ec, rsaP12 := p12Path(t, "ec.p12"), p12Path(t, "rsa-chain.p12")
	good := passFile(t, fixturePassword)

	oversized := filepath.Join(dir, "huge.p12")
	if err := os.WriteFile(oversized, make([]byte, ops.MaxPKCS12Size+1), 0o600); err != nil {
		t.Fatal(err)
	}
	garbage := filepath.Join(dir, "garbage.p12")
	if err := os.WriteFile(garbage, []byte("not a p12"), 0o600); err != nil {
		t.Fatal(err)
	}
	hugePass := passFile(t, strings.Repeat("x", maxPasswordFile+1))

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"wrong password", []string{"-p12", rsaP12, "-p12pass", passFile(t, "nope\n")}, "wrong password"},
		{"oversized file", []string{"-p12", oversized, "-p12pass", good}, "larger than"},
		{"oversized password file", []string{"-p12", ec, "-p12pass", hugePass}, "larger than"},
		{"not a p12", []string{"-p12", garbage, "-p12pass", good}, "not a usable .p12"},
		{"no MAC is not supported", []string{"-p12", p12Path(t, "nomac.p12"), "-p12pass", good}, "not supported"},
		{"too costly to open", []string{"-p12", p12Path(t, "costly.p12"), "-p12pass", good}, "too large or too costly"},
		{"missing file", []string{"-p12", filepath.Join(dir, "absent.p12"), "-p12pass", good}, "absent.p12"},
		{"missing password file", []string{"-p12", ec, "-p12pass", filepath.Join(dir, "absent.txt")}, "absent.txt"},
		{"p12 and key", []string{"-p12", ec, "-key", key, "-p12pass", good}, "not both"},
		{"p12 and cert", []string{"-p12", ec, "-cert", cert, "-p12pass", good}, "not both"},
		{"password file without p12", []string{"-p12pass", good, "-key", key, "-cert", cert}, "goes with -p12"},
		{"nothing to sign with", nil, "-p12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, msg := exec(append(append([]string{"sign"}, tc.args...), in, out)...)
			if code == 0 {
				t.Fatal("signed")
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("the message %q does not say %q", msg, tc.want)
			}
			if strings.Contains(msg, fixturePassword) {
				t.Errorf("the message holds the password: %q", msg)
			}
			if _, err := os.Stat(out); err == nil {
				t.Error("a refused signature left a file behind")
			}
		})
	}
}

// TestSignVerbP12WithAnExtraChain: -chain adds to what the .p12 held.
func TestSignVerbP12WithAnExtraChain(t *testing.T) {
	dir := t.TempDir()
	_, cert := credentials(t, dir, "rsa")
	out := filepath.Join(dir, "out.pdf")
	if code, _, msg := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", passFile(t, fixturePassword),
		"-chain", cert, fixture(t, 1), out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	verifiedOnce(t, out)
	if code, _, _ := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", passFile(t, fixturePassword),
		"-chain", filepath.Join(dir, "absent.pem"), fixture(t, 1), out); code == 0 {
		t.Error("a missing chain file was accepted")
	}
}

// TestNoWayToPutThePasswordOnTheCommandLine: -p12pass is a file name. A
// password given where a file name goes is read as a file name, and is refused
// as one that does not exist, not tried as a password.
func TestNoWayToPutThePasswordOnTheCommandLine(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, _, _ := exec("sign", "-p12", p12Path(t, "ec.p12"), "-p12pass", fixturePassword, fixture(t, 1), out)
	if code == 0 {
		t.Fatal("signed with the password as an argument")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a file was written")
	}
}
