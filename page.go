package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// Frame, node, and view IDs.
const (
	pageID       = "main"
	nodeGrid     = "grid"
	nodeFilter   = "filter"
	modalPalette = "palette"
	modalHelp    = "help"
	modalForm    = "form"
	detailText   = "detail.text"
	detailTail   = "detail.tail"

	viewHarnesses = "harnesses"
	viewModels    = "models"
	viewProviders = "providers"
	viewSkills    = "skills"
	viewAgents    = "agents"
	viewMCP       = "mcp"
	viewQuota     = "quota"
)

var allViews = []string{
	viewHarnesses,
	viewModels,
	viewProviders,
	viewSkills,
	viewAgents,
	viewMCP,
	viewQuota,
}

var viewSortKeys = map[string][]string{
	viewHarnesses: {"harness", "status", "procs", "catalog"},
	viewModels:    {"model", "harness", "provider", "context"},
	viewProviders: {"provider", "harness", "models", "access"},
	viewSkills:    {"name", "source", "scope"},
	viewAgents:    {"name", "source", "scope"},
	viewMCP:       {"server", "harness", "scope", "transport"},
	viewQuota:     {"harness", "window", "used", "left"},
}

var viewGroupKeys = map[string][]string{
	viewHarnesses: {"", "status", "catalog"},
	viewModels:    {"", "harness", "provider"},
	viewProviders: {"", "harness", "access"},
	viewSkills:    {"", "source", "scope"},
	viewAgents:    {"", "source", "scope"},
	viewMCP:       {"", "harness", "scope", "transport"},
	viewQuota:     {"", "harness"},
}

var densities = []string{ui.DensityBalanced, ui.DensityCompact, ui.DensitySpacious}

var defaultSettings = map[string]any{
	"matchMode":   "substring",
	"detail":      true,
	"detailRatio": 0.56,
	"stackWidth":  96,
	"density":     "balanced",
}

var modes = []core.Mode{
	{Name: "normal"},
	{Name: "input"},
	{Name: "palette"},
	{Name: "help"},
	{Name: "form"},
}

var viewColumns = map[string][]table.Column{
	viewHarnesses: {
		{Key: "harness", Title: "harness"},
		{Key: "status", Title: "status"},
		{Key: "procs", Title: "procs", Align: "right"},
		{Key: "catalog", Title: "catalog"},
		{Key: "executable", Title: "executable"},
	},
	viewModels: {
		{Key: "model", Title: "model"},
		{Key: "harness", Title: "harness"},
		{Key: "provider", Title: "provider"},
		{Key: "context", Title: "context", Align: "right"},
		{Key: "thinking", Title: "thinking"},
	},
	viewProviders: {
		{Key: "provider", Title: "provider"},
		{Key: "harness", Title: "harness"},
		{Key: "models", Title: "models", Align: "right"},
		{Key: "access", Title: "access"},
	},
	viewSkills: {
		{Key: "name", Title: "name"},
		{Key: "source", Title: "source"},
		{Key: "scope", Title: "scope"},
		{Key: "description", Title: "description"},
	},
	viewAgents: {
		{Key: "name", Title: "name"},
		{Key: "source", Title: "source"},
		{Key: "scope", Title: "scope"},
		{Key: "description", Title: "description"},
	},
	viewMCP: {
		{Key: "server", Title: "server"},
		{Key: "harness", Title: "harness"},
		{Key: "scope", Title: "scope"},
		{Key: "transport", Title: "transport"},
		{Key: "command", Title: "command"},
	},
	viewQuota: {
		{Key: "harness", Title: "harness"},
		{Key: "window", Title: "window"},
		{Key: "used", Title: "used", Align: "right"},
		{Key: "left", Title: "left", Align: "right"},
		{Key: "resets", Title: "resets"},
	},
}

// Page is the o-ai TUI state stored by value in the page frame's Data.
type Page struct {
	View string

	Inv           Inventory
	Quotas        []Quota
	QuotaRows     []Row
	SelectedModel string
	Loading       bool

	Grids map[string]table.Model

	Query     string
	QueryLine input.Line
	Typing    bool
	Match     table.MatchMode
	FilterErr string

	SortField map[string]string
	SortDesc  map[string]bool

	Detail    bool
	Ratio     float64
	DetailOff int
	detailKey string

	ThemeIdx int
	Density  string

	Banner, Tone string
}

func (p Page) clone() Page {
	grids := make(map[string]table.Model, len(p.Grids))
	for k, g := range p.Grids {
		grids[k] = g.Copy()
	}
	p.Grids = grids
	p.SortField = maps.Clone(p.SortField)
	p.SortDesc = maps.Clone(p.SortDesc)
	p.QueryLine.Text = slices.Clone(p.QueryLine.Text)
	return p
}

func pageOf(s core.State) Page {
	p, _ := s.Pages[pageID].Data.(Page)
	return p
}

func withPage(s core.State, p Page) core.State {
	f := s.Pages[pageID]
	f.Data = p
	s.Pages = maps.Clone(s.Pages)
	s.Pages[pageID] = f
	return s
}

func (p *Page) note(msg string) { p.Banner, p.Tone = msg, "success" }
func (p *Page) info(msg string) { p.Banner, p.Tone = msg, "info" }
func (p *Page) warn(msg string) { p.Banner, p.Tone = msg, "warning" }
func (p *Page) fail(msg string) { p.Banner, p.Tone = msg, "error" }

func (p *Page) curGrid() table.Model {
	if g, ok := p.Grids[p.View]; ok {
		return g
	}
	return *table.New(viewColumns[p.View], nil)
}

func (p *Page) setCurGrid(g table.Model) {
	if p.Grids == nil {
		p.Grids = map[string]table.Model{}
	}
	p.Grids[p.View] = g
}

func (p *Page) focusedID() (string, bool) {
	g := p.curGrid()
	r, ok := g.FocusedRow()
	return r.ID, ok
}

