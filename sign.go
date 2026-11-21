package ops

import (
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-authn/cms"
	"github.com/go-pdfkit/forms"
	"github.com/go-pdfkit/reader"
)

// Signing a document (ISO 32000-1 §12.8) is the other verb that has to write
// into a file rather than rebuild it, and for a stronger reason than a form: a
// signature covers the bytes of the file as they were when it was made, so a
// document already signed stays verifiable only if every one of those bytes is
// still where it was. A signature is therefore appended as an incremental
// update, by the same writer a filled form is.
//
// What is written, in that update:
//
//   - the signature dictionary (Table 252), as an uncompressed indirect object
//     whose values are all direct, as §12.8.1 requires of a byte range digest;
//   - the signature field (§12.7.4.5) — either one the document already holds,
//     empty, which is given its /V; or a new, invisible one, merged with its
//     widget annotation and added to the page's /Annots and to /Fields;
//   - the interactive form dictionary, with /SigFlags 3: SignaturesExist and
//     AppendOnly (Table 219), since a file holding a signature must from now on
//     only be appended to;
//   - and the cross-reference section, in whichever form the file already uses.
//
// The /Contents hex string is reserved before anything is hashed (Table 252:
// "Space for the Contents value must be allocated before the message digest is
// computed"), the /ByteRange is written with fixed-width numbers so that
// filling it in moves nothing, the two ranges either side of the hex string —
// its delimiters included, §12.8.3.3.2 — are hashed and signed as a detached
// CMS SignedData by go-authn/cms, and the DER is written into the reserved
// space, padded with zeros.

// SubFilter values a signature can be written with (Table 252, §12.8.3.3).
const (
	// PKCS7Detached is adbe.pkcs7.detached: a detached CMS SignedData over the
	// byte ranges.
	PKCS7Detached = "adbe.pkcs7.detached"
	// CAdESDetached is ETSI.CAdES.detached. The CMS is the same detached
	// SignedData, carrying the signing-certificate-v2 attribute and no
	// signing-time attribute (the time is the dictionary's /M). This package
	// does not claim conformance to a PAdES baseline profile: that standard
	// asks more than the shape of the CMS, and it has not been checked here.
	CAdESDetached = "ETSI.CAdES.detached"
)

// SignOptions says who signs and what the signature dictionary says.
type SignOptions struct {
	// Signer holds the private key (RSA, ECDSA or Ed25519, or any
	// crypto.Signer whose public key is one of those), and Certificate is its
	// certificate. Both are required. Chain is further certificates to embed.
	Signer      crypto.Signer
	Certificate *x509.Certificate
	Chain       []*x509.Certificate
	// Hash is the digest; SHA-256 when zero. SHA-1 is refused. PSS selects
	// RSASSA-PSS for an RSA key.
	Hash crypto.Hash
	PSS  bool

	// Field is the signature field's name. An existing, empty signature field
	// of that name is signed; when there is none, a new invisible field is
	// made on Page (counting from one; the first page when zero). An empty
	// Field picks the first of Signature1, Signature2… not already taken.
	Field string
	Page  int

	// Appearance makes the signature visible: a box on the page with an image
	// and/or text. nil keeps today's invisible signature, byte for byte.
	Appearance *SignAppearance

	// SubFilter is PKCS7Detached (the default) or CAdESDetached.
	SubFilter string

	// Name, Reason, Location and ContactInfo are the signer's claims, written
	// into the dictionary when not empty (Table 252). Time is /M, the claimed
	// time of signing; now when zero.
	Name, Reason, Location, ContactInfo string
	Time                                time.Time

	// Reserve is how many bytes of DER the /Contents placeholder holds. When
	// zero it is measured by signing once beforehand, which calls Signer
	// twice: a signer that asks a person for each signature (a smart card)
	// should be given a Reserve instead.
	Reserve int
}

