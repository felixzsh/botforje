package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
)

// SetupResolver resolves one bot's neutral account lifecycle and setup
// runner through its installed channel definition, never by scanning every
// bot through one device store (ADR 0053). *fleet.Fleet satisfies it.
type SetupResolver interface {
	SetupFor(scope, bot string) (botflota.AccountLifecycle, botflota.SetupRunner, error)
}

// bindingBodies bounds setup request bodies: inputs carry small typed
// values, never bulk payloads (ADR 0053).
const bindingBodyLimit = 1 << 16

type bindingIdentityView struct {
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
}

type bindingView struct {
	Provider   string              `json:"provider"`
	Adapter    string              `json:"adapter"`
	Mode       string              `json:"mode"`
	Namespace  string              `json:"namespace,omitempty"`
	Identity   bindingIdentityView `json:"identity"`
	Generation int64               `json:"generation"`
}

type bindingStatusView struct {
	Binding   *bindingView `json:"binding"`
	Condition string       `json:"condition"`
	Reasons   []string     `json:"reasons,omitempty"`
	Method    string       `json:"method,omitempty"`
}

func bindingViewOf(binding *botflota.Binding) *bindingView {
	if binding == nil {
		return nil
	}
	return &bindingView{
		Provider:  binding.Provider,
		Adapter:   binding.Adapter,
		Mode:      binding.Mode,
		Namespace: binding.Namespace,
		Identity: bindingIdentityView{
			Provider: binding.Identity.Provider,
			Subject:  binding.Identity.Subject,
		},
		Generation: binding.Generation,
	}
}

func bindingStatusViewOf(status botflota.BindingStatus) bindingStatusView {
	return bindingStatusView{
		Binding:   bindingViewOf(status.Binding),
		Condition: string(status.Condition),
		Reasons:   status.Reasons,
		Method:    status.Method,
	}
}

type setupDisplayView struct {
	Format    string    `json:"format"`
	Payload   string    `json:"payload"`
	ExpiresAt time.Time `json:"expires_at"`
	Hint      string    `json:"hint,omitempty"`
}

type setupFieldView struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

type setupApprovalView struct {
	Destination string `json:"destination,omitempty"`
	Instruction string `json:"instruction,omitempty"`
}

type setupProfileView struct {
	Methods     []string         `json:"methods,omitempty"`
	Options     []string         `json:"options,omitempty"`
	AllowCreate bool             `json:"allow_create,omitempty"`
	Params      []setupFieldView `json:"params,omitempty"`
}

type setupStepView struct {
	Kind     string             `json:"kind"`
	Display  *setupDisplayView  `json:"display,omitempty"`
	Fields   []setupFieldView   `json:"fields,omitempty"`
	Approval *setupApprovalView `json:"approval,omitempty"`
	Profile  *setupProfileView  `json:"profile,omitempty"`
}

