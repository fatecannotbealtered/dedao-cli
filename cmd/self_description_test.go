package cmd

import (
	"slices"
	"strings"
	"testing"
)

func TestReference_DescribesEveryCommandUsably(t *testing.T) {
	got := runCLI(t, nil, "reference", "--compact")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)

	for _, key := range []string{
		"tool", "version", "schema_version", "risk_tier", "minimum_skill_version",
		"release_readiness", "commands", "schemas", "exit_codes", "global_options", "security", "output",
	} {
		if _, ok := data[key]; !ok {
			t.Errorf("reference is missing %q", key)
		}
	}

	commands, _ := data["commands"].([]any)
	schemas, _ := data["schemas"].(map[string]any)
	if len(commands) == 0 {
		t.Fatal("reference enumerated no commands")
	}

	// CLI-SPEC §11 forbids stub schemas: every leaf must resolve to a real
	// entry in the top-level table and ship at least one runnable example.
	for _, raw := range commands {
		command, _ := raw.(map[string]any)
		path, _ := command["path"].(string)
		children, _ := command["children"].([]any)
		if len(children) > 0 {
			continue
		}
		label, _ := command["output_schema"].(string)
		if label == "" {
			t.Errorf("%s declares no output_schema", path)
			continue
		}
		schema, ok := schemas[label].(map[string]any)
		if !ok {
			t.Errorf("%s references unknown schema %q", path, label)
			continue
		}
		fields, _ := schema["fields"].([]any)
		if len(fields) == 0 {
			t.Errorf("schema %q for %s has no fields (stub)", label, path)
		}
		examples, _ := command["examples"].([]any)
		if len(examples) == 0 {
			t.Errorf("%s ships no examples", path)
		}
		for _, example := range examples {
			text, _ := example.(string)
			if !strings.HasPrefix(text, "dedao-cli ") {
				t.Errorf("%s example is not runnable: %q", path, text)
			}
		}
	}
}

func TestReference_ReleaseReadinessIsHonest(t *testing.T) {
	data := runCLI(t, nil, "reference", "--compact").Data(t)
	readiness, _ := data["release_readiness"].(map[string]any)

	level, _ := readiness["level"].(string)
	switch level {
	case "stable", "beta", "unpublishable":
	default:
		t.Fatalf("level = %q, want stable, beta, or unpublishable", level)
	}

	// A stable claim requires recorded live-smoke evidence. Anything else is
	// exactly the dishonesty the gate exists to catch.
	if level == "stable" {
		if status, _ := readiness["live_smoke_status"].(string); status == "missing" || status == "unknown" {
			t.Errorf("level is stable but live_smoke_status = %q", status)
		}
	}
	for _, key := range []string{"fcc_required", "fcc_status", "mock_upstream_status", "live_smoke_status", "reason", "required_evidence"} {
		if _, ok := readiness[key]; !ok {
			t.Errorf("release_readiness is missing %q", key)
		}
	}
}

func TestDoctor_ReportsReleaseReadinessConsistentWithReference(t *testing.T) {
	referenceData := runCLI(t, nil, "reference", "--compact").Data(t)
	readiness, _ := referenceData["release_readiness"].(map[string]any)
	level, _ := readiness["level"].(string)

	got := runCLI(t, nil, "doctor", "--state-dir", stateDir(t, false), "--compact")
	if got.Exit != 0 {
		t.Fatalf("doctor should not fail the process: exit %d", got.Exit)
	}
	checks, _ := got.Data(t)["checks"].([]any)

	found := false
	for _, raw := range checks {
		check, _ := raw.(map[string]any)
		if name, _ := check["check"].(string); name != "release_readiness" {
			continue
		}
		found = true
		status, _ := check["status"].(string)
		want := map[string]string{"stable": "pass", "beta": "warn"}[level]
		if want == "" {
			want = "fail"
		}
		if status != want {
			t.Errorf("doctor release_readiness = %q, want %q for level %q", status, want, level)
		}
		if status != "pass" && check["fix"] == nil {
			t.Error("a non-passing check must carry an actionable fix")
		}
	}
	if !found {
		t.Error("doctor must include a release_readiness check")
	}
}

func TestDoctor_ReportsMissingCredentialsAsFailure(t *testing.T) {
	got := runCLI(t, nil, "doctor", "--state-dir", stateDir(t, false), "--compact")
	checks, _ := got.Data(t)["checks"].([]any)
	for _, raw := range checks {
		check, _ := raw.(map[string]any)
		if name, _ := check["check"].(string); name != "credentials" {
			continue
		}
		if status, _ := check["status"].(string); status != "fail" {
			t.Errorf("credentials status = %q with no session, want fail", status)
		}
		if check["fix"] == nil {
			t.Error("a failing credentials check must say how to fix it")
		}
	}
}

func TestContextCommand_ReportsCredentialsWithoutLeakingThem(t *testing.T) {
	dir := stateDir(t, true)
	got := runCLI(t, nil, "context", "--state-dir", dir, "--compact")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	credentials, _ := data["credentials"].(map[string]any)
	for _, key := range []string{"configured", "checked", "valid", "refreshable"} {
		if _, ok := credentials[key]; !ok {
			t.Errorf("credentials is missing %q", key)
		}
	}
	// The cookie fixture value must never appear anywhere in the output.
	if strings.Contains(got.Stdout, "test-session") {
		t.Errorf("context leaked the session cookie:\n%s", got.Stdout)
	}
}

