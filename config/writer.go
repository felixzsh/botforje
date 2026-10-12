package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// managedDir holds the fragments a writer creates and the journal that makes a
// publication recoverable. It sits beside the main file, so a replacement
// never crosses a filesystem.
const managedDir = ".botflota"

// managedFragment is where a scope's new definitions go, named after the scope
// so one scope's definitions never mix with another's.
func managedFragment(main, scope string) string {
	return filepath.Join(filepath.Dir(main), managedDir, "scopes", scope+".yml")
}

// ErrScopeUnknown reports a scope the files do not hold. Publication replaces
// an existing scope and never creates one (ADR 0002).
var ErrScopeUnknown = errors.New("config: unknown scope")

// TreeUnreadable reports that the files could not be loaded at all, so nothing
// could be checked against them. An invalid manual document leaves the tree
// unreadable and blocks publication until its author corrects it; the message
// names the file and the reason, which is more than an scoped caller should
// learn, so the API decides how much of it to repeat (ADR 0002).
type TreeUnreadable struct{ Err error }

func (e TreeUnreadable) Error() string { return "config: " + e.Err.Error() }

func (e TreeUnreadable) Unwrap() error { return e.Err }

// VersionConflict reports that a scope changed after the version its editor
// read. Nothing is written; the editor reloads and reconciles.
type VersionConflict struct {
	Scope    string
	Expected string
	Actual   string
}

func (e VersionConflict) Error() string {
	return fmt.Sprintf("config: scope %q changed: expected version %s, found %s",
		e.Scope, e.Expected, e.Actual)
}

// InvalidCandidate reports that the tree a publication would produce does not
// validate, with the same problems a manual edit reports. No file is written
// (ADR 0002).
type InvalidCandidate struct{ Problems []ValidationError }

func (e InvalidCandidate) Error() string {
	messages := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		messages = append(messages, problem.Error())
	}
	return "config: invalid configuration: " + strings.Join(messages, "; ")
}

// ScopeWrite is one requested scope: the version its editor read, and the
// entire scope that replaces it. A publication is a full replacement, so an
// omitted entity is a deletion.
type ScopeWrite struct {
	Version string
	Scope   spec.Scope
}

// Publication replaces several existing scopes as one saved change.
type Publication struct{ Scopes map[string]ScopeWrite }

// Outcome is what one scope became. A publication matching what the files
// already hold changes nothing and says so.
type Outcome struct {
	Scope   string
	Version string
	Changed bool
}

// Writer saves scope configuration back into the files an instance loads. It
// edits every definition in the file that already declares it, routes new ones
// to a managed fragment, touches no other scope and no global setting, and
// either lands every file or leaves the tree exactly as it was — including
// after a crash (ADR 0002, ADR 0009).
//
// Writing re-encodes a file from the typed model, so a file that changes loses
// its comments and key order; formatting is not a config contract. A file whose
// content would not change is never rewritten.
type Writer struct {
	main string
	// catalog holds the installed channel definitions a candidate tree is
	// validated against, so a publication refuses an unknown channel exactly
	// like a manual edit (ADR 0052, ADR 0054).
	catalog *channels.Catalog
	// tree guards the files themselves. A publication replaces several of them
	// one after another, and a reader that loaded the tree in between would see
	// a scope split across an old and a new file. It is a channel rather than a
	// mutex so a caller waits inside its own context instead of forever.
	tree chan struct{}
}

// NewWriter returns a writer for the tree the main file loads; catalog holds
// the installed channel definitions its candidates are checked against.
func NewWriter(main string, catalog *channels.Catalog) (*Writer, error) {
	abs, err := filepath.Abs(main)
	if err != nil {
		return nil, err
	}
	return &Writer{
		main: filepath.Clean(abs), catalog: catalog,
		tree: make(chan struct{}, 1),
	}, nil
}

