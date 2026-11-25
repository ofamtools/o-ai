package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ofam.dev/o-ai/harness"
)

// SessionEvent is one normalized canonical event from any supported AI harness session log.
type SessionEvent struct {
	Index      int    `json:"index"`
	Timestamp  string `json:"timestamp,omitempty"`
	Harness    string `json:"harness"`
	Role       string `json:"role"`
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	ToolInput  any    `json:"toolInput,omitempty"`
	ToolOutput string `json:"toolOutput,omitempty"`
}

// sessionMeta holds lightweight metadata extracted during a bounded header peek.
type sessionMeta struct {
	SessionID string
	Project   string
	Cwd       string
	Title     string
	Preview   string
}

// sessionAdapter declaratively describes how one AI harness stores and encodes sessions.
// Adding a new harness is pure data: append one entry to sessionAdapters.
type sessionAdapter struct {
	Harness   string
	Roots     func(home string) []string
	Match     func(relPath, fullPath string, d fs.DirEntry) bool
	SessionID func(relPath, fullPath string) string
	Peek      func(path string) (sessionMeta, error)
	Parse     func(harnessName, path string, emit func(SessionEvent) bool) error
}

// sessionAdapters is the declarative registry of supported AI session harnesses.
var sessionAdapters = []sessionAdapter{
	{
		Harness: "claude",
		Roots: func(home string) []string {
			return []string{filepath.Join(sessionHarnessRoot("claude", home), "projects")}
		},
		Match: func(_, _ string, d fs.DirEntry) bool {
			return !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl")
		},
		SessionID: stemSessionID,
		Peek:      peekClaude,
		Parse:     parseClaude,
	},
	{
		Harness: "omp",
		Roots: func(home string) []string {
			return uniquePaths(
				filepath.Join(sessionHarnessRoot("omp", home), "sessions"),
				filepath.Join(home, ".omp", "sessions"),
			)
		},
		Match: func(_, _ string, d fs.DirEntry) bool {
			return !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") && d.Name() != "changes.jsonl"
		},
		SessionID: piOMPSessionID,
		Peek:      peekPiOMP,
		Parse:     parsePiOMP,
	},
	{
		Harness: "agy",
		Roots: func(home string) []string {
			return []string{filepath.Join(sessionHarnessRoot("agy", home), "brain")}
		},
		Match: func(relPath, _ string, d fs.DirEntry) bool {
			return !d.IsDir() && strings.HasSuffix(filepath.ToSlash(relPath), "/.system_generated/logs/transcript.jsonl")
		},
		SessionID: agySessionID,
		Peek:      peekAgy,
		Parse:     parseAgy,
	},
	{
		Harness: "codex",
		Roots: func(home string) []string {
			return []string{filepath.Join(sessionHarnessRoot("codex", home), "sessions")}
		},
		Match: func(_, _ string, d fs.DirEntry) bool {
			return !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl")
		},
		SessionID: stemSessionID,
		Peek:      peekCodex,
		Parse:     parseCodex,
	},
	{
		Harness: "pi",
		Roots: func(home string) []string {
			return uniquePaths(
				filepath.Join(sessionHarnessRoot("pi", home), "sessions"),
				filepath.Join(home, ".pi", "sessions"),
			)
		},
		Match: func(_, _ string, d fs.DirEntry) bool {
			return !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") && d.Name() != "changes.jsonl"
		},
		SessionID: piOMPSessionID,
		Peek:      peekPiOMP,
		Parse:     parsePiOMP,
	},
	{
		Harness: "opencode",
		Roots: func(home string) []string {
			base := sessionDataDir(home, "opencode")
			return uniquePaths(
				filepath.Join(base, "storage", "session"),
				filepath.Join(base, "sessions"),
			)
		},
		Match: func(_, _ string, d fs.DirEntry) bool {
			if d.IsDir() {
				return false
			}
			name := d.Name()
			if name == "auth.json" || name == "account.json" {
				return false
			}
			return strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".jsonl")
		},
		SessionID: stemSessionID,
		Peek:      peekOpenCode,
		Parse:     parseOpenCode,
	},
	{
		Harness: "matomeru",
		Roots: func(home string) []string {
			return []string{sessionDataDir(home, "matomeru")}
		},
		Match: func(relPath, _ string, d fs.DirEntry) bool {
			return !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") && !strings.HasPrefix(filepath.ToSlash(relPath), "backups/")
		},
		SessionID: stemSessionID,
		Peek:      peekMatomeru,
		Parse:     parseMatomeru,
	},
}

