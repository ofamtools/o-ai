package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

// RunCLI builds the uniform Ochazuke v2 / Oden v2 CLI app and executes args.
func (t *Tool) RunCLI(ctx context.Context, args []string) error {
	app := t.newOdenApp(args)
	return app.Run(ctx)
}

func (t *Tool) newOdenApp(args []string) *oden.App {
	info := cli.AppInfo{
		Name:         "o-ai",
		Version:      Version,
		Summary:      "Local AI harness, provider, model, skill, agent, MCP, and quota inventory",
		Modes:        func() []core.Mode { return modes },
		Bindings:     func() []core.Binding { return defaultBindings() },
		Specs:        func() []core.CommandSpec { return commandSpecs() },
		ExportConfig: t.exportConfig,
		RunTUI:       t.runTUIFromCLI,
		RunSmoke:     runSmoke,
		Manifest:     aiManifest(),
		Capabilities: &oden.Capabilities{
			Name:         "o-ai",
			Version:      Version,
			Summary:      "Local AI harness, provider, model, skill, agent, MCP, and quota inventory",
			Capabilities: []string{"query", "catalog", "tui"},
		},
		Query: t.aiQuery,
	}

	opts := []oden.Option{
		oden.WithStdio(t.Stdin, t.Stdout, t.Stderr),
	}
	if len(t.Env) > 0 {
		opts = append(opts, oden.WithEnv(t.Env))
	}
	app := cli.NewApp(info, args, opts...)

	// Register category nouns (harness, provider, model, thinking, skill, agent, setting, mcp, mcp-tool).
	for _, noun := range categories {
		app.Add(t.cmdNoun(noun))
	}
	// Register inventory, quota, session, list, collect, cache, tui.
	app.Add(
		t.cmdInventory(),
		t.cmdQuota(),
		t.cmdSession(),
		t.cmdList(),
		t.cmdCollect(),
		t.cmdCache(),
		t.cmdTUI(),
	)

	// Wrap Default so plural noun shorthands (e.g. "skills", "models", "providers li")
	// dispatch cleanly while preserving TUI launch on empty/flag args and oden.ErrNotFound
	// for unknown words ("did you mean: ...").
	baseDefault := app.Default
	app.Default = func(ctx context.Context, rootArgs []string) error {
		if len(rootArgs) > 0 {
			if noun := plural(rootArgs[0]); noun != "" {
				rest := rootArgs[1:]
				if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
					rest = append([]string{"list"}, rest...)
				}
				return t.RunCLI(ctx, append([]string{noun}, rest...))
			}
		}
		return baseDefault(ctx, rootArgs)
	}

	app.HelpText = `o-ai — local AI harness, provider, model, skill, agent, MCP, and quota inventory

Usage:
  o-ai [tui] [--no-probe] [--fresh] [--smoke]
  o-ai NOUN list [--harness H] [--provider P] [--model M] [--search TEXT] [--format json|jsonl|table]
  o-ai NOUN show ENTRY | --target ROW_ID
  o-ai model select ENTRY
  o-ai mcp tools [SERVER]
  o-ai inventory list | show | lists
  o-ai quota list | show | refresh [claude|codex]
  o-ai session list | show | mine [--harness H] [--limit N] [--since DURATION]
  o-ai cache path | status | clear
  o-ai NOUNs                         (shorthand for NOUN list, e.g. o-ai skills, o-ai models, o-ai sessions)

Nouns:
  harness, provider, model (mo), thinking, skill, agent, setting, mcp, mcp-tool, inventory, quota, session (sess)
`
	return app
}

// --- extension protocol: manifest + query + config export -------------------------

