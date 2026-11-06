package main

import (
	"io"
	"strings"
	"testing"

	"ofam.dev/ochazuke/namecheck"
)

// TestNoNewNameBranches holds o-ai's non-test Go to namecheck: no new
// comparison, case label or lookup table keyed by a noun, verb, mode,
// motion or parameter name its runtime declares, and no function grows
// or passes complexity 25. The accepted uses are testdata/namecheck.txt,
// which only shrinks.
func TestNoNewNameBranches(t *testing.T) {
	home := t.TempDir()
	tool := &Tool{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Env: []string{"HOME=" + home}, Home: home, Cwd: home, Prog: "o-ai"}
	namecheck.Check(t, namecheck.Config{
		Vocab:    tool.shell().Runtime.NameVocab(),
		Baseline: "testdata/namecheck.txt",
		Dirs:     []string{"."},
	})
}
