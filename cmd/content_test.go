package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// captionMock serves an article whose video carries caption tracks, plus the
// tracks themselves on the same mock host.
func captionMock(t *testing.T, srt, vtt string) *mockUpstream {
	t.Helper()
	mock := newMockUpstream(t)
	mock.handlers["/captions.srt"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(srt))
	}
	mock.handlers["/captions.vtt"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vtt))
	}
	video := map[string]any{"duration": 1405}
	if srt != "" {
		video["caption"] = mock.server.URL + "/captions.srt"
	}
	if vtt != "" {
		video["vtt_caption"] = mock.server.URL + "/captions.vtt"
	}
	mock.OK("/pc/bauhinia/pc/article/info", map[string]any{
		"article_info": map[string]any{
			"title": "第一讲",
			"video": []any{video},
		},
		"class_info": map[string]any{"name": "某门课"},
	})
	return mock
}

const srtTrack = "1\n00:00:00,000 --> 00:00:02,000\n开场白。\n"

func TestArticleCaptions_ReturnsTheTrackAndItsSize(t *testing.T) {
	got := runAuthed(t, captionMock(t, srtTrack, ""), "article-captions", "a1")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if format, _ := data["format"].(string); format != "srt" {
		t.Errorf("format = %q", format)
	}
	if text, _ := data["text"].(string); !strings.Contains(text, "开场白。") {
		t.Errorf("text = %q", text)
	}
	// Counted in characters, so a Chinese transcript is not reported as three
	// times its length.
	if chars, _ := data["chars"].(float64); int(chars) != len([]rune(srtTrack)) {
		t.Errorf("chars = %v, want %d", data["chars"], len([]rune(srtTrack)))
	}
}

// The two tracks are separate upstream, so asking for one the publisher did not
// make says so rather than converting the other.
func TestArticleCaptions_SaysWhenTheAskedForTrackIsAbsent(t *testing.T) {
	got := runAuthed(t, captionMock(t, srtTrack, ""),
		"article-captions", "a1", "--format-track", "vtt")
	if code := got.ErrorCode(t); code != "E_NOT_FOUND" {
		t.Errorf("code = %s, want E_NOT_FOUND", code)
	}
}

func TestArticleCaptions_RejectsAFormatItDoesNotServe(t *testing.T) {
	got := runAuthed(t, captionMock(t, srtTrack, ""),
		"article-captions", "a1", "--format-track", "ass")
	if code := got.ErrorCode(t); code != "E_VALIDATION" {
		t.Errorf("code = %s, want E_VALIDATION", code)
	}
}

func TestArticleCaptions_SaysWhenTheArticleHasNoVideo(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/pc/bauhinia/pc/article/info", map[string]any{
		"article_info": map[string]any{"title": "纯文字"},
	})
	got := runAuthed(t, mock, "article-captions", "a1")
	if code := got.ErrorCode(t); code != "E_NOT_FOUND" {
		t.Errorf("code = %s, want E_NOT_FOUND", code)
	}
}

// ebookMock serves an ebook the account may or may not read.
func ebookMock(t *testing.T, owned bool) *mockUpstream {
	t.Helper()
	mock := newMockUpstream(t)
	buy := 0
	if owned {
		buy = 1
	}
	mock.OK("/pc/ebook2/v1/pc/detail", map[string]any{
		"is_buy":          buy,
		"is_vip_book":     false,
		"operating_title": "一本书",
	})
	mock.OK("/api/pc/ebook2/v1/pc/read/token", map[string]any{"token": "tok"})
	mock.OK("/ebk_web/v1/get_book_info", map[string]any{
		"bookInfo": map[string]any{
			"toc": []any{
				map[string]any{"text": "序言", "level": 1, "playOrder": 1,
					"href": "Text/Chapter_1_1.xhtml#top"},
				map[string]any{"text": "第一章", "level": 1, "playOrder": 2,
					"href": "Text/Chapter_1_2.xhtml"},
			},
			"orders": []any{
				map[string]any{"chapterId": "Chapter_1_1", "pathInEpub": "Text/Chapter_1_1.xhtml"},
			},
		},
	})
	return mock
}

func TestEbookChapters_ListsTheContentsWithUsableChapterIDs(t *testing.T) {
	got := runAuthed(t, ebookMock(t, true), "ebook-chapters", "e1")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	toc, _ := data["toc"].([]any)
	if len(toc) != 2 {
		t.Fatalf("toc has %d entries, want 2", len(toc))
	}
	first, _ := toc[0].(map[string]any)
	// The anchor and the .xhtml suffix are not part of the id the page endpoint
	// takes, so they are stripped.
	if id, _ := first["chapter_id"].(string); id != "Chapter_1_1" {
		t.Errorf("chapter_id = %q, want Chapter_1_1", id)
	}
}

