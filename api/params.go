package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/felixzsh/botflota"
)

const (
	defaultLimit = 50
	maxLimit     = 500
)

func (s *Server) chatParams(
	w http.ResponseWriter, r *http.Request,
) (scope, bot string, chat *botflota.Chat, ok bool) {
	scope, ok = s.scopeParam(w, r)
	if !ok {
		return "", "", nil, false
	}
	bot, ok = s.botParam(w, r, scope)
	if !ok {
		return "", "", nil, false
	}
	if bot == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "bot is required")
		return "", "", nil, false
	}
	phone := r.URL.Query().Get("sender")
	if phone == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "sender is required")
		return "", "", nil, false
	}
	ctx := r.Context()
	sender, err := s.senders.Get(ctx, scope, phone)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "unknown sender")
		return "", "", nil, false
	}
	if err != nil {
		s.fail(w, "read sender", err)
		return "", "", nil, false
	}
	chat, err = s.chats.ForSender(ctx, scope, bot, sender.ID)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no chat for that sender")
		return "", "", nil, false
	}
	if err != nil {
		s.fail(w, "read chat", err)
		return "", "", nil, false
	}
	return scope, bot, chat, true
}

// An out-of-scope target answers 404, never 403, so a scoped principal
// cannot infer that another scope exists.
func (s *Server) scopeParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	scope := r.PathValue("scope")
	if !principalFrom(r.Context()).Allows(scope) {
		writeError(w, http.StatusNotFound, "not_found", "unknown scope")
		return "", false
	}
	if _, ok := s.config().Scopes[scope]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown scope")
		return "", false
	}
	return scope, true
}

// configScopeParam is scopeParam for the configuration routes, which address
// the saved document. A scope can exist in the files and not yet be running, so
// existence is answered by the saved tree as well as the active one; authority
// is still decided before either is read, and an out-of-scope target is still
// indistinguishable from an unknown one.
func (s *Server) configScopeParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	scope := r.PathValue("scope")
	if !principalFrom(r.Context()).Allows(scope) {
		writeError(w, http.StatusNotFound, "not_found", "unknown scope")
		return "", false
	}
	if _, ok := s.savedConfig().Scopes[scope]; ok {
		return scope, true
	}
	if _, ok := s.config().Scopes[scope]; ok {
		return scope, true
	}
	writeError(w, http.StatusNotFound, "not_found", "unknown scope")
	return "", false
}

// An empty bot is allowed; the caller decides whether it needs one.
func (s *Server) botParam(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	bot := r.URL.Query().Get("bot")
	if bot == "" {
		return "", true
	}
	return s.knownBot(w, scope, bot)
}

// botPathParam is botParam for a path segment, where an empty value cannot
// happen.
func (s *Server) botPathParam(
	w http.ResponseWriter, r *http.Request, scope string,
) (string, bool) {
	return s.knownBot(w, scope, r.PathValue("bot"))
}

func (s *Server) knownBot(w http.ResponseWriter, scope, bot string) (string, bool) {
	if _, ok := s.config().Scopes[scope].Bots[bot]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown bot")
		return "", false
	}
	return bot, true
}

func afterIDParam(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("after_id")
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("after_id must be a non-negative integer")
	}
	return id, nil
}

// A zero limit means every row only when allowAll, for a collection whose
// store supports an unbounded read; the callers that must not return
// everything keep it off.
func limitParam(r *http.Request, allowAll bool) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 || (limit == 0 && !allowAll) {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	if limit > maxLimit {
		return maxLimit, nil
	}
	return limit, nil
}

// The lower bound of a list read is a unix-millisecond timestamp.
func sinceParam(r *http.Request) (time.Time, error) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return time.Time{}, nil
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return time.Time{}, fmt.Errorf("since must be a unix millisecond timestamp")
	}
	return time.UnixMilli(ms), nil
}

// A zero count means every entry the phone makes available.
func countParam(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("count")
	if raw == "" {
		return 0, nil
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("count must be a non-negative integer")
	}
	return count, nil
}

// The timeout is the seconds to wait for one answer from the phone.
func timeoutParam(r *http.Request) (time.Duration, error) {
	raw := r.URL.Query().Get("timeout")
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("timeout must be a positive integer")
	}
	return time.Duration(seconds) * time.Second, nil
}

// The error detail reaches the log, never the client.
func (s *Server) fail(w http.ResponseWriter, operation string, err error) {
	s.log.Error("api request failed", "operation", operation, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}
