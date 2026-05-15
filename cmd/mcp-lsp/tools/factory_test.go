package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDispatchToolActionReportsValidActionsAndClosestMatch(t *testing.T) {
	_, err := dispatchToolAction(context.Background(), "edit", "code_acton", struct{}{}, map[string]actionHandler[struct{}]{
		"rename":        func(context.Context, struct{}) (any, error) { return nil, nil },
		"code_action":   func(context.Context, struct{}) (any, error) { return nil, nil },
		"replace_range": func(context.Context, struct{}) (any, error) { return nil, nil },
	})
	if err == nil {
		t.Fatalf("dispatch error = nil, want unsupported action")
	}
	for _, want := range []string{"valid actions:", "code_action", "replace_range", `did you mean "code_action"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatch error = %q, want %q", err.Error(), want)
		}
	}
}

func TestDispatchToolActionAcceptsLegacyFileReadAlias(t *testing.T) {
	got, err := dispatchToolAction(context.Background(), "file", "read", struct{}{}, map[string]actionHandler[struct{}]{
		"read_file": func(context.Context, struct{}) (any, error) { return "ok", nil },
	})
	if err != nil {
		t.Fatalf("dispatch read alias error = %v", err)
	}
	if got != "ok" {
		t.Fatalf("dispatch read alias result = %#v, want ok", got)
	}
}

func TestDecodeToolParamsAddsAIFriendlyHint(t *testing.T) {
	_, err := decodeToolParams[struct {
		Line int `json:"line"`
	}](json.RawMessage(`{"line":"1"}`), decodeStrict)
	if err == nil {
		t.Fatalf("decode error = nil, want numeric type error")
	}
	if !strings.Contains(err.Error(), "numeric fields as JSON numbers") {
		t.Fatalf("decode error = %q, want numeric hint", err.Error())
	}
}

func TestCursorErrorIncludesOneBasedHint(t *testing.T) {
	envelope := newToolErrorEnvelope("lsp_edit", "go", errors.New("line must be >= 1"))
	if envelope.Success {
		t.Fatalf("envelope success = true, want false")
	}
	if envelope.Code != "position_invalid" {
		t.Fatalf("envelope code = %q, want position_invalid", envelope.Code)
	}
	if !strings.Contains(strings.ToLower(envelope.Hint), "1-based") {
		t.Fatalf("envelope hint = %q, want one-based cursor guidance", envelope.Hint)
	}

	replaceEnvelope := newToolErrorEnvelope("lsp_edit", "go", errors.New("column is out of range"))
	if !strings.Contains(strings.ToLower(replaceEnvelope.Hint), "patch") {
		t.Fatalf("replace_range-style cursor hint = %q, want patch guidance", replaceEnvelope.Hint)
	}
}

func TestRenderListResultEmptyEnvelope(t *testing.T) {
	got, err := renderListResult([]string{}, 10, "no symbols found", func(items []string, total int) any {
		return map[string]any{"items": items, "total": total}
	})
	if err != nil {
		t.Fatalf("renderListResult() error = %v", err)
	}
	payload, ok := got.(emptyListEnvelope)
	if !ok {
		t.Fatalf("empty render result type = %T (%#v), want emptyListEnvelope", got, got)
	}
	if !payload.Success {
		t.Fatalf("empty envelope success = false, want true")
	}
	if len(payload.Data) != 0 || payload.Meta.Count != 0 {
		t.Fatalf("empty envelope = %#v, want empty data/count=0", payload)
	}
	if payload.Meta.Message != "no symbols found" {
		t.Fatalf("empty envelope message = %q", payload.Meta.Message)
	}
}
