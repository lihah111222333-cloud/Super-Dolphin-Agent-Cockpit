//go:build e2e

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	agentstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/terminaloutcome"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestStandaloneBinaryFullClaudeLifecycle is the explicit, binary-only Claude
// lifecycle selector. It verifies canonical terminal history and its report fence,
// plus the independent operator source lifecycle. Neither is an OUT/provider receipt
// or a proof that terminal identity is authorized by that operator source.
func TestStandaloneBinaryFullClaudeLifecycle(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	t.Cleanup(cancel)
	fixture := writeManagedServeE2EFixture(t, "claude", cli, home, model)
	daemon := startServeE2E(t, ctx, binary, fixture)
	client := &http.Client{Timeout: 120 * time.Second}
	session, _ := initializeServeE2E(t, client, daemon.ready.Endpoint, fixture.token)
	db, sources, source := openStandaloneClaudeEvidenceStore(t, ctx, fixture)
	launchManagedClaudeThroughMCP(t, client, daemon.ready.Endpoint, fixture, session, model)
	first := waitManagedClaudeReportThroughMCP(t, ctx, client, daemon.ready.Endpoint, fixture.token, session, "daemon-managed-claude", 0, "DAEMON_CLAUDE_TURN_OK")
	waitManagedClaudeRuntimeIdleThroughMCP(t, ctx, client, daemon.ready.Endpoint, fixture.token, session, "daemon-managed-claude")
	firstOutcome := readStandaloneClaudeFirstEvidence(t, ctx, db, fixture.workspace, first)
	observed := assertStandaloneManagedProcessTree(t, daemon, cli)
	second := sendManagedClaudeFollowUpThroughMCP(t, client, daemon.ready.Endpoint, fixture.token, session)
	if second.ReportSeq <= first || !strings.Contains(second.Report, "DAEMON_CLAUDE_SECOND_OK") {
		t.Fatalf("follow-up report invalid: seq=%d first=%d report=%q", second.ReportSeq, first, second.Report)
	}
	secondOutcome, err := readStandaloneClaudeTerminalEvidence(ctx, db, firstOutcome.Identity.AgentID, firstOutcome.Identity.PublicThreadID, second.ReportSeq, second.Report)
	if err != nil {
		t.Fatal(err)
	}
	assertStandaloneClaudeFollowUpIdentity(t, firstOutcome, secondOutcome)
	h := standaloneClaudePublicLifecycle{t: t, ctx: ctx, db: db, fixture: fixture, daemon: daemon, client: client, session: session, agentID: second.AgentID, requestID: 1000}
	h.stopAndCheckRecovery(secondOutcome)
	stopStandaloneClaudeEvidenceDaemon(t, ctx, daemon, client, sources, source)
	assertNoStandaloneManagedDescendants(t, observed)
}

// readStandaloneClaudeTerminalEvidence 读取生产 owner 的 head/history/outbox 身份闭包，不扫描文件或构造 OUT 回执。
func readStandaloneClaudeTerminalEvidence(ctx context.Context, db *sql.DB, agentID, threadID string, reportSeq int64, content string) (contract.TerminalOutcomeCommit, error) {
	outcome, err := terminaloutcome.New(db).GetPublicTerminalOutcome(ctx, agentID)
	if err != nil {
		return contract.TerminalOutcomeCommit{}, fmt.Errorf("missing or invalid canonical terminal persistence: %w", err)
	}
	if err := outcome.Validate(); err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	if err := validateStandaloneClaudeTerminalEvidence(outcome, agentID, threadID, reportSeq, content); err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	return outcome, nil
}

