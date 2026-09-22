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

type tokenRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

// tokenView never carries the hash or the clear value.
type tokenView struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

func (s *Server) handleTokensCreate(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	var request tokenRequest
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
		if _, ok := s.config().Scopes[scope]; !ok {
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
	clear, err := newToken()
	if err != nil {
		s.fail(w, "generate access token", err)
		return
	}
	hash := sha256.Sum256([]byte(clear))
	token := &botflota.AccessToken{
		Name: request.Name, Hash: hash[:], Scopes: scopes,
		CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt,
	}
	if err := s.tokens.Create(r.Context(), token); err != nil {
		s.fail(w, "create access token", err)
		return
	}
	view := viewToken(*token)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": view.ID, "name": view.Name, "scopes": view.Scopes,
		"created_at": view.CreatedAt, "expires_at": view.ExpiresAt,
		"token": clear,
	})
}

func (s *Server) handleTokensList(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	tokens, err := s.tokens.List(r.Context())
	if err != nil {
		s.fail(w, "list access tokens", err)
		return
	}
	views := make([]tokenView, 0, len(tokens))
	for _, token := range tokens {
		views = append(views, viewToken(token))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": views})
}

func (s *Server) handleTokensRevoke(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid token id")
		return
	}
	err = s.tokens.Revoke(r.Context(), id, time.Now().UTC())
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "unknown token")
		return
	}
	if err != nil {
		s.fail(w, "revoke access token", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func viewToken(token botflota.AccessToken) tokenView {
	view := tokenView{
		ID: token.ID, Name: token.Name, Scopes: token.Scopes,
		CreatedAt: token.CreatedAt,
	}
	if !token.ExpiresAt.IsZero() {
		at := token.ExpiresAt
		view.ExpiresAt = &at
	}
	if !token.RevokedAt.IsZero() {
		at := token.RevokedAt
		view.RevokedAt = &at
	}
	return view
}
