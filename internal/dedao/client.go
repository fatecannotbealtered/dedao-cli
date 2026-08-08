package dedao

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	// BaseURL is the only host this client talks to.
	BaseURL = "https://www.dedao.cn"

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

	// maxResponseBytes bounds every response so a hostile or broken upstream
	// cannot exhaust memory.
	maxResponseBytes = 8 << 20
)

// APIError carries the upstream failure in structured form.
//
// The status code is a field, never interpolated into the message: CLI-SPEC §6
// requires the error code to be derived from the status, and message-text
// sniffing misclassifies bodies that merely contain words like "not found".
type APIError struct {
	StatusCode   int
	Method       string
	Path         string
	Message      string
	BusinessCode any
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

// ErrAuthRequired signals no usable session; commands map it to E_AUTH.
var ErrAuthRequired = errors.New("login required; run `dedao-cli login`")

// Client is a read-only Dedao web API client.
type Client struct {
	http     *http.Client
	jar      *cookiejar.Jar
	stateDir string
	baseURL  string
	timeout  time.Duration
}

// Options configures a Client.
type Options struct {
	StateDir string
	Timeout  time.Duration
	// BaseURL overrides the upstream host. Production always leaves it empty;
	// it exists so contract tests can point the real client at a mock upstream
	// and exercise the true transport path rather than a stubbed one.
	BaseURL string
}

func New(options Options) (*Client, error) {
	stateDir, err := StateDir(options.StateDir)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	loadCookies(jar, stateDir)

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	baseURL := strings.TrimSuffix(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = BaseURL
	}
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Jar:     jar,
			Timeout: timeout,
			// Proxy deliberately unset: the session cookie is an account-level
			// credential and must not be routed through an inherited proxy.
			Transport: &http.Transport{},
		},
		jar:      jar,
		stateDir: stateDir,
		timeout:  timeout,
	}, nil
}

// StateDirectory reports where the session is persisted.
func (c *Client) StateDirectory() string { return c.stateDir }

// Authenticated reports whether a session cookie is loaded. It does not probe
// the network; `context` and `doctor` use it for a cheap local answer.
func (c *Client) Authenticated() bool {
	base, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return false
	}
	return len(c.jar.Cookies(base)) > 0
}

// SaveSession persists the current cookie jar.
func (c *Client) SaveSession() error { return saveCookies(c.jar, c.stateDir) }

// SessionProbe is the outcome of a cheap liveness check on the stored session.
type SessionProbe struct {
	Configured bool   `json:"configured"`
	Checked    bool   `json:"checked"`
	Valid      bool   `json:"valid"`
	Reason     string `json:"reason"`
}

// ProbeSession answers whether the stored cookie still authenticates.
//
// A cookie on disk is not the same as a live session: an expired one is present
// but useless, and reporting it as valid would send an agent down a path that
// can only 401. The probe is bounded by a short timeout and never returns an
// error -- an unreachable network yields Checked=false, which is honest, rather
// than a false negative.
func (c *Client) ProbeSession(ctx context.Context) SessionProbe {
	if !c.Authenticated() {
		return SessionProbe{Reason: "no session cookie stored"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if _, err := c.requestJSON(probeCtx, "GET", "/api/pc/user/info", nil, nil); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
			return SessionProbe{Configured: true, Checked: true, Reason: "session expired; log in again"}
		}
		if errors.As(err, &apiErr) && apiErr.BusinessCode != nil {
			return SessionProbe{Configured: true, Checked: true, Reason: "session rejected by upstream"}
		}
		return SessionProbe{Configured: true, Reason: "could not reach Dedao to verify the session"}
	}
	return SessionProbe{Configured: true, Checked: true, Valid: true, Reason: "session accepted"}
}

// Logout drops the session both in memory and on disk.
func (c *Client) Logout() error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	c.jar = jar
	c.http.Jar = jar
	return clearCookies(c.stateDir)
}

func (c *Client) requireAuth() error {
	if !c.Authenticated() {
		return ErrAuthRequired
	}
	return nil
}

// envelope is Dedao's uniform response wrapper: `h` carries the business status
// and `c` the content.
type envelope struct {
	H struct {
		C any    `json:"c"`
		E string `json:"e"`
	} `json:"h"`
	C json.RawMessage `json:"c"`
}