// acquire holds the files, waiting only while the caller's context lasts. A
// caller that gives up before the tree is free gets its context's error and
// writes nothing.
func (w *Writer) acquire(ctx context.Context) error {
	select {
	case w.tree <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) release() { <-w.tree }

// MainPath is the file this writer reads and replaces, which is where a caller
// looks to see what a publication saved.
func (w *Writer) MainPath() string { return w.main }

// Sync holds the files still while the caller reads them, so a load and its
// activation never overlap a publication replacing one of them. The config
// watcher takes it around each poll; a publication takes it itself. Release the
// returned function when the read is done.
func (w *Writer) Sync(ctx context.Context) (func(), error) {
	if err := w.acquire(ctx); err != nil {
		return nil, err
	}
	return w.release, nil
}

// Publish replaces whole existing scopes as one recoverable saved change. It
// loads the files again instead of trusting a snapshot, so a manual edit made
// since the editor read is the one it is checked against. Every requested
// version must still match, the tree the change would produce must validate,
// and only then is anything written.
func (w *Writer) Publish(
	ctx context.Context, pub Publication,
) ([]Outcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(pub.Scopes) == 0 {
		return nil, nil
	}
	// The files are held for the whole operation, so a reader never loads a
	// scope half-replaced, and a second publication waits rather than
	// interleaving.
	if err := w.acquire(ctx); err != nil {
		return nil, err
	}
	defer w.release()

	current, err := (Loader{}).Load(w.main)
	if err != nil {
		return nil, TreeUnreadable{Err: err}
	}
	outcomes, err := checkVersions(current, pub)
	if err != nil {
		return nil, err
	}
	// A scope that already holds exactly what was sent is not rewritten:
	// re-encoding it would only churn formatting, and an identical batch is a
	// no-op.
	changed := Publication{Scopes: map[string]ScopeWrite{}}
	for i := range outcomes {
		write := pub.Scopes[outcomes[i].Scope]
		version, err := ScopeVersion(write.Scope)
		if err != nil {
			return nil, err
		}
		if version == outcomes[i].Version {
			continue
		}
		outcomes[i].Changed = true
		changed.Scopes[outcomes[i].Scope] = write
	}
	if len(changed.Scopes) == 0 {
		return outcomes, nil
	}
	files, err := w.render(current, changed)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return outcomes, nil
	}
	candidate, err := (Loader{Overlay: files}).Load(w.main)
	if err != nil {
		return nil, err
	}
	if problems := Validate(candidate, w.catalog); len(problems) > 0 {
		return nil, InvalidCandidate{problems}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	record, err := stage(w.main, files)
	if err != nil {
		return nil, err
	}
	if err := applyWrite(w.main, record, 0); err != nil {
		// Nothing is committed until every file is in place, so the tree goes
		// back to what it was instead of staying half-published.
		if recovery := Recover(w.main); recovery != nil {
			return nil, fmt.Errorf(
				"config: publish failed and could not recover: %w",
				errors.Join(err, recovery))
		}
		return nil, err
	}
	for i := range outcomes {
		if !outcomes[i].Changed {
			continue
		}
		version, err := ScopeVersion(candidate.Scopes[outcomes[i].Scope])
		if err != nil {
			return nil, err
		}
		outcomes[i].Version = version
	}
	return outcomes, nil
}

// checkVersions confirms every requested scope still exists and still holds the
// version its editor read, before any content is rendered.
func checkVersions(current *Config, pub Publication) ([]Outcome, error) {
	outcomes := make([]Outcome, 0, len(pub.Scopes))
	for _, name := range SortedKeys(pub.Scopes) {
		write := pub.Scopes[name]
		saved, ok := current.Scopes[name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrScopeUnknown, name)
		}
		actual, err := ScopeVersion(saved)
		if err != nil {
			return nil, err
		}
		if write.Version != actual {
			return nil, VersionConflict{
				Scope: name, Expected: write.Version, Actual: actual,
			}
		}
		outcomes = append(outcomes, Outcome{Scope: name, Version: actual})
	}
	return outcomes, nil
}

// render returns the content every file would hold after the publication,
// keyed by absolute path. A file whose content is already correct is left out,
// so an already-applied publication writes nothing.
func (w *Writer) render(
	current *Config, pub Publication,
) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range SortedKeys(pub.Scopes) {
		managed := managedFragment(w.main, name)
		part := splitScope(current, name, pub.Scopes[name].Scope, managed)
		for _, path := range SortedKeys(part.perFile) {
			doc, err := openDocument(w.main, path, files)
			if err != nil {
				return nil, err
			}
			doc.setScope(name, *part.perFile[path])
			content, err := doc.encode()
			if err != nil {
				return nil, err
			}
			files[path] = content
		}
		if part.newManaged {
			if err := w.includeManaged(managed, files); err != nil {
				return nil, err
			}
		}
	}
	for path, content := range files {
		existing, err := os.ReadFile(path)
		if err == nil && bytes.Equal(existing, content) {
			delete(files, path)
		}
	}
	return files, nil
}

