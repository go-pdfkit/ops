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
