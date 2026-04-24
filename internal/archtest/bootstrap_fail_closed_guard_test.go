package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestBootstrapHandleCallbackFailsClosedOnUnknownMethod enforces
// P22 P4 S5b / plan §315: the bootstrap client's OnCallback must
// not default-ACK unknown methods. The guard is source-shape based
// to keep the wire-shape freeze audit-friendly.
func TestBootstrapHandleCallbackFailsClosedOnUnknownMethod(t *testing.T) {
	const path = "../../internal/mcpserver/common/bootstrap/lifecycle.go"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	required := []string{
		"errBootstrapUnknownMethod(",
		"dispatchLifecycleRequest(",
		"§315",
		"jrpc2.Code(-32601)",
	}
	for _, tok := range required {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected %q to be present (P22 P4 S5b: fail-closed unknown method must stay wired)", path, tok)
		}
	}
	// Pre-S5b pattern that must not return.
	forbidden := []string{
		"return map[string]bool{\"ok\": true}, nil\n}\n\nfunc (c *Client) dispatchRequest",
	}
	for _, tok := range forbidden {
		if strings.Contains(text, tok) {
			t.Errorf("%s: forbidden pre-S5b shape reappeared near %q", path, tok)
		}
	}
}

// TestBootstrapPendingHooksDropsBootAgentIDFallback enforces P22
// P4 S5b / plan §316: PendingHooks must use cfg.AgentID as the
// single authoritative identity source — no FirstNonEmpty fallback
// to boot.AgentID. Source-shape based so the contract change is
// visible in the guard diff when/if someone reintroduces the
// fallback.
func TestBootstrapPendingHooksDropsBootAgentIDFallback(t *testing.T) {
	const path = "../../internal/mcpserver/common/bootstrap/hooks.go"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)

	forbidden := []string{
		"FirstNonEmpty(c.cfg.AgentID, c.boot.AgentID)",
	}
	for _, tok := range forbidden {
		if strings.Contains(text, tok) {
			t.Errorf("%s: forbidden FirstNonEmpty fallback present (P22 P4 S5b: cfg.AgentID must be the sole identity source)", path)
		}
	}

	required := []string{
		"agentID := strings.TrimSpace(c.cfg.AgentID)",
		"§316",
	}
	for _, tok := range required {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected %q to be present (P22 P4 S5b)", path, tok)
		}
	}
}
