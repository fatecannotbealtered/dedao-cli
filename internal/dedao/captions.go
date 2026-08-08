package dedao

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Course videos ship a caption track: plain text, published by 得到 alongside
// the video, and readable by an account that owns the course.
//
// This reads the caption and nothing else. The video's own media urls are never
// emitted, because a caption is the transcript a subscriber is entitled to read
// while a media url is a redistributable copy of the recording.

// CaptionsResult is one article's caption track.
type CaptionsResult struct {
	ArticleEnid string `json:"article_enid"`
	Title       any    `json:"title"`
	Format      string `json:"format"`
	Duration    any    `json:"duration"`
	Text        string `json:"text"`
	Chars       int    `json:"chars"`
	Path        string `json:"path,omitempty"`
}

// ArticleCaptions downloads one article's video captions.
//
// `format` is srt or vtt: the two are separate tracks upstream, not one
// converted to the other, so the caller picks which the publisher made.
func (c *Client) ArticleCaptions(
	ctx context.Context,
	articleEnid, format, output string,
) (*CaptionsResult, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "srt" && format != "vtt" {
		return nil, &APIError{StatusCode: 400, Message: "caption format must be srt or vtt"}
	}
	info, err := c.ArticleInfo(ctx, articleEnid)
	if err != nil {
		return nil, err
	}
	articleInfo, _ := info["article_info"].(map[string]any)
	if articleInfo == nil {
		articleInfo = map[string]any{}
	}
	videos := asList(articleInfo["video"])
	if len(videos) == 0 {
		return nil, &APIError{
			StatusCode: 404,
			Message:    "this article carries no video, so it has no captions",
		}
	}
	video, _ := videos[0].(map[string]any)
	if video == nil {
		return nil, &APIError{Message: "the article's video metadata changed shape"}
	}

	field := "caption"
	if format == "vtt" {
		field = "vtt_caption"
	}
	captionURL := asString(video[field])
	if captionURL == "" {
		return nil, &APIError{
			StatusCode: 404,
			Message:    "this article publishes no " + format + " caption",
		}
	}

	raw, err := c.fetchCaption(ctx, captionURL, format)
	if err != nil {
		return nil, err
	}
	// The tracks are served with a BOM often enough that leaving it in would
	// put a stray character at the head of the first cue.
	text := strings.TrimPrefix(string(raw), "\ufeff")

	title := articleInfo["title"]
	if asString(title) == "" {
		if class, ok := info["class_info"].(map[string]any); ok {
			title = class["name"]
		}
	}
	result := &CaptionsResult{
		ArticleEnid: articleEnid,
		Title:       title,
		Format:      format,
		Duration:    video["duration"],
		Text:        text,
		Chars:       len([]rune(text)),
	}
	if strings.TrimSpace(output) != "" {
		path, err := writeCaption(output, text)
		if err != nil {
			return nil, err
		}
		result.Path = path
	}
	return result, nil
}

// fetchCaption downloads the caption track itself.
//
// The track lives on 得到's media host rather than behind the API, so it is
// fetched directly; the session still travels with it, because the host checks
// entitlement the same way.
func (c *Client) fetchCaption(ctx context.Context, captionURL, format string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, captionURL, nil)
	if err != nil {
		return nil, &APIError{
			StatusCode: 400,
			Message:    "the article's caption url is not one this build can fetch",
		}
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, &APIError{Message: "could not download the " + format + " caption"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, &APIError{
			StatusCode: response.StatusCode,
			Message:    "could not download the " + format + " caption",
		}
	}
	return io.ReadAll(io.LimitReader(response.Body, maxCaptionBytes))
}

// maxCaptionBytes caps a caption download. A transcript of a long lecture is a
// few hundred kilobytes; anything far past that is not a caption track.
const maxCaptionBytes = 8 << 20

// writeCaption saves the track next to wherever the caller asked.
func writeCaption(output, text string) (string, error) {
	path, err := filepath.Abs(output)
	if err != nil {
		return "", &APIError{StatusCode: 400, Message: "the output path could not be resolved"}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", &APIError{Message: "could not create the output directory"}
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", &APIError{Message: "could not write the caption file"}
	}
	return path, nil
}
