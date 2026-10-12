package botflota

import (
	"errors"
	"log/slog"
	"time"
)

// ErrNoSetup reports a channel definition that offers no account setup;
// the operation is unsupported, never a failure (ADR 0052, ADR 0053).
var ErrNoSetup = errors.New("no account setup offered")

// PairState is one live pairing session's lifecycle (ADR 0025).
type PairState string

const (
	PairPending   PairState = "pending"
	PairWaiting   PairState = "waiting"
	PairSucceeded PairState = "paired"
	PairFailed    PairState = "failed"
	PairCancelled PairState = "cancelled"
	PairExpired   PairState = "expired"
)

type PairRequest struct {
	// FullHistory asks for the full history window at registration (ADR 0035).
	FullHistory bool
}

// PairingResult is what a finished pairing stored, for whoever asked for the
// session instead of a log (ADR 0038). Identity is the normalized own account
// identity the adapter committed; the device locator stays in adapter-owned
// storage (ADR 0051). Import and address-book counts describe what the
// pairing observed; they never define generic account success (ADR 0053).
type PairingResult struct {
	Identity    IdentityKey
	Chunks      int
	Chats       int
	Messages    int
	Duplicate   int
	Skipped     int
	AddressBook int
	Elapsed     time.Duration
}

// PairingSession is one bot's live pairing session, read until Final
// (ADR 0025). Code is the display challenge the owner scans; it reaches the
// response that asks for it and the screen, never a log line. ActivationError
// reports a runtime activation failure separately from the setup outcome: the
// account is committed even when the bot could not start on it (ADR 0053).
type PairingSession struct {
	Scope     string
	Bot       string
	Method    string // how the account links: "qr" for WhatsApp
	State     PairState
	Code      string // the latest display challenge to present
	StartedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
	Error     string
	Result    PairingResult
	// ActivationError is set when the committed account could not be
	// activated (the bot did not start on it). Empty means activation
	// succeeded or was intentionally skipped for a teardown.
	ActivationError string
}

func (s PairingSession) Final() bool {
	switch s.State {
	case PairSucceeded, PairFailed, PairCancelled, PairExpired:
		return true
	}
	return false
}

// LogValue logs the session without its payload: the code is the credential
// that links the account, so it may reach the response that asks for it and
// the QR on screen, never a log line (ADR 0025).
func (s PairingSession) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("scope", s.Scope),
		slog.String("bot", s.Bot),
		slog.String("method", s.Method),
		slog.String("state", string(s.State)),
		slog.Bool("code_present", s.Code != ""),
		slog.Time("started_at", s.StartedAt),
		slog.Time("updated_at", s.UpdatedAt),
		slog.Time("expires_at", s.ExpiresAt),
		slog.String("error", s.Error),
		slog.String("activation_error", s.ActivationError),
		slog.Any("result", s.Result),
	)
}

// SetupState is one account-setup session's lifecycle (ADR 0053). It is the
// neutral contract every adapter's setup speaks; a login QR challenge is one
// step kind of it, never a universal method.
type SetupState string

const (
	SetupStarting         SetupState = "starting"
	SetupAwaitingInput    SetupState = "awaiting_input"
	SetupAwaitingApproval SetupState = "awaiting_approval"
	SetupSucceeded        SetupState = "succeeded"
	SetupFailed           SetupState = "failed"
	SetupCancelled        SetupState = "cancelled"
	SetupExpired          SetupState = "expired"
)

// Final reports whether the session reached an outcome its reader can keep.
func (s SetupState) Final() bool {
	switch s {
	case SetupSucceeded, SetupFailed, SetupCancelled, SetupExpired:
		return true
	}
	return false
}

// Setup step kinds: the small closed set a session can ask for, not provider
// commands or a general form engine (ADR 0053).
const (
	// SetupStepDisplay presents an expiring challenge: a QR image payload or
	// a login URI. Its payload expiry is separate from the session deadline.
	SetupStepDisplay = "display"
	// SetupStepInput requests typed values: a credential, an identifier, a
	// verification code, or a password. Secret values are write-only and
	// never come back on a read.
	SetupStepInput = "input"
	// SetupStepApproval waits for an external provider event after the
	// owner follows a safe destination or instruction code.
	SetupStepApproval = "approval"
	// SetupStepProfile selects or creates a managed profile through an
	// approved method with bounded, typed parameters.
	SetupStepProfile = "profile"
)

// SetupFieldKinds are the accepted typed input kinds of a step.
const (
	SetupFieldCredential = "credential"
	SetupFieldIdentifier = "identifier"
	SetupFieldCode       = "code"
	SetupFieldPassword   = "password"
)

// SetupDisplay is an expiring challenge to present: a QR payload or a login
// URI. ExpiresAt is the payload's own deadline, never the session's.
type SetupDisplay struct {
	Format    string // "qr" or "uri"
	Payload   string
	ExpiresAt time.Time
	Hint      string
}