func focusGridRow(g *table.Model, id string) bool {
	for i, it := range g.Items() {
		if !it.Header && it.Row.ID == id {
			g.SelectedIdx = i
			g.Focused()
			return true
		}
	}
	return false
}

func (p *Page) reproject() {
	for _, v := range allViews {
		p.rebuildView(v)
	}
}

func (p *Page) rebuildView(view string) {
	g, ok := p.Grids[view]
	if !ok {
		g = *table.New(viewColumns[view], nil)
	}
	keepRow, hasKeep := g.FocusedRow()
	cols := viewColumns[view]
	base := p.rowsForView(view)

	tmp := table.Model{Columns: cols, Rows: base, Query: p.Query, MatchMode: p.Match}
	rows := tmp.Filtered()
	if p.View == view {
		p.FilterErr = tmp.FilterErr
	}
	if tmp.FilterErr != "" {
		rows = nil
	}
	rows = slices.Clone(rows)
	sortKey := p.SortField[view]
	if sortKey == "" && len(viewSortKeys[view]) > 0 {
		sortKey = viewSortKeys[view][0]
	}
	desc := p.SortDesc[view]
	slices.SortStableFunc(rows, func(a, b table.Row) int {
		c := compareRow(a, b, sortKey)
		if desc {
			return -c
		}
		return c
	})
	g.Columns = cols
	g.Rows = rows
	if hasKeep {
		focusGridRow(&g, keepRow.ID)
	}
	g.Focused()
	if p.Grids == nil {
		p.Grids = map[string]table.Model{}
	}
	p.Grids[view] = g
}

