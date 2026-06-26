package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationReportProtocolFreeze 锁定报告 RPC 方法名和终态事件类型的唯一出处。
// report.go 与 rpc.go 必须引用 report_protocol.go 中的导出常量，避免协议字面量在多处漂移。
func TestOrchestrationReportProtocolFreeze(t *testing.T) {
	const (
		dir      = "../../cmd/mcp-orch/orchestration"
		producer = "report_protocol.go"
	)

	frozen := []string{
		"\"agent/reportEvent\"",
		"\"agent/rememberReportRequest\"",
		"\"thread/status/changed\"",
	}

	assertNoFrozenReportLiteralsOutsideProducer(t, dir, producer, frozen)
	assertReportProtocolProducerOwnsLiterals(t, dir, producer, frozen)
}

func assertNoFrozenReportLiteralsOutsideProducer(t *testing.T, dir, producer string, frozen []string) {
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
				t.Errorf("%s: frozen report-protocol literal %s appears outside %s (P22 P4 §64/§122/§283: reference the constant in %s instead of inlining)", path, tok, producer, producer)
			}
		}
	}
}

func assertReportProtocolProducerOwnsLiterals(t *testing.T, dir, producer string, frozen []string) {
	t.Helper()

	// 同时确认 report_protocol.go 仍持有这些字面量，避免删除常量时静默削弱守卫。
	data, err := os.ReadFile(filepath.Join(dir, producer))
	if err != nil {
		t.Fatalf("read %s: %v", producer, err)
	}
	text := string(data)
	for _, tok := range frozen {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected frozen report-protocol literal %s to be present", producer, tok)
		}
	}
}
