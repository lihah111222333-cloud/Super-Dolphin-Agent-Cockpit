package thread

import (
	"bytes"
	"encoding/json"
	"strings"
)

func buildStartSessionConfig(req StartRequest) map[string]any {
	cfg := map[string]any{}
	putConfigString(cfg, "approvalPolicy", req.ApprovalPolicy)
	putConfigString(cfg, "approval_policy", req.ApprovalPolicy)
	putConfigString(cfg, "approvals", req.ApprovalPolicy)
	putConfigString(cfg, "modelProvider", req.ModelProvider)
	putConfigString(cfg, "developerInstructions", req.DeveloperInstructions)
	putConfigString(cfg, "developer_instructions", req.DeveloperInstructions)
	putConfigString(cfg, "summary", req.Summary)
	putConfigString(cfg, "effort", req.Effort)
	putConfigString(cfg, "personality", req.Personality)
	putConfigJSON(cfg, "sandbox", req.Sandbox)
	for key, value := range req.Config {
		if _, exists := cfg[key]; !exists {
			cfg[key] = value
		}
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func putConfigString(cfg map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		cfg[key] = value
	}
}

func putConfigJSON(cfg map[string]any, key string, raw json.RawMessage) {
	raw = trimRawJSON(raw)
	if len(raw) == 0 {
		return
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		cfg[key] = value
	}
}

func trimRawJSON(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// looksLikeUUID returns true when s resembles a UUID (hex-and-dashes, 32+ hex chars).
// It rejects agent_id placeholders like "agent_17754..." that are not valid provider UUIDs.
func looksLikeUUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 32 {
		return false
	}
	hex := 0
	for _, c := range s {
		switch {
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
			hex++
		case c == '-':
			// ok
		default:
			return false
		}
	}
	return hex >= 32
}
