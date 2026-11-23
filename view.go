package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

type geom struct {
	tableW, tableH, rows int
	side                 bool
}

func (u *UI) geometry(p *Page, w, h int) geom {
	if w <= 0 || h <= 0 {
		w, h = 120, 30
	}
	body := h - 3
	if p.Typing {
		body--
	}
	body = max(body, 3)
	g := geom{tableW: w, tableH: body}
	if p.Detail {
		if w >= u.StackW {
			g.side = true
			g.tableW = int(math.Round(float64(max(0, w-1)) * p.Ratio))
		} else {
			g.tableH = int(math.Round(float64(max(0, body-1)) * p.Ratio))
		}
	}
	g.rows = max(g.tableH-2, 1)
	return g
}

func textNode(t, tone string) ui.ViewNode {
	return ui.ViewNode{Kind: ui.KindText, Tone: tone, Props: map[string]any{"text": t}}
}

func clip(s string, w int) string {
	if r := []rune(s); w > 1 && len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s
}

var viewTitles = map[string]string{
	viewHarnesses: "Harnesses",
	viewModels:    "Models",
	viewProviders: "Providers",
	viewSkills:    "Skills",
	viewAgents:    "Agents",
	viewMCP:       "MCP",
	viewQuota:     "Quota",
}

func (u *UI) view(s core.State) ui.ViewNode {
	p := pageOf(s)
	w := s.Width
	if w <= 0 {
		w = 120
	}
	g := u.geometry(&p, s.Width, s.Height)

	grid := p.curGrid().Copy()
	sortField := p.SortField[p.View]
	sortDesc := p.SortDesc[p.View]
	baseCols := viewColumns[p.View]

	grid.PageSize = g.rows
	grid.Columns = make([]table.Column, len(baseCols))
	for i, c := range baseCols {
		if c.Key == sortField {
			if sortDesc {
				c.Title += " ▼"
			} else {
				c.Title += " ▲"
			}
		}
		grid.Columns[i] = c
	}

	body := grid.View(nodeGrid)
	body.Title = fmt.Sprintf("%s %d", viewTitles[p.View], p.totalForView(p.View))
	if p.Detail {
		split := ui.ViewNode{ID: "body", Kind: ui.KindSplit, Ratio: p.Ratio, Gap: 1, Props: map[string]any{"axis": "horizontal"}}
		if !g.side {
			split.Props["axis"] = "vertical"
		}
		split.Children = []ui.ViewNode{body, u.detailNode(&p, &grid)}
		body = split
	}

	children := []ui.ViewNode{u.titleLine(&p, w), body}
	if p.Typing {
		children = append(children, p.inputNode())
	}
	children = append(children, p.infoLine(s, &grid, w), statusLine(&p, w))

	root := ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{
		{ID: "page", Kind: ui.KindVStack, Scope: "grid", Children: children},
	}}
	for _, m := range s.Modals {
		root.Children = append(root.Children, u.modalView(s, m))
	}
	return root
}

func (u *UI) titleLine(p *Page, w int) ui.ViewNode {
	tabs := make([]string, 0, len(allViews))
	for i, v := range allViews {
		lbl := fmt.Sprintf("%d %s", i+1, viewTitles[v])
		if v == p.View {
			lbl = "[" + lbl + "]"
		}
		tabs = append(tabs, lbl)
	}
	parts := []string{" o-ai", strings.Join(tabs, " ")}
	if p.Loading {
		parts = append(parts, "collecting…")
	}
	return textNode(clip(strings.Join(parts, " · "), w), "title")
}

func (p *Page) inputNode() ui.ViewNode {
	line := p.QueryLine
	line.Sync(p.Query)
	return ui.ViewNode{
		ID:        nodeFilter,
		Kind:      ui.KindInput,
		Focusable: true,
		Title:     "/ filter [" + string(p.Match) + "]",
		Props: map[string]any{
			"value":       p.Query,
			"cursor":      line.Cursor,
			"placeholder": "type to filter · enter keeps · esc clears · tab match mode · ctrl+a/e/w/u edit",
		},
	}
}

