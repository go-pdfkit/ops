// Copyright (c) 2026, the go-pdfkit/ops authors
// All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// ⛔ A VERB THAT NOTHING SAYS EXISTS is the same defect as a verb that does not
// exist, and it is the one this repository keeps producing: `place` was shipped
// with the drawing already present and unreachable, and the landing page said
// "49 verbs" when the command answered 53. Both are invisible — nothing fails,
// the feature is simply unfindable — so the only thing that catches them is a
// test that reads the prose and the table against each other.
//
// It deliberately checks BOTH directions. A missing mention hides a verb; a
// mention of a verb that no longer exists sends a reader to a command that
// errors out, which is worse, because they will believe the README over the
// error.
func TestEveryVerbIsInTheREADMEAndEveryREADMEVerbExists(t *testing.T) {
	doc, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	prose := string(doc)

	real := map[string]bool{}
	for _, c := range commands {
		real[c.name] = true
	}

	// ⛔ ONE parse feeds both directions. The first version of this test read
	// the README one way with a regexp and the other with strings.Contains,
	// and the two disagreed: it reported `decrypt` and `permissions` as
	// undocumented, because the README writes them as `pdfops -password
	// letmein decrypt`, which the Contains spelling could not see. A control
	// whose two halves count differently invents defects, and an invented one
	// costs what a real one costs.
	shown := map[string]bool{}
	invocation := regexp.MustCompile(`pdfops (?:-password \S+ )?([a-z][a-z-]*)`)
	for _, m := range invocation.FindAllStringSubmatch(prose, -1) {
		shown[m[1]] = true
	}

	for name := range real {
		// A backtick mention counts too: some verbs are explained in prose
		// before they are shown. What does not count is the bare word —
		// "number", "blank" and "place" are all ordinary English here.
		if !shown[name] && !strings.Contains(prose, "`"+name+"`") {
			t.Errorf("the README never mentions the %q verb, so nobody can find it", name)
		}
	}

	for name := range shown {
		if !real[name] {
			t.Errorf("the README shows `pdfops %s`, which is not a verb this command has", name)
		}
	}
}
