package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConfig(t *testing.T, version string) Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TEST_UPDATE_CACHE", dir)
	return Config{
		Tool:       "dedao-cli",
		Repo:       "owner/dedao-cli",
		NPMPackage: "@fateforge/dedao-cli",
		Version:    version,
		CacheEnv:   "TEST_UPDATE_CACHE",
	}
}

// Misclassifying the install method is the failure that does real damage:
// overwriting a file npm owns desyncs the manager, and printing an npm command
// for a standalone binary does nothing. Anything unrecognizable must refuse.
func TestDetectInstallMethod(t *testing.T) {
	for path, want := range map[string]InstallMethod{
		"/usr/lib/node_modules/@fateforge/dedao-cli/bin/dedao-cli": MethodNPM,
		`C:\Users\x\AppData\Roaming\npm\dedao-cli.exe`:             MethodNPM,
		"/home/x/go/bin/dedao-cli":                                 MethodBinary,
		"/usr/local/bin/dedao-cli":                                 MethodBinary,
		"":                                                         MethodUnknown,
	} {
		if got := DetectInstallMethod(path); got != want {
			t.Errorf("DetectInstallMethod(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestNewerThan(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.9", "1.0.0", false},
		{"v1.0.1", "1.0.0", true},
		{"1.0.1-rc1", "1.0.0", true},
	}
	for _, testCase := range cases {
		if got := NewerThan(testCase.candidate, testCase.current); got != testCase.want {
			t.Errorf("NewerThan(%q,%q) = %v", testCase.candidate, testCase.current, got)
		}
	}
}

// Severity decides whether an agent treats an update as routine or urgent, so
// the grading rules are worth pinning exactly.
func TestGradeSeverity(t *testing.T) {
	changelog := `# Changelog

## [1.2.0] - 2026-02-01

### Added

- something routine

## [1.1.0] - 2026-01-15

### Security

- fixed a credential leak

## [1.0.0] - 2026-01-01

### Added

- first release
`
	if got := GradeSeverity(changelog, "1.0.0", "1.2.0"); got != "warning" {
		t.Errorf("a delta containing a Security entry graded %q, want warning", got)
	}
	if got := GradeSeverity(changelog, "1.1.0", "1.2.0"); got != "info" {
		t.Errorf("a delta with no Security entry graded %q, want info", got)
	}
	if got := GradeSeverity(changelog, "1.9.0", "2.0.0"); got != "warning" {
		t.Errorf("a major bump graded %q, want warning", got)
	}
}

// A notice for the version already installed is stale by definition and must be
// suppressed, or every command would keep advertising an update that landed.
func TestReadCachedNotice_SuppressesStaleAdvisory(t *testing.T) {
	config := testConfig(t, "1.0.0")
	path := filepath.Join(os.Getenv("TEST_UPDATE_CACHE"), "update-notice.json")
	encoded, _ := json.Marshal(Notice{Type: "update_available", LatestVersion: "1.0.0"})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if notice := config.ReadCachedNotice(); notice != nil {
		t.Errorf("a notice for the running version was surfaced: %+v", notice)
	}

	encoded, _ = json.Marshal(Notice{Type: "update_available", LatestVersion: "1.1.0"})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if notice := config.ReadCachedNotice(); notice == nil {
		t.Error("a genuine newer-version notice was not surfaced")
	}
}

func TestReadCachedNotice_MissingCacheIsSilent(t *testing.T) {
	if notice := testConfig(t, "1.0.0").ReadCachedNotice(); notice != nil {
		t.Errorf("an empty cache produced a notice: %+v", notice)
	}
}

// The idempotent no-op must decide before any package manager is invoked, so an
// already-current install never shells out to npm.
func TestRun_AlreadyCurrentNeverDrivesTheManager(t *testing.T) {
	config := testConfig(t, "1.2.0")
	var invoked []string
	result, err := config.run(context.Background(), nil, "1.2.0", false,
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			invoked = append(invoked, name+" "+strings.Join(args, " "))
			return nil, nil
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(invoked) != 0 {
		t.Errorf("an already-current install ran %v", invoked)
	}
	if result.Status != "current" {
		t.Errorf("status = %q, want current", result.Status)
	}
	if result.UpdateAvailable {
		t.Error("update_available = true while already on the target")
	}
}

// A dry run previews and changes nothing.
func TestRun_DryRunDrivesNothing(t *testing.T) {
	config := testConfig(t, "1.0.0")
	var invoked []string
	result, err := config.run(context.Background(), nil, "1.1.0", true,
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			invoked = append(invoked, name)
			return nil, nil
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(invoked) != 0 {
		t.Errorf("a dry run executed %v", invoked)
	}
	if result.Status != "dry_run" || result.BinaryReplaced {
		t.Errorf("dry run reported %+v", result)
	}
	if result.SkillSyncCommand == "" {
		t.Error("a dry run must still name the skill sync command")
	}
}

// Skill sync failing after the binary moved is partial success, and the agent
// has to be told not to rely on new behavior yet.
func TestRun_SkillSyncFailureIsPartial(t *testing.T) {
	config := testConfig(t, "1.0.0")
	config.Method = MethodNPM
	result, err := config.run(context.Background(), nil, "1.1.0", false,
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "npx" {
				return []byte("boom"), errors.New("sync failed")
			}
			return nil, nil
		})
	if err == nil {
		t.Fatal("a failed skill sync returned success")
	}
	if result.Status != "partial" || !result.BinaryReplaced {
		t.Errorf("result = %+v, want partial with binary_replaced", result)
	}
	if result.SkillSyncStatus != "failed" || result.SkillSyncCommand == "" {
		t.Errorf("the agent was not told how to finish the sync: %+v", result)
	}
}
