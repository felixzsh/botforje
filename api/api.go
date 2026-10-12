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
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
)

type Fleet interface {
	Status() []botflota.BotStatus
	// StopBotForUnpair lets a send already in progress finish, but leaves
	// the rest of the queue for the unpair operation to fail (ADR 0040).
	StopBotForUnpair(scope, bot string) error
}

// ConfigApplier adopts a configuration the way a manual edit is adopted: the
// scopes that can become live now do, and the rest stay pending. *fleet.Fleet
// satisfies it; a server without one still saves, and the watcher activates the
// change on its next poll.
type ConfigApplier interface {
	ApplyConfig(
		ctx context.Context, cfg *config.Config, scopes []string, source *config.Source,
	) ([]string, map[string][]string, error)
}

// Options are the dependencies fleet run injects; every field is required
// except Log and Presence. Config is the shared snapshot a reload replaces
// (ADR 0009). Saved is that snapshot's counterpart: the configuration as it
// stands in the files, which may already differ from the running one.
type Options struct {
	Config *config.Source
	Saved  *config.Source
	// Writer saves scope configuration back into the files the instance loads;
	// fleet run injects the one over the main config file.
	Writer *config.Writer
	// Applier is optional: with it, a saved change becomes live in the same
	// request instead of waiting for the watcher.
	Applier ConfigApplier
	Fleet   Fleet
	// Setups resolves one bot's neutral account lifecycle and setup runner
	// through its installed channel definition (ADR 0053). It is required:
	// binding and setup operations select the bot's implementation, never
	// every bot through one device store.
	Setups   SetupResolver
	Bindings botflota.BindingStore
	// Pairings, Pairing, Presence, and Unpairer are the retired
	// WhatsApp-specific pairing path. They stay optional until their
	// last consumers (bots view, CLI) move to the neutral contracts.
	Pairings botflota.PairingStore
	Pairing  botflota.PairingRunner
	// Presence reports local pairing credentials for the bots view; fleet run
	// injects it, and a server without it answers from the stored pairing
	// state (ADR 0039).
	Presence botflota.DevicePresence
	// Unpairer drops a bot's link; the handler stops the running bot before
	// asking for it (ADR 0021, ADR 0039).
	Unpairer  botflota.AccountUnpairer
	Peers     botflota.PeerStore
	Directory botflota.DirectoryStore
	Chats     botflota.ChatStore
	Sessions  botflota.SessionStore
	Messages  botflota.MessageStore
	// Sends commits an API-origin outbound message outside a turn; it is
	// optional so a server without it answers 501 on the send route (ADR 0044).
	Sends botflota.SendStore
	// Channels is the installed set the bot's definition is resolved
	// against for send admission: target rules and declared content support
	// (ADR 0044, ADR 0052). It is optional; without it only the common
	// shape checks apply.
	Channels *channels.Catalog
	// History reads, deletes, and moves one bot's stored history. It is
	// optional: a server without it answers 501 on the history routes.
	History botflota.HistoryStore
	// Pauser stops the bots a history operation must work without and starts
	// them again. It is optional, and *fleet.Fleet satisfies it.
	Pauser   BotPauser
	Tokens   botflota.AccessTokenStore
	Resolver PrincipalResolver
	// RateBudgets are the per-minute request budgets; nil uses the built-in
	// defaults and an explicit zero turns a budget off (config: rate_limit).
	RateBudgets *config.RateBudgets
	Log         *slog.Logger
}

