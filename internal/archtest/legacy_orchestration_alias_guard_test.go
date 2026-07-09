package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyOrchestrationStringsStayInToolbridgeAliasIsolation 防止旧 orch 名重新散落到 toolbridge 生产代码。
// handler_peer_alias.go 是唯一允许声明 legacy peer realName / deny-only 名称的隔离层。
func TestLegacyOrchestrationStringsStayInToolbridgeAliasIsolation(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	dir := filepath.Join(root, "internal/platform/toolbridge")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Name() == "handler_peer_alias.go" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "orchestration_") {
			t.Errorf("%s contains legacy orchestration literal; route it through handler_peer_alias.go", path)
		}
	}
}