func compareRow(a, b table.Row, key string) int {
	switch key {
	case "procs", "models", "context":
		na, _ := a.Cells[key+"_num"].(int64)
		nb, _ := b.Cells[key+"_num"].(int64)
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	case "used", "left":
		fa, _ := a.Cells[key+"_num"].(float64)
		fb, _ := b.Cells[key+"_num"].(float64)
		if fa < fb {
			return -1
		}
		if fa > fb {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	default:
		sa := strings.ToLower(table.Text(a.Cells[key]))
		sb := strings.ToLower(table.Text(b.Cells[key]))
		if sa != sb {
			return strings.Compare(sa, sb)
		}
		return strings.Compare(a.ID, b.ID)
	}
}

func (p *Page) rowsForView(view string) []table.Row {
	switch view {
	case viewHarnesses:
		rows := make([]table.Row, 0, len(p.Inv.Harnesses))
		for _, h := range p.Inv.Harnesses {
			state := "missing"
			switch {
			case h.Installed:
				state = "installed"
			case h.Configured:
				state = "configured"
			}
			rows = append(rows, table.Row{
				ID:  h.ID,
				Ref: &core.Ref{Kind: "ai.harness", ID: h.ID},
				Cells: map[string]any{
					"harness":    h.ID,
					"status":     state,
					"procs":      strconv.Itoa(len(h.PIDs)),
					"procs_num":  int64(len(h.PIDs)),
					"catalog":    h.CatalogStatus,
					"executable": h.Executable,
				},
			})
		}
		return rows

	case viewModels:
		rows := make([]table.Row, 0, len(p.Inv.Models))
		for _, m := range p.Inv.Models {
			levels := make([]string, len(m.Thinking))
			for i, t := range m.Thinking {
				levels[i] = t.Slug
			}
			ctxWin := contextWindow(m.Metadata)
			name := oneLine(m.Name)
			if p.SelectedModel == m.ID {
				name = "★ " + name
			}
			rows = append(rows, table.Row{
				ID:  m.ID,
				Ref: &core.Ref{Kind: "ai.model", ID: m.ID},
				Cells: map[string]any{
					"model":       name,
					"harness":     m.Harness,
					"provider":    m.Provider,
					"context":     tokens(ctxWin),
					"context_num": ctxWin,
					"thinking":    strings.Join(levels, ","),
					"cliSlug":     m.CLISlug,
				},
			})
		}
		return rows

	case viewProviders:
		rows := make([]table.Row, 0, len(p.Inv.Providers))
		for _, pr := range p.Inv.Providers {
			rows = append(rows, table.Row{
				ID:  pr.ID,
				Ref: &core.Ref{Kind: "ai.provider", ID: pr.ID},
				Cells: map[string]any{
					"provider":   pr.Slug,
					"harness":    pr.Harness,
					"models":     strconv.Itoa(len(pr.Models)),
					"models_num": int64(len(pr.Models)),
					"access":     pr.Access,
				},
			})
		}
		return rows

	case viewSkills:
		return resourceTableRows("ai.skill", p.Inv.Skills)

	case viewAgents:
		return resourceTableRows("ai.agent", p.Inv.Agents)

	case viewMCP:
		rows := make([]table.Row, 0, len(p.Inv.MCPServers))
		for _, s := range p.Inv.MCPServers {
			cmd := strings.Join(append([]string{s.Command}, s.Args...), " ")
			if s.Command == "" {
				cmd = s.URL
			}
			if s.Disabled {
				cmd = "(disabled) " + cmd
			}
			rows = append(rows, table.Row{
				ID:  s.ID,
				Ref: &core.Ref{Kind: "ai.mcp", ID: s.ID},
				Cells: map[string]any{
					"server":    s.Name,
					"harness":   s.Harness,
					"scope":     s.Scope,
					"transport": s.Transport,
					"command":   cut(cmd),
				},
			})
		}
		return rows

	case viewQuota:
		rows := make([]table.Row, 0, len(p.QuotaRows))
		for _, r := range p.QuotaRows {
			cells := map[string]any{}
			if len(r.Cells) >= 5 {
				cells["harness"] = r.Cells[0]
				cells["window"] = r.Cells[1]
				cells["used"] = r.Cells[2]
				cells["left"] = r.Cells[3]
				cells["resets"] = r.Cells[4]
			}
			if qw, ok := r.Data.(QuotaWindow); ok {
				cells["used_num"] = qw.UsedPercent
				cells["left_num"] = qw.RemainingPercent
			}
			rows = append(rows, table.Row{
				ID:    r.ID,
				Ref:   &core.Ref{Kind: "ai.quota", ID: r.ID},
				Cells: cells,
			})
		}
		return rows
	}
	return nil
}

func resourceTableRows(kind string, list []Resource) []table.Row {
	rows := make([]table.Row, 0, len(list))
	for _, r := range list {
		src := strings.Join(r.Harnesses, ",")
		if src == "" {
			src = r.Source
		}
		rows = append(rows, table.Row{
			ID:  r.ID,
			Ref: &core.Ref{Kind: kind, ID: r.ID},
			Cells: map[string]any{
				"name":        r.Name,
				"source":      src,
				"scope":       r.Scope,
				"description": cut(r.Description),
				"path":        r.Path,
			},
		})
	}
	return rows
}

func (p *Page) totalForView(view string) int {
	switch view {
	case viewHarnesses:
		return len(p.Inv.Harnesses)
	case viewModels:
		return len(p.Inv.Models)
	case viewProviders:
		return len(p.Inv.Providers)
	case viewSkills:
		return len(p.Inv.Skills)
	case viewAgents:
		return len(p.Inv.Agents)
	case viewMCP:
		return len(p.Inv.MCPServers)
	case viewQuota:
		return len(p.QuotaRows)
	}
	return 0
}

// --- UI configuration ------------------------------------------------------------

// UIConfig holds options for building the o-ai TUI.
type UIConfig struct {
	Options     Options
	InitialView string
	Bindings    []core.Binding
	Theme       *ui.Theme
	Settings    *config.Settings
	Notes       []string
}

// UI is the immutable configuration shared by engine handlers and views.
type UI struct {
	Tool        *Tool
	Options     Options
	InitialView string
	Km          *core.Keymap
	Bindings    []core.Binding
	Specs       []core.CommandSpec
	Themes      []ui.Theme
	Match       table.MatchMode
	Ratio       float64
	StackW      int
	Detail      bool
	Density     string
	Notes       []string
}

func newUI(tool *Tool, cfg UIConfig) *UI {
	view := cfg.InitialView
	if !slices.Contains(allViews, view) {
		view = viewHarnesses
	}
	u := &UI{
		Tool:        tool,
		Options:     cfg.Options,
		InitialView: view,
		Notes:       cfg.Notes,
	}
	base := ui.DefaultDarkTheme()
	if cfg.Theme != nil {
		base = *cfg.Theme
	}
	u.Themes = []ui.Theme{base, ui.DefaultLightTheme(), ui.HighContrastDarkTheme()}
	set := cfg.Settings
	if set == nil {
		set, _ = config.LoadSettings(nil, defaultSettings)
	}
	switch m := table.MatchMode(set.String("matchMode", "substring")); m {
	case table.MatchSubstring, table.MatchFuzzy, table.MatchRegex:
		u.Match = m
	default:
		u.Match = table.MatchSubstring
	}
	u.Ratio = min(max(set.Float("detailRatio", 0.56), 0.25), 0.8)
	u.StackW = set.Int("stackWidth", 96)
	u.Detail = set.Bool("detail", true)
	u.Density = set.String("density", ui.DensityBalanced)
	if !slices.Contains(densities, u.Density) {
		u.Density = ui.DensityBalanced
	}
	u.Specs = commandSpecs()
	u.Bindings = config.MergeBindings(defaultBindings(), cfg.Bindings)
	km, err := core.NewKeymap(modes, u.Bindings, u.Specs...)
	if err != nil {
		u.Notes = append(u.Notes, "bindings: "+strings.ReplaceAll(err.Error(), "\n", "; "))
		if km == nil {
			km, _ = core.NewKeymap(modes, defaultBindings(), u.Specs...)
		}
	}
	u.Km = km
	return u
}

func (u *UI) theme(p *Page) ui.Theme {
	t := u.Themes[p.ThemeIdx%len(u.Themes)]
	t.Density = p.Density
	return t
}

// --- commands and bindings -------------------------------------------------------

func commandSpecs() []core.CommandSpec {
	user := func(id, desc string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Tags: []string{"user"}}
	}
	internal := func(id, desc string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc}
	}
	specs := []core.CommandSpec{
		user("view.next", "Switch to the next inventory view"),
		user("view.prev", "Switch to the previous inventory view"),
		user("view.harnesses", "Show the Harnesses view"),
		user("view.models", "Show the Models view"),
		user("view.providers", "Show the Providers view"),
		user("view.skills", "Show the Skills view"),
		user("view.agents", "Show the Agents view"),
		user("view.mcp", "Show the MCP Servers view"),
		user("view.quota", "Show the Quota view"),
		user("filter.open", "Filter rows across every column"),
		user("filter.clear", "Clear the active filter"),
		user("filter.mode.cycle", "Cycle filter match mode: substring, fuzzy, regex"),
		user("sort.cycle", "Sort by the next field"),
		user("sort.reverse", "Reverse the sort order"),
		user("group.cycle", "Group rows by the next field"),
		user("group.collapse", "Collapse or expand the focused group"),
		user("detail.toggle", "Show or hide the detail pane"),
		user("detail.scroll.down", "Scroll the detail pane down"),
		user("detail.scroll.up", "Scroll the detail pane up"),
		user("split.grow", "Widen the list next to the detail pane"),
		user("split.shrink", "Narrow the list next to the detail pane"),
		user("split.reset", "Reset the detail split"),
		user("inventory.refresh", "Refresh AI inventory from disk/harnesses (async)"),
		user("quota.refresh", "Fetch latest Claude/Codex quota windows (async)"),
		user("model.select", "Save the focused model as o-ai's default selection"),
		user("config.backup", "Backup the focused harness's primary settings file"),
		user("source.add", "Add a skill source directory (form)"),
		user("theme.cycle", "Cycle theme: dark, light, high contrast"),
		user("density.cycle", "Cycle density: balanced, compact, spacious"),
		user("palette.open", "Open the command palette"),
		user("help.open", "Show active key bindings"),
		user("app.close", "Quit"),

		internal("inventory.done", "Inventory refresh completed"),
		internal("quota.done", "Quota refresh completed"),
		internal("input.insert", "Type into the filter"),
		internal("input.accept", "Keep the filter and return to the list"),
		internal("input.cancel", "Clear the filter and return to the list"),
		internal("palette.insert", "Type into the palette query"),
		internal("palette.backspace", "Delete the last palette query character"),
		internal("palette.close", "Close the palette"),
		internal(picker.CmdConfirm, "Confirm picker selection"),
		internal(picker.CmdModeCycle, "Cycle picker match mode"),
		internal("help.close", "Close help"),
		internal(form.CmdInsert, "Type into form field"),
		internal(form.CmdBackspace, "Delete last character in form field"),
		internal(form.CmdToggle, "Toggle boolean form field"),
		internal(form.CmdSubmit, "Submit form"),
		internal(form.CmdCancel, "Cancel form"),
	}
	specs = append(specs, core.MotionSpecs()...)
	specs = append(specs, input.ReadlineSpecs()...)
	return specs
}

