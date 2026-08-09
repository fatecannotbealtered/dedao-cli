package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrinterSuccess_NormalizesIDFieldsRecursively(t *testing.T) {
	var out bytes.Buffer
	printer := NewPrinter(&out, Options{Format: FormatJSON, Compact: true})
	err := printer.Success(map[string]any{
		"id": 42,
		"nested": map[string]any{
			"article_id": 7,
			"valid":      true,
		},
		"items":     []any{map[string]any{"enid": 123}},
		"topic_ids": []any{1, 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	data, _ := envelope["data"].(map[string]any)
	if data["id"] != "42" {
		t.Errorf("id = %#v, want string 42", data["id"])
	}
	nested, _ := data["nested"].(map[string]any)
	if nested["article_id"] != "7" || nested["valid"] != true {
		t.Errorf("nested = %#v, want string article_id and boolean valid", nested)
	}
	items, _ := data["items"].([]any)
	first, _ := items[0].(map[string]any)
	if first["enid"] != "123" {
		t.Errorf("items[0].enid = %#v, want string 123", first["enid"])
	}
	ids, _ := data["topic_ids"].([]any)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Errorf("topic_ids = %#v, want [1 2] as strings", ids)
	}
}

func TestPrinterSuccess_NormalizesKeysAndSemanticTimesRecursively(t *testing.T) {
	var out bytes.Buffer
	printer := NewPrinter(&out, Options{Format: FormatJSON, Compact: true})
	err := printer.Success(map[string]any{
		"moduleList": []any{map[string]any{
			"contentId":   42,
			"publishTime": 1785988285,
			"displayTime": "15:01",
		}},
		"isMore":            false,
		"typeIdCollection":  []any{1, 2},
		"requestId":         9,
		"teacherInnerGoods": []any{},
		"currentTime":       "1785988285000",
		"createdAt":         "2026-08-06 11:51:25",
		"releaseDate":       "2026-08-09",
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	for _, key := range []string{"module_list", "is_more", "type_id_collection", "request_id", "teacher_inner_goods"} {
		if _, exists := data[key]; !exists {
			t.Errorf("normalized output is missing %q: %#v", key, data)
		}
	}
	for _, key := range []string{"moduleList", "isMore", "typeIdCollection", "requestId", "teacherInnerGoods"} {
		if _, exists := data[key]; exists {
			t.Errorf("camelCase key %q survived normalization", key)
		}
	}
	items := data["module_list"].([]any)
	item := items[0].(map[string]any)
	if item["content_id"] != "42" {
		t.Errorf("content_id = %#v, want string 42", item["content_id"])
	}
	if item["publish_time"] != "2026-08-06T03:51:25Z" {
		t.Errorf("publish_time = %#v, want RFC3339 UTC", item["publish_time"])
	}
	if item["display_label"] != "15:01" {
		t.Errorf("display_label = %#v, want partial display text", item["display_label"])
	}
	if _, exists := item["display_time"]; exists {
		t.Error("partial display text retained a semantic time key")
	}
	ids := data["type_id_collection"].([]any)
	if ids[0] != "1" || ids[1] != "2" || data["request_id"] != "9" {
		t.Errorf("identifier values were not stringified: %#v", data)
	}
	if data["current_time"] != "2026-08-06T03:51:25Z" || data["created_at"] != "2026-08-06T03:51:25Z" {
		t.Errorf("millisecond/local timestamps were not normalized: %#v", data)
	}
	if data["release_date"] != "2026-08-09" {
		t.Errorf("ISO calendar date = %#v, want date preserved without inventing a time", data["release_date"])
	}
}

func TestPrinterSuccess_RejectsNormalizedKeyCollision(t *testing.T) {
	var out bytes.Buffer
	printer := NewPrinter(&out, Options{Format: FormatJSON, Compact: true})
	err := printer.Success(map[string]any{"requestId": "first", "request_id": "second"})
	if err == nil || !strings.Contains(err.Error(), `"requestId" and "request_id"`) {
		t.Fatalf("collision error = %v, want both original keys named", err)
	}
	if out.Len() != 0 {
		t.Fatalf("collision wrote partial stdout: %q", out.String())
	}
}

func TestPrinterFailure_NormalizesDetails(t *testing.T) {
	var out bytes.Buffer
	printer := NewPrinter(&out, Options{Format: FormatJSON, Compact: true})
	err := printer.Failure(NewError("E_SERVER", "failed", map[string]any{
		"requestId":  7,
		"lastSeenAt": "2026-08-06T11:51:25+08:00",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	errorObject := envelope["error"].(map[string]any)
	details := errorObject["details"].(map[string]any)
	if details["request_id"] != "7" || details["last_seen_at"] != "2026-08-06T03:51:25Z" {
		t.Errorf("normalized details = %#v", details)
	}
}

func TestPrinterFailure_ReportsDetailsCollisionWithoutBreakingEnvelope(t *testing.T) {
	var out bytes.Buffer
	printer := NewPrinter(&out, Options{Format: FormatJSON, Compact: true})
	err := printer.Failure(NewError("E_SERVER", "failed", map[string]any{
		"requestId": "first", "request_id": "second",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	errorObject := envelope["error"].(map[string]any)
	if errorObject["code"] != "E_UNKNOWN" {
		t.Errorf("code = %#v, want E_UNKNOWN", errorObject["code"])
	}
	details := errorObject["details"].(map[string]any)
	message, _ := details["normalization_error"].(string)
	if !strings.Contains(message, `"requestId" and "request_id"`) {
		t.Errorf("normalization_error = %q", message)
	}
}
