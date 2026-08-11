package cmd

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/dedao-cli/internal/secret"
)

// The person signing in should experience one login, not two. These tests pin
// that: both authorizations start together, both settle together, and neither
// half can strand the other.

var devicePendingBody = map[string]any{
	// The server reports "still waiting" inside a successful envelope rather
	// than as an error. Getting that wrong would read as a finished login.
	"success": true,
	"data":    map[string]any{"msg": "authorization_pending"},
}

func withDeviceFlow(t *testing.T, mock *mockUpstream, tokenBody map[string]any) *mockUpstream {
	t.Helper()
	mock.handlers["/open/api/v1/oauth/device/code"] = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"code":                    "device-code-secret",
				"user_code":               "F6BA-BX84",
				"verification_uri":        "https://biji.com/openapi/oauth/authorize?code=device-code-secret",
				"verification_uri_qrcode": fakeQRImage,
				"expires_in":              600,
				"interval":                5,
			},
		})
	}
	mock.handlers["/open/api/v1/oauth/token"] = func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(tokenBody)
	}
	return mock
}

func errorDetails(t *testing.T, got result) map[string]any {
	t.Helper()
	envelope := got.Envelope(t)
	errorObject, _ := envelope["error"].(map[string]any)
	details, _ := errorObject["details"].(map[string]any)
	return details
}

func TestLogin_StartsTheNoteAuthorizationInTheSamePass(t *testing.T) {
	dir := stateDir(t, false)
	mock := withDeviceFlow(t, loginMock(t, 0), devicePendingBody)
	got := runCLI(t, mock, "login", "--state-dir", dir, "--compact")

	if code := got.ErrorCode(t); code != "E_HUMAN_REQUIRED" {
		t.Fatalf("code = %s, want E_HUMAN_REQUIRED", code)
	}
	getnote, _ := errorDetails(t, got)["getnote"].(map[string]any)
	if getnote == nil {
		t.Fatal("login did not start the note authorization")
	}
	if action, _ := getnote["action"].(string); action != "authorize_getnote" {
		t.Errorf("getnote.action = %q, want authorize_getnote", action)
	}
	if uri, _ := getnote["verification_uri"].(string); !strings.Contains(uri, "authorize") {
		t.Errorf("verification_uri = %q", uri)
	}
	if code, _ := getnote["user_code"].(string); code == "" {
		t.Error("the user code a person must confirm was not reported")
	}

	// The device code is carried inside the verification link by design -- that
	// link is what the person opens. What must not happen is echoing it as a
	// field of its own, which would invite callers to log or pass it around
	// separately from the one URL a human is meant to act on.
	for key, value := range getnote {
		if text, _ := value.(string); text == "device-code-secret" {
			t.Errorf("the device code is exposed as its own field %q", key)
		}
	}
}

func TestLogin_SkipGetnoteSignsInForContentOnly(t *testing.T) {
	dir := stateDir(t, false)
	mock := withDeviceFlow(t, loginMock(t, 0), devicePendingBody)
	got := runCLI(t, mock, "login", "--skip-getnote", "--state-dir", dir, "--compact")

	if _, exists := errorDetails(t, got)["getnote"]; exists {
		t.Error("--skip-getnote still started a note authorization")
	}
}

