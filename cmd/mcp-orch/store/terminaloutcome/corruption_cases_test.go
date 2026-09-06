package terminaloutcome

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type terminalOutboxCorruptionCase struct {
	name        string
	withPrivate bool
	mutate      func(*testing.T, *sql.DB, int64, contract.TerminalOutcomeCommit)
}

func terminalOutboxCorruptionCases() []terminalOutboxCorruptionCase {
	cases := append([]terminalOutboxCorruptionCase(nil), terminalOutboxRowCorruptionCases()...)
	cases = append(cases, terminalOutboxPayloadCorruptionCases()...)
	cases = append(cases, terminalOutboxHistoryCorruptionCases()...)
	return append(cases, terminalOutboxPrivateCorruptionCases()...)
}

func terminalOutboxRowCorruptionCases() []terminalOutboxCorruptionCase {
	return []terminalOutboxCorruptionCase{
		{
			name: "outbox terminal identity",
			mutate: func(t *testing.T, db *sql.DB, id int64, _ contract.TerminalOutcomeCommit) {
				mustExecTerminalCorruption(t, db, "UPDATE terminal_outcome_outbox_v2 SET terminal_identity = 'foreign-identity' WHERE id = ?", id)
			},
		},
		{
			name: "outbox event id",
			mutate: func(t *testing.T, db *sql.DB, id int64, _ contract.TerminalOutcomeCommit) {
				mustExecTerminalCorruption(t, db, "UPDATE terminal_outcome_outbox_v2 SET event_id = 'foreign-event' WHERE id = ?", id)
			},
		},
	}
}

func terminalOutboxPayloadCorruptionCases() []terminalOutboxCorruptionCase {
	mutations := []struct {
		name   string
		mutate func(*contract.TerminalOutcomeCommit)
	}{
		{name: "agent", mutate: func(value *contract.TerminalOutcomeCommit) { value.Identity.AgentID = "foreign-agent" }},
		{name: "thread", mutate: func(value *contract.TerminalOutcomeCommit) { value.Identity.PublicThreadID = "foreign-thread" }},
		{name: "turn", mutate: func(value *contract.TerminalOutcomeCommit) { value.Identity.ProviderTurnID = "foreign-turn" }},
		{name: "session", mutate: func(value *contract.TerminalOutcomeCommit) { value.Identity.SessionID = "foreign-session" }},
		{name: "generation", mutate: func(value *contract.TerminalOutcomeCommit) { value.Identity.Generation++ }},
	}
	cases := make([]terminalOutboxCorruptionCase, 0, len(mutations))
	for _, mutation := range mutations {
		mutation := mutation
		cases = append(cases, terminalOutboxCorruptionCase{
			name: "public payload " + mutation.name,
			mutate: func(t *testing.T, db *sql.DB, id int64, _ contract.TerminalOutcomeCommit) {
				mutateTerminalPublicPayload(t, db, id, mutation.mutate)
			},
		})
	}
	return cases
}

func terminalOutboxHistoryCorruptionCases() []terminalOutboxCorruptionCase {
	return []terminalOutboxCorruptionCase{
		terminalOutboxHistoryFieldCase("agent", "agent_id = 'foreign-agent'"),
		terminalOutboxHistoryFieldCase("event id", "event_id = 'foreign-event'"),
		terminalOutboxHistoryFieldCase("terminal identity", "terminal_identity = 'foreign-identity'"),
		terminalOutboxHistoryFieldCase("thread", "public_thread_id = 'foreign-thread'"),
		terminalOutboxHistoryFieldCase("turn", "provider_turn_id = 'foreign-turn'"),
		terminalOutboxHistoryFieldCase("session", "session_id = 'foreign-session'"),
		terminalOutboxHistoryFieldCase("generation", "generation = generation + 1"),
		terminalOutboxHistoryFieldCase("generation malformed", "generation = 'bad-generation'"),
		terminalOutboxHistoryFieldCase("public report", "public_report = 'tampered report'"),
	}
}

