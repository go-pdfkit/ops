package ops

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-pdfkit/reader"
	"github.com/go-pdfkit/reader/signature"
)

// ---- keys and certificates ----

type testKey struct {
	name   string
	signer crypto.Signer
	cert   *x509.Certificate
}

var keyCache = map[string]testKey{}

// key makes a self-signed certificate for one kind of key. Trust is not what
// is tested — pdfsig runs with -nocert and reader/signature never judges it —
// so a self-signed one is all a signature needs.
func key(t testing.TB, kind string) testKey {
	t.Helper()
	if k, ok := keyCache[kind]; ok {
		return k
	}
	var signer crypto.Signer
	var err error
	switch kind {
	case "rsa":
		signer, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ecdsa":
		signer, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ed25519":
		_, signer, err = ed25519.GenerateKey(rand.Reader)
	}
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "go-pdfkit/ops test signer " + kind},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
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
	k := testKey{kind, signer, cert}
	keyCache[kind] = k
	return k
}

func opts(t testing.TB, kind string) SignOptions {
	k := key(t, kind)
	return SignOptions{Signer: k.signer, Certificate: k.cert,
		Reason: "I agree", Location: "Paris", Name: "Test Signer",
		Time: time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)}
}

// ---- inputs ----

// sigFieldFile has an empty signature field already, the way a form prepared
// for signing does, and a text field beside it.
func sigFieldFile(t *testing.T, packed bool) []byte {
	t.Helper()
	return formFile(t, packed, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		sig := w.Add(reader.Dict{"FT": reader.Name("Sig"), "T": reader.String("approval"),
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(20), reader.Integer(20), reader.Integer(120), reader.Integer(60)}})
		text := w.Add(reader.Dict{"FT": reader.Name("Tx"), "T": reader.String("name"),
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(20), reader.Integer(100), reader.Integer(120), reader.Integer(130)}})
		return reader.Dict{"Fields": reader.Array{sig, text}}, reader.Array{sig, text}
	})
}

type input struct {
	name  string
	b     []byte
	field string // the field the first signature goes in
}

func inputs(t *testing.T) []input {
	return []input{
		{"classic-xref", simple(t, 2), ""},
		{"xref-stream", formFile(t, true, func(*reader.Writer, reader.Ref) (reader.Dict, reader.Array) { return nil, nil }), ""},
		{"empty-sig-field", sigFieldFile(t, false), "approval"},
		{"empty-sig-field-xref-stream", sigFieldFile(t, true), "approval"},
	}
}

// ---- judges ----

type pdfsigVerdict struct{ valid, whole bool }

// tool finds an external judge, failing when the environment variable says
// this lane installed it. A skip is not a pass, and says so.
func tool(t *testing.T, name, require string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv(require) == "1" {
			t.Fatalf("%s=1 and %s was not found", require, name)
		}
		t.Skipf("SKIPPED, NOT PASSED: %s is not installed, so the oracle comparison did not run", name)
	}
	return p
}

func pdfsigVerdicts(t *testing.T, pdfsig, path string) ([]pdfsigVerdict, string) {
	t.Helper()
	out, _ := exec.Command(pdfsig, "-nocert", path).CombinedOutput()
	var vs []pdfsigVerdict
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Signature #"):
			vs = append(vs, pdfsigVerdict{})
		case line == "- Total document signed" && len(vs) > 0:
			vs[len(vs)-1].whole = true
		case line == "- Signature Validation: Signature is Valid." && len(vs) > 0:
			vs[len(vs)-1].valid = true
		}
	}
	return vs, string(out)
}