// The refusals a signature can meet, besides ErrEncrypted.
var (
	// ErrReserveTooSmall: the CMS signature came out larger than the space
	// reserved for it, so nothing was written.
	ErrReserveTooSmall = errors.New("ops: the signature does not fit in the space reserved for it")
	// ErrReserveTooLarge: more space was asked for than a signature may reserve.
	ErrReserveTooLarge = errors.New("ops: the space asked for the signature is larger than the most one may reserve")
	// ErrFieldSigned: the field named already holds a signature.
	ErrFieldSigned = errors.New("ops: the signature field is already signed")
	// ErrNotSignatureField: the field named exists and is not a signature
	// field.
	ErrNotSignatureField = errors.New("ops: the field is not a signature field")
	// ErrNoSigner: SignOptions has no Signer or no Certificate.
	ErrNoSigner = errors.New("ops: a signature needs a signer and its certificate")
	// ErrNotReadable: the input is not a PDF the reader can open.
	ErrNotReadable = errors.New("ops: the input cannot be read as a PDF")
)

// reserveSlack is added to a measured signature: an ECDSA signature is DER
// integers whose length varies by a byte each from one signature to the next,
// and the lengths around them can grow by a byte when they do.
const reserveSlack = 32

// rangeDigits is the width each /ByteRange number is written in, so that the
// numbers can be filled in after the file is laid out without moving a byte.
const rangeDigits = 10

// maxSignedFile is the largest file a /ByteRange of that width can describe.
// A variable so that a test can reach the refusal without a 10 GB file.
//
// An int64, so that this builds where an int is 32 bits wide: 9 999 999 999
// does not fit one.
var maxSignedFile int64 = 9_999_999_999

// maxReserve is the most space a signature may ask for: a megabyte of DER,
// which holds a CMS message carrying many certificates and a timestamp, and
// is twice that in hexadecimal. A larger Reserve is refused before anything is
// allocated for it.
const maxReserve = 1 << 20

