package cmd

import "testing"

func TestLimitRejectsUnsupportedCommands(t *testing.T) {
	got := runCLI(t, nil, "status", "--limit", "1", "--compact")
	if got.Exit != 2 || got.ErrorCode(t) != "E_VALIDATION" {
		t.Fatalf("exit/code = %d/%s, want 2/E_VALIDATION", got.Exit, got.ErrorCode(t))
	}
}

func TestLimitMustBePositive(t *testing.T) {
	got := runCLI(t, nil, "library", "course", "--limit", "0", "--compact")
	if got.Exit != 2 || got.ErrorCode(t) != "E_VALIDATION" {
		t.Fatalf("exit/code = %d/%s, want 2/E_VALIDATION", got.Exit, got.ErrorCode(t))
	}
}
