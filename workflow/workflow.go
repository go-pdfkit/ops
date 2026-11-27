// Package workflow runs a node graph of ops verbs, the way BentoPDF's PDF
// Workflow Builder does: input nodes, processing nodes and output nodes joined
// by typed sockets, executed in topological order.
//
// The file format is BentoPDF's own (src/js/workflow/types.ts,
// SerializedWorkflow): {version, nodes:[{id,type,position,controls}],
// connections:[{id,source,sourceOutput,target,targetInput}]}, so a file
// exported from its editor runs here and one written here opens there, for
// the node types both have (see Mapping).
//
// ⛔ A workflow file is UNTRUSTED input. Parse caps its size, its node and
// connection counts, the length of every text control and the shape of every
// control value; Run caps fan-out, the pages and the bytes the whole graph may
// produce. Passwords are never read from, or written to, a workflow file: a
// control that looks like a secret is blanked on the way in and on the way out
// (BentoPDF does the same on save and export), and the secret a node needs
// at run time comes from Options.Secrets.
package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Version is the file format version BentoPDF writes (WORKFLOW_VERSION).
const Version = 1

// The caps on an untrusted file. BentoPDF's MAX_TEXT_CONTROL_LENGTH is 1000.
const (
	MaxFileBytes       = 1 << 20
	MaxNodes           = 256
	MaxConnections     = 1024
	MaxControlsPerNode = 64
	MaxTextLength      = 1000
	MaxIDLength        = 128
	MaxFanOut          = 16
)

// Position is where the editor drew a node. It has no effect on a run.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Node is one box of the graph. Controls hold strings, numbers and booleans
// only.
type Node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Position Position       `json:"position"`
	Controls map[string]any `json:"controls"`
}

// Connection joins one node's output socket to another node's input socket.
type Connection struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceOutput string `json:"sourceOutput"`
	Target       string `json:"target"`
	TargetInput  string `json:"targetInput"`
}

// Workflow is a parsed file.
type Workflow struct {
	Version     int          `json:"version"`
	Nodes       []Node       `json:"nodes"`
	Connections []Connection `json:"connections"`

	// Blanked lists the nodes whose secret controls were blanked on the way
	// in. It is not part of the file.
	Blanked []string `json:"-"`
}

// ErrInvalid is wrapped by every refusal of a malformed file.
var ErrInvalid = errors.New("workflow: invalid workflow file")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Read parses a workflow file from a reader, refusing one larger than
// MaxFileBytes without reading the rest.
func Read(r io.Reader) (*Workflow, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse parses a workflow file.
func Parse(b []byte) (*Workflow, error) {
	if len(b) > MaxFileBytes {
		return nil, invalid("larger than %d bytes", MaxFileBytes)
	}
	var raw struct {
		Version     int           `json:"version"`
		Nodes       *[]Node       `json:"nodes"`
		Connections *[]Connection `json:"connections"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, invalid("%v", err)
	}
	// BentoPDF: !Array.isArray(data.nodes) || !Array.isArray(data.connections).
	if raw.Nodes == nil || raw.Connections == nil {
		return nil, invalid("nodes and connections must both be lists")
	}
	w := &Workflow{Version: raw.Version, Nodes: *raw.Nodes, Connections: *raw.Connections}
	if len(w.Nodes) > MaxNodes {
		return nil, invalid("%d nodes; at most %d", len(w.Nodes), MaxNodes)
	}
	if len(w.Connections) > MaxConnections {
		return nil, invalid("%d connections; at most %d", len(w.Connections), MaxConnections)
	}
	for i := range w.Nodes {
		if err := w.Nodes[i].tidy(); err != nil {
			return nil, err
		}
		if blankSecrets(w.Nodes[i].Controls) {
			w.Blanked = append(w.Blanked, w.Nodes[i].ID)
		}
	}
	for _, c := range w.Connections {
		for _, s := range []string{c.ID, c.Source, c.SourceOutput, c.Target, c.TargetInput} {
			if len(s) > MaxIDLength {
				return nil, invalid("a connection field is %d bytes; at most %d", len(s), MaxIDLength)
			}
		}
	}
	return w, nil
}

// tidy checks the shape of a node and of every control value.
func (n *Node) tidy() error {
	if len(n.ID) > MaxIDLength || len(n.Type) > MaxIDLength {
		return invalid("a node id or type is longer than %d bytes", MaxIDLength)
	}
	if len(n.Controls) > MaxControlsPerNode {
		return invalid("node %q has %d controls; at most %d", n.ID, len(n.Controls), MaxControlsPerNode)
	}
	for k, v := range n.Controls {
		if len(k) > MaxIDLength {
			return invalid("node %q has a control name longer than %d bytes", n.ID, MaxIDLength)
		}
		switch v := v.(type) {
		case bool, float64:
			// encoding/json refuses 1e999 itself, so a float64 here is finite.
		case string:
			if len(v) > MaxTextLength {
				return invalid("node %q: control %q is %d bytes; at most %d", n.ID, k, len(v), MaxTextLength)
			}
		default:
			return invalid("node %q: control %q must be a string, a number or a boolean", n.ID, k)
		}
	}
	return nil
}

// secretWords are the fragments of a control name that mark it as a secret.
// Every node is checked, known or not, because an unknown node's password is
// as secret as a known one's.
var secretWords = []string{"pass", "secret", "token"}

func isSecret(key string) bool {
	k := strings.ToLower(key)
	for _, w := range secretWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// blankSecrets empties every secret control and says whether any held a value.
func blankSecrets(c map[string]any) bool {
	cleared := false
	for k, v := range c {
		if !isSecret(k) {
			continue
		}
		if s, ok := v.(string); !ok || s != "" {
			c[k] = ""
			cleared = true
		}
	}
	return cleared
}

// Marshal writes the workflow in BentoPDF's format, secrets blanked, without
// changing w.
func (w *Workflow) Marshal() ([]byte, error) {
	out := Workflow{Version: w.Version, Nodes: make([]Node, len(w.Nodes)), Connections: w.Connections}
	if out.Connections == nil {
		out.Connections = []Connection{}
	}
	for i, n := range w.Nodes {
		c := make(map[string]any, len(n.Controls))
		for k, v := range n.Controls {
			c[k] = v
		}
		blankSecrets(c)
		n.Controls = c
		out.Nodes[i] = n
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
