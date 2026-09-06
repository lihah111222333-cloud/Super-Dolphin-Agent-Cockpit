//go:build e2e

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	agentstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TestStandaloneManagedCodexTurn 运行一次真实受管 provider turn，不通过 HTTP 客户端模拟执行器。
func TestStandaloneManagedCodexTurn(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	execution := standalone.NewExecution(cfg)
	if err := execution.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := execution.Close(); err != nil {
			t.Errorf("managed Codex cleanup: %v", err)
		}
	})
	terminal := make(chan turndto.TurnTerminalV2, 1)
	execution.BindTerminalSink(func(_ context.Context, event turndto.TurnTerminalV2) { terminal <- event })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	launch := contract.LaunchRequest{AgentID: "managed-codex", Name: "managed-codex", Cwd: workspace,
		Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=" + model}}
	result, err := execution.Launch(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	assertManagedSessionPersisted(t, ctx, db, launch.AgentID, result)
	turnID, err := execution.SubmitTurn(ctx, contract.TurnSubmission{AgentID: launch.AgentID,
		Inputs: []turndto.InputItem{{Type: "text", Content: "Reply with exactly ORCH_CODEX_OK and do not use tools."}}})
	if err != nil || turnID == "" {
		t.Fatalf("submit managed Codex turn: id=%q err=%v", turnID, err)
	}
	select {
	case event := <-terminal:
		if event.Outcome != "success" || strings.TrimSpace(event.PublicSummary) == "" {
			t.Fatalf("managed Codex terminal=%#v", event)
		}
	case <-ctx.Done():
		t.Fatalf("managed Codex turn timeout: %v", ctx.Err())
	}
}

// TestStandaloneManagedCodexFork 验证 standalone 的 forked launch 使用真实 Codex thread/fork。
func TestStandaloneManagedCodexFork(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	execution := standalone.NewExecution(cfg)
	if err := execution.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := execution.Close(); err != nil {
			t.Errorf("managed Codex fork cleanup: %v", err)
		}
	})
	terminal := make(chan turndto.TurnTerminalV2, 4)
	execution.BindTerminalSink(func(_ context.Context, event turndto.TurnTerminalV2) { terminal <- event })
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	parent := contract.LaunchRequest{AgentID: "managed-codex-parent", Name: "managed-codex-parent", Cwd: workspace,
		Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=" + model}}
	parentResult := launchManagedCodexParent(t, ctx, execution, terminal, parent)
	child := contract.LaunchRequest{AgentID: "managed-codex-child", Name: "managed-codex-child", ParentID: parent.AgentID,
		Cwd: workspace, Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=" + model}}
	childResult := forkManagedCodexChild(t, ctx, execution, parent.AgentID, child)
	assertManagedCodexForkPersistence(t, ctx, db, parent, child, parentResult, childResult)
	if _, err := execution.SubmitTurn(ctx, contract.TurnSubmission{AgentID: child.AgentID,
		Inputs: []turndto.InputItem{{Type: "text", Content: "Reply briefly that the forked Codex thread is active. Do not use tools."}}}); err != nil {
		t.Fatal(err)
	}
	waitManagedCodexSuccess(t, ctx, terminal, "child")
}

// TestStandaloneManagedCodexResume 验证新 Execution 可用持久 thread/resume 接管旧 daemon 的身份。
func TestStandaloneManagedCodexResume(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	parent := contract.LaunchRequest{AgentID: "managed-codex-resume", Name: "managed-codex-resume", Cwd: workspace,
		Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=" + model}}
	first := standalone.NewExecution(cfg)
	if err := first.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		t.Fatal(err)
	}
	firstTerminal := make(chan turndto.TurnTerminalV2, 1)
	first.BindTerminalSink(func(_ context.Context, event turndto.TurnTerminalV2) { firstTerminal <- event })
	parentResult := launchManagedCodexParent(t, ctx, first, firstTerminal, parent)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := standalone.NewExecution(cfg)
	if err := second.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		t.Fatal(err)
	}
	secondTerminal := make(chan turndto.TurnTerminalV2, 1)
	second.BindTerminalSink(func(_ context.Context, event turndto.TurnTerminalV2) { secondTerminal <- event })
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("managed Codex resume cleanup: %v", err)
		}
	})
	activation := orchestration.PersistedRuntimeActivation{AgentID: parent.AgentID, ThreadID: parentResult.ThreadID,
		Provider: "codex", CWD: workspace}
	if err := second.ActivatePersistedRuntime(ctx, activation); err != nil {
		t.Fatal(err)
	}
	if !second.IsRunning(ctx, parent.AgentID) {
		t.Fatal("resumed Codex session is not running")
	}
	if _, err := second.SubmitTurn(ctx, contract.TurnSubmission{AgentID: parent.AgentID,
		Inputs: []turndto.InputItem{{Type: "text", Content: "Reply briefly that the resumed Codex thread is active. Do not use tools."}}}); err != nil {
		t.Fatal(err)
	}
	waitManagedCodexSuccess(t, ctx, secondTerminal, "resumed")
}

