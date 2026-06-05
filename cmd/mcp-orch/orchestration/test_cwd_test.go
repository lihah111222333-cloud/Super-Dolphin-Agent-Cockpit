package orchestration

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var testCWDCache sync.Map

func testCWD(t *testing.T, name string) string {
	t.Helper()
	key := t.Name() + "\x00" + name
	if value, ok := testCWDCache.Load(key); ok {
		return value.(string)
	}
	value, _ := testCWDCache.LoadOrStore(key, filepath.ToSlash(filepath.Join(t.TempDir(), name)))
	return value.(string)
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
	exec["provider"] = "claude"
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
