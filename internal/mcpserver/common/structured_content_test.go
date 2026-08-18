package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStructuredContentFromRawPreservesObjects(t *testing.T) {
	got, err := StructuredContentFromRaw(json.RawMessage(`{"success":true,"path":"smoke.go"}`))
	if err != nil {
		t.Fatalf("StructuredContentFromRaw(object) error = %v", err)
	}
	assertJSONObject(t, got)
	if string(got) != `{"success":true,"path":"smoke.go"}` {
		t.Fatalf("StructuredContentFromRaw(object) = %s, want original object", got)
	}
}

func TestStructuredContentFromRawWrapsArrays(t *testing.T) {
	got, err := StructuredContentFromRaw(json.RawMessage(`[{"name":"targetName"},{"name":"useTarget"}]`))
	if err != nil {
		t.Fatalf("StructuredContentFromRaw(array) error = %v", err)
	}
	var payload struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("StructuredContentFromRaw(array) produced invalid JSON object: %v; raw=%s", err, got)
	}
	if payload.Total != 2 || len(payload.Items) != 2 {
		t.Fatalf("StructuredContentFromRaw(array) = %s, want total/items wrapper", got)
	}
}

func TestStructuredContentFromRawWrapsScalars(t *testing.T) {
	got, err := StructuredContentFromRaw(json.RawMessage(`"package main\n"`))
	if err != nil {
		t.Fatalf("StructuredContentFromRaw(string) error = %v", err)
	}
	var payload struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("StructuredContentFromRaw(string) produced invalid JSON object: %v; raw=%s", err, got)
	}
	if payload.Value != "package main\n" {
		t.Fatalf("StructuredContentFromRaw(string) value = %q, want file text", payload.Value)
	}
}

func TestToolCallResultResponseStructuredContentIsObject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "string", value: "package main\n"},
		{name: "array", value: []map[string]any{{"name": "targetName"}}},
		{name: "object", value: map[string]any{"success": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, _, err := toolCallResultResponse(json.RawMessage(`1`), tc.value)
			if err != nil {
				t.Fatalf("toolCallResultResponse() error = %v", err)
			}
			payload, ok := resp.Result.(map[string]any)
			if !ok {
				t.Fatalf("Result = %T, want map", resp.Result)
			}
			raw, ok := payload["structuredContent"].(json.RawMessage)
			if !ok {
				t.Fatalf("structuredContent = %T, want json.RawMessage", payload["structuredContent"])
			}
			assertJSONObject(t, raw)
		})
	}
}

func TestToolCallResultResponseUsesPlainTextProviderForContent(t *testing.T) {
	value := plainTextToolCallResult{
		payload: plainTextPayload{Success: true, Count: 2},
		text:    "Plain result for the model",
	}

	resp, raw, err := toolCallResultResponse(json.RawMessage(`1`), value)
	if err != nil {
		t.Fatalf("toolCallResultResponse() error = %v", err)
	}
	if string(raw) != `{"success":true,"count":2}` {
		t.Fatalf("raw result = %s, want marshaled payload", raw)
	}
	payload, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result = %T, want map", resp.Result)
	}
	content, ok := payload["content"].([]map[string]string)
	if !ok {
		t.Fatalf("content = %T, want []map[string]string", payload["content"])
	}
	if len(content) != 1 || content[0]["text"] != "Plain result for the model" {
		t.Fatalf("content = %#v, want plain text provider output", content)
	}
	structuredContent, ok := payload["structuredContent"].(json.RawMessage)
	if !ok {
		t.Fatalf("structuredContent = %T, want json.RawMessage", payload["structuredContent"])
	}
	assertJSONObject(t, structuredContent)
	if string(structuredContent) != `{"success":true,"count":2}` {
		t.Fatalf("structuredContent = %s, want marshaled payload object", structuredContent)
	}
}

type plainTextPayload struct {
	Success bool `json:"success"`
	Count   int  `json:"count"`
}

type plainTextToolCallResult struct {
	payload plainTextPayload
	text    string
}

func (r plainTextToolCallResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.payload)
}

func (r plainTextToolCallResult) ToolResultText() string {
	return r.text
}

func assertJSONObject(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("raw = %s, want JSON object: %v", raw, err)
	}
}

func TestTextOnlyToolCallResultCarriesTrustedMetadataOutOfBand(t *testing.T) {
	const pathMarker = "C:\\Users\\private\\secret.go"
	value := ToolErrorEnvelope{
		Success: false,
		Error:   "Windows authorization is required.",
		Code:    "authorization_required",
		Meta: map[string]any{
			"authorization_required":  true,
			"windows_error_code":      uint32(5),
			"windows_permission_kind": "access_denied",
			"path_marker":             pathMarker,
		},
	}
	result, err := BuildToolCallResultWithPolicy(value, NewTextOnlyToolCallResultPolicy(func(value any) (string, bool) {
		envelope, ok := value.(ToolErrorEnvelope)
		if !ok {
			return "", false
		}
		return envelope.ToPlainText(), true
	}))
	if err != nil {
		t.Fatalf("BuildToolCallResultWithPolicy() error = %v", err)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("text-only result unexpectedly contains structuredContent: %#v", result)
	}
	meta, ok := result["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("text-only result _meta = %T, want trusted metadata object", result["_meta"])
	}
	if meta["authorization_required"] != true || meta["windows_error_code"] != uint32(5) || meta["windows_permission_kind"] != "access_denied" {
		t.Fatalf("ACL metadata = %#v, want typed authorization fields", meta)
	}
	if _, leaked := meta["path_marker"]; leaked {
		t.Fatalf("trusted metadata leaked path marker: %#v", meta)
	}
	content, ok := result["content"].([]map[string]string)
	if !ok || len(content) != 1 || strings.Contains(content[0]["text"], pathMarker) {
		t.Fatalf("text-only content = %#v, want pure public text", result["content"])
	}
}
