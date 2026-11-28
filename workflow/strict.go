package workflow

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
)

// strictKeys checks the keys of the JSON object in raw, which is valid JSON
// already: it refuses a value that is no object, a key that appears twice, and
// a key that spells a known key with other letter case. encoding/json would
// accept the last two, merging duplicates and matching keys without regard to
// case, so two readers could see two different workflows in one file.
func strictKeys(raw []byte, what string, known ...string) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, _ := dec.Token(); t != json.Delim('{') {
		return invalid("%s must be an object", what)
	}
	seen := map[string]bool{}
	for dec.More() {
		t, _ := dec.Token()
		k, _ := t.(string)
		if seen[k] {
			return invalid("%s has the key %q twice", what, k)
		}
		seen[k] = true
		for _, w := range known {
			if k != w && strings.EqualFold(k, w) {
				return invalid("%s has the key %q, which must be spelled %q", what, k, w)
			}
		}
		var skip json.RawMessage
		_ = dec.Decode(&skip)
	}
	return nil
}

// printable reports whether every rune of s is one a terminal shows as itself.
// An id is printed in messages, and a control character in one could rewrite
// the line around it.
func printable(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