func bind(mode, keys, cmd, hint string) core.Binding {
	return core.Binding{Mode: mode, Keys: strings.Fields(keys), Command: cmd, Hint: hint}
}

func bindClass(mode, cls, cmd string, args map[string]any, hint string) core.Binding {
	return core.Binding{Mode: mode, Class: cls, Command: cmd, Args: args, Hint: hint}
}

var unusedMotions = []string{
	"motion.left", "motion.right", "motion.start", "motion.end",
	"motion.word.next", "motion.word.prev", "motion.word.end",
	"motion.group.next", "motion.group.prev",
}

func standardMotions(mode string) []core.Binding {
	var out []core.Binding
	for _, bd := range core.StandardBindings(mode) {
		if !slices.Contains(unusedMotions, bd.Command) {
			out = append(out, bd)
		}
	}
	return out
}

func defaultBindings() []core.Binding {
	text := map[string]any{"text": "$text"}
	out := []core.Binding{
		bind("normal", "tab", "view.next", "next view"),
		bind("normal", "shift+tab", "view.prev", "prev view"),
		bind("normal", "1", "view.harnesses", "harnesses"),
		bind("normal", "2", "view.models", "models"),
		bind("normal", "3", "view.providers", "providers"),
		bind("normal", "4", "view.skills", "skills"),
		bind("normal", "5", "view.agents", "agents"),
		bind("normal", "6", "view.mcp", "mcp"),
		bind("normal", "7", "view.quota", "quota"),
		bind("normal", "/", "filter.open", "filter"),
		bind("normal", "esc", "filter.clear", "clear filter"),
		bind("normal", "alt+m", "filter.mode.cycle", "match mode"),
		bind("normal", "o", "sort.cycle", "sort"),
		bind("normal", "O", "sort.reverse", "reverse sort"),
		bind("normal", "g b", "group.cycle", "group"),
		bind("normal", "z a", "group.collapse", "fold group"),
		bind("normal", "enter", "detail.toggle", "detail"),
		bind("normal", "J", "detail.scroll.down", "scroll detail down"),
		bind("normal", "K", "detail.scroll.up", "scroll detail up"),
		bind("normal", ">", "split.grow", "wider list"),
		bind("normal", "<", "split.shrink", "narrower list"),
		bind("normal", "=", "split.reset", "reset split"),
		bind("normal", "r", "inventory.refresh", "refresh"),
		bind("normal", "F", "quota.refresh", "fetch quota"),
		bind("normal", "s", "model.select", "select model"),
		bind("normal", "b", "config.backup", "backup config"),
		bind("normal", "a", "source.add", "add source"),
		bind("normal", "g t", "theme.cycle", "theme"),
		bind("normal", "g d", "density.cycle", "density"),
		bind("normal", ":", "palette.open", "commands"),
		bind("normal", "?", "help.open", "help"),
		bind("normal", "q", "app.close", "quit"),
	}
	out = append(out, standardMotions("normal")...)

	out = append(out,
		// Filter input mode
		bindClass("input", core.ClassPrintable, "input.insert", text, "type"),
		bindClass("input", core.ClassPaste, "input.insert", text, "paste"),
		bind("input", "backspace", input.CmdBackspace, "delete"),
		bind("input", "enter", "input.accept", "keep filter"),
		bind("input", "esc", "input.cancel", "clear filter"),
		bind("input", "alt+m", "filter.mode.cycle", "match mode"),
		bind("input", "tab", "filter.mode.cycle", "match mode"),
		bind("input", "down", "motion.down", "next row"),
		bind("input", "ctrl+n", "motion.down", "next row"),
		bind("input", "up", "motion.up", "previous row"),
		bind("input", "ctrl+p", "motion.up", "previous row"),

		// Command palette mode
		bindClass("palette", core.ClassPrintable, "palette.insert", text, "type"),
		bindClass("palette", core.ClassPaste, "palette.insert", text, "paste"),
		bind("palette", "backspace", "palette.backspace", "delete"),
		bind("palette", "enter", picker.CmdConfirm, "run"),
		bind("palette", "esc", "palette.close", "close"),
		bind("palette", "alt+m", picker.CmdModeCycle, "match mode"),
		bind("palette", "tab", picker.CmdModeCycle, "match mode"),
		bind("palette", "down", "motion.down", "next"),
		bind("palette", "ctrl+n", "motion.down", "next"),
		bind("palette", "up", "motion.up", "previous"),
		bind("palette", "ctrl+p", "motion.up", "previous"),

		// Add skill source form mode
		bindClass("form", core.ClassPrintable, form.CmdInsert, text, "type"),
		bindClass("form", core.ClassPaste, form.CmdInsert, text, "paste"),
		bind("form", "backspace", form.CmdBackspace, "delete"),
		bind("form", "tab", "motion.down", "next field"),
		bind("form", "down", "motion.down", "next field"),
		bind("form", "shift+tab", "motion.up", "previous field"),
		bind("form", "up", "motion.up", "previous field"),
		bind("form", "enter", form.CmdSubmit, "save"),
		bind("form", "esc", form.CmdCancel, "cancel"),

		// Help modal
		bind("help", "esc", "help.close", "close"),
		bind("help", "q", "help.close", "close"),
		bind("help", "?", "help.close", "close"),
	)

	// Attach full Readline bindings on every text/filter/picker/form mode without collisions.
	for _, mode := range []string{"input", "palette", "form"} {
		taken := map[string]bool{}
		for _, bd := range out {
			if bd.Mode == mode && len(bd.Keys) > 0 {
				taken[strings.Join(bd.Keys, " ")] = true
			}
		}
		for _, rb := range input.ReadlineBindings(mode, "") {
			if k := strings.Join(rb.Keys, " "); !taken[k] {
				taken[k] = true
				out = append(out, rb)
			}
		}
	}

	out = append(out, standardMotions("help")...)
	return out
}

