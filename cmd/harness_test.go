package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// result is one captured CLI invocation: the two streams and the exit code an
// agent would actually observe.
type result struct {
	Stdout string
	Stderr string
	Exit   int
}

// Envelope decodes stdout as the machine contract, failing the test if stdout
// is not exactly one JSON document.
func (r result) Envelope(t *testing.T) map[string]any {
	t.Helper()
	var envelope map[string]any
	decoder := json.NewDecoder(strings.NewReader(r.Stdout))
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %s", err, r.Stdout)
	}
	// CLI-SPEC §4: exactly one document on stdout, nothing trailing.
	if decoder.More() {
		t.Fatalf("stdout carried more than one JSON document:\n%s", r.Stdout)
	}
	return envelope
}

func (r result) Data(t *testing.T) map[string]any {
	t.Helper()
	envelope := r.Envelope(t)
	if ok, _ := envelope["ok"].(bool); !ok {
		t.Fatalf("expected a success envelope, got: %s", r.Stdout)
	}
	data, _ := envelope["data"].(map[string]any)
	return data
}

func (r result) ErrorCode(t *testing.T) string {
	t.Helper()
	envelope := r.Envelope(t)
	if ok, _ := envelope["ok"].(bool); ok {
		t.Fatalf("expected a failure envelope, got: %s", r.Stdout)
	}
	errorObject, _ := envelope["error"].(map[string]any)
	code, _ := errorObject["code"].(string)
	return code
}

// mockUpstream is a stand-in Dedao. Handlers are keyed by request path so a
// test states only the endpoints it cares about; anything unexpected is a 404
// the test can assert on rather than a silent pass.
type mockUpstream struct {
	server   *httptest.Server
	handlers map[string]http.HandlerFunc
	Requests []string
}

func newMockUpstream(t *testing.T) *mockUpstream {
	t.Helper()
	mock := &mockUpstream{handlers: map[string]http.HandlerFunc{}}
	mock.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.Requests = append(mock.Requests, r.Method+" "+r.URL.Path)
		if handler, ok := mock.handlers[r.URL.Path]; ok {
			handler(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"h":{"c":404,"e":"no handler"},"c":null}`))
	}))
	t.Cleanup(mock.server.Close)
	return mock
}

// OK registers a Dedao-shaped success: business code 0 with `content` under `c`.
func (m *mockUpstream) OK(path string, content any) *mockUpstream {
	m.handlers[path] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"h": map[string]any{"c": 0, "e": ""},
			"c": content,
		})
	}
	return m
}

// Status registers a raw HTTP failure so the status -> code -> exit mapping can
// be exercised end to end.
func (m *mockUpstream) Status(path string, status int) *mockUpstream {
	m.handlers[path] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"upstream failure"}`))
	}
	return m
}

// BusinessError registers a 200 response carrying a non-zero business code,
// which Dedao uses for application-level failures.
func (m *mockUpstream) BusinessError(path string, code int, message string) *mockUpstream {
	m.handlers[path] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"h": map[string]any{"c": code, "e": message},
			"c": nil,
		})
	}
	return m
}

// Malformed registers a 200 response whose body is not JSON.
func (m *mockUpstream) Malformed(path string) *mockUpstream {
	m.handlers[path] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("<html>not json</html>"))
	}
	return m
}

// stateDir builds an isolated session directory. Tests must never read or write
// the real ~/.dedao-api.
func stateDir(t *testing.T, authenticated bool) string {
	t.Helper()
	dir := t.TempDir()
	if authenticated {
		cookies := `[{"name":"token","value":"test-session","domain":"127.0.0.1","path":"/","secure":false}]`
		if err := os.WriteFile(filepath.Join(dir, "cookies.json"), []byte(cookies), 0o600); err != nil {
			t.Fatalf("write cookie fixture: %v", err)
		}
	}
	return dir
}

// runCLI drives the real command boundary, which is what FCC coverage requires:
// a test that calls an internal helper proves nothing about the contract.
func runCLI(t *testing.T, mock *mockUpstream, args ...string) result {
	t.Helper()
	previous := baseURLOverride
	if mock != nil {
		baseURLOverride = mock.server.URL
	}
	t.Cleanup(func() { baseURLOverride = previous })

	var stdout, stderr bytes.Buffer
	exit := ExecuteArgs(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
	return result{Stdout: stdout.String(), Stderr: stderr.String(), Exit: exit}
}

// runAuthed is the common case: an isolated authenticated session pointed at a
// mock upstream.
func runAuthed(t *testing.T, mock *mockUpstream, args ...string) result {
	t.Helper()
	return runCLI(t, mock, append(args, "--state-dir", stateDir(t, true), "--compact")...)
}
