// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
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
	"strconv"
	"strings"

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

// readBounded reads a file, refusing one longer than max rather than reading it
// whole first.
func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
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
var askPassword = func(prompt string, errOut io.Writer) (string, error) {
	if !stdinIsTerminal() {
		return "", errors.New("the .p12 password is asked for on a terminal and standard input is not one; put it in a file and pass -p12pass <file>")
	}
	fmt.Fprint(errOut, prompt)
	b, err := readSecret()
	fmt.Fprintln(errOut)
	return string(b), err
}

// The terminal itself, replaceable so that a test needs none.
var (
	stdinIsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	readSecret      = func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) }
)

// p12Password reads the password from a file (one trailing line ending is not
// part of it), or asks for it. It never comes from an argument or from the
// environment, where other processes can read it.
func p12Password(c *context, file string) (string, error) {
	if file == "" {
		return askPassword("password for the .p12: ", c.errOut)
	}
	b, err := readBounded(file, maxPasswordFile)
	if err != nil {
		return "", err
	}
	s := strings.TrimSuffix(string(b), "\n")
	return strings.TrimSuffix(s, "\r"), nil
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
	err = o.LoadPKCS12(data, pass)
	switch {
	case err == nil:
		return nil
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
	p12Pass := fs.String("p12pass", "", "a FILE holding the .p12 password; without it the password is asked for on the terminal, never taken from the command line or the environment")
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
