package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/fatecannotbealtered/dedao-cli/internal/contract"
)

func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

// canonicalContract is contract/contract.json, the fleet-wide single source of
// truth. Asserting live output against it (rather than against constants in
// this repo) is what makes a tool unable to silently fork the shared contract.
func canonicalContract(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(), "contract", "contract.json"))
	if err != nil {
		t.Fatalf("read contract.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse contract.json: %v", err)
	}
	return parsed
}

func TestContract_SuccessEnvelopeKeysMatchCanonical(t *testing.T) {
	canonical := canonicalContract(t)
	envelopeSpec, _ := canonical["envelope"].(map[string]any)
	expected := stringsFrom(envelopeSpec["success_keys"])

	mock := newMockUpstream(t)
	mock.OK("/pc/sunflower/v1/resource/list", map[string]any{"list": []any{}})
	got := runAuthed(t, mock, "free").Envelope(t)

	assertKeySet(t, "success envelope", got, expected)
}

func TestContract_ErrorEnvelopeKeysMatchCanonical(t *testing.T) {
	canonical := canonicalContract(t)
	envelopeSpec, _ := canonical["envelope"].(map[string]any)

	mock := newMockUpstream(t)
	mock.Status("/pc/sunflower/v1/resource/list", 500)
	got := runAuthed(t, mock, "free").Envelope(t)

	assertKeySet(t, "error envelope", got, stringsFrom(envelopeSpec["error_keys"]))
	errorObject, _ := got["error"].(map[string]any)
	assertKeySet(t, "error object", errorObject, stringsFrom(envelopeSpec["error_object_keys"]))
}

// meta may only carry the required key plus documented optional ones. An extra
// key here is drift that would silently break agents parsing meta strictly.
func TestContract_MetaKeysAreBounded(t *testing.T) {
	canonical := canonicalContract(t)
	envelopeSpec, _ := canonical["envelope"].(map[string]any)
	allowed := map[string]bool{}
	for _, key := range append(stringsFrom(envelopeSpec["meta_required_keys"]), stringsFrom(envelopeSpec["meta_optional_keys"])...) {
		allowed[key] = true
	}

	mock := newMockUpstream(t)
	mock.OK("/pc/sunflower/v1/resource/list", map[string]any{"list": []any{}})
	envelope := runAuthed(t, mock, "free").Envelope(t)
	meta, _ := envelope["meta"].(map[string]any)

	if _, ok := meta["duration_ms"]; !ok {
		t.Error("meta.duration_ms is required on every response")
	}
	for key := range meta {
		if !allowed[key] {
			t.Errorf("meta carries undocumented key %q", key)
		}
	}
}

func TestContract_SchemaVersionMatchesCanonical(t *testing.T) {
	canonical := canonicalContract(t)
	envelopeSpec, _ := canonical["envelope"].(map[string]any)
	want, _ := envelopeSpec["schema_version_value"].(string)
	if contract.SchemaVersion != want {
		t.Errorf("SchemaVersion = %q, want %q", contract.SchemaVersion, want)
	}
}

// Every core code must keep exactly the canonical exit and retryability. This
// is what makes exit-code handling portable across the whole fleet.
func TestContract_CoreErrorCodesMatchCanonical(t *testing.T) {
	canonical := canonicalContract(t)
	codesSpec, _ := canonical["error_codes"].(map[string]any)
	core, _ := codesSpec["core"].(map[string]any)

	for name, raw := range core {
		spec, _ := raw.(map[string]any)
		wantExit := int(spec["exit"].(float64))
		wantRetryable, _ := spec["retryable"].(bool)

		got, ok := contract.Codes[name]
		if !ok {
			t.Errorf("core code %s is missing from the generated table", name)
			continue
		}
		if got.Exit != wantExit {
			t.Errorf("%s exit = %d, want %d", name, got.Exit, wantExit)
		}
		if got.Retryable != wantRetryable {
			t.Errorf("%s retryable = %v, want %v", name, got.Retryable, wantRetryable)
		}
	}
}

// Extension codes are allowed, but must obey the extension rules: E_* named,
// never redefining a core code, and exit 9 reserved for human-action codes.
func TestContract_ExtensionCodesObeyRules(t *testing.T) {
	canonical := canonicalContract(t)
	codesSpec, _ := canonical["error_codes"].(map[string]any)
	core, _ := codesSpec["core"].(map[string]any)

	for name, spec := range contract.Codes {
		if spec.Core {
			continue
		}
		if !strings.HasPrefix(name, "E_") {
			t.Errorf("extension code %q is not E_*", name)
		}
		if _, clash := core[name]; clash {
			t.Errorf("extension code %q redefines a core code", name)
		}
		if spec.Exit == 9 && name != "E_HUMAN_REQUIRED" && name != "E_2FA_REQUIRED" {
			t.Errorf("exit 9 is reserved for human-action codes, used by %q", name)
		}
	}
	if _, ok := contract.Codes["E_HUMAN_REQUIRED"]; !ok {
		t.Error("E_HUMAN_REQUIRED must be registered: QR login needs a human")
	}
}

// stdout is the contract channel; stderr is a side channel. A failure must put
// the envelope on stdout, never force an agent to scrape stderr.
func TestContract_FailureEnvelopeGoesToStdout(t *testing.T) {
	mock := newMockUpstream(t)
	mock.Status("/pc/sunflower/v1/resource/list", 500)
	got := runAuthed(t, mock, "free")

	if strings.TrimSpace(got.Stdout) == "" {
		t.Fatal("stdout carried no failure envelope")
	}
	got.ErrorCode(t) // asserts stdout parses as exactly one JSON document
	if !strings.Contains(got.Stderr, "E_SERVER") {
		t.Error("stderr should carry a short human-readable explanation")
	}
}

// --quiet suppresses the human line but must never suppress the machine
// contract or the exit code.
func TestContract_QuietSuppressesStderrNotStdout(t *testing.T) {
	mock := newMockUpstream(t)
	mock.Status("/pc/sunflower/v1/resource/list", 500)
	got := runCLI(t, mock, "free", "--state-dir", stateDir(t, true), "--compact", "--quiet")

	if strings.TrimSpace(got.Stderr) != "" {
		t.Errorf("--quiet left stderr output: %q", got.Stderr)
	}
	if code := got.ErrorCode(t); code != "E_SERVER" {
		t.Errorf("code = %s, want E_SERVER", code)
	}
	if got.Exit != 7 {
		t.Errorf("exit = %d, want 7", got.Exit)
	}
}

func stringsFrom(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func assertKeySet(t *testing.T, label string, actual map[string]any, expected []string) {
	t.Helper()
	got := make([]string, 0, len(actual))
	for key := range actual {
		got = append(got, key)
	}
	sort.Strings(got)
	want := append([]string{}, expected...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s keys = %v, want %v", label, got, want)
	}
}
