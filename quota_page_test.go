package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// The quota list of o-ai's page, hosted from o-ai's published catalog
// (the way mtm mounts it), shows the quota rows although the state leaves
// quota out: the noun is declared live, so the page fills it through
// quota list.
func TestHostedQuotaListFillsThroughItsVerb(t *testing.T) {
	w := newQuotaWorld(t)
	w.cache = t.TempDir()
	w.claudeLogin(quotaNow.Add(time.Hour))
	catalog, err := w.run("catalog")
	if err != nil {
		t.Fatal(err)
	}
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(catalog), &doc); err != nil {
		t.Fatal(err)
	}
	doc.Pages = []ocli.CatalogPage{{ID: "ai", Title: "AI", Layout: ocli.LayoutGroups, Lists: []ocli.CatalogList{
		{ID: "ai/settings", Noun: "settings"}, {ID: "ai/quota", Noun: "quota"},
	}}}
	var calls []string
	fetch := func(_ context.Context, noun, verb string, flags []string) ([]byte, error) {
		calls = append(calls, noun+"."+verb)
		args := append([]string{noun, verb}, flags...)
		if noun == "status" {
			args = append(args, "--no-probe", "--project", w.home)
		}
		out, err := w.run(args...)
		return []byte(out), err
	}
	cfg := ocli.PageConfig{
		Fetch:      func(ctx context.Context, noun, verb string) ([]byte, error) { return fetch(ctx, noun, verb, nil) },
		FetchFlags: fetch,
	}
	r := ochazuke.NewRuntime()
	r.Config().Disable()
	m := ochazuke.NewModel(r)
	m.Width, m.Height = 120, 40
	views, err := ocli.MountPages(m, doc, cfg)
	if err != nil || len(views) != 1 {
		t.Fatalf("MountPages = %v, %v", views, err)
	}
	v := views[0]
	if err := r.OpenPage(v.Page()); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		if err := v.Reload(); err != nil {
			t.Fatal(err)
		}
		if err := ocli.Settle(r, nil); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, item := range v.List("ai/quota").Items() {
			got = append(got, item.ID)
		}
		if !strings.Contains(strings.Join(got, " "), "quota/claude:session") {
			t.Fatalf("round %d: quota list rows = %v; calls %v", round, got, calls)
		}
	}
	n := 0
	for _, c := range calls {
		if c == "quota.list" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("quota.list ran %d times for 2 loads (the page reuses a live list within pages.live-ttl): %v", n, calls)
	}
}
