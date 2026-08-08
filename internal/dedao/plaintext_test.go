package dedao

import (
	"os"
	"path/filepath"
	"testing"
)

// A credential file left in the clear by an earlier build is an exposure that
// nothing reads any more. Logout has to remove every one of them, not just the
// name the current build happens to write.
func TestLogoutRemovesEveryPlaintextCredentialFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range legacyPlaintextNames {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("secret"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	if found := LegacyPlaintextFiles(dir); len(found) != len(legacyPlaintextNames) {
		t.Fatalf("found %d plaintext files, want %d", len(found), len(legacyPlaintextNames))
	}
	if err := clearCookies(dir); err != nil {
		t.Fatalf("clearCookies: %v", err)
	}
	if found := LegacyPlaintextFiles(dir); len(found) != 0 {
		t.Errorf("logout left %v behind", found)
	}
}

// A clean directory reports nothing, so the doctor check does not cry wolf.
func TestLegacyPlaintextFiles_CleanDirectoryReportsNothing(t *testing.T) {
	if found := LegacyPlaintextFiles(t.TempDir()); len(found) != 0 {
		t.Errorf("a clean state directory reported %v", found)
	}
}

// A directory of the same name is not a credential file.
func TestLegacyPlaintextFiles_IgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, legacyPlaintextNames[0]), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if found := LegacyPlaintextFiles(dir); len(found) != 0 {
		t.Errorf("a directory was reported as a credential file: %v", found)
	}
}
