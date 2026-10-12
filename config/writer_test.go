package config

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota/spec"
)

// writerTree lays out a small instance: a main file, one include holding a
// scope, and a second include holding another scope's bot.
func writerTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"botflota.yml": strings.Join([]string{
			"address: 127.0.0.1",
			"port: 3000",
			"include:",
			"  - scopes/support.yml",
			"  - scopes/ventas.yml",
			"scopes:",
			"  support:",
			"    display_name: Support",
			"    llms:",
			"      primary:",
			"        provider: openai",
			"        model: gpt-4o-mini",
			"        api_key: sk-live",
			"    graphs:",
			"      main:",
			"        root: hello",
			"        nodes:",
			"          hello:",
			"            goal: say hello",
			"    bots:",
			"      demo:",
			"        graph: main",
			"",
		}, "\n"),
		"scopes/support.yml": strings.Join([]string{
			"scopes:",
			"  support:",
			"    actions:",
			"      greet:",
			"        steps:",
			"          - message:",
			"              body: hello",
			"",
		}, "\n"),
		"scopes/ventas.yml": strings.Join([]string{
			"scopes:",
			"  ventas:",
			"    graphs:",
			"      main:",
			"        root: hola",
			"        nodes:",
			"          hola:",
			"            goal: say hi",
			"    bots:",
			"      other:",
			"        graph: main",
			"",
		}, "\n"),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return filepath.Join(dir, "botflota.yml")
}

func loadWriterTree(t *testing.T, main string) *Config {
	t.Helper()
	cfg, err := (Loader{}).Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func scopeVersion(t *testing.T, cfg *Config, name string) string {
	t.Helper()
	version, err := ScopeVersion(cfg.Scopes[name])
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}
	return version
}

// copyScope clones the maps a test edits, so a change never reaches the config
// the version was computed from.
func copyScope(scope spec.Scope) spec.Scope {
	copied := scope
	copied.LLMs = maps.Clone(scope.LLMs)
	copied.Actions = maps.Clone(scope.Actions)
	copied.Graphs = maps.Clone(scope.Graphs)
	copied.Bots = maps.Clone(scope.Bots)
	return copied
}

func TestWriterKeepsEachDefinitionInItsFile(t *testing.T) {
	main := writerTree(t)
	writer, err := NewWriter(main, testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	current := loadWriterTree(t, main)

	// Rename the bot's graph reference and add a new bot. The bot stays in the
	// main file; the new one has no origin anywhere, so it needs a fragment.
	next := copyScope(current.Scopes["support"])
	next.Bots = map[string]spec.Bot{
		"demo":   {Graph: "main"},
		"backup": {Graph: "main"},
	}
	outcomes, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Changed {
		t.Fatalf("outcomes = %+v, want one changed scope", outcomes)
	}

	after := loadWriterTree(t, main)
	if after.OriginFile("bot", "support", "demo") != main {
		t.Fatal("the existing bot left its file")
	}
	fragment := managedFragment(main, "support")
	if after.OriginFile("bot", "support", "backup") != fragment {
		t.Fatalf("the new bot is in %q, want the managed fragment",
			after.OriginFile("bot", "support", "backup"))
	}
	if got := after.Scopes["support"].DisplayName; got != "Support" {
		t.Fatalf("display_name = %q, want it kept in the main file", got)
	}
	// The scope's action never moved.
	if after.OriginFile("action", "support", "greet") !=
		filepath.Join(filepath.Dir(main), "scopes", "support.yml") {
		t.Fatal("the action left its file")
	}
	// Another scope is untouched, byte for byte.
	if !strings.Contains(string(readFile(t,
		filepath.Join(filepath.Dir(main), "scopes", "ventas.yml"))), "other") {
		t.Fatal("an unaddressed scope was edited")
	}
	if got := after.Scopes["support"].LLMs["primary"].APIKey.Reveal(); got != "sk-live" {
		t.Fatalf("api_key = %q, want the literal preserved", got)
	}
}

func TestWriterDeletesWhatTheScopeNoLongerHas(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	next := copyScope(current.Scopes["support"])
	delete(next.Bots, "demo")
	if _, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	after := loadWriterTree(t, main)
	if _, ok := after.Scopes["support"].Bots["demo"]; ok {
		t.Fatal("the removed bot is still loaded")
	}
	if !strings.Contains(string(readFile(t, main)), "gpt-4o-mini") {
		t.Fatal("the main file lost more than the bot")
	}
}

func TestWriterEditsADisplayNameInPlace(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	next := copyScope(current.Scopes["support"])
	next.DisplayName = "Customer support"
	if _, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	after := loadWriterTree(t, main)
	if after.Scopes["support"].DisplayName != "Customer support" {
		t.Fatalf("display_name = %q, want the new label",
			after.Scopes["support"].DisplayName)
	}
	if after.DisplayNameOrigin("support") != main {
		t.Fatal("the label moved to another file")
	}
}

func TestWriterPublishesSeveralScopesAtOnce(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	support := current.Scopes["support"]
	support.DisplayName = "Support desk"
	ventas := copyScope(current.Scopes["ventas"])
	ventas.DisplayName = "Sales"
	outcomes, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: support},
			"ventas":  {Version: scopeVersion(t, current, "ventas"), Scope: ventas},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %+v, want both scopes", outcomes)
	}
	after := loadWriterTree(t, main)
	if after.Scopes["support"].DisplayName != "Support desk" ||
		after.Scopes["ventas"].DisplayName != "Sales" {
		t.Fatalf("labels = %q and %q",
			after.Scopes["support"].DisplayName, after.Scopes["ventas"].DisplayName)
	}
}

