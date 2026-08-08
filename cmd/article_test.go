package cmd

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// articleNodes is a body shaped like a real one: an audio header, an image, a
// paragraph, and a blockquote.
const articleNodes = `[
 {"type":"audio","title":"第一讲","duration":1405},
 {"type":"image","url":"https://piccdn3.umiwi.com/a.jpg","legend":"配图"},
 {"type":"paragraph","text":"正文第一段。","contents":[{"type":"text","text":{"content":"正文第一段。"}}]},
 {"type":"blockquote","text":"被引用的一句。","contents":[{"type":"text","text":{"content":"被引用的一句。"}}]}
]`

// articleMock serves the two-step body flow: info hands out a token, then the
// content endpoint returns nodes. `token` controls whether info issues one,
// which is how the upstream signals entitlement.
func articleMock(t *testing.T, token string) *mockUpstream {
	t.Helper()
	mock := newMockUpstream(t)
	mock.OK("/pc/bauhinia/pc/article/info", map[string]any{
		"dd_article_token": token,
		"is_buy":           1,
		"is_free_try":      false,
		"like_num":         3,
		"class_enid":       "course1",
	})
	mock.handlers["/pc/ddarticle/v1/article/get/v2"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"h": map[string]any{"c": 0, "e": ""},
			"c": map[string]any{"content": articleNodes, "article": map[string]any{"Id": 1}},
		})
	}
	return mock
}

func TestArticle_RendersNodesTextAndMarkdown(t *testing.T) {
	for _, render := range []string{"nodes", "text", "markdown"} {
		t.Run(render, func(t *testing.T) {
			got := runAuthed(t, articleMock(t, "tok"), "article", "enid1", "--render", render)
			if got.Exit != 0 {
				t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
			}
			data := got.Data(t)
			if entitled, _ := data["entitled"].(bool); !entitled {
				t.Error("entitled = false for an owned article")
			}
			if count, _ := data["node_count"].(float64); count != 4 {
				t.Errorf("node_count = %v, want 4", data["node_count"])
			}
			// SEC-SPEC §2: the marker names the externally-controlled fields, so
			// an agent knows which values to quarantine. A bare `true` would
			// not answer that.
			marked, ok := data["_untrusted"].([]any)
			if !ok {
				t.Fatalf("_untrusted = %#v, want the array of untrusted fields",
					data["_untrusted"])
			}
			if !slices.Contains(marked, any(render)) {
				t.Errorf("_untrusted = %v; it must name the rendered body field %q",
					marked, render)
			}
			switch render {
			case "text":
				text, _ := data["text"].(string)
				if !strings.Contains(text, "正文第一段") {
					t.Errorf("text render lost the paragraph: %q", text)
				}
			case "markdown":
				md, _ := data["markdown"].(string)
				for _, want := range []string{"![", "> ", "[音频]"} {
					if !strings.Contains(md, want) {
						t.Errorf("markdown render is missing %q", want)
					}
				}
			default:
				if _, ok := data["nodes"].([]any); !ok {
					t.Error("nodes render did not return the node array")
				}
			}
		})
	}
}

// The upstream signals "not entitled" by declining to issue a content token.
// That must surface as a permission failure, not as an empty body that reads
// like the article had no text.
func TestArticle_WithoutTokenIsForbidden(t *testing.T) {
	got := runAuthed(t, articleMock(t, ""), "article", "enid1", "--render", "text")
	if code := got.ErrorCode(t); code != "E_FORBIDDEN" {
		t.Errorf("code = %s, want E_FORBIDDEN", code)
	}
	if got.Exit != 4 {
		t.Errorf("exit = %d, want 4", got.Exit)
	}
}

// The content token must never reach stdout, even though the client reads it
// internally to fetch the body.
func TestArticle_DoesNotLeakTheContentToken(t *testing.T) {
	got := runAuthed(t, articleMock(t, "super-secret-token"), "article", "enid1", "--render", "text")
	if strings.Contains(got.Stdout, "super-secret-token") {
		t.Errorf("the content token leaked into stdout:\n%s", got.Stdout)
	}
	if strings.Contains(got.Stdout, "dd_article_token") {
		t.Error("the token field name leaked into the payload")
	}
}

func TestArticle_RejectsUnknownRender(t *testing.T) {
	got := runAuthed(t, articleMock(t, "tok"), "article", "enid1", "--render", "pdf")
	if code := got.ErrorCode(t); code != "E_VALIDATION" {
		t.Errorf("code = %s, want E_VALIDATION", code)
	}
	if got.Exit != 2 {
		t.Errorf("exit = %d, want 2", got.Exit)
	}
}

// raw emits the rendered body unwrapped, which only makes sense once a
// rendering has been chosen.
func TestArticle_RawRequiresARendering(t *testing.T) {
	got := runCLI(t, articleMock(t, "tok"), "article", "enid1",
		"--state-dir", stateDir(t, true), "--format", "raw")
	if code := got.ErrorCode(t); code != "E_USAGE" {
		t.Errorf("code = %s, want E_USAGE", code)
	}
}

func TestArticle_RawEmitsTheBodyUnwrapped(t *testing.T) {
	got := runCLI(t, articleMock(t, "tok"), "article", "enid1",
		"--state-dir", stateDir(t, true), "--format", "raw", "--render", "text")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	if strings.Contains(got.Stdout, `"schema_version"`) {
		t.Error("raw output must not carry the machine envelope")
	}
	if !strings.Contains(got.Stdout, "正文第一段") {
		t.Errorf("raw output lost the body:\n%s", got.Stdout)
	}
}

func TestArticle_UnauthenticatedIsAuthError(t *testing.T) {
	got := runCLI(t, articleMock(t, "tok"), "article", "enid1",
		"--state-dir", stateDir(t, false), "--compact")
	if code := got.ErrorCode(t); code != "E_AUTH" {
		t.Errorf("code = %s, want E_AUTH", code)
	}
	if got.Exit != 4 {
		t.Errorf("exit = %d, want 4", got.Exit)
	}
}
