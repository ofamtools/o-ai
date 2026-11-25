package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ofam.dev/o-ai/harness"
)

// --- Antigravity (agy) adapter ---------------------------------------------------

func peekAgy(path string) (sessionMeta, error) {
	meta := sessionMeta{SessionID: agySessionID("", path)}
	var fallback string
	err := scanJSONL(path, peekMaxLines, peekMaxBytes, func(line []byte) bool {
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		switch text(obj, "type") {
		case "USER_INPUT":
			if p := cleanPreview(text(obj, "content")); p != "" {
				meta.Preview = p
				meta.Title = p
				return false
			}
		case "SYSTEM_MESSAGE":
			if fallback == "" {
				fallback = cleanPreview(text(obj, "content"))
			}
		}
		return true
	})
	if meta.Preview == "" {
		meta.Preview = fallback
		meta.Title = fallback
	}
	return meta, err
}

func parseAgy(harnessName, path string, emit func(SessionEvent) bool) error {
	var pendingTools []string
	em := newEventEmitter(harnessName, emit)
	return scanJSONL(path, 0, 0, func(line []byte) bool {
		if em.stopped {
			return false
		}
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		ts := text(obj, "created_at")
		if ts == "" {
			ts = text(obj, "timestamp")
		}
		switch text(obj, "type") {
		case "USER_INPUT":
			if content := text(obj, "content"); strings.TrimSpace(content) != "" {
				return em.send(SessionEvent{Timestamp: ts, Role: "user", Kind: "message", Text: content})
			}
		case "SYSTEM_MESSAGE":
			if content := text(obj, "content"); strings.TrimSpace(content) != "" {
				return em.send(SessionEvent{Timestamp: ts, Role: "system", Kind: "system", Text: content})
			}
		case "PLANNER_RESPONSE":
			if th := text(obj, "thinking"); strings.TrimSpace(th) != "" {
				if !em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "thinking", Thinking: th}) {
					return false
				}
			}
			resp := text(obj, "response")
			if resp == "" {
				resp = text(obj, "content")
			}
			if strings.TrimSpace(resp) != "" {
				if !em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "message", Text: resp}) {
					return false
				}
			}
			for _, rawTC := range array(obj["tool_calls"]) {
				tc := object(rawTC)
				if tc == nil {
					continue
				}
				name := text(tc, "name")
				pendingTools = append(pendingTools, name)
				if !em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "tool_call", ToolName: name, ToolInput: tc["args"]}) {
					return false
				}
			}
		case "GENERIC":
			content := text(obj, "content")
			if strings.TrimSpace(content) == "" {
				return true
			}
			toolName := text(obj, "tool_name")
			if toolName == "" && len(pendingTools) > 0 {
				toolName = pendingTools[0]
				pendingTools = pendingTools[1:]
			}
			if toolName != "" {
				return em.send(SessionEvent{Timestamp: ts, Role: "tool", Kind: "tool_result", ToolName: toolName, ToolOutput: content})
			}
			return em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "message", Text: content})
		}
		return !em.stopped
	})
}

// --- Codex adapter ---------------------------------------------------------------

func isCodexInjectedUser(payload map[string]any) bool {
	meta := object(payload["internal_chat_message_metadata_passthrough"])
	if meta == nil {
		return false
	}
	kinds := stringList(meta["content_item_kinds"])
	if len(kinds) == 0 {
		return false
	}
	for _, k := range kinds {
		if k == "user.text" || k == "unknown" {
			return false
		}
	}
	return true
}

