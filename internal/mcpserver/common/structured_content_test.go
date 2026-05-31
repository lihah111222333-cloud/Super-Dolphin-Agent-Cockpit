package common

import (
	"encoding/json"
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
