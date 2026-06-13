//go:build legacy_pg_fake

package taskdag

import (
	"encoding/json"
	"strings"
)

func validAgentConfigForTest(agent string) json.RawMessage {
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
		panic(err)
	}
	return raw
}
