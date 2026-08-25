package ops

import "github.com/go-pdfkit/reader"

// Compress asks for the file to be written with its objects packed into
// compressed streams and a cross-reference stream, which is what every writer
// since PDF 1.5 does and what makes a file a good deal smaller. It costs
// nothing but a version of 1.5, which every reader in use has understood for
// twenty years.
func (d *Doc) Compress() { d.packed = true }

// Encrypt protects the file that will be written. Two people can open it:
// whoever knows the user password, subject to the permissions, and whoever
// knows the owner password, subject to nothing.
//
// An encrypted file is not byte-for-byte reproducible — encryption needs
// randomness, by design — so a document written twice with the same call comes
// out different both times, and neither can be compared with the other.
func (d *Doc) Encrypt(e reader.Encryption) { d.protect = &e }

// Decrypt writes the file without protection. A document opened with the right
// password is already decrypted, so this only undoes an earlier call to
// [Doc.Encrypt]; a file read with [OpenWithPassword] and written out is
// unprotected either way.
func (d *Doc) Decrypt() { d.protect = nil }

// Protection reports how the file this document was read from was protected,
// and false when it was not protected at all — or when the document was not
// read from a file. It says nothing about how the document will be written:
// that is what was passed to [Doc.Encrypt].
func (d *Doc) Protection() (reader.Protection, bool) {
	for _, p := range d.pages {
		if p.src != nil {
			return p.src.Protection()
		}
	}
	return reader.Protection{}, false
}
