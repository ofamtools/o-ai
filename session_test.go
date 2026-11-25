package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolatedSessionHome(t *testing.T) string {
	t.Helper()
	home := isolatedHome(t)
	t.Setenv("XDG_DATA_HOME", "")
	return home
}

func runSessionCLI(t *testing.T, now time.Time, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	tool := &Tool{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errOut,
		Env:    os.Environ(),
		Home:   home,
		Cwd:    cwd,
		Prog:   "o-ai",
		now:    func() time.Time { return now },
	}
	err := tool.Main(context.Background(), args)
	return out.String(), errOut.String(), err
}

func putWithMtime(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	put(t, path, content)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

type fixturePaths struct {
	claude   string
	omp      string
	agy      string
	codex    string
	pi       string
	opencode string
	matomeru string
}

// buildAllHarnessFixtures populates isolated synthetic logs for all 7 harnesses
// with distinct modification timestamps so ordering and --since are deterministic.
func buildAllHarnessFixtures(t *testing.T, home string, base time.Time) fixturePaths {
	t.Helper()

	// 1. Claude Code (newest: base - 10m)
	claudePath := filepath.Join(home, ".claude", "projects", "-Users-dev-proj-alpha", "claude-001.jsonl")
	claudeLog := strings.Join([]string{
		`{"type":"last-prompt","sessionId":"claude-001"}`,
		`{"type":"user","sessionId":"claude-001","cwd":"/Users/dev/proj/alpha","timestamp":"2026-11-24T10:00:00Z","message":{"role":"user","content":"Refactor the parser cleanly"}}`,
		`{"type":"assistant","sessionId":"claude-001","timestamp":"2026-11-24T10:00:05Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Secret chain of thought about parser"},{"type":"text","text":"I will add a special-case switch for each format."},{"type":"tool_use","id":"tu1","name":"Edit","input":{"file":"parser.go"}},{"type":"tool_use","id":"tu2","name":"Bash","input":{"cmd":"go test"}}]}}`,
		`{"type":"user","sessionId":"claude-001","timestamp":"2026-11-24T10:00:08Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"NOISY_TOOL_OUTPUT_EDIT_OK"},{"type":"tool_result","tool_use_id":"tu2","content":"NOISY_TOOL_OUTPUT_BASH_PASS"}]}}`,
		`{"type":"user","sessionId":"claude-001","timestamp":"2026-11-24T10:00:15Z","message":{"role":"user","content":"No, don't add a special-case switch. Use a declarative adapter table instead."}}`,
		`{"type":"assistant","sessionId":"claude-001","timestamp":"2026-11-24T10:00:25Z","message":{"role":"assistant","content":[{"type":"text","text":"Replaced the switch with a declarative adapter slice."}]}}`,
	}, "\n") + "\n"
	putWithMtime(t, claudePath, claudeLog, base.Add(-10*time.Minute))

	// 2. OMP (base - 20m) + sidecar changes.jsonl that must be ignored
	ompPath := filepath.Join(home, ".omp", "agent", "sessions", "-proj-beta", "2026-11-24T09-00-00Z_omp-001.jsonl")
	ompLog := strings.Join([]string{
		`{"type":"title","v":1,"title":"OMP table layout"}`,
		`{"type":"session","version":3,"id":"omp-001","timestamp":"2026-11-24T09:00:00Z","cwd":"/Users/dev/proj/beta"}`,
		`{"type":"message","timestamp":"2026-11-24T09:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Build the beta table"}]}}`,
		`{"type":"message","timestamp":"2026-11-24T09:00:02Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"OMP hidden reasoning"},{"type":"toolCall","id":"c1","name":"write","arguments":{"path":"table.go"}},{"type":"text","text":"Wrote hardcoded table borders."}]}}`,
		`{"type":"message","timestamp":"2026-11-24T09:00:03Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"write","content":[{"type":"text","text":"OMP_NOISY_WRITE_RESULT"}]}}`,
		`{"type":"message","timestamp":"2026-11-24T09:00:10Z","message":{"role":"user","content":"Stop, that is too complex. Simplify the borders."}}`,
		`{"type":"message","timestamp":"2026-11-24T09:00:20Z","message":{"role":"assistant","content":[{"type":"text","text":"Simplified to whitespace separation."}]}}`,
	}, "\n") + "\n"
	putWithMtime(t, ompPath, ompLog, base.Add(-20*time.Minute))
	putWithMtime(t, filepath.Join(home, ".omp", "agent", "sessions", "-proj-beta", "omp-001", "changes.jsonl"), `{"type":"diff"}`+"\n", base.Add(-5*time.Minute))

	// 3. Antigravity / agy (base - 30m) + chunks copy that must be ignored
	agyPath := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "agy-uuid-001", ".system_generated", "logs", "transcript.jsonl")
	agyLog := strings.Join([]string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-24T08:00:00Z","content":"Audit the session scanner"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T08:00:05Z","thinking":"Agy internal thought","response":"Checking files now.","tool_calls":[{"name":"read_file","args":{"Path":"session.go"}}]}`,
		`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-24T08:00:06Z","content":"AGY_TOOL_FILE_CONTENTS_BLAH"}`,
		`{"step_index":3,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-24T08:00:12Z","content":"Wait, actually check streaming memory too."}`,
		`{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-24T08:00:20Z","response":"Verified O(1) line streaming."}`,
	}, "\n") + "\n"
	putWithMtime(t, agyPath, agyLog, base.Add(-30*time.Minute))
	putWithMtime(t, filepath.Join(home, ".gemini", "antigravity-cli", "brain", "agy-uuid-001", ".system_generated", "logs", "chunks", "transcript", "00000000.jsonl"), agyLog, base.Add(-1*time.Minute))

	// 4. Codex (base - 40m)
	codexPath := filepath.Join(home, ".codex", "sessions", "2026", "11", "24", "rollout-2026-11-24T07-00-00-codex-001.jsonl")
	codexLog := strings.Join([]string{
		`{"timestamp":"2026-11-24T07:00:00Z","type":"session_meta","payload":{"session_id":"codex-001","cwd":"/Users/dev/proj/gamma"}}`,
		`{"timestamp":"2026-11-24T07:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md injected"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["agents_md.instructions"]}}}`,
		`{"timestamp":"2026-11-24T07:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Implement codex streaming"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.text"]}}}`,
		`{"timestamp":"2026-11-24T07:00:03Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"Codex reasoning summary"}]}}`,
		`{"timestamp":"2026-11-24T07:00:04Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"call_1","name":"exec","input":"go test ./..."}}`,
		`{"timestamp":"2026-11-24T07:00:05Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_1","output":[{"type":"input_text","text":"CODEX_TOOL_OUTPUT_OK"}]}}`,
		`{"timestamp":"2026-11-24T07:00:06Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Codex streaming implemented."}]}}`,
	}, "\n") + "\n"
	putWithMtime(t, codexPath, codexLog, base.Add(-40*time.Minute))

	// 5. Pi (base - 50m)
	piPath := filepath.Join(home, ".pi", "agent", "sessions", "--Users-dev-proj-delta--", "2026-11-24T06-00-00Z_pi-001.jsonl")
	piLog := strings.Join([]string{
		`{"type":"session","version":3,"id":"pi-001","timestamp":"2026-11-24T06:00:00Z","cwd":"/Users/dev/proj/delta"}`,
		`{"type":"message","timestamp":"2026-11-24T06:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Check delta build"}]}}`,
		`{"type":"message","timestamp":"2026-11-24T06:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Delta build passes."}]}}`,
	}, "\n") + "\n"
	putWithMtime(t, piPath, piLog, base.Add(-50*time.Minute))

	// 6. OpenCode (base - 2h) + auth.json that must be ignored
	ocStorage := filepath.Join(home, ".local", "share", "opencode", "storage")
	ocSessPath := filepath.Join(ocStorage, "session", "projhash1", "ses_oc001.json")
	putWithMtime(t, ocSessPath, `{"id":"ses_oc001","directory":"/Users/dev/proj/epsilon","title":"OpenCode refactor","time":{"created":1795400000000,"updated":1795400100000}}`, base.Add(-2*time.Hour))
	put(t, filepath.Join(ocStorage, "message", "ses_oc001", "msg_1.json"), `{"id":"msg_1","sessionID":"ses_oc001","role":"user","time":{"created":1795400001000}}`)
	put(t, filepath.Join(ocStorage, "message", "ses_oc001", "msg_2.json"), `{"id":"msg_2","sessionID":"ses_oc001","role":"assistant","time":{"created":1795400002000}}`)
	put(t, filepath.Join(ocStorage, "part", "msg_1", "prt_1.json"), `{"id":"prt_1","messageID":"msg_1","type":"text","text":"Inspect epsilon"}`)
	put(t, filepath.Join(ocStorage, "part", "msg_2", "prt_1.json"), `{"id":"prt_1","messageID":"msg_2","type":"reasoning","text":"OpenCode deep thought"}`)
	put(t, filepath.Join(ocStorage, "part", "msg_2", "prt_2.json"), `{"id":"prt_2","messageID":"msg_2","type":"tool","tool":"edit","state":{"status":"completed","input":{"file":"e.go"},"output":"OC_EDIT_DONE"}}`)
	put(t, filepath.Join(ocStorage, "part", "msg_2", "prt_3.json"), `{"id":"prt_3","messageID":"msg_2","type":"text","text":"Updated e.go."}`)
	putWithMtime(t, filepath.Join(home, ".local", "share", "opencode", "auth.json"), `{"openai":{"key":"sk-secret-do-not-list"}}`, base.Add(-1*time.Minute))

	// 7. Matomeru (oldest: base - 48h) + backup jsonl that must be ignored
	mtmPath := filepath.Join(home, ".local", "share", "matomeru", "recordings", "mtm-001.jsonl")
	mtmLog := strings.Join([]string{
		`{"type":"header","format":"o-sandbox/v1","app":"matomeru","version":"0.4.0","startedAt":"2026-11-22T10:00:00Z","cwd":"/Users/dev/proj/matomeru"}`,
		`{"type":"event","seq":1,"at":"2026-11-22T10:00:01Z","kind":"frame","w":120,"h":40}`,
		`{"type":"event","seq":2,"at":"2026-11-22T10:00:02Z","kind":"command","command":"motion.down","mode":"normal","page":"lanes"}`,
		`{"role":"user","timestamp":"2026-11-22T10:00:05Z","text":"Wrong layout on lanes page, revert the border."}`,
		`{"role":"assistant","timestamp":"2026-11-22T10:00:10Z","text":"Reverted lane border."}`,
	}, "\n") + "\n"
	putWithMtime(t, mtmPath, mtmLog, base.Add(-48*time.Hour))
	putWithMtime(t, filepath.Join(home, ".local", "share", "matomeru", "backups", "ignore.jsonl"), `{"role":"user","text":"backup"}`+"\n", base.Add(-1*time.Minute))

	return fixturePaths{
		claude:   claudePath,
		omp:      ompPath,
		agy:      agyPath,
		codex:    codexPath,
		pi:       piPath,
		opencode: ocSessPath,
		matomeru: mtmPath,
	}
}

