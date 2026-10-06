package ops

import (
	"reflect"
	"testing"
)

func TestParseRange(t *testing.T) {
	cases := []struct {
		spec string
		want []int
	}{
		{"1", []int{1}},
		{"2-4", []int{2, 3, 4}},
		{"4-2", []int{4, 3, 2}},
		{"1,3,5", []int{1, 3, 5}},
		{"1,1,2", []int{1, 1, 2}},
		{"3-", []int{3, 4, 5}},
		{"-2", []int{1, 2}},
		{"-", []int{1, 2, 3, 4, 5}},
		{"all", []int{1, 2, 3, 4, 5}},
		{"*", []int{1, 2, 3, 4, 5}},
		{"ALL", []int{1, 2, 3, 4, 5}},
		{"even", []int{2, 4}},
		{"odd", []int{1, 3, 5}},
		{"last", []int{5}},
		{" 1 , 3 ", []int{1, 3}},
		{"1-2,last", []int{1, 2, 5}},
	}
	for _, c := range cases {
		got, err := ParseRange(c.spec, 5)
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.spec, got, c.want)
		}
	}
}

func TestParseRangeErrors(t *testing.T) {
	for _, spec := range []string{"", "   ", "x", "0", "6", "1-6", "0-2", "x-2", "1-x", "1,x"} {
		if got, err := ParseRange(spec, 5); err == nil {
			t.Errorf("%q: want an error, got %v", spec, got)
		}
	}
	if _, err := ParseRange("last", 0); err == nil {
		t.Error(`"last" on an empty document: want an error`)
	}
}

// ⛔ "all" of an empty document is nothing, and it used to be [1 0].
//
// sequence(1, 0) takes its descending branch -- the one that makes "3-1"
// reverse three pages -- so "all" on a document with no pages handed back two
// page numbers that do not exist, with no error. Every other spec already got
// this right, which is what made it hard to see.
func TestAllOfAnEmptyDocumentIsNothing(t *testing.T) {
	got, err := ParseRange("all", 0)
	if err != nil {
		t.Fatalf("err = %v, want none", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseRange(\"all\", 0) = %v, want nothing", got)
	}
	if got, err := ParseRange("*", 0); err != nil || len(got) != 0 {
		t.Errorf("ParseRange(\"*\", 0) = %v, %v", got, err)
	}

	// and the siblings, which were right all along -- so a change here cannot
	// quietly make one of them wrong
	for _, spec := range []string{"odd", "even"} {
		if got, err := ParseRange(spec, 0); err != nil || len(got) != 0 {
			t.Errorf("ParseRange(%q, 0) = %v, %v", spec, got, err)
		}
	}
	for _, spec := range []string{"last", "1", "1-"} {
		if _, err := ParseRange(spec, 0); err == nil {
			t.Errorf("ParseRange(%q, 0) was accepted", spec)
		}
	}

	// A document that HAS pages is untouched: "all" is still every page, and
	// a descending range still reverses.
	if got, _ := ParseRange("all", 3); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("ParseRange(\"all\", 3) = %v", got)
	}
	if got, _ := ParseRange("3-1", 3); len(got) != 3 || got[0] != 3 || got[2] != 1 {
		t.Errorf("ParseRange(\"3-1\", 3) = %v, which is the reversal this fix must not break", got)
	}
}

// The crash it caused, through the verb that reaches it. This is the test that
// fails with a panic rather than an assertion if the fix goes away.
func TestAVerbOverAnEmptyDocumentDoesNotPanic(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(*Doc) error
	}{
		{"Resize", func(d *Doc) error { return d.Resize("all", [4]float64{0, 0, 100, 100}) }},
		{"Rotate", func(d *Doc) error { return d.Rotate("all", 90) }},
		{"Select", func(d *Doc) error { return d.Select("all") }},
		{"Delete", func(d *Doc) error { return d.Delete("all") }},
		{"Crop", func(d *Doc) error { return d.Crop("all", [4]float64{0, 0, 100, 100}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := New()
			if err := c.do(d); err != nil {
				t.Logf("%s returned %v, which is a refusal and not a crash", c.name, err)
			}
			if n := d.PageCount(); n != 0 {
				t.Errorf("the document gained %d page(s) from an empty one", n)
			}
		})
	}
}