// Sign appends a signature to a document, as an incremental update, and
// returns the whole signed file. src itself is not modified, and is the first
// len(src) bytes of what is returned — or of it after a single line ending,
// when src does not end with one.
func Sign(src []byte, o SignOptions) ([]byte, error) {
	if o.Signer == nil || o.Certificate == nil {
		return nil, ErrNoSigner
	}
	sub := o.SubFilter
	switch sub {
	case "":
		sub = PKCS7Detached
	case PKCS7Detached, CAdESDetached:
	default:
		return nil, fmt.Errorf("ops: /SubFilter %q is not one this writes", sub)
	}
	if strings.Contains(o.Field, ".") {
		return nil, fmt.Errorf("ops: the field name %q has a period in it, which separates the parts of a name", o.Field)
	}
	d, err := reader.Open(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotReadable, err)
	}
	u, err := openIncrement(src, d)
	if err != nil {
		return nil, err
	}
	// The catalogue is rewritten when the form dictionary is new or written
	// into it, so it has to be an object of its own.
	rootRef, ok := d.Trailer().Get("Root").(reader.Ref)
	if !ok {
		return nil, errors.New("ops: the trailer does not name the catalogue as an object of its own")
	}
	// A /Root that names something other than a catalogue is a file the reader
	// repairs, and those were refused above (measured: TestACatalogue…).
	catalog, _ := d.Catalog()

	s := &signing{increment: u, written: map[int]reader.Object{}}
	form, hasForm := forms.Read(d)
	name := o.Field
	if name == "" {
		name = freeName(form, hasForm)
	}
	var field *forms.Field
	if hasForm {
		field, _ = form.Field(name)
	}

	acroRef, acroIsRef := catalog["AcroForm"].(reader.Ref)
	var fieldRef, pageRef reader.Ref
	if field != nil {
		if field.Kind != forms.Signature {
			return nil, fmt.Errorf("%w: %q is a %s field", ErrNotSignatureField, name, field.Kind)
		}
		if v, has := field.Dict()["V"]; has && v != (reader.Null{}) {
			return nil, fmt.Errorf("%w: %q", ErrFieldSigned, name)
		}
		if fieldRef, ok = field.Ref(); !ok {
			return nil, fmt.Errorf("ops: %q is written inside another object and cannot be changed on its own", name)
		}
	} else {
		page := o.Page
		if page == 0 {
			page = 1
		}
		if pageRef, ok = d.PageRef(page); !ok {
			return nil, fmt.Errorf("ops: the document has no page %d to put the signature on", page)
		}
	}
	var vis *visible
	if o.Appearance != nil {
		if vis, err = checkAppearance(d, o, field); err != nil {
			return nil, err
		}
	}
	s.next = s.highest() + 1
	if err := s.room(); err != nil {
		return nil, err
	}
	sigRef := s.alloc()

	// The form dictionary: the one the document has, wherever it lives, or a
	// new one written into the catalogue.
	var acro reader.Dict
	if acroIsRef {
		// One that cannot be read is replaced by one that can: it held
		// nothing anything could have used.
		have, _ := d.Get(acroRef)
		from, _ := have.(reader.Dict)
		acro = dictOf(s.written, acroRef, from)
	} else {
		from, _ := catalog["AcroForm"].(reader.Dict)
		acro = copyDict(from)
		cat := dictOf(s.written, rootRef, catalog)
		cat["AcroForm"] = acro
	}
	acro["SigFlags"] = reader.Integer(3)

	// widget is the annotation the appearance is written into, when there is one.
	var widget reader.Dict
	if field != nil {
		dict := dictOf(s.written, fieldRef, field.Dict())
		dict["V"] = sigRef
		if vis != nil {
			// ⛔ The widget's own reference, which is the field's when the two
			// are merged and another object when they are not.
			wRef, ok := vis.widget.Ref()
			if !ok {
				return nil, fmt.Errorf("ops: the widget of %q is written inside another object and cannot be changed on its own", name)
			}
			widget = dictOf(s.written, wRef, vis.widget.Dict())
		}
	} else {
		widgetRef := s.alloc()
		widget = reader.Dict{
			"FT": reader.Name("Sig"), "T": textString(name), "V": sigRef,
			"Type": reader.Name("Annot"), "Subtype": reader.Name("Widget"),
			// An invisible signature: a widget of no size, with the Print (4)
			// and Locked (128) annotation flags.
			"Rect": reader.Array{reader.Integer(0), reader.Integer(0), reader.Integer(0), reader.Integer(0)},
			"F":    reader.Integer(132),
			"P":    pageRef,
		}
		s.written[widgetRef.Num] = widget
		// The reader has just found this page by its reference, so it reads.
		pageObj, _ := d.Get(pageRef)
		pageDict, _ := pageObj.(reader.Dict)
		if err := s.addTo(dictOf(s.written, pageRef, pageDict), "Annots", widgetRef); err != nil {
			return nil, err
		}
		if err := s.addTo(acro, "Fields", widgetRef); err != nil {
			return nil, err
		}
	}

	opts := cms.SignOptions{
		Signer: o.Signer, Certificate: o.Certificate, Chain: o.Chain,
		Hash: o.Hash, PSS: o.PSS, Detached: true, SigningCertificateV2: true,
	}
	reserve := o.Reserve
	if reserve <= 0 {
		probe, err := cms.Sign(nil, opts)
		if err != nil {
			return nil, err
		}
		reserve = len(probe) + reserveSlack
	}
	// Checked against the bound before it is multiplied by anything: twice a
	// reserve near the top of an int is negative.
	if reserve > maxReserve {
		return nil, fmt.Errorf("%w: %d bytes asked for, at most %d", ErrReserveTooLarge, reserve, maxReserve)
	}
	when := o.Time
	if when.IsZero() {
		when = now()
	}
	if vis != nil {
		// After every number the invisible signature takes, so that one without
		// an Appearance is numbered as it always was.
		s.draw(widget, vis, o, when)
	}
	dict, brAt, contentsAt := signatureDict(sub, reserve, o, when)

	if err := s.room(); err != nil {
		return nil, err
	}
	out, offsets := s.append(s.written, map[int][]byte{sigRef.Num: dict})
	if int64(len(out)) > maxSignedFile {
		return nil, fmt.Errorf("ops: a file of %d bytes is larger than a /ByteRange of %d digits can describe", len(out), rangeDigits)
	}
	body := offsets[sigRef.Num] + len(objHeader(sigRef.Num))
	gapStart := body + contentsAt
	gapEnd := gapStart + 1 + 2*reserve + 1
	copy(out[body+brAt:], fmt.Sprintf("%0*d %0*d %0*d", rangeDigits, gapStart,
		rangeDigits, gapEnd, rangeDigits, len(out)-gapEnd))

	signed := make([]byte, 0, len(out)-(gapEnd-gapStart))
	signed = append(append(signed, out[:gapStart]...), out[gapEnd:]...)
	der, err := cms.Sign(signed, opts)
	if err != nil {
		return nil, err
	}
	if len(der) > reserve {
		return nil, fmt.Errorf("%w: %d bytes of signature, %d reserved", ErrReserveTooSmall, len(der), reserve)
	}
	hex.Encode(out[gapStart+1:], der)
	return out, nil
}

