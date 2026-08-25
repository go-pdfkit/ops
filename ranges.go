package ops

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRange turns the page selection people write — "1-3,7,10-", "even",
// "last", "all" — into page numbers counting from one, in the order given and
// with duplicates kept, because "1,1,2" really does mean three pages.
//
// A range with no start begins at the first page and one with no end runs to
// the last. A descending range like "5-2" counts down, which is how a reversed
// extract is written.
func ParseRange(spec string, pageCount int) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("ops: an empty page range selects nothing")
	}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		got, err := parseRangePart(strings.TrimSpace(part), pageCount)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// parseRangePart handles one comma-separated piece.
func parseRangePart(part string, pageCount int) ([]int, error) {
	switch strings.ToLower(part) {
	case "all", "*":
		return sequence(1, pageCount), nil
	case "even":
		return every(2, pageCount), nil
	case "odd":
		return every(1, pageCount), nil
	case "last":
		if pageCount == 0 {
			return nil, fmt.Errorf("ops: \"last\" names a page of an empty document")
		}
		return []int{pageCount}, nil
	}
	from, to, err := rangeBounds(part, pageCount)
	if err != nil {
		return nil, err
	}
	if from < 1 || from > pageCount || to < 1 || to > pageCount {
		return nil, fmt.Errorf("ops: %q is outside a document of %d pages", part, pageCount)
	}
	return sequence(from, to), nil
}

// rangeBounds reads "N", "N-M", "N-" or "-M".
func rangeBounds(part string, pageCount int) (from, to int, err error) {
	dash := strings.Index(part, "-")
	if dash < 0 {
		n, err := pageNumber(part)
		return n, n, err
	}
	head, tail := strings.TrimSpace(part[:dash]), strings.TrimSpace(part[dash+1:])
	from, to = 1, pageCount
	if head != "" {
		if from, err = pageNumber(head); err != nil {
			return 0, 0, err
		}
	}
	if tail != "" {
		if to, err = pageNumber(tail); err != nil {
			return 0, 0, err
		}
	}
	return from, to, nil
}

// pageNumber reads one page number.
func pageNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("ops: %q is not a page number", s)
	}
	return n, nil
}

// sequence counts from one bound to the other, in whichever direction they go.
func sequence(from, to int) []int {
	if from <= to {
		out := make([]int, 0, to-from+1)
		for i := from; i <= to; i++ {
			out = append(out, i)
		}
		return out
	}
	out := make([]int, 0, from-to+1)
	for i := from; i >= to; i-- {
		out = append(out, i)
	}
	return out
}

// every lists the pages from start onwards, two at a time.
func every(start, pageCount int) []int {
	var out []int
	for i := start; i <= pageCount; i += 2 {
		out = append(out, i)
	}
	return out
}
