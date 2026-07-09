package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationReverseLookupConfinedToAgentRegistry enforces the P4
// plan's identity fence (§63 / §121 / §282): the reverse-lookup that
// treats a remote agent/thread id as equivalent to the orchestration
// local agent name must live in a single authoritative function
// (lookupAgentByIdentityLocked in agent_registry.go), not scattered across
// the subpackage.
//
// Concretely, the literals `remoteAgentID ==` and `remoteThreadID ==`
// may only appear in agent_registry.go. Any other non-test .go file under
// cmd/mcp-orch/orchestration re-implementing that reverse-lookup
// fails this guard before it can drift into a second trust boundary.
func TestOrchestrationReverseLookupConfinedToAgentRegistry(t *testing.T) {
	const (
		dir   = "../../cmd/mcp-orch/orchestration"
		owner = "agent_registry.go"
	)

	// Narrow tokens: only flag the reverse-lookup shape that compares
	// a candidate's remote-id field against an external `agentID`
	// parameter. Sibling `remoteThreadID == ""` nil-checks inside the
	// launcher are unrelated and must not trip the guard.
	forbidden := []string{
		"remoteAgentID == agentID",
		"remoteThreadID == agentID",
	}

	assertReverseLookupConfinedToOwner(t, dir, owner, forbidden)
	assertReverseLookupOwnerContainsTokens(t, dir, owner, forbidden)
}

func assertReverseLookupConfinedToOwner(t *testing.T, dir string, owner string, forbidden []string) {
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
		if name == owner {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, tok := range forbidden {
			if strings.Contains(text, tok) {
				t.Errorf("%s: reverse-lookup literal %q appears outside %s (P22 P4 §63/§121/§282: route reverse-lookup through lookupAgentByIdentityLocked in %s)", path, tok, owner, owner)
			}
		}
	}
}

func assertReverseLookupOwnerContainsTokens(t *testing.T, dir string, owner string, forbidden []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, owner))
	if err != nil {
		t.Fatalf("read %s: %v", owner, err)
	}
	text := string(data)
	for _, tok := range forbidden {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected reverse-lookup literal %q to still be present", owner, tok)
		}
	}
}