func aiManifest() *oden.ExtManifest {
	return &oden.ExtManifest{
		Name:    "o-ai",
		Version: Version,
		Schemas: []oden.ExtSchema{
			{
				Kind:           "ai.harness",
				Title:          "Harness",
				Plural:         "Harnesses",
				DefaultColumns: []string{"id", "installed", "configured", "processStatus", "catalogStatus", "executable"},
				SummaryField:   "id",
				UniqueFields:   []string{"id"},
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "Harness identifier (claude, codex, omp, pi, opencode, gemini, clawed, agy)"},
					{Name: "installed", Type: "bool", Description: "Whether executable was found on PATH"},
					{Name: "configured", Type: "bool", Description: "Whether configuration files exist"},
					{Name: "processStatus", Type: "string", Description: "Running process status"},
					{Name: "catalogStatus", Type: "string", Description: "Model catalog probe status"},
					{Name: "executable", Type: "string", Description: "Resolved executable path"},
				},
			},
			{
				Kind:           "ai.provider",
				Title:          "Provider",
				Plural:         "Providers",
				DefaultColumns: []string{"slug", "harness", "models", "access"},
				SummaryField:   "slug",
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "Provider record ID"},
					{Name: "harness", Type: "string", Description: "Owning harness"},
					{Name: "slug", Type: "string", Description: "Provider slug"},
					{Name: "models", Type: "int", Description: "Number of models"},
					{Name: "access", Type: "string", Description: "Access mode"},
				},
			},
			{
				Kind:           "ai.model",
				Title:          "Model",
				Plural:         "Models",
				DefaultColumns: []string{"name", "harness", "provider", "cliSlug", "access"},
				SummaryField:   "name",
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "Model record ID"},
					{Name: "harness", Type: "string", Description: "Owning harness"},
					{Name: "provider", Type: "string", Description: "Provider slug"},
					{Name: "name", Type: "string", Description: "Display name"},
					{Name: "cliSlug", Type: "string", Description: "CLI model identifier"},
					{Name: "access", Type: "string", Description: "Access mode"},
				},
			},
			{
				Kind:           "ai.skill",
				Title:          "Skill",
				Plural:         "Skills",
				DefaultColumns: []string{"name", "harnesses", "scope", "source", "description"},
				SummaryField:   "name",
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "Skill record ID"},
					{Name: "name", Type: "string", Description: "Skill name"},
					{Name: "harnesses", Type: "list", Description: "Harnesses that discover this skill"},
					{Name: "scope", Type: "string", Description: "user, project, or source"},
					{Name: "source", Type: "string", Description: "Skill source name"},
					{Name: "path", Type: "string", Description: "Skill file path"},
					{Name: "description", Type: "string", Description: "Skill description"},
				},
			},
			{
				Kind:           "ai.agent",
				Title:          "Agent",
				Plural:         "Agents",
				DefaultColumns: []string{"name", "harnesses", "scope", "description"},
				SummaryField:   "name",
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "Agent record ID"},
					{Name: "name", Type: "string", Description: "Agent name"},
					{Name: "harnesses", Type: "list", Description: "Harnesses that discover this agent"},
					{Name: "scope", Type: "string", Description: "Scope"},
					{Name: "path", Type: "string", Description: "Agent file path"},
					{Name: "description", Type: "string", Description: "Agent description"},
				},
			},
			{
				Kind:           "ai.mcp",
				Title:          "MCP Server",
				Plural:         "MCP Servers",
				DefaultColumns: []string{"name", "harness", "scope", "transport", "command", "disabled"},
				SummaryField:   "name",
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "MCP server record ID"},
					{Name: "name", Type: "string", Description: "Server name"},
					{Name: "harness", Type: "string", Description: "Owning harness"},
					{Name: "scope", Type: "string", Description: "user, local, or project"},
					{Name: "transport", Type: "string", Description: "stdio or http"},
					{Name: "command", Type: "string", Description: "Command or URL"},
					{Name: "disabled", Type: "bool", Description: "Whether disabled"},
				},
			},
			{
				Kind:           "ai.mcp-tool",
				Title:          "MCP Tool",
				Plural:         "MCP Tools",
				DefaultColumns: []string{"name", "server", "harness", "description"},
				SummaryField:   "name",
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "MCP tool record ID"},
					{Name: "name", Type: "string", Description: "Tool name"},
					{Name: "server", Type: "string", Description: "Owning MCP server"},
					{Name: "harness", Type: "string", Description: "Harness that cached this tool"},
					{Name: "description", Type: "string", Description: "Tool description"},
				},
			},
			{
				Kind:           "ai.quota",
				Title:          "Quota",
				Plural:         "Quotas",
				DefaultColumns: []string{"harness", "window", "usedPercent", "remainingPercent", "resetsAt"},
				SummaryField:   "window",
				Fields: []oden.ExtField{
					{Name: "harness", Type: "string", Description: "claude or codex"},
					{Name: "window", Type: "string", Description: "Rate-limit window"},
					{Name: "usedPercent", Type: "number", Description: "Percent used"},
					{Name: "remainingPercent", Type: "number", Description: "Percent remaining"},
					{Name: "resetsAt", Type: "string", Description: "Reset timestamp"},
				},
			},
			{
				Kind:           "ai.session",
				Title:          "Session",
				Plural:         "Sessions",
				DefaultColumns: []string{"harness", "project", "modTime", "sizeBytes", "title"},
				SummaryField:   "title",
				Fields: []oden.ExtField{
					{Name: "id", Type: "string", Required: true, Description: "Session record ID (harness:sessionId)"},
					{Name: "harness", Type: "string", Description: "Owning harness (claude, omp, agy, codex, pi, opencode, matomeru)"},
					{Name: "path", Type: "string", Description: "Local session file path"},
					{Name: "sizeBytes", Type: "int", Description: "Session file size in bytes"},
					{Name: "modTime", Type: "string", Description: "Last modification timestamp (UTC RFC3339)"},
					{Name: "project", Type: "string", Description: "Project name derived from cwd"},
					{Name: "cwd", Type: "string", Description: "Session working directory"},
					{Name: "title", Type: "string", Description: "Session title or first user prompt preview"},
					{Name: "preview", Type: "string", Description: "Short single-line session preview"},
				},
			},
		},
		Commands: []oden.ExtCommand{
			{ID: "ai.harness.list", Description: "List local AI harnesses", Tags: []string{"read"}},
			{ID: "ai.provider.list", Description: "List AI providers", Tags: []string{"read"}},
			{ID: "ai.model.list", Description: "List AI models", Tags: []string{"read"}},
			{ID: "ai.model.select", Description: "Save default model selection", Tags: []string{"write"}},
			{ID: "ai.skill.list", Description: "List discovered skills", Tags: []string{"read"}},
			{ID: "ai.agent.list", Description: "List discovered agents", Tags: []string{"read"}},
			{ID: "ai.setting.list", Description: "List harness settings files", Tags: []string{"read"}},
			{ID: "ai.mcp.list", Description: "List configured MCP servers", Tags: []string{"read"}},
			{ID: "ai.mcp.tools", Description: "List cached MCP tools", Tags: []string{"read"}},
			{ID: "ai.inventory.show", Description: "Show full AI inventory", Tags: []string{"read"}},
			{ID: "ai.quota.list", Description: "List rate-limit quota windows", Tags: []string{"read"}},
			{ID: "ai.session.list", Description: "List local AI session logs", Tags: []string{"read"}},
			{ID: "ai.session.show", Description: "Inspect a normalized AI session transcript", Tags: []string{"read"}},
			{ID: "ai.session.mine", Description: "Mine pushback/resolution windows from local sessions", Tags: []string{"read"}},
		},
	}
}

