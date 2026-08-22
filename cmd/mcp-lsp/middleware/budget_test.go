package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBudgetOverflowSetsSuccessFalse(t *testing.T) {
	handler := WithOutputBudget("diagnostics", func(context.Context, json.RawMessage) (any, error) {
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
	handler := WithOutputBudget("structure", func(context.Context, json.RawMessage) (any, error) {
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

func TestDiagnosticsToolBudgetIsFiftyKiB(t *testing.T) {
	if got := ToolBudget("diagnostics"); got != 50*1024 {
		t.Fatalf("ToolBudget(diagnostics) = %d, want %d", got, 50*1024)
	}
}

func TestBudgetDescriptorsDoNotShareMutableState(t *testing.T) {
	budgets := defaultToolBudgets()
	if len(budgets) == 0 {
		t.Fatal("defaultToolBudgets() returned no descriptors")
	}
	budgets[0].maxBytes = 1
	if got := ToolBudget("grep"); got != 16*1024 {
		t.Fatalf("ToolBudget(grep) after descriptor mutation = %d, want %d", got, 16*1024)
	}

	hints := toolOverflowHints()
	grepHint, ok := hints["grep"]
	if !ok {
		t.Fatal("toolOverflowHints() missing grep descriptor")
	}
	grepHint.NextAction["tool"] = "mutated"
	if got := lookupHint("grep").NextAction["tool"]; got != "grep" {
		t.Fatalf("lookupHint(grep).NextAction[tool] after descriptor mutation = %#v, want grep", got)
	}
}

func TestXRefOverflowHintUsesSupportedArguments(t *testing.T) {
	hint := lookupHint("xref")
	if strings.Contains(hint.Hint, "verbosity") {
		t.Fatalf("xref overflow hint exposes unsupported verbosity argument: %q", hint.Hint)
	}
	args, ok := hint.NextAction["suggest_args"].(map[string]any)
	if !ok {
		t.Fatalf("xref suggest_args = %#v, want map", hint.NextAction["suggest_args"])
	}
	if _, present := args["verbosity"]; present {
		t.Fatalf("xref suggest_args contains unsupported verbosity: %#v", args)
	}
	if got := args["max_results"]; got != 10 {
		t.Fatalf("xref max_results suggestion = %#v, want 10", got)
	}
}