func peekCodex(path string) (sessionMeta, error) {
	var meta sessionMeta
	err := scanJSONL(path, peekMaxLines, peekMaxBytes, func(line []byte) bool {
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		payload := object(obj["payload"])
		if payload == nil {
			return true
		}
		switch text(obj, "type") {
		case "session_meta":
			if meta.SessionID == "" {
				if id := text(payload, "session_id"); id != "" {
					meta.SessionID = id
				} else {
					meta.SessionID = text(payload, "id")
				}
			}
			if meta.Cwd == "" {
				meta.Cwd = text(payload, "cwd")
			}
		case "turn_context":
			if meta.Cwd == "" {
				meta.Cwd = text(payload, "cwd")
			}
		case "response_item":
			if meta.Preview == "" && text(payload, "type") == "message" && text(payload, "role") == "user" && !isCodexInjectedUser(payload) {
				if p := cleanPreview(extractContentText(payload["content"])); p != "" {
					meta.Preview = p
				}
			}
		case "event_msg":
			if meta.Preview == "" {
				switch text(payload, "type") {
				case "user_message":
					if p := cleanPreview(text(payload, "message")); p != "" {
						meta.Preview = p
					}
				case "item_completed":
					if item := object(payload["item"]); item != nil && text(item, "type") == "UserMessage" {
						if p := cleanPreview(extractContentText(item["content"])); p != "" {
							meta.Preview = p
						}
					}
				}
			}
		}
		return meta.Cwd == "" || meta.Preview == ""
	})
	meta.Project = projectFromCwd(meta.Cwd)
	meta.Title = meta.Preview
	return meta, err
}

func parseCodex(harnessName, path string, emit func(SessionEvent) bool) error {
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
		ts := text(obj, "timestamp")
		payload := object(obj["payload"])
		if payload == nil {
			return true
		}
		switch text(obj, "type") {
		case "response_item":
			switch text(payload, "type") {
			case "message":
				role := text(payload, "role")
				if role == "developer" || (role == "user" && isCodexInjectedUser(payload)) {
					role = "system"
				}
				txt := extractContentText(payload["content"])
				if strings.TrimSpace(txt) == "" {
					return true
				}
				kind := "message"
				if role == "system" {
					kind = "system"
				}
				return em.send(SessionEvent{Timestamp: ts, Role: role, Kind: kind, Text: txt})
			case "reasoning":
				th := extractContentText(payload["summary"])
				if th == "" {
					th = text(payload, "text")
				}
				if strings.TrimSpace(th) != "" {
					return em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "thinking", Thinking: th})
				}
			case "function_call", "custom_tool_call":
				name := text(payload, "name")
				if id := text(payload, "call_id"); id != "" && name != "" {
					toolNames[id] = name
				}
				in := payload["arguments"]
				if in == nil {
					in = payload["input"]
				}
				return em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "tool_call", ToolName: name, ToolInput: in})
			case "function_call_output", "custom_tool_call_output":
				name := toolNames[text(payload, "call_id")]
				return em.send(SessionEvent{Timestamp: ts, Role: "tool", Kind: "tool_result", ToolName: name, ToolOutput: extractContentText(payload["output"])})
			}
		case "event_msg":
			if text(payload, "type") == "user_message" {
				if msg := text(payload, "message"); strings.TrimSpace(msg) != "" {
					return em.send(SessionEvent{Timestamp: ts, Role: "user", Kind: "message", Text: msg})
				}
			}
		}
		return !em.stopped
	})
}

// --- OpenCode adapter ------------------------------------------------------------

func peekOpenCode(path string) (sessionMeta, error) {
	if strings.HasSuffix(path, ".jsonl") {
		return peekGenericJSONL(path)
	}
	b, err := harness.ReadBounded(path)
	if err != nil {
		return sessionMeta{}, err
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return sessionMeta{}, err
	}
	cwd := text(obj, "directory")
	if cwd == "" {
		cwd = text(obj, "cwd")
	}
	title := cleanPreview(text(obj, "title"))
	return sessionMeta{
		SessionID: text(obj, "id"),
		Cwd:       cwd,
		Project:   projectFromCwd(cwd),
		Title:     title,
		Preview:   title,
	}, nil
}