func (t *Tool) aiQuery(_ context.Context, kind string, args []string) (oden.ExtQueryResult, error) {
	needle := strings.ToLower(strings.TrimSpace(strings.Join(args, " ")))
	o := t.defaultOptions()
	o.NoProbe = true
	o.Search = needle

	norm := strings.TrimPrefix(kind, "ai.")
	if norm == "" || norm == "item" {
		norm = "harness"
	}

	switch norm {
	case "harness", "provider", "model", "thinking", "skill", "agent", "setting", "mcp", "mcp-tool":
		in := t.inventory(o, specs[norm].need)
		entries := in.Entries(norm, o)
		items := make([]oden.ExtRecord, 0, len(entries))
		for _, e := range entries {
			fields := map[string]any{
				"id":      e.ID,
				"name":    e.Name,
				"harness": e.Harness,
				"summary": e.Summary,
			}
			if b, err := brief(norm, e.Value); err == nil {
				if raw, err := json.Marshal(b); err == nil {
					var m map[string]any
					if json.Unmarshal(raw, &m) == nil {
						for k, v := range m {
							fields[k] = v
						}
					}
				}
			}
			items = append(items, oden.ExtRecord{
				Kind:   "ai." + norm,
				ID:     e.ID,
				Fields: fields,
			})
		}
		return oden.ExtQueryResult{Items: items, Total: len(items)}, nil

	case "quota":
		rows, _, err := t.quotaListData(o)
		if err != nil && len(rows) == 0 {
			return oden.ExtQueryResult{}, err
		}
		items := make([]oden.ExtRecord, 0, len(rows))
		for _, r := range rows {
			if needle != "" && !strings.Contains(strings.ToLower(r.ID+" "+r.Label), needle) {
				continue
			}
			fields := map[string]any{"id": r.ID, "label": r.Label}
			if raw, err := json.Marshal(r.Data); err == nil {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil {
					for k, v := range m {
						fields[k] = v
					}
				}
			}
			items = append(items, oden.ExtRecord{
				Kind:   "ai.quota",
				ID:     r.ID,
				Fields: fields,
			})
		}
		return oden.ExtQueryResult{Items: items, Total: len(items)}, nil

	case "session":
		sessions, err := discoverSessions(sessionListOpts{Home: o.Home, Now: t.clock()})
		if err != nil {
			return oden.ExtQueryResult{}, err
		}
		items := make([]oden.ExtRecord, 0, len(sessions))
		for _, s := range sessions {
			hay := strings.ToLower(s.ID + " " + s.Harness + " " + s.Project + " " + s.Cwd + " " + s.Title + " " + s.Preview)
			if needle != "" && !strings.Contains(hay, needle) {
				continue
			}
			items = append(items, oden.ExtRecord{
				Kind: "ai.session",
				ID:   "session/" + s.ID,
				Fields: map[string]any{
					"id":        s.ID,
					"harness":   s.Harness,
					"path":      s.Path,
					"sizeBytes": s.SizeBytes,
					"modTime":   s.ModTime.Format(time.RFC3339),
					"project":   s.Project,
					"cwd":       s.Cwd,
					"title":     s.Title,
					"preview":   s.Preview,
				},
			})
		}
		return oden.ExtQueryResult{Items: items, Total: len(items)}, nil

	default:
		return oden.ExtQueryResult{}, fmt.Errorf("unknown kind %q (expected ai.harness, ai.provider, ai.model, ai.skill, ai.agent, ai.mcp, ai.mcp-tool, ai.quota, or ai.session)", kind)
	}
}