func (p *Page) infoLine(s core.State, grid *table.Model, w int) ui.ViewNode {
	var parts []string
	items := grid.Items()
	total := p.totalForView(p.View)
	pos := "no rows"
	if len(items) > 0 {
		pos = fmt.Sprintf("row %d/%d", grid.SelectedIdx+1, len(items))
	}
	if len(grid.Rows) != total {
		pos += fmt.Sprintf(" (%d of %d match)", len(grid.Rows), total)
	}
	parts = append(parts, pos, sortLabel(p))
	tone := "muted"
	if p.Query != "" || p.Typing {
		parts = append(parts, fmt.Sprintf("filter %s %q", p.Match, p.Query))
	}
	if p.FilterErr != "" {
		parts = append(parts, "regex error: "+p.FilterErr)
		tone = "error"
	}
	if grid.GroupField != "" {
		parts = append(parts, "grouped by "+grid.GroupField)
	}
	if in := s.Input; in.Count != nil || len(in.Prefix) > 0 {
		pre := strings.Join(in.Prefix, " ")
		if in.Count != nil {
			pre = strconv.Itoa(*in.Count) + " " + pre
		}
		parts = append(parts, "pending "+strings.TrimSpace(pre))
	}
	return textNode(clip(" "+strings.Join(parts, " · "), w), tone)
}

func statusLine(p *Page, w int) ui.ViewNode {
	if p.Banner != "" {
		return textNode(clip(" "+strings.ReplaceAll(p.Banner, "\n", " "), w), p.Tone)
	}
	hint := " ? help · : commands · 1-7/tab views · / filter · o/O sort · g b group · r refresh · F quota · s select · b backup · a add source · q quit"
	return textNode(clip(hint, w), "muted")
}

func (u *UI) detailNode(p *Page, grid *table.Model) ui.ViewNode {
	title := "Detail"
	it, ok := grid.Focused()
	var mdSource string
	switch {
	case !ok:
		if p.View == viewQuota {
			mdSource = "No quota windows loaded.\n\nPress `F` to fetch Claude Code / Codex plan quota."
		} else {
			mdSource = fmt.Sprintf("No %s found.", strings.ToLower(viewTitles[p.View]))
		}
	case it.Header:
		title = "Group"
		mdSource = fmt.Sprintf("### %s = %s\n\n%d item(s) · `z a` folds/expands group", grid.GroupField, it.Group, it.Count)
	default:
		title, mdSource = p.detailMarkdown(it.Row.ID)
	}

	md := markdown.New(title, mdSource)
	md.Offset = p.DetailOff
	mdNode := ui.ViewNode{
		ID:    detailText,
		Kind:  ui.KindMarkdown,
		Props: map[string]any{"markdown": textWindow(md.Source, p.DetailOff)},
	}
	tail := textNode(" ", "text")
	tail.ID = detailTail
	return ui.ViewNode{
		ID:    "detail",
		Kind:  ui.KindPanel,
		Title: title,
		Children: []ui.ViewNode{
			{ID: "detail.scroll", Kind: ui.KindScroll, Props: map[string]any{"offset": p.DetailOff}, Children: []ui.ViewNode{mdNode, tail}},
		},
	}
}

func (p *Page) detailMarkdown(id string) (string, string) {
	switch p.View {
	case viewHarnesses:
		for _, h := range p.Inv.Harnesses {
			if h.ID == id {
				return "Harness · " + h.ID, harnessDetailMarkdown(h)
			}
		}
	case viewModels:
		for _, m := range p.Inv.Models {
			if m.ID == id {
				return "Model · " + m.Name, modelDetailMarkdown(m, p.SelectedModel == m.ID)
			}
		}
	case viewProviders:
		for _, pr := range p.Inv.Providers {
			if pr.ID == id {
				return "Provider · " + pr.Slug, providerDetailMarkdown(pr)
			}
		}
	case viewSkills:
		for _, r := range p.Inv.Skills {
			if r.ID == id {
				return "Skill · " + r.Name, resourceDetailMarkdown(r)
			}
		}
	case viewAgents:
		for _, r := range p.Inv.Agents {
			if r.ID == id {
				return "Agent · " + r.Name, resourceDetailMarkdown(r)
			}
		}
	case viewMCP:
		for _, s := range p.Inv.MCPServers {
			if s.ID == id {
				return "MCP · " + s.Name, mcpDetailMarkdown(s, p.Inv.MCPTools)
			}
		}
	case viewQuota:
		for _, r := range p.QuotaRows {
			if r.ID == id {
				return "Quota · " + r.ID, quotaDetailMarkdown(r)
			}
		}
	}
	return "Detail", "_No record found._"
}

