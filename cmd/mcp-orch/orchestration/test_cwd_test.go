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
	return json.RawMessage(raw)
}
