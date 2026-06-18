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

func TestOutputBudgetUsesFinalTextNotIntermediateJSON(t *testing.T) {
	handler := WithOutputBudget("file", func(context.Context, json.RawMessage) (any, error) {
		return strings.Repeat("line\n", 20), nil
	}, Budget{MaxBytes: 100})

	got, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("budget middleware error = %v", err)
	}
	if _, ok := got.(map[string]any); ok {
		t.Fatalf("budget middleware returned overflow envelope for final text-sized output: %#v", got)
	}
}

func TestGrepToolBudgetIsSixteenKiB(t *testing.T) {
	if got := ToolBudget("grep"); got != 16*1024 {
		t.Fatalf("ToolBudget(grep) = %d, want %d", got, 16*1024)
	}
}

func TestFileToolBudgetIsFiftyKiB(t *testing.T) {
	if got := ToolBudget("file"); got != 50*1024 {
		t.Fatalf("ToolBudget(file) = %d, want %d", got, 50*1024)
	}
}

func TestEditToolBudgetIsThirtyTwoKiB(t *testing.T) {
	if got := ToolBudget("edit"); got != 32*1024 {
		t.Fatalf("ToolBudget(edit) = %d, want %d", got, 32*1024)
	}
}