func parseOpenCode(harnessName, path string, emit func(SessionEvent) bool) error {
	if strings.HasSuffix(path, ".jsonl") {
		return parseGenericJSONL(harnessName, path, emit)
	}
	b, err := harness.ReadBounded(path)
	if err != nil {
		return err
	}
	var sess map[string]any
	if err := json.Unmarshal(b, &sess); err != nil {
		return err
	}
	em := newEventEmitter(harnessName, emit)

	// Self-contained JSON export with embedded messages array.
	if msgs := array(sess["messages"]); len(msgs) > 0 {
		for _, raw := range msgs {
			if em.stopped {
				return nil
			}
			if m := object(raw); m != nil {
				emitGenericObject(m, em)
			}
		}
		return nil
	}

	// Standard OpenCode storage layout:
	//   storage/session/<projectID>/<sessionID>.json
	//   storage/message/<sessionID>/msg_*.json
	//   storage/part/<messageID>/prt_*.json
	sessionID := text(sess, "id")
	if sessionID == "" {
		sessionID = stemSessionID("", path)
	}
	storageRoot := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	msgDir := filepath.Join(storageRoot, "message", sessionID)
	msgEntries, err := os.ReadDir(msgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	type ocMsg struct {
		id      string
		role    string
		created float64
		ts      string
	}
	var messages []ocMsg
	for _, e := range msgEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		mb, err := harness.ReadBounded(filepath.Join(msgDir, e.Name()))
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(mb, &m) != nil {
			continue
		}
		created, _ := object(m["time"])["created"].(float64)
		messages = append(messages, ocMsg{
			id:      text(m, "id"),
			role:    text(m, "role"),
			created: created,
			ts:      formatTimestampAny(created),
		})
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].created != messages[j].created {
			return messages[i].created < messages[j].created
		}
		return messages[i].id < messages[j].id
	})

	for _, m := range messages {
		if em.stopped {
			return nil
		}
		partDir := filepath.Join(storageRoot, "part", m.id)
		partEntries, err := os.ReadDir(partDir)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(partEntries))
		for _, pe := range partEntries {
			if !pe.IsDir() && strings.HasSuffix(pe.Name(), ".json") {
				names = append(names, pe.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if em.stopped {
				return nil
			}
			pb, err := harness.ReadBounded(filepath.Join(partDir, name))
			if err != nil {
				continue
			}
			var part map[string]any
			if json.Unmarshal(pb, &part) != nil {
				continue
			}
			switch text(part, "type") {
			case "text":
				if txt := text(part, "text"); strings.TrimSpace(txt) != "" {
					em.send(SessionEvent{Timestamp: m.ts, Role: m.role, Kind: "message", Text: txt})
				}
			case "reasoning", "thinking":
				th := text(part, "text")
				if th == "" {
					th = text(part, "thinking")
				}
				if strings.TrimSpace(th) != "" {
					em.send(SessionEvent{Timestamp: m.ts, Role: "assistant", Kind: "thinking", Thinking: th})
				}
			case "tool":
				toolName := text(part, "tool")
				state := object(part["state"])
				var input any
				var output string
				if state != nil {
					input = state["input"]
					output = extractContentText(state["output"])
				}
				if !em.send(SessionEvent{Timestamp: m.ts, Role: "assistant", Kind: "tool_call", ToolName: toolName, ToolInput: input}) {
					return nil
				}
				if strings.TrimSpace(output) != "" {
					em.send(SessionEvent{Timestamp: m.ts, Role: "tool", Kind: "tool_result", ToolName: toolName, ToolOutput: output})
				}
			}
		}
	}
	return nil
}

// --- Matomeru & generic JSONL adapters -------------------------------------------

func peekMatomeru(path string) (sessionMeta, error) {
	var meta sessionMeta
	err := scanJSONL(path, peekMaxLines, peekMaxBytes, func(line []byte) bool {
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		if meta.Cwd == "" {
			meta.Cwd = text(obj, "cwd")
		}
		if meta.Project == "" {
			meta.Project = text(obj, "project")
		}
		if text(obj, "type") == "header" {
			if app := text(obj, "app"); meta.Title == "" && app != "" {
				if format := text(obj, "format"); format != "" {
					meta.Title = app + " (" + format + ")"
				} else {
					meta.Title = app
				}
			}
		} else if meta.Preview == "" {
			if text(obj, "role") == "user" {
				txt := text(obj, "text")
				if txt == "" {
					txt = extractContentText(obj["content"])
				}
				meta.Preview = cleanPreview(txt)
			} else if text(obj, "type") == "event" && text(obj, "kind") == "command" {
				meta.Preview = text(obj, "command")
			}
		}
		return meta.Title == "" || meta.Preview == ""
	})
	if meta.Project == "" {
		meta.Project = projectFromCwd(meta.Cwd)
	}
	if meta.Title == "" {
		meta.Title = meta.Preview
	}
	if meta.Preview == "" {
		meta.Preview = meta.Title
	}
	return meta, err
}

