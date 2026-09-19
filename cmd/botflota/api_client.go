package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/felixzsh/botflota/api"
	"github.com/felixzsh/botflota/config"
)

// apiClient calls the REST API of a running instance. Operational commands
// are clients of that API instead of opening the database themselves
// (ADR 0025).
type apiClient struct {
	base  string
	token string
	http  *http.Client
}

// newAPIClient resolves the base URL and the credential:
//
//   - BOTFLOTA_API_URL when set, otherwise address and port from the config.
//   - BOTFLOTA_TOKEN (owner or access token) when set, otherwise the
//     owner.token file under data_dir, which fleet run writes on first start.
func newAPIClient(cfg *config.Config) (*apiClient, error) {
	base := os.Getenv("BOTFLOTA_API_URL")
	if base == "" {
		if cfg.Port == 0 {
			return nil, errors.New(
				"port is 0, so the API is off; set BOTFLOTA_API_URL or a port")
		}
		address := cfg.Address
		if address == "" {
			address = "127.0.0.1"
		}
		base = "http://" + net.JoinHostPort(address, strconv.Itoa(cfg.Port))
	}
	token := os.Getenv("BOTFLOTA_TOKEN")
	if token == "" {
		dir, err := cfg.DataDirPath()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, api.OwnerTokenFile)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf(
				"no credential: set BOTFLOTA_TOKEN or start fleet run first (%s)", path)
		}
		token = strings.TrimSpace(string(data))
	}
	return &apiClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// get fetches a path and decodes the JSON body into out; a nil out discards
// the body.
func (c *apiClient) get(ctx context.Context, path string, out any) error {
	body, err := c.getRaw(ctx, path)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// getRaw fetches a path and returns the body, which a --json mode can pass
// through unchanged.
func (c *apiClient) getRaw(ctx context.Context, path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call the API at %s: %w", c.base, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, apiError(response.StatusCode, body)
	}
	return body, nil
}

// apiError turns the API's single error shape, `{"error":{"code","message"}}`,
// into an error for the terminal.
func apiError(status int, body []byte) error {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
		return fmt.Errorf("api: %s (%s)", parsed.Error.Message, http.StatusText(status))
	}
	return fmt.Errorf("api: unexpected response %d", status)
}
