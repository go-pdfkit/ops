package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/go-pdfkit/reader"
)

// runObjects writes the document's object graph as JSON: what the file is
// made of rather than what it says.
//
// ⛔ It is the graph REACHABLE FROM THE TRAILER, not every object in the file.
// That is the honest set, because it is the one a reader sees: an object no
// reference points at is in the bytes and in nobody's document, and listing it
// beside the rest would say the two are alike. The count of what was skipped
// is not reported for the same reason — this never looked.
func runObjects(c *context, args []string) error {
	fs := flags("objects")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs, 1, "<in.pdf>"); err != nil {
		return err
	}
	src, err := c.read(fs.Arg(0))
	if err != nil {
		return err
	}
	// An empty result is `[]`, never `null`: a caller testing the length of
	// what came back should not have to test for nothing first, and the
	// usual document has nothing unread.
	w := &graphWriter{get: src.Get, objects: map[string]any{}, unread: []string{}}
	trailer := w.value(src.Trailer(), 0)
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonGraph{
		Version:   src.Version(),
		Pages:     src.PageCount(),
		Encrypted: src.Encrypted(),
		Repaired:  src.Repaired(),
		Trailer:   trailer,
		Objects:   w.objects,
		Unread:    w.unread,
	})
}

// A jsonGraph is the whole answer.
type jsonGraph struct {
	Version   string         `json:"version"`
	Pages     int            `json:"pages"`
	Encrypted bool           `json:"encrypted"`
	Repaired  bool           `json:"repaired"`
	Trailer   any            `json:"trailer"`
	Objects   map[string]any `json:"objects"`
	// Unread names the references that could not be followed. ⛔ It is in the
	// output rather than on the error stream because this IS the answer for
	// a damaged file: "the graph, and these four edges lead nowhere" is what
	// somebody debugging one needs, and an empty list is the usual case.
	Unread []string `json:"unread"`
}

// a graphWriter walks the graph once, keeping each object it reaches.
//
// ⛔ It holds a GETTER rather than the document, and that is a seam with a
// reason. [reader.Document.Get] is forgiving by design — a reference to an
// object that was never written resolves to [reader.Null] rather than to an
// error — so no file this can be handed reaches the branch that records an
// unreadable reference. A real corpus will; a test cannot, unless the getter
// is something a test can supply.
type graphWriter struct {
	get     func(reader.Ref) (reader.Object, error)
	objects map[string]any
	unread  []string
}

// maxGraphDepth bounds the walk.
//
// ⛔ A PDF is a GRAPH and not a tree — a page points at its parent, which
// points back at the page — so the visited set is what stops the walk, and it
// does. This is the second guard, for depth that is real rather than cyclic:
// a thousand nested arrays is a legal file and a stack overflow is not a
// diagnosis.
const maxGraphDepth = 256

// value turns one object into something json can write.
//
//	null, true, 1, 1.5   as themselves
//	/Name                a string beginning with a slash
//	a string             {"text": "…"} or {"hex": "…"} when it is not text
//	an array             an array
//	a dictionary         an object
//	a reference          {"ref": "12 0"}, and the object itself under that key
//	a stream             {"dict": {…}, "length": n} — the BYTES are not here
//
// ⛔ A string is wrapped and a name is not, because the two have to be told
// apart and only one of them can be written plainly. A name cannot contain an
// unescaped slash at the front and anything else; a string can be anything at
// all, including "/Type".
//
// ⛔ And a stream's bytes are left out on purpose. They are the bulk of most
// files and they are binary, so including them would mean base64 — which
// turns a 35 MB file into a 47 MB JSON nobody reads. What is here is what
// says where they are and how they are encoded.
func (w *graphWriter) value(o reader.Object, depth int) any {
	if depth > maxGraphDepth {
		return map[string]any{"depth": "deeper than " + fmt.Sprint(maxGraphDepth)}
	}
	// ⛔ There is no `case nil`. [reader.Null] is how the format's null
	// arrives, and reader says why in its own words: "a missing dictionary
	// entry resolves to Null rather than to a nil Object, so callers never
	// have to nil-check". A case for nil here would be a branch nothing can
	// reach, guarding against the one thing that package promises not to do.
	switch v := o.(type) {
	case reader.Null:
		// The PDF null object, which is not the absence of one: a dictionary
		// entry whose value is null is PRESENT and says nothing, and the
		// format gives that its own meaning in a few places.
		return nil
	case reader.Bool:
		return bool(v)
	case reader.Integer:
		return int64(v)
	case reader.Real:
		return float64(v)
	case reader.Name:
		return "/" + string(v)
	case reader.String:
		return stringValue(v)
	case reader.Array:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = w.value(e, depth+1)
		}
		return out
	case reader.Dict:
		return w.dict(v, depth)
	case *reader.Stream:
		return map[string]any{
			"dict":   w.dict(v.Dict, depth),
			"length": len(v.Raw),
		}
	case reader.Ref:
		w.follow(v, depth)
		return map[string]any{"ref": fmt.Sprintf("%d %d", v.Num, v.Gen)}
	}
	// reader.Object is a closed set and every member is above. A value that
	// arrives here is a type added upstream, and saying what it was beats
	// writing null and losing it.
	return map[string]any{"unknown": fmt.Sprintf("%T", o)}
}

// dict writes a dictionary.
//
// ⛔ It does NOT sort the keys, and the output is still the same bytes twice:
// [encoding/json] sorts a map's keys itself. A sort here looked like the
// thing that made the output deterministic and was doing nothing — a mutation
// reversing it changed no test, which is how it was found. Determinism still
// matters; it just is not this function's doing.
func (w *graphWriter) dict(d reader.Dict, depth int) map[string]any {
	out := make(map[string]any, len(d))
	for k, v := range d {
		out[string(k)] = w.value(v, depth+1)
	}
	return out
}

// follow reads an object the graph points at, once.
func (w *graphWriter) follow(r reader.Ref, depth int) {
	key := fmt.Sprintf("%d %d", r.Num, r.Gen)
	if _, seen := w.objects[key]; seen {
		return
	}
	// ⛔ Marked as seen BEFORE it is read, or a cycle — a page's /Parent
	// pointing back at the page — never terminates. The placeholder is
	// replaced below; nothing reads it in between, because this is one pass.
	w.objects[key] = nil
	o, err := w.get(r)
	if err != nil {
		w.unread = append(w.unread, key)
		delete(w.objects, key)
		return
	}
	w.objects[key] = w.value(o, depth+1)
}

// stringValue writes a PDF string as text where it is text and as hex where
// it is not.
//
// ⛔ Through [reader.TextString], because the BYTES of a text string are not
// text: a title written in UTF-16 behind a byte order mark comes out of the
// raw bytes as "\xfe\xffL\x00a\x00…". What is left after that — a digest, an
// encrypted blob, an ID — has no text in it at all and is written as hex
// rather than as replacement characters.
func stringValue(s reader.String) map[string]any {
	if text, ok := reader.TextString(s); ok && utf8.ValidString(text) && !hasControl(text) {
		return map[string]any{"text": text}
	}
	return map[string]any{"hex": hex.EncodeToString([]byte(s))}
}

// hasControl reports a character that would make the JSON unreadable even
// though it is valid UTF-8. A trailer /ID is two sixteen-byte digests, and
// most of those decode to something nothing can print.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return true
		}
	}
	return false
}
