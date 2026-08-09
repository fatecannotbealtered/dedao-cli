// Package output implements the CLI's machine-readable stdout contract.
//
// stdout carries exactly one JSON document per invocation; progress and
// human-readable error text go to stderr. Success and failure share one
// envelope shape so an agent only has to branch on `ok` (CLI-SPEC §3-§4).
package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fatecannotbealtered/dedao-cli/internal/contract"
)

const (
	FormatJSON = "json"
	FormatText = "text"
	FormatRaw  = "raw"
)

// ErrNormalization identifies a contract failure caused by two upstream keys
// collapsing onto the same canonical output key (or by an unusable key).
var ErrNormalization = errors.New("output normalization failed")

// Hints is the actionable next step per error code. Agents branch on
// `error.code`; the hint is for the human reading stderr and rides along in
// `error.details.hint`.
var Hints = map[string]string{
	"E_AUTH":           "Session missing or expired. Run 'dedao-cli login' and have the user scan the QR code.",
	"E_FORBIDDEN":      "The account is not entitled to this content.",
	"E_NOT_FOUND":      "Verify the enid/id from a fresh library, search, or list result.",
	"E_VALIDATION":     "Check command arguments.",
	"E_USAGE":          "Check command arguments.",
	"E_CONFLICT":       "State changed since the preview. Re-read the resource and retry.",
	"E_NETWORK":        "Check network connectivity and proxy settings.",
	"E_RATE_LIMITED":   "Too many requests. Back off before retrying.",
	"E_SERVER":         "Dedao returned a server error. Retry later.",
	"E_TIMEOUT":        "The request timed out. Back off before retrying.",
	"E_HUMAN_REQUIRED": "A person must scan the login QR code. Relay the message and the resume command; do not retry automatically.",
	"E_INTEGRITY":      "Release integrity verification failed; do not retry. Report a possible supply-chain issue.",
	"E_IO":             "Local filesystem failure. Fix the environment, then retry.",
	"E_INTERRUPTED":    "Cancelled by signal or user. Nothing is left half-applied; re-run the command.",
	"E_UNKNOWN":        "Inspect error.details for more context.",
}

// Options controls one command invocation's output.
type Options struct {
	Format    string
	Compact   bool
	Fields    []string
	StartedAt time.Time
	Notices   []map[string]any
	// Untrusted names the payload fields carrying externally-controlled
	// content. It is stamped onto `data._untrusted` so an agent can tell which
	// values are data rather than instructions (SEC-SPEC §2).
	Untrusted []string
}

// Printer writes exactly one command result to stdout.
type Printer struct {
	out     io.Writer
	options Options
}

func NewPrinter(out io.Writer, options Options) *Printer {
	if options.Format == "" {
		options.Format = FormatJSON
	}
	return &Printer{out: out, options: options}
}

// meta is emitted on every response, success or failure. duration_ms is never
// omitted: 0 is a legal value and an agent must always be able to read it.
type meta struct {
	DurationMS int64            `json:"duration_ms"`
	Notices    []map[string]any `json:"notices,omitempty"`
}

type successEnvelope struct {
	OK            bool   `json:"ok"`
	SchemaVersion string `json:"schema_version"`
	Data          any    `json:"data"`
	Meta          meta   `json:"meta"`
}

type errorObject struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	Retryable bool           `json:"retryable"`
}

type errorEnvelope struct {
	OK            bool        `json:"ok"`
	SchemaVersion string      `json:"schema_version"`
	Error         errorObject `json:"error"`
	Meta          meta        `json:"meta"`
}

// CLIError is a stable E_* error carrying its canonical exit and retry semantics.
type CLIError struct {
	Code    string
	Message string
	Details map[string]any
	Cause   error
}

func NewError(code, message string, details map[string]any) *CLIError {
	if _, ok := contract.Codes[code]; !ok {
		code = "E_UNKNOWN"
	}
	if details == nil {
		details = map[string]any{}
	}
	if hint, ok := Hints[code]; ok {
		if _, exists := details["hint"]; !exists {
			details["hint"] = hint
		}
	}
	return &CLIError{Code: code, Message: message, Details: details}
}