// ours verifies with reader/signature and checks what every signature this
// writes must be: valid, its dictionary inside the signed bytes, and the last
// one covering the whole file.
func ours(t *testing.T, b []byte, want int) []signature.Result {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if d.Repaired() {
		t.Fatal("the signed file had to be repaired to be read")
	}
	rs, err := signature.VerifyAll(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != want {
		t.Fatalf("%d signatures, want %d", len(rs), want)
	}
	for i, r := range rs {
		if r.Err != nil {
			t.Errorf("signature #%d: %v", i+1, r.Err)
		}
		if !r.Signature.DictionarySigned {
			t.Errorf("signature #%d: its dictionary is not inside the signed bytes", i+1)
		}
		if whole := i == len(rs)-1; r.Signature.CoversWholeFile != whole {
			t.Errorf("signature #%d: covers the whole file %v, want %v", i+1, r.Signature.CoversWholeFile, whole)
		}
	}
	return rs
}

// TestSignedFilesAgreeWithTheOracles is the oracle: every key kind, every
// shape of file, signed once and then a second time. poppler's pdfsig must
// call each signature valid and the last one "Total document signed",
// reader/signature must agree, and qpdf --check must find the file clean.
//
// PDFSIG_REQUIRE=1, REQUIRE_QPDF=1 and REQUIRE_OPENSSL=1 make a missing judge
// a failure (CI sets all three).
func TestSignedFilesAgreeWithTheOracles(t *testing.T) {
	pdfsig := tool(t, "pdfsig", "PDFSIG_REQUIRE")
	qpdf := tool(t, "qpdf", "REQUIRE_QPDF")
	ossl := tool(t, "openssl", "REQUIRE_OPENSSL")
	dir := t.TempDir()
	var table strings.Builder
	for _, kind := range []string{"rsa", "ecdsa", "ed25519"} {
		for _, in := range inputs(t) {
			for _, sub := range []string{PKCS7Detached, CAdESDetached} {
				name := fmt.Sprintf("%s-%s-%s", kind, in.name, strings.ReplaceAll(sub, ".", "_"))
				o := opts(t, kind)
				o.SubFilter = sub
				o.Field = in.field
				once, err := Sign(in.b, o)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if !bytes.HasPrefix(once, in.b) {
					t.Fatalf("%s: the original is not the beginning of the signed file", name)
				}
				o.Field = ""
				twice, err := Sign(once, o)
				if err != nil {
					t.Fatalf("%s, second signature: %v", name, err)
				}
				if !bytes.HasPrefix(twice, once) {
					t.Fatalf("%s: the second signature rewrote the first's bytes", name)
				}
				ours(t, once, 1)
				ours(t, twice, 2)

				p := filepath.Join(dir, name+".pdf")
				if err := os.WriteFile(p, twice, 0o644); err != nil {
					t.Fatal(err)
				}
				vs, raw := pdfsigVerdicts(t, pdfsig, p)
				shape := len(vs) == 2 && !vs[0].whole && vs[1].whole
				valid := shape && vs[0].valid && vs[1].valid
				// ⛔ poppler 26.10 (NSS) calls an Ed25519 CMS signature
				// "Invalid" while reader/signature and openssl cms -verify
				// both accept it: recorded, not asserted, so the day poppler
				// accepts one shows in the table rather than as a failure.
				if !shape || (kind != "ed25519" && !valid) {
					t.Errorf("%s: pdfsig says %+v\n%s", name, vs, raw)
					continue
				}
				qOK := qpdfAgrees(t, qpdf, dir, name, in.b, twice)
				osslOK := opensslAgrees(t, ossl, dir, name, twice)
				fmt.Fprintf(&table, "%-58s pdfsig valid=%-5v whole=%v,%v | ours valid | openssl=%v | qpdf=%v\n",
					name, valid, vs[0].whole, vs[1].whole, osslOK, qOK)
			}
		}
	}
	t.Logf("\n%s", table.String())
}

// qpdfAgrees runs qpdf --check on the file before and after signing: the
// fixtures carry pages qpdf warns about, so what is asserted is that signing
// added no warning and did not change its verdict.
func qpdfAgrees(t *testing.T, qpdf, dir, name string, before, after []byte) bool {
	t.Helper()
	check := func(sub string, b []byte) (string, error) {
		d := filepath.Join(dir, sub)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(d, "f.pdf")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(qpdf, "--check", p).CombinedOutput()
		return strings.ReplaceAll(string(out), d, ""), err
	}
	was, wasErr := check("before-"+name, before)
	now, nowErr := check("after-"+name, after)
	if fmt.Sprint(wasErr) != fmt.Sprint(nowErr) || strings.Count(now, "WARNING") != strings.Count(was, "WARNING") ||
		strings.Contains(now, "ERROR") {
		t.Errorf("%s: qpdf --check before signing: %v\n%s\nafter: %v\n%s", name, wasErr, was, nowErr, now)
		return false
	}
	return true
}

// opensslAgrees hands each signature's DER and the bytes its /ByteRange covers
// to openssl cms -verify: a third judge, and the one that settles Ed25519.
func opensslAgrees(t *testing.T, ossl, dir, name string, b []byte) bool {
	t.Helper()
	d, err := reader.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	sigs, err := d.Signatures()
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range sigs {
		signed, err := d.SignedBytes(s)
		if err != nil {
			t.Fatal(err)
		}
		der := filepath.Join(dir, fmt.Sprintf("%s-%d.der", name, i))
		data := filepath.Join(dir, fmt.Sprintf("%s-%d.bin", name, i))
		if os.WriteFile(der, s.Contents, 0o644) != nil || os.WriteFile(data, signed, 0o644) != nil {
			t.Fatal("cannot write the files openssl reads")
		}
		out, err := exec.Command(ossl, "cms", "-verify", "-binary", "-inform", "DER", "-in", der,
			"-content", data, "-noverify", "-out", os.DevNull).CombinedOutput()
		if err != nil {
			t.Errorf("%s #%d: openssl cms -verify: %v\n%s", name, i+1, err, out)
			return false
		}
	}
	return true
}

// ---- the signature itself ----

func TestTheSignatureSaysWhatItWasGiven(t *testing.T) {
	o := opts(t, "ecdsa")
	o.ContactInfo = "+33 1 23 45 67 89"
	o.Time = time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", -(3*3600+30*60)))
	b, err := Sign(simple(t, 1), o)
	if err != nil {
		t.Fatal(err)
	}
	s := ours(t, b, 1)[0].Signature
	if s.Field != "Signature1" || s.SubFilter != PKCS7Detached || s.Filter != "Adobe.PPKLite" ||
		s.Name != "Test Signer" || s.Reason != "I agree" || s.Location != "Paris" ||
		s.M != "D:20260102030405-03'30" {
		t.Errorf("%+v", s)
	}
	if !bytes.Contains(b, []byte("/ContactInfo(+33 1 23 45 67 89)")) {
		t.Error("/ContactInfo was not written")
	}
	d, _ := reader.Open(b)
	cat, _ := d.Catalog()
	acro, _ := d.Resolve(cat["AcroForm"])
	if f, _ := reader.ToInt(acro.(reader.Dict)["SigFlags"]); f != 3 {
		t.Errorf("/SigFlags %d", f)
	}
	page, _ := d.Page(1)
	if annots, _ := d.Resolve(page["Annots"]); len(annots.(reader.Array)) != 1 {
		t.Errorf("/Annots %v", annots)
	}
}

func TestTheDefaultTimeIsNow(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return time.Date(2030, 5, 6, 7, 8, 9, 0, time.UTC) }
	o := opts(t, "ed25519")
	o.Time = time.Time{}
	b, err := Sign(simple(t, 1), o)
	if err != nil {
		t.Fatal(err)
	}
	if m := ours(t, b, 1)[0].Signature.M; m != "D:20300506070809+00'00" {
		t.Errorf("/M %s", m)
	}
}