// referer overrides the default Referer for one call. Some endpoints only
// issue a content token when the Referer matches the page that would legitimately
// be making the request, so the client sends the same one the web front-end does.
func (c *Client) doWithReferer(ctx context.Context, method, path string, params url.Values, body any, referer string) ([]byte, error) {
	return c.request(ctx, method, path, params, body, referer)
}

func (c *Client) do(ctx context.Context, method, path string, params url.Values, body any) ([]byte, error) {
	return c.request(ctx, method, path, params, body, c.baseURL+"/")
}

func (c *Client) request(ctx context.Context, method, path string, params url.Values, body any, referer string) ([]byte, error) {
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

	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("xi-dt", "web")
	request.Header.Set("Referer", referer)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		return nil, &APIError{
			StatusCode: response.StatusCode,
			Method:     method,
			Path:       path,
			Message:    fmt.Sprintf("%s %s returned HTTP %d", method, path, response.StatusCode),
		}
	}
	return payload, nil
}

// requestJSON performs one call and returns the sanitized `c` content.
func (c *Client) requestJSON(ctx context.Context, method, path string, params url.Values, body any) (any, error) {
	return c.requestJSONWithReferer(ctx, method, path, params, body, c.baseURL+"/")
}

func (c *Client) requestJSONWithReferer(ctx context.Context, method, path string, params url.Values, body any, referer string) (any, error) {
	value, err := c.requestJSONUnsanitized(ctx, method, path, params, body, referer)
	if err != nil {
		return nil, err
	}
	return Sanitize(value), nil
}

// requestJSONUnsanitized returns the payload with nothing stripped.
//
// Sanitize() drops any key whose name contains "token", which is right for
// anything the CLI prints -- but the article flow has to READ a field called
// dd_article_token to fetch the body at all. Sanitizing on the way in silently
// removed it and made every article look unentitled. Redaction belongs on the
// way out, so internal reads use this and every emitted payload is sanitized by
// the caller.
func (c *Client) requestJSONUnsanitized(ctx context.Context, method, path string, params url.Values, body any, referer string) (any, error) {
	raw, err := c.doWithReferer(ctx, method, path, params, body, referer)
	if err != nil {
		return nil, err
	}
	// Dedao occasionally prefixes payloads with a UTF-8 BOM.
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))

	var wrapper envelope
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, &APIError{
			StatusCode: 0,
			Method:     method,
			Path:       path,
			Message:    fmt.Sprintf("%s %s did not return JSON", method, path),
		}
	}
	if code := wrapper.H.C; code != nil && !isZeroCode(code) {
		message := wrapper.H.E
		if strings.TrimSpace(message) == "" {
			message = fmt.Sprintf("business code %v", code)
		}
		return nil, &APIError{
			StatusCode:   0,
			Method:       method,
			Path:         path,
			Message:      fmt.Sprintf("%s %s failed: %s", method, path, message),
			BusinessCode: code,
		}
	}
	if len(wrapper.C) == 0 {
		return map[string]any{}, nil
	}
	var content any
	if err := json.Unmarshal(wrapper.C, &content); err != nil {
		return nil, err
	}
	return unwrapSearchEnvelope(content), nil
}

// unwrapSearchEnvelope strips the second envelope the search backend nests
// inside Dedao's own.
//
// `/api/search/pc/tophits` answers `c: {status: {...}, data: {...}}`, so a
// caller that stopped at `c` got transport plumbing -- an apm id, timings, a
// duplicate status code -- and had to dig for the hits. Everything the command
// is for lives under `data`.
func unwrapSearchEnvelope(content any) any {
	object, ok := content.(map[string]any)
	if !ok || len(object) != 2 {
		return content
	}
	status, hasStatus := object["status"].(map[string]any)
	payload, hasData := object["data"]
	if !hasStatus || !hasData {
		return content
	}
	if _, isCoded := status["code"]; !isCoded {
		return content
	}
	return payload
}

func isZeroCode(value any) bool {
	switch typed := value.(type) {
	case float64:
		return typed == 0
	case int:
		return typed == 0
	case string:
		return typed == "" || typed == "0"
	default:
		return false
	}
}

// get is the read helper every query command funnels through.
func (c *Client) get(ctx context.Context, path string, params url.Values) (any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	return c.requestJSON(ctx, http.MethodGet, path, params, nil)
}

// post is the read helper for endpoints Dedao exposes over POST. Every caller
// is still a query: this client never mutates account state.
func (c *Client) post(ctx context.Context, path string, body any) (any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	return c.requestJSON(ctx, http.MethodPost, path, nil, body)
}