func WrapError(code, message string, cause error, details map[string]any) *CLIError {
	err := NewError(code, message, details)
	err.Cause = cause
	return err
}

func (e *CLIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *CLIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *CLIError) ExitCode() int {
	if e == nil {
		return 0
	}
	return contract.ExitFor(e.Code)
}

func (p *Printer) commandMeta() meta {
	duration := int64(0)
	if !p.options.StartedAt.IsZero() {
		duration = time.Since(p.options.StartedAt).Milliseconds()
	}
	return meta{DurationMS: duration, Notices: p.options.Notices}
}

func (p *Printer) Success(data any) error {
	if p.options.Format == FormatRaw {
		return errors.New("raw output requires Raw()")
	}
	normalized, err := normalizeOutputFields(data)
	if err != nil {
		return err
	}
	annotated, err := annotateUntrusted(normalized, p.options.Untrusted)
	if err != nil {
		return err
	}
	projected, err := projectFields(annotated, p.options.Fields)
	if err != nil {
		return err
	}
	if p.options.Format == FormatText {
		return p.writeJSON(projected)
	}
	return p.writeJSON(successEnvelope{
		OK:            true,
		SchemaVersion: contract.SchemaVersion,
		Data:          projected,
		Meta:          p.commandMeta(),
	})
}

func (p *Printer) Failure(cliErr *CLIError) error {
	if cliErr == nil {
		cliErr = NewError("E_UNKNOWN", "unknown error", nil)
	}
	if p.options.Format == FormatText {
		_, err := fmt.Fprintf(p.out, "%s: %s\n", cliErr.Code, cliErr.Message)
		return err
	}
	normalized, err := normalizeOutputFields(cliErr.Details)
	if err != nil {
		cliErr = NewError("E_UNKNOWN", "failed to normalize error details",
			map[string]any{"normalization_error": err.Error()})
		normalized = cliErr.Details
	}
	details, _ := normalized.(map[string]any)
	return p.writeJSON(errorEnvelope{
		OK:            false,
		SchemaVersion: contract.SchemaVersion,
		Error: errorObject{
			Code:      cliErr.Code,
			Message:   cliErr.Message,
			Details:   details,
			Retryable: contract.Retryable(cliErr.Code),
		},
		Meta: p.commandMeta(),
	})
}

// normalizeOutputFields enforces the fleet-wide JSON conventions at the last
// boundary before stdout: object keys are snake_case, IDs are strings, and
// semantic timestamps are RFC3339 UTC. It rejects key collisions instead of
// allowing map iteration order to decide which value survives.
func normalizeOutputFields(data any) (any, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return normalizeOutputValue(value, "$")
}

func normalizeOutputValue(value any, path string) (any, error) {
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		normalized := make(map[string]any, len(current))
		origins := make(map[string]string, len(current))
		for _, original := range keys {
			key := snakeCaseKey(original)
			if key == "" {
				return nil, fmt.Errorf("%w: output key at %s cannot be normalized: %q",
					ErrNormalization, path, original)
			}
			child, err := normalizeOutputValue(current[original], path+"."+key)
			if err != nil {
				return nil, err
			}
			if isIDField(key) {
				child = stringifyIDValue(child)
			}
			key, child = normalizeSemanticTime(key, child)
			if previous, exists := origins[key]; exists {
				return nil, fmt.Errorf("%w: output key collision at %s: %q and %q both normalize to %q",
					ErrNormalization, path, previous, original, key)
			}
			origins[key] = original
			normalized[key] = child
		}
		return normalized, nil
	case []any:
		for index, child := range current {
			normalized, err := normalizeOutputValue(child, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			current[index] = normalized
		}
		return current, nil
	default:
		return value, nil
	}
}

