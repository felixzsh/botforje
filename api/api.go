// Package api serves the REST API that fleet run composes and hosts in its
// own process (ADR 0025, ADR 0034). Every addressable object carries its
// scope; one middleware resolves the credential to a Principal and handlers
// authorize against it (ROADMAP Phase 10/13).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

type Fleet interface {
	Status() []botflota.BotStatus
	// StopBotForUnpair lets a send already in progress finish, but leaves
	// the rest of the queue for the unpair operation to fail (ADR 0040).
	StopBotForUnpair(scope, bot string) error
}

// Options are the dependencies fleet run injects; every field is required
// except Log and Presence. Config is the shared snapshot a reload replaces
// (ADR 0009).
type Options struct {
	Config   *config.Source
	Fleet    Fleet
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
	Pairing  botflota.PairingRunner
	// Presence reports local pairing credentials for the bots view; fleet run
	// injects it, and a server without it answers from the stored pairing
	// state (ADR 0039).
	Presence botflota.DevicePresence
	// Unpairer drops a bot's link; the handler stops the running bot before
	// asking for it (ADR 0021, ADR 0039).
	Unpairer botflota.AccountUnpairer
	Senders  botflota.SenderStore
	Contacts botflota.ContactStore
	Chats    botflota.ChatStore
	Sessions botflota.SessionStore
	Messages botflota.MessageStore
	Tokens   botflota.AccessTokenStore
	Resolver PrincipalResolver
	// RateBudgets are the per-minute request budgets; nil uses the built-in
	// defaults and an explicit zero turns a budget off (config: rate_limit).
	RateBudgets *config.RateBudgets
	Log         *slog.Logger
}

type Server struct {
	cfg      *config.Source
	fleet    Fleet
	accounts botflota.LinkedAccountStore
	pairings botflota.PairingStore
	pairing  botflota.PairingRunner
	presence botflota.DevicePresence
	unpairer botflota.AccountUnpairer
	senders  botflota.SenderStore
	contacts botflota.ContactStore
	chats    botflota.ChatStore
	sessions botflota.SessionStore
	messages botflota.MessageStore
	tokens   botflota.AccessTokenStore
	resolver PrincipalResolver
	log      *slog.Logger
	limiter  *rateLimiter
	// opsMu guards the per-bot operation locks pairing start, cancel, and
	// unpair share (ADR 0021, ADR 0039).
	opsMu   sync.Mutex
	ops     map[string]*sync.Mutex
	started time.Time
	handler http.Handler
}

func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("api: no config")
	}
	if opts.Fleet == nil {
		return nil, errors.New("api: no fleet")
	}
	if opts.Accounts == nil {
		return nil, errors.New("api: no linked-account store")
	}
	if opts.Pairings == nil {
		return nil, errors.New("api: no pairing store")
	}
	if opts.Pairing == nil {
		return nil, errors.New("api: no pairing runner")
	}
	if opts.Unpairer == nil {
		return nil, errors.New("api: no account unpairer")
	}
	if opts.Senders == nil {
		return nil, errors.New("api: no sender store")
	}
	if opts.Contacts == nil {
		return nil, errors.New("api: no contact store")
	}
	if opts.Chats == nil {
		return nil, errors.New("api: no chat store")
	}
	if opts.Sessions == nil {
		return nil, errors.New("api: no session store")
	}
	if opts.Messages == nil {
		return nil, errors.New("api: no message store")
	}
	if opts.Tokens == nil {
		return nil, errors.New("api: no access-token store")
	}
	if opts.Resolver == nil {
		return nil, errors.New("api: no principal resolver")
	}
	logger := opts.Log
	if logger == nil {
		logger = botflota.DiscardLogger()
	}
	server := &Server{
		cfg: opts.Config, fleet: opts.Fleet,
		accounts: opts.Accounts, pairings: opts.Pairings,
		pairing: opts.Pairing, presence: opts.Presence,
		unpairer: opts.Unpairer,
		senders:  opts.Senders, contacts: opts.Contacts, chats: opts.Chats,
		sessions: opts.Sessions, messages: opts.Messages,
		tokens: opts.Tokens, resolver: opts.Resolver, log: logger,
		limiter: newRateLimiter(opts.RateBudgets), started: time.Now(),
		ops: map[string]*sync.Mutex{},
	}
	server.handler = server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

// config is the active snapshot; a reload replaces it for every handler.
func (s *Server) config() *config.Config { return s.cfg.Current() }

// lockBot serializes the write operations of one bot — pairing start,
// cancel, and unpair — so an unpair never overlaps the session that stored
// the account, and a re-pair starts only after the unpair finished (ADR
// 0021, ADR 0039). The caller releases the lock through the return value.
func (s *Server) lockBot(scope, bot string) func() {
	key := scope + "/" + bot
	s.opsMu.Lock()
	lock, ok := s.ops[key]
	if !ok {
		lock = &sync.Mutex{}
		s.ops[key] = lock
	}
	s.opsMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/bots", s.handleBots)
	mux.HandleFunc("GET /api/v1/config", s.handleConfig)
	mux.HandleFunc("POST /api/v1/tokens", s.handleTokensCreate)
	mux.HandleFunc("GET /api/v1/tokens", s.handleTokensList)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.handleTokensRevoke)

	mux.HandleFunc("GET /api/v1/scopes/{scope}/bots", s.handleScopeBots)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/config", s.handleScopeConfig)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/chats", s.handleChats)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/contacts", s.handleContacts)
	mux.HandleFunc("POST /api/v1/scopes/{scope}/chats/sync", s.handleChatSync)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/messages", s.handleMessages)

	pairing := "/api/v1/scopes/{scope}/bots/{bot}/pairing"
	mux.HandleFunc("POST "+pairing, s.handlePairStart)
	mux.HandleFunc("GET "+pairing, s.handlePairState)
	mux.HandleFunc("DELETE "+pairing, s.handlePairCancel)

	account := "/api/v1/scopes/{scope}/bots/{bot}/account"
	mux.HandleFunc("DELETE "+account, s.handleAccountDelete)

	return s.authenticate(mux)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.resolver.Resolve(r)
		if err == nil && principal != nil {
			// One budget per credential: a token cannot drown the instance,
			// and every other credential keeps its own allowance.
			if principal.Key != "" {
				if ok, retry := s.limiter.allow("credential:"+principal.Key,
					s.limiter.credential); !ok {
					writeRateLimited(w, retry)
					return
				}
			}
		} else if ok, retry := s.limiter.allow(
			"unauthenticated:"+requestAddress(r), s.limiter.failedAttempt,
		); !ok {
			// An attempt with no usable credential accounts for nothing but
			// its source, so guessing is bounded per address and one
			// source's failures never spend another's budget.
			writeRateLimited(w, retry)
			return
		}
		switch {
		case errors.Is(err, ErrUnauthenticated):
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized",
				"missing or invalid credential")
			return
		case err != nil:
			s.log.Error("api authenticate failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
	})
}

// Listen binds here, so a bad address fails at startup instead of inside the
// serving goroutine.
func (s *Server) Listen() (net.Listener, error) {
	address := s.config().Address
	if address == "" {
		address = "127.0.0.1"
	}
	listener, err := net.Listen("tcp",
		net.JoinHostPort(address, strconv.Itoa(s.config().Port)))
	if err != nil {
		return nil, fmt.Errorf("api: listen: %w", err)
	}
	return listener, nil
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{Handler: s.handler, ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return fmt.Errorf("api: shutdown: %w", err)
		}
		return nil
	}
}

// errorBody is the single error shape of the API.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
