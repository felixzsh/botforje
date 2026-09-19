package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/felixzsh/botflota"
)

// OwnerTokenFile is the one-time clear owner token written under data_dir. The
// CLI reads it to call the API (ADR 0025).
const OwnerTokenFile = "owner.token"

// errUnauthenticated is the only failure the middleware reports as 401; every
// other resolution error is internal.
var errUnauthenticated = errors.New("api: unauthenticated")

// Auth resolves a request credential to a Principal: the owner token is the
// instance owner, and a stored access token is scoped. The clear credentials
// never enter logs.
type Auth struct {
	ownerHash []byte
	tokens    botflota.AccessTokenStore
}

// NewAuth builds the resolver from the owner token hash and the access-token
// store. Both are read-only: the owner token is bootstrapped separately.
func NewAuth(ownerHash []byte, tokens botflota.AccessTokenStore) *Auth {
	return &Auth{ownerHash: ownerHash, tokens: tokens}
}

// EnsureOwnerToken loads the stored owner token and creates one on first run.
// created reports a new token, which is also written to dataDir/owner.token
// with mode 0600. The clear token never enters logs.
func EnsureOwnerToken(
	ctx context.Context, store botflota.OwnerTokenStore, dataDir string,
) (hash []byte, created bool, err error) {
	token, err := store.Get(ctx)
	switch {
	case err == nil:
		return token.Hash, false, nil
	case !errors.Is(err, botflota.ErrNotFound):
		return nil, false, fmt.Errorf("api: load owner token: %w", err)
	}
	hash, err = RotateOwnerToken(ctx, store, dataDir)
	if err != nil {
		return nil, false, err
	}
	return hash, true, nil
}

// RotateOwnerToken replaces the stored owner token with a fresh one and
// rewrites the token file. Use it when the token or its file is lost.
func RotateOwnerToken(
	ctx context.Context, store botflota.OwnerTokenStore, dataDir string,
) ([]byte, error) {
	clear, err := newToken()
	if err != nil {
		return nil, err
	}
	// The clear token is 256 random bits, so a plain SHA-256 is enough: there
	// is no low-entropy secret to stretch.
	hash := sha256.Sum256([]byte(clear))
	token := &botflota.OwnerToken{Hash: hash[:], CreatedAt: time.Now().UTC()}
	if err := store.Put(ctx, token); err != nil {
		return nil, fmt.Errorf("api: store owner token: %w", err)
	}
	if err := writeOwnerTokenFile(dataDir, clear); err != nil {
		return nil, err
	}
	return hash[:], nil
}

// Resolve turns the request credential into a Principal. A missing, unknown,
// revoked, or expired credential fails with errUnauthenticated.
func (a *Auth) Resolve(r *http.Request) (*Principal, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, errUnauthenticated
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	if subtle.ConstantTimeCompare(sum[:], a.ownerHash) == 1 {
		return &Principal{Kind: KindOwner}, nil
	}
	if a.tokens == nil {
		return nil, errUnauthenticated
	}
	access, err := a.tokens.FindByHash(r.Context(), sum[:])
	if errors.Is(err, botflota.ErrNotFound) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if !access.Active(time.Now()) {
		return nil, errUnauthenticated
	}
	scopes := make(map[string]bool, len(access.Scopes))
	for _, scope := range access.Scopes {
		scopes[scope] = true
	}
	return &Principal{Kind: KindScoped, Scopes: scopes}, nil
}

// newToken returns 256 random bits as a URL-safe string, for the owner token
// and for minted access tokens.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("api: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeOwnerTokenFile(dataDir, clear string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("api: create data dir: %w", err)
	}
	path := filepath.Join(dataDir, OwnerTokenFile)
	if err := os.WriteFile(path, []byte(clear+"\n"), 0o600); err != nil {
		return fmt.Errorf("api: write owner token file: %w", err)
	}
	// WriteFile keeps the mode of an existing file; enforce privacy.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("api: protect owner token file: %w", err)
	}
	return nil
}