// Entitlement is the account's. An unowned book is refused rather than
// half-fetched.
func TestEbookCommands_RefuseAnUnownedBook(t *testing.T) {
	for _, args := range [][]string{
		{"ebook-chapters", "e1"},
		{"ebook-read", "e1", "--chapter", "Chapter_1_1"},
	} {
		got := runAuthed(t, ebookMock(t, false), args...)
		if code := got.ErrorCode(t); code != "E_FORBIDDEN" {
			t.Errorf("%v: code = %s, want E_FORBIDDEN", args, code)
		}
	}
}

func TestEbookRead_NeedsAChapterToRead(t *testing.T) {
	got := runAuthed(t, ebookMock(t, true), "ebook-read", "e1")
	if code := got.ErrorCode(t); code != "E_VALIDATION" {
		t.Errorf("code = %s, want E_VALIDATION", code)
	}
}

// A title substring resolves against the book's own contents, so a caller can
// name a chapter without first fetching the whole table.
func TestEbookRead_ResolvesAChapterByTitle(t *testing.T) {
	mock := ebookMock(t, true)
	mock.handlers["/ebk_web_go/v2/get_pages"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"h": map[string]any{"c": 0, "e": ""},
			"c": map[string]any{"pages": []any{}, "is_end": true},
		})
	}
	got := runAuthed(t, mock, "ebook-read", "e1", "--title", "序言")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if id, _ := data["chapter_id"].(string); id != "Chapter_1_1" {
		t.Errorf("chapter_id = %q, want Chapter_1_1", id)
	}
	if title, _ := data["title"].(string); title != "序言" {
		t.Errorf("title = %q", title)
	}
}

// An audiobook the account may not play is refused, and nothing is written.
func TestAudiobookMedia_RefusesAnUnauthorizedStream(t *testing.T) {
	mock := newMockUpstream(t)
	mock.OK("/pc/odob/pc/audio/detail/alias", map[string]any{
		"audio_detail": map[string]any{"has_play_auth": false},
	})
	mock.OK("/pc/odob/pc/audio/detail", map[string]any{
		"detail": map[string]any{"has_play_auth": false},
	})
	got := runAuthed(t, mock, "audiobook-media", "t1")
	if code := got.ErrorCode(t); code != "E_FORBIDDEN" {
		t.Errorf("code = %s, want E_FORBIDDEN", code)
	}
}

// dailyMock serves one owned course with one article in it.
func dailyMock(t *testing.T) *mockUpstream {
	t.Helper()
	mock := newMockUpstream(t)
	mock.OK("/api/hades/v2/product/list", map[string]any{
		"list":    []any{map[string]any{"enid": "c1", "title": "某门课"}},
		"is_more": false,
	})
	mock.OK("/api/pc/bauhinia/pc/class/purchase/article_list", map[string]any{
		"article_list": []any{
			map[string]any{"enid": "a1", "title": "旧文章", "id": 1, "order_num": 1},
		},
	})
	return mock
}

// The first run over a course takes a baseline instead of reporting the whole
// back catalogue as today's news, and says that it did.
func TestDaily_TakesABaselineOnFirstRun(t *testing.T) {
	got := runAuthed(t, dailyMock(t), "daily")
	if got.Exit != 0 {
		t.Fatalf("exit = %d: %s", got.Exit, got.Stdout)
	}
	data := got.Data(t)
	if baseline, _ := data["baseline_created"].(bool); !baseline {
		t.Error("baseline_created = false on a first run")
	}
	if count, _ := data["update_count"].(float64); count != 0 {
		t.Errorf("update_count = %v, want 0 on a first run", data["update_count"])
	}
	if path, _ := data["checkpoint"].(string); path == "" {
		t.Error("the checkpoint path was not reported")
	}
}

// Asking for the back catalogue explicitly gets it.
func TestDaily_ReportsExistingArticlesWhenAsked(t *testing.T) {
	got := runAuthed(t, dailyMock(t), "daily", "--include-existing")
	data := got.Data(t)
	if count, _ := data["update_count"].(float64); count != 1 {
		t.Errorf("update_count = %v, want 1", data["update_count"])
	}
}
