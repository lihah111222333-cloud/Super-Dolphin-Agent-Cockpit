package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestProcessTerminalOutcomeOutboxContinuesAfterOneItemFailure(t *testing.T) {
	firstErr := errors.New("first terminal outbox item unavailable")
	port := &terminalOutcomeBatchPort{
		items:    []contract.TerminalOutcomeOutboxItem{{ID: 1}, {ID: 2}},
		renewErr: map[int64]error{1: firstErr},
	}
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.terminalOutcomes = port

	err := svc.ProcessTerminalOutcomeOutbox(context.Background(), "batch-worker", time.Minute, 2)
	if !errors.Is(err, firstErr) {
		t.Fatalf("ProcessTerminalOutcomeOutbox() error = %v, want first item error", err)
	}
	if len(port.renewed) != 2 {
		t.Fatalf("renew calls = %v, want both items attempted", port.renewed)
	}
	if len(port.acked) != 1 || port.acked[0] != 2 {
		t.Fatalf("ACK calls = %v, want only later valid item", port.acked)
	}
}

type terminalOutcomeBatchPort struct {
	items    []contract.TerminalOutcomeOutboxItem
	renewErr map[int64]error
	renewed  []int64
	acked    []int64
}

func (p *terminalOutcomeBatchPort) ActivateTerminalOutcomeHead(context.Context, contract.TerminalOutcomeHeadActivation) (contract.TerminalOutcomeHead, error) {
	return contract.TerminalOutcomeHead{}, nil
}

func (p *terminalOutcomeBatchPort) CommitTerminalOutcome(context.Context, contract.TerminalOutcomeCommit) (contract.TerminalOutcomeCommitResult, error) {
	return contract.TerminalOutcomeCommitResult{}, nil
}

func (p *terminalOutcomeBatchPort) GetPublicTerminalOutcome(context.Context, string) (contract.TerminalOutcomeCommit, error) {
	return contract.TerminalOutcomeCommit{}, nil
}

func (p *terminalOutcomeBatchPort) ClaimTerminalOutcomeOutbox(context.Context, string, time.Duration, int) ([]contract.TerminalOutcomeOutboxItem, error) {
	return p.items, nil
}

func (p *terminalOutcomeBatchPort) RenewTerminalOutcomeOutbox(_ context.Context, id int64, _ string, _ string, lease time.Duration) (time.Time, error) {
	p.renewed = append(p.renewed, id)
	if err := p.renewErr[id]; err != nil {
		return time.Time{}, err
	}
	return time.Now().UTC().Add(lease), nil
}

func (p *terminalOutcomeBatchPort) MarkTerminalOutcomeProjected(_ context.Context, id int64, _ string, _ string) error {
	p.acked = append(p.acked, id)
	return nil
}

var _ contract.TerminalOutcomeCommitPort = (*terminalOutcomeBatchPort)(nil)
