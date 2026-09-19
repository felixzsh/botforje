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

// Auth validates the admin key on every request. This side only holds the
// hash; the clear key lives in the key file.
type Auth struct {
	hash []byte
}

// EnsureAuth loads the stored admin key and creates one on first run. created
// reports a new key, which is also written to dataDir/admin.key with mode
// 0600. The clear key never enters logs.
func EnsureAuth(
	ctx context.Context, store botflota.AdminKeyStore, dataDir string,
) (auth *Auth, created bool, err error) {
	key, err := store.Get(ctx)
	switch {
	case err == nil:
		return &Auth{hash: key.Hash}, false, nil
	case !errors.Is(err, botflota.ErrNotFound):
		return nil, false, fmt.Errorf("api: load admin key: %w", err)
	}
	auth, err = Rotate(ctx, store, dataDir)
	if err != nil {
		return nil, false, err
	}
	return auth, true, nil
}

// Rotate replaces the stored admin key with a fresh one and rewrites the key
// file. Use it when the key or its file is lost.
func Rotate(
	ctx context.Context, store botflota.AdminKeyStore, dataDir string,
) (*Auth, error) {
	clear, err := newAdminKey()
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
	return &Auth{hash: hash[:]}, nil
}

// Middleware requires the admin key on every request.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.match(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized",
				"missing or invalid admin key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) match(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return subtle.ConstantTimeCompare(sum[:], a.hash) == 1
}

func newAdminKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("api: generate admin key: %w", err)
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
