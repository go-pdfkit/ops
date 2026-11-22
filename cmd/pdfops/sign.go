// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-authn/pkcs12"
	"golang.org/x/term"

	"github.com/go-pdfkit/ops"
)

// The sizes this verb will read. A key, a certificate or a chain is a few
// kilobytes; a document is the only large input, and /ByteRange cannot describe
// more than ten digits of it anyway.
const (
	maxCredentialFile = 1 << 20
	maxSignInput      = 1 << 30
	// maxSignImage is the most of a picture this will read for a visible
	// signature; a signature's picture is a logo or a scanned hand.
	maxSignImage = 16 << 20
	// maxVisibleArg bounds the -visible value before it is parsed.
	maxVisibleArg = 256
)

// parseVisible reads the -visible value: four finite numbers, x0,y0,x1,y1.
// Whether they make a box that is on the page is ops.Sign's to say.
func parseVisible(s string) ([4]float64, error) {
	var r [4]float64
	if len(s) > maxVisibleArg {
		return r, fmt.Errorf("-visible is %d bytes long; it is four numbers", len(s))
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return r, fmt.Errorf("-visible %q should be x0,y0,x1,y1: four numbers", s)
	}
	for i, p := range parts[:min(len(parts), len(r))] {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return [4]float64{}, fmt.Errorf("-visible %q: %q is not a finite number", s, p)
		}
		r[i] = v
	}
	return r, nil
}

// The file system, replaceable so that a test can make a regular file fail to
// open or to read, which a directory no longer does (readBounded refuses one
// before opening it).
var (
	openFile = openNoBlock
	readAll  = io.ReadAll
	// passStdin is where -p12pass - reads the password from.
	passStdin io.Reader = os.Stdin
)

// readBounded reads a file, refusing one longer than max rather than reading it
// whole first. Only a regular file is read: opening a FIFO would wait for a
// writer for ever, and a device (/dev/zero) has no end to be refused at. The
// file is looked at AFTER it is opened, and opened without waiting: a path that
// was a regular file a moment ago may have been swapped for a FIFO, and only
// what was actually opened can be trusted.
func readBounded(path string, max int64) ([]byte, error) {
	f, err := openFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%s)", path, st.Mode().Type())
	}
	b, err := readAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than the %d bytes this reads", path, max)
	}
	return b, nil
}

// parseCertificates reads every CERTIFICATE block of a PEM file.
func parseCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, errors.New("no CERTIFICATE block found")
	}
	return certs, nil
}

// parsePrivateKey reads the first private key of a PEM file: PKCS #8, PKCS #1
// (RSA) or SEC 1 (EC). An encrypted key is refused, not guessed at.
func parsePrivateKey(data []byte) (crypto.Signer, error) {
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return nil, errors.New("no private key block found")
		}
		switch block.Type {
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			switch s := k.(type) {
			case *rsa.PrivateKey, *ecdsa.PrivateKey, ed25519.PrivateKey:
				return s.(crypto.Signer), nil
			}
			return nil, fmt.Errorf("a %T key cannot sign a PDF", k)
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "ENCRYPTED PRIVATE KEY":
			return nil, errors.New("the private key is encrypted; decrypt it first")
		}
	}
}

// maxPasswordFile bounds a password file: pkcs12 refuses a password over 1024
// characters, and each takes at most four bytes.
const maxPasswordFile = 4096 + 2

// askPassword reads the password with no echo, from the terminal on standard
// input. A variable so that a test can answer without one.
var askPassword = func(prompt string, errOut io.Writer) ([]byte, error) {
	if !stdinIsTerminal() {
		return nil, errors.New("the .p12 password is asked for on a terminal and standard input is not one; pass -p12pass <file>, or -p12pass - to read it from standard input")
	}
	fmt.Fprint(errOut, prompt)
	// term.ReadPassword turns echo off and back on when it returns, but a
	// Ctrl-C (or a kill) ends the process in between and would leave the
	// person's shell typing blind: put the terminal back first.
	if restore, err := saveTerminal(int(os.Stdin.Fd())); err == nil {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
		defer guardPrompt(restore, sigs, osExit)()
		defer signal.Stop(sigs)
	}
	b, err := readSecret()
	fmt.Fprintln(errOut)
	return b, err
}

