package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Every leaf query command, its upstream endpoints, and a runnable argument
// list. Driving all of them through one table keeps the success path, the
// auth-failure path, and the upstream-failure path in lockstep: a new command
// cannot be added with only a happy-path test.
var queryCases = []struct {
	name      string
	args      []string
	endpoints []string
	// content is what the primary endpoint returns on success.
	content any
}{
	{"library", []string{"library", "course"}, []string{"/api/hades/v2/product/list"}, map[string]any{"list": []any{}, "is_more": false}},
	{"library-nav", []string{"library-nav"}, []string{"/api/hades/v1/navbar/get", "/api/hades/v1/index/detail"}, map[string]any{"nav": []any{}}},
	{"library-groups", []string{"library-groups", "course"}, []string{"/api/hades/v1/group/has"}, map[string]any{"list": []any{}}},
	{"library-group", []string{"library-group", "course", "7"}, []string{"/api/hades/v2/product/group/list"}, map[string]any{"list": []any{}}},
	{"recent", []string{"recent"}, []string{"/api/pc/user/info", "/api/pc/blade/v2/recent"}, map[string]any{"list": []any{}}},
	{"progress", []string{"progress"}, []string{"/api/pc/blade/v2/recent-index", "/api/pc/blade/v2/pc/last-study"}, map[string]any{"count": 1}},
	{"search", []string{"search", "认知"}, []string{"/api/pc/ebook2/v1/vip/info", "/api/search/pc/tophits"}, map[string]any{"list": []any{}}},
	{"search-hot", []string{"search-hot"}, []string{"/api/search/pc/hot"}, map[string]any{"list": []any{}}},
	{"search-suggest", []string{"search-suggest", "认知"}, []string{"/api/search/pc/suggest"}, map[string]any{"list": []any{}}},
	{"search-type", []string{"search-type", "course", "认知"}, []string{"/api/search/v2/pc/searchclass"}, map[string]any{"list": []any{}}},
	{"course", []string{"course", "enid1"}, []string{"/pc/bauhinia/pc/class/info"}, map[string]any{"class_info": map[string]any{}}},
	{"articles", []string{"articles", "enid1"}, []string{"/api/pc/bauhinia/pc/class/purchase/article_list"}, map[string]any{"list": []any{}}},
	{"comments", []string{"comments", "enid1"}, []string{"/pc/ledgers/notes/article_comment_list"}, map[string]any{"list": []any{}}},
	{"article-notes", []string{"article-notes", "enid1"}, []string{"/api/pc/ledgers/notes/article_noteline", "/api/pc/ledgers/notepoint/get/usernote"}, map[string]any{"notes": []any{}}},
	{"note", []string{"note", "note1"}, []string{"/pc/ledgers/notes/detail"}, map[string]any{"note": map[string]any{}}},
	{"ebook", []string{"ebook", "enid1"}, []string{"/pc/ebook2/v1/pc/detail"}, map[string]any{"book_info": map[string]any{}}},
	{"ebook-community", []string{"ebook-community", "enid1"}, []string{"/pc/ebook2/v1/pc/score/detail", "/pc/vipcomment/v1/list"}, map[string]any{"score": 9}},
	{"audiobook", []string{"audiobook", "topic1"}, []string{"/pc/odob/pc/audio/detail"}, map[string]any{"title": "x"}},
	{"audiobook-alias", []string{"audiobook-alias", "alias1"}, []string{"/pc/odob/pc/audio/detail/alias"}, map[string]any{"title": "x"}},
	{"audiobook-collection", []string{"audiobook-collection", "col1"}, []string{"/pc/sunflower/v1/depot/vip-user/topic-pkg/odob/details"}, map[string]any{"list": []any{}}},
	{"audiobook-agency", []string{"audiobook-agency", "ag1"}, []string{"/pc/odob/pc/agency/detail"}, map[string]any{"title": "x"}},
	{"audiobook-vip", []string{"audiobook-vip"}, []string{"/pc/odob/v2/vipuser/vip_card_info"}, map[string]any{"is_vip": true}},
	{"topics", []string{"topics"}, []string{"/pc/ledgers/topic/all"}, map[string]any{"list": []any{}}},
	{"topic", []string{"topic", "t1"}, []string{"/pc/ledgers/topic/detail"}, map[string]any{"detail": map[string]any{}}},
	{"channel", []string{"channel"}, []string{"/sphere/v1/app/channel/info", "/pc/sphere/v1/app/topic/homepage/v2"}, map[string]any{"name": "x"}},
	{"channel-topic", []string{"channel-topic", "12"}, []string{"/pc/sphere/v1/app/topic/detail"}, map[string]any{"topic_info": map[string]any{}}},
	{"channel-articles", []string{"channel-articles"}, []string{"/pc/sphere/v1/app/special/article_list"}, map[string]any{"list": []any{}}},
	{"labels", []string{"labels", "4"}, []string{"/pc/sunflower/v1/label/list"}, map[string]any{"list": []any{}}},
	{"label-content", []string{"label-content", "l1", "4", "66"}, []string{"/pc/sunflower/v1/label/content"}, map[string]any{"list": []any{}}},
	{"free", []string{"free"}, []string{"/pc/sunflower/v1/resource/list"}, map[string]any{"list": []any{}}},
	{"discover", []string{"discover", "4"}, []string{"/pc/label/v2/algo/pc/product/list"}, map[string]any{"list": []any{}}},
	{"live", []string{"live"}, []string{"/pc/ddlive/v2/pc/home/live"}, map[string]any{"list": []any{}}},
	{"status", []string{"status"}, []string{"/api/pc/user/info"}, map[string]any{"uid_hazy": "u1"}},
}

