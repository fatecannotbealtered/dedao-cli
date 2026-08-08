package dedao

import (
	"net/url"
	"strings"
)

// Markers that make any key sensitive regardless of the explicit sets: playback
// tokens, signatures, and DRM material must never reach stdout (SEC-SPEC §2).
var sensitiveMarkers = []string{
	"token", "secret", "signature", "credential", "drm", "m3u8", "bitrate",
}

// IsSensitiveName reports whether a response key or query parameter must be
// dropped before anything is emitted.
func IsSensitiveName(name string) bool {
	folded := strings.ToLower(name)
	if _, ok := sensitiveKeys[folded]; ok {
		return true
	}
	if _, ok := sensitiveQueryKeys[folded]; ok {
		return true
	}
	if _, ok := sensitiveMediaKeys[folded]; ok {
		return true
	}
	for _, marker := range sensitiveMarkers {
		if strings.Contains(folded, marker) {
			return true
		}
	}
	return false
}

// SanitizeURL strips sensitive query parameters while leaving everything else
// byte-identical. Non-http(s) values are returned untouched.
func SanitizeURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return value
	}
	query := parsed.Query()
	for key := range query {
		if IsSensitiveName(key) {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// Sanitize walks a decoded payload, dropping sensitive keys and scrubbing every
// string that parses as a url. Applied to every response before it is printed.
func Sanitize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if IsSensitiveName(key) {
				continue
			}
			out[key] = Sanitize(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, Sanitize(item))
		}
		return out
	case string:
		return SanitizeURL(typed)
	default:
		return value
	}
}

// SafeAudiobookMetadata filters audiobook payloads down to an explicit
// allowlist. Audiobook responses carry playback material the account is not
// entitled to redistribute, so this is a whitelist rather than a denylist:
// unknown upstream fields are dropped instead of leaking by default.
func SafeAudiobookMetadata(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any)
		for key, item := range typed {
			if _, ok := audiobookSafeFields[key]; !ok {
				continue
			}
			out[key] = SafeAudiobookMetadata(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, SafeAudiobookMetadata(item))
		}
		return out
	default:
		return Sanitize(value)
	}
}
