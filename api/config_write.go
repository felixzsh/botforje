package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

const (
	// maxPublicationScopes bounds one batch, so a request cannot ask the writer
	// to re-encode an unbounded part of the tree at once.
	maxPublicationScopes = 64
	// maxWriteBody bounds a publication payload; a whole instance config is far
	// below it.
	maxWriteBody = 8 << 20
)

// scopeWriteBody replaces one whole scope: the version its editor read, and the
// scope as it should be saved. An omitted entity is a deletion.
type scopeWriteBody struct {
	Version *string     `json:"version"`
	Config  *spec.Scope `json:"config"`
}

// publicationBody publishes several existing scopes as one saved change, each
// with its own expected version.
type publicationBody struct {
	Scopes map[string]scopeWriteBody `json:"scopes"`
}

// scopeWriteResult is what one scope became. Saved and active are reported
// apart, because a saved change can still wait for a safe moment to activate
// (ADR 0009).
type scopeWriteResult struct {
	Scope         string `json:"scope"`
	SavedVersion  string `json:"saved_version"`
	ActiveVersion string `json:"active_version"`
	Status        string `json:"status"`
}

// handleScopeConfigPut replaces one existing scope. The route never creates a
// scope, never rekeys one, and never touches instance settings or owner policy.
func (s *Server) handleScopeConfigPut(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.configScopeParam(w, r)
	if !ok {
		return
	}
	var body scopeWriteBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Config == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config",
			"config is required")
		return
	}
	if body.Version == nil {
		writeError(w, http.StatusPreconditionRequired, "version_required",
			"version is required")
		return
	}
	s.publish(w, r, config.Publication{Scopes: map[string]config.ScopeWrite{
		scope: {Version: *body.Version, Scope: *body.Config},
	}})
}

// handleConfigPublications publishes several existing scopes as one saved
// change. One stale, unknown, or unauthorized member fails the whole request,
// and it reveals nothing about a scope outside the caller's authority.
func (s *Server) handleConfigPublications(w http.ResponseWriter, r *http.Request) {
	var body publicationBody
	if !decodeBody(w, r, &body) {
		return
	}
	if len(body.Scopes) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config",
			"at least one scope is required")
		return
	}
	if len(body.Scopes) > maxPublicationScopes {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config",
			"too many scopes in one publication")
		return
	}
	// Authority is settled for every requested key before any of them is
	// loaded, so naming one forbidden scope never discloses the others.
	principal := principalFrom(r.Context())
	publication := config.Publication{Scopes: map[string]config.ScopeWrite{}}
	for _, name := range config.SortedKeys(body.Scopes) {
		if !principal.Allows(name) {
			writeError(w, http.StatusNotFound, "not_found", "unknown scope")
			return
		}
		if _, known := s.savedConfig().Scopes[name]; !known {
			if _, running := s.config().Scopes[name]; !running {
				writeError(w, http.StatusNotFound, "not_found", "unknown scope")
				return
			}
		}
		write := body.Scopes[name]
		if write.Config == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_config",
				"config is required for every scope")
			return
		}
		if write.Version == nil {
			writeError(w, http.StatusPreconditionRequired, "version_required",
				"version is required for every scope")
			return
		}
		publication.Scopes[name] = config.ScopeWrite{
			Version: *write.Version,
			Scope:   *write.Config,
		}
	}
	s.publish(w, r, publication)
}