// TestTamperingAnySignedByteBreaksIt is the property: change any one byte
// inside the signed ranges and the signature no longer holds.
func TestTamperingAnySignedByteBreaksIt(t *testing.T) {
	b, err := Sign(simple(t, 1), opts(t, "ecdsa"))
	if err != nil {
		t.Fatal(err)
	}
	s := ours(t, b, 1)[0].Signature
	br := s.ByteRange
	// Every byte of a small file is cheap to try; positions are sampled
	// across both ranges, each end of each range always included.
	var at []int64
	for i := int64(0); i < br[1]; i += 7 {
		at = append(at, i)
	}
	for i := br[2]; i < br[2]+br[3]; i += 7 {
		at = append(at, i)
	}
	at = append(at, br[1]-1, br[2], br[2]+br[3]-1)
	for _, i := range at {
		bad := append([]byte(nil), b...)
		bad[i] ^= 0x01
		d, err := reader.Open(bad)
		if err != nil {
			continue // a file that no longer reads has not been forged
		}
		rs, err := signature.VerifyAll(d)
		if err != nil {
			continue
		}
		for _, r := range rs {
			if r.Err == nil && r.Signature.CoversWholeFile {
				t.Fatalf("flipping byte %d left a valid whole-file signature", i)
			}
		}
	}
}

