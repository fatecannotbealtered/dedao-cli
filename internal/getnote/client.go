// Package getnote implements the small, explicit client used by the GetNote
// command namespace. It intentionally does not share Dedao cookies or hosts.
package getnote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const BaseURL = "https://openapi.biji.com"

const maxResponseBytes = 8 << 20

// APIError keeps transport and in-band failures typed so the command layer
// can map them to the fleet's stable E_* error codes.
type APIError struct {
	StatusCode   int
	Method       string
	Path         string
	Message      string
	BusinessCode any
	Details      map[string]any
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("%s %s failed", e.Method, e.Path)
}

var ErrAuthRequired = errors.New("note access is not authorized yet; run `dedao-cli login`")

type Options struct {
	APIKey     string
	ClientID   string
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
}

type Client struct {
	apiKey   string
	clientID string
	baseURL  string
	http     *http.Client
}

func New(options Options) *Client {
	base := strings.TrimSuffix(strings.TrimSpace(options.BaseURL), "/")
	if base == "" {
		base = BaseURL
	}
	hc := options.HTTPClient
	if hc == nil {
		timeout := options.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		hc = &http.Client{Timeout: timeout, Transport: &http.Transport{}}
	}
	return &Client{apiKey: strings.TrimSpace(options.APIKey), clientID: strings.TrimSpace(options.ClientID), baseURL: base, http: hc}
}

func (c *Client) Configured() bool { return c.apiKey != "" && c.clientID != "" }

func (c *Client) request(ctx context.Context, method, path string, params url.Values, body any) (any, error) {
	if !c.Configured() {
		return nil, ErrAuthRequired
	}
	target := c.baseURL + path
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Client-ID", c.clientID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, apiHTTPError(raw, resp.StatusCode, method, path, resp.Header)
	}
	return decode(raw, method, path)
}

func apiHTTPError(raw []byte, status int, method, path string, headers http.Header) *APIError {
	apiErr := &APIError{
		StatusCode: status,
		Method:     method,
		Path:       path,
		Message:    "GetNote upstream request failed",
		Details:    map[string]any{},
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err == nil {
		if object, ok := value.(map[string]any); ok {
			envelopeErr := apiEnvelopeError(object, method, path)
			apiErr.BusinessCode = envelopeErr.BusinessCode
			apiErr.Details = envelopeErr.Details
		}
	}
	if retryAfter := strings.TrimSpace(headers.Get("Retry-After")); retryAfter != "" {
		apiErr.Details["retry_after"] = retryAfter
	}
	if requestID := firstHeader(headers, "X-Request-ID", "X-Trace-ID"); requestID != "" {
		apiErr.Details["request_id"] = requestID
	}
	return apiErr
}

func firstHeader(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func decode(raw []byte, method, path string) (any, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, &APIError{Method: method, Path: path, Message: fmt.Sprintf("%s %s did not return JSON", method, path)}
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	if success, exists := object["success"].(bool); exists && !success {
		return nil, apiEnvelopeError(object, method, path)
	}
	if code, exists := object["code"]; exists && !zeroCode(code) {
		return nil, apiEnvelopeError(object, method, path)
	}
	if data, exists := object["data"]; exists {
		return data, nil
	}
	return value, nil
}

func apiEnvelopeError(object map[string]any, method, path string) *APIError {
	code := object["code"]
	if upstream, ok := object["error"].(map[string]any); ok {
		if upstreamCode, exists := upstream["code"]; exists {
			code = upstreamCode
		}
	}
	return &APIError{
		Method:       method,
		Path:         path,
		Message:      "GetNote upstream request failed",
		BusinessCode: code,
		Details:      getnoteErrorDetails(object),
	}
}

func getnoteErrorDetails(object map[string]any) map[string]any {
	details := map[string]any{}
	for _, key := range []string{"code", "message", "retry_after"} {
		if value, exists := object[key]; exists {
			details[key] = value
		}
	}
	copyFirstGetnoteField(details, object, "request_id", "request_id", "requestId")
	copyFirstGetnoteField(details, object, "trace_id", "trace_id", "traceId")
	if rateLimit, ok := object["rate_limit"].(map[string]any); ok {
		filtered := map[string]any{}
		for _, key := range []string{"retry_after", "limit", "remaining", "reset_at"} {
			if value, exists := rateLimit[key]; exists {
				filtered[key] = value
			}
		}
		if len(filtered) > 0 {
			details["rate_limit"] = filtered
		}
	}
	if upstream, exists := object["error"]; exists {
		switch value := upstream.(type) {
		case string:
			details["error"] = value
		case map[string]any:
			filtered := map[string]any{}
			for _, key := range []string{"code", "message", "reason", "retry_after"} {
				if field, ok := value[key]; ok {
					filtered[key] = field
				}
			}
			copyFirstGetnoteField(filtered, value, "request_id", "request_id", "requestId")
			copyFirstGetnoteField(filtered, value, "trace_id", "trace_id", "traceId")
			if len(filtered) > 0 {
				details["error"] = filtered
			}
		}
	}
	return details
}

func copyFirstGetnoteField(target, source map[string]any, targetKey string, sourceKeys ...string) {
	for _, key := range sourceKeys {
		if value, exists := source[key]; exists {
			target[targetKey] = value
			return
		}
	}
}

func zeroCode(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case float64:
		return v == 0
	case string:
		return strings.TrimSpace(v) == "" || v == "0" || strings.EqualFold(v, "ok") || strings.EqualFold(v, "success")
	case json.Number:
		return string(v) == "0"
	default:
		return false
	}
}

func (c *Client) get(ctx context.Context, path string, params url.Values) (any, error) {
	return c.request(ctx, http.MethodGet, path, params, nil)
}

func (c *Client) post(ctx context.Context, path string, body map[string]any) (any, error) {
	return c.request(ctx, http.MethodPost, path, nil, body)
}

func (c *Client) Notes(ctx context.Context, params url.Values) (any, error) {
	return c.get(ctx, "/open/api/v1/resource/note/list", params)
}

func (c *Client) Note(ctx context.Context, id string) (any, error) {
	return c.get(ctx, "/open/api/v1/resource/note/detail", url.Values{"id": {id}})
}

func (c *Client) Save(ctx context.Context, body map[string]any) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/note/save", body)
}

