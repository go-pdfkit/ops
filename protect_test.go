package ops

import (
	"errors"
	"testing"

	"github.com/go-pdfkit/reader"
)

// bulky is a document whose pages carry enough repetition to be worth
// compressing, which a handful of bytes would not be.
func bulky(t *testing.T, n int) []byte {
	t.Helper()
	return buildPDF(t, n, func(i int, d reader.Dict) {
		d["Keywords"] = reader.String("the same long string on every single page, over and over")
	})
}

// opened reads a file the test has built, or fails.
func opened(t *testing.T, b []byte) *Doc {
	t.Helper()
	d, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCompressingAFile(t *testing.T) {
	// Packing the objects into compressed streams has to leave the pages
	// exactly as they were and the file smaller than it was.
	src := bulky(t, 40)
	plain, err := opened(t, src).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	d := opened(t, src)
	d.Compress()
	packed, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) >= len(plain) {
		t.Errorf("packed is %d bytes and plain is %d", len(packed), len(plain))
	}
	if got, want := contentsOf(t, packed), contentsOf(t, plain); !equal(got, want) {
		t.Error("packing changed what is on the pages")
	}
	// A packed file needs a version that says so.
	back, err := reader.Open(packed)
	if err != nil {
		t.Fatal(err)
	}
	if back.Version() < "1.5" {
		t.Errorf("a packed file says it is version %s", back.Version())
	}
}

func TestEncryptingAFile(t *testing.T) {
	// Both passwords open it and nothing else does; what the pages say is
	// unchanged; and the permissions written are the ones asked for.
	for _, packed := range []bool{false, true} {
		d := opened(t, simple(t, 3))
		if packed {
			d.Compress()
		}
		d.Encrypt(reader.Encryption{
			UserPassword: "letmein", OwnerPassword: "iownit",
			Permissions: reader.PermPrint | reader.PermExtract,
		})
		out, err := d.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Open(out); !errors.Is(err, reader.ErrWrongPassword) {
			t.Errorf("packed=%v: an encrypted file opened with no password: %v", packed, err)
		}
		if _, err := reader.OpenWithPassword(out, "guess"); !errors.Is(err, reader.ErrWrongPassword) {
			t.Errorf("packed=%v: a wrong password opened it", packed)
		}
		for _, pw := range []string{"letmein", "iownit"} {
			back, err := reader.OpenWithPassword(out, pw)
			if err != nil {
				t.Fatalf("packed=%v: %q did not open it: %v", packed, pw, err)
			}
			if back.PageCount() != 3 {
				t.Errorf("packed=%v: %q sees %d pages", packed, pw, back.PageCount())
			}
			p, ok := back.Protection()
			if !ok {
				t.Fatalf("packed=%v: it says it is not protected", packed)
			}
			if p.Permissions != reader.PermPrint|reader.PermExtract {
				t.Errorf("packed=%v: it allows %v", packed, p.Permissions)
			}
			if want := pw == "iownit"; p.Owner != want {
				t.Errorf("packed=%v: %q opened as owner = %v", packed, pw, p.Owner)
			}
		}
	}
}

func TestDecryptingAFile(t *testing.T) {
	// A file read with its password and written out again is not protected,
	// and Decrypt takes back an Encrypt that has not happened yet.
	d := opened(t, simple(t, 2))
	d.Encrypt(reader.Encryption{UserPassword: "shh"})
	locked, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := OpenWithPassword(locked, "shh")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := back.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Open(plain); err != nil {
		t.Errorf("a file written from a decrypted document still needs a password: %v", err)
	}
	if got := contentsOf(t, plain); !equal(got, pages(1, 2)) {
		t.Errorf("the pages came out as %v", got)
	}

	d2 := opened(t, simple(t, 1))
	d2.Encrypt(reader.Encryption{UserPassword: "shh"})
	d2.Decrypt()
	out, err := d2.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Open(out); err != nil {
		t.Errorf("Decrypt did not take back Encrypt: %v", err)
	}
}

func TestWhatADocumentSaysItWasProtectedWith(t *testing.T) {
	// It reports the file it was read from, not what it will be written as.
	d := opened(t, simple(t, 1))
	d.Encrypt(reader.Encryption{UserPassword: "shh", Permissions: reader.PermPrint})
	locked, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := d.Protection(); ok {
		t.Errorf("a document read from an unprotected file reported %+v", p)
	}

	back, err := OpenWithPassword(locked, "shh")
	if err != nil {
		t.Fatal(err)
	}
	p, ok := back.Protection()
	if !ok {
		t.Fatal("a document read from a protected file says it was not")
	}
	if p.Method != "AES-256" || p.Permissions != reader.PermPrint {
		t.Errorf("it reports %+v", p)
	}

	// A document with no page borrowed from anywhere has nothing to report.
	blank := &Doc{version: "1.7"}
	if err := blank.InsertBlank(1); err != nil {
		t.Fatal(err)
	}
	if p, ok := blank.Protection(); ok {
		t.Errorf("a document made from nothing reported %+v", p)
	}
}
