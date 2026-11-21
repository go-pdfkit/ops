package ops

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/go-pdfkit/reader"
)

// signOrFail calls Sign and turns a panic into a failed assertion: a guard
// whose removal is only noticed because the code after it crashes is not held
// by any test.
func signOrFail(t *testing.T, src []byte, o SignOptions) (out []byte, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Sign panicked instead of refusing: %v", r)
			out, err = nil, errors.New("panic")
		}
	}()
	return Sign(src, o)
}

func TestARefusalIsASentinelNotAPanic(t *testing.T) {
	good := opts(t, "ecdsa")

	// A nil signer, and a nil certificate, each on its own.
	for name, o := range map[string]SignOptions{
		"no signer":      {Certificate: key(t, "rsa").cert},
		"no certificate": {Signer: key(t, "rsa").signer},
	} {
		if out, err := signOrFail(t, simple(t, 1), o); !errors.Is(err, ErrNoSigner) || out != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Something that is not a PDF.
	if out, err := signOrFail(t, []byte("not a pdf"), good); !errors.Is(err, ErrNotReadable) || out != nil {
		t.Errorf("not a pdf: %v", err)
	}

	// A file that does not say where its table is.
	broken := bytes.Replace(simple(t, 1), []byte("startxref"), []byte("startxrEf"), 1)
	if out, err := signOrFail(t, broken, good); !errors.Is(err, ErrNotAppendable) || out != nil {
		t.Errorf("no startxref: %v", err)
	}

	// A signature too large for the space reserved: nothing is returned.
	small := good
	small.Reserve = 10
	if out, err := signOrFail(t, simple(t, 1), small); !errors.Is(err, ErrReserveTooSmall) || out != nil {
		t.Errorf("small reserve: %v", err)
	}

	// A document with no form at all is signed, and under the first free name.
	b, err := signOrFail(t, simple(t, 1), good)
	if err != nil {
		t.Fatal(err)
	}
	if got := ours(t, b, 1)[0].Signature.Field; got != "Signature1" {
		t.Errorf("field %q", got)
	}
}

// lastSection is the bytes the final startxref of a file points at.
func lastSection(t *testing.T, b []byte) []byte {
	t.Helper()
	i := bytes.LastIndex(b, []byte("startxref"))
	if i < 0 {
		t.Fatal("no startxref")
	}
	rest := bytes.TrimSpace(b[i+len("startxref"):])
	end := bytes.IndexAny(rest, "\r\n")
	if end < 0 {
		t.Fatal("no offset after startxref")
	}
	at, err := strconv.Atoi(string(rest[:end]))
	if err != nil || at < 0 || at >= len(b) {
		t.Fatalf("bad startxref offset %q", rest[:end])
	}
	return b[at:]
}

// TestTheUpdateKeepsTheKindOfCrossReferenceTheFileHas: a plain table appended
// to a file whose table is a stream is refused by the strict readers, and a
// stream appended to a plain one is as wrong.
func TestTheUpdateKeepsTheKindOfCrossReferenceTheFileHas(t *testing.T) {
	for _, packed := range []bool{false, true} {
		src := formFile(t, packed, func(*reader.Writer, reader.Ref) (reader.Dict, reader.Array) { return nil, nil })
		b, err := signOrFail(t, src, opts(t, "ecdsa"))
		if err != nil {
			t.Fatal(err)
		}
		sec := lastSection(t, b)
		isTable := bytes.HasPrefix(sec, []byte("xref"))
		if isTable == packed {
			t.Errorf("input packed=%v: update starts %q", packed, sec[:min(20, len(sec))])
		}
		if packed && !bytes.Contains(sec[:min(200, len(sec))], []byte("/XRef")) {
			t.Errorf("the update's section is not a cross-reference stream: %q", sec[:min(40, len(sec))])
		}
	}
}

// FuzzSign: whatever bytes come in, Sign returns a signed file or an error and
// never panics; a file it returns keeps the input as its prefix. The corpus is
// in testdata/fuzz/FuzzSign.
func FuzzSign(f *testing.F) {
	k := key(f, "ecdsa")
	o := SignOptions{Signer: k.signer, Certificate: k.cert, Reserve: 512}
	f.Add([]byte("%PDF-1.4\n1 0 obj\n<</Type/Catalog>>\nendobj\ntrailer\n<</Root 1 0 R>>\nstartxref\n0\n%%EOF\n"))
	f.Add([]byte("startxref\n99999999999\n%%EOF"))
	// A small valid PDF with a signature field, so that the fuzzer starts from
	// a file the reader accepts and the writer is what it reaches.
	f.Add(sigFieldFile(f, false))
	f.Add(sigFieldFile(f, true))
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 1<<16 {
			t.Skip()
		}
		out, err := Sign(src, o)
		if err == nil && !bytes.HasPrefix(out, src) {
			t.Fatal("the input is not the start of the signed file")
		}
	})
}

// TestAFileWithNoFormIsGivenTheFirstName: without a form there is nothing to
// look names up in, and the first one is free.
func TestAFileWithNoFormIsGivenTheFirstName(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("looked a name up in a form that is not there: %v", r)
		}
	}()
	if got := freeName(nil, false); got != "Signature1" {
		t.Errorf("the name is %q", got)
	}
}

// TestAReserveBeyondTheBoundIsRefused: 1<<62 made twice the reserve negative
// and strings.Repeat panicked, and 1<<36 asked for 128 GiB. Both are refused,
// by name, before anything is allocated; the bound itself is allowed.
func TestAReserveBeyondTheBoundIsRefused(t *testing.T) {
	// The largest int there is, rather than 1<<62, so the test also builds where
	// an int is 32 bits: twice it overflows there just as 1<<62 does here.
	for _, reserve := range []int{maxReserve + 1, maxReserve * 1024, math.MaxInt} {
		o := opts(t, "ecdsa")
		o.Reserve = reserve
		out, err := signOrFail(t, simple(t, 1), o)
		if !errors.Is(err, ErrReserveTooLarge) || out != nil {
			t.Errorf("reserve %d: %v", reserve, err)
		}
	}
	o := opts(t, "ecdsa")
	o.Reserve = maxReserve
	if _, err := signOrFail(t, simple(t, 1), o); err != nil {
		t.Errorf("the largest reserve allowed was refused: %v", err)
	}
}

// TestStartxrefAndSectionGuards pins the two readers of the file's tail on both
// sides of each guard, asserting rather than relying on a crash.
func TestStartxrefAndSectionGuards(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked: %v", r)
		}
	}()
	// No marker: what follows byte 8 must not be mistaken for an offset.
	if v, ok := lastStartxref([]byte("01234567 5\nxxxxxxxxxxxxxxx")); ok {
		t.Errorf("no startxref, yet offset %d", v)
	}
	if _, ok := lastStartxref([]byte("startxref\nabc\n%%EOF aaaaaaaaaaaa")); ok {
		t.Error("an offset that is not digits was accepted")
	}
	if v, ok := lastStartxref([]byte("startxref\n 7\n%%EOF aaaaaaaaaaaa")); !ok || v != 7 {
		t.Errorf("%d %v", v, ok)
	}
	for _, at := range []int{-1, 5, 99} {
		if xrefIsStream([]byte("xref"), at) {
			t.Errorf("offset %d outside the file is a stream", at)
		}
	}
}
