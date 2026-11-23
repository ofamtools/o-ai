package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Version is the application and catalog version.
const Version = "0.2.0"

// Namespace is the singular namespace o-ai declares.
const Namespace = "ai"

// nouns are o-ai's domain nouns: the inventory categories, inventory, and quota.
var nouns = append(append([]string{}, categories...), "inventory", "quota")

// nounSpec describes what a noun's list/state projections omit or keep and
// what costly collection parts they need.
type nounSpec struct {
	omit  []string
	brief []string // a state row's data: these keys of the record; none keeps the lean record
	need  Need
	live  bool // fetched on every call, outside the inventory (quota.go); not in the state
}

var catalogs = Need{Catalogs: true}

// specs are the nouns' needs. Resource and settings lists read files only;
// harness and inventory read everything, processes included.
var specs = map[string]nounSpec{
	"harness":   {omit: []string{"settings"}, brief: []string{"id", "installed", "configured", "executable", "processStatus", "catalogStatus"}, need: needAll},
	"provider":  {omit: []string{"models"}, brief: []string{"harness", "slug", "access"}, need: catalogs},
	"model":     {omit: []string{"metadata"}, brief: []string{"harness", "provider", "name", "cliSlug"}, need: catalogs},
	"thinking":  {brief: []string{"slug", "harness", "provider", "cliSlug"}, need: catalogs},
	"skill":     {omit: []string{"content"}, brief: resourceBrief},
	"agent":     {omit: []string{"content"}, brief: resourceBrief, need: Need{RuntimeAgents: true}},
	"setting":   {brief: []string{"path", "scope", "error"}},
	"mcp":       {brief: []string{"name", "harness", "scope", "transport", "command", "url", "disabled"}},
	"mcp-tool":  {omit: []string{"inputSchema"}, brief: []string{"server", "name", "description", "harness"}},
	"inventory": {need: needAll},
	"quota":     {live: true},
}

var resourceBrief = []string{"name", "description", "path", "harnesses", "scope", "source"}

// briefText is how many characters of a state row's description stay; a
// longer one is cut and ends in "…".
const briefText = 160

// brief is a record as a state row carries it: the noun's brief keys
// alone, a description cut to briefText; a noun with no brief keys gives
// its lean record.
func brief(noun string, v any) (any, error) {
	keep := specs[noun].brief
	if len(keep) == 0 {
		return lean(noun, v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(keep))
	for _, k := range keep {
		if raw, ok := m[k]; ok {
			out[k] = raw
		}
	}
	if raw, ok := out["description"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s = oneLine(s); len([]rune(s)) > briefText {
				s = string([]rune(s)[:briefText-1]) + "…"
			}
			out["description"], _ = json.Marshal(s)
		}
	}
	return out, nil
}

// lean is a record as a list row carries it: its JSON without the noun's
// omit fields, every other key unchanged.
func lean(noun string, v any) (any, error) {
	omit := specs[noun].omit
	if len(omit) == 0 {
		return v, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for _, k := range omit {
		delete(m, k)
	}
	return m, nil
}

// Row is one element of a list.
type Row struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Data  any      `json:"data,omitempty"`
	Cells []string `json:"cells,omitempty"`
}

// LineRow is one JSON Lines element tagged with its noun.
type LineRow struct {
	Noun  string   `json:"noun"`
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Data  any      `json:"data,omitempty"`
	Cells []string `json:"cells,omitempty"`
}

// KindCount is an inventory summary row's data: how many records of a kind.
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// nounRows is one noun's rows from an inventory: the counts, else
// the noun's entries after the model filter, each record as shape gives it
// (lean for a list, brief for the state).
func nounRows(noun string, in Inventory, o Options, model string, shape func(string, any) (any, error)) ([]Row, error) {
	rows := []Row{}
	var err error
	add := func(id, label string, value any) {
		row := Row{ID: noun + "/" + id, Label: label, Cells: cellsOf(noun, value)}
		if err == nil {
			row.Data, err = shape(noun, value)
		}
		rows = append(rows, row)
	}
	if noun == "inventory" {
		for _, c := range kindCounts(in) {
			add(c.Kind, fmt.Sprintf("%s: %d", c.Kind, c.Count), c)
		}
	} else {
		for _, e := range filterModel(in.Entries(noun, o), model) {
			add(e.ID, label(noun, e), e.Value)
		}
	}
	return rows, err
}

// state returns every non-live noun's rows in one collection, as JSON Lines.
func (t *Tool) state(o Options, model string) ([]LineRow, error) {
	in := t.inventory(o, stateNeed())
	var out []LineRow
	for _, noun := range stateNouns() {
		rows, err := nounRows(noun, in, o, model, brief)
		if err != nil {
			return nil, err
		}
		out = append(out, lines(noun, rows)...)
	}
	return out, nil
}

// stateNouns are the nouns the state answers: every noun except live ones.
func stateNouns() []string {
	var out []string
	for _, noun := range nouns {
		if !specs[noun].live {
			out = append(out, noun)
		}
	}
	return out
}

// lines converts rows to JSON Lines tagged with their noun.
func lines(noun string, rows []Row) []LineRow {
	out := make([]LineRow, len(rows))
	for i, r := range rows {
		out[i] = LineRow{Noun: noun, ID: r.ID, Label: r.Label, Data: r.Data, Cells: r.Cells}
	}
	return out
}

// label is a row's label: the name, a thinking level's model and slug, and
// the harness for every kind but harness itself.
func label(noun string, e Entry) string {
	name := e.Name
	if noun == "thinking" {
		name = e.Summary
	}
	if r, ok := e.Value.(Resource); ok && e.Harness == "" && r.Source != "" {
		return oneLine(name) + " [" + r.Source + "]"
	}
	if noun == "harness" || e.Harness == "" {
		return oneLine(name)
	}
	return oneLine(name) + " [" + e.Harness + "]"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func kindCounts(in Inventory) []KindCount {
	return []KindCount{
		{"harnesses", len(in.Harnesses)}, {"providers", len(in.Providers)}, {"models", len(in.Models)},
		{"skills", len(in.Skills)}, {"agents", len(in.Agents)}, {"mcps", len(in.MCPServers)}, {"mcp-tools", len(in.MCPTools)},
		{"warnings", len(in.Warnings)},
	}
}

// inventory is the collection need asks for: this process's, else the
// cache file's (cache.go), else a new one; warnings go to Stderr the first
// time. Processes are inspected afresh on every call that needs them.
func (t *Tool) inventory(o Options, need Need) Inventory {
	if o.NoProbe {
		need.Catalogs, need.RuntimeAgents = false, false // nothing to probe
	}
	key := cacheKey(o)
	e, ok := t.cache[key]
	if !ok || !e.Need.covers(need) {
		e = cacheEntry{Need: need, Inventory: t.cachedInventory(o, key, need)}
		printWarnings(t.Stderr, e.Inventory)
		if t.cache == nil {
			t.cache = map[string]cacheEntry{}
		}
		t.cache[key] = e
	}
	in := e.Inventory
	if need.Processes {
		in.Harnesses = append([]Harness{}, in.Harnesses...)
		in.Warnings = append([]string{}, in.Warnings...)
		for i := range in.Harnesses {
			in.Harnesses[i].PIDs = []int{}
		}
		inspectProcesses(&in, o)
	}
	return in
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
