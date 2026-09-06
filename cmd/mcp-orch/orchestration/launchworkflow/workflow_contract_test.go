package launchworkflow

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/launchassembler"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorreservation"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaunchWorkflowContract 验证真实 SQLite workflow 契约。
func TestLaunchWorkflowContract(t *testing.T) {
	wf, req := workflowFixture(t)
	got, err := wf.Run(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowResult(t, got)
	assertWorkflowRejects(t, wf, req)
}

// workflowFixture 创建完整持久化 owner 组合。
func workflowFixture(t *testing.T) (*Workflow, Request) {
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "workflow.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mustWorkflow(t, sqliteruntime.RunEmbeddedMigrations(t.Context(), db))
	srcs, err := operatorsource.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	src, err := srcs.Activate(t.Context(), operatorsource.ActivateRequest{PrincipalRef: "p", ScopeRef: "s", AuthContext: "a"})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := operatorreservation.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	rr := operatorreservation.Request{ReservationID: "r", SourceID: src.SourceID, SourceFenceRef: src.SourceFenceRef, PrincipalRef: "p", ScopeRef: "s", AuthContext: "a", Generation: src.Generation, WorkspaceRef: "w", CallerPrincipalRef: "p", IdempotencyNamespace: "n", IdempotencyKeyDigest: strings.Repeat("a", 64), ContentIntent: "launch", ContentProof: "proof", ContentCandidate: "content://x", ContentDigestKey: strings.Repeat("c", 64), SizeBytes: 1, MediaType: "text/plain", ProfileRef: "profile", QuotaRef: "q", RetentionSeconds: 60, Deadline: "2099-01-01T00:00:00.000000000Z", Source: operatorsource.Selector{SourceID: src.SourceID, SourceFenceRef: src.SourceFenceRef, PrincipalRef: "p", ScopeRef: "s", AuthContext: "a", Generation: src.Generation}}
	mustWorkflow(t, func() error { _, e := rs.Create(t.Context(), rr); return e }())
	cs, err := orchestrationcommand.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	as, err := launchassembler.New(cs, rs)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := identity.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := New(as, ids)
	if err != nil {
		t.Fatal(err)
	}
	return wf, Request{Intent: launchassembler.Request{CommandID: "cmd", CallerPrincipalRef: "p", ScopeRef: "s", IdempotencyNamespace: "n", IdempotencyDigest: strings.Repeat("a", 64), RequestDigest: strings.Repeat("b", 64), DigestKeyID: "k", ContentRef: "content://x", SourceFenceRef: src.SourceFenceRef, TargetFenceRef: src.SourceFenceRef, OperatorReservationID: "r", OperatorSourceID: src.SourceID, OperatorGeneration: src.Generation, ContentKind: "operator_content_ref", Profile: "profile"}, AgentID: "agent", IdentityKind: "managed_agent", ExternalIdentityRef: "ext", ExternalEpoch: "epoch", ReservationID: "res", IncarnationID: "inc", PreparationID: "prep", IdentityDigest: strings.Repeat("d", 64), BindingDigest: strings.Repeat("e", 64), ReservationGeneration: 1, IncarnationGeneration: 1, PreparationGeneration: 1, ExpectedIncarnationGeneration: 1}
}
func assertWorkflowResult(t *testing.T, got Result) {
	t.Helper()
	if got.Command.Intent.CommandID != "cmd" || got.Reservation.CommandID == nil || got.Incarnation.IncarnationID != "inc" || got.Preparation.PreparationID != "prep" {
		t.Fatalf("bad result: %#v", got)
	}
}
func assertWorkflowRejects(t *testing.T, wf *Workflow, req Request) {
	t.Helper()
	bad := req
	bad.AgentID = ""
	if _, err := wf.Run(t.Context(), bad); err == nil {
		t.Fatal("empty agent accepted")
	}
	stale := req
	stale.Intent.SourceFenceRef = "stale"
	if _, err := wf.Run(t.Context(), stale); err == nil {
		t.Fatal("stale fence accepted")
	}
}

// mustWorkflow fails a setup helper without hiding the actual error.
func mustWorkflow(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