// ---- refusals ----

func TestAnEncryptedDocumentIsNotSigned(t *testing.T) {
	w := reader.NewWriter("1.7")
	w.Encrypt(reader.Encryption{OwnerPassword: "secret"})
	pagesRef := w.Reserve()
	pageRef := w.Add(reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef,
		"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(200), reader.Integer(200)}})
	w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
	locked, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(locked, opts(t, "ecdsa")); !errors.Is(err, ErrEncrypted) {
		t.Errorf("%v", err)
	}
}

func TestASignatureLargerThanItsReserveIsRefused(t *testing.T) {
	o := opts(t, "rsa")
	o.Reserve = 100
	if _, err := Sign(simple(t, 1), o); !errors.Is(err, ErrReserveTooSmall) {
		t.Errorf("%v", err)
	}
	// One byte is enough to refuse, and the exact size is enough to fit: an
	// RSA signature is the same length every time.
	probe, err := Sign(simple(t, 1), opts(t, "rsa"))
	if err != nil {
		t.Fatal(err)
	}
	n := len(ours(t, probe, 1)[0].Signature.Contents)
	o.Reserve = n - 1
	if _, err := Sign(simple(t, 1), o); !errors.Is(err, ErrReserveTooSmall) {
		t.Errorf("reserve %d of %d: %v", o.Reserve, n, err)
	}
	o.Reserve = n
	b, err := Sign(simple(t, 1), o)
	if err != nil {
		t.Fatalf("reserve %d of %d: %v", o.Reserve, n, err)
	}
	ours(t, b, 1)
}

func TestAFieldIsNotSignedTwice(t *testing.T) {
	o := opts(t, "ecdsa")
	o.Field = "approval"
	once, err := Sign(sigFieldFile(t, false), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(once, o); !errors.Is(err, ErrFieldSigned) {
		t.Errorf("%v", err)
	}
	// A field whose /V is null holds nothing, and is signed.
	nulled := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		sig := w.Add(reader.Dict{"FT": reader.Name("Sig"), "T": reader.String("approval"), "V": reader.Null{},
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(0), reader.Integer(0)}})
		return reader.Dict{"Fields": reader.Array{sig}}, reader.Array{sig}
	})
	b, err := Sign(nulled, o)
	if err != nil {
		t.Fatal(err)
	}
	ours(t, b, 1)
}

func TestOnlyASignatureFieldIsSigned(t *testing.T) {
	o := opts(t, "ecdsa")
	o.Field = "name"
	if _, err := Sign(sigFieldFile(t, false), o); !errors.Is(err, ErrNotSignatureField) {
		t.Errorf("%v", err)
	}
}

