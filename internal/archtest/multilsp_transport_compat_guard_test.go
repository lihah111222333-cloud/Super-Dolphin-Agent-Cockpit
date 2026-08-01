package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLSPTransportCompatFreeze 固定 multilsp server-request 兼容表的唯一来源。
// 所有冻结的方法字面量必须集中在 transport.go，不能散落到其他实现文件。
//
// 兼容逻辑与 transport 生命周期共享同一责任文件；该守卫确保后续兼容项仍形成单文件 diff，
// 便于代码审查和下游适配方确认协议变化。
func TestMultiLSPTransportCompatFreeze(t *testing.T) {
	const (
		dir      = "../../cmd/mcp-lsp/multilsp"
		producer = "transport.go"
	)
	frozen := frozenMultilspTransportCompatLiterals()
	assertFrozenCompatLiteralsOutsideProducer(t, dir, producer, frozen)
	assertFrozenCompatLiteralsInProducer(t, dir, producer, frozen)
}

func frozenMultilspTransportCompatLiterals() []string {
	return []string{
		"\"client/registerCapability\"",
		"\"client/unregisterCapability\"",
		"\"window/workDoneProgress/create\"",
		"\"workspace/configuration\"",
		"\"workspace/semanticTokens/refresh\"",
		"\"workspace/codeLens/refresh\"",
		"\"workspace/inlayHint/refresh\"",
		"\"workspace/diagnostic/refresh\"",
	}
}

func assertFrozenCompatLiteralsOutsideProducer(t *testing.T, dir, producer string, frozen []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == producer {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, tok := range frozen {
			if strings.Contains(text, tok) {
				t.Errorf("%s: frozen LSP compat literal %s appears outside %s (register the method + response shape in %s instead of inlining)", path, tok, producer, producer)
			}
		}
	}
}

func assertFrozenCompatLiteralsInProducer(t *testing.T, dir, producer string, frozen []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, producer))
	if err != nil {
		t.Fatalf("read %s: %v", producer, err)
	}
	text := string(data)
	for _, tok := range frozen {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected frozen LSP compat literal %s to be present", producer, tok)
		}
	}
}
