package workflow

import (
	"fmt"
	"strings"
	"testing"
)

func has1(issues []Issue, l Level, node, sub string) bool {
	for _, i := range issues {
		if i.Level == l && i.Node == node && strings.Contains(i.Message, sub) {
			return true
		}
	}
	return false
}

func TestCheckSoundWorkflow(t *testing.T) {
	w := line(N("", "RotateNode", "angle", "90"))
	if is := Check(w); len(is) != 0 {
		t.Fatalf("%v", is)
	}
}

func TestCheckFindings(t *testing.T) {
	in, out := N("in", "PDFInputNode"), N("out", "DownloadNode")
	cases := []struct {
		name  string
		w     *Workflow
		level Level
		node  string
		sub   string
	}{
		{"version", func() *Workflow { w := line(N("", "RotateNode")); w.Version = 2; return w }(), Warning, "", "version is 2"},
		{"no id", WF([]Node{in, out, N("", "RotateNode")}, C("in", "out")), Error, "", "has no id"},
		{"dup id", WF([]Node{in, out, N("in", "RotateNode")}, C("in", "out")), Error, "in", "used by another"},
		{"unsupported", WF([]Node{in, out, N("c", "CropNode")}, C("in", "out")), Error, "c", "no pdfops verb"},
		{"unknown", WF([]Node{in, out, N("c", "NopeNode")}, C("in", "out")), Error, "c", `unknown node type "NopeNode"`},
		{"unknown wired", WF([]Node{in, out, N("c", "NopeNode")}, C("in", "c"), C("c", "out")), Error, "c", "unknown node type"},
		{"no input", WF([]Node{out}), Error, "", "no input node"},
		{"no output", WF([]Node{in}), Error, "", "no output node"},
		{"missing end", WF([]Node{in, out}, C("in", "out"), C("in", "ghost")), Error, "", "not a node"},
		{"bad output name", WF([]Node{in, out}, Connection{ID: "x", Source: "in", SourceOutput: "z", Target: "out", TargetInput: "pdf"}), Error, "in", `no output "z"`},
		{"bad input name", WF([]Node{in, out}, Connection{ID: "x", Source: "in", SourceOutput: "pdf", Target: "out", TargetInput: "z"}), Error, "out", `no input "z"`},
		{"two into one", WF([]Node{in, N("in2", "PDFInputNode"), N("m", "RotateNode"), out}, C("in", "m"), C("in2", "m"), C("m", "out")), Error, "m", "takes one connection"},
		{"not connected", WF([]Node{in, out, N("m", "RotateNode")}, C("in", "out")), Error, "m", "is not connected"},
		{"overlay needs both", WF([]Node{in, N("o", "OverlayNode"), out}, C("in", "o"), C("o", "out")), Error, "o", `input "overlay" is not connected`},
		{"cycle", WF([]Node{in, out, N("a", "RotateNode"), N("b", "ReversePagesNode")}, C("in", "a"), C("a", "b"), C("b", "a"), C("a", "out")), Error, "", "cycle through: a, b"},
		{"self loop", WF([]Node{in, out, N("a", "RotateNode")}, C("in", "a"), C("a", "a"), C("a", "out")), Error, "a", "takes one connection"},
		{"encrypt last", WF([]Node{in, N("e", "EncryptNode"), N("r", "RotateNode"), out}, C("in", "e"), C("e", "r"), C("r", "out")), Error, "e", "Encrypt must be the last"},
		{"dangling", WF([]Node{in, out, N("d", "RotateNode")}, C("in", "out"), C("in", "d")), Warning, "d", "does not lead to an output"},
		{"ignored", line(N("", "WatermarkNode", "opacity", 0.5, "angle", 45, "text", "x")), Warning, "mid", "controls not honoured here: angle, opacity"},
	}
	for _, c := range cases {
		if is := Check(c.w); !has1(is, c.level, c.node, c.sub) {
			t.Errorf("%s: %v", c.name, is)
		}
	}
}

func TestCheckFanOut(t *testing.T) {
	ns := []Node{N("in", "PDFInputNode")}
	var cs []Connection
	for i := 0; i <= MaxFanOut; i++ {
		id := fmt.Sprintf("o%d", i)
		ns = append(ns, N(id, "DownloadNode"))
		cs = append(cs, C("in", id))
	}
	if is := Check(WF(ns, cs...)); !has1(is, Error, "in", "more than 16 connections") {
		t.Fatalf("%v", is)
	}
	if is := Check(WF(ns[:MaxFanOut+1], cs[:MaxFanOut]...)); HasError(is) {
		t.Fatalf("exactly the limit is allowed: %v", is)
	}
}

func TestCheckBlankedSecrets(t *testing.T) {
	w, err := Parse([]byte(`{"version":1,"nodes":[{"id":"in","type":"PDFInputNode","controls":{"password":"p"}},{"id":"o","type":"DownloadNode","controls":{}}],"connections":[{"id":"c","source":"in","sourceOutput":"pdf","target":"o","targetInput":"pdf"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if is := Check(w); !has1(is, Warning, "in", "password was in the file") || HasError(is) {
		t.Fatalf("%v", is)
	}
}

func TestSocketKinds(t *testing.T) {
	for _, c := range []struct {
		a, b Socket
		ok   bool
	}{{PDF, PDF, true}, {PDF, MultiPDF, true}, {MultiPDF, PDF, true}, {Image, Image, true}, {Image, PDF, false}, {PDF, Image, false}, {MultiPDF, Image, false}} {
		if compatible(c.a, c.b) != c.ok {
			t.Errorf("%s -> %s", c.a, c.b)
		}
	}
}

// An image socket cannot be reached through the table today, so the check is
// exercised with a node type that has one.
func TestCheckImageSocket(t *testing.T) {
	nodes["TestImageNode"] = &spec{label: "Img", category: Input, out: []port{{"pdf", Image, false}}}
	defer delete(nodes, "TestImageNode")
	w := WF([]Node{N("i", "TestImageNode"), N("m", "RotateNode"), N("out", "DownloadNode")}, C("i", "m"), C("m", "out"))
	if is := Check(w); !has1(is, Error, "m", "Image output cannot feed a PDF input") {
		t.Fatalf("%v", is)
	}
}

func TestMapping(t *testing.T) {
	m := Mapping()
	covered, no := 0, 0
	for i, r := range m {
		if i > 0 && m[i-1].Type >= r.Type {
			t.Fatalf("not sorted at %s", r.Type)
		}
		if r.Verb == "" {
			no++
		} else {
			covered++
		}
	}
	// The 76 keys of BentoPDF's nodeRegistry: 74 node types, plus its two
	// legacy Download names; 29 types run here (31 rows), 45 do not.
	if len(m) != 76 || covered != 31 || no != 45 {
		t.Fatalf("%d rows: %d covered, %d without a verb", len(m), covered, no)
	}
	for tp := range unsupported {
		if nodes[tp] != nil {
			t.Fatalf("%s is both", tp)
		}
	}
	t.Logf("%d covered (incl. 2 legacy names), %d without a verb", covered, no)
}
