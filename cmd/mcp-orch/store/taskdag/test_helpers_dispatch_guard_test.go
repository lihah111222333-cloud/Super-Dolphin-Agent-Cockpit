//go:build legacy_pg_fake

package taskdag

import (
	"encoding/json"
	"strings"
	"testing"
)

func validAgentConfigForTest(t *testing.T, agent string) json.RawMessage {
	t.Helper()

	agentKey := strings.TrimSpace(agent)
	if agentKey == "" {
		agentKey = "agent-test"
	}
	raw, err := json.Marshal(map[string]any{
		"exec": map[string]string{
			"agent_key": agentKey,
			"cwd":       "/tmp/node-cwd",
		},
	})
	if err != nil {
		t.Fatalf("marshal agent config: %v", err)
	}
	return raw
}
