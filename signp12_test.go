package ops

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-authn/pkcs12"
)

// The fixtures in testdata/p12 were written by OpenSSL 4.1 (`openssl pkcs12
// -export`, PBES2/AES-256 and an HMAC-SHA-256 MAC) with the password in
// password.txt; rsa-chain.p12 also holds the CA that issued its certificate.
const p12Password = "fixture-only password"

func p12File(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "p12", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSignWithAP12 is the round trip: a .p12 is loaded, a document is signed
// with it, and reader/signature verifies the signature against the certificate
// the file held.
func TestSignWithAP12(t *testing.T) {
	for _, tc := range []struct {
		file  string
		cn    string
		chain int
	}{
		{"rsa-chain.p12", "ops p12 test signer rsa", 1},
		{"ec.p12", "ops p12 test signer ec", 0},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var o SignOptions
			if err := o.LoadPKCS12(p12File(t, tc.file), p12Password); err != nil {
				t.Fatal(err)
			}
			if o.Certificate.Subject.CommonName != tc.cn || len(o.Chain) != tc.chain {
				t.Fatalf("certificate %q with %d chain certificates", o.Certificate.Subject.CommonName, len(o.Chain))
			}
			signed, err := Sign(simple(t, 1), o)
			if err != nil {
				t.Fatal(err)
			}
			rs := ours(t, signed, 1)
			if !rs[0].Signature.CoversWholeFile {
				t.Error("the signature does not cover the whole file")
			}
		})
	}
}

// TestLoadPKCS12LeavesOtherFieldsAlone: only the key, certificate and chain
// are set.
func TestLoadPKCS12LeavesOtherFieldsAlone(t *testing.T) {
	o := SignOptions{Reason: "I agree", Page: 3, PSS: true}
	if err := o.LoadPKCS12(p12File(t, "ec.p12"), p12Password); err != nil {
		t.Fatal(err)
	}
	if o.Reason != "I agree" || o.Page != 3 || !o.PSS {
		t.Errorf("loading changed %+v", o)
	}
}

func TestLoadPKCS12Refusals(t *testing.T) {
	big := make([]byte, MaxPKCS12Size+1)
	for _, tc := range []struct {
		name     string
		data     []byte
		password string
		want     error
		also     error // the go-authn/pkcs12 error it must still answer to
	}{
		{"wrong password", p12File(t, "rsa-chain.p12"), "not the password", ErrPKCS12Password, pkcs12.ErrIncorrectPassword},
		{"empty password", p12File(t, "ec.p12"), "", ErrPKCS12Password, pkcs12.ErrIncorrectPassword},
		// A password pkcs12 cannot use at all (not UTF-8, over 1024 characters) is
		// still a password problem, not a damaged file.
		{"password not UTF-8", p12File(t, "ec.p12"), "\xff\xfe\xfd", ErrPKCS12Password, pkcs12.ErrPassword},
		{"password too long", p12File(t, "ec.p12"), strings.Repeat("a", 1025), ErrPKCS12Password, pkcs12.ErrPassword},
		{"oversized", big, p12Password, ErrPKCS12TooLarge, pkcs12.ErrLimit},
		// 2 000 000 key-derivation iterations, over the 1 000 000 bound: refused
		// before any hashing.
		{"too many iterations", p12File(t, "costly.p12"), p12Password, ErrPKCS12TooLarge, pkcs12.ErrLimit},
		{"not a p12", []byte("-----BEGIN CERTIFICATE-----"), p12Password, ErrPKCS12Malformed, pkcs12.ErrMalformed},
		{"empty", nil, p12Password, ErrPKCS12Malformed, pkcs12.ErrMalformed},
		{"no integrity MAC", p12File(t, "nomac.p12"), p12Password, ErrPKCS12Unsupported, pkcs12.ErrNoMAC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := SignOptions{Reason: "kept"}
			err := o.LoadPKCS12(tc.data, tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
			if tc.also != nil && !errors.Is(err, tc.also) {
				t.Errorf("error %v does not wrap %v", err, tc.also)
			}
			if o.Signer != nil || o.Certificate != nil || o.Chain != nil || o.Reason != "kept" {
				t.Errorf("a refused load changed the options: %+v", o)
			}
			for _, other := range []error{ErrPKCS12Password, ErrPKCS12TooLarge, ErrPKCS12Malformed, ErrPKCS12Unsupported} {
				if other != tc.want && errors.Is(err, other) {
					t.Errorf("answers to %v as well as %v", other, tc.want)
				}
			}
			if strings.Contains(err.Error(), tc.password) && tc.password != "" {
				t.Errorf("the message holds the password: %v", err)
			}
		})
	}
}

// TestAnX25519KeyCannotSign: pkcs12 can return one; a signature cannot use it.
func TestAnX25519KeyCannotSign(t *testing.T) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signerOf(k); !errors.Is(err, ErrPKCS12Unsupported) {
		t.Errorf("error %v", err)
	}
	o := SignOptions{Reason: "kept"}
	if err := o.useBundle(&pkcs12.Bundle{PrivateKey: k}); !errors.Is(err, ErrPKCS12Unsupported) || o.Signer != nil || o.Reason != "kept" {
		t.Errorf("a bundle holding an X25519 key: %v %+v", err, o)
	}
	e, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if s, err := signerOf(e); err != nil || s == nil {
		t.Errorf("an ECDSA key was refused: %v", err)
	}
}

// TestMaxPKCS12SizeIsTheLibrarys: the bound a caller checks before reading a
// file is the one the decoder enforces, and a file exactly at it is read.
func TestMaxPKCS12SizeIsTheLibrarys(t *testing.T) {
	if MaxPKCS12Size != pkcs12.DefaultMaxInputSize {
		t.Fatalf("MaxPKCS12Size = %d", MaxPKCS12Size)
	}
	var o SignOptions
	err := o.LoadPKCS12(make([]byte, MaxPKCS12Size), p12Password)
	if errors.Is(err, ErrPKCS12TooLarge) {
		t.Errorf("a file of exactly MaxPKCS12Size is over the bound: %v", err)
	}
}
