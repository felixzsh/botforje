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

// KeyFile is the one-time clear admin key written under data_dir. The CLI
// reads it to call the API (ADR 0025).
const KeyFile = "admin.key"

// errUnauthenticated is the only failure the middleware reports as 401; every
// other resolution error is internal.
var errUnauthenticated = errors.New("api: unauthenticated")

// Auth resolves a request credential to a Principal: the admin key is the
// instance owner, and a stored API key is scoped. The clear credentials never
// enter logs.
type Auth struct {
	adminHash []byte
	tokens    botflota.APIKeyStore
}

// NewAuth builds the resolver from the admin key hash and the token store.
// Both are read-only: the admin key is bootstrapped separately.
func NewAuth(adminHash []byte, tokens botflota.APIKeyStore) *Auth {
	return &Auth{adminHash: adminHash, tokens: tokens}
}

// EnsureAuth loads the stored admin key and creates one on first run. created
// reports a new key, which is also written to dataDir/admin.key with mode
// 0600. The clear key never enters logs.
func EnsureAuth(
	ctx context.Context, store botflota.AdminKeyStore, dataDir string,
) (hash []byte, created bool, err error) {
	key, err := store.Get(ctx)
	switch {
	case err == nil:
		return key.Hash, false, nil
	case !errors.Is(err, botflota.ErrNotFound):
		return nil, false, fmt.Errorf("api: load admin key: %w", err)
	}
	hash, err = Rotate(ctx, store, dataDir)
	if err != nil {
		return nil, false, err
	}
	return hash, true, nil
}

// Rotate replaces the stored admin key with a fresh one and rewrites the key
// file. Use it when the key or its file is lost.
func Rotate(
	ctx context.Context, store botflota.AdminKeyStore, dataDir string,
) ([]byte, error) {
	clear, err := newSecret()
	if err != nil {
		return nil, err
	}
	// The clear key is 256 random bits, so a plain SHA-256 is enough: there is
	// no low-entropy secret to stretch.
	hash := sha256.Sum256([]byte(clear))
	key := &botflota.AdminKey{Hash: hash[:], CreatedAt: time.Now().UTC()}
	if err := store.Put(ctx, key); err != nil {
		return nil, fmt.Errorf("api: store admin key: %w", err)
	}
	if err := writeKeyFile(dataDir, clear); err != nil {
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
	if subtle.ConstantTimeCompare(sum[:], a.adminHash) == 1 {
		return &Principal{Kind: KindOwner}, nil
	}
	if a.tokens == nil {
		return nil, errUnauthenticated
	}
	key, err := a.tokens.FindByHash(r.Context(), sum[:])
	if errors.Is(err, botflota.ErrNotFound) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if !key.Active(time.Now()) {
		return nil, errUnauthenticated
	}
	scopes := make(map[string]bool, len(key.Scopes))
	for _, scope := range key.Scopes {
		scopes[scope] = true
	}
	return &Principal{Kind: KindScoped, Scopes: scopes}, nil
}

// newSecret returns 256 random bits as a URL-safe string, for the admin key
// and for minted API keys.
func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("api: generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeKeyFile(dataDir, clear string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("api: create data dir: %w", err)
	}
	path := filepath.Join(dataDir, KeyFile)
	if err := os.WriteFile(path, []byte(clear+"\n"), 0o600); err != nil {
		return fmt.Errorf("api: write admin key file: %w", err)
	}
	// WriteFile keeps the mode of an existing file; enforce privacy.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("api: protect admin key file: %w", err)
	}
	return nil
}