type Server struct {
	cfg       *config.Source
	saved     *config.Source
	writer    *config.Writer
	applier   ConfigApplier
	fleet     Fleet
	setups    SetupResolver
	bindings  botflota.BindingStore
	pairings  botflota.PairingStore
	pairing   botflota.PairingRunner
	presence  botflota.DevicePresence
	unpairer  botflota.AccountUnpairer
	peers     botflota.PeerStore
	directory botflota.DirectoryStore
	chats     botflota.ChatStore
	sessions  botflota.SessionStore
	messages  botflota.MessageStore
	sends     botflota.SendStore
	channels  *channels.Catalog
	history   botflota.HistoryStore
	pauser    BotPauser
	tokens    botflota.AccessTokenStore
	resolver  PrincipalResolver
	log       *slog.Logger
	limiter   *rateLimiter
	// opsMu guards the per-bot operation locks pairing, account, and history
	// operations share (ADR 0021, ADR 0039, ADR 0042).
	opsMu sync.Mutex
	ops   map[botflota.BotRef]*sync.Mutex
	// mintMu pairs the exclusive-scope check with the insert it guards, so
	// two concurrent mints cannot both pass a check the other then breaks
	// (config: exclusive_scopes; ADR 0039: one process owns the instance).
	mintMu  sync.Mutex
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
	if opts.Saved == nil {
		return nil, errors.New("api: no saved config")
	}
	if opts.Writer == nil {
		return nil, errors.New("api: no config writer")
	}
	if opts.Setups == nil {
		return nil, errors.New("api: no account setup resolver")
	}
	if opts.Bindings == nil {
		return nil, errors.New("api: no linked-account store")
	}
	if opts.Peers == nil {
		return nil, errors.New("api: no peer store")
	}
	if opts.Directory == nil {
		return nil, errors.New("api: no directory store")
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
	if opts.Config.Current().ExclusiveScopes {
		// Startup is not a request, so the check runs on its own context.
		if err := checkExclusiveScopes(context.Background(), opts.Tokens); err != nil {
			return nil, err
		}
	}
	logger := opts.Log
	if logger == nil {
		logger = botflota.DiscardLogger()
	}
	server := &Server{
		cfg: opts.Config, saved: opts.Saved, writer: opts.Writer,
		applier: opts.Applier, fleet: opts.Fleet,
		setups:   opts.Setups,
		bindings: opts.Bindings, pairings: opts.Pairings,
		pairing: opts.Pairing, presence: opts.Presence,
		unpairer: opts.Unpairer,
		peers:    opts.Peers, directory: opts.Directory, chats: opts.Chats,
		sessions: opts.Sessions, messages: opts.Messages, sends: opts.Sends,
		channels: opts.Channels,
		history:  opts.History,
		pauser:   opts.Pauser,
		tokens:   opts.Tokens, resolver: opts.Resolver, log: logger,
		limiter: newRateLimiter(opts.RateBudgets), started: time.Now(),
		ops: map[botflota.BotRef]*sync.Mutex{},
	}
	server.handler = server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

// config is the active snapshot; a reload replaces it for every handler.
func (s *Server) config() *config.Config { return s.cfg.Current() }

// savedConfig is the configuration as the files stand, which is what an editor
// reads and writes against: a scope can be valid and saved while the running
// instance still holds the previous one (ADR 0009).
func (s *Server) savedConfig() *config.Config { return s.saved.Current() }

// lockBot serializes the write operations of one bot — pairing start,
// cancel, unpair, and the history operations that stop it — so they never
// overlap, and one of them cannot restart a bot another is still working on
// (ADR 0021, ADR 0039, ADR 0042). The caller releases the lock through the
// return value.
func (s *Server) lockBot(scope, bot string) func() {
	return s.lockBots(botflota.BotRef{Scope: scope, Bot: bot})
}

// lockBots takes several bot locks in a stable order, so two operations that
// overlap on two bots cannot deadlock each other. Repeats are ignored.
func (s *Server) lockBots(refs ...botflota.BotRef) func() {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Scope != refs[j].Scope {
			return refs[i].Scope < refs[j].Scope
		}
		return refs[i].Bot < refs[j].Bot
	})
	unlocks := make([]func(), 0, len(refs))
	for i, ref := range refs {
		if i > 0 && ref == refs[i-1] {
			continue
		}
		s.opsMu.Lock()
		lock, ok := s.ops[ref]
		if !ok {
			lock = &sync.Mutex{}
			s.ops[ref] = lock
		}
		s.opsMu.Unlock()
		lock.Lock()
		unlocks = append(unlocks, lock.Unlock)
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
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
	mux.HandleFunc("GET /api/v1/scopes/{scope}/bots/{bot}", s.handleScopeBot)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/contacts", s.handleContacts)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/contacts/{subject}", s.handleContact)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/bots/{bot}/contacts", s.handleAddressBook)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/config", s.handleScopeConfig)
	mux.HandleFunc("PUT /api/v1/scopes/{scope}/config", s.handleScopeConfigPut)
	mux.HandleFunc("POST /api/v1/config/publications", s.handleConfigPublications)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/chats", s.handleChats)
	mux.HandleFunc("PUT /api/v1/scopes/{scope}/chats/control", s.handleChatControl)
	mux.HandleFunc("POST /api/v1/scopes/{scope}/chats/sync", s.handleChatSync)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/v1/scopes/{scope}/messages", s.handleMessages)
	mux.HandleFunc("POST /api/v1/scopes/{scope}/messages", s.handleSendMessage)

	binding := "/api/v1/scopes/{scope}/bots/{bot}/binding"
	mux.HandleFunc("GET "+binding, s.handleBindingInspect)
	mux.HandleFunc("DELETE "+binding, s.handleBindingDelete)
	mux.HandleFunc("GET "+binding+"/methods", s.handleBindingMethods)
	mux.HandleFunc("POST "+binding+"/setups", s.handleSetupBegin)
	mux.HandleFunc("GET "+binding+"/setups/{id}", s.handleSetupRead)
	mux.HandleFunc("POST "+binding+"/setups/{id}/inputs", s.handleSetupInput)
	mux.HandleFunc("DELETE "+binding+"/setups/{id}", s.handleSetupCancel)

	history := "/api/v1/scopes/{scope}/bots/{bot}/history"
	mux.HandleFunc("GET "+history+"/export", s.handleHistoryExport)
	mux.HandleFunc("DELETE "+history, s.handleHistoryDelete)
	mux.HandleFunc("POST "+history+"/migrations", s.handleHistoryMigrate)

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
