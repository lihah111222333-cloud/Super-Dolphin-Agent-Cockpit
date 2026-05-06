package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationReportProtocolFreeze enforces P22 P4 §64 / §122 /
// §283: the agent/reportEvent / agent/rememberReportRequest RPC
// method names and the special thread/status/changed terminal event
// type must live in a single authoritative protocol file
// (cmd/mcp-orch/orchestration/report_protocol.go). Sibling report.go
// and rpc.go must reference the exported symbols instead of inlining
// the literals.
//
// Freezing the surface in one place means any contract change is a
// single-file diff that code review, archtests, and downstream
// consumers can audit together.
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

	// Also verify report_protocol.go still owns each frozen literal,
	// so deletions cannot silently weaken the freeze.
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
