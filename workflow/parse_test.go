package workflow

import (
	"errors"
	"math"
	"strings"
	"testing"
)

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

const okJSON = `{"version":1,"nodes":[{"id":"a","type":"PDFInputNode","position":{"x":1,"y":2},"controls":{}}],"connections":[]}`

func TestParseOK(t *testing.T) {
	w, err := Parse([]byte(okJSON))
	if err != nil || len(w.Nodes) != 1 || w.Nodes[0].Position.Y != 2 || w.Version != 1 {
		t.Fatalf("%+v %v", w, err)
	}
	w, err = Read(strings.NewReader(okJSON))
	if err != nil || len(w.Nodes) != 1 {
		t.Fatal(err)
	}
}

func TestParseRefusals(t *testing.T) {
	long := strings.Repeat("x", MaxIDLength+1)
	ctrl := func(n int) string {
		var b []string
		for i := 0; i < n; i++ {
			b = append(b, `"k`+strings.Repeat("a", i%3)+string(rune('A'+i%26))+string(rune('a'+i/26))+`":1`)
		}
		return strings.Join(b, ",")
	}
	nodes := func(n int) string {
		return `{"nodes":[` + strings.TrimSuffix(strings.Repeat(`{"id":"a","type":"t","controls":{}},`, n), ",") + `],"connections":[]}`
	}
	conns := func(n int) string {
		return `{"nodes":[],"connections":[` + strings.TrimSuffix(strings.Repeat(`{},`, n), ",") + `]}`
	}
	cases := []struct{ name, in, want string }{
		{"not json", `{`, "unexpected end"},
		{"no lists", `{"version":1}`, "must both be lists"},
		{"no connections", `{"nodes":[]}`, "must both be lists"},
		{"too many nodes", nodes(MaxNodes + 1), "257 nodes"},
		{"too many connections", conns(MaxConnections + 1), "1025 connections"},
		{"long id", `{"nodes":[{"id":"` + long + `","type":"t","controls":{}}],"connections":[]}`, "longer than"},
		{"long type", `{"nodes":[{"id":"a","type":"` + long + `","controls":{}}],"connections":[]}`, "longer than"},
		{"many controls", `{"nodes":[{"id":"a","type":"t","controls":{` + ctrl(MaxControlsPerNode+1) + `}}],"connections":[]}`, "65 controls"},
		{"long control name", `{"nodes":[{"id":"a","type":"t","controls":{"` + long + `":1}}],"connections":[]}`, "control name longer"},
		{"long text", `{"nodes":[{"id":"a","type":"t","controls":{"k":"` + strings.Repeat("y", MaxTextLength+1) + `"}}],"connections":[]}`, "at most 1000"},
		{"array control", `{"nodes":[{"id":"a","type":"t","controls":{"k":[1]}}],"connections":[]}`, "must be a string"},
		{"null control", `{"nodes":[{"id":"a","type":"t","controls":{"k":null}}],"connections":[]}`, "must be a string"},
		{"huge number", `{"nodes":[{"id":"a","type":"t","controls":{"k":1e999}}],"connections":[]}`, "invalid"},
		{"long connection field", `{"nodes":[],"connections":[{"source":"` + long + `"}]}`, "connection field"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.in))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestReadBounds(t *testing.T) {
	big := strings.Repeat(" ", MaxFileBytes+1)
	if _, err := Read(strings.NewReader(big)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := Read(failReader{}); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	// Exactly at the limit is fine for the size check (it fails as JSON).
	if _, err := Parse([]byte(strings.Repeat(" ", MaxFileBytes))); err == nil || strings.Contains(err.Error(), "larger than") {
		t.Fatal(err)
	}
}

func TestSecretsAreBlankedAndNeverWritten(t *testing.T) {
	src := `{"version":1,"nodes":[
	 {"id":"e","type":"EncryptNode","controls":{"userPassword":"hunter2","ownerPassword":"","Token":7,"title":"x"}},
	 {"id":"q","type":"Whatever","controls":{"client_secret":"s3"}}],"connections":[]}`
	w, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(w.Blanked, ","); got != "e,q" {
		t.Fatalf("blanked %q", got)
	}
	if w.Nodes[0].Controls["userPassword"] != "" || w.Nodes[0].Controls["Token"] != "" || w.Nodes[0].Controls["title"] != "x" {
		t.Fatalf("%v", w.Nodes[0].Controls)
	}
	// A workflow built by hand with a password in it never serialises it.
	h := WF([]Node{N("e", "EncryptNode", "userPassword", "hunter2", "title", "x")})
	h.Connections = nil
	b, err := h.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "hunter2") || !strings.Contains(s, `"userPassword": ""`) || !strings.Contains(s, `"connections": []`) {
		t.Fatalf("%s", s)
	}
	if h.Nodes[0].Controls["userPassword"] != "hunter2" {
		t.Fatal("Marshal changed the workflow")
	}
	// And it reads back.
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalError(t *testing.T) {
	w := WF([]Node{N("a", "PDFInputNode")})
	w.Nodes[0].Position.X = math.Inf(1)
	if _, err := w.Marshal(); err == nil {
		t.Fatal("infinity was written")
	}
}

func TestIssueString(t *testing.T) {
	if s := (Issue{Error, "", "boom"}).String(); s != "error: boom" {
		t.Fatal(s)
	}
	if s := (Issue{Warning, "n1", "hm"}).String(); s != `warning: node "n1": hm` {
		t.Fatal(s)
	}
	if HasError([]Issue{{Warning, "", ""}}) || !HasError([]Issue{{Warning, "", ""}, {Error, "", ""}}) {
		t.Fatal("HasError")
	}
}
