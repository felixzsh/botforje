package api

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/felixzsh/botflota"
)

// keyRequest is the owner payload that mints a scoped API key.
type keyRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

// keyView is a minted key without its hash or clear value.
type keyView struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// handleKeysCreate mints a scoped API key. The clear key is returned once and
// never stored; only its hash is kept (ROADMAP Phase 13).
func (s *Server) handleKeysCreate(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	var request keyRequest
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if len(request.Scopes) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"at least one scope is required")
		return
	}
	scopes := make([]string, 0, len(request.Scopes))
	seen := make(map[string]bool, len(request.Scopes))
	for _, scope := range request.Scopes {
		if seen[scope] {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"duplicate scope "+scope)
			return
		}
		if _, ok := s.cfg.Scopes[scope]; !ok {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"unknown scope "+scope)
			return
		}
		seen[scope] = true
		scopes = append(scopes, scope)
	}
	var expiresAt time.Time
	if request.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, request.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"expires_at must be RFC 3339")
			return
		}
		if !parsed.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"expires_at must be in the future")
			return
		}
		expiresAt = parsed.UTC()
	}
	clear, err := newSecret()
	if err != nil {
		s.fail(w, "generate api key", err)
		return
	}
	hash := sha256.Sum256([]byte(clear))
	key := &botflota.APIKey{
		Name: request.Name, Hash: hash[:], Scopes: scopes,
		CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt,
	}
	if err := s.keys.Create(r.Context(), key); err != nil {
		s.fail(w, "create api key", err)
		return
	}
	view := viewKey(*key)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": view.ID, "name": view.Name, "scopes": view.Scopes,
		"created_at": view.CreatedAt, "expires_at": view.ExpiresAt,
		"key": clear,
	})
}

// handleKeysList lists the minted keys without hashes.
func (s *Server) handleKeysList(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	keys, err := s.keys.List(r.Context())
	if err != nil {
		s.fail(w, "list api keys", err)
		return
	}
	views := make([]keyView, 0, len(keys))
	for _, key := range keys {
		views = append(views, viewKey(key))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": views})
}

// handleKeysRevoke revokes one key. Revoking an already revoked key is a
// no-op.
func (s *Server) handleKeysRevoke(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid key id")
		return
	}
	err = s.keys.Revoke(r.Context(), id, time.Now().UTC())
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "unknown key")
		return
	}
	if err != nil {
		s.fail(w, "revoke api key", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func viewKey(key botflota.APIKey) keyView {
	view := keyView{
		ID: key.ID, Name: key.Name, Scopes: key.Scopes, CreatedAt: key.CreatedAt,
	}
	if !key.ExpiresAt.IsZero() {
		at := key.ExpiresAt
		view.ExpiresAt = &at
	}
	if !key.RevokedAt.IsZero() {
		at := key.RevokedAt
		view.RevokedAt = &at
	}
	return view
}