func parseMatomeru(harnessName, path string, emit func(SessionEvent) bool) error {
	em := newEventEmitter(harnessName, emit)
	return scanJSONL(path, 0, 0, func(line []byte) bool {
		if em.stopped {
			return false
		}
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		switch text(obj, "type") {
		case "header":
			summary := strings.TrimSpace(strings.Join([]string{text(obj, "app"), text(obj, "version"), text(obj, "format")}, " "))
			if summary != "" {
				return em.send(SessionEvent{Timestamp: text(obj, "startedAt"), Role: "system", Kind: "system", Text: summary})
			}
			return true
		case "event":
			ts := text(obj, "at")
			if ts == "" {
				ts = text(obj, "timestamp")
			}
			switch text(obj, "kind") {
			case "frame", "resize":
				return true
			case "command":
				cmd := text(obj, "command")
				page, mode := text(obj, "page"), text(obj, "mode")
				desc := cmd
				if page != "" || mode != "" {
					desc = fmt.Sprintf("%s (mode=%s page=%s)", cmd, mode, page)
				}
				return em.send(SessionEvent{Timestamp: ts, Role: "user", Kind: "event", ToolName: cmd, Text: desc})
			case "key":
				k := text(obj, "key")
				if k == "" {
					k = text(obj, "text")
				}
				if k != "" {
					return em.send(SessionEvent{Timestamp: ts, Role: "user", Kind: "event", Text: "key: " + k})
				}
				return true
			}
		}
		emitGenericObject(obj, em)
		return !em.stopped
	})
}

func peekGenericJSONL(path string) (sessionMeta, error) {
	var meta sessionMeta
	err := scanJSONL(path, peekMaxLines, peekMaxBytes, func(line []byte) bool {
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		if meta.Cwd == "" {
			meta.Cwd = text(obj, "cwd")
		}
		if meta.Title == "" {
			meta.Title = text(obj, "title")
		}
		if meta.Preview == "" && text(obj, "role") == "user" {
			txt := text(obj, "text")
			if txt == "" {
				txt = extractContentText(obj["content"])
			}
			meta.Preview = cleanPreview(txt)
		}
		return meta.Cwd == "" || meta.Preview == ""
	})
	meta.Project = projectFromCwd(meta.Cwd)
	if meta.Title == "" {
		meta.Title = meta.Preview
	}
	return meta, err
}

func parseGenericJSONL(harnessName, path string, emit func(SessionEvent) bool) error {
	em := newEventEmitter(harnessName, emit)
	return scanJSONL(path, 0, 0, func(line []byte) bool {
		if em.stopped {
			return false
		}
		var obj map[string]any
		if json.Unmarshal(line, &obj) != nil {
			return true
		}
		emitGenericObject(obj, em)
		return !em.stopped
	})
}

func emitGenericObject(obj map[string]any, em *eventEmitter) {
	role := text(obj, "role")
	kind := text(obj, "kind")
	ts := text(obj, "timestamp")
	if ts == "" {
		ts = text(obj, "at")
	}
	if th := text(obj, "thinking"); strings.TrimSpace(th) != "" {
		if !em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "thinking", Thinking: th}) {
			return
		}
	}
	if tn := text(obj, "toolName"); tn != "" {
		if kind == "tool_result" || text(obj, "toolOutput") != "" {
			em.send(SessionEvent{Timestamp: ts, Role: "tool", Kind: "tool_result", ToolName: tn, ToolOutput: text(obj, "toolOutput")})
			return
		}
		em.send(SessionEvent{Timestamp: ts, Role: "assistant", Kind: "tool_call", ToolName: tn, ToolInput: obj["toolInput"]})
		return
	}
	txt := text(obj, "text")
	if txt == "" {
		txt = extractContentText(obj["content"])
	}
	if role != "" && strings.TrimSpace(txt) != "" {
		if kind == "" {
			kind = "message"
			if role == "system" {
				kind = "system"
			}
		}
		em.send(SessionEvent{Timestamp: ts, Role: role, Kind: kind, Text: txt})
	}
}
