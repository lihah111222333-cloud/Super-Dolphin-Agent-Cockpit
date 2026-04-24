package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestBootstrapSubscribeHooksPersistsDesiredStateOnLiveFailure
// enforces P22 P2 bootstrap-S2 / plan §498 / §504: the live-call
// failure branch of SubscribeHooks must persist the desired
// subscription state before returning the error so the reconnect
// path (replayHookSubscriptions) can retry.
//
// This archtest is deliberately source-shape based: it verifies that
// hooks.go still contains both `c.hooks.store(` and
// `c.hooks.markReplayPending(` inside the file so a future refactor
// that drops the persistence call fails the build rather than
// silently regressing the contract.
func TestBootstrapSubscribeHooksPersistsDesiredStateOnLiveFailure(t *testing.T) {
	const path = "../../internal/mcpserver/common/bootstrap/hooks.go"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)

	required := []string{
		"c.hooks.store(",             // desired state must be persisted
		"c.hooks.markReplayPending(", // and marked as pending replay
	}
	for _, tok := range required {
		if !strings.Contains(text, tok) {
			t.Errorf("%s: expected %q to be present (P22 P2 bootstrap-S2: live-call failure must persist desired state for replay)", path, tok)
		}
	}

	// Also sanity-check the contract docstring stays. If someone
	// removes the comment block, they probably removed the
	// behaviour too.
	for _, anchor := range []string{
		"P22 P2",
		"§498",
		"§504",
	} {
		if !strings.Contains(text, anchor) {
			t.Errorf("%s: expected SubscribeHooks contract docstring to still reference %q", path, anchor)
		}
	}
}