// scopePlacement is one scope's new content divided by the file that will hold
// each part of it. A definition keeps the file that declares it today; a
// definition no file declares is new and goes to the managed fragment. Every
// file that holds the scope today gets an entry, even an empty one: a
// publication replaces the whole scope, so an omitted entity is deleted where
// it lives (ADR 0002).
type scopePlacement struct {
	perFile map[string]*spec.Scope
	// newManaged marks content the managed fragment receives that no file
	// declares today, which is when the loader needs the include that reaches
	// it.
	newManaged bool
}

func splitScope(
	current *Config, name string, next spec.Scope, managed string,
) scopePlacement {
	placement := scopePlacement{perFile: map[string]*spec.Scope{}}
	part := func(path string) *spec.Scope {
		if _, ok := placement.perFile[path]; !ok {
			placement.perFile[path] = &spec.Scope{}
		}
		return placement.perFile[path]
	}
	// origin is where a definition lives today, or the managed fragment for one
	// no file declares yet.
	origin := func(kind, key string) string {
		file := current.OriginFile(kind, name, key)
		if file == "" {
			placement.newManaged = true
			return managed
		}
		return file
	}
	for _, llm := range SortedKeys(next.LLMs) {
		target := part(origin("llm", llm))
		put(target, &target.LLMs, next.LLMs, llm)
	}
	for _, action := range SortedKeys(next.Actions) {
		target := part(origin("action", action))
		put(target, &target.Actions, next.Actions, action)
	}
	for _, graph := range SortedKeys(next.Graphs) {
		target := part(origin("graph", graph))
		put(target, &target.Graphs, next.Graphs, graph)
	}
	for _, bot := range SortedKeys(next.Bots) {
		target := part(origin("bot", bot))
		put(target, &target.Bots, next.Bots, bot)
	}
	if displayHolder := current.DisplayNameOrigin(name); displayHolder != "" {
		part(displayHolder).DisplayName = next.DisplayName
	} else if next.DisplayName != "" {
		placement.newManaged = true
		part(managed).DisplayName = next.DisplayName
	}
	for _, file := range scopeFiles(current, name) {
		part(file)
	}
	return placement
}

// scopeFiles lists every file that holds content for one scope today, which is
// what a full replacement must rewrite: a file left out would keep definitions
// the publication omitted.
func scopeFiles(cfg *Config, scope string) []string {
	current, ok := cfg.Scopes[scope]
	if !ok {
		return nil
	}
	found := map[string]bool{}
	add := func(file string) {
		if file != "" {
			found[file] = true
		}
	}
	for _, name := range SortedKeys(current.LLMs) {
		add(cfg.originOf("llm", scope, name))
	}
	for _, name := range SortedKeys(current.Actions) {
		add(cfg.originOf("action", scope, name))
	}
	for _, name := range SortedKeys(current.Graphs) {
		add(cfg.originOf("graph", scope, name))
	}
	for _, name := range SortedKeys(current.Bots) {
		add(cfg.originOf("bot", scope, name))
	}
	add(cfg.DisplayNameOrigin(scope))
	return SortedKeys(found)
}

func put[V any](scope *spec.Scope, field *map[string]V, values map[string]V, name string) {
	if *field == nil {
		*field = map[string]V{}
	}
	(*field)[name] = values[name]
}

// includeManaged adds a managed fragment to the main file's include list, so
// the loader reaches it. An include that already names it is left alone.
func (w *Writer) includeManaged(path string, files map[string][]byte) error {
	entry, err := filepath.Rel(filepath.Dir(w.main), path)
	if err != nil {
		return err
	}
	doc, err := openDocument(w.main, w.main, files)
	if err != nil {
		return err
	}
	if slices.Contains(doc.conf.Include, entry) {
		return nil
	}
	doc.conf.Include = append(doc.conf.Include, entry)
	content, err := doc.encode()
	if err != nil {
		return err
	}
	files[w.main] = content
	return nil
}

// document is one file being rewritten: the main configuration, which also
// carries global settings and the include list, or a fragment, which carries
// scopes only (ADR 0016).
type document struct {
	path string
	main bool
	conf *Config
	frag *fragment
}

// openDocument reads a file from the content already rendered, or from disk.
// Reading a rendered file is what lets one publication change a scope and the
// main file's include list in the same pass.
func openDocument(main, path string, files map[string][]byte) (*document, error) {
	data, ok := files[path]
	if !ok {
		var err error
		if data, err = os.ReadFile(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) || path == main {
				return nil, err
			}
			data = nil
		}
	}
	doc := &document{path: path, main: path == main}
	if doc.main {
		doc.conf = &Config{}
	} else {
		doc.frag = &fragment{}
	}
	if data == nil {
		return doc, nil
	}
	target := any(doc.frag)
	if doc.main {
		target = doc.conf
	}
	if err := decodeStrict(path, data, target); err != nil {
		return nil, err
	}
	return doc, nil
}