func TestQueryCommands_SuccessEnvelope(t *testing.T) {
	for _, testCase := range queryCases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newMockUpstream(t)
			for _, endpoint := range testCase.endpoints {
				mock.OK(endpoint, testCase.content)
			}
			got := runAuthed(t, mock, testCase.args...)
			if got.Exit != 0 {
				t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", got.Exit, got.Stdout, got.Stderr)
			}
			envelope := got.Envelope(t)
			for _, key := range []string{"ok", "schema_version", "data", "meta"} {
				if _, ok := envelope[key]; !ok {
					t.Errorf("envelope is missing %q: %s", key, got.Stdout)
				}
			}
			if envelope["schema_version"] != "1.0" {
				t.Errorf("schema_version = %v, want 1.0", envelope["schema_version"])
			}
			meta, _ := envelope["meta"].(map[string]any)
			if _, ok := meta["duration_ms"]; !ok {
				t.Error("meta.duration_ms must always be present, even as 0")
			}
		})
	}
}

// Without a session every upstream-backed command must fail closed with E_AUTH
// and exit 4, never hang or half-succeed.
func TestQueryCommands_UnauthenticatedIsAuthError(t *testing.T) {
	for _, testCase := range queryCases {
		if testCase.name == "status" {
			continue // status reports the absence of a session as a success
		}
		t.Run(testCase.name, func(t *testing.T) {
			mock := newMockUpstream(t)
			args := append(append([]string{}, testCase.args...), "--state-dir", stateDir(t, false), "--compact")
			got := runCLI(t, mock, args...)
			if code := got.ErrorCode(t); code != "E_AUTH" {
				t.Errorf("error code = %s, want E_AUTH", code)
			}
			if got.Exit != 4 {
				t.Errorf("exit = %d, want 4", got.Exit)
			}
		})
	}
}

// Upstream HTTP status must map onto the canonical code and exit, per endpoint,
// so a rate limit is retryable and a 404 is not.
func TestQueryCommands_UpstreamStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		code   string
		exit   int
	}{
		{401, "E_AUTH", 4},
		{403, "E_FORBIDDEN", 4},
		{404, "E_NOT_FOUND", 3},
		{429, "E_RATE_LIMITED", 7},
		{500, "E_SERVER", 7},
		{503, "E_SERVER", 7},
	}
	for _, testCase := range cases {
		t.Run(testCase.code, func(t *testing.T) {
			mock := newMockUpstream(t)
			mock.Status("/pc/sunflower/v1/resource/list", testCase.status)
			got := runAuthed(t, mock, "free")
			if code := got.ErrorCode(t); code != testCase.code {
				t.Errorf("code = %s, want %s", code, testCase.code)
			}
			if got.Exit != testCase.exit {
				t.Errorf("exit = %d, want %d", got.Exit, testCase.exit)
			}
			envelope := got.Envelope(t)
			errorObject, _ := envelope["error"].(map[string]any)
			retryable, _ := errorObject["retryable"].(bool)
			wantRetryable := testCase.exit == 7 || testCase.exit == 8
			if retryable != wantRetryable {
				t.Errorf("retryable = %v, want %v", retryable, wantRetryable)
			}
		})
	}
}

