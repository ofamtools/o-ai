package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	otea "ofam.dev/ochazuke/tea"
)

// driver feeds an Ochazuke v2 tea.Shell synchronously without a TTY.
type driver struct{ sh *otea.Shell }

func newDriver(sh *otea.Shell, initCmd tea.Cmd, w, h int) *driver {
	sh.Engine.PrefixTimeout = 0
	sh.MaxFPS = 0
	d := &driver{sh: sh}
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
	d.pump(initCmd)
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.sh.Update(msg)
	d.pump(cmd)
}

func (d *driver) pump(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var m tea.Msg
	select {
	case m = <-done:
	case <-time.After(5 * time.Second):
		return
	}
	switch m := m.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			d.pump(c)
		}
	case tea.QuitMsg, nil:
	default:
		d.send(m)
	}
}

func (d *driver) keys(names ...string) {
	for _, n := range names {
		d.send(keyMsg(n))
	}
}

func (d *driver) typeText(s string) {
	for _, r := range s {
		if r == ' ' {
			d.send(keyMsg("space"))
		} else {
			d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

var namedKeys = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
	"delete": tea.KeyDelete,
}

func keyMsg(n string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for {
		if rest, ok := strings.CutPrefix(n, "ctrl+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModCtrl, rest
		} else if rest, ok := strings.CutPrefix(n, "alt+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModAlt, rest
		} else if rest, ok := strings.CutPrefix(n, "shift+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModShift, rest
		} else {
			break
		}
	}
	if r, ok := namedKeys[n]; ok {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	if n == "space" {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " ", Mod: mod}
	}
	r := []rune(n)[0]
	if mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	return tea.KeyPressMsg{Code: r, Text: n}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[A-Za-z]|\x1b\][^\a\x1b]*(\a|\x1b\\)`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

func writeSmokeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func runSmoke(out io.Writer, dump bool) error {
	return runSmokeWithSize(out, dump, 120, 30)
}

func runSmokeWithSize(out io.Writer, dump bool, width, height int) error {
	dir, err := os.MkdirTemp("", "o-ai-smoke-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	home := filepath.Join(dir, "home")
	cache := filepath.Join(dir, "cache")

	// Populate isolated fixture configs (never mutates real user configs/caches).
	fixtures := map[string]string{
		filepath.Join(home, ".claude", "settings.json"): `{"model":"opus"}`,
		filepath.Join(home, ".claude.json"):             `{"mcpServers":{"tuios":{"type":"stdio","command":"tuios","args":["mcp"]}}}`,
		filepath.Join(home, ".codex", "models_cache.json"): `{
			"fetched_at": "2026-10-01T00:00:00Z",
			"models": [{"slug":"codex-pro","display_name":"Codex Pro","context_window":200000,"supported_reasoning_levels":[{"effort":"high","description":"Deep"}]}]
		}`,
		filepath.Join(home, ".claude", "skills", "review", "SKILL.md"): "---\nname: review\ndescription: Smoke review skill.\n---\nCheck diffs carefully.\n",
	}
	for path, content := range fixtures {
		if err := writeSmokeFile(path, content); err != nil {
			return err
		}
	}

	env := []string{"XDG_CACHE_HOME=" + cache, "HOME=" + home, "PATH="}
	tool := &Tool{
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Env:    env,
		Home:   home,
		Cwd:    home,
		Prog:   "o-ai",
	}
	opts := tool.defaultOptions()
	opts.Home = home
	opts.Project = home
	opts.NoProbe = true

	sh, initCmd := newShell(newUI(tool, UIConfig{Options: opts, InitialView: viewHarnesses}))
	d := newDriver(sh, initCmd, width, height)

	frame := plain(sh.View().Content)
	for _, want := range []string{"o-ai", "Harnesses", "claude", "codex"} {
		if !strings.Contains(frame, want) {
			return fmt.Errorf("smoke frame missing %q", want)
		}
	}

	// Exercise views, filter + Readline, sort, group, palette, form, help.
	d.keys("2") // Models view
	modelsFrame := plain(sh.View().Content)
	if !strings.Contains(modelsFrame, "Codex Pro") {
		return errors.New("smoke models view missing Codex Pro fixture")
	}
	d.keys("4") // Skills view
	skillsFrame := plain(sh.View().Content)
	if !strings.Contains(skillsFrame, "review") {
		return errors.New("smoke skills view missing review fixture")
	}
	d.keys("6") // MCP view
	mcpFrame := plain(sh.View().Content)
	if !strings.Contains(mcpFrame, "tuios") {
		return errors.New("smoke mcp view missing tuios fixture")
	}
	d.keys("1") // Back to Harnesses
	d.keys("/")
	d.typeText("clau")
	d.keys("ctrl+w", "esc")
	d.keys("o", "O")
	d.keys("g", "b", "g", "b")
	d.keys(":", "esc")
	d.keys("a", "esc")
	d.keys("?", "esc")

	if dump {
		_, err = fmt.Fprintln(out, strings.TrimRight(frame, "\n"))
		return err
	}
	_, err = fmt.Fprintf(out, "smoke ok: harnesses/models/skills/mcp views, filter/readline, sort/group, palette/form/help verified\n")
	return err
}
