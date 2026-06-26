package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationLauncherProtocolFreeze 固定 remoteLauncher 对外 RPC 协议面。
// 出站方法名和响应 alias 必须集中在协议常量中，并与 app 侧 thread RPC handler 共享，
// 避免 launcher.go 或相邻文件散落裸字符串导致契约漂移。
//
// 测试扫描 orchestration 包内所有非测试 Go 文件；每个冻结字面量只能出现在协议生产文件中。
func TestOrchestrationLauncherProtocolFreeze(t *testing.T) {
	const (
		dir              = "../../cmd/mcp-orch/orchestration"
		producer         = "launcherwire/protocol.go"
		contractProducer = "../../internal/contract/rpc_handler.go"
	)

	// 这里只冻结 remoteLauncher 出站 RPC 方法名。
	// 响应 alias 会与包内入站 JSON tag 重名，若在这里扫描会误报；alias 自身由协议文件守住。
	frozen := []string{
		"\"thread/start\"",
		"\"thread/fork\"",
		"\"thread/stop\"",
		"\"thread/archive\"",
		"\"thread/name/set\"",
		"\"turn/start\"",
	}
	requiredAliases := []string{
		"MethodThreadStart   = contract.ThreadRPCStart",
		"MethodThreadFork    = contract.ThreadRPCFork",
		"MethodThreadStop    = contract.ThreadRPCStop",
		"MethodThreadArchive = contract.ThreadRPCArchive",
		"MethodThreadNameSet = contract.ThreadRPCNameSet",
		"MethodTurnStart     = contract.TurnRPCStart",
	}

	assertFrozenLauncherLiteralsOnlyInProducer(t, dir, producer, frozen)
	assertFileContainsAll(t, filepath.Join(dir, producer), requiredAliases, "expected launcher protocol alias")
	assertFileContainsAll(t, contractProducer, frozen, "expected shared RPC method literal")
}

func assertFrozenLauncherLiteralsOnlyInProducer(t *testing.T, dir, producer string, frozen []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		name, ok := launcherProtocolScanFile(e, producer)
		if !ok {
			continue
		}
		path := filepath.Join(dir, name)
		for _, tok := range frozen {
			if strings.Contains(readGuardTextFile(t, path), tok) {
				t.Errorf("%s: frozen launcher-protocol literal %s appears outside %s (P22 P4 §62/§120/§280: add/rename it in %s instead of inlining)", path, tok, producer, producer)
			}
		}
	}
}

func launcherProtocolScanFile(e os.DirEntry, producer string) (string, bool) {
	if e.IsDir() {
		return "", false
	}
	name := e.Name()
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return "", false
	}
	return name, name != producer
}

func readGuardTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertFileContainsAll(t *testing.T, path string, required []string, label string) {
	t.Helper()
	text := readGuardTextFile(t, path)
	for _, token := range required {
		if !strings.Contains(text, token) {
			t.Errorf("%s: %s %q to be present", path, label, token)
		}
	}
}
