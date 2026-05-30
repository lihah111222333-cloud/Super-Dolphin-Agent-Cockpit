package multilsp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInitOptionsSettingsAnswerWorkspaceConfiguration(t *testing.T) {
	initOptions := map[string]any{
		"settings": map[string]any{
			"python": map[string]any{
				"pythonPath": "/__super_dolphin_no_system_python__/python",
			},
		},
	}
	handler := configurationRequestHandlerFromInitOptions(initOptions)
	if handler == nil {
		t.Fatal("configurationRequestHandlerFromInitOptions() = nil, want handler")
	}

	result, err := handler(context.Background(), LSPCompatMethodWorkspaceConfiguration, json.RawMessage(`{"items":[{"section":"python"},{"section":"python.analysis"}]}`))
	if err != nil {
		t.Fatalf("workspace/configuration handler: %v", err)
	}
	items, ok := result.([]any)
	if !ok {
		t.Fatalf("workspace/configuration result = %T, want []any", result)
	}
	if len(items) != 2 {
		t.Fatalf("workspace/configuration item count = %d, want 2", len(items))
	}
	python, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("python section = %#v, want map", items[0])
	}
	if got := python["pythonPath"]; got != "/__super_dolphin_no_system_python__/python" {
		t.Fatalf("python.pythonPath = %#v, want packaged no-system interpreter sentinel", got)
	}
	if items[1] != nil {
		t.Fatalf("python.analysis section = %#v, want nil for unset section", items[1])
	}
}
