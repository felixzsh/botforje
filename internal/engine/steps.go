package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/felixzsh/botflota/internal/config"
	"github.com/felixzsh/botflota/internal/domain"
	"github.com/felixzsh/botflota/internal/template"
)

const defaultRequestTimeout = 10 * time.Second

func (r *run) steps(steps []config.Step, where string) error {
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

// message renders a reply (ADR 0003). The body is the deterministic text and
// the fallback; the intent asks the LLM to realize it. With no provider, an
// anchored message keeps the body and an intent-only message fails. An
// optional To overrides the recipient; a To that resolves empty is an error,
// never a silent reply to the sender.
func (r *run) message(m config.Message) error {
	recipient := r.msg.Sender
	if m.To != "" {
		rendered, err := template.Render(m.To, r.tctx)
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
		rendered, err := template.Render(body, r.tctx)
		if err != nil {
			return err
		}
		body = rendered
	}
	if m.Intent != "" {
		generated, err := r.engine.llm.Realize(r.ctx, domain.RealizeRequest{
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
	r.out = append(r.out, domain.OutgoingMessage{
		Bot:       r.engine.bot,
		Recipient: recipient,
		Body:      body,
		Kind:      domain.KindText,
	})
	return nil
}

func (r *run) pickVariant(body config.Text) string {
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
func (r *run) request(req config.Request) error {
	url, err := template.Render(req.URL, r.tctx)
	if err != nil {
		return err
	}
	body, err := template.Render(req.Body, r.tctx)
	if err != nil {
		return err
	}
	headers := make(map[string]string, len(req.Headers))
	for name, value := range req.Headers {
		rendered, err := template.Render(value, r.tctx)
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
) (template.RequestResult, error) {
	ctx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return template.RequestResult{}, err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := r.engine.http.Do(request)
	if err != nil {
		return template.RequestResult{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return template.RequestResult{}, err
	}
	return template.RequestResult{
		Status:  response.StatusCode,
		Headers: flattenHeaders(response.Header),
		Body:    decodeBody(response.Header.Get("Content-Type"), data),
	}, nil
}

func (r *run) location(loc config.Location) error {
	name, err := template.Render(loc.Name, r.tctx)
	if err != nil {
		return err
	}
	address, err := template.Render(loc.Address, r.tctx)
	if err != nil {
		return err
	}
	url, err := template.Render(loc.URL, r.tctx)
	if err != nil {
		return err
	}
	description, err := template.Render(loc.Description, r.tctx)
	if err != nil {
		return err
	}
	r.out = append(r.out, domain.OutgoingMessage{
		Bot:       r.engine.bot,
		Recipient: r.msg.Sender,
		Kind:      domain.KindLocation,
		Location: &domain.Location{
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
