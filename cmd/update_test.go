package cmd

import (
	"strings"
	"testing"
)

// `update --check` is a read-only probe. With no network it must still answer
// in the machine contract rather than hanging or panicking.
func TestUpdateCheck_ReportsInstallMethodAndContract(t *testing.T) {
	got := runCLI(t, nil, "update", "--check", "--timeout", "2s", "--compact")
	// Offline, the release feed is unreachable and that is an honest E_NETWORK.
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
	if code := got.ErrorCode(t); code != "E_NETWORK" {
		t.Errorf("code = %s, want E_NETWORK when the release feed is unreachable", code)
	}
	if got.Exit != 7 {
		t.Errorf("exit = %d, want 7", got.Exit)
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
