package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/dedao-cli/internal/secret"
)

func TestLogout_ConfirmTokenRejectsChangedCredentials(t *testing.T) {
	dir := stateDir(t, true)
	mock := newMockUpstream(t)
	preview := runCLI(t, mock, "logout", "--dry-run", "--state-dir", dir, "--compact")
	if preview.Exit != 0 {
		t.Fatalf("dry-run exit = %d: %s", preview.Exit, preview.Stdout)
	}
	token, _ := preview.Data(t)["confirm_token"].(string)

	changed := `[{"name":"token","value":"changed-session","domain":"127.0.0.1","path":"/","secure":false}]`
	if err := secret.New(dir).Save("session", []byte(changed)); err != nil {
		t.Fatalf("replace credential fixture: %v", err)
	}

	confirmed := runCLI(t, mock, "logout", "--confirm", token, "--state-dir", dir, "--compact")
	if confirmed.Exit != 6 || confirmed.ErrorCode(t) != "E_CONFLICT" {
		t.Fatalf("changed credential confirm = exit %d/code %s, want 6/E_CONFLICT",
			confirmed.Exit, confirmed.ErrorCode(t))
	}
	raw, err := secret.New(dir).Load("session")
	if err != nil || !strings.Contains(string(raw), "changed-session") {
		t.Fatalf("changed credential was deleted: raw=%q err=%v", raw, err)
	}
}

func TestLogout_ConfirmTokenRejectsChangedLegacyPlaintext(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		prepare func(*testing.T, string)
	}{
		{name: "added after dry-run"},
		{
			name: "replaced after dry-run",
			prepare: func(t *testing.T, dir string) {
				if got := runCLI(t, nil, "status", "--state-dir", dir, "--compact"); got.Exit != 0 {
					t.Fatalf("migrate session exit = %d: %s", got.Exit, got.Stdout)
				}
				if err := os.WriteFile(filepath.Join(dir, "cookies.json"), []byte("old-legacy-session"), 0o600); err != nil {
					t.Fatalf("seed legacy credential: %v", err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := stateDir(t, true)
			if testCase.prepare != nil {
				testCase.prepare(t, dir)
			}
			preview := runCLI(t, nil, "logout", "--dry-run", "--state-dir", dir, "--compact")
			if preview.Exit != 0 {
				t.Fatalf("dry-run exit = %d: %s", preview.Exit, preview.Stdout)
			}
			token, _ := preview.Data(t)["confirm_token"].(string)

			legacyPath := filepath.Join(dir, "cookies.json")
			changed := []byte("new-legacy-session")
			if err := os.WriteFile(legacyPath, changed, 0o600); err != nil {
				t.Fatalf("change legacy credential: %v", err)
			}

			confirmed := runCLI(t, nil, "logout", "--confirm", token, "--state-dir", dir, "--compact")
			if confirmed.Exit != 6 || confirmed.ErrorCode(t) != "E_CONFLICT" {
				t.Fatalf("changed legacy credential confirm = exit %d/code %s, want 6/E_CONFLICT",
					confirmed.Exit, confirmed.ErrorCode(t))
			}
			raw, err := os.ReadFile(legacyPath)
			if err != nil || string(raw) != string(changed) {
				t.Fatalf("changed legacy credential was deleted: raw=%q err=%v", raw, err)
			}
		})
	}
}
