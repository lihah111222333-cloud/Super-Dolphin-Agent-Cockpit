package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationLauncherProtocolFreeze enforces P22 P4 §62 / §120 /
// §280: the remoteLauncher's outbound RPC method names and response
// alias keys must live in a single explicit protocol file
// (cmd/mcp-orch/orchestration/launcher_protocol.go), not scattered as
// raw string literals through launcher.go or other siblings. Freezing
// the shell here ensures any change to the outbound contract is
// visible as a diff in one place and guarded against silent drift.
//
// The test scans every non-test .go file under
// cmd/mcp-orch/orchestration. For each frozen literal, the only
// accepted producer file is launcher_protocol.go.
func TestOrchestrationLauncherProtocolFreeze(t *testing.T) {
	const (
		dir      = "../../cmd/mcp-orch/orchestration"
		producer = "launcher_protocol.go"
	)

	// The guard freezes remoteLauncher outbound RPC method names. The
	// response alias keys (`threadId` / `agentId` / `turn_id`) deliberately
	// overlap with inbound JSON struct tags elsewhere in the subpackage,
	// so guarding them here would produce false positives; the key
	// freeze lives inside launcher_protocol.go itself.
	frozen := []string{
		"\"thread/start\"",
		"\"thread/stop\"",
		"\"thread/archive\"",
		"\"thread/name/set\"",
		"\"turn/start\"",
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
				t.Errorf("%s: frozen launcher-protocol literal %s appears outside %s (P22 P4 §62/§120/§280: add/rename it in %s instead of inlining)", path, tok, producer, producer)
			}
		}
	}

	// Also verify launcher_protocol.go actually contains each literal,
	// so deletions don't accidentally weaken the freeze.
	data, err := os.ReadFile(filepath.Join(dir, producer))
	if err != nil {
		t.Fatalf("read %s: %v", producer, err)
	}
	text := string(data)
	for _, tok := range frozen {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected frozen launcher-protocol literal %s to be present", producer, tok)
		}
	}
}
