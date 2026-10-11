// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

// Repaired says whether any file this document's pages came from had to be
// rebuilt before it could be read: a cross-reference table that did not match
// the objects, a trailer that was not there, an offset that pointed into the
// middle of something else.
//
// [reader] does the rebuilding on its own, and quietly, which is the right
// behaviour for a reader — a caller asking for page three wants page three,
// not a lecture. But it leaves nobody able to SAY it happened, and that is
// worth saying for two different reasons: a file that needed rebuilding should
// be written back out and kept in its rebuilt form, and a file that did not
// need it should not be presented as rescued.
//
// ⛔ ANY, not all. A merged document's pages come from several files, and one
// damaged source is the answer to "did this need repairing": a true that
// depended on every source being broken would be false for every real merge.
func (d *Doc) Repaired() bool {
	for _, p := range d.pages {
		if p.src != nil && p.src.Repaired() {
			return true
		}
	}
	return false
}
