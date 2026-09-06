package content

import (
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
)

func TestContentOperatorSourceContract(t *testing.T) {
	f := newContentTestFixture(t, "operator-source-contract")
	ctx := t.Context()
	first := contentReserve(t, f, "operator-source-1", 1)
	sources, err := operatorsource.NewStore(f.db)
	contentNoError(t, err)
	s1, err := sources.Activate(ctx, operatorsource.ActivateRequest{PrincipalRef: "principal-1", ScopeRef: "scope-1", AuthContext: "auth-1", ExpectedGeneration: 0})
	contentNoError(t, err)
	b1 := OperatorSourceBinding{s1.SourceID, s1.SourceFenceRef, s1.PrincipalRef, s1.ScopeRef, s1.AuthContext, s1.Generation}
	got, err := f.store.BindOperatorSource(ctx, first.ReservationID, b1)
	contentNoError(t, err)
	if got.Binding != b1 {
		t.Fatalf("binding = %#v, want %#v", got.Binding, b1)
	}
	second := contentReserve(t, f, "operator-source-2", 1)
	if _, err = f.store.BindOperatorSource(ctx, second.ReservationID, b1); err != nil {
		t.Fatal(err)
	}
	replay, err := f.store.BindOperatorSource(ctx, first.ReservationID, b1)
	contentNoError(t, err)
	if replay != got {
		t.Fatalf("replay = %#v, want %#v", replay, got)
	}
	conflict := b1
	conflict.ScopeRef = "scope-other"
	if _, err = f.store.BindOperatorSource(ctx, first.ReservationID, conflict); !errors.Is(err, ErrReplayConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	if resolved, err := f.store.ResolveSource(ctx, first.ReservationID); err != nil || resolved.Binding != b1 {
		t.Fatalf("resolve = %#v, %v", resolved, err)
	}
	_, err = sources.Revoke(ctx, operatorsource.Selector{PrincipalRef: b1.PrincipalRef, ScopeRef: b1.ScopeRef, AuthContext: b1.AuthContext, SourceID: b1.SourceID, SourceFenceRef: b1.FenceRef, Generation: b1.Generation})
	contentNoError(t, err)
	if _, err = f.store.ResolveSource(ctx, first.ReservationID); !errors.Is(err, ErrContentUnavailable) {
		t.Fatalf("revoked resolve = %v", err)
	}
	s2, err := sources.Activate(ctx, operatorsource.ActivateRequest{PrincipalRef: "principal-2", ScopeRef: "scope-2", AuthContext: "auth-2", ExpectedGeneration: 1})
	contentNoError(t, err)
	if _, err = f.store.ResolveSource(ctx, first.ReservationID); !errors.Is(err, ErrContentUnavailable) {
		t.Fatalf("old binding resolve = %v", err)
	}
	third := contentReserve(t, f, "operator-source-3", 1)
	b2 := OperatorSourceBinding{s2.SourceID, s2.SourceFenceRef, s2.PrincipalRef, s2.ScopeRef, s2.AuthContext, s2.Generation}
	if _, err = f.store.BindOperatorSource(ctx, third.ReservationID, b2); err != nil {
		t.Fatal(err)
	}
}
