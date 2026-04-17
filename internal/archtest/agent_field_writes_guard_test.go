package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAgentFieldWritesCentralized guards against direct zero-value writes to
// agentState bookkeeping fields inside cmd/mcp-orch/orchestration. The P1-2
// remediation consolidated these zeroing writes into three named helpers
// (clearAgentLifecycleErrorLocked / clearAgentStopReasonLocked /
// clearAgentTurnStateLocked) in runtime.go. The two hottest files —
// launch_helpers.go and process_lifecycle.go — must not regress to raw
// agent.X = zero assignments.
//
// Scope is intentionally narrow (only these two files) to avoid churn while
// still locking down the treated hot paths.
func TestAgentFieldWritesCentralized(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	scoped := []string{
		filepath.Join("cmd", "mcp-orch", "orchestration", "launch_helpers.go"),
		filepath.Join("cmd", "mcp-orch", "orchestration", "process_lifecycle.go"),
	}

	// Each pattern matches `agent.<field> = <zero-value>` with any amount of
	// horizontal whitespace. We forbid these specific zeroings; assignment to
	// non-zero values (e.g. agent.lastError = err.Error()) is still allowed.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`agent\.lastError\s*=\s*""`),
		regexp.MustCompile(`agent\.stopRequested\s*=\s*false`),
		regexp.MustCompile(`agent\.stopReason\s*=\s*""`),
		regexp.MustCompile(`agent\.activeTurnID\s*=\s*""`),
		regexp.MustCompile(`agent\.threadID\s*=\s*""`),
		regexp.MustCompile(`agent\.exitedAt\s*=\s*nil`),
	}

	var violations []string
	for _, rel := range scoped {
		abs := filepath.Join(root, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, re := range patterns {
				if re.MatchString(line) {
					violations = append(violations,
						rel+":"+itoaAFW(lineNo+1)+": direct zero-write bypasses agent-state helper: "+trimmed)
				}
			}
		}
	}

	if len(violations) > 0 {
		t.Fatalf("agent field zero-write guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func itoaAFW(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