// A stored-but-dead session must report configured=true, valid=false. Folding
// the two together would send an agent down a path that can only 401.
func TestContextCommand_ExpiredSessionIsConfiguredButInvalid(t *testing.T) {
	mock := newMockUpstream(t)
	mock.Status("/api/pc/user/info", 401)
	got := runCLI(t, mock, "context", "--state-dir", stateDir(t, true), "--compact")

	credentials, _ := got.Data(t)["credentials"].(map[string]any)
	if configured, _ := credentials["configured"].(bool); !configured {
		t.Error("configured = false, want true: a cookie is stored")
	}
	if valid, _ := credentials["valid"].(bool); valid {
		t.Error("valid = true for an expired session")
	}
	if checked, _ := credentials["checked"].(bool); !checked {
		t.Error("checked = false, but the probe did reach upstream")
	}
}

func TestChangelog_ParsesEmbeddedSource(t *testing.T) {
	got := runCLI(t, nil, "changelog", "--compact")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if data["current_version"] != version {
		t.Errorf("current_version = %v, want %v", data["current_version"], version)
	}
	entries, _ := data["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("changelog produced no entries; the embed or the parser is broken")
	}
	first, _ := entries[0].(map[string]any)
	if _, ok := first["version"]; !ok {
		t.Error("entry is missing version")
	}
	if _, ok := first["changes"]; !ok {
		t.Error("entry is missing changes")
	}
}

// --since must return a strict delta, so an agent that already read version X
// is not handed X again.
func TestChangelog_SinceReturnsStrictlyNewer(t *testing.T) {
	got := runCLI(t, nil, "changelog", "--since", version, "--compact")
	entries, _ := got.Data(t)["entries"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if entry["version"] == version {
			t.Errorf("--since %s returned %s itself", version, version)
		}
	}
}

func TestChangelog_ParserSkipsUnreleased(t *testing.T) {
	entries := parseChangelog("## [Unreleased]\n\n### Added\n- pending\n\n## [1.0.0] - 2026-01-01\n\n### Added\n- shipped\n")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (Unreleased must be skipped)", len(entries))
	}
	if entries[0].Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", entries[0].Version)
	}
	if entries[0].Date != "2026-01-01" {
		t.Errorf("date = %q, want 2026-01-01", entries[0].Date)
	}
	if got := entries[0].Changes["added"]; len(got) != 1 || got[0] != "shipped" {
		t.Errorf("changes.added = %v, want [shipped]", got)
	}
}

func TestVersionFlag_ReportsToolVersion(t *testing.T) {
	got := runCLI(t, nil, "--version")
	if got.Exit != 0 {
		t.Fatalf("exit = %d", got.Exit)
	}
	if !strings.Contains(got.Stdout, version) {
		t.Errorf("--version output %q does not contain %q", got.Stdout, version)
	}
}

// The `_untrusted` marker on a payload is generated from the command's declared
// schema, so a field named there but absent from the schema's own field list
// would send an agent looking for a value that never arrives. This catches the
// typo at build time rather than at the agent.
func TestSchemas_UntrustedFieldsAreDeclaredFields(t *testing.T) {
	for name, schema := range outputSchemas {
		for _, untrusted := range schema.UntrustedFields {
			if !slices.Contains(schema.Fields, untrusted) {
				t.Errorf("schema %q marks %q untrusted but does not declare it as a field",
					name, untrusted)
			}
		}
	}
}

// Every command that returns upstream content must resolve to a non-empty
// untrusted list; a silent empty one is how the marker would quietly disappear.
func TestSchemas_ContentCommandsDeclareUntrustedFields(t *testing.T) {
	selfDescribing := map[string]bool{
		"reference": true, "context": true, "doctor": true, "changelog": true,
		"status": true, "logout": true, "login": true, "login-resume": true,
		"route": true,
		// `update` reports this tool's own release state, not upstream content.
		"update": true,
	}
	for command := range commandSchemas {
		if selfDescribing[command] {
			continue
		}
		if len(untrustedFieldsFor(command)) == 0 {
			t.Errorf("%s returns upstream content but declares no untrusted fields", command)
		}
	}
}

// release_readiness explains which commands are deliberately absent. Both tools
// once listed a command there that was in fact declared and working, which is
// the exact dishonesty the readiness gate exists to prevent -- and it fed
// straight into the Skill, where it told agents to refuse supported requests.
func TestReference_ReadinessDoesNotDisownADeclaredCommand(t *testing.T) {
	data := runCLI(t, nil, "reference", "--compact").Data(t)
	readiness, _ := data["release_readiness"].(map[string]any)
	reason, _ := readiness["reason"].(string)

	open := strings.Index(reason, "commands (")
	if open < 0 {
		return // no "not implemented" list to check
	}
	list := reason[open+len("commands ("):]
	if close := strings.Index(list, ")"); close >= 0 {
		list = list[:close]
	}

	declared := map[string]bool{}
	commands, _ := data["commands"].([]any)
	for _, raw := range commands {
		if entry, ok := raw.(map[string]any); ok {
			if name, _ := entry["name"].(string); name != "" {
				declared[name] = true
			}
		}
	}
	for _, part := range strings.Split(list, ",") {
		name := strings.Trim(strings.TrimSpace(part), "`*")
		if declared[name] {
			t.Errorf("release_readiness calls %q unimplemented, but it is declared in reference", name)
		}
	}
}