// publish restores the secrets the caller could not see, hands the change to the
// writer, and reports what each scope became.
func (s *Server) publish(
	w http.ResponseWriter, r *http.Request, publication config.Publication,
) {
	for name, write := range publication.Scopes {
		saved, ok := s.savedConfig().Scopes[name]
		if !ok {
			saved = s.config().Scopes[name]
		}
		publication.Scopes[name] = config.ScopeWrite{
			Version: write.Version,
			Scope:   config.RestoreMaskedSecrets(saved, write.Scope),
		}
	}
	outcomes, err := s.writer.Publish(r.Context(), publication)
	if err != nil {
		s.writePublicationError(w, r, err)
		return
	}
	if changed(outcomes) {
		s.adopt(r)
	}
	results := make([]scopeWriteResult, 0, len(outcomes))
	status := http.StatusOK
	for _, outcome := range outcomes {
		active, err := config.ScopeVersion(s.config().Scopes[outcome.Scope])
		if err != nil {
			s.fail(w, "version active scope", err)
			return
		}
		state := "pending"
		if active == outcome.Version {
			state = "active"
		} else {
			// Saved but not live: saying so is why both versions are reported.
			status = http.StatusAccepted
		}
		results = append(results, scopeWriteResult{
			Scope:         outcome.Scope,
			SavedVersion:  outcome.Version,
			ActiveVersion: active,
			Status:        state,
		})
	}
	writeJSON(w, status, map[string]any{"scopes": results})
}

// changed reports whether anything was written. A publication that matched what
// the files already hold leaves nothing to adopt.
func changed(outcomes []config.Outcome) bool {
	for _, outcome := range outcomes {
		if outcome.Changed {
			return true
		}
	}
	return false
}

// adopt loads the files a publication just wrote and hands the changed scopes
// to the running fleet, the same way the watcher handles a manual edit. The
// files are held for the whole load and activation, so the fleet never adopts a
// scope that is still being replaced. A server without an applier leaves the
// change saved; the watcher adopts it on its next poll.
func (s *Server) adopt(r *http.Request) {
	if s.applier == nil {
		return
	}
	unlock := s.writer.Sync()
	defer unlock()
	candidate, err := (config.Loader{}).Load(s.writer.MainPath())
	if err != nil {
		// The files were validated before they were written, so this is not an
		// expected outcome; the change stays saved and the watcher reports it.
		s.log.Error("api adopt failed; the change stays saved", "error", err)
		return
	}
	plan := config.PlanReload(s.config(), candidate)
	// The active snapshot is what the fleet updates; the saved one follows the
	// files below.
	applied, _, err := s.applier.ApplyConfig(r.Context(), candidate, plan.Scopes, s.cfg)
	if err != nil {
		s.log.Error("api adopt failed; the change stays saved", "error", err)
	}
	// The saved snapshot follows the files whatever became live, so a reader
	// sees the document an editor must write against.
	s.saved.Replace(candidate)
	if len(applied) == 0 && len(plan.Scopes) == 0 {
		return
	}
	s.log.Info("published configuration activated", "scopes", applied)
}

// writePublicationError maps what the writer refused to a status an editor can
// act on. Validation problems name the offending file and object, which is what
// an authorized caller needs to fix its own scope (ADR 0002).
func (s *Server) writePublicationError(
	w http.ResponseWriter, r *http.Request, err error,
) {
	var conflict config.VersionConflict
	var invalid config.InvalidCandidate
	switch {
	case errors.Is(err, config.ErrScopeUnknown):
		writeError(w, http.StatusNotFound, "not_found", "unknown scope")
	case errors.As(err, &conflict):
		if !principalFrom(r.Context()).Allows(conflict.Scope) {
			writeError(w, http.StatusNotFound, "not_found", "unknown scope")
			return
		}
		writeError(w, http.StatusPreconditionFailed, "stale_version",
			"the scope changed since it was read")
	case errors.As(err, &invalid):
		messages := make([]string, 0, len(invalid.Problems))
		for _, problem := range invalid.Problems {
			messages = append(messages, problem.Error())
		}
		writeError(w, http.StatusUnprocessableEntity, "invalid_config",
			strings.Join(messages, "; "))
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusRequestTimeout, "canceled", "the request was canceled")
	default:
		s.fail(w, "publish configuration", err)
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"the request body is not valid JSON for this route")
		return false
	}
	return true
}
