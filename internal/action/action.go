// Package action derives the things you can do to this server from the things
// that actually exist on it, and runs them.
//
// There is no list of actions anywhere in this package. A container can be
// started, stopped, and restarted because it is a container; a stack because
// it is a stack. That is what keeps the dashboard from drifting out of date
// with the host.
package action

import (
	"fmt"
	"strings"
)

// Kind is the sort of thing an action targets.
type Kind string

const (
	KindContainer Kind = "container"
	KindStack     Kind = "stack"
	KindUnit      Kind = "unit"
)

// Spec describes one runnable action.
type Spec struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Kind   Kind   `json:"kind"`
	Target string `json:"target"`
	Verb   string `json:"verb"`

	// Confirm is the sentence shown in the confirmation step. Every action has
	// one: there are no danger tiers and nothing runs unconfirmed.
	Confirm string `json:"confirm"`
}

// Verbs available on each kind. This is the only place the vocabulary lives.
var verbsByKind = map[Kind][]string{
	KindContainer: {"start", "stop", "restart"},
	KindStack:     {"start", "stop", "restart"},
	KindUnit:      {"start", "stop", "restart"},
}

// ID builds the canonical identifier for an action.
func ID(kind Kind, verb, target string) string {
	return fmt.Sprintf("%s.%s.%s", kind, verb, target)
}

// ParseID splits an action id back into its parts.
func ParseID(id string) (kind Kind, verb, target string, err error) {
	parts := strings.SplitN(id, ".", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("malformed action id %q", id)
	}
	kind = Kind(parts[0])
	if _, ok := verbsByKind[kind]; !ok {
		return "", "", "", fmt.Errorf("unknown action kind %q", parts[0])
	}
	verb = parts[1]
	if !allowedVerb(kind, verb) {
		return "", "", "", fmt.Errorf("%q is not something you can do to a %s", verb, kind)
	}
	target = parts[2]
	if !validTarget(target) {
		return "", "", "", fmt.Errorf("%q is not a valid target name", target)
	}
	return kind, verb, target, nil
}

// validTarget restricts targets to the characters container names, compose
// projects, and unit names actually use.
//
// Nothing in this package reaches a shell — Docker and systemd are driven
// through their APIs — so this is defence in depth rather than the thing
// standing between a crafted id and the host.
func validTarget(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '@' || r == '\\':
		default:
			return false
		}
	}
	return true
}

func allowedVerb(kind Kind, verb string) bool {
	for _, v := range verbsByKind[kind] {
		if v == verb {
			return true
		}
	}
	return false
}

// For builds the full set of actions available on one target.
func For(kind Kind, target, displayName string) []Spec {
	if displayName == "" {
		displayName = target
	}
	noun := map[Kind]string{
		KindContainer: "container",
		KindStack:     "stack",
		KindUnit:      "service",
	}[kind]

	specs := make([]Spec, 0, len(verbsByKind[kind]))
	for _, verb := range verbsByKind[kind] {
		specs = append(specs, Spec{
			ID:      ID(kind, verb, target),
			Label:   strings.ToUpper(verb[:1]) + verb[1:] + " " + displayName,
			Kind:    kind,
			Target:  target,
			Verb:    verb,
			Confirm: fmt.Sprintf("%s the %s %s?", strings.ToUpper(verb[:1])+verb[1:], displayName, noun),
		})
	}
	return specs
}