func (d *document) setScope(name string, scope spec.Scope) {
	if d.main {
		if d.conf.Scopes == nil {
			d.conf.Scopes = map[string]spec.Scope{}
		}
		d.conf.Scopes[name] = scope
		return
	}
	if d.frag.Scopes == nil {
		d.frag.Scopes = map[string]spec.Scope{}
	}
	d.frag.Scopes[name] = scope
}

func (d *document) encode() ([]byte, error) {
	if d.main {
		return encode(d.conf)
	}
	return encode(d.frag)
}

// encode writes a document from its typed model with every secret as the file
// already writes it. Encoding a secret's mask would replace a working
// credential with asterisks, so a rewrite must never be the reason a saved key
// changes (ADR 0014).
func encode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(writtenValue(reflect.ValueOf(value))); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// journalEntry is one file a publication replaces. existed says whether the
// file was there before, which is what tells recovery whether to restore a
// backup or remove a file the publication created.
type journalEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
}

type journal struct {
	Entries []journalEntry `json:"entries"`
}

func journalPath(main string) string {
	return filepath.Join(filepath.Dir(main), managedDir, "publish.json")
}

func stagedPath(path string) string { return path + ".botflota-new" }
func backupPath(path string) string { return path + ".botflota-old" }

// stage writes every new file beside its target and records what it is about to
// replace, so a crash between the first and the last replacement can be undone
// completely.
func stage(main string, files map[string][]byte) (*journal, error) {
	record := &journal{}
	for _, path := range SortedKeys(files) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(stagedPath(path), files[path], 0o644); err != nil {
			return nil, err
		}
		_, statErr := os.Stat(path)
		record.Entries = append(record.Entries,
			journalEntry{Path: path, Existed: statErr == nil})
		if statErr != nil {
			continue
		}
		original, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(backupPath(path), original, 0o644); err != nil {
			return nil, err
		}
	}
	return record, writeJournal(journalPath(main), record)
}

func writeJournal(path string, record *journal) error {
	content, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// The record lands in one rename, so recovery never trusts a half-written
	// journal.
	temporary := path + ".botflota-new"
	if err := os.WriteFile(temporary, content, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// applyWrite puts every staged file in place and commits by dropping the
// journal. stopAfter replaces only that many files so a test can stop where a
// crash would; 0 replaces them all. Backups are cleanup, after the commit: a
// crash before it must find every backup in place, or recovery could not
// restore the tree as a whole.
func applyWrite(main string, record *journal, stopAfter int) error {
	for i, entry := range record.Entries {
		if stopAfter > 0 && i >= stopAfter {
			return nil
		}
		if err := os.Rename(stagedPath(entry.Path), entry.Path); err != nil {
			return err
		}
	}
	if err := os.Remove(journalPath(main)); err != nil {
		return err
	}
	for _, entry := range record.Entries {
		// A backup left behind is inert: the next publication overwrites it in
		// stage, so cleanup cannot be the reason a committed publication fails.
		_ = os.Remove(backupPath(entry.Path))
	}
	return nil
}

// Recover restores the tree a publication was replacing when it did not finish.
// Startup runs it before the first load, so an interrupted publication leaves
// the instance on the last complete tree instead of a half-written one.
func Recover(main string) error {
	path := journalPath(main)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record journal
	if err := json.Unmarshal(content, &record); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, entry := range record.Entries {
		backup, err := os.ReadFile(backupPath(entry.Path))
		switch {
		case entry.Existed && err == nil:
			if err := os.WriteFile(entry.Path, backup, 0o644); err != nil {
				return err
			}
		case entry.Existed && !errors.Is(err, os.ErrNotExist):
			return err
		case entry.Existed:
			// The journal is still here, so the publication never committed
			// and its backups are complete. One missing is corruption; leaving
			// that target unrepaired would mix old and new files.
			return fmt.Errorf("config: recover %s: backup is missing", entry.Path)
		case !entry.Existed:
			// The publication created this file, so undoing means removing it.
			if err := os.Remove(entry.Path); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		for _, leftover := range []string{backupPath(entry.Path), stagedPath(entry.Path)} {
			if err := os.Remove(leftover); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return os.Remove(path)
}
