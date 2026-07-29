package terminaloutcome

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestTerminalCommitRequiresRealVersionedCurrentHead(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	commit := terminalCommitFixture("terminal:real-head")

	if _, err := store.CommitTerminalOutcome(ctx, commit); !errors.Is(err, contract.ErrTerminalOutcomeConflict) {
		t.Fatalf("commit without current head error = %v, want conflict", err)
	}
	assertRepairTableCount(t, db, "public_terminal_outcome_history", 0)
	assertRepairTableCount(t, db, "terminal_outcome_outbox_v2", 0)

	head, err := store.ActivateTerminalOutcomeHead(ctx, contract.TerminalOutcomeHeadActivation{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: commit.Identity.AgentID,
		PublicThreadID: commit.Identity.PublicThreadID, ProviderTurnID: commit.Identity.ProviderTurnID,
		SessionID: commit.Identity.SessionID, Generation: commit.Identity.Generation,
		ExpectedActiveState: commit.Identity.ExpectedActiveState, ActivatedAt: commit.OccurredAt.Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("ActivateTerminalOutcomeHead() error = %v", err)
	}
	commit.Identity.HeadVersion = head.Version
	if _, err := store.CommitTerminalOutcome(ctx, commit); err != nil {
		t.Fatalf("commit against real head error = %v", err)
	}
}

func TestSessionActivationHeadAdvancesToRealTurnHead(t *testing.T) {
	store, _ := newTestStore(t)
	commit := terminalCommitFixture("terminal:session-to-turn")
	sessionHead, err := store.ActivateTerminalOutcomeHead(context.Background(), contract.TerminalOutcomeHeadActivation{
		Capability: contract.TerminalOutcomeCapabilityV2, AgentID: commit.Identity.AgentID,
		PublicThreadID: commit.Identity.PublicThreadID, ProviderTurnID: "session-terminal:" + commit.Identity.SessionID,
		SessionID: commit.Identity.SessionID, Generation: commit.Identity.Generation,
		ExpectedActiveState: "idle", ActivatedAt: commit.OccurredAt.Add(-2 * time.Second),
	})
	if err != nil {
		t.Fatalf("activate session head: %v", err)
	}
	turnHead, err := store.ActivateTerminalOutcomeHead(context.Background(), activationFromCommit(commit))
	if err != nil {
		t.Fatalf("advance session head to turn: %v", err)
	}
	if turnHead.Version != sessionHead.Version+1 || turnHead.ProviderTurnID != commit.Identity.ProviderTurnID {
		t.Fatalf("turn head = %#v, want version %d and provider turn %q", turnHead, sessionHead.Version+1, commit.Identity.ProviderTurnID)
	}
	commit.Identity.HeadVersion = turnHead.Version
	if _, err := store.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("commit real turn head: %v", err)
	}
}

func TestTerminalCommitRealHeadMismatchMatrixHasZeroSideEffects(t *testing.T) {
	base := terminalCommitFixture("terminal:mismatch")
	tests := []struct {
		name   string
		mutate func(*contract.TerminalOutcomeCommit)
	}{
		{name: "agent", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.AgentID = "agent-other" }},
		{name: "thread", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.PublicThreadID = "thread-other" }},
		{name: "turn", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.ProviderTurnID = "turn-other" }},
		{name: "session", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.SessionID = "session-other" }},
		{name: "generation", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.Generation++ }},
		{name: "state", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.ExpectedActiveState = "awaiting_user_input" }},
		{name: "version", mutate: func(v *contract.TerminalOutcomeCommit) { v.Identity.HeadVersion++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, db := newTestStore(t)
			head, err := store.ActivateTerminalOutcomeHead(context.Background(), activationFromCommit(base))
			if err != nil {
				t.Fatalf("activate head: %v", err)
			}
			value := base
			value.Identity.HeadVersion = head.Version
			test.mutate(&value)
			if _, err := store.CommitTerminalOutcome(context.Background(), value); !errors.Is(err, contract.ErrTerminalOutcomeConflict) {
				t.Fatalf("mismatch commit error = %v, want conflict", err)
			}
			assertRepairTableCount(t, db, "public_terminal_outcome_history", 0)
			assertRepairTableCount(t, db, "terminal_outcome_outbox_v2", 0)
		})
	}
}