func snakeCaseKey(key string) string {
	// reference.error_codes is keyed by the canonical E_* enum names from
	// contract.json; those names are values encoded as object keys, not field
	// names, and must remain byte-identical for portable error handling.
	if strings.HasPrefix(key, "E_") && strings.ToUpper(key) == key {
		return key
	}
	runes := []rune(key)
	out := make([]rune, 0, len(runes)+4)
	for index, current := range runes {
		if current == '_' {
			if len(out) == 0 || out[len(out)-1] != '_' {
				out = append(out, '_')
			}
			continue
		}
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			if len(out) > 0 && out[len(out)-1] != '_' {
				out = append(out, '_')
			}
			continue
		}
		if unicode.IsUpper(current) {
			previousLowerOrDigit := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
			nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			previousUpper := index > 0 && unicode.IsUpper(runes[index-1])
			if len(out) > 0 && out[len(out)-1] != '_' && (previousLowerOrDigit || previousUpper && nextLower) {
				out = append(out, '_')
			}
		}
		out = append(out, unicode.ToLower(current))
	}
	for len(out) > 0 && out[len(out)-1] == '_' {
		out = out[:len(out)-1]
	}
	return string(out)
}

func isIDField(key string) bool {
	for _, part := range strings.Split(key, "_") {
		switch part {
		case "id", "ids", "enid", "enids", "uid", "uids", "pid", "pids", "sid", "sids", "uuid", "uuids":
			return true
		}
	}
	return false
}

func stringifyIDValue(value any) any {
	switch current := value.(type) {
	case []any:
		for index, item := range current {
			current[index] = stringifyIDValue(item)
		}
		return current
	case json.Number:
		return current.String()
	case int:
		return strconv.FormatInt(int64(current), 10)
	case int8:
		return strconv.FormatInt(int64(current), 10)
	case int16:
		return strconv.FormatInt(int64(current), 10)
	case int32:
		return strconv.FormatInt(int64(current), 10)
	case int64:
		return strconv.FormatInt(current, 10)
	case uint:
		return strconv.FormatUint(uint64(current), 10)
	case uint8:
		return strconv.FormatUint(uint64(current), 10)
	case uint16:
		return strconv.FormatUint(uint64(current), 10)
	case uint32:
		return strconv.FormatUint(uint64(current), 10)
	case uint64:
		return strconv.FormatUint(current, 10)
	case float32:
		value := float64(current)
		if math.Trunc(value) == value {
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
	case float64:
		if math.Trunc(current) == current {
			return strconv.FormatFloat(current, 'f', -1, 64)
		}
	}
	return value
}

func normalizeSemanticTime(key string, value any) (string, any) {
	if !isSemanticTimeField(key) || value == nil {
		return key, value
	}
	if timestamp, ok := semanticTimestamp(value); ok {
		return canonicalTimeKey(key), timestamp
	}
	return timeLabelKey(key), value
}

func isSemanticTimeField(key string) bool {
	switch key {
	case "surplus_time", "elapsed_time", "interval_time", "response_time":
		return false
	case "time", "timestamp", "paytime", "time_now":
		return true
	}
	return strings.HasSuffix(key, "_at") || strings.HasSuffix(key, "_at_ms") ||
		strings.HasSuffix(key, "_at_unix") || strings.HasSuffix(key, "_time")
}

func canonicalTimeKey(key string) string {
	if strings.HasSuffix(key, "_at_ms") {
		return strings.TrimSuffix(key, "_ms")
	}
	if strings.HasSuffix(key, "_at_unix") {
		return strings.TrimSuffix(key, "_unix")
	}
	return key
}

func timeLabelKey(key string) string {
	for _, suffix := range []string{"_at_ms", "_at_unix", "_at", "_time"} {
		if strings.HasSuffix(key, suffix) {
			base := strings.TrimSuffix(key, suffix)
			if base != "" {
				return base + "_label"
			}
		}
	}
	if key == "time_now" {
		return "now_label"
	}
	return key + "_label"
}

func semanticTimestamp(value any) (string, bool) {
	text := strings.TrimSpace(fmt.Sprint(value))
	if number, ok := value.(json.Number); ok {
		text = number.String()
	}
	if epoch, err := strconv.ParseInt(text, 10, 64); err == nil && epoch > 0 {
		switch len(text) {
		case 10:
			return time.Unix(epoch, 0).UTC().Format(time.RFC3339), true
		case 13:
			return time.UnixMilli(epoch).UTC().Format(time.RFC3339), true
		}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return parsed.UTC().Format(time.RFC3339Nano), true
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006年01月02日"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC().Format(time.RFC3339), true
		}
	}
	china := time.FixedZone("China Standard Time", 8*60*60)
	for _, layout := range []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04",
		"2006/01/02 15:04:05", "2006/01/02 15:04",
		"2006年01月02日 15:04:05", "2006年01月02日 15:04",
	} {
		if parsed, err := time.ParseInLocation(layout, text, china); err == nil {
			return parsed.UTC().Format(time.RFC3339), true
		}
	}
	return "", false
}

