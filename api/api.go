// Package api serves the read-only REST API that fleet run composes and hosts
// in its own process (ADR 0025, ADR 0034). Handlers stay small and direct:
// they read the live fleet and the stores, and every addressable object
// carries its scope.
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
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
)

// Fleet is the live side of the API: the runtime state of the running bots.
type Fleet interface {
	Status() []fleet.BotStatus
}

// Options are the dependencies fleet run injects. Config, Fleet, Accounts,
// Pairings, and Auth are required.
type Options struct {
	Config   *config.Config
	Fleet    Fleet
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
	Auth     *Auth
	Log      *slog.Logger
}

// Server is the REST API.
type Server struct {
	cfg      *config.Config
	fleet    Fleet
	accounts botflota.LinkedAccountStore
	pairings botflota.PairingStore
	auth     *Auth
	log      *slog.Logger
	started  time.Time
	handler  http.Handler
}

// New validates the dependencies and builds the routes.
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
	if opts.Auth == nil {
		return nil, errors.New("api: no auth")
	}
	logger := opts.Log
	if logger == nil {
		logger = botflota.DiscardLogger()
	}
	server := &Server{
		cfg: opts.Config, fleet: opts.Fleet,
		accounts: opts.Accounts, pairings: opts.Pairings,
		auth: opts.Auth, log: logger, started: time.Now(),
	}
	server.handler = server.routes()
	return server, nil
}

// Handler returns the routes with auth applied. Tests use it directly.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/bots", s.handleBots)
	mux.HandleFunc("GET /api/v1/config", s.handleConfig)
	return s.auth.Middleware(mux)
}

// Listen binds the configured address so a bad address fails at startup
// instead of inside the serving goroutine.
func (s *Server) Listen() (net.Listener, error) {
	address := s.cfg.Address
	if address == "" {
		address = "127.0.0.1"
	}
	listener, err := net.Listen("tcp",
		net.JoinHostPort(address, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return nil, fmt.Errorf("api: listen: %w", err)
	}
	return listener, nil
}

// Serve serves on the listener until ctx is cancelled and then shuts down in
// an orderly way.
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
