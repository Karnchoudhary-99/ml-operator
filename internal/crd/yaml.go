package crd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// node is one value in a YAML document built by this package.
// kind is "scalar", "map" or "list"; maps keep insertion order so
// generated manifests are stable and readable. A literal scalar
// (integer, number, boolean) is emitted as-is; a string scalar is
// quoted when it would be misread as another type.
type node struct {
	kind    string
	scal    string
	literal bool
	pairs   []pair
	items   []*node
}

type pair struct {
	key string
	val *node
}

// scalar builds a scalar node. Numbers and booleans are passed as
// their literal text ("1", "true"); everything else is treated as a
// string and quoted when the YAML emitter needs it.
func scalar(v string) *node { return &node{kind: "scalar", scal: v} }

// str builds a string scalar node.
func str(v string) *node { return scalar(v) }

// integer builds an integer scalar node, emitted unquoted.
func integer(v int64) *node {
	return &node{kind: "scalar", scal: strconv.FormatInt(v, 10), literal: true}
}

// integer32 is integer for int32 values.
func integer32(v int32) *node { return integer(int64(v)) }

// number builds a floating-point scalar node, emitted unquoted.
func number(v float64) *node {
	return &node{kind: "scalar", scal: strconv.FormatFloat(v, 'g', -1, 64), literal: true}
}

// boolean builds a boolean scalar node, emitted unquoted.
func boolean(v bool) *node {
	return &node{kind: "scalar", scal: strconv.FormatBool(v), literal: true}
}

// mapping builds an ordered map node from key/value pairs.
func mapping(pairs ...pair) *node { return &node{kind: "map", pairs: pairs} }

// field is a convenience constructor for one map entry.
func field(key string, val *node) pair { return pair{key: key, val: val} }

// list builds a list node.
func list(items ...*node) *node { return &node{kind: "list", items: items} }

// render writes the node as YAML with the given indent level.
func (n *node) render(sb *strings.Builder, indent int) {
	switch n.kind {
	case "scalar":
		writeScalar(sb, n)
	case "map":
		for _, p := range n.pairs {
			sb.WriteString(strings.Repeat(" ", indent))
			sb.WriteString(p.key)
			sb.WriteString(":")
			renderValue(sb, p.val, indent)
		}
	case "list":
		for _, item := range n.items {
			sb.WriteString(strings.Repeat(" ", indent))
			sb.WriteString("-")
			renderListItem(sb, item, indent)
		}
	}
}

// renderValue renders a value after its key (or list dash): scalars
// inline, maps and lists on the following lines — except the first
// entry of a map inside a list, which stays on the dash line
// ("- name: x"), the conventional compact form.
func renderValue(sb *strings.Builder, n *node, indent int) {
	switch n.kind {
	case "scalar":
		sb.WriteString(" ")
		writeScalar(sb, n)
		sb.WriteString("\n")
	case "map":
		if len(n.pairs) == 0 {
			sb.WriteString(" {}\n")
			return
		}
		sb.WriteString("\n")
		n.render(sb, indent+2)
	case "list":
		if len(n.items) == 0 {
			sb.WriteString(" []\n")
			return
		}
		sb.WriteString("\n")
		n.render(sb, indent+2)
	}
}

// renderListItem renders the first line of a map that follows a list
// dash: "- key: value" on the dash line, the rest indented below it.
// Only used for the first pair; the remaining pairs render normally.
func renderListItem(sb *strings.Builder, n *node, indent int) {
	if n.kind != "map" || len(n.pairs) == 0 {
		renderValue(sb, n, indent)
		return
	}
	sb.WriteString(" ")
	p := n.pairs[0]
	sb.WriteString(p.key)
	sb.WriteString(":")
	renderValue(sb, p.val, indent+2)
	for _, rest := range n.pairs[1:] {
		sb.WriteString(strings.Repeat(" ", indent+2))
		sb.WriteString(rest.key)
		sb.WriteString(":")
		renderValue(sb, rest.val, indent+2)
	}
}

// writeScalar writes one scalar: literally for typed values,
// quoted-when-needed for strings.
func writeScalar(sb *strings.Builder, n *node) {
	if n.literal {
		sb.WriteString(n.scal)
		return
	}
	sb.WriteString(quoteScalar(n.scal))
}

// quoteScalar quotes a string scalar when it would be misread as
// another YAML type, or when it contains characters that need
// escaping. Plain strings stay unquoted so the output reads like
// hand-written YAML. The quoting is single-quote style, which
// needs no escaping except doubling an embedded single quote.
func quoteScalar(s string) string {
	if s == "" {
		return `""`
	}
	if needsQuoting(s) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

// needsQuoting reports whether the scalar must be quoted to survive
// a YAML round trip as a string.
func needsQuoting(s string) bool {
	// Looks like a bool, null, or number: quote it, or a string
	// "true" would come back as a boolean.
	switch strings.ToLower(s) {
	case "true", "false", "null", "~", "yes", "no", "on", "off":
		return true
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	// Leading/trailing space, or a character that starts a YAML
	// construct, or ": " / " #" inside the string.
	if s != strings.TrimSpace(s) {
		return true
	}
	if strings.Contains(s, ": ") || strings.Contains(s, " #") {
		return true
	}
	switch s[0] {
	case '[', ']', '{', '}', ',', '&', '*', '#', '?', '|', '-', '<', '>', '=', '!', '%', '@', '`', '"', '\'':
		return true
	}
	return false
}

// keys returns the sorted keys of a map, for deterministic output
// when a manifest is built from a Go map (e.g. env vars).
func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// renderDoc renders a whole document: the node plus a trailing
// newline, and validates the output is not empty.
func renderDoc(n *node) (string, error) {
	var sb strings.Builder
	n.render(&sb, 0)
	out := sb.String()
	if out == "" {
		return "", fmt.Errorf("cannot render an empty document")
	}
	return out, nil
}