func TestWriterRejectsAStaleVersion(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	next := copyScope(current.Scopes["support"])
	next.DisplayName = "Support desk"
	// An editor that read an older version sends a version nobody has now.
	_, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: "v-stale", Scope: next},
		},
	})
	var conflict VersionConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want a version conflict", err)
	}
	if conflict.Scope != "support" {
		t.Fatalf("conflict scope = %q, want support", conflict.Scope)
	}
	if !strings.Contains(string(readFile(t, main)), "Support") {
		t.Fatal("a refused publication wrote to the main file")
	}
}

func TestWriterRefusesToCreateAScope(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	_, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"nuevo": {Version: "v-any", Scope: spec.Scope{}},
		},
	})
	if !errors.Is(err, ErrScopeUnknown) {
		t.Fatalf("error = %v, want ErrScopeUnknown", err)
	}
}

func TestWriterValidatesBeforeWriting(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)
	before := string(readFile(t, main))

	// A bot pointing at a graph the scope does not define is invalid, and the
	// files must be untouched.
	next := copyScope(current.Scopes["support"])
	next.Bots = map[string]spec.Bot{"demo": {Graph: "missing"}}
	_, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	})
	var invalid InvalidCandidate
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want an invalid candidate", err)
	}
	if string(readFile(t, main)) != before {
		t.Fatal("an invalid candidate wrote to the files")
	}
	if _, err := os.Stat(journalPath(main)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an invalid candidate left a journal behind")
	}
}

func TestWriterChangesNothingForAnIdenticalScope(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)
	before := string(readFile(t, main))

	outcomes, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {
				Version: scopeVersion(t, current, "support"),
				Scope:   copyScope(current.Scopes["support"]),
			},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if outcomes[0].Changed {
		t.Fatal("an unchanged scope reported a change")
	}
	if string(readFile(t, main)) != before {
		t.Fatal("an unchanged scope rewrote the main file")
	}
}

