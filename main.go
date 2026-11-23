// Command o-ai is the AI hub's data tool and TUI: the local AI harnesses (omp,
// claude, codex, pi, opencode, gemini, clawed) and their providers, models,
// thinking levels, skills, agents, MCP servers/tools, settings, and plan quota,
// built on Ochazuke v2 and Oden v2.
//
//	o-ai [tui] [--no-probe] [--fresh] [--smoke]
//	o-ai NOUN list [--harness H] [--provider P] [--model M] [--search TEXT]
//	o-ai NOUN show ENTRY|--target ID
//	o-ai model select ENTRY
//	o-ai mcp tools [SERVER]          the tools omp cached for a server
//	o-ai inventory show|list|lists   the whole inventory or summary
//	o-ai quota list|show|refresh     rate-limit windows: % used, % left, resets
//	o-ai NOUNs                       NOUN list (o-ai skills, o-ai models)
//	o-ai catalog | query | capabilities | binding | inspect | config | version | smoke
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ofam.dev/oden"
)

// Tool is one invocation's world: streams, environment, and the home and
// working directory a verb's --home and --project default to.
type Tool struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Env            []string
	Home, Cwd      string
	Prog           string // "o ai" under o, "mtm ai" under mtm, else "o-ai"; messages only

	cache   map[string]cacheEntry         // this process's collections, by cache key
	collect func(Options, Need) Inventory // CollectNeed unless a test counts collections
	now     func() time.Time              // time.Now unless a test sets the clock
	quota   quotaDeps                     // quota's transport, keychain and endpoints (quota.go)
}

func main() {
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.Abs(home)
	}
	cwd, cwdErr := os.Getwd()
	prog := progName(os.Getenv)
	if err = errors.Join(err, cwdErr); err != nil {
		os.Exit(oden.Report(os.Stderr, prog, err))
	}
	t := &Tool{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Env: os.Environ(), Home: home, Cwd: cwd, Prog: prog}
	os.Exit(oden.Report(os.Stderr, t.Prog, t.Main(context.Background(), os.Args[1:])))
}

// progName is how messages name this command: the word under the parent oden
// app that ran it, else the bin name.
func progName(getenv func(string) string) string {
	if parent := getenv("ODEN_PARENT"); parent != "" {
		return parent + " ai"
	}
	return "o-ai"
}

// Main runs one invocation of o-ai on the Ochazuke v2 / Oden v2 CLI.
func (t *Tool) Main(ctx context.Context, args []string) error {
	return t.RunCLI(ctx, args)
}

// plural is the noun a plural word names (skills, harnesses), else "".
func plural(word string) string {
	for _, noun := range nouns {
		if word == noun+"s" || word == noun+"es" {
			return noun
		}
	}
	return ""
}

func filterModel(rows []Entry, query string) []Entry {
	if query == "" {
		return rows
	}
	var out []Entry
	for _, row := range rows {
		if m, ok := row.Value.(Model); ok {
			if m.ID == query || m.Slug == query || m.CLISlug == query || strings.EqualFold(m.Name, query) {
				out = append(out, row)
			}
		} else if strings.HasPrefix(row.ID, query+":") || strings.Contains(row.ID, "/"+query+":") || strings.Contains(row.ID, ":"+query+":") || strings.HasPrefix(row.Summary, query+" · ") {
			out = append(out, row)
		}
	}
	return out
}

func selectEntry(rows []Entry, query string) (Entry, error) {
	var exact, partial []Entry
	for _, row := range rows {
		match := row.ID == query || strings.EqualFold(row.Name, query)
		switch v := row.Value.(type) {
		case Model:
			match = match || v.Slug == query || v.CLISlug == query
		case Provider:
			match = match || v.Slug == query
		case Resource:
			match = match || v.Path == query
		}
		if match {
			exact = append(exact, row)
		} else if strings.Contains(strings.ToLower(row.ID+" "+row.Name), strings.ToLower(query)) {
			partial = append(partial, row)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return Entry{}, fmt.Errorf("no match for %q", query)
	}
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.ID)
	}
	return Entry{}, fmt.Errorf("ambiguous selection %q; use an exact ID or --harness/--provider:\n  %s", query, strings.Join(ids, "\n  "))
}

func printWarnings(out io.Writer, in Inventory) {
	if out == nil {
		return
	}
	for _, w := range in.Warnings {
		fmt.Fprintln(out, "warning:", w)
	}
	for _, h := range in.Harnesses {
		for _, w := range h.Warnings {
			fmt.Fprintf(out, "warning [%s]: %s\n", h.ID, w)
		}
	}
}

func selectionPath(o Options) string {
	return filepath.Join(o.Home, ".config", "aiview", "selection.json")
}

func saveSelection(o Options, id string) error {
	path := selectionPath(o)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".selection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(map[string]string{"model": id})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func loadSelection(o Options) (string, error) {
	m, err := readConfig(selectionPath(o))
	if err != nil {
		return "", errors.New("no saved model selection; use 'o-ai model select ID' first")
	}
	id := text(m, "model")
	if id == "" {
		return "", errors.New("saved model selection is empty")
	}
	return id, nil
}