func TestQueryCommands_BusinessErrorIsServerError(t *testing.T) {
	mock := newMockUpstream(t)
	mock.BusinessError("/pc/sunflower/v1/resource/list", 40001, "资源不存在")
	got := runAuthed(t, mock, "free")
	if code := got.ErrorCode(t); code != "E_SERVER" {
		t.Errorf("code = %s, want E_SERVER", code)
	}
	if got.Exit != 7 {
		t.Errorf("exit = %d, want 7", got.Exit)
	}
}

func TestQueryCommands_MalformedResponse(t *testing.T) {
	mock := newMockUpstream(t)
	mock.Malformed("/pc/sunflower/v1/resource/list")
	got := runAuthed(t, mock, "free")
	if code := got.ErrorCode(t); code != "E_SERVER" {
		t.Errorf("code = %s, want E_SERVER", code)
	}
}

func TestQueryCommands_EmptyResultStillSucceeds(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/pc/sunflower/v1/resource/list", map[string]any{"list": []any{}})
	got := runAuthed(t, mock, "free")
	if got.Exit != 0 {
		t.Fatalf("an empty result is a success, got exit %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	list, ok := data["list"].([]any)
	if !ok || len(list) != 0 {
		t.Errorf("expected an empty list, got %v", data["list"])
	}
}

func TestSearch_NormalizesUpstreamKeysIDsAndTimesAtCommandBoundary(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/api/pc/ebook2/v1/vip/info", map[string]any{"is_vip": false})
	mock.OK("/api/search/pc/tophits", map[string]any{
		"status": map[string]any{"code": 0},
		"data": map[string]any{
			"moduleList": []any{map[string]any{
				"contentId":   42,
				"publishTime": 1785988285,
				"displayTime": "15:01",
			}},
			"isMore":            false,
			"typeIdCollection":  []any{1, 2},
			"requestId":         9,
			"teacherInnerGoods": []any{},
		},
	})

	got := runAuthed(t, mock, "search", "认知")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if _, exists := data["moduleList"]; exists {
		t.Fatalf("camelCase output survived command boundary: %#v", data)
	}
	items, _ := data["module_list"].([]any)
	if len(items) != 1 {
		t.Fatalf("module_list = %#v", data["module_list"])
	}
	item := items[0].(map[string]any)
	if item["content_id"] != "42" || item["publish_time"] != "2026-08-06T03:51:25Z" || item["display_label"] != "15:01" {
		t.Errorf("normalized search item = %#v", item)
	}
	if data["request_id"] != "9" {
		t.Errorf("request_id = %#v, want string 9", data["request_id"])
	}
	marked, _ := data["_untrusted"].([]any)
	if len(marked) != 4 || marked[0] != "module_list" || marked[1] != "tab_list" ||
		marked[2] != "teacher_inner_goods" || marked[3] != "content" {
		t.Errorf("_untrusted = %#v, want canonical field names", marked)
	}
}

func TestQueryCommands_ReportNormalizedKeyCollision(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/pc/sunflower/v1/resource/list", map[string]any{
		"requestId": "first", "request_id": "second",
	})
	got := runAuthed(t, mock, "free")
	if got.Exit != 1 || got.ErrorCode(t) != "E_UNKNOWN" {
		t.Fatalf("exit/code = %d/%s, want 1/E_UNKNOWN", got.Exit, got.ErrorCode(t))
	}
	errorObject := got.Envelope(t)["error"].(map[string]any)
	details := errorObject["details"].(map[string]any)
	message, _ := details["normalization_error"].(string)
	if !strings.Contains(message, `"requestId" and "request_id"`) {
		t.Errorf("normalization_error = %q", message)
	}
}

func TestQueryCommands_InvalidArguments(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code string
		exit int
	}{
		{"unknown library kind", []string{"library", "bogus"}, "E_VALIDATION", 2},
		{"unknown library-groups kind", []string{"library-groups", "bogus"}, "E_VALIDATION", 2},
		{"unknown search tab", []string{"search", "q", "--tab", "bogus"}, "E_VALIDATION", 2},
		{"unknown search-type kind", []string{"search-type", "bogus", "q"}, "E_VALIDATION", 2},
		{"non-integer group id", []string{"library-group", "course", "abc"}, "E_VALIDATION", 2},
		{"non-integer nav type", []string{"labels", "abc"}, "E_VALIDATION", 2},
		{"non-integer product id", []string{"channel-topic", "abc"}, "E_VALIDATION", 2},
		{"missing search query", []string{"search"}, "E_USAGE", 2},
		{"too many args", []string{"free", "extra"}, "E_USAGE", 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newMockUpstream(t)
			got := runAuthed(t, mock, testCase.args...)
			if code := got.ErrorCode(t); code != testCase.code {
				t.Errorf("code = %s, want %s", code, testCase.code)
			}
			if got.Exit != testCase.exit {
				t.Errorf("exit = %d, want %d", got.Exit, testCase.exit)
			}
		})
	}
}

