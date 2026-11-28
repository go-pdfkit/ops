package workflow

import (
	"fmt"
	"sort"
	"strings"
)

// Level says how serious an Issue is.
type Level string

// The two levels. A run is refused when there is any Error.
const (
	Error   Level = "error"
	Warning Level = "warning"
)

// Issue is one finding of Check. Node is the node's id, empty when the
// finding is about the whole graph.
type Issue struct {
	Level   Level
	Node    string
	Message string
}

func (i Issue) String() string {
	if i.Node == "" {
		return fmt.Sprintf("%s: %s", i.Level, i.Message)
	}
	return fmt.Sprintf("%s: node %q: %s", i.Level, i.Node, i.Message)
}

// HasError says whether any issue is an Error.
func HasError(issues []Issue) bool {
	for _, i := range issues {
		if i.Level == Error {
			return true
		}
	}
	return false
}

// graph is the part of a workflow that Check found sound enough to walk.
type graph struct {
	byID  map[string]int // node id -> index in nodes
	nodes []Node         // the first node of each id, in file order
	conns []Connection   // connections whose two ends exist
}

// Check validates a workflow without running it: typed sockets, cycles, an
// input and an output, Encrypt last, node types, fan-out. Unknown node types
// are errors that name the node — they are never silently dropped.
func Check(w *Workflow) []Issue {
	var out []Issue
	add := func(l Level, node, format string, a ...any) {
		out = append(out, Issue{l, node, fmt.Sprintf(format, a...)})
	}
	if w.Version != Version {
		add(Warning, "", "file version is %d, this reader knows %d", w.Version, Version)
	}
	g := graph{byID: map[string]int{}}
	inputs, outputs := 0, 0
	for i, n := range w.Nodes {
		switch {
		case n.ID == "":
			add(Error, "", "node %d has no id", i+1)
			continue
		case has(g.byID, n.ID):
			add(Error, n.ID, "the id is used by another node")
			continue
		}
		g.byID[n.ID] = len(g.nodes)
		g.nodes = append(g.nodes, n)
		s := nodes[n.Type]
		switch {
		case s != nil:
			if s.category == Input {
				inputs++
			}
			if s.category == Output {
				outputs++
			}
			if ig := present(n, s.ignored); len(ig) > 0 {
				add(Warning, n.ID, "%s: controls not honoured here: %s", s.label, strings.Join(ig, ", "))
			}
		case unsupported[n.Type] != "":
			add(Error, n.ID, "%q has no pdfops verb yet", n.Type)
		default:
			add(Error, n.ID, "unknown node type %q", n.Type)
		}
	}
	for _, id := range w.Blanked {
		add(Warning, id, "a password was in the file and has been dropped; give it as a secret at run time")
	}
	if inputs == 0 {
		add(Error, "", "the workflow has no input node")
	}
	if outputs == 0 {
		add(Error, "", "the workflow has no output node")
	}

	taken := map[string]int{} // target\x00input -> connections
	fan := map[string]int{}   // source\x00output -> connections
	for _, c := range w.Connections {
		si, sok := g.byID[c.Source]
		ti, tok := g.byID[c.Target]
		if !sok || !tok {
			add(Error, "", "connection %q joins %q to %q, and one of them is not a node", c.ID, c.Source, c.Target)
			continue
		}
		g.conns = append(g.conns, c)
		from, to := nodes[g.nodes[si].Type], nodes[g.nodes[ti].Type]
		if from == nil || to == nil {
			continue // the node's own error already said so
		}
		op := find(from.out, c.SourceOutput)
		ip := find(to.in, c.TargetInput)
		if op == nil {
			add(Error, c.Source, "%s has no output %q", from.label, c.SourceOutput)
		}
		if ip == nil {
			add(Error, c.Target, "%s has no input %q", to.label, c.TargetInput)
		}
		if op == nil || ip == nil {
			continue
		}
		if !compatible(op.socket, ip.socket) {
			add(Error, c.Target, "a %s output cannot feed a %s input", op.socket, ip.socket)
		}
		k := c.Target + "\x00" + c.TargetInput
		taken[k]++
		if taken[k] == 2 && !ip.multi {
			add(Error, c.Target, "input %q takes one connection", c.TargetInput)
		}
		f := c.Source + "\x00" + c.SourceOutput
		fan[f]++
		if fan[f] == MaxFanOut+1 {
			add(Error, c.Source, "output %q feeds more than %d connections", c.SourceOutput, MaxFanOut)
		}
		if g.nodes[si].Type == "EncryptNode" && to.category != Output {
			add(Error, c.Source, "Encrypt must be the last processing step: it feeds %s", to.label)
		}
	}
	for _, n := range g.nodes {
		s := nodes[n.Type]
		if s == nil {
			continue
		}
		for _, p := range s.in {
			if taken[n.ID+"\x00"+p.name] == 0 {
				add(Error, n.ID, "%s: input %q is not connected", s.label, p.name)
			}
		}
	}
	if _, left := g.order(); left != nil {
		add(Error, "", "the graph has a cycle through: %q", left)
		return out
	}
	reach := g.reaching()
	for _, n := range g.nodes {
		if s := nodes[n.Type]; s != nil && s.category != Output && !reach[n.ID] {
			add(Warning, n.ID, "%s does not lead to an output and will be skipped", s.label)
		}
	}
	return out
}

func has[V any](m map[string]V, k string) bool { _, ok := m[k]; return ok }

// find returns the port of that name.
func find(ps []port, name string) *port {
	for i := range ps {
		if ps[i].name == name {
			return &ps[i]
		}
	}
	return nil
}

// present lists, sorted, the keys among keys that the node sets.
func present(n Node, keys []string) []string {
	var out []string
	for _, k := range keys {
		if _, ok := n.Controls[k]; ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// order is Kahn's algorithm, as BentoPDF's topologicalSort: node ids in an
// order where every source precedes its target. On a cycle the first result is
// nil and the second names the nodes that could not be placed.
func (g *graph) order() ([]string, []string) {
	indeg := make(map[string]int, len(g.byID))
	next := map[string][]string{}
	for _, c := range g.conns {
		indeg[c.Target]++
		next[c.Source] = append(next[c.Source], c.Target)
	}
	order := []string{}
	var ready []string
	// File order, so a run is the same every time.
	for _, n := range g.nodes {
		if indeg[n.ID] == 0 {
			ready = append(ready, n.ID)
		}
	}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, t := range next[id] {
			if indeg[t]--; indeg[t] == 0 {
				ready = append(ready, t)
			}
		}
	}
	if len(order) == len(g.byID) {
		return order, nil
	}
	var left []string
	for id, d := range indeg {
		if d > 0 {
			left = append(left, id)
		}
	}
	sort.Strings(left)
	return nil, left
}

// reaching is the set of nodes from which an output node can be reached.
func (g *graph) reaching() map[string]bool {
	reach := map[string]bool{}
	var queue []string
	for _, n := range g.nodes {
		if s := nodes[n.Type]; s != nil && s.category == Output {
			reach[n.ID] = true
			queue = append(queue, n.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, c := range g.conns {
			if c.Target == id && !reach[c.Source] {
				reach[c.Source] = true
				queue = append(queue, c.Source)
			}
		}
	}
	return reach
}