func (p *Printer) Raw(data []byte) error {
	if p.options.Format != FormatRaw {
		return errors.New("Raw() requires --format raw")
	}
	_, err := p.out.Write(data)
	return err
}

func (p *Printer) writeJSON(value any) error {
	var (
		encoded []byte
		err     error
	)
	if p.options.Compact {
		encoded, err = json.Marshal(value)
	} else {
		encoded, err = json.MarshalIndent(value, "", "  ")
	}
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = p.out.Write(encoded)
	return err
}

// annotateUntrusted stamps the declared untrusted field list onto the payload.
//
// SEC-SPEC §2 wants `_untrusted` to *name* the externally-controlled fields, not
// merely assert that some exist: an agent has to know which values to quarantine.
// The list comes from the command's declared output schema, so what a caller
// reads here and what `reference` promises cannot drift apart.
func annotateUntrusted(data any, fields []string) (any, error) {
	if len(fields) == 0 {
		return data, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		// A non-object payload has no place to carry the marker. That is a
		// schema declaration bug rather than a runtime one, and a guard test
		// catches it; returning the payload unchanged keeps the command usable.
		return data, nil
	}
	object["_untrusted"] = fields
	return object, nil
}

func projectFields(data any, fields []string) (any, error) {
	if len(fields) == 0 {
		return data, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("--fields requires an object result: %w", err)
	}
	projected := make(map[string]any, len(fields))
	for _, rawField := range fields {
		field := strings.TrimSpace(rawField)
		if field == "" {
			continue
		}
		value, ok := object[field]
		if !ok {
			return nil, fmt.Errorf("unknown output field %q", field)
		}
		projected[field] = value
	}
	if marked, ok := object["_untrusted"].([]any); ok {
		kept := make([]any, 0, len(marked))
		for _, value := range marked {
			field, ok := value.(string)
			if !ok {
				continue
			}
			if _, present := projected[field]; present {
				kept = append(kept, field)
			}
		}
		if len(kept) > 0 {
			projected["_untrusted"] = kept
		} else {
			delete(projected, "_untrusted")
		}
	}
	return projected, nil
}

// CodeForStatus maps an upstream HTTP status onto a stable error code. One
// function owns this mapping so the status -> code -> exit contract cannot
// drift between the transport and command layers (CLI-SPEC §6).
func CodeForStatus(status int) string {
	switch status {
	case 401:
		return "E_AUTH"
	case 403:
		return "E_FORBIDDEN"
	case 404:
		return "E_NOT_FOUND"
	case 408:
		return "E_TIMEOUT"
	case 409:
		return "E_CONFLICT"
	case 429:
		return "E_RATE_LIMITED"
	}
	switch {
	case status >= 500 && status <= 599:
		return "E_SERVER"
	case status >= 400 && status <= 499:
		return "E_VALIDATION"
	default:
		return "E_SERVER"
	}
}