func (c *Client) Update(ctx context.Context, body map[string]any) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/note/update", body)
}

func (c *Client) Delete(ctx context.Context, id string) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/note/delete", map[string]any{"note_id": id})
}

func (c *Client) Share(ctx context.Context, id string, excludeAudio bool) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/note/sharing", map[string]any{"note_id": id, "share_exclude_audio": excludeAudio})
}

func (c *Client) Task(ctx context.Context, id string) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/note/task/progress", map[string]any{"task_id": id})
}

func (c *Client) Search(ctx context.Context, query, topicID string, topK int) (any, error) {
	body := map[string]any{"query": query}
	if topK > 0 {
		body["top_k"] = topK
	}
	path := "/open/api/v1/resource/recall"
	if topicID != "" {
		path = "/open/api/v1/resource/recall/knowledge"
		body["topic_id"] = topicID
	}
	return c.post(ctx, path, body)
}

func (c *Client) Tags(ctx context.Context, action string, body map[string]any) (any, error) {
	path := "/open/api/v1/resource/note/tags/add"
	if action == "delete" {
		path = "/open/api/v1/resource/note/tags/delete"
	}
	return c.post(ctx, path, body)
}

func (c *Client) TagList(ctx context.Context, noteID string) (any, error) {
	return c.Note(ctx, noteID)
}

func (c *Client) KnowledgeBases(ctx context.Context, params url.Values) (any, error) {
	return c.get(ctx, "/open/api/v1/resource/knowledge/list", params)
}

func (c *Client) KnowledgeNotes(ctx context.Context, id string, params url.Values) (any, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("topic_id", id)
	return c.get(ctx, "/open/api/v1/resource/knowledge/notes", params)
}

func (c *Client) KnowledgeCreate(ctx context.Context, body map[string]any) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/knowledge/create", body)
}

func (c *Client) KnowledgeAdd(ctx context.Context, body map[string]any) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/knowledge/note/batch-add", body)
}

func (c *Client) KnowledgeRemove(ctx context.Context, body map[string]any) (any, error) {
	return c.post(ctx, "/open/api/v1/resource/knowledge/note/remove", body)
}