// --- engine & handlers -----------------------------------------------------------

type pageFn func(s core.State, p *Page, c core.Command) (core.State, []core.Effect)

func (u *UI) wrap(f pageFn) core.Handler {
	return func(s core.State, c core.Command) core.StepResult {
		p := pageOf(s).clone()
		if c.Actor.Kind != "system" {
			p.Banner, p.Tone = "", ""
		}
		rows := u.geometry(&p, s.Width, s.Height).rows
		for k, g := range p.Grids {
			g.PageSize = rows
			p.Grids[k] = g
		}
		s, effects := f(s, &p, c)
		curKey := p.currentDetailKey()
		if !p.Detail {
			p.DetailOff = 0
		} else if curKey != p.detailKey {
			p.detailKey, p.DetailOff = curKey, 0
		}
		return core.Next(withPage(s, p), effects...)
	}
}

func (p *Page) currentDetailKey() string {
	id, _ := p.focusedID()
	return p.View + ":" + id
}

func setPage(s core.State, mode, focus string) core.State {
	f := s.Pages[pageID]
	f.Mode, f.FocusID = mode, focus
	s.Pages = maps.Clone(s.Pages)
	s.Pages[pageID] = f
	return s
}

func openModal(s core.State, id, mode string, data any) core.State {
	s = closeModal(s, id)
	s.Modals = append(slices.Clone(s.Modals), core.Frame{ID: id, Kind: core.FrameModal, Mode: mode, FocusID: id, Data: data})
	return s
}

func closeModal(s core.State, id string) core.State {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		s.Modals = slices.Delete(slices.Clone(s.Modals), i, i+1)
	}
	return s
}

func modalData(s core.State, id string) any {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		return s.Modals[i].Data
	}
	return nil
}

func setModalData(s core.State, id string, data any) core.State {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		s.Modals = slices.Clone(s.Modals)
		s.Modals[i].Data = data
	}
	return s
}

func dispatch(c core.Command) core.Effect { return core.CustomEffect{Kind: "dispatch", Data: c} }

type quotaFetchResult struct {
	Rows   []Row
	Quotas []Quota
}

type sourceForm struct {
	M *form.Model
}