// TestStandaloneManagedCodexArchive 验证 Codex thread/archive 成功后执行器确实停止。
func TestStandaloneManagedCodexArchive(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	execution := standalone.NewExecution(cfg)
	if err := execution.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := execution.Close(); err != nil {
			t.Errorf("managed Codex archive cleanup: %v", err)
		}
	})
	terminal := make(chan turndto.TurnTerminalV2, 1)
	execution.BindTerminalSink(func(_ context.Context, event turndto.TurnTerminalV2) { terminal <- event })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	launch := contract.LaunchRequest{AgentID: "managed-codex-archive", Name: "managed-codex-archive", Cwd: workspace,
		Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=" + model}}
	result, err := execution.Launch(ctx, launch)
	if err != nil {
		t.Fatal(err)
	}
	assertManagedSessionPersisted(t, ctx, db, launch.AgentID, result)
	if _, err := execution.SubmitTurn(ctx, contract.TurnSubmission{AgentID: launch.AgentID,
		Inputs: []turndto.InputItem{{Type: "text", Content: "Reply briefly that the Codex archive test is ready. Do not use tools."}}}); err != nil {
		t.Fatal(err)
	}
	waitManagedCodexSuccess(t, ctx, terminal, "archive precondition")
	if err := execution.Archive(ctx, launch.AgentID); err != nil {
		t.Fatalf("archive managed Codex thread: %v", err)
	}
	if execution.IsRunning(ctx, launch.AgentID) {
		t.Fatal("Codex execution remained running after archive")
	}
}

// TestStandaloneManagedCodexInterrupt 验证真实 Codex turn/interrupt 的终态回传和请求围栏。
func TestStandaloneManagedCodexInterrupt(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CODEX")
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: cli, Home: home, Models: []string{model}, Sandbox: "workspace-write"}}}
	db := managedProviderDatabase(t, state)
	execution := standalone.NewExecution(cfg)
	if err := execution.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := execution.Close(); err != nil {
			t.Errorf("managed Codex interrupt cleanup: %v", err)
		}
	})
	terminal := make(chan turndto.TurnTerminalV2, 1)
	execution.BindTerminalSink(func(_ context.Context, event turndto.TurnTerminalV2) { terminal <- event })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	launch := contract.LaunchRequest{AgentID: "managed-codex-interrupt", Name: "managed-codex-interrupt", Cwd: workspace,
		Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=" + model}}
	if _, err := execution.Launch(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.SubmitTurn(ctx, contract.TurnSubmission{AgentID: launch.AgentID,
		Inputs: []turndto.InputItem{{Type: "text", Content: "Use the shell tool to run sleep 20 before replying. Do not answer until the command finishes."}}}); err != nil {
		t.Fatal(err)
	}
	if err := interruptManagedCodexTurn(ctx, execution, launch.AgentID); err != nil {
		t.Fatalf("interrupt managed Codex turn: %v", err)
	}
	assertManagedCodexInterrupted(t, ctx, terminal)
}

func interruptManagedCodexTurn(ctx context.Context, execution *standalone.Execution, agentID string) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := execution.Interrupt(ctx, agentID, "user_request")
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "no active turn") {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return err
		case <-ticker.C:
		}
	}
}

