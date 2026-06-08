package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCWD(t *testing.T, name string) string {
	t.Helper()
	base := filepath.Join(os.TempDir(), "super-dolphin-test-cwd", sanitizeTestCWDPart(t.Name()))
	path := filepath.Join(base, sanitizeTestCWDPart(name))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create test cwd %s: %v", path, err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(base)
	})
	return filepath.ToSlash(path)
}

func sanitizeTestCWDPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "empty"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, value)
}

func testRawConfig(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	for _, name := range []string{"node-cwd", "replan-cwd", "validation-cwd", "agent-launch"} {
		raw = strings.ReplaceAll(raw, "/tmp/"+name, testCWD(t, name))
	}
	raw = testConfigWithDefaultAgentProvider(raw)
	return json.RawMessage(raw)
}

func testConfigWithDefaultAgentProvider(raw string) string {
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return raw
	}
	exec, ok := config["exec"].(map[string]any)
	if !ok || testConfigString(exec, "provider") != "" {
		return raw
	}
	if testConfigString(exec, "agent_key") == "" && testConfigString(exec, "prompt_key") == "" {
		return raw
	}
	exec["provider"] = "codex"
	out, err := json.Marshal(config)
	if err != nil {
		return raw
	}
	return string(out)
}

func testConfigString(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
