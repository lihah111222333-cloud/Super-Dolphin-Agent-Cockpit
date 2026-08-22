package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestHandleToolCallRejectsThreeToolSchemaViolationsBeforeHandler(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args string
	}{
		{name: "xref rejects direction on references", tool: "xref", args: `{"action":"references","pos":"main.go:1:1","direction":"both"}`},
		{name: "structure rejects conflicting workspace selectors", tool: "structure", args: `{"action":"workspace_symbol","query":"Handler","file_path":"main.go","workspace_language":"go"}`},
		{name: "diagnostics rejects legacy action", tool: "diagnostics", args: `{"action":"diagnostics","file_path":"main.go"}`},
		{name: "diagnostics rejects both target forms", tool: "diagnostics", args: `{"file_path":"main.go","file_paths":["other.go"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			defs := toolDefinitions(ToolHandlers{test.tool: func(context.Context, json.RawMessage) (any, error) {
				called = true
				return "unexpected", nil
			}})
			_, err := handleToolCall(context.Background(), defs, test.tool, json.RawMessage(test.args))
			var coded *common.CodedToolError
			if !errors.As(err, &coded) || coded.Code != "invalid_params" || coded.Retryable {
				t.Fatalf("error = %T %v, want non-retryable invalid_params", err, err)
			}
			if called {
				t.Fatal("handler ran before schema validation")
			}
		})
	}
}

func TestHandleToolCallRejectsRemovedTools(t *testing.T) {
	defs := toolDefinitions(ToolHandlers{})
	for _, removed := range []string{"edit", "patch_edit", "file", "read_file", "inspect", "grep", "completion"} {
		_, err := handleToolCall(context.Background(), defs, removed, json.RawMessage(`{}`))
		if err == nil || !strings.Contains(err.Error(), "unknown tool: "+removed) {
			t.Fatalf("removed tool %q error = %v, want unknown tool", removed, err)
		}
	}
}

func TestToolDefinitionAssemblyCompilesEveryThreeToolSchema(t *testing.T) {
	manifests := newLSPToolManifests()
	defs, err := compileToolDefinitions(manifests, nil)
	if err != nil {
		t.Fatalf("compileToolDefinitions() error = %v", err)
	}
	if len(defs) != 3 {
		t.Fatalf("compiled definitions = %d, want 3", len(defs))
	}
	for _, def := range defs {
		if def.validator == nil {
			t.Fatalf("tool %q has no schema validator", def.Manifest.Name)
		}
	}
}
