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

// Config validation rejects a `${...}` reference earlier (ADR 0014); this is
// a last guard so a secret can never leave through a message.
var errSecretInMessage = errors.New("unresolved secret reference in outgoing message")

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
		case step.Media != nil:
			err = r.media(*step.Media)
		case step.Contact != nil:
			err = r.contact(*step.Contact)
		case step.Poll != nil:
			err = r.poll(*step.Poll)
		default:
			err = fmt.Errorf("empty step")
		}
		if err != nil {
			return fmt.Errorf("engine: %s step %d: %w", where, i, err)
		}
	}
	return nil
}

func (r *run) message(m spec.Message) error {
	recipient, err := r.recipient(m.To)
	if err != nil {
		return err
	}
	body := r.pickVariant(m.Body)
	if body != "" {
		rendered, renderErr := botflota.RenderTemplate(body, r.tctx.Get)
		if renderErr != nil {
			return renderErr
		}
		body = rendered
	}
	// A reply is preset unless the model really produced the body; a failed
	// realization that falls back to the anchor stays preset.
	origin := botflota.OriginPreset
	if m.Intent != "" {
		generated, err := r.engine.llm.Realize(r.ctx, botflota.RealizeRequest{
			Intent: m.Intent,
			Policy: m.Policy,
			Anchor: body,
			Text:   r.msg.Content.VisibleText(),
		})
		switch {
		case err == nil && generated != "":
			body = generated
			origin = botflota.OriginLLM
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
		Bot:            r.engine.bot,
		RecipientPhone: recipient,
		Content:        botflota.TextContent{Body: body},
		Origin:         origin,
		Routing:        r.routing,
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

// A `to` that resolves empty is an error, never a silent reply to the contact.
func (r *run) recipient(to string) (string, error) {
	if to == "" {
		return r.msg.FromPhone, nil
	}
	rendered, err := botflota.RenderTemplate(to, r.tctx.Get)
	if err != nil {
		return "", err
	}
	if rendered == "" {
		return "", fmt.Errorf("message recipient %q resolved empty", to)
	}
	return rendered, nil
}

func (r *run) media(m spec.Media) error {
	mediaType, ok := mediaTypeOf(m.Kind)
	if !ok {
		return fmt.Errorf("unknown media kind %q", m.Kind)
	}
	url, err := botflota.RenderTemplate(m.URL, r.tctx.Get)
	if err != nil {
		return err
	}
	path, err := botflota.RenderTemplate(m.Path, r.tctx.Get)
	if err != nil {
		return err
	}
	name, err := botflota.RenderTemplate(m.Name, r.tctx.Get)
	if err != nil {
		return err
	}
	caption, err := botflota.RenderTemplate(m.Caption, r.tctx.Get)
	if err != nil {
		return err
	}
	recipient, err := r.recipient(m.To)
	if err != nil {
		return err
	}
	switch {
	case url == "" && path == "":
		return errors.New("media step has neither url nor path")
	case url != "" && path != "":
		return errors.New("media step has both url and path")
	}
	for _, field := range []string{url, path, name, caption} {
		if err := checkNoSecret(field); err != nil {
			return err
		}
	}
	r.out = append(r.out, botflota.OutgoingMessage{
		Bot:            r.engine.bot,
		RecipientPhone: recipient,
		Content: botflota.MediaContent{
			Type: mediaType, URL: url, Path: path, Name: name,
			Caption: caption, MIME: m.MIME,
		},
		Origin:  botflota.OriginPreset,
		Routing: r.routing,
	})
	return nil
}

func mediaTypeOf(name string) (botflota.MediaType, bool) {
	mediaType := botflota.MediaType(name)
	if !mediaType.Valid() {
		return "", false
	}
	return mediaType, true
}

func (r *run) contact(c spec.Contact) error {
	name, err := botflota.RenderTemplate(c.Name, r.tctx.Get)
	if err != nil {
		return err
	}
	phone, err := botflota.RenderTemplate(c.Phone, r.tctx.Get)
	if err != nil {
		return err
	}
	recipient, err := r.recipient(c.To)
	if err != nil {
		return err
	}
	if name == "" || phone == "" {
		return errors.New("contact step needs a name and a phone")
	}
	for _, field := range []string{name, phone} {
		if err := checkNoSecret(field); err != nil {
			return err
		}
	}
	r.out = append(r.out, botflota.OutgoingMessage{
		Bot:            r.engine.bot,
		RecipientPhone: recipient,
		Content:        botflota.ContactContent{Name: name, Phone: phone},
		Origin:         botflota.OriginPreset,
		Routing:        r.routing,
	})
	return nil
}

func (r *run) poll(p spec.Poll) error {
	question, err := botflota.RenderTemplate(p.Question, r.tctx.Get)
	if err != nil {
		return err
	}
	options := make([]string, 0, len(p.Options))
	for _, option := range p.Options {
		rendered, err := botflota.RenderTemplate(option, r.tctx.Get)
		if err != nil {
			return err
		}
		options = append(options, rendered)
	}
	recipient, err := r.recipient(p.To)
	if err != nil {
		return err
	}
	if question == "" {
		return errors.New("poll step needs a question")
	}
	if len(options) < 2 {
		return errors.New("poll step needs at least two options")
	}
	for _, field := range append([]string{question}, options...) {
		if err := checkNoSecret(field); err != nil {
			return err
		}
	}
	r.out = append(r.out, botflota.OutgoingMessage{
		Bot:            r.engine.bot,
		RecipientPhone: recipient,
		Content: botflota.PollContent{
			Question: question, Options: options, Selectable: p.Selectable,
		},
		Origin:  botflota.OriginPreset,
		Routing: r.routing,
	})
	return nil
}

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
		if value.HasRef() {
			// A secret reference is revealed here, the one consumer that
			// sends it, and never rendered as a template (ADR 0014).
			headers[name] = value.Reveal()
			continue
		}
		rendered, err := botflota.RenderTemplate(value.Raw(), r.tctx.Get)
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
		Bot:            r.engine.bot,
		RecipientPhone: r.msg.FromPhone,
		Content: botflota.LocationContent{
			Latitude:    loc.Latitude,
			Longitude:   loc.Longitude,
			Name:        name,
			Address:     address,
			URL:         url,
			Description: description,
		},
		Origin:  botflota.OriginPreset,
		Routing: r.routing,
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
