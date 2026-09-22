package main

import (
	"bytes"
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

// apiClient calls the REST API of a running instance instead of opening the
// database itself (ADR 0025).
type apiClient struct {
	base  string
	token string
	http  *http.Client
}

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
		http:  &http.Client{},
	}, nil
}

// apiTimeout keeps a hung instance from hanging the terminal; a sync call
// overrides it because the phone decides how long it takes.
const apiTimeout = 10 * time.Second

// get: a nil out discards the body.
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

func (c *apiClient) getRaw(ctx context.Context, path string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, nil, apiTimeout)
}

// delete calls a destructive route with no body; its timeout bounds the whole
// call because the instance drains the stopped bot before answering.
func (c *apiClient) delete(
	ctx context.Context, path string, timeout time.Duration,
) error {
	_, err := c.do(ctx, http.MethodDelete, path, nil, timeout)
	return err
}

// post's timeout bounds the whole call, because some operations wait on the
// phone.
func (c *apiClient) post(
	ctx context.Context, path string, out any, timeout time.Duration,
) error {
	return c.postJSON(ctx, path, nil, out, timeout)
}

// postJSON sends a JSON payload and decodes the response; a nil payload
// sends no body.
func (c *apiClient) postJSON(
	ctx context.Context, path string, payload, out any, timeout time.Duration,
) error {
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = encoded
	}
	result, err := c.do(ctx, http.MethodPost, path, body, timeout)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(result, out)
}

func (c *apiClient) do(
	ctx context.Context, method, path string, body []byte, timeout time.Duration,
) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path,
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf(
			"call the API at %s (is fleet run up?): %w", c.base, err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, apiError(response.StatusCode, answer)
	}
	return answer, nil
}

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
