package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/oden"
)

func TestContextualHelpDoesNotCollect(t *testing.T) {
	for _, args := range [][]string{
		{"model"}, {"model", "--help"}, {"mo", "--help"},
		{"provider"}, {"provider", "--help"}, {"model", "list", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, errOut strings.Builder
			tool := &Tool{
				Stdout: &out, Stderr: &errOut, Prog: "o-ai",
				collect: func(Options, Need) Inventory {
					t.Fatal("help must not collect inventory")
					return Inventory{}
				},
			}
			if err := tool.Main(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			combined := out.String() + errOut.String()
			for _, want := range []string{"o-ai", "list"} {
				if !strings.Contains(combined, want) {
					t.Errorf("help missing %q:\n%s", want, combined)
				}
			}
		})
	}
}

func TestV2CLIProtocolAndSmoke(t *testing.T) {
	out, _, err := runCLI(t, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	var doc oden.ExtManifest
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Name != "o-ai" || len(doc.Schemas) < 8 {
		t.Fatalf("unexpected manifest: %+v", doc)
	}

	out, _, err = runCLI(t, "capabilities", "--json")
	if err != nil || !strings.Contains(out, `"o-ai"`) {
		t.Fatalf("capabilities: %v\n%s", err, out)
	}

	out, _, err = runCLI(t, "binding", "check")
	if err != nil || !strings.Contains(out, "0 errors") {
		t.Fatalf("binding check: %v\n%s", err, out)
	}

	out, _, err = runCLI(t, "inspect", "actions")
	if err != nil || !strings.Contains(out, "inventory.refresh") {
		t.Fatalf("inspect actions: %v\n%s", err, out)
	}

	out, _, err = runCLI(t, "--version")
	if err != nil || !strings.Contains(out, Version) {
		t.Fatalf("version: %v\n%s", err, out)
	}

	out, _, err = runCLI(t, "--smoke")
	if err != nil || !strings.Contains(out, "smoke ok") {
		t.Fatalf("smoke: %v\n%s", err, out)
	}
}

func TestSkillRowsShowByTargetAndName(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".claude", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: Claude's review.\n---\nclaude body\n")
	put(t, filepath.Join(home, ".omp", "agent", "skills", "review", "SKILL.md"), "---\nname: review\ndescription: OMP's review.\n---\nomp body\n")
	put(t, filepath.Join(home, ".claude", "skills", "broken", "SKILL.md"), "---\nname: [unclosed\n---\nbody\n")
	flags := []string{"--home", home, "--project", home, "--no-probe"}
	out, _, err := runCLI(t, append([]string{"skills"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	rows, data := rowsOf[Resource](t, out)
	var raw []struct{ Data map[string]any }
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatal(err)
	}
	for _, r := range raw {
		if _, ok := r.Data["content"]; ok {
			t.Fatalf("a skill row carries content: %v", r.Data)
		}
	}
	var reviews []Row
	broken := false
	for i, r := range rows {
		if !strings.HasPrefix(r.ID, "skill/skill:") {
			t.Errorf("row id %q", r.ID)
		}
		if data[i].Name == "review" {
			reviews = append(reviews, r)
		}
		if strings.Contains(data[i].Path, "broken") {
			broken = true
		}
	}
	if len(reviews) < 2 || !broken {
		t.Fatalf("want both review copies and the broken skill listed: %s", out)
	}
	out, _, err = runCLI(t, append([]string{"skill", "show", "--target", reviews[1].ID}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	var shown Resource
	if err := json.Unmarshal([]byte(out), &shown); err != nil {
		t.Fatal(err)
	}
	if "skill/"+shown.ID != reviews[1].ID || shown.Content == "" {
		t.Fatalf("show --target %s = %s", reviews[1].ID, out)
	}
	if _, _, err := runCLI(t, append([]string{"skill", "show", "review"}, flags...)...); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("a name with two copies must be ambiguous, got %v", err)
	}
	out, _, err = runCLI(t, append([]string{"sk", "sh", "review", "--harness", "claude"}, flags...)...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude body") {
		t.Fatalf("show by name: %s", out)
	}
}

func TestStatusRows(t *testing.T) {
	home := isolatedHome(t)
	put(t, filepath.Join(home, ".claude", "skills", "one", "SKILL.md"), "---\nname: one\ndescription: d\n---\n")
	out, _, err := runCLI(t, "inventory", "list", "--home", home, "--project", home, "--no-probe")
	if err != nil {
		t.Fatal(err)
	}
	rows, counts := rowsOf[KindCount](t, out)
	if len(rows) != 8 || rows[3].ID != "inventory/skills" || counts[3].Count < 1 {
		t.Fatalf("status rows: %s", out)
	}
}

// A state row's data keeps the noun's brief keys, a description on one
// line cut to briefText characters; a noun with no brief keys keeps its
// lean record.
func TestBriefKeepsTheNounsKeys(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got, err := brief("skill", Resource{ID: "skill:claude:/p", Kind: "skill", Name: "n", Description: "two\nlines " + long, Path: "/p", Harnesses: []string{"claude"}, Scope: "user", Access: "a", Content: "body", Metadata: map[string]any{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	desc, _ := m["description"].(string)
	if len(m) != 5 || m["name"] != "n" || m["path"] != "/p" || m["scope"] != "user" || m["content"] != nil || m["access"] != nil {
		t.Fatalf("brief skill %s", b)
	}
	if !strings.HasPrefix(desc, "two lines word") || !strings.HasSuffix(desc, "…") || len([]rune(desc)) != briefText {
		t.Fatalf("brief description %q (%d runes)", desc, len([]rune(desc)))
	}
	if got, _ := brief("inventory", KindCount{"skills", 2}); got.(KindCount).Count != 2 {
		t.Fatalf("brief status %+v", got)
	}
}