func TestWriterRecoversAnInterruptedPublication(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	next := copyScope(current.Scopes["support"])
	next.Bots["extra"] = spec.Bot{}
	before := map[string]string{}
	for _, path := range current.Files() {
		before[path] = string(readFile(t, path))
	}

	files, err := writer.render(current, Publication{Scopes: map[string]ScopeWrite{
		"support": {Version: scopeVersion(t, current, "support"), Scope: next},
	}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	record, err := stage(main, files)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Stop after the first replacement, where a crash would.
	if err := applyWrite(main, record, 1); err != nil {
		t.Fatalf("applyWrite: %v", err)
	}
	if _, err := os.Stat(journalPath(main)); err != nil {
		t.Fatal("the journal did not survive an interrupted publication")
	}

	if err := Recover(main); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for path, content := range before {
		if string(readFile(t, path)) != content {
			t.Fatalf("%s was not restored", path)
		}
	}
	if _, err := os.Stat(managedFragment(main, "support")); !errors.Is(
		err, os.ErrNotExist) {
		t.Fatal("the managed fragment survived a recovered publication")
	}
	if _, err := os.Stat(journalPath(main)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Recover left the journal behind")
	}
	// The tree loads and holds what it held before.
	after := loadWriterTree(t, main)
	if _, ok := after.Scopes["support"].Bots["extra"]; ok {
		t.Fatal("a recovered publication kept the new bot")
	}
}

// interruptedPublication stages and applies a change to one scope, stopping
// after every file was replaced and before the journal is dropped, which is
// where a crash leaves the tree recoverable. It returns the record and what
// each file held before.
func interruptedPublication(
	t *testing.T, main, scope string, next spec.Scope,
) (*journal, map[string]string) {
	t.Helper()
	writer, err := NewWriter(main, testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	current := loadWriterTree(t, main)
	before := map[string]string{}
	for _, path := range current.Files() {
		before[path] = string(readFile(t, path))
	}
	files, err := writer.render(current, Publication{Scopes: map[string]ScopeWrite{
		scope: {Version: scopeVersion(t, current, scope), Scope: next},
	}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	record, err := stage(main, files)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	for _, entry := range record.Entries {
		if err := os.Rename(stagedPath(entry.Path), entry.Path); err != nil {
			t.Fatalf("rename: %v", err)
		}
	}
	return record, before
}

func TestWriterRecoversFromACrashBeforeTheCommit(t *testing.T) {
	main := writerTree(t)
	next := copyScope(loadWriterTree(t, main).Scopes["support"])
	next.Bots["extra"] = spec.Bot{}
	record, before := interruptedPublication(t, main, "support", next)

	for _, entry := range record.Entries {
		if !entry.Existed {
			continue
		}
		if _, err := os.Stat(backupPath(entry.Path)); err != nil {
			t.Fatalf("the backup of %s is gone before the commit: %v", entry.Path, err)
		}
	}
	if err := Recover(main); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for path, content := range before {
		if string(readFile(t, path)) != content {
			t.Fatalf("%s was not restored", path)
		}
	}
}

func TestRecoverRefusesAMissingBackup(t *testing.T) {
	main := writerTree(t)
	next := copyScope(loadWriterTree(t, main).Scopes["support"])
	next.Bots["extra"] = spec.Bot{}
	record, _ := interruptedPublication(t, main, "support", next)
	for _, entry := range record.Entries {
		if !entry.Existed {
			continue
		}
		if err := os.Remove(backupPath(entry.Path)); err != nil {
			t.Fatalf("remove backup: %v", err)
		}
		break
	}
	// Restoring what is left would mix old and new files, so the tree stays
	// untouched and the operator is told.
	if err := Recover(main); err == nil {
		t.Fatal("Recover accepted a missing backup")
	}
}

func TestWriterDeletesADefinitionWhoseFileHoldsNothingElse(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	// scopes/support.yml holds the scope's action and nothing else, so an
	// omitted definition must still rewrite that file.
	next := copyScope(current.Scopes["support"])
	delete(next.Actions, "greet")
	if _, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	after := loadWriterTree(t, main)
	if _, ok := after.Scopes["support"].Actions["greet"]; ok {
		t.Fatal("the omitted action is still loaded")
	}
	if _, ok := after.Scopes["support"]; !ok {
		t.Fatal("the scope disappeared with the file that held the action")
	}
}

func TestWriterClearsAScopeItEmpties(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	if _, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {
				Version: scopeVersion(t, current, "support"), Scope: spec.Scope{},
			},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	scope, ok := loadWriterTree(t, main).Scopes["support"]
	if !ok {
		t.Fatal("the scope disappeared; a publication never removes a scope")
	}
	if scope.DisplayName != "" || len(scope.LLMs) != 0 || len(scope.Actions) != 0 ||
		len(scope.Graphs) != 0 || len(scope.Bots) != 0 {
		t.Fatalf("scope = %+v, want it emptied", scope)
	}
}

func TestWriterRoundTripKeepsTheDocumentTheSame(t *testing.T) {
	main := writerTree(t)
	before, err := (Loader{}).Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A document that comes back from the writer must be indistinguishable from
	// the one that went in. If it is not, a reload sees a change nobody made:
	// an empty collection written where the file had none loads as a non-nil
	// map, and the scope no longer compares equal to the running one.
	writer, err := NewWriter(main, testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	current := loadWriterTree(t, main)
	next := copyScope(current.Scopes["support"])
	next.DisplayName = "Support desk"
	if _, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	after, err := (Loader{}).Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(before.Scopes["ventas"], after.Scopes["ventas"]) {
		t.Fatalf("an untouched scope changed:\n%#v\n%#v",
			before.Scopes["ventas"], after.Scopes["ventas"])
	}
	plan := PlanReload(before, after)
	if len(plan.Scopes) != 1 || plan.Scopes[0] != "support" {
		t.Fatalf("changed scopes = %v, want only the published one", plan.Scopes)
	}
}

func TestWriterKeepsASavedCredentialIntact(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))
	current := loadWriterTree(t, main)

	next := copyScope(current.Scopes["support"])
	next.DisplayName = "Support desk"
	if _, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// The mask must never reach the file: it would replace a working key.
	if strings.Contains(string(readFile(t, main)), "***") {
		t.Fatal("the writer saved a mask as a credential")
	}
	got := loadWriterTree(t, main).Scopes["support"].LLMs["primary"].APIKey.Reveal()
	if got != "sk-live" {
		t.Fatalf("api_key = %q, want the credential that was saved", got)
	}
}

func TestWriterSyncHoldsTheFilesStill(t *testing.T) {
	main := writerTree(t)
	writer, _ := NewWriter(main, testCatalog(t))

	unlock, err := writer.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	published := make(chan struct{})
	go func() {
		defer close(published)
		_, _ = writer.Publish(context.Background(), Publication{
			Scopes: map[string]ScopeWrite{"support": {Version: "v-any"}},
		})
	}()
	select {
	case <-published:
		t.Fatal("a publication ran while the files were held")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("a publication stayed blocked after the files were released")
	}
}

func TestRecoverWithoutAJournalDoesNothing(t *testing.T) {
	main := writerTree(t)
	before := string(readFile(t, main))
	if err := Recover(main); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if string(readFile(t, main)) != before {
		t.Fatal("Recover changed a tree with no pending publication")
	}
}

func writeTreeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
