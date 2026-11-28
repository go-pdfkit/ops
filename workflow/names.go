package workflow

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameBytes bounds the name of an output file, ".pdf" included.
const MaxNameBytes = 128

// name gives every output a safe, unique file name ending in .pdf. Two names
// are the same when they differ only by case, because the file systems an
// archive is unpacked on (Windows, macOS) say so.
func (r *Result) name() {
	seen := map[string]bool{}
	next := map[string]int{} // the next number to try for a name, so the search is linear
	for i := range r.Files {
		base := cleanName(r.Files[i].Name)
		key := strings.ToLower(base)
		n := base
		for seen[strings.ToLower(n)] {
			k := max(next[key], 2)
			next[key] = k + 1
			n = numbered(base, k)
		}
		seen[strings.ToLower(n)] = true
		r.Files[i].Name = n
	}
}

// numbered inserts -k before the .pdf, shortening the stem to fit.
func numbered(base string, k int) string {
	suffix := fmt.Sprintf("-%d.pdf", k)
	return cut(strings.TrimSuffix(base, ".pdf"), MaxNameBytes-len(suffix)) + suffix
}

// cut shortens s to at most n bytes, on a character boundary.
func cut(s string, n int) string {
	n = min(n, len(s))
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// windowsReserved are the device names Windows will not let a file have,
// with or without an extension.
var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

func reserved(stem string) bool {
	first, _, _ := strings.Cut(stem, ".")
	first = strings.ToUpper(strings.TrimRight(first, " "))
	if windowsReserved[first] {
		return true
	}
	return len(first) == 4 && (strings.HasPrefix(first, "COM") || strings.HasPrefix(first, "LPT")) && first[3] >= '1' && first[3] <= '9'
}

// cleanName keeps the last element of a name, without control characters, a
// path, a drive prefix or a character Windows refuses, and gives it a .pdf
// suffix, at most MaxNameBytes long. A name is data from a file name or a
// workflow, and ends up in an archive: it must not climb out of it, nor name
// a device.
func cleanName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '\\':
			b.WriteByte('/')
		case strings.ContainsRune(`<>:"|?*`, r):
			b.WriteByte('_')
		case unicode.IsPrint(r):
			b.WriteRune(r)
		}
	}
	base := strings.TrimSpace(path.Base(b.String()))
	stem := base
	if strings.HasSuffix(strings.ToLower(stem), ".pdf") {
		stem = stem[:len(stem)-4]
	}
	stem = strings.TrimRight(stem, ". ")
	if stem == "" || stem == "/" {
		stem = "document"
	}
	if reserved(stem) {
		stem = "_" + stem
	}
	return cut(stem, MaxNameBytes-len(".pdf")) + ".pdf"
}