// The terminal itself, replaceable so that a test needs none.
var (
	stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	readSecret      = func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) }
	getState        = term.GetState
	restoreState    = term.Restore
)

// saveTerminal remembers the state of the terminal on fd and returns what puts
// it back.
func saveTerminal(fd int) (restore func(), err error) {
	st, err := getState(fd)
	if err != nil {
		return nil, err
	}
	return func() { _ = restoreState(fd, st) }, nil
}

// guardPrompt waits for a signal while the prompt is open: on one it restores
// the terminal, ends a line, and exits with the shell's code for it (128+n,
// 130 for Ctrl-C). The function it returns ends the wait, without a signal,
// and returns once the guard has stopped.
func guardPrompt(restore func(), sigs <-chan os.Signal, exit func(int)) (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case s := <-sigs:
			restore()
			fmt.Fprintln(os.Stderr)
			code := 130
			switch s {
			case syscall.SIGTERM:
				code = 143
			case syscall.SIGQUIT:
				code = 131
			}
			exit(code)
		case <-done:
		}
	}()
	return func() { close(done); <-stopped }
}

// p12Password reads the password from a file (one trailing line ending is not
// part of it), or asks for it. It never comes from an argument or from the
// environment, where other processes can read it. The caller zeroes the bytes
// when it is done.
func p12Password(c *context, file string) ([]byte, error) {
	if file == "" {
		return askPassword("password for the .p12: ", c.errOut)
	}
	var b []byte
	var err error
	if file == "-" {
		b, err = readAll(io.LimitReader(passStdin, maxPasswordFile+1))
		if err == nil && len(b) > maxPasswordFile {
			err = fmt.Errorf("the password on standard input is longer than the %d bytes this reads", maxPasswordFile)
		}
	} else {
		b, err = readBounded(file, maxPasswordFile)
	}
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r")), nil
}

// loadP12 fills in the signer, certificate and chain from a .p12 file, saying
// what is wrong in words that tell a person what to do.
func loadP12(c *context, o *ops.SignOptions, file, passFile string) error {
	data, err := readBounded(file, ops.MaxPKCS12Size)
	if err != nil {
		return fmt.Errorf("%w (a .p12 is a few kilobytes: is it the right file?)", err)
	}
	pass, err := p12Password(c, passFile)
	if err != nil {
		return err
	}
	// Best effort: the string made for the call cannot be erased, but the
	// buffer read from the file or the terminal can. The file's bytes are
	// left alone: a parsed certificate may point into them, and they are the
	// file as it sits on disk, still encrypted.
	defer clear(pass)
	err = o.LoadPKCS12(data, string(pass))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pkcs12.ErrPassword):
		return fmt.Errorf("%s: the password cannot be used: it must be valid UTF-8 and at most 1024 characters", file)
	case errors.Is(err, ops.ErrPKCS12Password):
		return fmt.Errorf("%s: wrong password (or the file was damaged: the format cannot tell the two apart)", file)
	case errors.Is(err, ops.ErrPKCS12Unsupported):
		return fmt.Errorf("%s: not supported: %v; re-export it with a current OpenSSL (AES, SHA-256 MAC) and an RSA, ECDSA or Ed25519 key", file, err)
	case errors.Is(err, ops.ErrPKCS12TooLarge):
		return fmt.Errorf("%s: refused as too large or too costly to open: %v", file, err)
	}
	return fmt.Errorf("%s: %w", file, err)
}

var hashNames = map[string]crypto.Hash{
	"": 0, "sha256": crypto.SHA256, "sha384": crypto.SHA384, "sha512": crypto.SHA512,
}

