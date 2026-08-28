package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestStableToolSchemaPreflightErrorReportsFieldsWithoutHandlerCall(t *testing.T) {
	called := false
	defs := toolDefinitions(ToolHandlers{
		"structure": func(_ context.Context, _ json.RawMessage) (any, error) {
			called = true
			return nil, nil
		},
	})
	_, err := handleToolCall(context.Background(), defs, "structure", json.RawMessage(`{"action":"document_symbol","symbol":"guess"}`))
	if err == nil || called {
		t.Fatalf("schema preflight err=%v handler_called=%t, want reject before handler", err, called)
	}
	var coded *common.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "invalid_params" || coded.Retryable {
		t.Fatalf("schema preflight error = %T %v, want non-retryable invalid_params", err, err)
	}
	if got := coded.Meta["unknown_fields"]; !strings.Contains(strings.TrimSpace(fmt.Sprint(got)), "symbol") {
		t.Fatalf("unknown_fields = %#v, want symbol", got)
	}
	if got := coded.Meta["allowed_fields"]; got == nil {
		t.Fatal("allowed_fields missing from schema preflight metadata")
	}
	if !strings.Contains(coded.Hint, "retryable=false") {
		t.Fatalf("schema preflight hint = %q, want retryable=false", coded.Hint)
	}
}
