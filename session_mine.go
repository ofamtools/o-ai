package main

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// MineWindow is one compact (assistant -> user pushback -> resolution) candidate window
// extracted deterministically without any LLM call.
type MineWindow struct {
	SessionID       string `json:"sessionId"`
	Harness         string `json:"harness"`
	Path            string `json:"path"`
	Project         string `json:"project,omitempty"`
	Cwd             string `json:"cwd,omitempty"`
	Timestamp       string `json:"timestamp,omitempty"`
	TurnIndex       int    `json:"turnIndex"`
	MatchedTrigger  string `json:"matchedTrigger"`
	AssistantBefore string `json:"assistantBefore,omitempty"`
	UserPushback    string `json:"userPushback"`
	Resolution      string `json:"resolution,omitempty"`
}

type sessionMineOpts struct {
	Home    string
	Harness string
	Limit   int
	Since   time.Duration
	Pattern string
	Format  string // "jsonl", "json"
	Now     time.Time
}

// defaultPushbackPattern matches common human pushback, correction, or architectural
// steering turns in coding agent transcripts.
var defaultPushbackPattern = regexp.MustCompile(`(?i)\b(no[,!.\s]|nope|stop|don'?t|do not|never|wrong|incorrect|actually[, ]|wait[,!.\s]|hold on|instead|rather than|not what i|revert|undo|scratch that|too complex|over-?engineer|special[- ]case|hack|workaround|simplify|keep it simple|why did you|that broke|doesn'?t work|still broken|should be|shouldn'?t|must not|prefer|better to)\b`)

const (
	mineMaxTurnRunes   = 1200
	mineMaxBlobRunes   = 8000
	mineMinPushbackLen = 3
)

type convTurn struct {
	role      string
	text      string
	timestamp string
	index     int
}

func mineSessions(w io.Writer, opts sessionMineOpts) error {
	pat := defaultPushbackPattern
	customPattern := false
	if strings.TrimSpace(opts.Pattern) != "" {
		var err error
		pat, err = regexp.Compile("(?i)" + opts.Pattern)
		if err != nil {
			return fmt.Errorf("invalid --pattern %q: %w", opts.Pattern, err)
		}
		customPattern = true
	}

	files, err := discoverSessionFiles(sessionListOpts{
		Home:    opts.Home,
		Harness: opts.Harness,
		Since:   opts.Since,
		Now:     opts.Now,
	})
	if err != nil {
		return err
	}

	var windows []MineWindow
	var enc *json.Encoder
	if opts.Format == "jsonl" {
		enc = json.NewEncoder(w)
		enc.SetEscapeHTML(false)
	}
	emitted := 0

	for _, f := range files {
		if opts.Limit > 0 && emitted >= opts.Limit {
			break
		}
		// Stream session while stripping thinking, tool_call, tool_result, and system events.
		var turns []convTurn
		_ = f.adapter.Parse(f.adapter.Harness, f.path, func(ev SessionEvent) bool {
			if ev.Kind != "message" || (ev.Role != "user" && ev.Role != "assistant") {
				return true
			}
			txt := strings.TrimSpace(ev.Text)
			if txt == "" {
				return true
			}
			if len(turns) > 0 && turns[len(turns)-1].role == ev.Role {
				prev := &turns[len(turns)-1]
				if len([]rune(prev.text)) < mineMaxBlobRunes {
					prev.text += "\n" + txt
				}
				return true
			}
			turns = append(turns, convTurn{
				role:      ev.Role,
				text:      txt,
				timestamp: ev.Timestamp,
				index:     ev.Index,
			})
			return true
		})

		if len(turns) == 0 {
			continue
		}

		// Defer metadata peek until a session actually yields at least one window.
		var meta sessionMeta
		var metaLoaded bool

		for i, turn := range turns {
			if opts.Limit > 0 && emitted >= opts.Limit {
				break
			}
			if turn.role != "user" {
				continue
			}
			rlen := len([]rune(turn.text))
			if !customPattern && (rlen < mineMinPushbackLen || rlen > mineMaxBlobRunes) {
				continue
			}
			match := pat.FindString(turn.text)
			if match == "" {
				continue
			}

			var before, after string
			for j := i - 1; j >= 0; j-- {
				if turns[j].role == "assistant" {
					before = truncateRunes(turns[j].text, mineMaxTurnRunes)
					break
				}
			}
			for j := i + 1; j < len(turns); j++ {
				if turns[j].role == "assistant" {
					after = truncateRunes(turns[j].text, mineMaxTurnRunes)
					break
				}
			}

			if !metaLoaded {
				meta, _ = f.adapter.Peek(f.path)
				metaLoaded = true
			}
			sid := meta.SessionID
			if sid == "" {
				sid = f.adapter.SessionID(f.relPath, f.path)
			}

			win := MineWindow{
				SessionID:       f.adapter.Harness + ":" + sid,
				Harness:         f.adapter.Harness,
				Path:            f.path,
				Project:         meta.Project,
				Cwd:             meta.Cwd,
				Timestamp:       turn.timestamp,
				TurnIndex:       turn.index,
				MatchedTrigger:  strings.TrimSpace(match),
				AssistantBefore: before,
				UserPushback:    truncateRunes(turn.text, mineMaxTurnRunes),
				Resolution:      after,
			}
			if enc != nil {
				if err := enc.Encode(win); err != nil {
					return err
				}
			} else {
				windows = append(windows, win)
			}
			emitted++
		}
	}

	if opts.Format == "json" {
		if windows == nil {
			windows = []MineWindow{}
		}
		jenc := json.NewEncoder(w)
		jenc.SetEscapeHTML(false)
		jenc.SetIndent("", "  ")
		return jenc.Encode(windows)
	}
	return nil
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
