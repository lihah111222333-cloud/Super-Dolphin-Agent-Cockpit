package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationLauncherProtocolFreeze enforces P22 P4 §62 / §120 /
// §280: the remoteLauncher's outbound RPC method names and response
// alias keys must live in explicit protocol constants shared with the
// app-side thread RPC handlers, not scattered as raw string literals through
// launcher.go or other siblings. Freezing the shell here ensures any change to
// the outbound contract is visible as a diff in one place and guarded against
// silent drift.
//
// The test scans every non-test .go file under
// cmd/mcp-orch/orchestration. For each frozen literal, the only
// accepted producer file is launcher_protocol.go.
func TestOrchestrationLauncherProtocolFreeze(t *testing.T) {
	const (
		dir              = "../../cmd/mcp-orch/orchestration"
		producer         = "launcherwire/protocol.go"
		contractProducer = "../../internal/contract/rpc_handler.go"
	)

	// The guard freezes remoteLauncher outbound RPC method names. The
	// response alias keys (`threadId` / `agentId` / `turn_id`) deliberately
	// overlap with inbound JSON struct tags elsewhere in the subpackage,
	// so guarding them here would produce false positives; the key
	// freeze lives inside launcher_protocol.go itself.
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
