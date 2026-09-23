package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/felixzsh/botflota"
)

// OwnerTokenFile is the clear-token file the CLI reads to call the API.
const OwnerTokenFile = "owner.token"

// ErrUnauthenticated is the only resolution error that answers 401. A custom
// resolver must return it (or wrap it) for a missing, unknown, revoked, or
// expired credential, so a failure stays a 401 and not a 500.
var ErrUnauthenticated = errors.New("api: unauthenticated")

// Auth resolves a request credential to a Principal; clear credentials never
// enter logs.
type Auth struct {
	ownerHash []byte
	tokens    botflota.AccessTokenStore
}

func NewAuth(ownerHash []byte, tokens botflota.AccessTokenStore) *Auth {
	return &Auth{ownerHash: ownerHash, tokens: tokens}
}

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

func (a *Auth) Resolve(r *http.Request) (*Principal, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, ErrUnauthenticated
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	// The budget key derives from the credential's own hash: enough to tell
	// one token from another without storing or exposing the token.
	key := hex.EncodeToString(sum[:16])
	if subtle.ConstantTimeCompare(sum[:], a.ownerHash) == 1 {
		return &Principal{Kind: KindOwner, Key: key}, nil
	}
	if a.tokens == nil {
		return nil, ErrUnauthenticated
	}
	access, err := a.tokens.FindByHash(r.Context(), sum[:])
	if errors.Is(err, botflota.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if !access.Active(time.Now()) {
		return nil, ErrUnauthenticated
	}
	scopes := make(map[string]bool, len(access.Scopes))
	for _, scope := range access.Scopes {
		scopes[scope] = true
	}
	return &Principal{Kind: KindScoped, Scopes: scopes, Key: key}, nil
}

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
