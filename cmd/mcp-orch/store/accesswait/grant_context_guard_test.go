package accesswait

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestGrantEntryPointsRejectForeignOrOperatorContextBeforeEffects(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	request := validGrantRequest("grant-context-guard", "grant/context-guard", strings.Repeat("9", 64))
	foreign := bindTestCaller(t, "foreign", request.ScopeRef, "task")
	if _, err := fixture.store.CreateGrant(foreign, request); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("CreateGrant(foreign context) = %v, want ErrAccessDenied", err)
	}
	if got := accessGrantCount(t, fixture); got != 0 {
		t.Fatalf("foreign CreateGrant changed durable grants: %d", got)
	}

	grant := mustCreateGrant(t, fixture.store, request)
	if _, err := fixture.store.Authorize(foreign, accessRequestFor(request, "read")); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("Authorize(foreign context) = %v, want ErrAccessDenied", err)
	}
	if _, err := fixture.store.RevokeGrant(foreign, RevokeGrantRequest{
		GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: grant.CallerPrincipalRef,
		ScopeRef: grant.ScopeRef, ReasonRef: "foreign", RequestDigest: strings.Repeat("a", 64), RevokedAt: time.Now().UTC(),
	}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("RevokeGrant(foreign context) = %v, want ErrAccessDenied", err)
	}
	if got := mustGetGrant(t, fixture.store, grant.GrantID); got.Status != "active" {
		t.Fatalf("foreign RevokeGrant changed status: %q", got.Status)
	}

	operator := bindTestCaller(t, "principal", request.ScopeRef, "operator")
	if _, err := fixture.store.CreateGrant(operator, requestWithID(request, "grant-operator-guard")); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("CreateGrant(operator context) = %v, want ErrAccessDenied", err)
	}
}

func TestRevokeGrantRejectsRequestDigestMismatch(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	request := validGrantRequest("grant-digest-guard", "grant/digest-guard", strings.Repeat("c", 64))
	grant := mustCreateGrant(t, fixture.store, request)
	_, err := fixture.store.RevokeGrant(t.Context(), RevokeGrantRequest{
		GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: grant.CallerPrincipalRef,
		ScopeRef: grant.ScopeRef, ReasonRef: "digest-mismatch", RequestDigest: strings.Repeat("d", 64), RevokedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("RevokeGrant(digest mismatch) = %v, want ErrAccessDenied", err)
	}
	if got := mustGetGrant(t, fixture.store, grant.GrantID); got.Status != "active" {
		t.Fatalf("digest-mismatched revoke changed status: %q", got.Status)
	}
}

func bindTestCaller(t *testing.T, principalRef, scopeRef, profile string) context.Context {
	t.Helper()
	principal, err := common.NewCallerPrincipal(principalRef, scopeRef, profile, "test-auth", time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := common.BindCallerPrincipal(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func accessGrantCount(t *testing.T, fixture accessWaitFixture) int {
	t.Helper()
	var count int
	if err := fixture.db.QueryRowContext(t.Context(), "SELECT count(*) FROM orchestration_access_grants").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func mustGetGrant(t *testing.T, store *Store, grantID string) AccessGrant {
	t.Helper()
	grant, err := store.GetGrant(t.Context(), grantID)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func requestWithID(request AccessGrantRequest, id string) AccessGrantRequest {
	request.GrantID = id
	request.IdempotencyNamespace += "/operator"
	request.IdempotencyKeyDigest = strings.Repeat("b", 64)
	return request
}