func sessionHarnessRoot(id, home string) string {
	userHome, _ := os.UserHomeDir()
	// When the caller explicitly isolated --home to a different directory,
	// ignore host environment overrides that would leak the real home.
	if userHome != "" && filepath.Clean(home) != filepath.Clean(userHome) {
		switch id {
		case "omp", "pi":
			return filepath.Join(home, "."+id, "agent")
		case "agy":
			return filepath.Join(home, ".gemini", "antigravity-cli")
		case "claude", "codex":
			return filepath.Join(home, "."+id)
		}
	}
	return harness.Root(id, home)
}

func sessionDataDir(home, name string) string {
	userHome, _ := os.UserHomeDir()
	if userHome == "" || filepath.Clean(home) == filepath.Clean(userHome) {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, name)
		}
	}
	return filepath.Join(home, ".local", "share", name)
}

func uniquePaths(paths ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		p = filepath.Clean(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func findSessionAdapter(name string) (sessionAdapter, bool) {
	for _, a := range sessionAdapters {
		if a.Harness == name {
			return a, true
		}
	}
	return sessionAdapter{}, false
}

func supportedSessionHarnesses() []string {
	out := make([]string, len(sessionAdapters))
	for i, a := range sessionAdapters {
		out[i] = a.Harness
	}
	return out
}

// detectSessionAdapter resolves an adapter for an arbitrary file path by path heuristics
// first, then by sniffing the first non-empty JSON record.
func detectSessionAdapter(path, hint string) (sessionAdapter, error) {
	if hint != "" {
		a, ok := findSessionAdapter(hint)
		if !ok {
			return sessionAdapter{}, fmt.Errorf("unknown harness %q (supported: %s)", hint, strings.Join(supportedSessionHarnesses(), ", "))
		}
		return a, nil
	}
	slashed := filepath.ToSlash(path)
	switch {
	case strings.Contains(slashed, "/.claude/"):
		return sessionAdapters[0], nil
	case strings.Contains(slashed, "/.omp/"):
		return sessionAdapters[1], nil
	case strings.Contains(slashed, "/antigravity-cli/"):
		return sessionAdapters[2], nil
	case strings.Contains(slashed, "/.codex/"):
		return sessionAdapters[3], nil
	case strings.Contains(slashed, "/.pi/"):
		return sessionAdapters[4], nil
	case strings.Contains(slashed, "/opencode/"):
		return sessionAdapters[5], nil
	case strings.Contains(slashed, "/matomeru/"):
		return sessionAdapters[6], nil
	}
	if strings.HasSuffix(path, ".json") {
		return sessionAdapters[5], nil
	}
	detected := "claude"
	_ = scanJSONL(path, 8, 64*1024, func(line []byte) bool {
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		switch text(obj, "type") {
		case "session_meta", "response_item", "event_msg", "turn_context":
			detected = "codex"
			return false
		case "SYSTEM_MESSAGE", "USER_INPUT", "PLANNER_RESPONSE", "GENERIC":
			detected = "agy"
			return false
		case "title", "session", "model_change":
			detected = "omp"
			return false
		case "header":
			detected = "matomeru"
			return false
		case "event":
			if text(obj, "kind") != "" {
				detected = "matomeru"
				return false
			}
		case "user", "assistant", "last-prompt":
			detected = "claude"
			return false
		}
		return true
	})
	a, _ := findSessionAdapter(detected)
	return a, nil
}

// --- session ID extractors -------------------------------------------------------

func stemSessionID(_, fullPath string) string {
	base := filepath.Base(fullPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func piOMPSessionID(_, fullPath string) string {
	stem := stemSessionID("", fullPath)
	if _, after, ok := strings.Cut(stem, "_"); ok && after != "" {
		return after
	}
	return stem
}

func agySessionID(relPath, fullPath string) string {
	parts := strings.Split(filepath.ToSlash(fullPath), "/")
	for i := len(parts) - 1; i >= 1; i-- {
		if parts[i] == ".system_generated" {
			return parts[i-1]
		}
	}
	return stemSessionID(relPath, fullPath)
}

// --- streaming JSONL scanner & emitter -------------------------------------------

const (
	peekMaxLines = 64
	peekMaxBytes = 256 * 1024
)

type eventEmitter struct {
	harness string
	emit    func(SessionEvent) bool
	idx     int
	stopped bool
}

func newEventEmitter(harnessName string, emit func(SessionEvent) bool) *eventEmitter {
	return &eventEmitter{harness: harnessName, emit: emit}
}

func (e *eventEmitter) send(ev SessionEvent) bool {
	if e.stopped {
		return false
	}
	ev.Index = e.idx
	ev.Harness = e.harness
	e.idx++
	if !e.emit(ev) {
		e.stopped = true
		return false
	}
	return true
}

func scanJSONL(path string, maxLines int, maxBytes int64, fn func(line []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	var totalBytes int64
	lines := 0
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			totalBytes += int64(len(line))
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				lines++
				if !fn(trimmed) {
					return nil
				}
			}
			if (maxLines > 0 && lines >= maxLines) || (maxBytes > 0 && totalBytes >= maxBytes) {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// --- shared content extraction helpers -------------------------------------------

func extractContentText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, item := range x {
			switch blk := item.(type) {
			case string:
				if blk != "" {
					parts = append(parts, blk)
				}
			case map[string]any:
				bt := text(blk, "type")
				if bt == "thinking" || bt == "tool_use" || bt == "toolCall" {
					continue
				}
				if s := text(blk, "text"); s != "" {
					parts = append(parts, s)
				} else if s := extractContentText(blk["content"]); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if s := text(x, "text"); s != "" {
			return s
		}
		if s := extractContentText(x["content"]); s != "" {
			return s
		}
	}
	return ""
}

// emitContentBlocks normalizes Anthropic/Pi/OMP style content strings or block slices.
func emitContentBlocks(em *eventEmitter, ts, role string, content any, toolNames map[string]string) bool {
	switch c := content.(type) {
	case string:
		if strings.TrimSpace(c) != "" {
			return em.send(SessionEvent{Timestamp: ts, Role: role, Kind: "message", Text: c})
		}
	case []any:
		for _, item := range c {
			blk := object(item)
			if blk == nil {
				continue
			}
			switch text(blk, "type") {
			case "text":
				if t := text(blk, "text"); strings.TrimSpace(t) != "" {
					if !em.send(SessionEvent{Timestamp: ts, Role: role, Kind: "message", Text: t}) {
						return false
					}
				}
			case "thinking":
				th := text(blk, "thinking")
				if th == "" {
					th = text(blk, "text")
				}
				if strings.TrimSpace(th) != "" {
					if !em.send(SessionEvent{Timestamp: ts, Role: role, Kind: "thinking", Thinking: th}) {
						return false
					}
				}
			case "tool_use", "toolCall":
				name := text(blk, "name")
				if id := text(blk, "id"); id != "" && name != "" {
					toolNames[id] = name
				}
				in := blk["input"]
				if in == nil {
					in = blk["arguments"]
				}
				if !em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "tool_call", ToolName: name, ToolInput: in}) {
					return false
				}
			case "tool_result", "toolResult":
				name := text(blk, "toolName")
				if name == "" {
					name = toolNames[text(blk, "tool_use_id")]
				}
				if name == "" {
					name = toolNames[text(blk, "toolCallId")]
				}
				out := extractContentText(blk["content"])
				if !em.send(SessionEvent{Timestamp: ts, Role: "tool", Kind: "tool_result", ToolName: name, ToolOutput: out}) {
					return false
				}
			}
		}
	}
	return !em.stopped
}

func cleanPreview(s string) string {
	s = strings.TrimSpace(oneLine(s))
	if r := []rune(s); len(r) > briefText {
		return string(r[:briefText-1]) + "…"
	}
	return s
}

func projectFromCwd(cwd string) string {
	cwd = strings.TrimRight(strings.TrimSpace(cwd), "/")
	if cwd == "" || cwd == "/" {
		return cwd
	}
	return filepath.Base(cwd)
}

func decodeClaudeProjectDir(dirName string) string {
	if strings.HasPrefix(dirName, "-") {
		return "/" + strings.ReplaceAll(strings.TrimPrefix(dirName, "-"), "-", "/")
	}
	return dirName
}

func formatTimestampAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x > 1e12 {
			return time.UnixMilli(int64(x)).UTC().Format(time.RFC3339)
		}
		if x > 1e9 {
			return time.Unix(int64(x), 0).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// --- Claude Code, OMP, and Pi adapters -------------------------------------------

func peekClaude(path string) (sessionMeta, error) {
	var meta sessionMeta
	err := scanJSONL(path, peekMaxLines, peekMaxBytes, func(line []byte) bool {
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		if meta.SessionID == "" {
			meta.SessionID = text(obj, "sessionId")
		}
		if meta.Cwd == "" {
			meta.Cwd = text(obj, "cwd")
		}
		if meta.Title == "" {
			if t := text(obj, "customTitle"); t != "" {
				meta.Title = t
			} else if t := text(obj, "title"); t != "" {
				meta.Title = t
			}
		}
		if meta.Preview == "" && text(obj, "type") == "user" {
			if msg := object(obj["message"]); msg != nil {
				if p := cleanPreview(extractContentText(msg["content"])); p != "" {
					meta.Preview = p
				}
			}
		}
		return meta.Cwd == "" || meta.Preview == ""
	})
	if meta.Cwd == "" {
		if parent := filepath.Base(filepath.Dir(path)); parent != "" && parent != "projects" {
			meta.Cwd = decodeClaudeProjectDir(parent)
		}
	}
	meta.Project = projectFromCwd(meta.Cwd)
	if meta.Title == "" {
		meta.Title = meta.Preview
	}
	return meta, err
}

func parseClaude(harnessName, path string, emit func(SessionEvent) bool) error {
	toolNames := map[string]string{}
	em := newEventEmitter(harnessName, emit)
	return scanJSONL(path, 0, 0, func(line []byte) bool {
		if em.stopped {
			return false
		}
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		typ := text(obj, "type")
		if typ != "user" && typ != "assistant" {
			return true
		}
		msg := object(obj["message"])
		if msg == nil {
			return true
		}
		role := text(msg, "role")
		if role == "" {
			role = typ
		}
		return emitContentBlocks(em, text(obj, "timestamp"), role, msg["content"], toolNames)
	})
}

func peekPiOMP(path string) (sessionMeta, error) {
	var meta sessionMeta
	err := scanJSONL(path, peekMaxLines, peekMaxBytes, func(line []byte) bool {
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		switch text(obj, "type") {
		case "title":
			if t := strings.TrimSpace(text(obj, "title")); t != "" {
				meta.Title = t
			}
		case "session":
			if meta.SessionID == "" {
				meta.SessionID = text(obj, "id")
			}
			if meta.Cwd == "" {
				meta.Cwd = text(obj, "cwd")
			}
		case "message":
			if meta.Preview == "" {
				if msg := object(obj["message"]); msg != nil && text(msg, "role") == "user" {
					if p := cleanPreview(extractContentText(msg["content"])); p != "" {
						meta.Preview = p
					}
				}
			}
		}
		return meta.Cwd == "" || meta.Preview == ""
	})
	meta.Project = projectFromCwd(meta.Cwd)
	if meta.Title == "" {
		meta.Title = meta.Preview
	}
	return meta, err
}

func parsePiOMP(harnessName, path string, emit func(SessionEvent) bool) error {
	toolNames := map[string]string{}
	em := newEventEmitter(harnessName, emit)
	return scanJSONL(path, 0, 0, func(line []byte) bool {
		if em.stopped {
			return false
		}
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil || text(obj, "type") != "message" {
			return true
		}
		msg := object(obj["message"])
		if msg == nil {
			return true
		}
		ts := text(obj, "timestamp")
		if ts == "" {
			ts = formatTimestampAny(msg["timestamp"])
		}
		role := text(msg, "role")
		if role == "toolResult" || role == "tool" {
			name := text(msg, "toolName")
			if name == "" {
				name = toolNames[text(msg, "toolCallId")]
			}
			return em.send(SessionEvent{Timestamp: ts, Role: "tool", Kind: "tool_result", ToolName: name, ToolOutput: extractContentText(msg["content"])})
		}
		return emitContentBlocks(em, ts, role, msg["content"], toolNames)
	})
}
