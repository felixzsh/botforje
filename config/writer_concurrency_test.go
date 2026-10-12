package config

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/felixzsh/botflota/spec"
)

// writerTreeWithTwoScopesInOneFile puts both scopes in the main file, which is
// where concurrent publications have to share physical storage.
func writerTreeWithTwoScopesInOneFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, "botflota.yml")
	content := `address: 127.0.0.1
scopes:
  support:
    graphs:
      main:
        root: hello
        nodes:
          hello:
            goal: greet
    bots:
      demo:
        graph: main
        channel:
          provider: whatsapp
          mode: native
  ventas:
    graphs:
      main:
        root: hola
        nodes:
          hola:
            goal: greet
    bots:
      other:
        graph: main
        channel:
          provider: whatsapp
          mode: native
`
	if err := writeTreeFile(main, content); err != nil {
		t.Fatalf("write main: %v", err)
	}
	return main
}

func TestWriterSerializesConcurrentScopesInOneFile(t *testing.T) {
	main := writerTreeWithTwoScopesInOneFile(t)
	writer, err := NewWriter(main, testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	current := loadWriterTree(t, main)

	// Both editors read the same file and publish at the same time. Neither may
	// lose the other's label, and both scopes must survive in one document.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, scope := range []string{"support", "ventas"} {
		next := copyScope(current.Scopes[scope])
		next.DisplayName = scope + " desk"
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = writer.Publish(context.Background(), Publication{
				Scopes: map[string]ScopeWrite{scope: {
					Version: scopeVersion(t, current, scope), Scope: next,
				}},
			})
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("publication %d: %v", i, err)
		}
	}
	after := loadWriterTree(t, main)
	if after.Scopes["support"].DisplayName != "support desk" {
		t.Fatalf("support label = %q, lost by the other publication",
			after.Scopes["support"].DisplayName)
	}
	if after.Scopes["ventas"].DisplayName != "ventas desk" {
		t.Fatalf("ventas label = %q, lost by the other publication",
			after.Scopes["ventas"].DisplayName)
	}
}

func TestWriterConflictsOnTwoStaleWrites(t *testing.T) {
	main := writerTreeWithTwoScopesInOneFile(t)
	writer, err := NewWriter(main, testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	current := loadWriterTree(t, main)
	version := scopeVersion(t, current, "support")
	// Two editors hold the same version and submit different labels, so exactly
	// one of them can be right about what the scope held.
	first := copyScope(current.Scopes["support"])
	first.DisplayName = "First"
	second := copyScope(current.Scopes["support"])
	second.DisplayName = "Second"
	edits := []spec.Scope{first, second}
	results := make(chan error, 2)
	for _, edit := range edits {
		go func() {
			_, err := writer.Publish(context.Background(), Publication{
				Scopes: map[string]ScopeWrite{"support": {Version: version, Scope: edit}},
			})
			results <- err
		}()
	}
	firstErr, secondErr := <-results, <-results
	if firstErr == nil && secondErr == nil {
		t.Fatal("two writes on one version both landed")
	}
	if firstErr != nil && secondErr != nil {
		t.Fatal("both writes on one version were refused")
	}
	var conflict VersionConflict
	refused := firstErr
	if refused == nil {
		refused = secondErr
	}
	if !errors.As(refused, &conflict) {
		t.Fatalf("refusal = %v, want a version conflict", refused)
	}
	// An identical publication is a no-op, so whichever landed left the file
	// valid and loadable.
	if _, err := (Loader{}).Load(main); err != nil {
		t.Fatalf("the file was left unloadable: %v", err)
	}
}

func TestWriterGivesUpOnACanceledWait(t *testing.T) {
	main := writerTreeWithTwoScopesInOneFile(t)
	writer, err := NewWriter(main, testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	unlock, err := writer.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = writer.Publish(ctx, Publication{
		Scopes: map[string]ScopeWrite{"support": {Version: "v-any"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the context's cancellation", err)
	}

	// A reader that gives up waiting also gets the context's error instead of
	// blocking forever.
	if _, err := writer.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync error = %v, want the context's cancellation", err)
	}
}

func TestRecoverIsIdempotentAndSafeAfterCompletion(t *testing.T) {
	main := writerTreeWithTwoScopesInOneFile(t)
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
	// A completed publication leaves nothing to recover, and running recovery
	// again must not undo it.
	if err := Recover(main); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got := loadWriterTree(t, main).Scopes["support"].DisplayName; got != "Support desk" {
		t.Fatalf("display_name = %q, want the published label", got)
	}
}

func TestLockDirResolvesASmallProbe(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "botflota.yml")
	if err := writeTreeFile(main, "data_dir: .data\nscopes: {}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LockDir(main)
	if err != nil {
		t.Fatalf("LockDir: %v", err)
	}
	if want := filepath.Join(dir, ".data"); got != want {
		t.Fatalf("LockDir = %q, want %q", got, want)
	}

	// The probe never needs the rest of the document to be valid.
	broken := filepath.Join(dir, "broken.yml")
	if err := writeTreeFile(broken,
		"data_dir: /var/lib/botflota\nscopes:\n  support:\n    graphs: nope\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, err := LockDir(broken); err != nil || got != "/var/lib/botflota" {
		t.Fatalf("LockDir = %q, %v; want /var/lib/botflota", got, err)
	}

	t.Run("an unreadable file is reported", func(t *testing.T) {
		if _, err := LockDir(filepath.Join(dir, "missing.yml")); err == nil {
			t.Fatal("a missing main file resolved a lock directory")
		}
	})
}
