package main

import (
	"fmt"
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

// TestCompletionScriptsFollowTheTree pins the scripts to the CLI tree: every
// command, and every flag of every command, must reach the shell on its own.
func TestCompletionScriptsFollowTheTree(t *testing.T) {
	cases := []struct {
		shell  string
		prefix string
		marker string
		sigil  string
	}{
		{shell: "bash", marker: "complete -F _botflota_complete botflota", sigil: "--force"},
		{shell: "zsh", prefix: "#compdef botflota", sigil: "--force"},
		{shell: "fish", marker: "complete -c botflota", sigil: "-l force"},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			code, script, stderr := captureCompletion(t, []string{tc.shell})
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want silence on a successful run", stderr)
			}
			if tc.prefix != "" && !strings.HasPrefix(script, tc.prefix) {
				t.Errorf("script starts with %q, want %q",
					strings.SplitN(script, "\n", 2)[0], tc.prefix)
			}
			if tc.marker != "" && !strings.Contains(script, tc.marker) {
				t.Errorf("script is missing %q", tc.marker)
			}
			walkCommands(cliTree(), nil, func(node *commandHelp, path []string) {
				if !strings.Contains(script, node.name) {
					t.Errorf("script never mentions the command %q (%s)",
						node.name, casePath(path))
				}
				for _, f := range flagsOf(node) {
					if !strings.Contains(script, f.Name) {
						t.Errorf("script never mentions flag %q of %q",
							f.Name, node.name)
					}
				}
			})
			if !strings.Contains(script, tc.sigil) {
				t.Errorf("script is missing the leaf flag %q", tc.sigil)
			}
			if !strings.Contains(script, "help") {
				t.Error("script is missing the help flag")
			}
			if tc.shell == "zsh" &&
				!strings.Contains(script, `"$funcstack[1]" = "_botflota"`) {
				// Without the guard an autoloaded file calls itself forever.
				t.Error("zsh script has no autoload guard")
			}
		})
	}
}

// TestCompletionScriptsCarryNoFormattingArtifacts guards the templates: a tab
// from their indentation or a failed format verb would reach the shell as
// garbage, and an unexpected expansion would be a literal dollar expanded.
func TestCompletionScriptsCarryNoFormattingArtifacts(t *testing.T) {
	for _, shell := range completionShells() {
		t.Run(shell, func(t *testing.T) {
			_, script, _ := captureCompletion(t, []string{shell})
			for _, unwanted := range []string{"\t", "%!", "{{", "}}", "\\$", "\r"} {
				if strings.Contains(script, unwanted) {
					t.Errorf("script contains %q", unwanted)
				}
			}
			// bash needs one array subscript; anything else that opens an
			// expansion is an artifact of the generator.
			rest := strings.ReplaceAll(script, "${COMP_WORDS[i]}", "")
			if i := strings.Index(rest, "${"); i >= 0 {
				t.Errorf("script has an unexpected expansion: %q",
					rest[i:min(i+16, len(rest))])
			}
			if !strings.HasSuffix(script, "\n") {
				t.Error("script does not end with a newline")
			}
		})
	}
}

// TestFishCompletionKeepsOnlyCommandPathWords pins the correction of finding
// R3-001: the helper may extend the matched path with a word only when that
// word names a subcommand of the level reached so far, so a half-typed word, a
// flag, or a flag value cannot silence every rule of the level it sits in.
func TestFishCompletionKeepsOnlyCommandPathWords(t *testing.T) {
	_, script, _ := captureCompletion(t, []string{"fish"})
	walkCommands(cliTree(), nil, func(node *commandHelp, path []string) {
		if len(node.sub) == 0 {
			return
		}
		want := fmt.Sprintf("case %s\n                contains -- $word %s\n"+
			"                or continue\n",
			quoteFish(casePath(path)), strings.Join(commandNames(node), " "))
		if !strings.Contains(script, want) {
			t.Errorf("node %q does not whitelist its subcommands:\n%s", node.name, want)
		}
	})
	if !strings.Contains(script, "            case '*'\n                continue\n") {
		t.Error("the helper has no fallback for a word that names no command")
	}
	// One path word per line, or the caller's command substitution receives
	// the joined path as a single element and every nested rule stops matching.
	if !strings.Contains(script, "    for part in $path\n        echo $part\n") {
		t.Error("the helper does not emit one path word per line")
	}
}

func TestCompletionUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "unknown shell", args: []string{"powershell"}},
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
			if !strings.Contains(stderr, "bash, zsh, fish") {
				t.Errorf("stderr does not name the accepted shells: %q", stderr)
			}
		})
	}
}