func (t *Tool) exportConfig(jsonl bool) ([]byte, error) {
	o := t.defaultOptions()
	sources, _ := skillSources(o)
	sel, _ := loadSelection(o)
	cfg := map[string]any{
		"name":          "o-ai",
		"version":       Version,
		"cache":         t.cachePath(),
		"quotaCache":    t.quotaCachePath(),
		"sourcesPath":   sourcesPath(o.Home),
		"selectionPath": selectionPath(o),
		"selection":     sel,
		"skillSources":  sources,
		"settings":      defaultSettings,
	}
	if jsonl {
		var b strings.Builder
		for _, k := range []string{"name", "version", "cache", "quotaCache", "sourcesPath", "selectionPath", "selection", "skillSources", "settings"} {
			line, err := json.Marshal(map[string]any{"key": k, "value": cfg[k]})
			if err != nil {
				return nil, err
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		return []byte(b.String()), nil
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// --- shared flag parameters & parsing --------------------------------------------

func collectFlagParams() []oden.Parameter {
	return []oden.Parameter{
		{Name: "project", Type: "string", Description: "Inspect this project's settings and resources (default: cwd)"},
		{Name: "home", Type: "string", Description: "Alternate home for file discovery and child processes"},
		{Name: "timeout", Type: "string", Description: "Per metadata command deadline, a Go duration (default 15s)"},
		{Name: "no-probe", Type: "bool", Description: "Files only; do not run harness metadata commands"},
		{Name: "fresh", Type: "bool", Description: "Collect now even if the inventory cache is young"},
		{Name: "ttl", Type: "string", Description: "How old a cached inventory may be, a Go duration (default 5m; 0s collects)"},
		{Name: "backoff", Type: "string", Description: "After a vendor refusal, how long before asking again (default 1m)"},
		{Name: "format", Type: "string", Default: "json", Enum: []string{"json", "jsonl", "table"}, Description: "Output format: json, jsonl, or table"},
	}
}

func filterFlagParams() []oden.Parameter {
	return []oden.Parameter{
		{Name: "harness", Type: "string", Description: "Exact harness id, e.g. omp, claude, codex"},
		{Name: "provider", Type: "string", Description: "Exact provider slug"},
		{Name: "model", Type: "string", Description: "Filter models and thinking levels by model id, slug or display name"},
		{Name: "search", Type: "string", Description: "Case-insensitive id, name and description search"},
		{Name: "server", Type: "string", Description: "Exact MCP server name"},
	}
}

func targetFlagParams() []oden.Parameter {
	return []oden.Parameter{
		{Name: "target", Type: "string", Description: "Exact row ID (NOUN/ENTRY_ID)"},
	}
}

func allListParams() []oden.Parameter {
	out := collectFlagParams()
	out = append(out, filterFlagParams()...)
	return out
}

func allShowParams() []oden.Parameter {
	out := allListParams()
	out = append(out, targetFlagParams()...)
	return out
}

func (t *Tool) defaultOptions() Options {
	home := t.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	cwd := t.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return Options{
		Home:    home,
		Project: cwd,
		Timeout: 15 * time.Second,
		TTL:     defaultTTL,
		Backoff: time.Minute,
	}
}

func (t *Tool) parseOptions(p oden.Parsed) (Options, string, error) {
	o := t.defaultOptions()
	if v := p.String("home"); v != "" {
		o.Home = v
	}
	if v := p.String("project"); v != "" {
		o.Project = v
	}
	o.Harness = p.String("harness")
	o.Provider = p.String("provider")
	o.Search = p.String("search")
	o.Server = p.String("server")
	o.NoProbe = p.Bool("no-probe")
	o.Fresh = p.Bool("fresh")
	if v := p.String("timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return o, "", fmt.Errorf("invalid positive duration %q", v)
		}
		o.Timeout = d
	}
	if v := p.String("ttl"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return o, "", fmt.Errorf("invalid --ttl %q (a duration, 0s or more)", v)
		}
		o.TTL = d
	}
	if v := p.String("backoff"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return o, "", fmt.Errorf("invalid --backoff %q (a duration, 0s or more)", v)
		}
		o.Backoff = d
	}
	var err error
	if o.Home, err = filepath.Abs(expandPath(o.Home, t.Home, t.Cwd)); err != nil {
		return o, "", err
	}
	if o.Project, err = filepath.Abs(expandPath(o.Project, o.Home, t.Cwd)); err != nil {
		return o, "", err
	}
	for _, path := range []string{o.Home, o.Project} {
		if !isDir(path) {
			return o, "", errors.New("not a readable directory: " + path)
		}
	}
	return o, p.String("model"), nil
}

// formatFromParsed extracts the requested output format.
// o-ai defaults to "json" for list/show commands (preserving v1 JSON compatibility),
// and also supports "jsonl" and "table".
func formatFromParsed(p oden.Parsed) (string, error) {
	// Check if --format was explicitly passed.
	if f := p.String("format"); f != "" {
		switch f {
		case "json", "jsonl", "table":
			return f, nil
		default:
			return "", fmt.Errorf("unknown --format %q: json, jsonl, or table", f)
		}
	}
	return "json", nil
}

func writeRowsOutput(c *oden.Ctx, noun string, rows []Row, format string) error {
	switch format {
	case "jsonl":
		return writeJSON(c.Stdout, lines(noun, rows))
	case "table":
		if tb, ok := tables[noun]; ok {
			headers := make([]string, len(tb.columns))
			for i, col := range tb.columns {
				headers[i] = col.Title
			}
			trows := make([][]string, 0, len(rows))
			for _, r := range rows {
				trows = append(trows, r.Cells)
			}
			return c.WriteTable(headers, trows)
		}
		trows := make([][]string, 0, len(rows))
		for _, r := range rows {
			trows = append(trows, []string{r.ID, r.Label})
		}
		return c.WriteTable([]string{"id", "label"}, trows)
	default:
		enc := json.NewEncoder(c.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(rows)
	}
}

// --- noun commands ---------------------------------------------------------------

func (t *Tool) cmdNoun(noun string) oden.Command {
	children := []oden.Command{
		{
			Name:       "list",
			Summary:    "list " + noun + " rows",
			Usage:      "[--harness H] [--provider P] [--model M] [--search TEXT] [--format json|jsonl|table]",
			Parameters: allListParams(),
			Handle: func(c *oden.Ctx) error {
				return t.handleNounList(c, noun)
			},
		},
		{
			Name:       "show",
			Summary:    "show one " + noun + " record",
			Usage:      "[ENTRY] [--target ROW_ID]",
			Parameters: allShowParams(),
			Handle: func(c *oden.Ctx) error {
				return t.handleNounShow(c, noun, false)
			},
		},
		{
			Name:       "get",
			Summary:    "get one " + noun + " row by row ID",
			Usage:      "<ROW_ID>",
			Parameters: allListParams(),
			Handle: func(c *oden.Ctx) error {
				return t.handleNounGet(c, noun)
			},
		},
	}

	if noun == "model" {
		children = append(children, oden.Command{
			Name:       "select",
			Summary:    "save ENTRY as o-ai's default model selection",
			Usage:      "<ENTRY> [--harness H] [--provider P]",
			Parameters: allShowParams(),
			Handle: func(c *oden.Ctx) error {
				return t.handleNounShow(c, noun, true)
			},
		})
	}
	if noun == "mcp" {
		children = append(children, oden.Command{
			Name:       "tools",
			Summary:    "list cached MCP tools for a server",
			Usage:      "[SERVER] [--server NAME] [--target ROW_ID]",
			Parameters: allShowParams(),
			Handle: func(c *oden.Ctx) error {
				return t.handleMCPTools(c)
			},
		})
	}

	cmd := oden.Command{
		Name:     noun,
		Summary:  "inspect " + noun + " records",
		Children: children,
	}
	if noun == "model" {
		cmd.Aliases = []string{"mo"}
	}
	return cmd
}

func (t *Tool) handleNounList(c *oden.Ctx, noun string) error {
	p, err := c.Parse(allListParams())
	if err != nil {
		return err
	}
	o, model, err := t.parseOptions(p)
	if err != nil {
		return err
	}
	if (noun == "mcp" || noun == "mcp-tool") && o.Server == "" && len(p.Positional()) > 0 {
		o.Server = p.Arg(0, "")
	}
	format, err := formatFromParsed(p)
	if err != nil {
		return err
	}
	rows, err := nounRows(noun, t.inventory(o, specs[noun].need), o, model, lean)
	if err != nil {
		return err
	}
	return writeRowsOutput(c, noun, rows, format)
}

func (t *Tool) handleMCPTools(c *oden.Ctx) error {
	p, err := c.Parse(allShowParams())
	if err != nil {
		return err
	}
	o, model, err := t.parseOptions(p)
	if err != nil {
		return err
	}
	if o.Server == "" {
		if target := p.String("target"); target != "" {
			id := strings.TrimPrefix(target, "mcp/")
			if idx := strings.LastIndex(id, ":"); idx >= 0 {
				o.Server = id[idx+1:]
			} else {
				o.Server = id
			}
		} else if len(p.Positional()) > 0 {
			o.Server = p.Arg(0, "")
		}
	}
	format, err := formatFromParsed(p)
	if err != nil {
		return err
	}
	rows, err := nounRows("mcp-tool", t.inventory(o, specs["mcp-tool"].need), o, model, lean)
	if err != nil {
		return err
	}
	return writeRowsOutput(c, "mcp-tool", rows, format)
}

func (t *Tool) handleNounShow(c *oden.Ctx, noun string, sel bool) error {
	p, err := c.Parse(allShowParams())
	if err != nil {
		return err
	}
	o, model, err := t.parseOptions(p)
	if err != nil {
		return err
	}
	rows := filterModel(t.inventory(o, specs[noun].need).Entries(noun, o), model)

	var row Entry
	if target := p.String("target"); target != "" {
		tNoun, id, ok := strings.Cut(target, "/")
		if !ok || id == "" || tNoun != noun {
			return fmt.Errorf("not a %s row id (%s/ID): %s", noun, noun, target)
		}
		found := false
		for _, e := range rows {
			if e.ID == id {
				row, found = e, true
				break
			}
		}
		if !found {
			return fmt.Errorf("no %s %q with these filters", noun, id)
		}
	} else {
		var query string
		if len(p.Positional()) > 0 {
			query = p.Arg(0, "")
		}
		if query == "" && noun == "model" && !sel {
			if query, err = loadSelection(o); err != nil {
				return err
			}
		}
		if query == "" {
			// If interactive TTY and candidates exist, use c.Resolve!
			if len(rows) > 0 {
				cands := entriesToCandidates(rows)
				cand, rerr := c.Resolve("", cands, noun)
				if rerr == nil {
					row = cand.Data.(Entry)
				} else {
					verb := "show"
					if sel {
						verb = "select"
					}
					return fmt.Errorf("%s %s needs ENTRY or --target ROW_ID", noun, verb)
				}
			} else {
				verb := "show"
				if sel {
					verb = "select"
				}
				return fmt.Errorf("%s %s needs ENTRY or --target ROW_ID", noun, verb)
			}
		} else {
			row, err = selectEntry(rows, query)
			if err != nil {
				return err
			}
		}
	}

	if sel {
		if err := saveSelection(o, row.ID); err != nil {
			return err
		}
	}
	return c.WriteJSON(row.Value)
}

func (t *Tool) handleNounGet(c *oden.Ctx, noun string) error {
	p, err := c.Parse(allListParams())
	if err != nil {
		return err
	}
	if len(p.Positional()) == 0 {
		return fmt.Errorf("%s get requires a ROW_ID", noun)
	}
	o, model, err := t.parseOptions(p)
	if err != nil {
		return err
	}
	wantID := p.Arg(0, "")
	if !strings.Contains(wantID, "/") {
		wantID = noun + "/" + wantID
	}
	rows, err := nounRows(noun, t.inventory(o, specs[noun].need), o, model, lean)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.ID == wantID {
			return c.WriteJSON(r)
		}
	}
	return fmt.Errorf("no %s row %q", noun, wantID)
}

func entriesToCandidates(rows []Entry) []oden.Candidate {
	out := make([]oden.Candidate, 0, len(rows))
	for _, e := range rows {
		var badges []string
		if e.Harness != "" {
			badges = append(badges, e.Harness)
		}
		if e.Provider != "" {
			badges = append(badges, e.Provider)
		}
		out = append(out, oden.Candidate{
			ID:       e.ID,
			Title:    e.Name,
			Subtitle: e.Summary,
			Badges:   badges,
			Data:     e,
		})
	}
	return out
}

// --- inventory, quota, list, collect, cache, tui commands ------------------------

func (t *Tool) cmdInventory() oden.Command {
	return oden.Command{
		Name:    "inventory",
		Summary: "inspect the full AI inventory and summary counts",
		Children: []oden.Command{
			{
				Name:       "list",
				Summary:    "show counts of each inventory kind",
				Usage:      "[--no-probe] [--fresh] [--format json|jsonl|table]",
				Parameters: collectFlagParams(),
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(collectFlagParams())
					if err != nil {
						return err
					}
					o, model, err := t.parseOptions(p)
					if err != nil {
						return err
					}
					format, err := formatFromParsed(p)
					if err != nil {
						return err
					}
					rows, err := nounRows("inventory", t.inventory(o, specs["inventory"].need), o, model, lean)
					if err != nil {
						return err
					}
					return writeRowsOutput(c, "inventory", rows, format)
				},
			},
			{
				Name:       "show",
				Summary:    "show the complete inventory JSON",
				Usage:      "[--no-probe] [--fresh]",
				Parameters: collectFlagParams(),
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(collectFlagParams())
					if err != nil {
						return err
					}
					o, _, err := t.parseOptions(p)
					if err != nil {
						return err
					}
					return c.WriteJSON(t.inventory(o, specs["inventory"].need))
				},
			},
			{
				Name:       "lists",
				Summary:    "emit every list's rows as JSON Lines",
				Usage:      "[--no-probe] [--fresh]",
				Parameters: allListParams(),
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(allListParams())
					if err != nil {
						return err
					}
					o, model, err := t.parseOptions(p)
					if err != nil {
						return err
					}
					rows, err := t.state(o, model)
					if err != nil {
						return err
					}
					return writeJSON(c.Stdout, rows)
				},
			},
		},
	}
}