// runSign signs a document with a key and certificate held in PEM files, as an
// incremental update: every byte of the input is the start of the output.
func runSign(c *context, args []string) error {
	fs := flags("sign")
	keyFile := fs.String("key", "", "the private key, PEM: RSA, ECDSA or Ed25519")
	certFile := fs.String("cert", "", "the signer's certificate, PEM")
	p12File := fs.String("p12", "", "a .p12 / .pfx file holding the key, certificate and chain, instead of -key and -cert")
	p12Pass := fs.String("p12pass", "", "a FILE holding the .p12 password, or - for standard input; without it the password is asked for on the terminal, never taken from the command line or the environment")
	chainFile := fs.String("chain", "", "further certificates to embed, PEM")
	field := fs.String("field", "", "the signature field to fill, or the name of a new invisible one")
	page := fs.Int("page", 0, "the page a new field goes on; 0 means the first")
	reason := fs.String("reason", "", "why it is signed")
	location := fs.String("location", "", "where it is signed")
	name := fs.String("name", "", "who signs, as the signer claims")
	contact := fs.String("contact", "", "how to reach the signer")
	sub := fs.String("subfilter", "pkcs7", "pkcs7 (adbe.pkcs7.detached) or cades (ETSI.CAdES.detached)")
	hash := fs.String("hash", "", "sha256, sha384 or sha512; sha256 when empty")
	pss := fs.Bool("pss", false, "RSASSA-PSS for an RSA key")
	reserve := fs.Int("reserve", 0, "bytes of space for the CMS signature; 0 measures it")
	visible := fs.String("visible", "", "make the signature visible in the box x0,y0,x1,y1 (points, on -page)")
	imageFile := fs.String("image", "", "a picture to show in the visible signature")
	text := fs.String("text", "", "the visible signature's text; by default the signer's name and the date")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	switch {
	case *p12File != "" && (*keyFile != "" || *certFile != ""):
		return errors.New("sign takes -p12 or -key and -cert, not both")
	case *p12File == "" && *p12Pass != "":
		return errors.New("-p12pass goes with -p12")
	case *p12File == "" && (*keyFile == "" || *certFile == ""):
		return errors.New("sign needs -key and -cert, or -p12")
	case *p12Pass == "-" && (fs.Arg(0) == "-" || fs.Arg(0) == "/dev/stdin"):
		return errors.New("-p12pass - reads the password from standard input, which the PDF input cannot also be")
	}
	o := ops.SignOptions{
		Field: *field, Page: *page, Reason: *reason, Location: *location,
		Name: *name, ContactInfo: *contact, PSS: *pss, Reserve: *reserve,
	}
	switch *sub {
	case "pkcs7":
		o.SubFilter = ops.PKCS7Detached
	case "cades":
		o.SubFilter = ops.CAdESDetached
	default:
		return fmt.Errorf("%q is not a -subfilter; try pkcs7 or cades", *sub)
	}
	h, ok := hashNames[*hash]
	if !ok {
		return fmt.Errorf("%q is not a -hash; try sha256, sha384 or sha512", *hash)
	}
	o.Hash = h
	var err error
	if *visible != "" || *imageFile != "" || *text != "" {
		o.Appearance = &ops.SignAppearance{Text: *text}
		if *visible != "" {
			if o.Appearance.Rect, err = parseVisible(*visible); err != nil {
				return err
			}
		}
		if *imageFile != "" {
			if o.Appearance.Image, err = readBounded(*imageFile, maxSignImage); err != nil {
				return err
			}
		}
	}

	if *p12File != "" {
		if err := loadP12(c, &o, *p12File, *p12Pass); err != nil {
			return err
		}
	} else {
		keyPEM, err := readBounded(*keyFile, maxCredentialFile)
		if err != nil {
			return err
		}
		if o.Signer, err = parsePrivateKey(keyPEM); err != nil {
			return fmt.Errorf("%s: %w", *keyFile, err)
		}
		certPEM, err := readBounded(*certFile, maxCredentialFile)
		if err != nil {
			return err
		}
		certs, err := parseCertificates(certPEM)
		if err != nil {
			return fmt.Errorf("%s: %w", *certFile, err)
		}
		o.Certificate = certs[0]
		o.Chain = certs[1:]
	}
	if *chainFile != "" {
		chainPEM, err := readBounded(*chainFile, maxCredentialFile)
		if err != nil {
			return err
		}
		more, err := parseCertificates(chainPEM)
		if err != nil {
			return fmt.Errorf("%s: %w", *chainFile, err)
		}
		o.Chain = append(o.Chain, more...)
	}

	src, err := readBounded(fs.Arg(0), maxSignInput)
	if err != nil {
		return err
	}
	signed, err := ops.Sign(src, o)
	if errors.Is(err, ops.ErrAppearanceRect) && *visible == "" {
		return fmt.Errorf("%w (-image and -text need -visible x0,y0,x1,y1, unless -field names an existing signature field)", err)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(fs.Arg(1), signed, 0o644)
}