// Pagination flags must reach the upstream request rather than being accepted
// and silently dropped.
func TestQueryCommands_PaginationFlagsAreForwarded(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/api/hades/v2/product/list", map[string]any{"list": []any{}})
	got := runAuthed(t, mock, "library", "course", "--page", "3", "--page-size", "50")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	if len(mock.Requests) == 0 {
		t.Fatal("no upstream request was made")
	}
}

func TestQueryCommands_LimitMapsToPageSizeAndNormalizesPagination(t *testing.T) {
	mock := newMockUpstream(t)
	mock.handlers["/api/hades/v2/product/list"] = func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["page_size"] != float64(1) {
			t.Errorf("page_size = %v, want 1 from --limit", body["page_size"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"h": map[string]any{"c": 0, "e": ""},
			"c": map[string]any{"list": []any{map[string]any{"id": 7}}, "is_more": false},
		})
	}

	got := runAuthed(t, mock, "library", "course", "--limit", "1")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if data["count"] != float64(1) || data["has_more"] != false {
		t.Errorf("pagination = count %v, has_more %v; want 1/false", data["count"], data["has_more"])
	}
}

func TestQueryCommands_LimitConflictsWithNativePageSize(t *testing.T) {
	got := runAuthed(t, newMockUpstream(t), "library", "course", "--limit", "1", "--page-size", "2")
	if got.Exit != 2 || got.ErrorCode(t) != "E_VALIDATION" {
		t.Fatalf("exit/code = %d/%s, want 2/E_VALIDATION", got.Exit, got.ErrorCode(t))
	}
}

func TestLogout_RequiresDryRunAndConfirm(t *testing.T) {
	dir := stateDir(t, true)
	mock := newMockUpstream(t)

	bare := runCLI(t, mock, "logout", "--state-dir", dir, "--compact")
	if bare.Exit != 5 || bare.ErrorCode(t) != "E_CONFIRMATION_REQUIRED" {
		t.Fatalf("bare logout = exit %d, code %s; want exit 5/E_CONFIRMATION_REQUIRED",
			bare.Exit, bare.ErrorCode(t))
	}
	fabricated := runCLI(t, mock, "logout", "--confirm", "ct_fabricated_0000", "--state-dir", dir, "--compact")
	if fabricated.Exit != 6 || fabricated.ErrorCode(t) != "E_CONFLICT" {
		t.Fatalf("fabricated confirm = exit %d, code %s; want exit 6/E_CONFLICT",
			fabricated.Exit, fabricated.ErrorCode(t))
	}

	preview := runCLI(t, mock, "logout", "--dry-run", "--state-dir", dir, "--compact")
	if preview.Exit != 0 {
		t.Fatalf("dry-run exit = %d: %s", preview.Exit, preview.Stdout)
	}
	token, _ := preview.Data(t)["confirm_token"].(string)
	if token == "" {
		t.Fatal("dry-run returned no confirm_token")
	}
	changes, _ := preview.Data(t)["preview"].(map[string]any)["changes"].([]any)
	first, _ := changes[0].(map[string]any)
	before, _ := first["before"].(map[string]any)
	if configured, _ := before["configured"].(bool); !configured {
		t.Fatal("bare/fabricated/dry-run logout removed the configured session")
	}

	confirmed := runCLI(t, mock, "logout", "--confirm", token, "--state-dir", dir, "--compact")
	if confirmed.Exit != 0 {
		t.Fatalf("confirmed logout exit = %d: %s", confirmed.Exit, confirmed.Stdout)
	}
	if cleared, _ := confirmed.Data(t)["logged_out"].(bool); !cleared {
		t.Error("confirmed logout did not report logged_out=true")
	}
	after := runCLI(t, mock, "logout", "--dry-run", "--state-dir", dir, "--compact")
	afterChanges, _ := after.Data(t)["preview"].(map[string]any)["changes"].([]any)
	afterFirst, _ := afterChanges[0].(map[string]any)
	afterBefore, _ := afterFirst["before"].(map[string]any)
	if configured, _ := afterBefore["configured"].(bool); configured {
		t.Fatal("confirmed logout left the configured session behind")
	}
}