func (t *Tool) cmdQuota() oden.Command {
	quotaParams := []oden.Parameter{
		{Name: "harness", Type: "string", Description: "Only this harness: claude or codex"},
		{Name: "home", Type: "string", Description: "Alternate home directory"},
		{Name: "timeout", Type: "string", Description: "Usage request deadline (default 15s)"},
		{Name: "fresh", Type: "bool", Description: "Ask the vendor now, bypassing cache"},
		{Name: "ttl", Type: "string", Description: "Cache TTL duration (default 5m; 0s asks vendor)"},
		{Name: "backoff", Type: "string", Description: "Backoff duration after vendor refusal (default 1m)"},
		{Name: "format", Type: "string", Default: "json", Enum: []string{"json", "jsonl", "table"}, Description: "Output format: json, jsonl, or table"},
	}
	quotaShowParams := append(append([]oden.Parameter{}, quotaParams...), targetFlagParams()...)

	runList := func(c *oden.Ctx, forceFresh bool) error {
		p, err := c.Parse(quotaParams)
		if err != nil {
			return err
		}
		o, _, err := t.parseOptions(p)
		if err != nil {
			return err
		}
		if forceFresh {
			o.Fresh = true
		}
		if o.Harness == "" && len(p.Positional()) > 0 {
			o.Harness = p.Arg(0, "")
		}
		format, err := formatFromParsed(p)
		if err != nil {
			return err
		}
		rows, _, qerr := t.quotaListData(o)
		if len(rows) > 0 {
			if werr := writeRowsOutput(c, "quota", rows, format); werr != nil {
				return werr
			}
		}
		return qerr
	}

	return oden.Command{
		Name:    "quota",
		Summary: "inspect Claude Code and Codex plan quota windows",
		Children: []oden.Command{
			{
				Name:       "list",
				Summary:    "list rate-limit quota windows",
				Usage:      "[claude|codex] [--fresh] [--format json|jsonl|table]",
				Parameters: quotaParams,
				Handle:     func(c *oden.Ctx) error { return runList(c, false) },
			},
			{
				Name:       "refresh",
				Summary:    "fetch quota windows now, bypassing cache",
				Usage:      "[claude|codex] [--format json|jsonl|table]",
				Parameters: quotaParams,
				Handle:     func(c *oden.Ctx) error { return runList(c, true) },
			},
			{
				Name:       "show",
				Summary:    "show a harness's full quota record",
				Usage:      "[claude|codex] [--target ROW_ID]",
				Parameters: quotaShowParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(quotaShowParams)
					if err != nil {
						return err
					}
					o, _, err := t.parseOptions(p)
					if err != nil {
						return err
					}
					harnessName := ""
					if target := p.String("target"); target != "" {
						id := strings.TrimPrefix(target, "quota/")
						harnessName, _, _ = strings.Cut(id, ":")
					}
					if harnessName == "" && len(p.Positional()) > 0 {
						harnessName = p.Arg(0, "")
					}
					val, err := t.quotaShowData(o, harnessName)
					if err != nil {
						return err
					}
					return c.WriteJSON(val)
				},
			},
		},
	}
}

