package ops

import (
	"crypto"
	"errors"
	"fmt"

	"github.com/go-authn/pkcs12"
)

// Signing with a .p12 / .pfx file (PKCS #12, RFC 7292) is what a browser tool
// and a Windows or macOS certificate export hand a person: one password-
// protected file holding the key, the certificate and its chain. go-authn/pkcs12
// decodes it, bounded for untrusted input; this file only maps what it returns
// onto SignOptions and what it refuses onto errors a person can act on.

// MaxPKCS12Size is the largest .p12 / .pfx file LoadPKCS12 reads. A real one is
// a few kilobytes; the bound is go-authn/pkcs12's own default for an input.
const MaxPKCS12Size = pkcs12.DefaultMaxInputSize

// The refusals LoadPKCS12 can meet. Each wraps the go-authn/pkcs12 error it
// stands for, so errors.Is answers for either.
var (
	// ErrPKCS12Password: the password is wrong, or the file was changed after
	// it was made (the format cannot tell the two apart, by design), or the
	// password cannot be used at all (not valid UTF-8, over 1024 characters;
	// errors.Is(err, pkcs12.ErrPassword) tells that case).
	ErrPKCS12Password = errors.New("ops: the .p12 password is wrong, or the file is corrupted")
	// ErrPKCS12Unsupported: a well-formed file that uses an algorithm or a key
	// this cannot read, or a key that cannot sign a PDF (X25519).
	ErrPKCS12Unsupported = errors.New("ops: the .p12 uses an algorithm or key type that is not supported")
	// ErrPKCS12TooLarge: the file, or something it asks for (iterations,
	// certificates, key size), is over a bound.
	ErrPKCS12TooLarge = errors.New("ops: the .p12 is larger or costlier than the bounds allow")
	// ErrPKCS12Malformed: the input is not a readable .p12, holds no private
	// key or no matching certificate, or holds more than one key.
	ErrPKCS12Malformed = errors.New("ops: the file is not a usable .p12")
)

// LoadPKCS12 reads a .p12 / .pfx file and sets o.Signer, o.Certificate and
// o.Chain from it; every other field of o is left alone. The password is the
// file's, as text; the empty password is tried before the absent one, as
// go-authn/pkcs12 does. LoadPKCS12 keeps neither data nor password after it
// returns, and o is untouched when it fails. A Go string cannot be erased,
// though: the password string the caller passed, and any copy made while
// decoding, stay in memory until the garbage collector reuses it. A caller
// that cares should hold the password in a []byte, convert it for this one
// call and zero the buffer afterwards (pdfops does). Do not zero data: the
// certificates it returns may point into it.
//
// The certificate is not judged. Its validity dates, key usage and extended
// key usage are not checked, and neither is its chain to a root: this loads a
// key, a certificate and the certificates beside it, and the caller (before
// signing) and the verifier (afterwards) decide whether they are good enough.
func (o *SignOptions) LoadPKCS12(data []byte, password string) error {
	b, err := pkcs12.Decode(data, password, nil)
	switch {
	case err == nil:
	case errors.Is(err, pkcs12.ErrIncorrectPassword):
		return fmt.Errorf("%w: %w", ErrPKCS12Password, err)
	case errors.Is(err, pkcs12.ErrPassword):
		return fmt.Errorf("%w: the password cannot be used (it must be valid UTF-8 and at most 1024 characters): %w", ErrPKCS12Password, err)
	case errors.Is(err, pkcs12.ErrLimit):
		return fmt.Errorf("%w: %w", ErrPKCS12TooLarge, err)
	case errors.Is(err, pkcs12.ErrUnsupported), errors.Is(err, pkcs12.ErrNoMAC):
		return fmt.Errorf("%w: %w", ErrPKCS12Unsupported, err)
	default:
		return fmt.Errorf("%w: %w", ErrPKCS12Malformed, err)
	}
	return o.useBundle(b)
}

// useBundle sets the options from what the file held, once the key is known
// to be one that signs.
func (o *SignOptions) useBundle(b *pkcs12.Bundle) error {
	s, err := signerOf(b.PrivateKey)
	if err != nil {
		return err
	}
	o.Signer, o.Certificate, o.Chain = s, b.Certificate, b.CACerts
	return nil
}

// signerOf accepts the keys a PDF signature can use. go-authn/pkcs12 also
// returns an X25519 key, which can agree on a secret but not sign.
func signerOf(k crypto.PrivateKey) (crypto.Signer, error) {
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("%w: a %T key cannot sign a PDF", ErrPKCS12Unsupported, k)
	}
	return s, nil
}