func TestTheDefaultNameIsOneNotTaken(t *testing.T) {
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		f := w.Add(reader.Dict{"FT": reader.Name("Tx"), "T": reader.String("Signature1"),
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"), "P": page,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)}})
		return reader.Dict{"Fields": reader.Array{f}}, reader.Array{f}
	})
	b, err := Sign(src, opts(t, "ecdsa"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ours(t, b, 1)[0].Signature.Field; got != "Signature2" {
		t.Errorf("field %q", got)
	}
}

func TestOptionsThatCannotBeSigned(t *testing.T) {
	src := simple(t, 1)
	for name, o := range map[string]SignOptions{
		"no signer":      {Certificate: key(t, "rsa").cert},
		"no certificate": {Signer: key(t, "rsa").signer},
	} {
		if _, err := Sign(src, o); err == nil {
			t.Errorf("%s: signed", name)
		}
	}
	o := opts(t, "rsa")
	o.SubFilter = "adbe.pkcs7.sha1"
	if _, err := Sign(src, o); err == nil {
		t.Error("an unsupported /SubFilter was written")
	}
	o = opts(t, "rsa")
	o.Field = "a.b"
	if _, err := Sign(src, o); err == nil {
		t.Error("a field name with a period was written")
	}
	o = opts(t, "rsa")
	o.Page = 9
	if _, err := Sign(src, o); err == nil {
		t.Error("a signature was put on a page that does not exist")
	}
	o = opts(t, "rsa")
	o.Hash = crypto.SHA1
	if _, err := Sign(src, o); err == nil {
		t.Error("a SHA-1 signature was written")
	}
	if _, err := Sign([]byte("not a pdf"), opts(t, "rsa")); err == nil {
		t.Error("something that is not a PDF was signed")
	}
	broken := bytes.Replace(src, []byte("startxref"), []byte("startxrEf"), 1)
	if _, err := Sign(broken, opts(t, "rsa")); err == nil {
		t.Error("a file with no usable table was signed")
	}
}

// failingSigner signs the first n times and fails after: the probe and the
// signature itself are separate calls, and either can fail.
type failingSigner struct {
	crypto.Signer
	n int
}

func (f *failingSigner) Sign(r io.Reader, digest []byte, o crypto.SignerOpts) ([]byte, error) {
	if f.n == 0 {
		return nil, errors.New("the card was removed")
	}
	f.n--
	return f.Signer.Sign(r, digest, o)
}

func TestASignerThatFails(t *testing.T) {
	for _, n := range []int{0, 1} {
		o := opts(t, "ecdsa")
		o.Signer = &failingSigner{Signer: o.Signer, n: n}
		if _, err := Sign(simple(t, 1), o); err == nil {
			t.Errorf("signed with a signer that failed after %d signatures", n)
		}
	}
}

func TestAFileTooLargeForItsByteRange(t *testing.T) {
	defer func(n int) { maxSignedFile = n }(maxSignedFile)
	maxSignedFile = 100
	if _, err := Sign(simple(t, 1), opts(t, "ecdsa")); err == nil {
		t.Error("a file larger than its /ByteRange can say was signed")
	}
}

// ---- the shapes a document can hold its form in ----

// TestFormsHeldEveryWay signs documents whose form dictionary, field list and
// annotation list are each written inside their parent or as objects of their
// own, since an update has to change each where it is.
func TestFormsHeldEveryWay(t *testing.T) {
	build := func(acroRef, fieldsRef, annotsRef bool) []byte {
		w := reader.NewWriter("1.7")
		pagesRef := w.Reserve()
		pageRef := w.Reserve()
		text := w.Add(reader.Dict{"FT": reader.Name("Tx"), "T": reader.String("name"),
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"), "P": pageRef,
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(10), reader.Integer(10)}})
		var annots reader.Object = reader.Array{text}
		if annotsRef {
			annots = w.Add(reader.Array{text})
		}
		w.Put(pageRef, reader.Dict{"Type": reader.Name("Page"), "Parent": pagesRef, "Annots": annots,
			"MediaBox": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(200), reader.Integer(200)}})
		w.Put(pagesRef, reader.Dict{"Type": reader.Name("Pages"), "Kids": reader.Array{pageRef}, "Count": reader.Integer(1)})
		var fields reader.Object = reader.Array{text}
		if fieldsRef {
			fields = w.Add(reader.Array{text})
		}
		var acro reader.Object = reader.Dict{"Fields": fields}
		if acroRef {
			acro = w.Add(acro)
		}
		out, err := w.Finish(reader.Dict{"Root": w.Add(reader.Dict{"Type": reader.Name("Catalog"), "Pages": pagesRef, "AcroForm": acro})})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, acroRef := range []bool{false, true} {
		for _, fieldsRef := range []bool{false, true} {
			for _, annotsRef := range []bool{false, true} {
				b, err := Sign(build(acroRef, fieldsRef, annotsRef), opts(t, "ecdsa"))
				if err != nil {
					t.Fatalf("acro %v fields %v annots %v: %v", acroRef, fieldsRef, annotsRef, err)
				}
				ours(t, b, 1)
				d, _ := reader.Open(b)
				page, _ := d.Page(1)
				annots, _ := d.Resolve(page["Annots"])
				if n := len(annots.(reader.Array)); n != 2 {
					t.Errorf("acro %v fields %v annots %v: %d annotations", acroRef, fieldsRef, annotsRef, n)
				}
			}
		}
	}
}

func TestAnArrayThatIsNotAnArray(t *testing.T) {
	src := buildPDF(t, 1, func(_ int, d reader.Dict) { d["Annots"] = reader.Ref{Num: 1} })
	if _, err := Sign(src, opts(t, "ecdsa")); err == nil {
		t.Error("a page whose /Annots names something that is not an array was signed")
	}
	src = formFile(t, false, func(w *reader.Writer, _ reader.Ref) (reader.Dict, reader.Array) {
		return reader.Dict{"Fields": w.Add(reader.Integer(5))}, nil
	})
	if _, err := Sign(src, opts(t, "ecdsa")); err == nil {
		t.Error("a form whose /Fields names something that is not an array was signed")
	}
	// The same array named twice is changed once, with both additions.
	s := &signing{written: map[int]reader.Object{7: reader.Array{reader.Integer(1)}}}
	got, err := s.arrayAt(reader.Ref{Num: 7})
	if err != nil || len(got) != 1 {
		t.Errorf("%v %v", got, err)
	}
}

func TestACatalogueThatIsNotAnObjectOfItsOwn(t *testing.T) {
	src := simple(t, 1)
	d, _ := reader.Open(src)
	root := d.Trailer()["Root"].(reader.Ref)
	// Point the trailer at an object that is not a dictionary: the first
	// page's content stream.
	page, _ := d.Page(1)
	content := page["Contents"].(reader.Ref)
	bad := bytes.Replace(src, []byte(fmt.Sprintf("/Root %d 0 R", root.Num)), []byte(fmt.Sprintf("/Root %d 0 R", content.Num)), 1)
	if bytes.Equal(bad, src) {
		t.Fatal("the trailer was not found to change")
	}
	// The reader repairs such a file, and a repaired file is not added to.
	if _, err := Sign(bad, opts(t, "ecdsa")); err == nil || !strings.Contains(err.Error(), "repaired") {
		t.Errorf("a document whose /Root is a stream: %v", err)
	}
	// A catalogue written into the trailer itself reads, and cannot be
	// rewritten by an update.
	pages := page["Parent"].(reader.Ref)
	direct := bytes.Replace(src, []byte(fmt.Sprintf("/Root %d 0 R", root.Num)), []byte(fmt.Sprintf("/Root <</Type/Catalog/Pages %d 0 R>>", pages.Num)), 1)
	if _, err := Sign(direct, opts(t, "ecdsa")); err == nil || !strings.Contains(err.Error(), "catalogue") {
		t.Errorf("a document whose catalogue is written into its trailer: %v", err)
	}
}

func TestASignatureFieldInsideAnotherObject(t *testing.T) {
	src := formFile(t, false, func(w *reader.Writer, page reader.Ref) (reader.Dict, reader.Array) {
		sig := reader.Dict{"FT": reader.Name("Sig"), "T": reader.String("approval")}
		return reader.Dict{"Fields": reader.Array{sig}}, nil
	})
	o := opts(t, "ecdsa")
	o.Field = "approval"
	if _, err := Sign(src, o); err == nil {
		t.Error("a field written inside the form dictionary was changed on its own")
	}
}