type setupSessionView struct {
	ID              string         `json:"id"`
	Method          string         `json:"method"`
	Mode            string         `json:"mode,omitempty"`
	State           string         `json:"state"`
	Revision        int64          `json:"revision"`
	StartedAt       time.Time      `json:"started_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	ExpiresAt       time.Time      `json:"expires_at"`
	ErrorCode       string         `json:"error_code,omitempty"`
	ActivationError string         `json:"activation_error,omitempty"`
	Step            *setupStepView `json:"step,omitempty"`
}

// setupSessionViewOf sanitizes the session for its owner: step fields expose
// names, kinds, and labels only, never submitted secret values (ADR 0053).
func setupSessionViewOf(session *botflota.SetupSession) setupSessionView {
	view := setupSessionView{
		ID: session.ID, Method: session.Method, Mode: session.Mode,
		State: string(session.State), Revision: session.Revision,
		StartedAt: session.StartedAt, UpdatedAt: session.UpdatedAt,
		ExpiresAt:       session.ExpiresAt,
		ErrorCode:       session.ErrorCode,
		ActivationError: session.ActivationError,
	}
	if session.Step != nil {
		step := &setupStepView{Kind: session.Step.Kind}
		if session.Step.Display != nil {
			step.Display = &setupDisplayView{
				Format:    session.Step.Display.Format,
				Payload:   session.Step.Display.Payload,
				ExpiresAt: session.Step.Display.ExpiresAt,
				Hint:      session.Step.Display.Hint,
			}
		}
		for _, field := range session.Step.Fields {
			step.Fields = append(step.Fields, setupFieldView{
				Name: field.Name, Kind: field.Kind, Label: field.Label,
			})
		}
		if session.Step.Approval != nil {
			step.Approval = &setupApprovalView{
				Destination: session.Step.Approval.Destination,
				Instruction: session.Step.Approval.Instruction,
			}
		}
		if session.Step.Profile != nil {
			profile := &setupProfileView{
				Methods:     session.Step.Profile.Methods,
				Options:     session.Step.Profile.Options,
				AllowCreate: session.Step.Profile.AllowCreate,
			}
			for _, param := range session.Step.Profile.Params {
				profile.Params = append(profile.Params, setupFieldView{
					Name: param.Name, Kind: param.Kind, Label: param.Label,
				})
			}
			step.Profile = profile
		}
		view.Step = step
	}
	return view
}

type setupBeginRequest struct {
	Method     string            `json:"method"`
	RequestKey string            `json:"request_key"`
	Params     map[string]string `json:"params"`
}

type setupInputRequest struct {
	Revision   int64             `json:"revision"`
	RequestKey string            `json:"request_key"`
	Values     map[string]string `json:"values"`
}

func decodeBindingBody(
	w http.ResponseWriter, r *http.Request, dst any,
) bool {
	err := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, bindingBodyLimit)).Decode(dst)
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return false
	}
	return true
}

// setupEndpoints resolves the bot's neutral lifecycle and setup runner. The
// bot is known to exist and the scope is authorized before this runs, so an
// unresolvable bot answers 404 and a definition without setup answers 422
// unsupported_operation, never 500 (ADR 0052, ADR 0053).
func (s *Server) setupEndpoints(
	w http.ResponseWriter, r *http.Request, scope, bot string,
) (botflota.AccountLifecycle, botflota.SetupRunner, bool) {
	if s.setups == nil {
		writeError(w, http.StatusNotImplemented, "unsupported_operation",
			"account setup is not available on this instance")
		return nil, nil, false
	}
	lifecycle, runner, err := s.setups.SetupFor(scope, bot)
	switch {
	case errors.Is(err, botflota.ErrNoSetup):
		writeError(w, http.StatusUnprocessableEntity, "unsupported_operation",
			"that channel offers no account setup")
		return nil, nil, false
	case err != nil:
		s.fail(w, "resolve account setup", err)
		return nil, nil, false
	}
	return lifecycle, runner, true
}

func (s *Server) setupRunner(
	w http.ResponseWriter, r *http.Request, scope, bot string,
) (botflota.SetupRunner, bool) {
	_, runner, ok := s.setupEndpoints(w, r, scope, bot)
	return runner, ok
}

// handleBindingInspect answers the committed binding with its actionable
// readiness condition; a bot without one stays inspectable (ADR 0053).
func (s *Server) handleBindingInspect(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	lifecycle, _, ok := s.setupEndpoints(w, r, scope, bot)
	if !ok {
		return
	}
	status, err := lifecycle.InspectBinding(r.Context(), scope, bot)
	if err != nil {
		s.fail(w, "inspect binding", err)
		return
	}
	writeJSON(w, http.StatusOK, bindingStatusViewOf(status))
}

// handleBindingMethods names the supported setup methods of the bot's
// definition; a definition with no remote setup reports none (ADR 0053).
func (s *Server) handleBindingMethods(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	runner, ok := s.setupRunner(w, r, scope, bot)
	if !ok {
		return
	}
	methods, err := runner.SetupMethods(r.Context(), scope, bot)
	if err != nil {
		s.fail(w, "read setup methods", err)
		return
	}
	if methods == nil {
		methods = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"methods": methods})
}

// handleSetupBegin stages one setup session until provider confirmation and
// local commit; a second live session answers 409 (ADR 0053).
func (s *Server) handleSetupBegin(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	var request setupBeginRequest
	if !decodeBindingBody(w, r, &request) {
		return
	}
	runner, ok := s.setupRunner(w, r, scope, bot)
	if !ok {
		return
	}
	unlock := s.lockBot(scope, bot)
	defer unlock()
	session, err := runner.BeginSetup(r.Context(), scope, bot, botflota.SetupBegin{
		Method: request.Method, RequestKey: request.RequestKey, Params: request.Params,
	})
	switch {
	case errors.Is(err, botflota.ErrBusy):
		writeError(w, http.StatusConflict, "setup_in_progress",
			"a setup session is already running for that bot")
	case errors.Is(err, botflota.ErrConflict):
		writeError(w, http.StatusConflict, "conflict",
			"that request key is already used with different parameters")
	case errors.Is(err, botflota.ErrUnsupportedKind):
		writeError(w, http.StatusUnprocessableEntity, "unsupported_operation",
			"that setup method is not supported")
	case err != nil:
		s.fail(w, "begin setup", err)
	default:
		writeSetupSession(w, http.StatusAccepted, session)
	}
}

// handleSetupRead follows one setup session; challenges are authenticated
// and never cacheable (ADR 0053).
func (s *Server) handleSetupRead(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	runner, ok := s.setupRunner(w, r, scope, bot)
	if !ok {
		return
	}
	session, err := runner.ReadSetup(r.Context(), scope, bot, r.PathValue("id"))
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no setup session with that id")
		return
	}
	if err != nil {
		s.fail(w, "read setup", err)
		return
	}
	writeSetupSession(w, http.StatusOK, session)
}

// handleSetupInput answers the session's pending step. The revision must
// match; a stale one is rejected without effect, and submitted values are
// never echoed back (ADR 0053).
func (s *Server) handleSetupInput(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	var request setupInputRequest
	if !decodeBindingBody(w, r, &request) {
		return
	}
	runner, ok := s.setupRunner(w, r, scope, bot)
	if !ok {
		return
	}
	unlock := s.lockBot(scope, bot)
	defer unlock()
	session, err := runner.InputSetup(
		r.Context(), scope, bot, r.PathValue("id"), botflota.SetupInput{
			Revision:   request.Revision,
			RequestKey: request.RequestKey,
			Values:     request.Values,
		})
	switch {
	case errors.Is(err, botflota.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no setup session with that id")
	case errors.Is(err, botflota.ErrConflict):
		writeError(w, http.StatusConflict, "conflict",
			"stale revision or conflicting request key")
	case err != nil:
		s.fail(w, "answer setup step", err)
	default:
		writeSetupSession(w, http.StatusOK, session)
	}
}

// handleSetupCancel cancels one setup session idempotently; a committed
// session keeps its actual outcome instead (ADR 0053).
func (s *Server) handleSetupCancel(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	runner, ok := s.setupRunner(w, r, scope, bot)
	if !ok {
		return
	}
	unlock := s.lockBot(scope, bot)
	defer unlock()
	err := runner.CancelSetup(r.Context(), scope, bot, r.PathValue("id"))
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no setup session with that id")
		return
	}
	if err != nil {
		s.fail(w, "cancel setup", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBindingDelete forgets the local binding under the bot's lock: the
// running bot stops first, letting an in-flight send finish, and only then
// is the binding removed. History, chats, and the directory stay; the
// provider account and any managed profile are never destroyed (ADR 0053).
func (s *Server) handleBindingDelete(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	lifecycle, _, ok := s.setupEndpoints(w, r, scope, bot)
	if !ok {
		return
	}
	unlock := s.lockBot(scope, bot)
	defer unlock()
	ctx := r.Context()
	status, err := lifecycle.InspectBinding(ctx, scope, bot)
	if err != nil {
		s.fail(w, "inspect binding", err)
		return
	}
	if status.Binding == nil {
		writeError(w, http.StatusNotFound, "not_found",
			"no committed binding for that bot")
		return
	}
	if err := s.fleet.StopBotForUnpair(scope, bot); err != nil {
		s.fail(w, "stop bot", err)
		return
	}
	if err := lifecycle.DetachBinding(ctx, scope, bot); err != nil {
		if errors.Is(err, botflota.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				"no committed binding for that bot")
			return
		}
		s.fail(w, "detach binding", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeSetupSession answers with a sanitized session: challenges must not be
// cached by intermediaries, and no submitted secret travels back (ADR 0053).
func writeSetupSession(w http.ResponseWriter, status int, session *botflota.SetupSession) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, setupSessionViewOf(session))
}
