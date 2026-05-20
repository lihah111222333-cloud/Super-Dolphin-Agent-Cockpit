package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBudgetOverflowSetsSuccessFalse(t *testing.T) {
	handler := WithOutputBudget("edit", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"success": true, "data": strings.Repeat("x", 1024)}, nil
	}, Budget{MaxBytes: 64})

	got, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("budget middleware error = %v", err)
	}
	payload := got.(map[string]any)
	if payload["success"] != false || payload["error_code"] != "result_too_large" {
		t.Fatalf("overflow payload = %#v, want success false result_too_large", payload)
	}
	if payload["original_success"] != true {
		t.Fatalf("original_success = %#v, want true", payload["original_success"])
	}
}

func TestGenericBudgetOverflowSetsSuccessFalse(t *testing.T) {
	handler := WithOutputBudget("grep", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"success": true, "data": strings.Repeat("x", 1024)}, nil
	}, Budget{MaxBytes: 64})

	got, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("budget middleware error = %v", err)
	}
	payload := got.(map[string]any)
	if payload["success"] != false || payload["error_code"] != "result_too_large" {
		t.Fatalf("overflow payload = %#v, want success false result_too_large", payload)
	}
	if payload["original_success"] != true {
		t.Fatalf("original_success = %#v, want true", payload["original_success"])
	}
}
