package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

const defaultRequestTimeout = 10 * time.Second

// errSecretInMessage reports a `${...}` reference that reached an outgoing
// message. Config validation rejects it earlier (ADR 0014); this is a last
// guard so a secret can never leave through a message.
var errSecretInMessage = errors.New("unresolved secret reference in outgoing message")

// checkNoSecret rejects a `${...}` reference in an outgoing string. Templates
// use `{{...}}`, and secrets stay inside their field.
func checkNoSecret(text string) error {
	if strings.Contains(text, "${") {
		return errSecretInMessage
	}
	return nil
}

func (r *run) steps(steps []spec.Step, where string) error {
	for i, step := range steps {
		var err error
		switch {
		case step.Message != nil:
			err = r.message(*step.Message)
		case step.Request != nil:
			err = r.request(*step.Request)
		case step.Location != nil:
			err = r.location(*step.Location)
		default:
			err = fmt.Errorf("empty step")
		}
		if err != nil {
			return fmt.Errorf("engine: %s step %d: %w", where, i, err)
		}
	}
	return nil
}

// message renders a reply (ADR 0032). The body is the deterministic text and
// the fallback; the intent asks the LLM to realize it. With no provider, an
// anchored message keeps the body and an intent-only message fails. An
// optional To overrides the recipient; a To that resolves empty is an error,
// never a silent reply to the sender.
func (r *run) message(m spec.Message) error {
	recipient := r.msg.Sender
	if m.To != "" {
		rendered, err := botflota.RenderTemplate(m.To, r.tctx.Get)
		if err != nil {
			return err
		}
		if rendered == "" {
			return fmt.Errorf("message recipient %q resolved empty", m.To)
		}
		recipient = rendered
	}
	body := r.pickVariant(m.Body)
	if body != "" {
		rendered, err := botflota.RenderTemplate(body, r.tctx.Get)
		if err != nil {
			return err
		}
		body = rendered
	}
	if m.Intent != "" {
		generated, err := r.engine.llm.Realize(r.ctx, botflota.RealizeRequest{
			Intent: m.Intent,
			Policy: m.Policy,
			Anchor: body,
			Text:   r.msg.Body,
		})
		switch {
		case err == nil && generated != "":
			body = generated
		case err != nil && body == "":
			return err
		}
	}
	if body == "" {
		return nil
	}
	if err := checkNoSecret(body); err != nil {
		return err
	}
	r.out = append(r.out, botflota.OutgoingMessage{
		Bot:       r.engine.bot,
		Recipient: recipient,
		Body:      body,
		Kind:      botflota.KindText,
	})
	return nil
}

func (r *run) pickVariant(body spec.Text) string {
	switch len(body) {
	case 0:
		return ""
	case 1:
		return body[0]
	default:
		return body[r.engine.rand.Intn(len(body))]
	}
}

// request renders and performs an HTTP call, retrying on failure, and stores
// the captured result under the node root as {{node.<name>.resp.*}}.
func (r *run) request(req spec.Request) error {
	url, err := botflota.RenderTemplate(req.URL, r.tctx.Get)
	if err != nil {
		return err
	}
	body, err := botflota.RenderTemplate(req.Body, r.tctx.Get)
	if err != nil {
		return err
	}
	headers := make(map[string]string, len(req.Headers))
	for name, value := range req.Headers {
		rendered, err := botflota.RenderTemplate(value, r.tctx.Get)
		if err != nil {
			return err
		}
		headers[name] = rendered
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	timeout := defaultRequestTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt <= req.Retry; attempt++ {
		started := time.Now()
		result, err := r.doRequest(method, url, headers, body, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		result.Duration = time.Since(started)
		return r.tctx.SetRequest(req.Name, result)
	}
	return fmt.Errorf("request %q: %w", req.Name, lastErr)
}

func (r *run) doRequest(
	method, url string, headers map[string]string, body string, timeout time.Duration,
) (requestResult, error) {
	ctx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return requestResult{}, err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := r.engine.http.Do(request)
	if err != nil {
		return requestResult{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return requestResult{}, err
	}
	return requestResult{
		Status:  response.StatusCode,
		Headers: flattenHeaders(response.Header),
		Body:    decodeBody(response.Header.Get("Content-Type"), data),
	}, nil
}

func (r *run) location(loc spec.Location) error {
	name, err := botflota.RenderTemplate(loc.Name, r.tctx.Get)
	if err != nil {
		return err
	}
	address, err := botflota.RenderTemplate(loc.Address, r.tctx.Get)
	if err != nil {
		return err
	}
	url, err := botflota.RenderTemplate(loc.URL, r.tctx.Get)
	if err != nil {
		return err
	}
	description, err := botflota.RenderTemplate(loc.Description, r.tctx.Get)
	if err != nil {
		return err
	}
	for _, field := range []string{name, address, url, description} {
		if err := checkNoSecret(field); err != nil {
			return err
		}
	}
	r.out = append(r.out, botflota.OutgoingMessage{
		Bot:       r.engine.bot,
		Recipient: r.msg.Sender,
		Kind:      botflota.KindLocation,
		Location: &botflota.Location{
			Latitude:    loc.Latitude,
			Longitude:   loc.Longitude,
			Name:        name,
			Address:     address,
			URL:         url,
			Description: description,
		},
	})
	return nil
}

func flattenHeaders(header http.Header) map[string]string {
	out := make(map[string]string, len(header))
	for name, values := range header {
		out[name] = strings.Join(values, ", ")
	}
	return out
}

func decodeBody(contentType string, data []byte) any {
	if strings.Contains(contentType, "application/json") {
		var decoded any
		if err := json.Unmarshal(data, &decoded); err == nil {
			return decoded
		}
	}
	return string(data)
}
