package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 1x1 PNG stands in for the real QR image.
var fakeQRImage = "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
})

// loginMock wires the three endpoints the QR handshake touches. checkStatus is
// what /check_login reports: 0 pending, 1 scanned, 2 expired.
func loginMock(t *testing.T, checkStatus int) *mockUpstream {
	t.Helper()
	mock := newMockUpstream(t)
	mock.handlers["/"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	mock.handlers["/loginapi/getAccessToken"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("anon-token"))
	}
	mock.handlers["/oauth/api/embedded/qrcode"] = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errCode": 0,
			"data":    map[string]any{"qrCodeString": "qr-string-1", "qrCode": fakeQRImage},
		})
	}
	mock.handlers["/oauth/api/embedded/qrcode/check_login"] = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errCode": 0,
			"data":    map[string]any{"status": checkStatus},
		})
	}
	mock.OK("/api/pc/user/info", map[string]any{"uid_hazy": "u1", "nickname": "someone"})
	return mock
}

// `login` must hand the code to a human and stop. Blocking or polling on the
// user's behalf is exactly what CLI-SPEC §16.3 forbids.
func TestLogin_ReturnsHumanRequiredWithoutBlocking(t *testing.T) {
	dir := stateDir(t, false)
	got := runCLI(t, loginMock(t, 0), "login", "--state-dir", dir, "--compact")

	if code := got.ErrorCode(t); code != "E_HUMAN_REQUIRED" {
		t.Fatalf("code = %s, want E_HUMAN_REQUIRED", code)
	}
	if got.Exit != 9 {
		t.Errorf("exit = %d, want 9", got.Exit)
	}

	envelope := got.Envelope(t)
	errorObject, _ := envelope["error"].(map[string]any)
	if retryable, _ := errorObject["retryable"].(bool); retryable {
		t.Error("E_HUMAN_REQUIRED must not be retryable: a person has to act")
	}
	details, _ := errorObject["details"].(map[string]any)
	if action, _ := details["action"].(string); action != "scan_qr" {
		t.Errorf("details.action = %v, want scan_qr", details["action"])
	}
	resume, _ := details["resume"].(string)
	if !strings.Contains(resume, "login-resume") {
		t.Errorf("details.resume = %q, must name the resume command", resume)
	}

	qrPath, _ := details["qr_path"].(string)
	if qrPath == "" {
		t.Fatal("details.qr_path is required: a bare path-less message is useless to a human")
	}
	if _, err := os.Stat(qrPath); err != nil {
		t.Errorf("QR image was not written to %s: %v", qrPath, err)
	}
}

func TestLogin_HonorsExplicitQRPath(t *testing.T) {
	dir := stateDir(t, false)
	target := filepath.Join(t.TempDir(), "custom-qr.png")
	got := runCLI(t, loginMock(t, 0), "login", "--state-dir", dir, "--qr-path", target, "--compact")

	if code := got.ErrorCode(t); code != "E_HUMAN_REQUIRED" {
		t.Fatalf("code = %s", code)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("--qr-path was ignored: %v", err)
	}
}

// Resuming before the scan repeats the same signal rather than spinning, so the
// agent relays to the human again instead of burning the code.
func TestLoginResume_StillPendingRepeatsHumanRequired(t *testing.T) {
	dir := stateDir(t, false)
	runCLI(t, loginMock(t, 0), "login", "--state-dir", dir, "--compact")

	got := runCLI(t, loginMock(t, 0), "login-resume", "--state-dir", dir, "--compact")
	if code := got.ErrorCode(t); code != "E_HUMAN_REQUIRED" {
		t.Errorf("code = %s, want E_HUMAN_REQUIRED", code)
	}
	if got.Exit != 9 {
		t.Errorf("exit = %d, want 9", got.Exit)
	}
}

func TestLoginResume_ScannedCompletesAndPersistsSession(t *testing.T) {
	dir := stateDir(t, false)
	runCLI(t, loginMock(t, 0), "login", "--state-dir", dir, "--compact")

	got := runCLI(t, loginMock(t, 1), "login-resume", "--state-dir", dir, "--compact")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if loggedIn, _ := data["logged_in"].(bool); !loggedIn {
		t.Error("logged_in = false after a successful scan")
	}
	// The session persists sealed, and the plaintext file an earlier build wrote
	// must not reappear alongside it.
	if _, err := os.Stat(filepath.Join(dir, "session.enc")); err != nil {
		t.Errorf("session was not persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cookies.json")); !os.IsNotExist(err) {
		t.Error("a plaintext cookies.json was written next to the sealed session")
	}
	// The handshake is finished, so the pending record must be gone.
	if _, err := os.Stat(filepath.Join(dir, "login-pending.json")); err == nil {
		t.Error("pending login record survived a completed login")
	}
}

// An expired code is a conflict, not a retryable failure: looping on it can
// never succeed, so the agent must be told to mint a new one.
func TestLoginResume_ExpiredCodeIsConflict(t *testing.T) {
	dir := stateDir(t, false)
	runCLI(t, loginMock(t, 0), "login", "--state-dir", dir, "--compact")

	got := runCLI(t, loginMock(t, 2), "login-resume", "--state-dir", dir, "--compact")
	if code := got.ErrorCode(t); code != "E_CONFLICT" {
		t.Errorf("code = %s, want E_CONFLICT", code)
	}
	if got.Exit != 6 {
		t.Errorf("exit = %d, want 6", got.Exit)
	}
	envelope := got.Envelope(t)
	errorObject, _ := envelope["error"].(map[string]any)
	details, _ := errorObject["details"].(map[string]any)
	if resume, _ := details["resume"].(string); !strings.Contains(resume, "login") {
		t.Error("an expired code must point at a fresh login")
	}
}

func TestLoginResume_WithoutPendingLoginIsConflict(t *testing.T) {
	got := runCLI(t, loginMock(t, 1), "login-resume", "--state-dir", stateDir(t, false), "--compact")
	if code := got.ErrorCode(t); code != "E_CONFLICT" {
		t.Errorf("code = %s, want E_CONFLICT", code)
	}
}

// The QR string and the anonymous OAuth token are credentials for the duration
// of the handshake and must not be echoed to stdout.
func TestLogin_DoesNotLeakHandshakeSecrets(t *testing.T) {
	got := runCLI(t, loginMock(t, 0), "login", "--state-dir", stateDir(t, false), "--compact")
	for _, secret := range []string{"anon-token", "qr-string-1"} {
		if strings.Contains(got.Stdout, secret) {
			t.Errorf("login leaked %q on stdout:\n%s", secret, got.Stdout)
		}
	}
}