func harnessDetailMarkdown(h Harness) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", h.ID)
	fmt.Fprintf(&b, "- **installed**: `%v`\n", h.Installed)
	fmt.Fprintf(&b, "- **configured**: `%v`\n", h.Configured)
	if h.Executable != "" {
		fmt.Fprintf(&b, "- **executable**: `%s`\n", h.Executable)
	}
	fmt.Fprintf(&b, "- **processes**: %s (%d PIDs)\n", h.ProcessStatus, len(h.PIDs))
	fmt.Fprintf(&b, "- **catalog**: %s\n", h.CatalogStatus)
	if len(h.Settings) > 0 {
		b.WriteString("\n### Settings Files\n\n")
		for _, s := range h.Settings {
			fmt.Fprintf(&b, "- `%s` (%s)", s.Path, s.Scope)
			if s.Error != "" {
				fmt.Fprintf(&b, " — **error**: %s", s.Error)
			}
			b.WriteByte('\n')
		}
	}
	if len(h.Warnings) > 0 {
		b.WriteString("\n### Warnings\n\n")
		for _, w := range h.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func modelDetailMarkdown(m Model, selected bool) string {
	var b strings.Builder
	title := m.Name
	if selected {
		title += " ★"
	}
	fmt.Fprintf(&b, "## %s\n\n", title)
	fmt.Fprintf(&b, "- **id**: `%s`\n", m.ID)
	fmt.Fprintf(&b, "- **harness**: `%s`\n", m.Harness)
	fmt.Fprintf(&b, "- **provider**: `%s`\n", m.Provider)
	fmt.Fprintf(&b, "- **cliSlug**: `%s`\n", m.CLISlug)
	fmt.Fprintf(&b, "- **access**: %s\n", m.Access)
	if ctx := contextWindow(m.Metadata); ctx > 0 {
		fmt.Fprintf(&b, "- **context**: %s (%d tokens)\n", tokens(ctx), ctx)
	}
	if len(m.Args) > 0 {
		fmt.Fprintf(&b, "\n### Launch\n\n```sh\n%s\n```\n", strings.Join(m.Args, " "))
	}
	if len(m.Thinking) > 0 {
		b.WriteString("\n### Thinking Levels\n\n")
		for _, t := range m.Thinking {
			fmt.Fprintf(&b, "- **%s** (`%s`)", t.Name, t.Slug)
			if t.Description != "" {
				fmt.Fprintf(&b, ": %s", t.Description)
			}
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func providerDetailMarkdown(pr Provider) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", pr.Slug)
	fmt.Fprintf(&b, "- **harness**: `%s`\n", pr.Harness)
	fmt.Fprintf(&b, "- **access**: %s\n", pr.Access)
	fmt.Fprintf(&b, "- **models**: %d\n", len(pr.Models))
	if len(pr.Models) > 0 {
		b.WriteString("\n### Models\n\n")
		for _, m := range pr.Models {
			fmt.Fprintf(&b, "- `%s`\n", m)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func resourceDetailMarkdown(r Resource) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", r.Name)
	if r.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(r.Description))
	}
	fmt.Fprintf(&b, "- **scope**: `%s`\n", r.Scope)
	if len(r.Harnesses) > 0 {
		fmt.Fprintf(&b, "- **harnesses**: %s\n", strings.Join(r.Harnesses, ", "))
	}
	if r.Source != "" {
		fmt.Fprintf(&b, "- **source**: `%s`\n", r.Source)
	}
	fmt.Fprintf(&b, "- **path**: `%s`\n", r.Path)
	if r.ResolvedPath != "" && r.ResolvedPath != r.Path {
		fmt.Fprintf(&b, "- **resolved**: `%s`\n", r.ResolvedPath)
	}
	if strings.TrimSpace(r.Content) != "" {
		b.WriteString("\n---\n\n")
		b.WriteString(strings.TrimSpace(r.Content))
	}
	return strings.TrimRight(b.String(), "\n")
}

func mcpDetailMarkdown(s MCPServer, allTools []MCPTool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", s.Name)
	fmt.Fprintf(&b, "- **harness**: `%s`\n", s.Harness)
	fmt.Fprintf(&b, "- **scope**: `%s`\n", s.Scope)
	fmt.Fprintf(&b, "- **transport**: `%s`\n", s.Transport)
	if s.Disabled {
		b.WriteString("- **status**: disabled\n")
	}
	if s.Command != "" {
		fmt.Fprintf(&b, "- **command**: `%s`\n", strings.Join(append([]string{s.Command}, s.Args...), " "))
	}
	if s.URL != "" {
		fmt.Fprintf(&b, "- **url**: `%s`\n", s.URL)
	}
	if len(s.Env) > 0 {
		fmt.Fprintf(&b, "- **env keys**: %s\n", strings.Join(s.Env, ", "))
	}
	if len(s.Headers) > 0 {
		fmt.Fprintf(&b, "- **header keys**: %s\n", strings.Join(s.Headers, ", "))
	}
	var tools []MCPTool
	for _, t := range allTools {
		if t.Server == s.Name {
			tools = append(tools, t)
		}
	}
	if len(tools) > 0 {
		b.WriteString("\n### Cached Tools\n\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "- **%s**", t.Name)
			if t.Description != "" {
				fmt.Fprintf(&b, " — %s", cut(t.Description))
			}
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func quotaDetailMarkdown(r Row) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", r.Label)
	switch q := r.Data.(type) {
	case QuotaWindow:
		fmt.Fprintf(&b, "- **harness**: `%s`\n", q.Harness)
		fmt.Fprintf(&b, "- **window**: `%s`\n", q.Window)
		fmt.Fprintf(&b, "- **used**: %.1f%%\n", q.UsedPercent)
		fmt.Fprintf(&b, "- **remaining**: %.1f%%\n", q.RemainingPercent)
		if q.ResetsAt != "" {
			fmt.Fprintf(&b, "- **resets at**: `%s`\n", q.ResetsAt)
		}
		if q.ResetsIn != "" {
			fmt.Fprintf(&b, "- **resets in**: %s\n", q.ResetsIn)
		}
		if q.Source != "" {
			fmt.Fprintf(&b, "- **source**: `%s`\n", q.Source)
		}
		if q.Stale {
			b.WriteString("- **stale**: yes (window reset since observation)\n")
		}
	case Quota:
		fmt.Fprintf(&b, "- **harness**: `%s`\n", q.Harness)
		if q.Error != "" {
			fmt.Fprintf(&b, "- **error**: %s\n", q.Error)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func textWindow(textBody string, off int) string {
	lines := strings.Split(textBody, "\n")
	off = min(max(off, 0), len(lines))
	return strings.Join(lines[off:], "\n")
}

func (u *UI) modalView(_ core.State, f core.Frame) ui.ViewNode {
	switch f.ID {
	case modalPalette:
		m, _ := f.Data.(*picker.Model)
		if m == nil {
			break
		}
		return ui.ViewNode{
			ID: "palette.overlay", Kind: ui.KindOverlay, Scope: "palette",
			Title: "Command palette · " + string(m.MatchMode),
			Props: map[string]any{"widthPct": 70, "heightPct": 70},
			Children: []ui.ViewNode{
				m.View(modalPalette),
				textNode("enter runs · esc closes · tab match mode · ↑↓ move · ctrl+a/e/w/u edit", "muted"),
			},
		}
	case modalHelp:
		off, _ := f.Data.(int)
		return ui.ViewNode{
			ID: modalHelp, Kind: ui.KindOverlay, Scope: "help", Focusable: true,
			Title:    "Help · active key bindings",
			Props:    map[string]any{"widthPct": 80, "heightPct": 85},
			Children: []ui.ViewNode{textNode(textWindow(strings.Join(u.helpLines(), "\n"), off), "text")},
		}
	case modalForm:
		sf, ok := f.Data.(sourceForm)
		if !ok || sf.M == nil {
			break
		}
		kids := []ui.ViewNode{sf.M.View(modalForm)}
		for _, sp := range sf.M.Specs {
			if e := sf.M.Errors[sp.Name]; e != "" {
				kids = append(kids, textNode(sp.Name+": "+e, "error"))
			}
		}
		kids = append(kids, textNode("type to edit (ctrl+a/e/w/u) · tab/↑↓ field · enter saves · esc cancels", "muted"))
		return ui.ViewNode{
			ID: modalForm, Kind: ui.KindOverlay, Scope: "form", Focusable: true,
			Title:    sf.M.Title,
			Props:    map[string]any{"widthPct": 72, "heightPct": 60},
			Children: kids,
		}
	}
	return textNode("", "muted")
}

// --- help generation -------------------------------------------------------------

func keysByCommand(u *UI, mode string) map[string][]string {
	out := map[string][]string{}
	for _, bd := range u.Bindings {
		if bd.Mode != mode || bd.Hidden || bd.Unbind || len(bd.Keys) == 0 {
			continue
		}
		k := strings.Join(bd.Keys, " ")
		if !slices.Contains(out[bd.Command], k) {
			out[bd.Command] = append(out[bd.Command], k)
		}
	}
	return out
}

func (u *UI) helpLines() []string {
	desc := map[string]string{}
	for _, sp := range u.Specs {
		desc[sp.ID] = sp.Description
	}
	keys := keysByCommand(u, "normal")
	var lines []string
	lines = append(lines, "o-ai — active key bindings (j/k scroll · esc/q/? close)", "")
	for _, sp := range u.Specs {
		if !sp.HasTag("user") {
			continue
		}
		ks := strings.Join(keys[sp.ID], ", ")
		lines = append(lines, fmt.Sprintf("  %-14s  %-22s  %s", ks, sp.ID, desc[sp.ID]))
	}
	lines = append(lines, "", "Text / Filter / Form / Picker Readline keys:",
		"  ctrl+a / ctrl+e   line start / end",
		"  alt+b  / alt+f    word left / right",
		"  ctrl+w / ctrl+u   kill word / line backward",
		"  ctrl+k / ctrl+y   kill line forward / yank")
	return lines
}