func assertManagedCodexInterrupted(t *testing.T, ctx context.Context, terminal <-chan turndto.TurnTerminalV2) {
	t.Helper()
	select {
	case event := <-terminal:
		if event.Outcome != "interrupted" || event.TerminationCause != "user_request" || strings.TrimSpace(event.TerminationRequestID) == "" {
			t.Fatalf("managed Codex interrupt terminal=%#v", event)
		}
	case <-ctx.Done():
		t.Fatalf("managed Codex interrupt timeout: %v", ctx.Err())
	}
}

func launchManagedCodexParent(t *testing.T, ctx context.Context, execution *standalone.Execution, terminal chan turndto.TurnTerminalV2, parent contract.LaunchRequest) orchestration.LaunchResult {
	t.Helper()
	result, err := execution.Launch(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execution.SubmitTurn(ctx, contract.TurnSubmission{AgentID: parent.AgentID,
		Inputs: []turndto.InputItem{{Type: "text", Content: "Reply briefly that the parent Codex thread is ready for forking. Do not use tools."}}}); err != nil {
		t.Fatal(err)
	}
	waitManagedCodexSuccess(t, ctx, terminal, "parent")
	return result
}

func forkManagedCodexChild(t *testing.T, ctx context.Context, execution *standalone.Execution, parentID string, child contract.LaunchRequest) orchestration.LaunchResult {
	t.Helper()
	result, err := execution.Fork(ctx, parentID, child)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertManagedCodexForkPersistence(t *testing.T, ctx context.Context, db *sql.DB, parent, child contract.LaunchRequest, parentResult, childResult orchestration.LaunchResult) {
	t.Helper()
	if childResult.ThreadID == "" || childResult.ThreadID == parentResult.ThreadID {
		t.Fatalf("fork result = %#v, want distinct child thread", childResult)
	}
	assertManagedSessionPersisted(t, ctx, db, child.AgentID, childResult)
	thread, err := agentstore.NewThreadStore(db).GetByThreadID(ctx, childResult.ThreadID)
	if err != nil || thread.ParentAgentID != parent.AgentID {
		t.Fatalf("forked thread parent = %#v err=%v, want %q", thread, err, parent.AgentID)
	}
}

func waitManagedCodexSuccess(t *testing.T, ctx context.Context, terminal <-chan turndto.TurnTerminalV2, label string) {
	t.Helper()
	select {
	case event := <-terminal:
		if event.Outcome != "success" || strings.TrimSpace(event.PublicSummary) == "" {
			t.Fatalf("managed Codex %s terminal=%#v", label, event)
		}
	case <-ctx.Done():
		t.Fatalf("managed Codex %s turn timeout: %v", label, ctx.Err())
	}
}

func requireManagedProviderInputs(t *testing.T, prefix string) (string, string, string) {
	t.Helper()
	cli, home, model := os.Getenv("ORCH_E2E_"+prefix+"_BIN"), os.Getenv("ORCH_E2E_"+prefix+"_HOME"), os.Getenv("ORCH_E2E_"+prefix+"_MODEL")
	if !filepath.IsAbs(cli) || !filepath.IsAbs(home) || strings.TrimSpace(model) == "" {
		t.Fatalf("ORCH_E2E_%s_BIN, _HOME and _MODEL must be explicit absolute provider inputs", prefix)
	}
	return cli, home, strings.TrimSpace(model)
}

func managedProviderDatabase(t *testing.T, state string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(state, "orch.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertManagedSessionPersisted(t *testing.T, ctx context.Context, db *sql.DB, agentID string, result orchestration.LaunchResult) {
	t.Helper()
	thread, err := agentstore.NewThreadStore(db).GetByThreadID(ctx, result.ThreadID)
	if err != nil || thread.AgentID != agentID {
		t.Fatalf("managed session persistence: thread=%#v err=%v", thread, err)
	}
}
