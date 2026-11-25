package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ofam.dev/oden"
)

// SessionSummary is the lightweight metadata record for one discovered local AI session.
type SessionSummary struct {
	ID        string    `json:"id"`
	Harness   string    `json:"harness"`
	Path      string    `json:"path"`
	SizeBytes int64     `json:"sizeBytes"`
	ModTime   time.Time `json:"modTime"`
	Project   string    `json:"project,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Title     string    `json:"title,omitempty"`
	Preview   string    `json:"preview,omitempty"`
}

// SessionTranscript is the JSON envelope returned by `o-ai session show --format json`.
type SessionTranscript struct {
	ID         string         `json:"id"`
	Harness    string         `json:"harness"`
	Path       string         `json:"path"`
	Project    string         `json:"project,omitempty"`
	Cwd        string         `json:"cwd,omitempty"`
	Title      string         `json:"title,omitempty"`
	EventCount int            `json:"eventCount"`
	Events     []SessionEvent `json:"events"`
}

type sessionListOpts struct {
	Home    string
	Harness string
	Limit   int
	Since   time.Duration
	Now     time.Time
}

type sessionShowOpts struct {
	IncludeThinking bool
	Tools           string // "all", "none", or regex/substring pattern
	Format          string // "json", "jsonl", "md"
}

// --- CLI command wiring ----------------------------------------------------------

func (t *Tool) cmdSession() oden.Command {
	listParams := []oden.Parameter{
		{Name: "harness", Type: "string", Description: "Filter to one harness (claude, omp, agy, codex, pi, opencode, matomeru)"},
		{Name: "limit", Type: "int", Description: "Maximum number of sessions to return (0 = all)"},
		{Name: "since", Type: "string", Description: "Only sessions modified within this duration (e.g. 24h, 7d, 30m)"},
		{Name: "home", Type: "string", Description: "Alternate home directory for session discovery"},
		{Name: "format", Type: "string", Default: "json", Enum: []string{"json", "jsonl", "table"}, Description: "Output format: json, jsonl, or table"},
	}
	showParams := []oden.Parameter{
		{Name: "thinking", Type: "bool", Description: "Include assistant thinking/reasoning events (default false)"},
		{Name: "tools", Type: "string", Default: "all", Description: "Tool filter: all, none, or a regex/substring matching toolName"},
		{Name: "format", Type: "string", Default: "json", Enum: []string{"json", "jsonl", "md"}, Description: "Output format: json, jsonl, or md"},
		{Name: "harness", Type: "string", Description: "Hint harness when reading an arbitrary path or narrowing an ID"},
		{Name: "home", Type: "string", Description: "Alternate home directory for ID lookup"},
	}
	mineParams := []oden.Parameter{
		{Name: "harness", Type: "string", Description: "Filter to one harness"},
		{Name: "limit", Type: "int", Description: "Maximum number of candidate windows to emit (0 = all)"},
		{Name: "since", Type: "string", Description: "Only scan sessions modified within this duration (e.g. 24h, 7d)"},
		{Name: "pattern", Type: "string", Description: "Custom regex matching user pushback turns (overrides built-in heuristic)"},
		{Name: "home", Type: "string", Description: "Alternate home directory for session discovery"},
		{Name: "format", Type: "string", Default: "jsonl", Enum: []string{"jsonl", "json"}, Description: "Output format: jsonl or json"},
	}

	return oden.Command{
		Name:    "session",
		Aliases: []string{"sess"},
		Summary: "discover, inspect, and mine local AI harness sessions",
		Children: []oden.Command{
			{
				Name:       "list",
				Summary:    "list discovered local AI session files (newest first)",
				Usage:      "[--harness NAME] [--limit N] [--since DURATION] [--format json|jsonl|table]",
				Parameters: listParams,
				Handle:     t.handleSessionList,
			},
			{
				Name:       "show",
				Summary:    "parse and display one session as normalized canonical events",
				Usage:      "<id-or-path> [--thinking] [--tools all|none|PATTERN] [--format json|jsonl|md]",
				Parameters: showParams,
				Handle:     t.handleSessionShow,
			},
			{
				Name:       "mine",
				Summary:    "extract compact (assistant -> user pushback -> resolution) windows across sessions",
				Usage:      "[--harness NAME] [--limit N] [--since DURATION] [--pattern REGEX] [--format jsonl|json]",
				Parameters: mineParams,
				Handle:     t.handleSessionMine,
			},
		},
	}
}

func (t *Tool) resolveSessionHome(p oden.Parsed) (string, error) {
	home := t.defaultOptions().Home
	if v := p.String("home"); v != "" {
		home = v
	}
	abs, err := filepath.Abs(expandPath(home, t.Home, t.Cwd))
	if err != nil {
		return "", err
	}
	if !isDir(abs) {
		return "", fmt.Errorf("not a readable directory: %s", abs)
	}
	return abs, nil
}

func parseSinceDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	// Support convenient day ("7d") and week ("2w") suffixes in addition to Go durations.
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid --since duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "w"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid --since duration %q", s)
		}
		return time.Duration(n * float64(7*24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid --since duration %q", s)
	}
	return d, nil
}

func (t *Tool) handleSessionList(c *oden.Ctx) error {
	p, err := c.Parse([]oden.Parameter{
		{Name: "harness", Type: "string"},
		{Name: "limit", Type: "int"},
		{Name: "since", Type: "string"},
		{Name: "home", Type: "string"},
		{Name: "format", Type: "string", Default: "json", Enum: []string{"json", "jsonl", "table"}},
	})
	if err != nil {
		return err
	}
	home, err := t.resolveSessionHome(p)
	if err != nil {
		return err
	}
	since, err := parseSinceDuration(p.String("since"))
	if err != nil {
		return err
	}
	limit := p.Int("limit")
	if limit < 0 {
		return fmt.Errorf("invalid --limit %d (must be >= 0)", limit)
	}
	format, err := formatFromParsed(p)
	if err != nil {
		return err
	}
	sessions, err := discoverSessions(sessionListOpts{
		Home:    home,
		Harness: p.String("harness"),
		Limit:   limit,
		Since:   since,
		Now:     t.clock(),
	})
	if err != nil {
		return err
	}
	return writeRowsOutput(c, "session", sessionRows(sessions), format)
}

func (t *Tool) handleSessionShow(c *oden.Ctx) error {
	p, err := c.Parse([]oden.Parameter{
		{Name: "thinking", Type: "bool"},
		{Name: "tools", Type: "string", Default: "all"},
		{Name: "format", Type: "string", Default: "json", Enum: []string{"json", "jsonl", "md"}},
		{Name: "harness", Type: "string"},
		{Name: "home", Type: "string"},
	})
	if err != nil {
		return err
	}
	if len(p.Positional()) == 0 {
		return fmt.Errorf("session show requires <id-or-path>")
	}
	home, err := t.resolveSessionHome(p)
	if err != nil {
		return err
	}
	format := p.String("format")
	if format == "" {
		format = "json"
	}
	switch format {
	case "json", "jsonl", "md":
	default:
		return fmt.Errorf("unknown --format %q: json, jsonl, or md", format)
	}
	showOpts := sessionShowOpts{
		IncludeThinking: p.Bool("thinking"),
		Tools:           p.String("tools"),
		Format:          format,
	}
	filter, err := compileSessionEventFilter(showOpts)
	if err != nil {
		return err
	}

	summary, adapter, err := resolveSessionTarget(home, t.Cwd, p.Arg(0, ""), p.String("harness"))
	if err != nil {
		return err
	}
	return writeSessionShow(c.Stdout, summary, adapter, filter, format)
}

func (t *Tool) handleSessionMine(c *oden.Ctx) error {
	p, err := c.Parse([]oden.Parameter{
		{Name: "harness", Type: "string"},
		{Name: "limit", Type: "int"},
		{Name: "since", Type: "string"},
		{Name: "pattern", Type: "string"},
		{Name: "home", Type: "string"},
		{Name: "format", Type: "string", Default: "jsonl", Enum: []string{"jsonl", "json"}},
	})
	if err != nil {
		return err
	}
	home, err := t.resolveSessionHome(p)
	if err != nil {
		return err
	}
	since, err := parseSinceDuration(p.String("since"))
	if err != nil {
		return err
	}
	limit := p.Int("limit")
	if limit < 0 {
		return fmt.Errorf("invalid --limit %d (must be >= 0)", limit)
	}
	format := p.String("format")
	if format == "" {
		format = "jsonl"
	}
	switch format {
	case "jsonl", "json":
	default:
		return fmt.Errorf("unknown --format %q: jsonl or json", format)
	}
	return mineSessions(c.Stdout, sessionMineOpts{
		Home:    home,
		Harness: p.String("harness"),
		Limit:   limit,
		Since:   since,
		Pattern: p.String("pattern"),
		Format:  format,
		Now:     t.clock(),
	})
}

// --- Session discovery (stat + sort + bounded header peek) -----------------------

type discoveredFile struct {
	adapter sessionAdapter
	path    string
	relPath string
	size    int64
	modTime time.Time
}

func discoverSessionFiles(opts sessionListOpts) ([]discoveredFile, error) {
	adapters := sessionAdapters
	if opts.Harness != "" {
		a, ok := findSessionAdapter(opts.Harness)
		if !ok {
			return nil, fmt.Errorf("unknown harness %q (supported: %s)", opts.Harness, strings.Join(supportedSessionHarnesses(), ", "))
		}
		adapters = []sessionAdapter{a}
	}

	var cutoff time.Time
	if opts.Since > 0 {
		now := opts.Now
		if now.IsZero() {
			now = time.Now()
		}
		cutoff = now.Add(-opts.Since)
	}

	seen := map[string]bool{}
	var files []discoveredFile
	for _, a := range adapters {
		for _, root := range a.Roots(opts.Home) {
			if !isDir(root) {
				continue
			}
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return nil
				}
				if d.IsDir() {
					// Skip noisy/duplicate subdirectories early without entering them.
					if a.Harness == "agy" && d.Name() == "chunks" {
						return filepath.SkipDir
					}
					if a.Harness == "matomeru" && rel == "backups" {
						return filepath.SkipDir
					}
					return nil
				}
				if !a.Match(rel, path, d) {
					return nil
				}
				clean := filepath.Clean(path)
				if seen[clean] {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				if !cutoff.IsZero() && info.ModTime().Before(cutoff) {
					return nil
				}
				seen[clean] = true
				files = append(files, discoveredFile{
					adapter: a,
					path:    clean,
					relPath: rel,
					size:    info.Size(),
					modTime: info.ModTime().UTC(),
				})
				return nil
			})
		}
	}

	// Deterministic newest-first ordering; break ties by path.
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.After(files[j].modTime)
		}
		return files[i].path < files[j].path
	})
	return files, nil
}

func discoverSessions(opts sessionListOpts) ([]SessionSummary, error) {
	files, err := discoverSessionFiles(opts)
	if err != nil {
		return nil, err
	}
	if opts.Limit > 0 && len(files) > opts.Limit {
		files = files[:opts.Limit]
	}

	out := make([]SessionSummary, 0, len(files))
	for _, f := range files {
		meta, _ := f.adapter.Peek(f.path)
		sid := meta.SessionID
		if sid == "" {
			sid = f.adapter.SessionID(f.relPath, f.path)
		}
		out = append(out, SessionSummary{
			ID:        f.adapter.Harness + ":" + sid,
			Harness:   f.adapter.Harness,
			Path:      f.path,
			SizeBytes: f.size,
			ModTime:   f.modTime,
			Project:   meta.Project,
			Cwd:       meta.Cwd,
			Title:     meta.Title,
			Preview:   meta.Preview,
		})
	}
	return out, nil
}

func sessionRows(sessions []SessionSummary) []Row {
	rows := make([]Row, 0, len(sessions))
	for _, s := range sessions {
		name := s.Title
		if name == "" {
			name = s.Preview
		}
		if name == "" {
			name = s.ID
		}
		rows = append(rows, Row{
			ID:    "session/" + s.ID,
			Label: cut(name) + " [" + s.Harness + "]",
			Data:  s,
			Cells: cellsOf("session", s),
		})
	}
	return rows
}

func resolveSessionTarget(home, cwd, target, harnessHint string) (SessionSummary, sessionAdapter, error) {
	// 1. Direct file path?
	candidatePath := expandPath(target, home, cwd)
	if info, err := os.Stat(candidatePath); err == nil && !info.IsDir() {
		abs, err := filepath.Abs(candidatePath)
		if err != nil {
			abs = candidatePath
		}
		adapter, err := detectSessionAdapter(abs, harnessHint)
		if err != nil {
			return SessionSummary{}, sessionAdapter{}, err
		}
		meta, _ := adapter.Peek(abs)
		sid := meta.SessionID
		if sid == "" {
			sid = adapter.SessionID(filepath.Base(abs), abs)
		}
		return SessionSummary{
			ID:        adapter.Harness + ":" + sid,
			Harness:   adapter.Harness,
			Path:      abs,
			SizeBytes: info.Size(),
			ModTime:   info.ModTime().UTC(),
			Project:   meta.Project,
			Cwd:       meta.Cwd,
			Title:     meta.Title,
			Preview:   meta.Preview,
		}, adapter, nil
	}

	// 2. Session ID lookup across discovered files.
	files, err := discoverSessionFiles(sessionListOpts{Home: home, Harness: harnessHint})
	if err != nil {
		return SessionSummary{}, sessionAdapter{}, err
	}
	want := strings.TrimPrefix(target, "session/")
	var exact, partial []discoveredFile
	for _, f := range files {
		fileSID := f.adapter.SessionID(f.relPath, f.path)
		fullID := f.adapter.Harness + ":" + fileSID
		switch {
		case fullID == want || fileSID == want || filepath.Base(f.path) == want:
			exact = append(exact, f)
		case strings.HasPrefix(fullID, want) || strings.HasPrefix(fileSID, want):
			partial = append(partial, f)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	if len(matches) == 0 {
		// Secondary fallback: peek files when an embedded sessionId differs from filename.
		for _, f := range files {
			meta, _ := f.adapter.Peek(f.path)
			if meta.SessionID == "" {
				continue
			}
			fullID := f.adapter.Harness + ":" + meta.SessionID
			if fullID == want || meta.SessionID == want {
				matches = append(matches, f)
			}
		}
	}
	if len(matches) == 0 {
		return SessionSummary{}, sessionAdapter{}, fmt.Errorf("no session matching %q", target)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, min(len(matches), 5))
		for _, m := range matches[:min(len(matches), 5)] {
			ids = append(ids, m.adapter.Harness+":"+m.adapter.SessionID(m.relPath, m.path))
		}
		return SessionSummary{}, sessionAdapter{}, fmt.Errorf("ambiguous session %q; matches: %s", target, strings.Join(ids, ", "))
	}
	f := matches[0]
	meta, _ := f.adapter.Peek(f.path)
	sid := meta.SessionID
	if sid == "" {
		sid = f.adapter.SessionID(f.relPath, f.path)
	}
	return SessionSummary{
		ID:        f.adapter.Harness + ":" + sid,
		Harness:   f.adapter.Harness,
		Path:      f.path,
		SizeBytes: f.size,
		ModTime:   f.modTime,
		Project:   meta.Project,
		Cwd:       meta.Cwd,
		Title:     meta.Title,
		Preview:   meta.Preview,
	}, f.adapter, nil
}

// --- Filtering and Show output ---------------------------------------------------

type sessionEventFilter struct {
	includeThinking bool
	toolsMode       string // "all", "none", "pattern"
	toolRegex       *regexp.Regexp
}

func compileSessionEventFilter(opts sessionShowOpts) (sessionEventFilter, error) {
	f := sessionEventFilter{includeThinking: opts.IncludeThinking, toolsMode: "all"}
	tools := strings.TrimSpace(opts.Tools)
	switch strings.ToLower(tools) {
	case "", "all":
		f.toolsMode = "all"
	case "none":
		f.toolsMode = "none"
	default:
		re, err := regexp.Compile("(?i)" + tools)
		if err != nil {
			return f, fmt.Errorf("invalid --tools pattern %q: %w", tools, err)
		}
		f.toolsMode = "pattern"
		f.toolRegex = re
	}
	return f, nil
}

func (f sessionEventFilter) keep(ev SessionEvent) (SessionEvent, bool) {
	if ev.Kind == "thinking" {
		if !f.includeThinking {
			return SessionEvent{}, false
		}
		return ev, true
	}
	if !f.includeThinking {
		ev.Thinking = ""
	}
	if ev.Kind == "tool_call" || ev.Kind == "tool_result" {
		switch f.toolsMode {
		case "none":
			return SessionEvent{}, false
		case "pattern":
			if !f.toolRegex.MatchString(ev.ToolName) {
				return SessionEvent{}, false
			}
		}
	}
	return ev, true
}

func writeSessionShow(w io.Writer, summary SessionSummary, adapter sessionAdapter, filter sessionEventFilter, format string) error {
	switch format {
	case "jsonl":
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		var encErr error
		err := adapter.Parse(adapter.Harness, summary.Path, func(ev SessionEvent) bool {
			filtered, ok := filter.keep(ev)
			if !ok {
				return true
			}
			if encErr = enc.Encode(filtered); encErr != nil {
				return false
			}
			return true
		})
		if encErr != nil {
			return encErr
		}
		return err

	case "md":
		title := summary.Title
		if title == "" {
			title = summary.ID
		}
		if _, err := fmt.Fprintf(w, "# %s\n\n", oneLine(title)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "- **Session**: `%s`\n- **Harness**: `%s`\n", summary.ID, summary.Harness); err != nil {
			return err
		}
		if summary.Cwd != "" {
			if _, err := fmt.Fprintf(w, "- **CWD**: `%s`\n", summary.Cwd); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "- **Path**: `%s`\n\n", summary.Path); err != nil {
			return err
		}
		var writeErr error
		err := adapter.Parse(adapter.Harness, summary.Path, func(ev SessionEvent) bool {
			filtered, ok := filter.keep(ev)
			if !ok {
				return true
			}
			if writeErr = writeMarkdownEvent(w, filtered); writeErr != nil {
				return false
			}
			return true
		})
		if writeErr != nil {
			return writeErr
		}
		return err

	default: // "json"
		events := []SessionEvent{}
		err := adapter.Parse(adapter.Harness, summary.Path, func(ev SessionEvent) bool {
			if filtered, ok := filter.keep(ev); ok {
				events = append(events, filtered)
			}
			return true
		})
		if err != nil {
			return err
		}
		doc := SessionTranscript{
			ID:         summary.ID,
			Harness:    summary.Harness,
			Path:       summary.Path,
			Project:    summary.Project,
			Cwd:        summary.Cwd,
			Title:      summary.Title,
			EventCount: len(events),
			Events:     events,
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
}

func writeMarkdownEvent(w io.Writer, ev SessionEvent) error {
	header := fmt.Sprintf("### [%d] %s", ev.Index, titleCase(ev.Role))
	if ev.Kind != "" && ev.Kind != "message" {
		header += " (" + ev.Kind + ")"
	}
	if ev.Timestamp != "" {
		header += " — " + ev.Timestamp
	}
	if _, err := fmt.Fprintf(w, "%s\n\n", header); err != nil {
		return err
	}
	switch ev.Kind {
	case "thinking":
		for _, line := range strings.Split(strings.TrimSpace(ev.Thinking), "\n") {
			if _, err := fmt.Fprintf(w, "> %s\n", line); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintln(w)
		return err
	case "tool_call":
		if _, err := fmt.Fprintf(w, "**Tool Call**: `%s`\n", ev.ToolName); err != nil {
			return err
		}
		if ev.ToolInput != nil {
			b, err := json.MarshalIndent(ev.ToolInput, "", "  ")
			if err == nil {
				if _, err := fmt.Fprintf(w, "```json\n%s\n```\n\n", b); err != nil {
					return err
				}
				return nil
			}
		}
		_, err := fmt.Fprintln(w)
		return err
	case "tool_result":
		if _, err := fmt.Fprintf(w, "**Tool Output**: `%s`\n```\n%s\n```\n\n", ev.ToolName, strings.TrimRight(ev.ToolOutput, "\n")); err != nil {
			return err
		}
		return nil
	default:
		_, err := fmt.Fprintf(w, "%s\n\n", strings.TrimSpace(ev.Text))
		return err
	}
}

func titleCase(s string) string {
	if s == "" {
		return "Event"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
