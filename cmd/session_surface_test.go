package cmd

import (
	"path/filepath"
	"testing"

	"github.com/fatecannotbealtered/dedao-cli/internal/secret"
)

// Content and notes authenticate different hosts by different means. That is an
// upstream fact, not something a caller should have to know: one tool answers
// "am I set up" once, and clears this machine once.

func seedGetnoteCredentials(t *testing.T, dir string) *secret.Store {
	t.Helper()
	store := secret.New(filepath.Join(dir, "getnote"))
	if err := store.Save(getnoteAPIKeySecret, []byte("gk_live_test")); err != nil {
		t.Fatalf("seed GetNote API key: %v", err)
	}
	if err := store.Save(getnoteClientIDSecret, []byte("cli_test")); err != nil {
		t.Fatalf("seed GetNote client ID: %v", err)
	}
	return store
}

func TestStatus_ReportsBothCredentialDomains(t *testing.T) {
	dir := stateDir(t, false)

	before := runCLI(t, nil, "status", "--state-dir", dir, "--compact")
	if before.Exit != 0 {
		t.Fatalf("status exit = %d: %s", before.Exit, before.Stdout)
	}
	getnote, _ := before.Data(t)["getnote"].(map[string]any)
	if getnote == nil {
		t.Fatal("status did not report the GetNote credential state")
	}
	if configured, _ := getnote["configured"].(bool); configured {
		t.Error("getnote.configured = true before any credential was stored")
	}

	seedGetnoteCredentials(t, dir)
	after := runCLI(t, nil, "status", "--state-dir", dir, "--compact")
	getnote, _ = after.Data(t)["getnote"].(map[string]any)
	if configured, _ := getnote["configured"].(bool); !configured {
		t.Error("getnote.configured = false after credentials were stored")
	}
	// The source matters to a caller deciding whether logout can remove it.
	if source, _ := getnote["api_key_source"].(string); source != "encrypted_store" {
		t.Errorf("api_key_source = %q, want encrypted_store", source)
	}
}

func TestLogout_ClearsBothCredentialDomains(t *testing.T) {
	dir := stateDir(t, true)
	store := seedGetnoteCredentials(t, dir)

	preview := runCLI(t, nil, "logout", "--dry-run", "--state-dir", dir, "--compact")
	if preview.Exit != 0 {
		t.Fatalf("dry-run exit = %d: %s", preview.Exit, preview.Stdout)
	}
	data := preview.Data(t)
	// The preview must name both deletions, or a caller confirms one thing and
	// gets another.
	resources := map[string]bool{}
	if changes, _ := data["preview"].(map[string]any); changes != nil {
		for _, raw := range changes["changes"].([]any) {
			change, _ := raw.(map[string]any)
			resource, _ := change["resource"].(string)
			resources[resource] = true
		}
	}
	for _, want := range []string{"local_credentials", "getnote_stored_credentials"} {
		if !resources[want] {
			t.Errorf("dry-run preview does not name %q; got %v", want, resources)
		}
	}

	token, _ := data["confirm_token"].(string)
	confirmed := runCLI(t, nil, "logout", "--confirm", token, "--state-dir", dir, "--compact")
	if confirmed.Exit != 0 {
		t.Fatalf("confirm exit = %d: %s", confirmed.Exit, confirmed.Stdout)
	}
	if removed, _ := confirmed.Data(t)["getnote_stored_credentials_removed"].(bool); !removed {
		t.Error("logout did not report removing the GetNote credentials")
	}
	if store.Has(getnoteAPIKeySecret) || store.Has(getnoteClientIDSecret) {
		t.Error("logout left GetNote credentials on disk")
	}
}

// The token is bound to both halves, so a preview taken against one GetNote key
// cannot authorise deleting a different one.
func TestLogout_ConfirmTokenRejectsChangedGetnoteCredentials(t *testing.T) {
	dir := stateDir(t, true)
	store := seedGetnoteCredentials(t, dir)

	preview := runCLI(t, nil, "logout", "--dry-run", "--state-dir", dir, "--compact")
	if preview.Exit != 0 {
		t.Fatalf("dry-run exit = %d: %s", preview.Exit, preview.Stdout)
	}
	token, _ := preview.Data(t)["confirm_token"].(string)

	if err := store.Save(getnoteAPIKeySecret, []byte("gk_live_rotated")); err != nil {
		t.Fatalf("rotate GetNote API key: %v", err)
	}

	confirmed := runCLI(t, nil, "logout", "--confirm", token, "--state-dir", dir, "--compact")
	if confirmed.Exit != 6 || confirmed.ErrorCode(t) != "E_CONFLICT" {
		t.Fatalf("rotated credential confirm = exit %d/code %s, want 6/E_CONFLICT",
			confirmed.Exit, confirmed.ErrorCode(t))
	}
	if !store.Has(getnoteAPIKeySecret) {
		t.Error("the rotated GetNote credential was deleted anyway")
	}
}

