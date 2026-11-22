package ops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoadPKCS12: whatever the bytes and the password, LoadPKCS12 returns a
// signer, or an error that answers to one of the four sentinels and leaves the
// options untouched. The seeds are the four fixtures; testdata/fuzz/
// FuzzLoadPKCS12 holds the hostile ones (a corpus entry replays on every
// `go test`).
func FuzzLoadPKCS12(f *testing.F) {
	for _, name := range []string{"rsa-chain.p12", "ec.p12", "nomac.p12", "costly.p12"} {
		b, err := os.ReadFile(filepath.Join("testdata", "p12", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, p12Password)
		f.Add(b, "")
	}
	f.Fuzz(func(t *testing.T, data []byte, password string) {
		var o SignOptions
		err := o.LoadPKCS12(data, password)
		if err == nil {
			if o.Signer == nil || o.Certificate == nil {
				t.Fatalf("no error and no signer: %+v", o)
			}
			return
		}
		if !errors.Is(err, ErrPKCS12Password) && !errors.Is(err, ErrPKCS12Unsupported) &&
			!errors.Is(err, ErrPKCS12TooLarge) && !errors.Is(err, ErrPKCS12Malformed) {
			t.Fatalf("an error outside the four: %v", err)
		}
		if o.Signer != nil || o.Certificate != nil || o.Chain != nil {
			t.Fatalf("a refused load changed the options: %+v", o)
		}
	})
}