// openStandaloneClaudeEvidenceStore 精确打开该 daemon 配置的数据库并验证当前 operator source。
func openStandaloneClaudeEvidenceStore(t *testing.T, ctx context.Context, fixture serveE2EFixture) (*sql.DB, *operatorsource.Store, operatorsource.Source) {
	t.Helper()
	cfg, err := standalone.LoadConfig(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	db, err := newStandaloneDB(newStandalonePlatformConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	sources, err := operatorsource.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	source, err := sources.GetLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertStandaloneClaudeActiveSource(t, source, cfg)
	return db, sources, source
}

// readStandaloneClaudeFirstEvidence 核对本次 Claude launch 的持久绑定和首轮结果。
func readStandaloneClaudeFirstEvidence(t *testing.T, ctx context.Context, db *sql.DB, workspace string, first int64) contract.TerminalOutcomeCommit {
	t.Helper()
	binding, err := agentstore.NewBindingStore(db).GetByAgentID(ctx, "daemon-managed-claude")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Provider != "claude" || binding.Cwd != workspace || binding.ProviderThreadID == "" {
		t.Fatal("launch binding mismatch")
	}
	firstOutcome, err := readStandaloneClaudeTerminalEvidence(ctx, db, binding.AgentID, binding.ProviderThreadID, first, "DAEMON_CLAUDE_TURN_OK")
	if err != nil {
		t.Fatal(err)
	}
	return firstOutcome
}

// assertStandaloneClaudeFollowUpIdentity 拒绝第二轮重用旧终态或切换会话身份。
func assertStandaloneClaudeFollowUpIdentity(t *testing.T, firstOutcome, secondOutcome contract.TerminalOutcomeCommit) {
	t.Helper()
	if secondOutcome.Identity.ProviderTurnID == firstOutcome.Identity.ProviderTurnID || secondOutcome.Identity.EventID == firstOutcome.Identity.EventID || secondOutcome.Identity.TerminalIdentity == firstOutcome.Identity.TerminalIdentity || secondOutcome.Identity.SessionID != firstOutcome.Identity.SessionID || secondOutcome.Identity.Generation < firstOutcome.Identity.Generation {
		t.Fatal("follow-up reused a terminal identity or changed the session unexpectedly")
	}
}

// stopStandaloneClaudeEvidenceDaemon 检查 daemon 停止及精确 operator source 撤销。
func stopStandaloneClaudeEvidenceDaemon(t *testing.T, ctx context.Context, daemon *serveE2EProcess, client *http.Client, sources *operatorsource.Store, source operatorsource.Source) {
	t.Helper()
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatalf("stop binary: %v", err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
	stopped, err := sources.GetLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertOperatorStoppedSource(t, stopped, source)
	if stopped.Generation != source.Generation || stopped.SourceFenceRef != source.SourceFenceRef {
		t.Fatal("shutdown changed operator source identity")
	}
}

// validateStandaloneClaudeTerminalEvidence 将输入报告 fence 与持久内容逐一对应。
func validateStandaloneClaudeTerminalEvidence(outcome contract.TerminalOutcomeCommit, agentID, threadID string, reportSeq int64, content string) error {
	if agentID == "" || threadID == "" || reportSeq <= 0 || strings.TrimSpace(content) == "" || outcome.Identity.AgentID != agentID || outcome.Identity.PublicThreadID != threadID || outcome.Identity.HeadVersion != uint64(reportSeq) || outcome.PublicOutcome.Kind != "success" || !strings.Contains(outcome.PublicReport, content) {
		return fmt.Errorf("canonical terminal agent/thread/report fence/content mismatch")
	}
	return nil
}

// assertStandaloneClaudeActiveSource 核对 ready 所对应来源与显式配置身份。
func assertStandaloneClaudeActiveSource(t *testing.T, source operatorsource.Source, cfg standalone.Config) {
	t.Helper()
	if source.State != "active" || source.PrincipalRef != cfg.PrincipalRef || source.ScopeRef != cfg.ScopeRef || source.SourceID == "" || source.SourceFenceRef == "" || source.Generation <= 0 {
		t.Fatal("ready source does not match this daemon configuration")
	}
}
