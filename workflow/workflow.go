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
// at run time comes from Options.Secrets. Keys are exact and unique, and ids
// hold only printable characters.
//
// Cancellation of the context given to Run is observed between nodes, not
// inside one: the ops verbs take no context, so a node that has started runs
// to its end, within the page and byte bounds.
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
		Version     int                `json:"version"`
		Nodes       *[]json.RawMessage `json:"nodes"`
		Connections *[]json.RawMessage `json:"connections"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, invalid("%v", err)
	}
	// The keys are exact and unique: encoding/json would match them without
	// regard to case and merge repeats.
	if err := strictKeys(b, "the file", "version", "nodes", "connections"); err != nil {
		return nil, err
	}
	// BentoPDF: !Array.isArray(data.nodes) || !Array.isArray(data.connections).
	if raw.Nodes == nil || raw.Connections == nil {
		return nil, invalid("nodes and connections must both be lists")
	}
	// The counts are checked before any element is decoded.
	if len(*raw.Nodes) > MaxNodes {
		return nil, invalid("%d nodes; at most %d", len(*raw.Nodes), MaxNodes)
	}
	if len(*raw.Connections) > MaxConnections {
		return nil, invalid("%d connections; at most %d", len(*raw.Connections), MaxConnections)
	}
	w := &Workflow{Version: raw.Version, Nodes: make([]Node, len(*raw.Nodes)), Connections: make([]Connection, len(*raw.Connections))}
	for i, el := range *raw.Nodes {
		if err := strictKeys(el, "a node", "id", "type", "position", "controls"); err != nil {
			return nil, err
		}
		n := &w.Nodes[i]
		var ctl struct {
			Controls json.RawMessage `json:"controls"`
		}
		if err := json.Unmarshal(el, n); err != nil {
			return nil, invalid("%v", err)
		}
		_ = json.Unmarshal(el, &ctl) // el has been decoded once already
		if n.Controls != nil {
			if err := strictKeys(ctl.Controls, "the controls of a node"); err != nil {
				return nil, err
			}
		}
		if err := n.tidy(); err != nil {
			return nil, err
		}
		if blankSecrets(n.Controls) {
			w.Blanked = append(w.Blanked, n.ID)
		}
	}
	for i, el := range *raw.Connections {
		if err := strictKeys(el, "a connection", "id", "source", "sourceOutput", "target", "targetInput"); err != nil {
			return nil, err
		}
		c := &w.Connections[i]
		if err := json.Unmarshal(el, c); err != nil {
			return nil, invalid("%v", err)
		}
		for _, s := range []string{c.ID, c.Source, c.SourceOutput, c.Target, c.TargetInput} {
			if len(s) > MaxIDLength {
				return nil, invalid("a connection field is %d bytes; at most %d", len(s), MaxIDLength)
			}
			if !printable(s) {
				return nil, invalid("a connection field holds a character that is not printable")
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
	if !printable(n.ID) || !printable(n.Type) {
		return invalid("a node id or type holds a character that is not printable")
	}
	if len(n.Controls) > MaxControlsPerNode {
		return invalid("node %q has %d controls; at most %d", n.ID, len(n.Controls), MaxControlsPerNode)
	}
	for k, v := range n.Controls {
		if len(k) > MaxIDLength {
			return invalid("node %q has a control name longer than %d bytes", n.ID, MaxIDLength)
		}
		if !printable(k) {
			return invalid("node %q has a control name that is not printable", n.ID)
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
