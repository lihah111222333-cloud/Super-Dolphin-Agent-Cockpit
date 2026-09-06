package orchestration

import (
	"context"
	"testing"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type cliBackendFixture struct {
	sink     func(context.Context, turndto.TurnTerminalV2)
	exitSink func(contract.CLIProcessExit)
	running  bool
}

type cliReceiptBackendFixture struct {
	cliBackendFixture
	receiptSink func(context.Context, turndto.TurnTerminalV2) error
}

func (b *cliReceiptBackendFixture) BindTerminalSinkWithReceipt(sink func(context.Context, turndto.TurnTerminalV2) error) {
	b.receiptSink = sink
}

func (b *cliBackendFixture) Launch(context.Context, LaunchRequest) (LaunchResult, error) {
	b.running = true
	return LaunchResult{ThreadID: "cli-thread", RemoteAgentID: "cli-agent"}, nil
}
func (b *cliBackendFixture) Fork(ctx context.Context, _ string, req LaunchRequest) (LaunchResult, error) {
	return b.Launch(ctx, req)
}
func (b *cliBackendFixture) Stop(context.Context, string) error            { b.running = false; return nil }
func (b *cliBackendFixture) Archive(ctx context.Context, id string) error  { return b.Stop(ctx, id) }
func (*cliBackendFixture) Interrupt(context.Context, string, string) error { return nil }
func (*cliBackendFixture) SubmitTurn(context.Context, TurnSubmission) (string, error) {
	return "cli-turn", nil
}
func (b *cliBackendFixture) IsRunning(context.Context, string) bool { return b.running }
func (b *cliBackendFixture) BindTerminalSink(sink func(context.Context, turndto.TurnTerminalV2)) {
	b.sink = sink
}
func (b *cliBackendFixture) BindProcessExitSink(sink func(contract.CLIProcessExit)) {
	b.exitSink = sink
}

func TestCLILauncherBindsSessionGeneration(t *testing.T) {
	backend := &cliBackendFixture{}
	launcher, err := NewCLILauncher(backend)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	_, err = svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "cli-agent", Name: "CLI", Cwd: t.TempDir(), Command: []string{"unused"},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := svc.registry.agents["cli-agent"]
	if agent == nil || agent.sessionGeneration == 0 || agent.sessionGeneration != agent.launchSeq {
		t.Fatal("CLI session generation was not bound before first turn")
	}
	if backend.sink == nil {
		t.Fatal("CLI terminal sink was not bound")
	}
}

func TestCLILauncherPreservesReceiptCapableBackend(t *testing.T) {
	backend := &cliReceiptBackendFixture{}
	launcher, err := NewCLILauncher(backend)
	if err != nil {
		t.Fatal(err)
	}
	_ = NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	if backend.receiptSink == nil {
		t.Fatal("CLI launcher did not bind receipt-capable terminal sink")
	}
	if backend.sink != nil {
		t.Fatal("CLI launcher also bound legacy void terminal sink")
	}
	if backend.exitSink == nil {
		t.Fatal("CLI launcher did not bind process-exit sink alongside receipt sink")
	}
}