func TestSameAgentCanCompleteConsecutiveTurnsWithoutOldTerminalOverwritingCurrent(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	first := terminalCommitFixture("terminal:first")
	firstHead, err := store.ActivateTerminalOutcomeHead(ctx, activationFromCommit(first))
	if err != nil {
		t.Fatalf("activate first: %v", err)
	}
	first.Identity.HeadVersion = firstHead.Version
	if _, err := store.CommitTerminalOutcome(ctx, first); err != nil {
		t.Fatalf("commit first: %v", err)
	}
	for _, activation := range []contract.TerminalOutcomeHeadActivation{
		activationFromCommit(first),
		func() contract.TerminalOutcomeHeadActivation {
			value := activationFromCommit(first)
			value.PublicThreadID = "wrong-thread"
			return value
		}(),
		func() contract.TerminalOutcomeHeadActivation {
			value := activationFromCommit(first)
			value.SessionID = "wrong-session"
			return value
		}(),
	} {
		if _, err := store.ActivateTerminalOutcomeHead(ctx, activation); !errors.Is(err, contract.ErrTerminalOutcomeConflict) {
			t.Fatalf("reactivate completed turn with thread=%q session=%q error = %v, want conflict",
				activation.PublicThreadID, activation.SessionID, err)
		}
	}

	second := terminalCommitFixture("terminal:second")
	second.Identity.ProviderTurnID = "turn-2"
	second.Identity.TerminalIdentity = "terminal:second:identity"
	second.OccurredAt = second.OccurredAt.Add(time.Minute)
	second.PublicOutcome.CompletedAt = second.OccurredAt
	secondHead, err := store.ActivateTerminalOutcomeHead(ctx, activationFromCommit(second))
	if err != nil {
		t.Fatalf("activate second: %v", err)
	}
	if secondHead.Version <= firstHead.Version {
		t.Fatalf("second head version = %d, want > %d", secondHead.Version, firstHead.Version)
	}
	if _, err := store.GetPublicTerminalOutcome(ctx, first.Identity.AgentID); !errors.Is(err, contract.ErrTerminalOutcomeActive) {
		t.Fatalf("current read while second running error = %v, want active", err)
	}
	second.Identity.HeadVersion = secondHead.Version
	if _, err := store.CommitTerminalOutcome(ctx, second); err != nil {
		t.Fatalf("commit second: %v", err)
	}
	assertRepairTableCount(t, db, "public_terminal_outcome_history", 2)
	current, err := store.GetPublicTerminalOutcome(ctx, first.Identity.AgentID)
	if err != nil || current.Identity.EventID != second.Identity.EventID {
		t.Fatalf("current terminal = %#v, %v; want second", current, err)
	}
}