func newEngine(u *UI) *core.Engine {
	e := core.NewEngine(u.Km)
	h := func(id string, f pageFn) { e.Handle(id, u.wrap(f)) }
	scoped := func(scope, id string, f pageFn) { e.HandleScope(scope, id, u.wrap(f)) }

	for _, id := range []string{
		"motion.down", "motion.up", "motion.top", "motion.bottom",
		"motion.page.down", "motion.page.up", "motion.half.down", "motion.half.up",
		"motion.block.next", "motion.block.prev",
	} {
		scoped("grid", id, func(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
			g := p.curGrid()
			if (c.ID == "motion.bottom" || c.ID == "motion.top") && c.Count > 0 {
				g.SelectedIdx = c.Count - 1
				g.Focused()
				p.setCurGrid(g)
				return s, nil
			}
			g.HandleCommand(c)
			p.setCurGrid(g)
			return s, nil
		})
		scoped("palette", id, u.paletteCmd)
		scoped("help", id, u.scrollModal(modalHelp))
		scoped("form", id, u.formCmd)
	}

	// --- view switching
	setView := func(v string) pageFn {
		return func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
			p.View = v
			p.reproject()
			return s, nil
		}
	}
	h("view.next", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		i := slices.Index(allViews, p.View)
		p.View = allViews[(i+1)%len(allViews)]
		p.reproject()
		return s, nil
	})
	h("view.prev", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		i := slices.Index(allViews, p.View)
		p.View = allViews[(i-1+len(allViews))%len(allViews)]
		p.reproject()
		return s, nil
	})
	h("view.harnesses", setView(viewHarnesses))
	h("view.models", setView(viewModels))
	h("view.providers", setView(viewProviders))
	h("view.skills", setView(viewSkills))
	h("view.agents", setView(viewAgents))
	h("view.mcp", setView(viewMCP))
	h("view.quota", setView(viewQuota))

	// --- filter
	h("filter.open", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.Typing = true
		p.QueryLine.Sync(p.Query)
		return setPage(s, "input", nodeFilter), nil
	})
	h("filter.mode.cycle", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		m := table.Model{MatchMode: p.Match}
		m.CycleMatchMode()
		p.Match = m.MatchMode
		p.reproject()
		p.info("match mode: " + string(p.Match))
		return s, nil
	})
	h("filter.clear", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		if p.Query != "" {
			p.Query = ""
			p.QueryLine.Set("")
			p.reproject()
			p.info("filter cleared")
		}
		return s, nil
	})
	editQuery := func(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
		p.QueryLine.Sync(p.Query)
		changed := false
		if c.ID == "input.insert" {
			changed = p.QueryLine.Insert(c.Arg("text"))
		} else {
			changed, _ = p.QueryLine.HandleCommand(c)
		}
		if changed {
			p.Query = p.QueryLine.Value()
			p.reproject()
		}
		return s, nil
	}
	h("input.insert", editQuery)
	for _, id := range input.ReadlineCommandIDs() {
		h(id, editQuery)
	}
	h("input.accept", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.Typing = false
		return setPage(s, "normal", nodeGrid), nil
	})
	h("input.cancel", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.Typing, p.Query = false, ""
		p.QueryLine.Set("")
		p.reproject()
		return setPage(s, "normal", nodeGrid), nil
	})

	// --- sort, group, detail, split
	h("sort.cycle", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		keys := viewSortKeys[p.View]
		if len(keys) == 0 {
			return s, nil
		}
		cur := p.SortField[p.View]
		i := slices.Index(keys, cur)
		next := keys[(i+1)%len(keys)]
		p.SortField[p.View] = next
		p.SortDesc[p.View] = next == "procs" || next == "models" || next == "context" || next == "used"
		p.reproject()
		p.info(sortLabel(p))
		return s, nil
	})
	h("sort.reverse", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.SortDesc[p.View] = !p.SortDesc[p.View]
		p.reproject()
		p.info(sortLabel(p))
		return s, nil
	})
	h("group.cycle", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		g := p.curGrid()
		keys := viewGroupKeys[p.View]
		if len(keys) == 0 {
			return s, nil
		}
		i := slices.Index(keys, g.GroupField)
		g.GroupField = keys[(i+1)%len(keys)]
		g.Collapsed = nil
		g.SelectedIdx = 0
		g.Focused()
		p.setCurGrid(g)
		if g.GroupField == "" {
			p.info("ungrouped")
		} else {
			p.info("grouped by " + g.GroupField)
		}
		return s, nil
	})
	h("group.collapse", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		g := p.curGrid()
		if g.GroupField == "" {
			p.warn("not grouped — g b groups")
			return s, nil
		}
		g.ToggleGroup()
		p.setCurGrid(g)
		return s, nil
	})
	h("detail.toggle", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.Detail = !p.Detail
		return s, nil
	})
	scrollDetail := func(dir int) pageFn {
		return func(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
			if !p.Detail {
				p.info("detail pane hidden — enter shows it")
				return s, nil
			}
			if _, atEnd := s.Layout.Rects[detailTail]; dir < 0 {
				p.DetailOff = max(p.DetailOff-c.Times(), 0)
			} else if !atEnd {
				p.DetailOff += c.Times()
			}
			return s, nil
		}
	}
	h("detail.scroll.down", scrollDetail(1))
	h("detail.scroll.up", scrollDetail(-1))
	resize := func(d float64) pageFn {
		return func(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
			if d == 0 {
				p.Ratio = u.Ratio
			} else {
				p.Ratio = min(max(p.Ratio+d*float64(c.Times()), 0.25), 0.8)
			}
			return s, nil
		}
	}
	h("split.grow", resize(0.05))
	h("split.shrink", resize(-0.05))
	h("split.reset", resize(0))

	// --- appearance
	h("theme.cycle", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.ThemeIdx = (p.ThemeIdx + 1) % len(u.Themes)
		th := u.theme(p)
		p.info("theme: " + th.Name)
		return s, []core.Effect{core.CustomEffect{Kind: "theme", Data: th}}
	})
	h("density.cycle", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		i := slices.Index(densities, p.Density)
		p.Density = densities[(i+1)%len(densities)]
		p.info("density: " + p.Density)
		return s, []core.Effect{core.CustomEffect{Kind: "theme", Data: u.theme(p)}}
	})

	// --- inventory & quota async actions
	h("inventory.refresh", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.Loading = true
		p.info("refreshing AI inventory…")
		tool := u.Tool
		opts := u.Options
		opts.Fresh = true
		return s, []core.Effect{core.AsyncEffect{
			Slot:    "inventory",
			Command: "inventory.done",
			Run: func(_ context.Context) (any, error) {
				return tool.inventory(opts, needAll), nil
			},
		}}
	})
	h("inventory.done", func(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
		p.Loading = false
		if err, _ := c.Args["error"].(error); err != nil {
			p.fail("refresh: " + err.Error())
			return s, nil
		}
		if inv, ok := c.Args["value"].(Inventory); ok {
			p.Inv = inv
			if sel, err := loadSelection(u.Options); err == nil {
				p.SelectedModel = sel
			}
			p.reproject()
			p.note(fmt.Sprintf("inventory refreshed: %d harnesses, %d models, %d skills", len(inv.Harnesses), len(inv.Models), len(inv.Skills)))
		}
		return s, nil
	})

	h("quota.refresh", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		p.Loading = true
		p.info("fetching plan quota windows…")
		tool := u.Tool
		opts := u.Options
		opts.Fresh = true
		return s, []core.Effect{core.AsyncEffect{
			Slot:    "quota",
			Command: "quota.done",
			Run: func(_ context.Context) (any, error) {
				rows, qs, err := tool.quotaListData(opts)
				return quotaFetchResult{Rows: rows, Quotas: qs}, err
			},
		}}
	})
	h("quota.done", func(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
		p.Loading = false
		if qr, ok := c.Args["value"].(quotaFetchResult); ok {
			p.QuotaRows = qr.Rows
			p.Quotas = qr.Quotas
			p.reproject()
		}
		if err, _ := c.Args["error"].(error); err != nil {
			p.warn("quota: " + err.Error())
			return s, nil
		}
		p.note(fmt.Sprintf("quota updated (%d rows)", len(p.QuotaRows)))
		return s, nil
	})

	// --- model select, config backup, add skill source form
	h("model.select", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		if p.View != viewModels {
			p.warn("switch to Models view (2) to select a model")
			return s, nil
		}
		id, ok := p.focusedID()
		if !ok {
			p.warn("no model selected")
			return s, nil
		}
		if err := saveSelection(u.Options, id); err != nil {
			p.fail("select model: " + err.Error())
			return s, nil
		}
		p.SelectedModel = id
		p.reproject()
		p.note("selected model " + id)
		return s, nil
	})

	h("config.backup", func(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
		var srcPath string
		if p.View == viewHarnesses {
			if id, ok := p.focusedID(); ok {
				for _, hr := range p.Inv.Harnesses {
					if hr.ID == id && len(hr.Settings) > 0 {
						srcPath = hr.Settings[0].Path
						break
					}
				}
			}
		}
		if srcPath == "" {
			p.warn("focus a configured harness in Harnesses view (1) to backup its settings")
			return s, nil
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			p.fail("backup read: " + err.Error())
			return s, nil
		}
		backupDir := filepath.Join(u.Options.Home, ".config", "o-ai", "backups")
		if err := os.MkdirAll(backupDir, 0o700); err != nil {
			p.fail("backup dir: " + err.Error())
			return s, nil
		}
		dst := filepath.Join(backupDir, fmt.Sprintf("%s.backup.%d", filepath.Base(srcPath), time.Now().UnixMilli()))
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			p.fail("backup write: " + err.Error())
			return s, nil
		}
		p.note("backed up to " + dst)
		return s, nil
	})

	h("source.add", func(s core.State, _ *Page, _ core.Command) (core.State, []core.Effect) {
		m := newSourceForm()
		return openModal(s, modalForm, "form", sourceForm{M: m}), nil
	})
	for _, id := range append([]string{form.CmdInsert, form.CmdBackspace, form.CmdToggle, form.CmdSubmit, form.CmdCancel}, input.ReadlineCommandIDs()...) {
		scoped("form", id, u.formCmd)
	}

	// --- modals
	h("palette.open", u.paletteOpen)
	for _, id := range append([]string{"palette.insert", "palette.backspace", "palette.close", picker.CmdConfirm, picker.CmdModeCycle}, input.ReadlineCommandIDs()...) {
		scoped("palette", id, u.paletteCmd)
	}
	h("help.open", func(s core.State, _ *Page, _ core.Command) (core.State, []core.Effect) {
		return openModal(s, modalHelp, "help", 0), nil
	})
	h("help.close", func(s core.State, _ *Page, _ core.Command) (core.State, []core.Effect) {
		return closeModal(s, modalHelp), nil
	})
	h("app.close", func(s core.State, _ *Page, _ core.Command) (core.State, []core.Effect) {
		return s, []core.Effect{core.QuitEffect{}}
	})
	return e
}