// signing is an increment with the objects it is about to write.
type signing struct {
	increment
	written map[int]reader.Object
}

// alloc hands out the next object number.
func (s *signing) alloc() reader.Ref {
	ref := reader.Ref{Num: s.next}
	s.next++
	return ref
}

// addTo appends a reference to the array a dictionary holds under key: in
// place when the array is written in the dictionary, as a rewritten object when
// the dictionary only points at it, and as a new array when there is none.
func (s *signing) addTo(dict reader.Dict, key reader.Name, ref reader.Ref) error {
	switch v := dict[key].(type) {
	case reader.Ref:
		arr, err := s.arrayAt(v)
		if err != nil {
			return err
		}
		s.written[v.Num] = append(arr, ref)
	case reader.Array:
		dict[key] = append(append(reader.Array(nil), v...), ref)
	default:
		dict[key] = reader.Array{ref}
	}
	return nil
}

// arrayAt is the array an object holds, as already changed by this update when
// it has been.
func (s *signing) arrayAt(ref reader.Ref) (reader.Array, error) {
	if have, ok := s.written[ref.Num].(reader.Array); ok {
		return have, nil
	}
	o, _ := s.doc.Get(ref)
	arr, ok := o.(reader.Array)
	if !ok {
		return nil, fmt.Errorf("ops: object %d should be an array and is not", ref.Num)
	}
	return append(reader.Array(nil), arr...), nil
}

// freeName is the first SignatureN the form does not already use.
func freeName(form *forms.Form, hasForm bool) string {
	// Each taken name is a distinct field, so with n fields one of the first
	// n+1 names is free; the search ends there whatever the form answers.
	n := 0
	if hasForm {
		n = len(form.Fields())
	}
	i := 1
	for ; i <= n; i++ {
		if _, taken := form.Field(fmt.Sprintf("Signature%d", i)); !taken {
			break
		}
	}
	return fmt.Sprintf("Signature%d", i)
}

// signatureDict writes the signature dictionary with its two placeholders, and
// says where in it the /ByteRange's three unknown numbers begin and where the
// /Contents hex string's "<" is.
func signatureDict(sub string, reserve int, o SignOptions, when time.Time) (b []byte, brAt, contentsAt int) {
	b = append(b, "<</Type/Sig/Filter/Adobe.PPKLite/SubFilter/"...)
	b = append(b, sub...)
	b = append(b, "/ByteRange[0 "...)
	brAt = len(b)
	b = append(b, fmt.Sprintf("%0*d %0*d %0*d", rangeDigits, 0, rangeDigits, 0, rangeDigits, 0)...)
	b = append(b, "]/Contents"...)
	contentsAt = len(b)
	b = append(b, '<')
	b = append(b, strings.Repeat("0", 2*reserve)...)
	b = append(b, '>')
	b = append(b, "/M"...)
	b = reader.AppendObject(b, reader.String(pdfDate(when)))
	for _, e := range []struct {
		key reader.Name
		val string
	}{{"Name", o.Name}, {"Reason", o.Reason}, {"Location", o.Location}, {"ContactInfo", o.ContactInfo}} {
		if e.val != "" {
			b = append(b, '/')
			b = append(b, e.key...)
			b = reader.AppendObject(b, textString(e.val))
		}
	}
	b = append(b, ">>"...)
	return b, brAt, contentsAt
}
