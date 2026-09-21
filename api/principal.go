package api

import (
	"context"
	"net/http"
)

// PrincipalResolver resolves a credential to a Principal. A host may swap in
// its own without changing any handler; Auth is the built-in one
// (ROADMAP Phase 13).
type PrincipalResolver interface {
	Resolve(r *http.Request) (*Principal, error)
}

type Kind string

const (
	KindOwner Kind = "owner"

	KindScoped Kind = "scoped"
)

// Principal is what handlers authorize against; they never see the credential
// itself (ROADMAP Phase 10).
type Principal struct {
	Kind   Kind
	Scopes map[string]bool
}

// Allows covers both reading and changing a scope.
func (p *Principal) Allows(scope string) bool {
	if p == nil {
		return false
	}
	if p.Kind == KindOwner {
		return true
	}
	return p.Scopes[scope]
}

func (p *Principal) Owner() bool { return p != nil && p.Kind == KindOwner }

type principalContextKey struct{}

func withPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// principalFrom reads the Principal the middleware always set before a
// handler runs.
func principalFrom(ctx context.Context) *Principal {
	principal, _ := ctx.Value(principalContextKey{}).(*Principal)
	return principal
}
