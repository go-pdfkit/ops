package ops

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
)

// goldenSigned signs the fixture with everything fixed: an Ed25519 key from a
// seed, a certificate with a fixed serial and dates, CAdES (which carries no
// signing time of its own), a fixed /M and a fixed reserve. Every byte of the
// result is therefore the same on every run.
func goldenSigned(t *testing.T, mutate func(*SignOptions)) []byte {
	t.Helper()
	signer := ed25519.NewKeyFromSeed([]byte("go-pdfkit/ops golden seed 32 byt"))
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "golden"},
		NotBefore:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:     time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, signer.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	o := SignOptions{Signer: signer, Certificate: cert, SubFilter: CAdESDetached,
		Reserve: 4096, Time: time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)}
	if mutate != nil {
		mutate(&o)
	}
	out, err := Sign(formFile(t, false, nilForm), o)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func nilForm(*reader.Writer, reader.Ref) (reader.Dict, reader.Array) { return nil, nil }

func goldenHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// goldenInvisible is the SHA-256 of goldenSigned with no Appearance, measured
// on ops v0.39.0 (origin/main at b1c3548, before Appearance existed), by
// running this same function there. An invisible signature must stay what it
// was, byte for byte.
const goldenInvisible = "db1b97b26e7f079626b688cf785ce41e114499737fc216f318c4ff5ee5b59692"

func TestAnInvisibleSignatureIsByteForByteWhatItWas(t *testing.T) {
	if got := goldenHash(goldenSigned(t, nil)); got != goldenInvisible {
		t.Errorf("an invisible signature changed: %s, want %s", got, goldenInvisible)
	}
	visible := goldenHash(goldenSigned(t, func(o *SignOptions) {
		o.Appearance = &SignAppearance{Rect: [4]float64{10, 10, 110, 50}}
	}))
	if visible == goldenInvisible {
		t.Error("a visible signature is the same bytes as an invisible one")
	}
}
