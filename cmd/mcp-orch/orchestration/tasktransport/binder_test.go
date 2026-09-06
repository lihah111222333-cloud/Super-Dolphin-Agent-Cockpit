package tasktransport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestBindTaskToolScopeProjectsVerifiedAttribution(t *testing.T) {
	attr := validAttribution(t, "task/verified")
	ctx, err := common.BindCallerAttribution(context.Background(), attr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = bindPrincipal(ctx, "principal/verified")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindTaskToolScope(ctx, BindRequest{Template: ScopeTemplate{
		Family: "mcp-orch", CWD: "/workspace/project", WorkspaceRoots: []string{"/workspace/project/extra"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	scope, ok := common.ToolScopeFromContext(bound)
	if !ok {
		t.Fatal("bound context has no ToolScope")
	}
	if scope.AgentID != attr.TaskID || scope.Family != "orch" || scope.CWD != "/workspace/project" {
		t.Fatalf("scope = %#v, want task attribution plus normalized transport scope", scope)
	}
}

func TestBindTaskToolScopeRejectsMetadataImpersonation(t *testing.T) {
	ctx, err := common.BindCallerAttribution(context.Background(), validAttribution(t, "task/real"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = bindPrincipal(ctx, "principal/real")
	if err != nil {
		t.Fatal(err)
	}
	claimed := common.ToolCallParams{MetaAgentID: "task/forged"}.Scope("mcp-orch")
	bound, err := BindTaskToolScope(ctx, BindRequest{
		Template:     ScopeTemplate{Family: "orch", CWD: "/workspace"},
		ClaimedScope: claimed,
	})
	if bound != ctx || !errors.Is(err, ErrMetadataImpersonation) {
		t.Fatalf("BindTaskToolScope() = (%v, %v), want unchanged context and metadata rejection", bound, err)
	}
}

func TestBindTaskToolScopeRejectsMalformedMetadataBeforeNormalization(t *testing.T) {
	ctx, err := common.BindCallerAttribution(context.Background(), validAttribution(t, "task/real"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = bindPrincipal(ctx, "principal/real")
	if err != nil {
		t.Fatal(err)
	}
	for _, claimed := range []common.ToolScope{
		{CWD: "relative"},
		{WorkspaceRoots: []string{"relative"}},
		{Family: " "},
	} {
		bound, bindErr := BindTaskToolScope(ctx, BindRequest{Template: ScopeTemplate{Family: "orch"}, ClaimedScope: claimed})
		if bound != ctx || !errors.Is(bindErr, ErrMetadataImpersonation) {
			t.Fatalf("claimed scope %#v => (%v, %v), want unchanged context and metadata rejection", claimed, bound, bindErr)
		}
	}
}

func TestBindTaskToolScopeRejectsMissingAttribution(t *testing.T) {
	bound, err := BindTaskToolScope(context.Background(), BindRequest{Template: ScopeTemplate{Family: "orch"}})
	if bound == nil || !errors.Is(err, ErrAttributionMissing) {
		t.Fatalf("BindTaskToolScope() = (%v, %v), want missing attribution", bound, err)
	}
}

func TestBindTaskToolScopeRejectsAttributionWithoutPrincipal(t *testing.T) {
	ctx, err := common.BindCallerAttribution(context.Background(), validAttribution(t, "task/real"))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindTaskToolScope(ctx, BindRequest{Template: ScopeTemplate{Family: "orch"}})
	if bound != ctx || !errors.Is(err, ErrPrincipalMissing) {
		t.Fatalf("BindTaskToolScope() = (%v, %v), want unchanged context and missing principal", bound, err)
	}
}

func TestBindTaskToolScopeRejectsOperatorPrincipal(t *testing.T) {
	ctx, err := common.BindCallerAttribution(context.Background(), validAttribution(t, "task/real"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = bindPrincipalWithProfile(ctx, "principal/operator", "operator")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := BindTaskToolScope(ctx, BindRequest{Template: ScopeTemplate{Family: "orch"}})
	if bound != ctx || !errors.Is(err, ErrOperatorPrincipal) {
		t.Fatalf("BindTaskToolScope() = (%v, %v), want unchanged context and operator rejection", bound, err)
	}
}

func TestBindTaskToolScopeRejectsPreboundScopeMismatch(t *testing.T) {
	ctx, err := common.BindCallerAttribution(context.Background(), validAttribution(t, "task/real"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = bindPrincipal(ctx, "principal/real")
	if err != nil {
		t.Fatal(err)
	}
	ctx = common.WithToolScope(ctx, common.ToolScope{AgentID: "task/forged", Family: "orch", CWD: "/workspace"})
	bound, err := BindTaskToolScope(ctx, BindRequest{Template: ScopeTemplate{Family: "orch", CWD: "/workspace"}})
	if bound != ctx || !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("BindTaskToolScope() = (%v, %v), want unchanged context and scope mismatch", bound, err)
	}
}

func TestBindTaskToolScopeRejectsInvalidTemplate(t *testing.T) {
	ctx, err := common.BindCallerAttribution(context.Background(), validAttribution(t, "task/real"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = bindPrincipal(ctx, "principal/real")
	if err != nil {
		t.Fatal(err)
	}
	for _, template := range []ScopeTemplate{
		{},
		{Family: " orch"},
		{Family: "orch", CWD: "relative"},
		{Family: "orch", CWD: "/workspace", WorkspaceRoots: []string{"relative"}},
		{Family: "orch", WorkspaceRoots: []string{"/workspace"}},
		{Family: "orch", CWD: `\\server\share`},
		{Family: "orch", CWD: "//server/share"},
		{Family: "orch", CWD: "/workspace", WorkspaceRoots: []string{`\\server\share`}},
		{Family: "orch", CWD: "/workspace", WorkspaceRoots: []string{"//server/share"}},
		{Family: "orch", CWD: "/workspace\x00"},
		{Family: "orch", CWD: "/workspace", WorkspaceRoots: []string{"/workspace/extra\x00"}},
	} {
		if bound, bindErr := BindTaskToolScope(ctx, BindRequest{Template: template}); bound != ctx || !errors.Is(bindErr, ErrBindingInvalid) {
			t.Fatalf("template %#v => (%v, %v), want unchanged context and invalid binding", template, bound, bindErr)
		}
	}
}

func bindPrincipal(ctx context.Context, principalRef string) (context.Context, error) {
	return bindPrincipalWithProfile(ctx, principalRef, "task_http_session_v1")
}

func bindPrincipalWithProfile(ctx context.Context, principalRef, profile string) (context.Context, error) {
	principal, err := common.NewCallerPrincipal(
		principalRef, "scope/task", profile, "auth/task-session", time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		return ctx, err
	}
	return common.BindCallerPrincipal(ctx, principal)
}

func validAttribution(t *testing.T, taskID string) common.CallerAttribution {
	t.Helper()
	attribution, err := common.NewCallerAttribution(taskID, common.ExecutionFence{
		AgentID: taskID, ReservationID: "reservation/" + taskID, ReservationGeneration: 1,
		IncarnationID: "incarnation/" + taskID, IncarnationGeneration: 1, BindingVersion: 1,
		BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence/" + taskID,
	}, common.IssuerBinding{
		Kind: common.CallerAttributionIssuerProviderBridge, BindingRef: "issuer/" + taskID,
		Generation: 1, Digest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return attribution
}