func (t *Tool) cmdList() oden.Command {
	return oden.Command{
		Name:       "list",
		Aliases:    []string{"ls"},
		Summary:    "show inventory summary counts",
		Usage:      "[--no-probe] [--fresh] [--format json|jsonl|table]",
		Parameters: collectFlagParams(),
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(collectFlagParams())
			if err != nil {
				return err
			}
			o, model, err := t.parseOptions(p)
			if err != nil {
				return err
			}
			format, err := formatFromParsed(p)
			if err != nil {
				return err
			}
			rows, err := nounRows("inventory", t.inventory(o, specs["inventory"].need), o, model, lean)
			if err != nil {
				return err
			}
			return writeRowsOutput(c, "inventory", rows, format)
		},
	}
}

func (t *Tool) cmdCollect() oden.Command {
	return oden.Command{
		Name:       "collect",
		Summary:    "collect and print the full AI inventory",
		Usage:      "[--no-probe] [--fresh]",
		Parameters: collectFlagParams(),
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(collectFlagParams())
			if err != nil {
				return err
			}
			o, _, err := t.parseOptions(p)
			if err != nil {
				return err
			}
			return c.WriteJSON(t.inventory(o, specs["inventory"].need))
		},
	}
}

func (t *Tool) cmdCache() oden.Command {
	return oden.Command{
		Name:    "cache",
		Summary: "inspect or clear the o-ai inventory and quota cache",
		Children: []oden.Command{
			{
				Name:    "path",
				Summary: "print cache file paths",
				Usage:   "[--json]",
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(nil)
					if err != nil {
						return err
					}
					paths := map[string]string{
						"inventory": t.cachePath(),
						"inputs":    inputsPath(t.cachePath()),
						"quota":     t.quotaCachePath(),
					}
					if p.Format() == "json" {
						return c.WriteJSON(paths)
					}
					return c.Printf("%s\n", t.cachePath())
				},
			},
			{
				Name:    "status",
				Summary: "show cache file sizes and entry counts",
				Usage:   "[--json]",
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(nil)
					if err != nil {
						return err
					}
					invPath := t.cachePath()
					invFile := readCache(invPath)
					qFile := t.readQuotaCache()
					info := map[string]any{
						"inventoryPath":    invPath,
						"inventoryEntries": len(invFile.Entries),
						"quotaPath":        t.quotaCachePath(),
						"quotaEntries":     len(qFile),
					}
					if p.Format() == "json" {
						return c.WriteJSON(info)
					}
					return c.WriteTable([]string{"cache", "entries", "path"}, [][]string{
						{"inventory", fmt.Sprintf("%d", len(invFile.Entries)), invPath},
						{"quota", fmt.Sprintf("%d", len(qFile)), t.quotaCachePath()},
					})
				},
			},
			{
				Name:    "clear",
				Summary: "remove cached inventory, inputs, and quota files",
				Handle: func(c *oden.Ctx) error {
					if _, err := c.Parse(nil); err != nil {
						return err
					}
					removed := 0
					for _, p := range []string{t.cachePath(), inputsPath(t.cachePath()), t.quotaCachePath()} {
						if err := os.Remove(p); err == nil {
							removed++
						}
					}
					return c.Printf("cleared %d cache file(s)\n", removed)
				},
			},
		},
	}
}

func (t *Tool) cmdTUI() oden.Command {
	return oden.Command{
		Name:    "tui",
		Summary: "launch the interactive Ochazuke v2 TUI",
		Usage:   "[--no-probe] [--fresh] [--view harnesses|models|providers|skills|agents|mcp|quota]",
		Handle: func(c *oden.Ctx) error {
			return t.runTUIFromCLI(c.Context, c.Args)
		},
	}
}

func (t *Tool) runTUIFromCLI(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("o-ai", flag.ContinueOnError)
	fs.SetOutput(t.Stderr)
	noProbe := fs.Bool("no-probe", false, "files only; do not run harness metadata commands")
	fresh := fs.Bool("fresh", false, "collect now even if cache is young")
	view := fs.String("view", viewHarnesses, "initial view: harnesses, models, providers, skills, agents, mcp, quota")
	home := fs.String("home", "", "alternate home directory")
	project := fs.String("project", "", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o := t.defaultOptions()
	o.NoProbe = *noProbe
	o.Fresh = *fresh
	if *home != "" {
		o.Home = *home
	}
	if *project != "" {
		o.Project = *project
	}
	return runTUI(t, UIConfig{
		Options:     o,
		InitialView: *view,
	})
}