func sortLabel(p *Page) string {
	field := p.SortField[p.View]
	if field == "" && len(viewSortKeys[p.View]) > 0 {
		field = viewSortKeys[p.View][0]
	}
	dir := "asc"
	if p.SortDesc[p.View] {
		dir = "desc"
	}
	return "sorted by " + field + " " + dir
}

func (u *UI) scrollModal(id string) pageFn {
	return func(s core.State, _ *Page, c core.Command) (core.State, []core.Effect) {
		page := max(s.Height*70/100-4, 1)
		n := c.Times()
		delta := map[string]int{
			"motion.down": n, "motion.up": -n,
			"motion.page.down": n * page, "motion.page.up": -n * page,
			"motion.half.down": n * page / 2, "motion.half.up": -n * page / 2,
			"motion.top": -1 << 30, "motion.bottom": 1 << 30,
		}[c.ID]
		clampOff := func(off, total int) int { return min(max(off, 0), max(total-page, 0)) }
		if d, ok := modalData(s, id).(int); ok {
			return setModalData(s, id, clampOff(d+delta, len(u.helpLines()))), nil
		}
		return s, nil
	}
}

// --- palette & form --------------------------------------------------------------

func (u *UI) paletteOpen(s core.State, p *Page, _ core.Command) (core.State, []core.Effect) {
	keys := keysByCommand(u, "normal")
	var items []picker.Item
	for _, spec := range u.Specs {
		if spec.HasTag("user") {
			sub := spec.Description
			if k := keys[spec.ID]; len(k) > 0 {
				sub += "  [" + strings.Join(k, ", ") + "]"
			}
			items = append(items, picker.Item{ID: spec.ID, Title: spec.ID, Subtitle: sub, Keywords: []string{spec.Description}})
		}
	}
	m := picker.New("Commands", items)
	m.Scope = "palette"
	m.Placeholder = "type to filter commands · tab match mode · ctrl+a/e/w/u edit"
	m.MatchMode = picker.MatchMode(p.Match)
	return openModal(s, modalPalette, "palette", m), nil
}

func (u *UI) paletteCmd(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
	old, ok := modalData(s, modalPalette).(*picker.Model)
	if !ok {
		return s, nil
	}
	m := *old
	switch c.ID {
	case "palette.insert":
		m.AppendQuery(c.Arg("text"))
	case "palette.backspace":
		m.BackspaceQuery()
	case "palette.close":
		return closeModal(s, modalPalette), nil
	default:
		m.PageSize = max(s.Height*70/100-4, 1)
		picked, _ := m.HandleCommand(c)
		if picked != nil {
			s = closeModal(s, modalPalette)
			return s, []core.Effect{dispatch(core.Command{ID: picked.ID, Source: "palette", Actor: core.Actor{Kind: "user"}})}
		}
	}
	if m.FilterErr != "" {
		p.warn("regex: " + m.FilterErr)
	}
	return setModalData(s, modalPalette, &m), nil
}