func TestOutboxClaimTokenFencesExpiredOwner(t *testing.T) {
	store, db := newTestStore(t)
	commit := terminalCommitFixture("terminal:claim-token")
	head, err := store.ActivateTerminalOutcomeHead(context.Background(), activationFromCommit(commit))
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	commit.Identity.HeadVersion = head.Version
	if _, err := store.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("commit: %v", err)
	}
	first, err := store.ClaimTerminalOutcomeOutbox(context.Background(), "worker-a", time.Minute, 1)
	if err != nil || len(first) != 1 || first[0].ClaimToken == "" {
		t.Fatalf("first claim = %#v, %v", first, err)
	}
	if _, err := db.Exec("UPDATE terminal_outcome_outbox_v2 SET lease_expires_at = 1 WHERE id = ?", first[0].ID); err != nil {
		t.Fatalf("expire first: %v", err)
	}
	if _, err := store.RenewTerminalOutcomeOutbox(context.Background(), first[0].ID, "worker-a", first[0].ClaimToken, time.Minute); !errors.Is(err, contract.ErrTerminalOutboxFence) {
		t.Fatalf("expired renew error = %v, want fence", err)
	}
	second, err := store.ClaimTerminalOutcomeOutbox(context.Background(), "worker-b", time.Minute, 1)
	if err != nil || len(second) != 1 || second[0].ClaimToken == first[0].ClaimToken {
		t.Fatalf("second claim = %#v, %v", second, err)
	}
	if err := store.MarkTerminalOutcomeProjected(context.Background(), first[0].ID, "worker-a", first[0].ClaimToken); !errors.Is(err, contract.ErrTerminalOutboxFence) {
		t.Fatalf("late ACK error = %v, want fence", err)
	}
	if _, err := store.RenewTerminalOutcomeOutbox(context.Background(), second[0].ID, "worker-b", second[0].ClaimToken, time.Minute); err != nil {
		t.Fatalf("current renew error = %v", err)
	}
	if err := store.MarkTerminalOutcomeProjected(context.Background(), second[0].ID, "worker-b", second[0].ClaimToken); err != nil {
		t.Fatalf("current ACK error = %v", err)
	}
}

func TestPoisonOutboxItemDoesNotBlockLaterValidItem(t *testing.T) {
	store, db := newTestStore(t)
	first := activateCommitFixture(t, store, terminalCommitFixture("terminal:poison-first"))
	firstResult, err := store.CommitTerminalOutcome(context.Background(), first)
	if err != nil {
		t.Fatalf("commit first: %v", err)
	}
	second := terminalCommitFixture("terminal:poison-second")
	second.Identity.ProviderTurnID = "turn-2"
	second.Identity.TerminalIdentity = "terminal:poison-second:identity"
	second.OccurredAt = second.OccurredAt.Add(time.Minute)
	second = activateCommitFixture(t, store, second)
	secondResult, err := store.CommitTerminalOutcome(context.Background(), second)
	if err != nil {
		t.Fatalf("commit second: %v", err)
	}
	if _, err := db.Exec("UPDATE terminal_outcome_outbox_v2 SET public_payload_json = ? WHERE id = ?",
		`{"unknown":"`+terminalSecretFixture+`"}`, firstResult.OutboxID); err != nil {
		t.Fatalf("corrupt first outbox: %v", err)
	}
	items, err := store.ClaimTerminalOutcomeOutbox(context.Background(), "worker-poison", time.Minute, 10)
	if err != nil {
		t.Fatalf("claim with poison: %v", err)
	}
	if len(items) != 1 || items[0].ID != secondResult.OutboxID {
		t.Fatalf("claimed items = %#v, want only later valid item %d", items, secondResult.OutboxID)
	}
	var status, lastError string
	if err := db.QueryRow("SELECT status, last_error FROM terminal_outcome_outbox_v2 WHERE id = ?", firstResult.OutboxID).Scan(&status, &lastError); err != nil {
		t.Fatalf("read poison status: %v", err)
	}
	if status != "poisoned" || strings.Contains(lastError, terminalSecretFixture) {
		t.Fatalf("poison record = status:%q error:%q", status, lastError)
	}
}

func activationFromCommit(commit contract.TerminalOutcomeCommit) contract.TerminalOutcomeHeadActivation {
	return contract.TerminalOutcomeHeadActivation{
		Capability: commit.Identity.Capability, AgentID: commit.Identity.AgentID,
		PublicThreadID: commit.Identity.PublicThreadID, ProviderTurnID: commit.Identity.ProviderTurnID,
		SessionID: commit.Identity.SessionID, Generation: commit.Identity.Generation,
		ExpectedActiveState: commit.Identity.ExpectedActiveState, ActivatedAt: commit.OccurredAt.Add(-time.Second),
	}
}

func assertRepairTableCount(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
