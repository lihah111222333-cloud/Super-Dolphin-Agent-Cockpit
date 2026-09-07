//go:build e2e

package main

import (
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/terminaloutcome"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestStandaloneClaudeTerminalEvidenceRejectsUnrelatedPersistence 用真实 SQLite owner 锁定旧 turn、异任务与 schema 关键词不能充当执行证据。
func TestStandaloneClaudeTerminalEvidenceRejectsUnrelatedPersistence(t *testing.T) {
	db := managedProviderDatabase(t, t.TempDir())
	ctx := t.Context()
	// 迁移创建的全部 schema 以及三个关键词均不能替代持久 terminal 行。
	if _, err := db.ExecContext(ctx, `CREATE TABLE source_identity_content (content TEXT); INSERT INTO source_identity_content VALUES ('source identity content')`); err != nil {
		t.Fatal(err)
	}
	if _, err := readStandaloneClaudeTerminalEvidence(ctx, db, "agent", "thread", 1, "source identity content"); err == nil {
		t.Fatal("schema keywords passed as terminal evidence")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	store := terminaloutcome.New(db)
	activation := contract.TerminalOutcomeHeadActivation{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: "agent", PublicThreadID: "thread", ProviderTurnID: "turn-1", SessionID: "session", Generation: 1, ExpectedActiveState: "turn_running", ActivatedAt: now,
	}
	head, err := store.ActivateTerminalOutcomeHead(ctx, activation)
	if err != nil {
		t.Fatal(err)
	}
	commit := contract.TerminalOutcomeCommit{
		SchemaVersion: 2, ProjectionKind: "turn_completed",
		Identity:      contract.CanonicalTerminalIdentity{Capability: activation.Capability, AgentID: activation.AgentID, PublicThreadID: activation.PublicThreadID, ProviderTurnID: activation.ProviderTurnID, SessionID: activation.SessionID, Generation: activation.Generation, EventID: "event-1", TerminalIdentity: "terminal-1", ExpectedActiveState: activation.ExpectedActiveState, HeadVersion: head.Version},
		PublicOutcome: contract.PublicOutcome{Kind: "success", Code: "success", Summary: "first report", CompletedAt: now}, PublicReport: "first report", OccurredAt: now,
	}
	if _, err := store.CommitTerminalOutcome(ctx, commit); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, agent, thread, content string
		seq                          int64
		wantError                    bool
	}{
		{"exact-current", "agent", "thread", "first report", int64(head.Version), false},
		{"wrong-agent", "foreign-agent", "thread", "first report", int64(head.Version), true},
		{"wrong-thread", "agent", "foreign-thread", "first report", int64(head.Version), true},
		{"old-turn-for-new-report", "agent", "thread", "first report", int64(head.Version) + 1, true},
		{"wrong-content", "agent", "thread", "second report", int64(head.Version), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readStandaloneClaudeTerminalEvidence(ctx, db, tc.agent, tc.thread, tc.seq, tc.content)
			if (err != nil) != tc.wantError {
				t.Fatalf("evidence error=%v, want error=%t", err, tc.wantError)
			}
		})
	}
}