func newSourceForm() *form.Model {
	specs := []core.ParamSpec{
		{Name: "name", Type: core.ParamString, Required: true, Description: "Skill source name (e.g. team)"},
		{Name: "path", Type: core.ParamString, Required: true, Description: "Directory path containing skills (e.g. ~/team/skills)"},
	}
	m := form.New("Add skill source", specs, map[string]any{"name": "", "path": ""})
	m.SubmitLabel = "Save"
	m.Editing = true
	return m
}

func (u *UI) formCmd(s core.State, p *Page, c core.Command) (core.State, []core.Effect) {
	sf, ok := modalData(s, modalForm).(sourceForm)
	if !ok || sf.M == nil {
		return s, nil
	}
	m := *sf.M
	m.Values = maps.Clone(sf.M.Values)
	m.Errors = maps.Clone(sf.M.Errors)
	sf.M = &m

	res, _ := m.HandleCommand(c)
	if c.ID == "motion.down" || c.ID == "motion.up" {
		m.Editing = m.Editable()
		m.HandleCommand(core.Command{ID: form.CmdEdit})
	}
	switch {
	case res.Cancelled:
		p.info("cancelled")
		return closeModal(s, modalForm), nil
	case res.Submitted:
		name := strings.TrimSpace(fmt.Sprint(res.Values["name"]))
		path := strings.TrimSpace(fmt.Sprint(res.Values["path"]))
		if name == "" || name == "<nil>" {
			m.Errors["name"] = "required"
			p.fail("name is required")
			return setModalData(s, modalForm, sf), nil
		}
		if path == "" || path == "<nil>" {
			m.Errors["path"] = "required"
			p.fail("path is required")
			return setModalData(s, modalForm, sf), nil
		}
		if err := addSkillSourceFile(u.Options.Home, Source{Name: name, Path: path}); err != nil {
			p.fail("save source: " + err.Error())
			return setModalData(s, modalForm, sf), nil
		}
		p.note("added skill source " + name)
		return closeModal(s, modalForm), []core.Effect{dispatch(core.Command{ID: "inventory.refresh", Source: "form", Actor: core.Actor{Kind: "system"}})}
	case len(res.ErrorFields) > 0:
		var msgs []string
		for _, field := range res.ErrorFields {
			msgs = append(msgs, field+": "+m.Errors[field])
		}
		p.fail("not saved — " + strings.Join(msgs, "; "))
	}
	return setModalData(s, modalForm, sf), nil
}

func addSkillSourceFile(home string, src Source) error {
	p := sourcesPath(home)
	var cfg struct {
		Skills []Source `json:"skills"`
	}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	updated := false
	for i := range cfg.Skills {
		if cfg.Skills[i].Name == src.Name {
			cfg.Skills[i].Path = src.Path
			updated = true
			break
		}
	}
	if !updated {
		cfg.Skills = append(cfg.Skills, src)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return writeJSONAtomic(p, cfg)
}

// --- shell construction ----------------------------------------------------------

func newShell(u *UI) (*otea.Shell, tea.Cmd) {
	p := Page{
		View:      u.InitialView,
		Grids:     map[string]table.Model{},
		SortField: map[string]string{},
		SortDesc:  map[string]bool{},
		Match:     u.Match,
		Ratio:     u.Ratio,
		Detail:    u.Detail,
		Density:   u.Density,
	}
	for _, v := range allViews {
		p.Grids[v] = *table.New(viewColumns[v], nil)
		if len(viewSortKeys[v]) > 0 {
			p.SortField[v] = viewSortKeys[v][0]
		}
	}
	p.Inv = u.Tool.inventory(u.Options, needAll)
	if sel, err := loadSelection(u.Options); err == nil {
		p.SelectedModel = sel
	}
	// Load cached quota without network calls if present.
	qOpts := u.Options
	qOpts.Fresh = false
	if qCache := u.Tool.readQuotaCache(); len(qCache) > 0 {
		var qs []Quota
		for _, h := range quotaHarnesses {
			if e, ok := qCache[h+"@"+u.Options.Home]; ok {
				qs = append(qs, e.Quota)
			}
		}
		if len(qs) > 0 {
			p.Quotas = qs
			p.QuotaRows = quotaRows(qs, u.Tool.clock())
		}
	}
	p.reproject()
	if len(u.Notes) > 0 {
		p.warn(strings.Join(u.Notes, " · "))
	}

	initial := core.NewState(core.Frame{ID: pageID, FocusID: nodeGrid, Mode: "normal", Data: p})
	sh := otea.New(newEngine(u), initial, u.view, u.theme(&p))
	sh.OnPassThrough = func(e core.PassToWidgetEffect) tea.Cmd {
		if e.Mouse == nil || e.NodeID != nodeGrid {
			return nil
		}
		id := map[string]string{"wheeldown": "motion.down", "wheelup": "motion.up"}[e.Mouse.Button]
		if id == "" {
			return nil
		}
		return func() tea.Msg { return core.CommandEvent{Command: core.Command{ID: id, Count: 3, Source: "mouse"}} }
	}
	sh.OnCustom = func(e core.CustomEffect) tea.Cmd {
		switch d := e.Data.(type) {
		case core.Command:
			if e.Kind == "dispatch" {
				return func() tea.Msg { return core.CommandEvent{Command: d} }
			}
		case ui.Theme:
			sh.Theme = d
		}
		return nil
	}
	return sh, nil
}

type startup struct {
	*otea.Shell
	cmd tea.Cmd
}

func (s startup) Init() tea.Cmd { return s.cmd }

func runTUI(t *Tool, cfg UIConfig) error {
	sh, cmd := newShell(newUI(t, cfg))
	_, err := tea.NewProgram(startup{sh, cmd}).Run()
	if errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}
