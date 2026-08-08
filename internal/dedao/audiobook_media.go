package dedao

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// An audiobook this account may play can be saved locally, the same way the
// player streams it. What is never returned is the play url or the stream key:
// those are what turn one listener's authorization into a shareable copy, so
// they stay inside this function and out of the payload.
//
// `has_play_auth` is the account's answer and is required. Nothing here tries a
// stream the account was not told it may play.

// hlsSegmentKeyURI and hlsSegmentIV read the key line of a playlist.
var (
	hlsKeyURI  = regexp.MustCompile(`URI="([^"]+)"`)
	hlsKeyIV   = regexp.MustCompile(`IV=0x([0-9A-Fa-f]+)`)
	unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]+`)
)

// maxSegmentBytes caps one stream segment. HLS segments are seconds long; a
// segment far past this is not one.
const maxSegmentBytes = 64 << 20

// safeMediaBasename turns a title into a filename that is safe on every
// platform this tool runs on.
func safeMediaBasename(title, suffix string) string {
	cleaned := strings.Trim(unsafeName.ReplaceAllString(title, "_"), " .")
	if cleaned == "" {
		cleaned = "media"
	}
	if runes := []rune(cleaned); len(runes) > 80 {
		cleaned = string(runes[:80])
	}
	return cleaned + suffix
}

// aes128Decrypt undoes one segment's encryption.
func aes128Decrypt(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, &APIError{Message: "the stream key could not be used"}
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, &APIError{Message: "a stream segment was not a whole number of blocks"}
	}
	plaintext := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, data)
	pad := int(plaintext[len(plaintext)-1])
	if pad >= 1 && pad <= aes.BlockSize && pad <= len(plaintext) {
		valid := true
		for _, value := range plaintext[len(plaintext)-pad:] {
			if int(value) != pad {
				valid = false
				break
			}
		}
		if valid {
			plaintext = plaintext[:len(plaintext)-pad]
		}
	}
	return plaintext, nil
}

// fetchMedia performs one authenticated media fetch.
func (c *Client) fetchMedia(ctx context.Context, target string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, &APIError{StatusCode: 400, Message: "the stream url could not be used"}
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := c.http.Do(request)
	if err != nil {
		// The url is deliberately absent from the message: it carries the
		// account's playback authorization.
		return nil, &APIError{Message: "the audiobook stream could not be read"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, &APIError{
			StatusCode: response.StatusCode,
			Message:    "the audiobook stream could not be read",
		}
	}
	return io.ReadAll(io.LimitReader(response.Body, limit))
}

// resolveURL joins a playlist-relative reference against its base.
func resolveURL(base, reference string) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	target, err := parsed.Parse(reference)
	if err != nil {
		return ""
	}
	return target.String()
}

// mediaPlaylist fetches a playlist, following one master-to-variant hop.
//
// A master playlist lists variant streams rather than segments; without the hop
// the caller would try to download playlist urls as if they were audio.
func (c *Client) mediaPlaylist(ctx context.Context, playlistURL string) (string, string, error) {
	raw, err := c.fetchMedia(ctx, playlistURL, maxSegmentBytes)
	if err != nil {
		return "", "", err
	}
	text := string(raw)
	master := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#EXT-X-STREAM-INF") {
			master = true
			break
		}
	}
	if !master {
		return text, playlistURL, nil
	}
	variant := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			variant = line
			break
		}
	}
	if variant == "" {
		return "", "", &APIError{Message: "the audiobook stream lists no playable variant"}
	}
	variantURL := resolveURL(playlistURL, variant)
	if variantURL == "" {
		return "", "", &APIError{Message: "the audiobook stream lists no playable variant"}
	}
	raw, err = c.fetchMedia(ctx, variantURL, maxSegmentBytes)
	if err != nil {
		return "", "", err
	}
	return string(raw), variantURL, nil
}

// downloadHLS saves an AES-128 HLS stream to one local file.
func (c *Client) downloadHLS(ctx context.Context, playlistURL, destination string) error {
	text, base, err := c.mediaPlaylist(ctx, playlistURL)
	if err != nil {
		return err
	}

	type segment struct {
		url      string
		sequence int
	}
	var segments []segment
	keyURI, keyIV := "", []byte(nil)
	sequence := 0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			if parsed, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:")); err == nil {
				sequence = parsed
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			if match := hlsKeyURI.FindStringSubmatch(line); match != nil {
				keyURI = resolveURL(base, match[1])
			}
			if match := hlsKeyIV.FindStringSubmatch(line); match != nil {
				if decoded, err := hex.DecodeString(match[1]); err == nil {
					keyIV = decoded
				}
			}
		case strings.HasPrefix(line, "#"):
		default:
			if target := resolveURL(base, line); target != "" {
				segments = append(segments, segment{url: target, sequence: sequence})
			}
			sequence++
		}
	}
	if len(segments) == 0 {
		return &APIError{Message: "the audiobook stream carries no segments"}
	}

	var key []byte
	if keyURI != "" {
		key, err = c.fetchMedia(ctx, keyURI, 1024)
		if err != nil {
			return err
		}
		if len(key) != aes.BlockSize {
			return &APIError{Message: "the audiobook stream key was not the expected length"}
		}
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return &APIError{Message: "could not create the output directory"}
	}
	file, err := os.Create(destination)
	if err != nil {
		return &APIError{Message: "could not create the output file"}
	}
	defer func() { _ = file.Close() }()
	for _, item := range segments {
		data, err := c.fetchMedia(ctx, item.url, maxSegmentBytes)
		if err != nil {
			return err
		}
		if key != nil {
			iv := keyIV
			if iv == nil {
				// Without an explicit IV, HLS uses the segment's sequence
				// number as a big-endian 128-bit value.
				iv = make([]byte, aes.BlockSize)
				binary.BigEndian.PutUint64(iv[8:], uint64(item.sequence))
			}
			if data, err = aes128Decrypt(data, key, iv); err != nil {
				return err
			}
		}
		if _, err := file.Write(data); err != nil {
			return &APIError{Message: "could not write the audiobook file"}
		}
	}
	return nil
}

// audiobookAliasRaw reads an audiobook's play record before it is redacted.
//
// The sanitized `audiobook-alias` command deliberately strips the play url;
// this reads the same record unredacted so the stream can be fetched, and it
// stays inside this file.
func (c *Client) audiobookAliasRaw(ctx context.Context, aliasID string) (map[string]any, error) {
	value, err := c.requestJSONUnsanitized(ctx, http.MethodPost,
		"/pc/odob/pc/audio/detail/alias", nil,
		map[string]any{"alias_id": aliasID}, "")
	if err != nil {
		return nil, err
	}
	raw, _ := value.(map[string]any)
	if raw == nil {
		raw = map[string]any{}
	}
	return raw, nil
}

// AudiobookMedia saves an authorized audiobook to a local file.
func (c *Client) AudiobookMedia(
	ctx context.Context,
	topicOrAlias, output string,
) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}

	// The argument is either an alias id or a topic id; the alias endpoint is
	// tried first because it is the one that carries playback authorization.
	aliasID := topicOrAlias
	raw, err := c.audiobookAliasRaw(ctx, aliasID)
	if err != nil {
		raw = nil
	}
	if detail, _ := raw["audio_detail"].(map[string]any); raw == nil ||
		!truthyValue(detail["has_play_auth"]) {
		topic, err := c.AudiobookInfo(ctx, topicOrAlias)
		if err != nil {
			return nil, err
		}
		info, _ := topic.(map[string]any)
		topicDetail, _ := info["detail"].(map[string]any)
		if topicDetail == nil {
			return nil, &APIError{StatusCode: 404, Message: "that audiobook has no detail record"}
		}
		if !truthyValue(topicDetail["has_play_auth"]) {
			return nil, &APIError{
				StatusCode: 403,
				Message:    "this account is not authorized to play that audiobook",
			}
		}
		audioID := asString(topicDetail["audio_id"])
		if audioID == "" {
			return nil, &APIError{Message: "that audiobook names no audio track"}
		}
		aliasID = audioID
		if raw, err = c.audiobookAliasRaw(ctx, aliasID); err != nil {
			return nil, err
		}
	}

	detail, _ := raw["audio_detail"].(map[string]any)
	if !truthyValue(detail["has_play_auth"]) {
		return nil, &APIError{
			StatusCode: 403,
			Message:    "this account is not authorized to play that audiobook",
		}
	}
	playURL := asString(detail["mp3_play_url"])
	if playURL == "" {
		return nil, &APIError{Message: "that audiobook has no playable stream"}
	}

	title := asString(detail["title"])
	destination := strings.TrimSpace(output)
	if destination == "" {
		name := title
		if name == "" {
			name = aliasID
		}
		destination = filepath.Join(c.stateDir, "media", safeMediaBasename(name, ".ts"))
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return nil, &APIError{StatusCode: 400, Message: "the output path could not be resolved"}
	}
	if err := c.downloadHLS(ctx, playURL, destination); err != nil {
		return nil, err
	}
	info, err := os.Stat(destination)
	if err != nil {
		return nil, &APIError{Message: "the audiobook file could not be measured after writing"}
	}
	return map[string]any{
		"alias_id": aliasID,
		"title":    emptyToNil(title),
		"path":     destination,
		"bytes":    info.Size(),
		"format":   "mpegts",
		"note":     "authorized stream saved locally; the play url and key are not returned",
	}, nil
}