func TestLoginResume_CompletesBothHalvesInOneCall(t *testing.T) {
	dir := stateDir(t, false)
	authorized := map[string]any{"success": true, "data": map[string]any{
		"api_key": "gk_live_minted", "client_id": "cli_minted", "expires_at": 1893456000,
	}}
	mock := withDeviceFlow(t, loginMock(t, 1), authorized)

	if start := runCLI(t, mock, "login", "--state-dir", dir, "--compact"); start.Exit != 9 {
		t.Fatalf("login exit = %d, want 9: %s", start.Exit, start.Stdout)
	}
	got := runCLI(t, mock, "login-resume", "--state-dir", dir, "--compact")
	if got.Exit != 0 {
		t.Fatalf("login-resume exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if loggedIn, _ := data["logged_in"].(bool); !loggedIn {
		t.Error("logged_in = false")
	}
	getnote, _ := data["getnote"].(map[string]any)
	if authorized, _ := getnote["authorized"].(bool); !authorized {
		t.Fatalf("getnote.authorized = false: %#v", getnote)
	}

	// The minted credentials must land in the encrypted store, so the very next
	// note command works without another human step.
	store := secret.New(filepath.Join(dir, "getnote"))
	raw, err := store.Load(getnoteAPIKeySecret)
	if err != nil || string(raw) != "gk_live_minted" {
		t.Errorf("stored API key = %q, err = %v", raw, err)
	}
}

func TestLoginResume_NoteHalfStillPendingKeepsAskingForTheHuman(t *testing.T) {
	dir := stateDir(t, false)
	mock := withDeviceFlow(t, loginMock(t, 1), devicePendingBody)

	runCLI(t, mock, "login", "--state-dir", dir, "--compact")
	got := runCLI(t, mock, "login-resume", "--state-dir", dir, "--compact")

	if code := got.ErrorCode(t); code != "E_HUMAN_REQUIRED" {
		t.Fatalf("code = %s, want E_HUMAN_REQUIRED", code)
	}
	getnote, _ := errorDetails(t, got)["getnote"].(map[string]any)
	if pending, _ := getnote["pending"].(bool); !pending {
		t.Errorf("getnote.pending = false while the authorization was outstanding: %#v", getnote)
	}
}

// A note authorization that can never complete must not hold the session
// hostage: the content half is already signed in and stays that way.
func TestLoginResume_ExpiredNoteAuthorizationDoesNotStrandTheLogin(t *testing.T) {
	dir := stateDir(t, false)
	expired := map[string]any{"success": true, "data": map[string]any{"msg": "expired_token"}}
	mock := withDeviceFlow(t, loginMock(t, 1), expired)

	runCLI(t, mock, "login", "--state-dir", dir, "--compact")
	got := runCLI(t, mock, "login-resume", "--state-dir", dir, "--compact")

	if got.Exit != 0 {
		t.Fatalf("login-resume exit = %d, want 0: %s", got.Exit, got.Stdout)
	}
	getnote, _ := got.Data(t)["getnote"].(map[string]any)
	if authorized, _ := getnote["authorized"].(bool); authorized {
		t.Error("an expired authorization was reported as authorized")
	}
	if pending, _ := getnote["pending"].(bool); pending {
		t.Error("an expired authorization is still reported as pending")
	}
}

// Someone already signed in for content who only needs note access must not be
// blocked by the QR half -- minting a code they will not scan can fail outright,
// and then the half they actually wanted never starts.
func TestLogin_AlreadySignedInGoesStraightToTheNoteAuthorization(t *testing.T) {
	dir := stateDir(t, true)
	mock := withDeviceFlow(t, newMockUpstream(t), devicePendingBody)
	// No QR endpoints are registered: reaching for one would fail the command.
	got := runCLI(t, mock, "login", "--state-dir", dir, "--compact")

	if code := got.ErrorCode(t); code != "E_HUMAN_REQUIRED" {
		t.Fatalf("code = %s, want E_HUMAN_REQUIRED: %s", code, got.Stdout)
	}
	details := errorDetails(t, got)
	if _, minted := details["qr_path"]; minted {
		t.Error("login minted a Dedao QR for an already-signed-in account")
	}
	getnote, _ := details["getnote"].(map[string]any)
	if action, _ := getnote["action"].(string); action != "authorize_getnote" {
		t.Errorf("getnote.action = %q, want authorize_getnote", action)
	}
}

// The follow-up call matters as much as the first: an already-signed-in account
// has no QR outstanding, so resume must settle the note half instead of
// reporting that there is no pending login.
func TestLoginResume_AlreadySignedInSettlesTheNoteHalf(t *testing.T) {
	dir := stateDir(t, true)
	authorized := map[string]any{"success": true, "data": map[string]any{
		"api_key": "gk_live_minted", "client_id": "cli_minted",
	}}
	mock := withDeviceFlow(t, newMockUpstream(t), authorized)

	if start := runCLI(t, mock, "login", "--state-dir", dir, "--compact"); start.Exit != 9 {
		t.Fatalf("login exit = %d, want 9: %s", start.Exit, start.Stdout)
	}
	got := runCLI(t, mock, "login-resume", "--state-dir", dir, "--compact")
	if got.Exit != 0 {
		t.Fatalf("login-resume exit = %d, want 0: %s", got.Exit, got.Stdout)
	}
	getnote, _ := got.Data(t)["getnote"].(map[string]any)
	if authorized, _ := getnote["authorized"].(bool); !authorized {
		t.Fatalf("getnote.authorized = false: %#v", getnote)
	}
	store := secret.New(filepath.Join(dir, "getnote"))
	if raw, err := store.Load(getnoteAPIKeySecret); err != nil || string(raw) != "gk_live_minted" {
		t.Errorf("stored API key = %q, err = %v", raw, err)
	}
}

// Still waiting on the person, with the session already established.
func TestLoginResume_AlreadySignedInKeepsAskingWhileNotePending(t *testing.T) {
	dir := stateDir(t, true)
	mock := withDeviceFlow(t, newMockUpstream(t), devicePendingBody)

	runCLI(t, mock, "login", "--state-dir", dir, "--compact")
	got := runCLI(t, mock, "login-resume", "--state-dir", dir, "--compact")

	if code := got.ErrorCode(t); code != "E_HUMAN_REQUIRED" {
		t.Fatalf("code = %s, want E_HUMAN_REQUIRED: %s", code, got.Stdout)
	}
}

// With both halves already in place there is nothing for a human to do.
func TestLogin_FullyConfiguredReportsNothingToDo(t *testing.T) {
	dir := stateDir(t, true)
	seedGetnoteCredentials(t, dir)
	got := runCLI(t, newMockUpstream(t), "login", "--state-dir", dir, "--compact")

	if got.Exit != 0 {
		t.Fatalf("exit = %d, want 0: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if already, _ := data["already_signed_in"].(bool); !already {
		t.Error("already_signed_in = false for a fully configured account")
	}
	getnote, _ := data["getnote"].(map[string]any)
	if authorized, _ := getnote["authorized"].(bool); !authorized {
		t.Errorf("getnote.authorized = false though credentials were stored: %#v", getnote)
	}
}

// A note authorization that cannot even start must not cost the person the
// login they came for.
func TestLogin_UnavailableNoteAuthorizationStillSignsInForContent(t *testing.T) {
	dir := stateDir(t, false)
	mock := loginMock(t, 1) // no device endpoints at all
	if start := runCLI(t, mock, "login", "--state-dir", dir, "--compact"); start.Exit != 9 {
		t.Fatalf("login exit = %d, want 9", start.Exit)
	}
	got := runCLI(t, mock, "login-resume", "--state-dir", dir, "--compact")
	if got.Exit != 0 {
		t.Fatalf("login-resume exit = %d, want 0: %s", got.Exit, got.Stdout)
	}
	if loggedIn, _ := got.Data(t)["logged_in"].(bool); !loggedIn {
		t.Error("logged_in = false after the note half was unavailable")
	}
}
