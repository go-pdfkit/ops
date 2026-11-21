// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

// credentials writes a key and a self-signed certificate as PEM files.
func credentials(t *testing.T, dir, kind string) (keyPath, certPath string) {
	t.Helper()
	var signer crypto.Signer
	var err error
	switch kind {
	case "rsa":
		signer, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ecdsa":
		signer, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	default:
		_, signer, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cli " + kind},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		t.Fatal(err)
	}
	keyPath = filepath.Join(dir, kind+".key.pem")
	certPath = filepath.Join(dir, kind+".cert.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return keyPath, certPath
}

func TestSignVerb(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 2)
	orig, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"rsa", "ecdsa", "ed25519"} {
		key, cert := credentials(t, dir, kind)
		out := filepath.Join(dir, kind+".pdf")
		if code, _, msg := exec("sign", "-key", key, "-cert", cert, "-reason", "I agree",
			"-field", "approval", in, out); code != 0 {
			t.Fatalf("%s: code %d: %s", kind, code, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(b, orig) {
			t.Errorf("%s: the input is not the start of the output", kind)
		}
		d, err := reader.Open(b)
		if err != nil {
			t.Fatal(err)
		}
		rs, err := signature.VerifyAll(d)
		if err != nil || len(rs) != 1 || rs[0].Err != nil || !rs[0].Signature.CoversWholeFile {
			t.Errorf("%s: %v %+v", kind, err, rs)
		}
	}
}

func TestSignVerbRefusals(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	key, cert := credentials(t, dir, "ecdsa")
	out := filepath.Join(dir, "out.pdf")
	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"bad subfilter":   {"sign", "-key", key, "-cert", cert, "-subfilter", "x", in, out},
		"bad hash":        {"sign", "-key", key, "-cert", cert, "-hash", "md5", in, out},
		"one argument":    {"sign", "-key", key, "-cert", cert, in},
		"unknown sub-opt": {"sign", "-key", key, "-cert", cert, "-nope", in, out},
	} {
		if code, _, msg := exec(args...); code == 0 {
			t.Errorf("%s: signed", name)
		} else if strings.TrimSpace(msg) == "" {
			t.Errorf("%s: refused without a word", name)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused signature left a file behind")
	}
}

func TestPrivateKeyFormats(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecDER, _ := x509.MarshalECPrivateKey(ec)
	r, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, block := range map[string]*pem.Block{
		"sec1":  {Type: "EC PRIVATE KEY", Bytes: ecDER},
		"pkcs1": {Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(r)},
	} {
		if _, err := parsePrivateKey(pem.EncodeToMemory(block)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := parsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1}})); err == nil {
		t.Error("an encrypted key was read")
	}
}

// FuzzParseCredentials: whatever a PEM file holds, reading it returns or
// refuses, and never panics. The corpus is in testdata/fuzz.
func FuzzParseCredentials(f *testing.F) {
	f.Add([]byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"))
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"))
	f.Add([]byte("-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parsePrivateKey(data)
		_, _ = parseCertificates(data)
	})
}

func TestSignVerbCAdESWithAChainAndOddInputs(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	key, cert := credentials(t, dir, "ecdsa")
	_, chain := credentials(t, dir, "rsa")
	out := filepath.Join(dir, "cades.pdf")
	if code, _, msg := exec("sign", "-key", key, "-cert", cert, "-chain", chain,
		"-subfilter", "cades", "-hash", "sha384", in, out); code != 0 {
		t.Fatalf("code %d: %s", code, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if rs, err := signature.VerifyAll(d); err != nil || len(rs) != 1 || rs[0].Err != nil {
		t.Errorf("%v %+v", err, rs)
	}

	// A key that is not one a PDF can be signed with, and a certificate block
	// that does not parse.
	x, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(x)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err == nil {
		t.Error("an X25519 key was taken for a signing key")
	}
	if _, err := parsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}})); err == nil {
		t.Error("a bad PKCS #8 key was read")
	}
	if _, err := parseCertificates(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}})); err == nil {
		t.Error("a bad certificate was read")
	}
}

