package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// Every verb o-ai defines declares its history kind (Operation.HistoryKind)
// rather than leaving it to the default, and the catalog's history is that
// kind. A new verb without one fails here.
func TestEveryVerbDeclaresItsHistory(t *testing.T) {
	out, _, err := runCLI(t, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	// The framework's verbs carry the catalog's builtin, so o-ai's own
	// settings.* and agent.* (DefineOwn) are never mistaken for them and a
	// library release that adds framework verbs does not change the count.
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ(), Prog: "o-ai"}
	ops := tool.shell().Runtime.Operations
	n := 0
	for _, noun := range doc.Nouns {
		for _, v := range noun.Verbs {
			key := noun.Name + "." + v.Name
			if v.Builtin {
				continue
			}
			n++
			if v.History == "" || ops[key].HistoryKind == "" || v.History != ops[key].HistoryKind {
				t.Errorf("catalog verb %s: history %q, declared %q", key, v.History, ops[key].HistoryKind)
			}
		}
	}
	if n < len(categories)*2+5 {
		t.Fatalf("the catalog has %d o-ai verbs, want every noun's list and show and more", n)
	}
}

// A spot check of the kinds: every read is a fetch; model select writes the
// selection file, a change.
func TestVerbHistoryKinds(t *testing.T) {
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ(), Prog: "o-ai"}
	ops := tool.shell().Runtime.Operations
	for key, want := range map[string]string{
		"skill.list": ochazuke.HistoryFetch, "model.show": ochazuke.HistoryFetch, "status.state": ochazuke.HistoryFetch,
		"status.show": ochazuke.HistoryFetch, "settings.list": ochazuke.HistoryFetch, "quota.show": ochazuke.HistoryFetch, "mcp.tools": ochazuke.HistoryFetch,
		"model.select": ochazuke.HistoryChange,
	} {
		if got := ops[key].HistoryKind; got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
}

// TestSettingsNounIsOAIsOwn: o-ai's settings noun owns the short name
// (DefineOwn), so `settings list` is the inventory kind's verb, and the
// framework's setting verbs stay reachable as the setting noun.
func TestSettingsNounIsOAIsOwn(t *testing.T) {
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ(), Prog: "o-ai"}
	r := tool.shell().Runtime
	if got := r.CanonicalNoun("settings"); got != "settings" {
		t.Fatalf("settings resolves to %q, want o-ai's own", got)
	}
	if op, ok := r.Operation("settings.list"); !ok || op.Builtin {
		t.Fatalf("settings.list is not o-ai's: %+v", op)
	}
	// settings is a thing noun (a facts store over its list), so its set is
	// its own core verb, unavailable: not the framework's setting set.
	if op, ok := r.Operation("settings.set"); !ok || op.Builtin || op.Unavailable == "" {
		t.Fatalf("settings.set is not o-ai's own unavailable core verb: %+v", op)
	}
	if op, ok := r.Operation("setting.path"); !ok || !op.Builtin {
		t.Fatalf("setting.path is not the framework's: %+v", op)
	}
}
