package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/dedao-cli/internal/update"
)

// `update --check` is a read-only probe. With no network it must still answer
// in the machine contract rather than hanging or panicking.
func TestUpdateCheck_ReportsInstallMethodAndContract(t *testing.T) {
	got := runCLI(t, nil, "update", "--check", "--timeout", "2s", "--compact")
	// The release feed may be unreachable, time out, or rate-limit an
	// unauthenticated CI runner; each outcome must honor the machine contract.
	if got.Exit == 0 {
		data := got.Data(t)
		for _, field := range []string{
			"current_version", "install_method", "update_available", "skill_sync_supported",
		} {
			if _, ok := data[field]; !ok {
				t.Errorf("update --check omitted %q", field)
			}
		}
		return
	}
	switch code := got.ErrorCode(t); code {
	case "E_NETWORK", "E_RATE_LIMITED":
		if got.Exit != 7 {
			t.Errorf("exit = %d, want 7 for %s", got.Exit, code)
		}
	case "E_TIMEOUT":
		if got.Exit != 8 {
			t.Errorf("exit = %d, want 8 for E_TIMEOUT", got.Exit)
		}
	default:
		t.Errorf("code = %s, want E_NETWORK, E_RATE_LIMITED, or E_TIMEOUT", code)
	}
}

// `update` takes no confirm token and has no leaf subcommands (CLI-SPEC §14).
func TestUpdate_HasNoConfirmGateOrSubcommands(t *testing.T) {
	got := runCLI(t, nil, "update", "nonsense", "--compact")
	if code := got.ErrorCode(t); code != "E_USAGE" {
		t.Errorf("code = %s, want E_USAGE for a leaf argument", code)
	}

	reference := runCLI(t, nil, "reference", "--compact").Data(t)
	commands, _ := reference["commands"].([]any)
	for _, raw := range commands {
		entry, _ := raw.(map[string]any)
		if name, _ := entry["name"].(string); name != "update" {
			continue
		}
		if children, _ := entry["children"].([]any); len(children) > 0 {
			t.Errorf("update declares %d subcommands; it must be a single command", len(children))
		}
		for _, param := range entry["params"].([]any) {
			field, _ := param.(map[string]any)
			if name, _ := field["name"].(string); name == "--confirm" {
				t.Error("update declares a confirm token; self-update is exempt from the write gate")
			}
		}
		return
	}
	t.Error("update is not declared in reference")
}

// A dry run must change nothing and must still name the skill-sync command.
func TestUpdate_DryRunIsReadOnly(t *testing.T) {
	got := runCLI(t, nil, "update", "--dry-run", "--target-version", "9.9.9", "--compact")
	if got.Exit != 0 {
		// Offline resolution is fine; the target was supplied so it should not
		// have needed the network at all.
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if status, _ := data["status"].(string); status != "dry_run" {
		t.Errorf("status = %q, want dry_run", status)
	}
	if replaced, _ := data["binary_replaced"].(bool); replaced {
		t.Error("a dry run reported the binary was replaced")
	}
	if command, _ := data["skill_sync_command"].(string); !strings.Contains(command, "npx skills add") {
		t.Errorf("skill_sync_command = %q", command)
	}
}

// Asking for the version already installed is a no-op, and must not shell out.
func TestUpdate_AlreadyCurrentIsANoOp(t *testing.T) {
	got := runCLI(t, nil, "update", "--target-version", version, "--compact")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if status, _ := data["status"].(string); status != "current" {
		t.Errorf("status = %q, want current", status)
	}
	if available, _ := data["update_available"].(bool); available {
		t.Error("update_available = true while on the target version")
	}
}

func TestUpdate_CancelledContextEmitsTerminalState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	exit := ExecuteArgs(ctx, []string{"update", "--target-version", "9.9.9", "--compact"},
		strings.NewReader(""), &stdout, &stderr)
	got := result{Stdout: stdout.String(), Stderr: stderr.String(), Exit: exit}
	if code := got.ErrorCode(t); code != "E_INTERRUPTED" {
		t.Fatalf("code = %q, want E_INTERRUPTED; stdout: %s", code, got.Stdout)
	}
	if got.Exit != 130 {
		t.Errorf("exit = %d, want 130", got.Exit)
	}
	errorObject, _ := got.Envelope(t)["error"].(map[string]any)
	details, _ := errorObject["details"].(map[string]any)
	for _, field := range []string{"stage", "current_version", "binary_replaced", "skill_sync_status"} {
		if _, ok := details[field]; !ok {
			t.Errorf("interruption details omitted %q", field)
		}
	}
	if stage, _ := details["stage"].(string); stage != "discover" {
		t.Errorf("stage = %q, want discover", stage)
	}
}

func TestUpdate_TimeoutAfterBinarySwapExplainsPartialRecovery(t *testing.T) {
	err := asCLIError(updateFailure(&update.Result{
		Stage:            "skill_sync",
		PreviousVersion:  "1.0.0",
		CurrentVersion:   "1.1.0",
		BinaryReplaced:   true,
		SkillSyncStatus:  "failed",
		SkillSyncCommand: "npx skills add owner/repo -y -g",
	}, context.DeadlineExceeded))
	if err.Code != "E_TIMEOUT" {
		t.Fatalf("code = %s, want E_TIMEOUT", err.Code)
	}
	for _, want := range []string{"1.1.0", "npx skills add owner/repo", "changelog --since 1.0.0"} {
		if !strings.Contains(err.Message, want) {
			t.Errorf("message %q does not contain %q", err.Message, want)
		}
	}
}

// Business commands must never phone home; the notice comes from the cache or
// not at all.
func TestCommands_DoNotAttachNoticesWithoutACache(t *testing.T) {
	got := runCLI(t, nil, "reference", "--compact")
	envelope := got.Envelope(t)
	meta, _ := envelope["meta"].(map[string]any)
	if _, present := meta["notices"]; present {
		t.Error("meta.notices was emitted with an empty cache; it must be omitted")
	}
}
