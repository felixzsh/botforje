package api

import (
	"context"
	"net/http"
)

// PrincipalResolver turns the credential of a request into a Principal. Auth
// is the built-in implementation; a host may inject another resolver, for
// example for delegated sign-in, and the handlers never change (ROADMAP
// Phase 13).
type PrincipalResolver interface {
	Resolve(r *http.Request) (*Principal, error)
}

// Kind is the authority of an authenticated principal.
type Kind string

const (
	// KindOwner is the instance owner: every scope plus instance settings and
	// credential management (ADR 0025).
	KindOwner Kind = "owner"
	// KindScoped is a credential limited to a set of scopes (ROADMAP
	// Phase 13).
	KindScoped Kind = "scoped"
)

// Principal is the authenticated caller behind a request, resolved once in the
// middleware. Authorization reads it and never the credential, so Phase 13
// swaps the resolver instead of every handler (ROADMAP Phase 10).
type Principal struct {
	Kind   Kind
	Scopes map[string]bool
}

// Allows reports whether the principal may read or change scope.
func (p *Principal) Allows(scope string) bool {
	if p == nil {
		return false
	}
	if p.Kind == KindOwner {
		return true
	}
	return p.Scopes[scope]
}

// Owner reports whether the principal is the instance owner.
func (p *Principal) Owner() bool { return p != nil && p.Kind == KindOwner }

type principalContextKey struct{}

func withPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// principalFrom returns the authenticated caller. The middleware always sets
// it before a handler runs.
func principalFrom(ctx context.Context) *Principal {
	principal, _ := ctx.Value(principalContextKey{}).(*Principal)
	return principal
}