// SetupField is one typed value a step requests. Secret kinds are write-only:
// a read exposes only the name, kind, and label, never a submitted value.
type SetupField struct {
	Name  string
	Kind  string
	Label string
}

// SetupApproval waits for the provider after the owner acts elsewhere.
type SetupApproval struct {
	Destination string
	Instruction string
}

// SetupProfile selects or creates a managed profile of a shared backend.
// Options are the existing profiles the owner may select; AllowCreate permits
// creating one with at most Params, whose secret entries stay write-only.
type SetupProfile struct {
	Methods     []string
	Options     []string
	AllowCreate bool
	Params      []SetupField
}

// SetupStep is the pending step of a session, nil when the session waits for
// no owner action. Exactly one of Display, Fields, Approval, or Profile is
// set, matching Kind.
type SetupStep struct {
	Kind     string
	Display  *SetupDisplay
	Fields   []SetupField
	Approval *SetupApproval
	Profile  *SetupProfile
}

// SetupSession is one bot's neutral account-setup session (ADR 0053): the
// scoped bot, the session id, the method and account mode, the state and its
// revision, the times, a sanitized error code, and the optional typed next
// step. Revision advances on every visible change; an input names the
// revision it answers, so a stale retry cannot act twice.
type SetupSession struct {
	Bot       BotRef
	ID        string
	Method    string
	Mode      string
	State     SetupState
	Revision  int64
	StartedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
	// ErrorCode is sanitized and safe to show: it names the failure, never
	// the credential, identity, or provider detail behind it.
	ErrorCode string
	// ActivationError reports that the committed binding could not be
	// activated (the bot did not start on it) while the setup itself
	// succeeded. It carries a start failure, never a secret; the binding
	// stays usable and a new activation can be attempted.
	ActivationError string
	Step            *SetupStep
}

// Final reports whether the session reached an outcome.
func (s SetupSession) Final() bool { return s.State.Final() }

// LogValue logs the session without secrets: only step presence and field
// names travel, never payloads or submitted values.
func (s SetupSession) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("scope", s.Bot.Scope),
		slog.String("bot", s.Bot.Bot),
		slog.String("session", s.ID),
		slog.String("method", s.Method),
		slog.String("state", string(s.State)),
		slog.Int64("revision", s.Revision),
		slog.Time("started_at", s.StartedAt),
		slog.Time("updated_at", s.UpdatedAt),
		slog.Time("expires_at", s.ExpiresAt),
		slog.String("error", s.ErrorCode),
		slog.String("activation_error", s.ActivationError),
	}
	if s.Step != nil {
		attrs = append(attrs, slog.String("step", s.Step.Kind))
	}
	return slog.GroupValue(attrs...)
}

// SetupBegin starts one setup session. RequestKey makes the start idempotent:
// repeating the key returns the same session, while reusing it for different
// parameters is rejected. Keys are scoped to the bot and session. Params
// carries the method's bounded creation parameters, never a reusable secret
// the session must retain.
type SetupBegin struct {
	Method     string
	RequestKey string
	Params     map[string]string
}

// SetupInput answers the session's pending step. Revision must match the
// session's current revision; a stale one is rejected without effect.
// RequestKey makes the input idempotent: resubmitting the same key returns
// the recorded outcome instead of submitting the credential or code twice.
// Values holds the typed answers by field name; secret entries are
// write-only and never readable back.
type SetupInput struct {
	Revision   int64
	RequestKey string
	Values     map[string]string
}

// ReadinessCondition is the actionable account condition readiness exposes
// (ADR 0053): credential presence, provider authorization, transport health,
// and import progress are separate facts, and stored credentials or a
// connection-startup return never prove authorization. Missing transport
// reporting means unknown, carried as a reason rather than a permission.
type ReadinessCondition string

const (
	// ReadinessUnconfigured means no binding is committed; the bot is
	// inspectable and setup-capable.
	ReadinessUnconfigured ReadinessCondition = "unconfigured"
	// ReadinessConfigured means a binding is committed and its local
	// credentials are present; provider authorization and transport
	// health are reported as reasons, never assumed.
	ReadinessConfigured ReadinessCondition = "configured"
	// ReadinessActionRequired means the binding needs its owner: the
	// credentials are missing or the provider revoked them.
	ReadinessActionRequired ReadinessCondition = "action_required"
	// ReadinessUnavailable means the account cannot be used from here:
	// the backend is unreachable or the mode is not served.
	ReadinessUnavailable ReadinessCondition = "unavailable"
)

// BindingStatus is what inspection of one bot's account returns: the
// committed binding when one exists, its actionable condition, the safe
// reasons behind it, and the setup method to use when one applies.
type BindingStatus struct {
	Binding   *Binding
	Condition ReadinessCondition
	Reasons   []string
	Method    string
}