// TestSignVerbSaysWhyItRefused asserts WHICH refusal each case meets, since a
// refusal removed from the code is otherwise replaced by a later failure that
// also exits non-zero.
func TestSignVerbSaysWhyItRefused(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, 1)
	key, cert := credentials(t, dir, "ecdsa")
	out := filepath.Join(dir, "out.pdf")
	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(dir, "huge.pem")
	if err := os.WriteFile(huge, bytes.Repeat([]byte("x"), maxCredentialFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "absent")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no key", []string{"-cert", cert, in, out}, "needs -key and -cert"},
		{"no cert", []string{"-key", key, in, out}, "needs -key and -cert"},
		{"missing key", []string{"-key", absent, "-cert", cert, in, out}, "absent"},
		{"key is a directory", []string{"-key", dir, "-cert", cert, in, out}, "is a directory"},
		{"huge key", []string{"-key", huge, "-cert", cert, in, out}, "larger than"},
		{"garbage key", []string{"-key", garbage, "-cert", cert, in, out}, "garbage.pem: no private key"},
		{"missing cert", []string{"-key", key, "-cert", absent, in, out}, "no such file"},
		{"cert is a directory", []string{"-key", key, "-cert", dir, in, out}, "is a directory"},
		{"chain is a directory", []string{"-key", key, "-cert", cert, "-chain", dir, in, out}, "is a directory"},
		{"garbage cert", []string{"-key", key, "-cert", garbage, in, out}, "garbage.pem: no CERTIFICATE"},
		{"missing chain", []string{"-key", key, "-cert", cert, "-chain", absent, in, out}, "no such file"},
		{"garbage chain", []string{"-key", key, "-cert", cert, "-chain", garbage, in, out}, "garbage.pem: no CERTIFICATE"},
		{"missing input", []string{"-key", key, "-cert", cert, absent, out}, "absent"},
		{"input is a directory", []string{"-key", key, "-cert", cert, dir, out}, "is a directory"},
		{"not a pdf", []string{"-key", key, "-cert", cert, garbage, out}, "cannot be read as a PDF"},
		{"reserve of 1<<62", []string{"-key", key, "-cert", cert, "-reserve", "4611686018427387904", in, out}, "larger than the most one may reserve"},
		{"reserve of 1<<36", []string{"-key", key, "-cert", cert, "-reserve", "68719476736", in, out}, "larger than the most one may reserve"},
	} {
		var code int
		var msg string
		if crashes(func() { code, _, msg = exec(append([]string{"sign"}, c.args...)...) }) {
			t.Errorf("%s: the verb crashed instead of refusing", c.name)
			continue
		}
		if code == 0 || !strings.Contains(msg, c.want) {
			t.Errorf("%s: code %d, message %q, want it to say %q", c.name, code, msg, c.want)
		}
	}
}

func TestReadBoundedLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := readBounded(p, 5); err != nil || string(b) != "12345" {
		t.Errorf("a file of exactly the limit: %q %v", b, err)
	}
	if _, err := readBounded(p, 4); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("one byte over the limit: %v", err)
	}
}

// crashes reports whether f panics: a guard whose removal is only noticed by a
// crash is not held by a test.
func crashes(f func()) (c bool) {
	defer func() { c = recover() != nil }()
	f()
	return false
}

func TestPEMReadersRefuseWithoutCrashing(t *testing.T) {
	var certErr, keyErr error
	if crashes(func() { _, certErr = parseCertificates([]byte("not pem")) }) {
		t.Fatal("parseCertificates crashed on text that is not PEM")
	}
	if certErr == nil || !strings.Contains(certErr.Error(), "no CERTIFICATE") {
		t.Errorf("%v", certErr)
	}
	if crashes(func() { _, keyErr = parsePrivateKey([]byte("not pem")) }) {
		t.Fatal("parsePrivateKey crashed on text that is not PEM")
	}
	if keyErr == nil || !strings.Contains(keyErr.Error(), "no private key") {
		t.Errorf("%v", keyErr)
	}
	// Blocks of other types are skipped, and running out of them is a refusal.
	other := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1}})
	if crashes(func() { _, keyErr = parsePrivateKey(other) }) {
		t.Fatal("parsePrivateKey crashed on a file holding only a public key")
	}
	if keyErr == nil || !strings.Contains(keyErr.Error(), "no private key") {
		t.Errorf("a public key: %v", keyErr)
	}
}
