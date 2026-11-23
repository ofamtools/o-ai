package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stateWorld runs o-ai's real collection (no probes) over a temporary home
// and cache, a new Tool per call as separate processes would.
type stateWorld struct {
	t           *testing.T
	home, cache string
	now         time.Time
	collections int
}

func newStateWorld(t *testing.T) *stateWorld {
	w := &stateWorld{t: t, home: isolatedHome(t), cache: t.TempDir(), now: time.Now()}
	writeSkill(t, filepath.Join(w.home, ".claude", "skills", "first", "SKILL.md"), "first")
	return w
}

func writeSkill(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (w *stateWorld) tool(out *bytes.Buffer) *Tool {
	return &Tool{Stdin: strings.NewReader(""), Stdout: out, Stderr: &bytes.Buffer{}, Env: append(os.Environ(), "XDG_CACHE_HOME="+w.cache), Home: w.home, Cwd: w.home, Prog: "o-ai",
		now:     func() time.Time { return w.now },
		collect: func(o Options, need Need) Inventory { w.collections++; return CollectNeed(o, need) }}
}

func (w *stateWorld) fingerprint() (string, error) {
	tl := w.tool(&bytes.Buffer{})
	o := tl.defaultOptions()
	o.NoProbe = true
	return tl.stateFingerprint(o)
}

func (w *stateWorld) runLists() string {
	w.t.Helper()
	var out bytes.Buffer
	if err := w.tool(&out).Main(context.Background(), []string{"inventory", "lists", "--no-probe"}); err != nil {
		w.t.Fatal(err)
	}
	return out.String()
}

// TestStateFingerprintFollowsInputs pins the fingerprint's inputs: a new
// skill deep in a tree, an environment variable a collector reads and the
// TTL each change it or refuse it.
func TestStateFingerprintFollowsInputs(t *testing.T) {
	w := newStateWorld(t)
	if _, err := w.fingerprint(); err == nil {
		t.Fatal("a fingerprint with no recorded collection")
	}
	out := w.runLists()
	if !strings.Contains(out, `"noun":"skill"`) || w.collections != 1 {
		t.Fatalf("first state: %d collections, out=%s", w.collections, out)
	}
	fp, err := w.fingerprint()
	if err != nil || fp == "" {
		t.Fatalf("fingerprint after a collection: %q %v", fp, err)
	}
	if again, _ := w.fingerprint(); again != fp {
		t.Fatalf("fingerprint of unchanged inputs moved: %s, %s", fp, again)
	}
	// Repeat call within TTL with unchanged inputs does not recollect.
	w.runLists()
	if w.collections != 1 {
		t.Fatalf("unchanged polls collected: %d", w.collections)
	}

	// A skill added two directories below a skills root.
	writeSkill(t, filepath.Join(w.home, ".claude", "skills", "group", "second", "SKILL.md"), "second")
	if moved, _ := w.fingerprint(); moved == fp {
		t.Fatal("a new skill deep in a tree left the fingerprint")
	}
	out = w.runLists()
	if w.collections != 2 || !strings.Contains(out, "second") {
		t.Fatalf("after a new skill: %d collections, out=%s", w.collections, out)
	}
	fp, _ = w.fingerprint()

	// An environment variable in cacheEnv is another collection.
	t.Setenv("CODEX_HOME", filepath.Join(w.home, "elsewhere"))
	if other, err := w.fingerprint(); err == nil && other == fp {
		t.Fatal("CODEX_HOME changed and the fingerprint did not")
	}
	t.Setenv("CODEX_HOME", "")

	// A collection as old as the TTL refuses the shortcut: the verb
	// collects again.
	w.now = w.now.Add(defaultTTL)
	if _, err := w.fingerprint(); err == nil {
		t.Fatal("a fingerprint past the TTL")
	}
	w.runLists()
	if w.collections != 3 {
		t.Fatalf("past the TTL: %d collections", w.collections)
	}
}

// TestInventoryCacheRecollectsWhenInputsChange: a settings edit within the
// TTL is a new collection, not the cached inventory.
func TestInventoryCacheRecollectsWhenInputsChange(t *testing.T) {
	w := newStateWorld(t)
	w.runLists()
	w.runLists()
	if w.collections != 1 {
		t.Fatalf("unchanged inputs within the TTL: %d collections", w.collections)
	}
	settings := filepath.Join(w.home, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	w.runLists()
	if w.collections != 2 {
		t.Fatalf("a settings file appeared: %d collections", w.collections)
	}
}
