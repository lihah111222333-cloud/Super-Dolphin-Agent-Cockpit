package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThreadTurnPendingLaunchSpawnerContractGuard 固定 PendingLaunchSpawner 的归属。
// 接口必须位于 internal/contract，不能回到 turn 消费包，否则 thread 模块会重新产生反向依赖。
// 测试通过源码扫描约束两件事：turn 包不能重新声明接口；thread/module.go 必须引用 contract 版本。
func TestThreadTurnPendingLaunchSpawnerContractGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	turnDir := filepath.Join(root, "internal", "module", "turn")
	assertTurnDoesNotDeclarePendingLaunchSpawner(t, turnDir)
	assertThreadModuleUsesContractPendingLaunchSpawner(t, root)
}

func assertTurnDoesNotDeclarePendingLaunchSpawner(t *testing.T, turnDir string) {
	t.Helper()

	// turn 包不能重新导出 PendingLaunchSpawner 接口；只有真实声明才算违规。
	entries, err := os.ReadDir(turnDir)
	if err != nil {
		t.Fatalf("read %s: %v", turnDir, err)
	}
	const forbiddenDecl = "type PendingLaunchSpawner interface"
	var declHits []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(turnDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), forbiddenDecl) {
			declHits = append(declHits, name)
		}
	}
	if len(declHits) > 0 {
		t.Fatalf("internal/module/turn reintroduced the PendingLaunchSpawner interface (P4 §2.5 side-channel violation); offending files: %v", declHits)
	}
}

func assertThreadModuleUsesContractPendingLaunchSpawner(t *testing.T, root string) {
	t.Helper()

	// thread/module.go 必须引用 contract.PendingLaunchSpawner，并避免在代码位置重新引用 turn 版本。
	threadModule := filepath.Join(root, "internal", "module", "thread", "module.go")
	data, err := os.ReadFile(threadModule)
	if err != nil {
		t.Fatalf("read %s: %v", threadModule, err)
	}
	src := string(data)
	if !strings.Contains(src, "contract.PendingLaunchSpawner") {
		t.Errorf("thread/module.go must reference contract.PendingLaunchSpawner after P4 S2")
	}
	// 只匹配会形成真实包依赖的旧符号形态，注释中的文字说明不应触发失败。
	forbiddenRefs := []string{
		"var _ turn.PendingLaunchSpawner",
		"fx.As(new(turn.PendingLaunchSpawner))",
	}
	for _, token := range forbiddenRefs {
		if strings.Contains(src, token) {
			t.Errorf("thread/module.go reintroduced active reference to turn.PendingLaunchSpawner: %q", token)
		}
	}
}