func TestSessionListDiscoveryFiltersAndFormats(t *testing.T) {
	home := isolatedSessionHome(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	buildAllHarnessFixtures(t, home, now)

	// Full list across all 7 harnesses, sorted newest-first.
	out, _, err := runSessionCLI(t, now, "session", "list", "--home", home)
	if err != nil {
		t.Fatalf("session list: %v", err)
	}
	rows, data := rowsOf[SessionSummary](t, out)
	if len(rows) != 7 {
		t.Fatalf("got %d sessions, want 7:\n%s", len(rows), out)
	}
	wantOrder := []string{"claude", "omp", "agy", "codex", "pi", "opencode", "matomeru"}
	for i, wantH := range wantOrder {
		if data[i].Harness != wantH {
			t.Errorf("row[%d] harness = %q, want %q", i, data[i].Harness, wantH)
		}
		if !strings.HasPrefix(rows[i].ID, "session/"+wantH+":") {
			t.Errorf("row[%d] id = %q", i, rows[i].ID)
		}
		if rows[i].Label == "" {
			t.Errorf("row[%d] empty label", i)
		}
		if data[i].SizeBytes <= 0 || data[i].Path == "" || data[i].ModTime.IsZero() {
			t.Errorf("row[%d] missing metadata: %+v", i, data[i])
		}
	}
	if data[0].Project != "alpha" || data[0].Cwd != "/Users/dev/proj/alpha" || data[0].Title != "Refactor the parser cleanly" {
		t.Errorf("claude summary mismatch: %+v", data[0])
	}
	if data[1].Title != "OMP table layout" || data[1].Project != "beta" {
		t.Errorf("omp summary mismatch: %+v", data[1])
	}
	if data[5].Title != "OpenCode refactor" || data[5].Project != "epsilon" {
		t.Errorf("opencode summary mismatch: %+v", data[5])
	}

	// Alias `sess` + --harness + --limit.
	out, _, err = runSessionCLI(t, now, "sess", "list", "--home", home, "--harness", "codex", "--limit", "5")
	if err != nil {
		t.Fatalf("sess list --harness codex: %v", err)
	}
	_, codexOnly := rowsOf[SessionSummary](t, out)
	if len(codexOnly) != 1 || codexOnly[0].Harness != "codex" || codexOnly[0].Project != "gamma" {
		t.Fatalf("codex filter: %+v", codexOnly)
	}

	// --limit 3.
	out, _, err = runSessionCLI(t, now, "sessions", "--home", home, "--limit", "3")
	if err != nil {
		t.Fatalf("sessions --limit 3: %v", err)
	}
	_, top3 := rowsOf[SessionSummary](t, out)
	if len(top3) != 3 || top3[0].Harness != "claude" || top3[2].Harness != "agy" {
		t.Fatalf("top3: %+v", top3)
	}

	// --since 90m should return the 5 sessions within 90m (claude..pi), excluding opencode (2h) and matomeru (48h).
	out, _, err = runSessionCLI(t, now, "session", "list", "--home", home, "--since", "90m")
	if err != nil {
		t.Fatalf("session list --since 90m: %v", err)
	}
	_, recent := rowsOf[SessionSummary](t, out)
	if len(recent) != 5 {
		t.Fatalf("--since 90m returned %d sessions, want 5", len(recent))
	}

	// --since 1d should return 6 sessions (excluding matomeru at 48h).
	out, _, err = runSessionCLI(t, now, "session", "list", "--home", home, "--since", "1d")
	if err != nil {
		t.Fatalf("session list --since 1d: %v", err)
	}
	_, day := rowsOf[SessionSummary](t, out)
	if len(day) != 6 {
		t.Fatalf("--since 1d returned %d sessions, want 6", len(day))
	}

	// --format jsonl and --format table.
	jsonlOut, _, err := runSessionCLI(t, now, "session", "list", "--home", home, "--limit", "2", "--format", "jsonl")
	if err != nil {
		t.Fatalf("session list jsonl: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(jsonlOut), "\n"); len(lines) != 2 || !strings.Contains(lines[0], `"noun":"session"`) {
		t.Fatalf("unexpected jsonl output: %s", jsonlOut)
	}

	tableOut, _, err := runSessionCLI(t, now, "session", "list", "--home", home, "--limit", "2", "--format", "table")
	if err != nil {
		t.Fatalf("session list table: %v", err)
	}
	if !strings.Contains(tableOut, "claude") || !strings.Contains(tableOut, "omp") {
		t.Fatalf("unexpected table output: %s", tableOut)
	}
}

func TestSessionShowFiltersAndFormats(t *testing.T) {
	home := isolatedSessionHome(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	fx := buildAllHarnessFixtures(t, home, now)

	// Default show by ID: thinking hidden, all tools shown.
	out, _, err := runSessionCLI(t, now, "session", "show", "claude:claude-001", "--home", home)
	if err != nil {
		t.Fatalf("session show claude: %v", err)
	}
	var doc SessionTranscript
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode show json: %v\n%s", err, out)
	}
	if doc.Harness != "claude" || doc.ID != "claude:claude-001" {
		t.Fatalf("unexpected doc header: %+v", doc)
	}
	if strings.Contains(out, "Secret chain of thought") {
		t.Fatal("thinking leaked when --thinking was false")
	}
	if !strings.Contains(out, "NOISY_TOOL_OUTPUT_EDIT_OK") || !strings.Contains(out, "NOISY_TOOL_OUTPUT_BASH_PASS") {
		t.Fatalf("expected tool outputs with --tools all:\n%s", out)
	}

	// --thinking=true --tools none.
	out, _, err = runSessionCLI(t, now, "session", "show", "session/claude:claude-001", "--home", home, "--thinking=true", "--tools", "none")
	if err != nil {
		t.Fatalf("session show thinking+none: %v", err)
	}
	if !strings.Contains(out, "Secret chain of thought") {
		t.Fatalf("expected thinking event when --thinking=true:\n%s", out)
	}
	if strings.Contains(out, "NOISY_TOOL_OUTPUT") || strings.Contains(out, `"tool_call"`) {
		t.Fatalf("expected zero tools when --tools none:\n%s", out)
	}

	// --tools 'edit|write' filters tool calls & results by regex.
	out, _, err = runSessionCLI(t, now, "session", "show", fx.claude, "--home", home, "--tools", "edit|write")
	if err != nil {
		t.Fatalf("session show tools pattern: %v", err)
	}
	if !strings.Contains(out, "NOISY_TOOL_OUTPUT_EDIT_OK") {
		t.Fatalf("expected Edit tool output with --tools edit|write:\n%s", out)
	}
	if strings.Contains(out, "NOISY_TOOL_OUTPUT_BASH_PASS") {
		t.Fatalf("Bash tool should have been filtered out:\n%s", out)
	}

	// --format jsonl.
	jsonlOut, _, err := runSessionCLI(t, now, "session", "show", fx.opencode, "--home", home, "--thinking=true", "--format", "jsonl")
	if err != nil {
		t.Fatalf("session show opencode jsonl: %v", err)
	}
	var ocEvents []SessionEvent
	for _, line := range strings.Split(strings.TrimSpace(jsonlOut), "\n") {
		var ev SessionEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("jsonl line %q: %v", line, err)
		}
		ocEvents = append(ocEvents, ev)
	}
	if len(ocEvents) != 5 {
		t.Fatalf("opencode events = %d, want 5 (user, thinking, tool_call, tool_result, assistant):\n%s", len(ocEvents), jsonlOut)
	}
	if ocEvents[1].Kind != "thinking" || ocEvents[2].ToolName != "edit" || ocEvents[3].ToolOutput != "OC_EDIT_DONE" {
		t.Fatalf("unexpected opencode events: %+v", ocEvents)
	}

	// --format md.
	mdOut, _, err := runSessionCLI(t, now, "session", "show", fx.agy, "--home", home, "--thinking=true", "--format", "md")
	if err != nil {
		t.Fatalf("session show agy md: %v", err)
	}
	for _, want := range []string{"# Audit the session scanner", "**Harness**: `agy`", "> Agy internal thought", "**Tool Call**: `read_file`", "**Tool Output**: `read_file`"} {
		if !strings.Contains(mdOut, want) {
			t.Errorf("markdown missing %q:\n%s", want, mdOut)
		}
	}

	// Verify codex, omp, pi, matomeru all parse cleanly via show.
	for name, path := range map[string]string{
		"omp":      fx.omp,
		"codex":    fx.codex,
		"pi":       fx.pi,
		"matomeru": fx.matomeru,
	} {
		raw, _, err := runSessionCLI(t, now, "session", "show", path, "--home", home, "--thinking=true")
		if err != nil {
			t.Fatalf("show %s: %v", name, err)
		}
		var tr SessionTranscript
		if err := json.Unmarshal([]byte(raw), &tr); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if tr.Harness != name || len(tr.Events) == 0 {
			t.Fatalf("unexpected transcript for %s: %+v", name, tr)
		}
	}

	// Verify streaming handles a >128KB single JSONL line cleanly.
	bigLine := `{"type":"user","cwd":"/tmp/big","timestamp":"2026-11-24T11:00:00Z","message":{"role":"user","content":"` + strings.Repeat("x", 150*1024) + `"}}` + "\n"
	bigPath := filepath.Join(t.TempDir(), "huge-line.jsonl")
	put(t, bigPath, bigLine)
	bigOut, _, err := runSessionCLI(t, now, "session", "show", bigPath, "--home", home, "--format", "jsonl")
	if err != nil {
		t.Fatalf("large line show failed: %v", err)
	}
	if !strings.Contains(bigOut, `"role":"user"`) {
		t.Fatalf("large line event missing")
	}
}

func TestSessionMineExtractsPushbackWindowsWithoutNoise(t *testing.T) {
	home := isolatedSessionHome(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	buildAllHarnessFixtures(t, home, now)

	// Default heuristic mine across all harnesses (JSONL).
	out, _, err := runSessionCLI(t, now, "session", "mine", "--home", home)
	if err != nil {
		t.Fatalf("session mine: %v", err)
	}
	// Verify zero tool outputs or thinking leaked into mined windows.
	for _, forbidden := range []string{"NOISY_TOOL_OUTPUT", "OMP_NOISY_WRITE_RESULT", "AGY_TOOL_FILE_CONTENTS", "CODEX_TOOL_OUTPUT_OK", "Secret chain of thought", "OMP hidden reasoning", "Agy internal thought"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("mine output leaked stripped noise %q:\n%s", forbidden, out)
		}
	}

	var windows []MineWindow
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var w MineWindow
		if err := json.Unmarshal([]byte(line), &w); err != nil {
			t.Fatalf("decode mine jsonl %q: %v", line, err)
		}
		windows = append(windows, w)
	}
	// Expected pushback hits: claude ("No, don't..."), omp ("Stop, that is too complex..."),
	// agy ("Wait, actually..."), matomeru ("Wrong layout..., revert...").
	if len(windows) != 4 {
		t.Fatalf("got %d mined windows, want 4:\n%s", len(windows), out)
	}
	if windows[0].Harness != "claude" ||
		!strings.Contains(windows[0].AssistantBefore, "special-case switch") ||
		!strings.Contains(windows[0].UserPushback, "declarative adapter table") ||
		!strings.Contains(windows[0].Resolution, "declarative adapter slice") {
		t.Fatalf("unexpected claude mined window: %+v", windows[0])
	}

	// Custom --pattern + --format json + --limit 1.
	jsonOut, _, err := runSessionCLI(t, now, "session", "mine", "--home", home, "--pattern", "streaming memory", "--format", "json", "--limit", "1")
	if err != nil {
		t.Fatalf("session mine custom pattern: %v", err)
	}
	var custom []MineWindow
	if err := json.Unmarshal([]byte(jsonOut), &custom); err != nil {
		t.Fatalf("decode mine json: %v\n%s", err, jsonOut)
	}
	if len(custom) != 1 || custom[0].Harness != "agy" || !strings.Contains(custom[0].Resolution, "O(1) line streaming") {
		t.Fatalf("unexpected custom pattern mine result: %+v", custom)
	}
}