func terminalOutboxHistoryFieldCase(name, assignment string) terminalOutboxCorruptionCase {
	return terminalOutboxCorruptionCase{
		name: "history " + name,
		mutate: func(t *testing.T, db *sql.DB, _ int64, commit contract.TerminalOutcomeCommit) {
			mustExecTerminalCorruption(t, db, "UPDATE public_terminal_outcome_history SET "+assignment+" WHERE terminal_identity = ?", commit.Identity.TerminalIdentity)
		},
	}
}

func terminalOutboxPrivateCorruptionCases() []terminalOutboxCorruptionCase {
	return []terminalOutboxCorruptionCase{
		terminalOutboxPrivateFieldCase("row identity", "owner_agent_id = 'foreign-agent'"),
		terminalOutboxPrivateFieldCase("row thread", "public_thread_id = 'foreign-thread'"),
		terminalOutboxPrivateFieldCase("row turn", "provider_turn_id = 'foreign-turn'"),
		{
			name:        "private payload identity",
			withPrivate: true,
			mutate: func(t *testing.T, db *sql.DB, _ int64, commit contract.TerminalOutcomeCommit) {
				mustExecTerminalCorruption(t, db, "UPDATE terminal_outcome_private_dag_payloads SET payload_json = ? WHERE terminal_identity = ?", `{"ownerAgentId":"foreign-agent","publicThreadId":"`+commit.Identity.PublicThreadID+`","providerTurnId":"`+commit.Identity.ProviderTurnID+`","result":"owner artifact"}`, commit.Identity.TerminalIdentity)
			},
		},
		{
			name:        "missing private row",
			withPrivate: true,
			mutate: func(t *testing.T, db *sql.DB, id int64, _ contract.TerminalOutcomeCommit) {
				mustExecTerminalCorruption(t, db, "UPDATE terminal_outcome_outbox_v2 SET private_dag_payload_id = 999999 WHERE id = ?", id)
			},
		},
		{
			name:        "malformed private row id",
			withPrivate: true,
			mutate: func(t *testing.T, db *sql.DB, id int64, _ contract.TerminalOutcomeCommit) {
				mustExecTerminalCorruption(t, db, "UPDATE terminal_outcome_outbox_v2 SET private_dag_payload_id = 'bad-private-id' WHERE id = ?", id)
			},
		},
	}
}

func terminalOutboxPrivateFieldCase(name, assignment string) terminalOutboxCorruptionCase {
	return terminalOutboxCorruptionCase{
		name:        "private " + name,
		withPrivate: true,
		mutate: func(t *testing.T, db *sql.DB, _ int64, commit contract.TerminalOutcomeCommit) {
			mustExecTerminalCorruption(t, db, "UPDATE terminal_outcome_private_dag_payloads SET "+assignment+" WHERE terminal_identity = ?", commit.Identity.TerminalIdentity)
		},
	}
}

func runTerminalOutboxCorruptionCase(t *testing.T, test terminalOutboxCorruptionCase) {
	t.Helper()
	store, db := newTestStore(t)
	commit := terminalCommitFixture("terminal:corruption:" + strings.ReplaceAll(test.name, " ", "-"))
	if test.withPrivate {
		commit.PrivateDAG = &contract.OwnerScopedDAGPayload{
			OwnerAgentID: commit.Identity.AgentID, PublicThreadID: commit.Identity.PublicThreadID,
			ProviderTurnID: commit.Identity.ProviderTurnID, Result: "owner artifact",
		}
	}
	commit = activateCommitFixture(t, store, commit)
	result, err := store.CommitTerminalOutcome(context.Background(), commit)
	if err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	test.mutate(t, db, result.OutboxID, commit)
	items, err := store.ClaimTerminalOutcomeOutbox(context.Background(), "corruption-worker", time.Minute, 1)
	if err != nil {
		t.Fatalf("ClaimTerminalOutcomeOutbox() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("ClaimTerminalOutcomeOutbox() items = %#v, want poisoned item omitted", items)
	}
	assertTerminalOutboxPoisoned(t, db, result.OutboxID)
}
