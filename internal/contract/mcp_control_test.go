package contract_test

import (
	"reflect"
	"strings"
	"testing"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestMCPProtocolToolKeepsLifecycleOutOfJSONContract(t *testing.T) {
	toolType := reflect.TypeFor[mcpdto.MCPTool]()
	got := make(map[string]bool, toolType.NumField())
	for i := 0; i < toolType.NumField(); i++ {
		tag := toolType.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		got[name] = true
	}

	want := map[string]bool{
		"name":         true,
		"description":  true,
		"inputSchema":  true,
		"outputSchema": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MCPTool JSON fields = %#v, want protocol-only fields %#v", got, want)
	}
	for _, forbidden := range []string{
		"lifecycle", "lifecycleState", "state", "reason", "source", "updatedBy",
		"workspaceRoot", "serverName", "toolName",
	} {
		if got[forbidden] {
			t.Fatalf("MCPTool unexpectedly exposes lifecycle field %q", forbidden)
		}
	}
}
