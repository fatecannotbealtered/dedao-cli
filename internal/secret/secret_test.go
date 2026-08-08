package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `[{"name":"SESSION","value":"super-secret-cookie-value"}]`

func TestSaveLoad_RoundTrips(t *testing.T) {
	store := New(t.TempDir())
	if err := store.Save("session", []byte(sample)); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.Load("session")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(got) != sample {
		t.Errorf("round-tripped to %q", got)
	}
}

// The whole point: the secret must not be readable on disk. A test that only
// checked the round-trip would pass just as well with plaintext storage.
func TestSaved_SecretIsNotOnDiskInTheClear(t *testing.T) {
	dir := t.TempDir()
	if err := New(dir).Save("session", []byte(sample)); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was written")
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if strings.Contains(string(raw), "super-secret-cookie-value") {
			t.Errorf("%s carries the secret in the clear", entry.Name())
		}
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("a temp copy survived: %s", entry.Name())
		}
	}
}

// Every save uses a fresh nonce and salt, so the same plaintext must not
// produce the same ciphertext -- otherwise an observer could tell that a
// session was unchanged, or replay an old file over a new one undetected.
func TestSave_IsNotDeterministic(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := store.Save("session", []byte(sample)); err != nil {
		t.Fatalf("save: %v", err)
	}
	first, _ := os.ReadFile(store.path("session"))
	if err := store.Save("session", []byte(sample)); err != nil {
		t.Fatalf("save again: %v", err)
	}
	second, _ := os.ReadFile(store.path("session"))
	if string(first) == string(second) {
		t.Error("two saves of the same value produced identical files")
	}
}

// A tampered file must fail closed. GCM makes this automatic, but the failure
// has to reach the caller rather than yielding garbage.
func TestLoad_RejectsTamperedCiphertext(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := store.Save("session", []byte(sample)); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(store.path("session"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Flip a character inside the base64 payload.
	tampered := strings.Replace(string(raw), `"data":"`, `"data":"A`, 1)
	if err := os.WriteFile(store.path("session"), []byte(tampered), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.Load("session"); err == nil {
		t.Error("a tampered secret was accepted")
	}
}

// The file backend binds to the machine, so a state directory copied elsewhere
// must not open. Simulated by sealing with one salt and opening with another.
func TestFileBackend_KeyIsSaltBound(t *testing.T) {
	first, err := machineKey([]byte("salt-one-0123456"))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	second, err := machineKey([]byte("salt-two-0123456"))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if string(first) == string(second) {
		t.Error("the derived key ignores the salt")
	}
	if len(first) != 32 {
		t.Errorf("key length = %d, want 32", len(first))
	}
}

func TestLoad_MissingSecretIsNotFound(t *testing.T) {
	if _, err := New(t.TempDir()).Load("session"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDelete_RemovesTheSecret(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := store.Save("session", []byte(sample)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !store.Has("session") {
		t.Fatal("Has = false after save")
	}
	if err := store.Delete("session"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if store.Has("session") {
		t.Error("Has = true after delete")
	}
	if _, err := store.Load("session"); err != ErrNotFound {
		t.Errorf("load after delete: %v", err)
	}
}

// Deleting something that was never stored is what `logout` does on a fresh
// machine; it must not be an error.
func TestDelete_IsIdempotent(t *testing.T) {
	if err := New(t.TempDir()).Delete("session"); err != nil {
		t.Errorf("delete on empty store: %v", err)
	}
}

// Adoption is what makes the migration real: the legacy plaintext has to be
// sealed and then removed, or the encryption is decorative.
func TestAdoptPlaintext_SealsAndRemovesTheOriginal(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "cookies.json")
	if err := os.WriteFile(legacy, []byte(sample), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := New(dir)

	got, ok := store.AdoptPlaintext("session", legacy)
	if !ok || string(got) != sample {
		t.Fatalf("adopt returned ok=%v data=%q", ok, got)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("the plaintext original was left in place")
	}
	reopened, err := store.Load("session")
	if err != nil || string(reopened) != sample {
		t.Errorf("adopted secret did not reload: %v / %q", err, reopened)
	}
}

func TestAdoptPlaintext_NothingToAdopt(t *testing.T) {
	dir := t.TempDir()
	if _, ok := New(dir).AdoptPlaintext("session", filepath.Join(dir, "absent.json")); ok {
		t.Error("adopt reported success with no legacy file")
	}
}

// The backend has to be reportable, because SEC-SPEC §4 requires the fallback
// to be visible rather than silent.
func TestBackend_IsReported(t *testing.T) {
	backend := New(t.TempDir()).Backend()
	if backend != BackendKeyring && backend != BackendFile {
		t.Errorf("backend = %q, want %q or %q", backend, BackendKeyring, BackendFile)
	}
	// TestMain pins the suite to the file backend, so that is what must show.
	if backend != BackendFile {
		t.Errorf("backend = %q despite %s=file", backend, backendEnv)
	}
}

// Two state directories must not share a keyring entry, or logging out of one
// would silently break the other.
func TestKeyringUser_IsNamespacedByStateDir(t *testing.T) {
	first := New(filepath.Join(t.TempDir(), "a")).keyringUser("session")
	second := New(filepath.Join(t.TempDir(), "b")).keyringUser("session")
	if first == second {
		t.Error("two state directories resolved to the same keyring entry")
	}
	if !strings.HasPrefix(first, "session-") {
		t.Errorf("keyring user %q does not name the secret", first)
	}
}