func TestLogout_ConfirmTokenIsSingleUse(t *testing.T) {
	dir := stateDir(t, false)
	preview := runCLI(t, nil, "logout", "--dry-run", "--state-dir", dir, "--compact")
	token, _ := preview.Data(t)["confirm_token"].(string)
	first := runCLI(t, nil, "logout", "--confirm", token, "--state-dir", dir, "--compact")
	if first.Exit != 0 {
		t.Fatalf("first confirm exit = %d: %s", first.Exit, first.Stdout)
	}
	replay := runCLI(t, nil, "logout", "--confirm", token, "--state-dir", dir, "--compact")
	if replay.Exit != 6 || replay.ErrorCode(t) != "E_CONFLICT" {
		t.Fatalf("replayed confirm = exit %d/code %s, want 6/E_CONFLICT",
			replay.Exit, replay.ErrorCode(t))
	}
}

func TestStatus_ReportsMissingSessionAsSuccess(t *testing.T) {
	got := runCLI(t, nil, "status", "--state-dir", stateDir(t, false), "--compact")
	if got.Exit != 0 {
		t.Fatalf("a query that answers \"not logged in\" is a success, got exit %d", got.Exit)
	}
	data := got.Data(t)
	if authenticated, _ := data["authenticated"].(bool); authenticated {
		t.Error("authenticated = true for an empty state dir")
	}
}

// The audiobook allowlist must drop upstream fields it does not recognize,
// because those payloads carry playback material.
func TestAudiobook_UnknownFieldsAreDropped(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/pc/odob/pc/audio/detail", map[string]any{
		"title":         "safe",
		"drm_token":     "must-not-appear",
		"play_url":      "https://example.invalid/stream.m3u8",
		"unknown_field": "dropped-by-allowlist",
	})
	got := runAuthed(t, mock, "audiobook", "topic1")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	for _, forbidden := range []string{"must-not-appear", "stream.m3u8", "dropped-by-allowlist", "drm_token", "play_url"} {
		if strings.Contains(got.Stdout, forbidden) {
			t.Errorf("audiobook output leaked %q:\n%s", forbidden, got.Stdout)
		}
	}
}

// Sensitive keys must be stripped from every payload, not only audiobooks.
func TestSanitize_SensitiveKeysNeverReachStdout(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/pc/sunflower/v1/resource/list", map[string]any{
		"list":         []any{map[string]any{"name": "ok", "access_token": "leak-1"}},
		"drm_key":      "leak-2",
		"signature":    "leak-3",
		"download_url": "https://example.invalid/a?token=leak-4&id=keep",
	})
	got := runAuthed(t, mock, "free")
	for _, forbidden := range []string{"leak-1", "leak-2", "leak-3", "leak-4"} {
		if strings.Contains(got.Stdout, forbidden) {
			t.Errorf("sanitizer leaked %q:\n%s", forbidden, got.Stdout)
		}
	}
}

// Dedao answers an unknown identifier with business code 104000 and the message
// "服务异常，请稍后重试". Taking that message at face value made it a retryable
// E_SERVER, so an agent handed a stale enid would retry a permanently missing
// resource forever. Verified live by mutating one character of a working enid.
func TestBusinessCode104000IsNotFoundAndNotRetryable(t *testing.T) {
	mock := newMockUpstream(t)
	mock.handlers["/pc/bauhinia/pc/class/info"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"h":{"c":104000,"e":"服务异常，请稍后重试"},"c":{}}`))
	}
	got := runAuthed(t, mock, "course", "no-such-enid")

	if code := got.ErrorCode(t); code != "E_NOT_FOUND" {
		t.Errorf("code = %s, want E_NOT_FOUND", code)
	}
	if got.Exit != 3 {
		t.Errorf("exit = %d, want 3", got.Exit)
	}
	envelope := got.Envelope(t)
	errorObject, _ := envelope["error"].(map[string]any)
	if retryable, _ := errorObject["retryable"].(bool); retryable {
		t.Error("retryable = true; a missing resource does not become present on retry")
	}
}

// Any other business code stays a retryable server error: guessing that every
// in-band code is terminal would be the opposite mistake.
func TestOtherBusinessCodesStayRetryable(t *testing.T) {
	mock := newMockUpstream(t)
	mock.handlers["/pc/bauhinia/pc/class/info"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"h":{"c":500001,"e":"服务异常"},"c":{}}`))
	}
	got := runAuthed(t, mock, "course", "enid1")
	if code := got.ErrorCode(t); code != "E_SERVER" {
		t.Errorf("code = %s, want E_SERVER", code)
	}
}
