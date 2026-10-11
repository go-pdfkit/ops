// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"bytes"
	"regexp"
	"testing"
)

// brokenXref points a file's startxref at the wrong place, which is the
// commonest way a PDF arrives damaged: the objects are all there and the table
// that says where they are is not.
func brokenXref(t *testing.T, b []byte) []byte {
	t.Helper()
	re := regexp.MustCompile(`startxref\s*\n\s*\d+`)
	out := re.ReplaceAll(b, []byte("startxref\n999999"))
	if bytes.Equal(out, b) {
		t.Fatal("the fixture has no startxref to break, so this proves nothing")
	}
	return out
}

// ⛔ A POSITIVE control first. Without it, "Repaired() is false" would pass on
// a fixture that was never broken, and the whole test would be measuring
// nothing — which is what the brokenXref helper's own fatal guards against on
// the other side.
func TestRepairedIsFalseForAFileThatIsNotDamaged(t *testing.T) {
	d, err := Open(simple(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if d.Repaired() {
		t.Error("a file that opens normally was reported as rebuilt")
	}
}

func TestRepairedIsTrueForAFileWhoseTableIsWrong(t *testing.T) {
	d, err := Open(brokenXref(t, simple(t, 2)))
	if err != nil {
		t.Fatalf("a file with a wrong startxref could not be opened at all: %v", err)
	}
	if !d.Repaired() {
		t.Error("a file whose cross-reference table was wrong was not reported as rebuilt")
	}
	// And it is really readable: a repair that loses the pages is not a repair.
	if got := d.PageCount(); got != 2 {
		t.Errorf("PageCount() = %d, want 2", got)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// ⛔ What comes out must NOT itself need repairing. That is the whole point
	// of writing it back: a rebuilt file kept in its rebuilt form.
	back, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Repaired() {
		t.Error("what was written had to be rebuilt again to be read")
	}
}

// ⛔ ANY source, not all. A merged document's pages come from several files,
// and one damaged source is the answer to "did this need repairing" — a true
// that depended on every source being broken would be false for every real
// merge.
func TestOneDamagedSourceIsEnough(t *testing.T) {
	good, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	bad, err := Open(brokenXref(t, simple(t, 1)))
	if err != nil {
		t.Fatal(err)
	}
	good.Append(bad)
	if !good.Repaired() {
		t.Error("a merge of a clean file and a damaged one was not reported as rebuilt")
	}
	// The other order too, because "any" is not "the first".
	good2, err := Open(simple(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	bad2, err := Open(brokenXref(t, simple(t, 1)))
	if err != nil {
		t.Fatal(err)
	}
	bad2.Append(good2)
	if !bad2.Repaired() {
		t.Error("the damaged file first was not reported as rebuilt")
	}
}

// A document built here rather than borrowed has no source to have been
// repaired, and must not claim one.
func TestADocumentBuiltFromNothingWasNotRepaired(t *testing.T) {
	d := New()
	d.Blank(595, 842)
	if d.Repaired() {
		t.Error("a document with no source file was reported as rebuilt")
	}
}
