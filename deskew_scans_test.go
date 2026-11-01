// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package ops

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// A second population: real library scans, which the synthetic fixture in
// deskew_test.go cannot stand in for.
//
// ⛔ THERE IS NO GROUND TRUTH HERE, so this does not check the angle. Nobody
// knows how far a 1924 newspaper was lying off square on the scanner bed, and
// a test that asserted a number would be asserting my own detector's output.
// What it checks instead is a property that needs no ground truth and that a
// detector fitting noise cannot satisfy: SELF-CONSISTENCY. If the measurement
// says a page is tilted by 3° and the correction is sound, measuring the
// corrected page must find nothing left. A detector latching onto a stray
// speck, a page border or a column rule would correct by its own wrong angle
// and the second measurement would not settle.
//
// The corpus is the one go-pdfkit/conformance's pdfscans baselines are taken
// from. It is large and lives outside any repository, so this skips when it is
// not there rather than failing — and says so, because a skipped test that
// looks like a pass is the thing this whole file exists to avoid.

const scansCorpusDefault = "/Users/Shared/pdfscans"

func scansCorpus(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("PDFSCANS_CORPUS")
	if dir == "" {
		dir = scansCorpusDefault
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("the scanned-PDF corpus is not at %s (set PDFSCANS_CORPUS): %v", dir, err)
	}
	return dir
}

// TestDeskewIsSelfConsistentOnRealScans measures each scan, straightens it,
// and measures again. The residual is the whole test.
func TestDeskewIsSelfConsistentOnRealScans(t *testing.T) {
	root := scansCorpus(t)
	// One collection, and the first few files of it: the corpus runs to
	// gigabytes and rendering a page is the expensive part. ia-texts is the
	// printed-text collection, which is what a deskew is for.
	paths, err := filepath.Glob(filepath.Join(root, "ia-texts", "*.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skipf("no PDFs under %s/ia-texts", root)
	}
	sort.Strings(paths)
	if len(paths) > 8 {
		paths = paths[:8]
	}

	var angles, residuals []float64
	for _, path := range paths {
		pdf, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Open(pdf)
		if err != nil {
			t.Logf("%s: cannot be opened (%v) — not this test's business", filepath.Base(path), err)
			continue
		}
		found, err := d.Deskew(DeskewOptions{})
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		out, err := d.Bytes()
		if err != nil {
			t.Errorf("%s: writing back: %v", filepath.Base(path), err)
			continue
		}
		after, err := SkewAngles(out, DeskewOptions{})
		if err != nil {
			t.Errorf("%s: re-measuring: %v", filepath.Base(path), err)
			continue
		}
		if len(after) != len(found) {
			t.Errorf("%s: %d pages before, %d after", filepath.Base(path), len(found), len(after))
			continue
		}
		for i := range found {
			if found[i].Err != nil || after[i].Err != nil {
				continue // a blank or unrenderable page says nothing here
			}
			angles = append(angles, found[i].Angle)
			if found[i].Corrected {
				residuals = append(residuals, after[i].Angle)
				// ⛔ THE TEST IS IDEMPOTENCE, which needs no tolerance of my
				// choosing. Deskewing a page that has just been deskewed must
				// do nothing: whatever is left has to be below the threshold
				// that was applied, or the operation has not reached a fixed
				// point and running it twice is not the same as running it
				// once. My first version compared against 0.35°, a number I
				// picked, and failed a page whose residual was 0.45° — inside
				// the 0.5° default, so the second pass would have left it
				// alone. That was the tolerance being wrong, not the page.
				if math.Abs(after[i].Angle) >= defaultDeskewThreshold {
					t.Errorf("%s page %d: measured %+.2f°, corrected, and %+.2f° remains — "+
						"above the %.1f° threshold, so deskewing again would move it again",
						filepath.Base(path), found[i].Page, found[i].Angle, after[i].Angle,
						defaultDeskewThreshold)
				}
			} else if math.Abs(found[i].Angle) >= 0.5 {
				t.Errorf("%s page %d: %+.2f° is above the 0.5° default threshold but was not corrected",
					filepath.Base(path), found[i].Page, found[i].Angle)
			}
		}
	}

	if len(angles) == 0 {
		t.Fatal("no page of the corpus could be measured, so this test checked nothing")
	}
	// A distribution, logged rather than asserted: library scans are mostly
	// near level, and a detector reporting large angles across a whole corpus
	// would be visible here long before anybody noticed a crooked page.
	sort.Float64s(angles)
	t.Logf("%d pages measured: median %+.2f°, range %+.2f° to %+.2f°, %d corrected",
		len(angles), angles[len(angles)/2], angles[0], angles[len(angles)-1], len(residuals))
	var worst float64
	for _, r := range residuals {
		worst = math.Max(worst, math.Abs(r))
	}
	if len(residuals) > 0 {
		t.Logf("worst residual after correction over %d corrected pages: %.3f°", len(residuals), worst)
	}
}