// `article-notes` returns two things, and only one of them belongs to the
// account. Dedao's own summary of an article arrives whether or not the person
// ever highlighted anything, so the payload must never let a reader present the
// publisher's words as the user's.
func TestArticleNotes_SeparatesTheAccountsWritingFromDedaosOwn(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/api/pc/ledgers/notes/article_noteline", map[string]any{"notes": []any{}})
	mock.OK("/api/pc/ledgers/notepoint/get/usernote", map[string]any{
		// Measured against the live service: editorial text arrives while the
		// ownership flag is 0, and upstream sends that flag as a number.
		"content":           "1、尼采的名言“上帝死了”……",
		"has_article_point": 0,
	})
	got := runAuthed(t, mock, "article-notes", "a1")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if _, exists := data["point"]; exists {
		t.Error("the publisher's summary is still named `point`, next to the account's `notes`")
	}
	if _, exists := data["article_point"]; !exists {
		t.Error("article_point is missing")
	}
	if owned, _ := data["account_wrote_point"].(bool); owned {
		t.Error("account_wrote_point = true though the upstream flag was 0")
	}
}

func TestArticleNotes_ReportsAPointTheAccountDidWrite(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/api/pc/ledgers/notes/article_noteline", map[string]any{"notes": []any{}})
	mock.OK("/api/pc/ledgers/notepoint/get/usernote", map[string]any{
		"content": "我的划线", "has_article_point": 1,
	})
	data := runAuthed(t, mock, "article-notes", "a1").Data(t)
	if owned, _ := data["account_wrote_point"].(bool); !owned {
		t.Error("account_wrote_point = false though the upstream flag was 1")
	}
}

// A permission wall must not look like a service fault. Dedao answers 90015 for
// content the account has no subscription to; E_SERVER is retryable, so an agent
// would have retried a wall that never opens.
func TestBusinessCode_EntitlementWallIsForbiddenNotServerFault(t *testing.T) {
	for _, testCase := range []struct {
		code any
		want string
	}{
		{90015, "E_FORBIDDEN"},
		{5218, "E_NOT_FOUND"},
		{4000, "E_NOT_FOUND"},
		{104000, "E_NOT_FOUND"},
		{500000, "E_SERVER"},
	} {
		if got := businessCode(testCase.code); got != testCase.want {
			t.Errorf("businessCode(%v) = %s, want %s", testCase.code, got, testCase.want)
		}
	}
}

// Switching Dedao accounts must not cost the note credentials: they belong to a
// different service and a different sign-in, and re-authorizing them is a
// separate human step.
func TestLogout_KeepGetnotePreservesTheNoteCredentials(t *testing.T) {
	dir := stateDir(t, true)
	store := seedGetnoteCredentials(t, dir)

	preview := runCLI(t, nil, "logout", "--keep-getnote", "--dry-run", "--state-dir", dir, "--compact")
	if preview.Exit != 0 {
		t.Fatalf("dry-run exit = %d: %s", preview.Exit, preview.Stdout)
	}
	data := preview.Data(t)
	// The preview must not promise a deletion that will not happen.
	changes, _ := data["preview"].(map[string]any)
	for _, raw := range changes["changes"].([]any) {
		change, _ := raw.(map[string]any)
		if resource, _ := change["resource"].(string); resource == "getnote_stored_credentials" {
			t.Error("the preview names the GetNote deletion even with --keep-getnote")
		}
	}
	if kept, _ := data["getnote_credentials_kept"].(bool); !kept {
		t.Error("getnote_credentials_kept = false under --keep-getnote")
	}

	token, _ := data["confirm_token"].(string)
	confirmed := runCLI(t, nil, "logout", "--keep-getnote", "--confirm", token,
		"--state-dir", dir, "--compact")
	if confirmed.Exit != 0 {
		t.Fatalf("confirm exit = %d: %s", confirmed.Exit, confirmed.Stdout)
	}
	if !store.Has(getnoteAPIKeySecret) || !store.Has(getnoteClientIDSecret) {
		t.Fatal("--keep-getnote deleted the note credentials anyway")
	}
	// The Dedao half must still be gone, or this is not a logout at all.
	if authed, _ := runCLI(t, nil, "status", "--state-dir", dir, "--compact").
		Data(t)["authenticated"].(bool); authed {
		t.Error("the Dedao session survived logout")
	}
}

// A token minted for one scope must not execute the other, in either direction.
func TestLogout_ConfirmTokenIsBoundToTheScope(t *testing.T) {
	for _, testCase := range []struct{ preview, confirm string }{
		{"--keep-getnote", ""},
		{"", "--keep-getnote"},
	} {
		dir := stateDir(t, true)
		store := seedGetnoteCredentials(t, dir)

		previewArgs := []string{"logout", "--dry-run", "--state-dir", dir, "--compact"}
		if testCase.preview != "" {
			previewArgs = append([]string{"logout", testCase.preview}, previewArgs[1:]...)
		}
		token, _ := runCLI(t, nil, previewArgs...).Data(t)["confirm_token"].(string)

		confirmArgs := []string{"logout", "--confirm", token, "--state-dir", dir, "--compact"}
		if testCase.confirm != "" {
			confirmArgs = append([]string{"logout", testCase.confirm}, confirmArgs[1:]...)
		}
		got := runCLI(t, nil, confirmArgs...)
		if got.ErrorCode(t) != "E_CONFLICT" {
			t.Errorf("preview %q -> confirm %q = %s, want E_CONFLICT",
				testCase.preview, testCase.confirm, got.ErrorCode(t))
		}
		if !store.Has(getnoteAPIKeySecret) {
			t.Errorf("preview %q -> confirm %q deleted the note credentials",
				testCase.preview, testCase.confirm)
		}
	}
}
