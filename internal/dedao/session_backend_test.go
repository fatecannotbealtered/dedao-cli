package dedao

import (
	"os"
	"path/filepath"
	"testing"
)

// The backend `context` and `doctor` report must be the one recorded in the
// stored session, not a live probe: after a keyring-to-file degradation (or
// the reverse) the session on disk is the truth, and probing where a new
// secret would go misreports where this one is.
func TestSessionBackendPrefersTheStoredSessionBackend(t *testing.T) {
	t.Setenv("DEDAO_SECRET_BACKEND", "file")
	dir := t.TempDir()
	// Only the plaintext envelope header is read; no decryption happens here.
	sealed := `{"v":1,"backend":"keyring","salt":"","nonce":"","data":""}`
	if err := os.WriteFile(filepath.Join(dir, "session.enc"), []byte(sealed), 0o600); err != nil {
		t.Fatalf("seed session.enc: %v", err)
	}
	if got := SessionBackend(dir); got != "keyring" {
		t.Errorf("SessionBackend = %q, want the stored backend %q", got, "keyring")
	}
}

// With nothing stored there is no recorded backend to prefer, so the live
// probe answers where a login would put the session.
func TestSessionBackendFallsBackToTheLiveProbeWhenNothingIsStored(t *testing.T) {
	t.Setenv("DEDAO_SECRET_BACKEND", "file")
	if got := SessionBackend(t.TempDir()); got != "encrypted-file" {
		t.Errorf("SessionBackend = %q, want %q", got, "encrypted-file")
	}
}
