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
	"os"

	"github.com/go-pdfkit/ops"
)

// The sizes this verb will read. A key, a certificate or a chain is a few
// kilobytes; a document is the only large input, and /ByteRange cannot describe
// more than ten digits of it anyway.
const (
	maxCredentialFile = 1 << 20
	maxSignInput      = 1 << 30
)

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

var hashNames = map[string]crypto.Hash{
	"": 0, "sha256": crypto.SHA256, "sha384": crypto.SHA384, "sha512": crypto.SHA512,
}

// runSign signs a document with a key and certificate held in PEM files, as an
// incremental update: every byte of the input is the start of the output.
func runSign(c *context, args []string) error {
	fs := flags("sign")
	keyFile := fs.String("key", "", "the private key, PEM: RSA, ECDSA or Ed25519")
	certFile := fs.String("cert", "", "the signer's certificate, PEM")
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 2, "<in.pdf> <out.pdf>"); err != nil {
		return err
	}
	if *keyFile == "" || *certFile == "" {
		return errors.New("sign needs -key and -cert")
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
	if err != nil {
		return err
	}
	return os.WriteFile(fs.Arg(1), signed, 0o644)
}
