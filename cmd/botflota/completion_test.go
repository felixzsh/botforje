package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureCompletion runs the completion command with both streams redirected,
// so a test reads exactly what a shell user would get.
func captureCompletion(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "stdout")
	errPath := filepath.Join(dir, "stderr")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create stdout: %v", err)
	}
	errOut, err := os.Create(errPath)
	if err != nil {
		t.Fatalf("create stderr: %v", err)
	}
	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errOut
	code := runCompletion(args)
	os.Stdout, os.Stderr = previousOut, previousErr
	if err := out.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}
	if err := errOut.Close(); err != nil {
		t.Fatalf("close stderr: %v", err)
	}
	stdout, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return code, string(stdout), string(stderr)
}

// TestBashCompletionFollowsTheTree pins the script to the CLI tree: every
// command, and every flag of every command, must reach the shell on its own.
func TestBashCompletionFollowsTheTree(t *testing.T) {
	code, script, stderr := captureCompletion(t, []string{"bash"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want silence on a successful run", stderr)
	}
	const register = "complete -F _botflota_complete botflota"
	if !strings.Contains(script, register) {
		t.Errorf("script is missing %q", register)
	}
	walkCommands(cliTree(), nil, func(node *commandHelp, path []string) {
		if !strings.Contains(script, node.name) {
			t.Errorf("script never mentions the command %q (%s)",
				node.name, casePath(path))
		}
		for _, f := range flagsOf(node) {
			if !strings.Contains(script, f.Name) {
				t.Errorf("script never mentions flag %q of %q", f.Name, node.name)
			}
		}
	})
	// A representative leaf: its own flag and help must both be offered.
	for _, want := range []string{"--force", "-h", "--help"} {
		if !strings.Contains(script, want) {
			t.Errorf("script is missing %q", want)
		}
	}
}

// TestBashCompletionCarriesNoFormattingArtifacts guards the generator: a tab
// from its indentation or a failed format verb would reach the shell as
// garbage, and an unexpected expansion would be a literal dollar expanded.
func TestBashCompletionCarriesNoFormattingArtifacts(t *testing.T) {
	_, script, _ := captureCompletion(t, []string{"bash"})
	for _, unwanted := range []string{"\t", "%!", "{{", "}}", "\\$", "\r"} {
		if strings.Contains(script, unwanted) {
			t.Errorf("script contains %q", unwanted)
		}
	}
	// The array subscript is the only expansion the script needs; anything
	// else that opens one is an artifact of the generator.
	rest := strings.ReplaceAll(script, "${COMP_WORDS[i]}", "")
	if i := strings.Index(rest, "${"); i >= 0 {
		t.Errorf("script has an unexpected expansion: %q", rest[i:min(i+16, len(rest))])
	}
	if !strings.HasSuffix(script, "\n") {
		t.Error("script does not end with a newline")
	}
}

func TestCompletionUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "unknown shell", args: []string{"powershell"}},
		{name: "shell without a script", args: []string{"zsh"}},
		{name: "missing shell", args: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := captureCompletion(t, tc.args)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want no script", stdout)
			}
			if !strings.Contains(stderr, "usage: botflota completion") {
				t.Errorf("stderr is missing the command help: %q", stderr)
			}
			if !strings.Contains(stderr, "bash") {
				t.Errorf("stderr does not name the accepted shell: %q", stderr)
			}
		})
	}
}
