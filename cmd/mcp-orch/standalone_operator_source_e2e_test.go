//go:build e2e

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
)

func TestMCPOrchServeOperatorSourceLifecycle(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	first := startServeE2E(t, ctx, binary, fixture)
	cfg, err := standalone.LoadConfig(fixture.config)
	operatorSourceRequireNoError(t, err)
	db, err := newStandaloneDB(newStandalonePlatformConfig(cfg))
	operatorSourceRequireNoError(t, err)
	t.Cleanup(func() { operatorSourceRequireNoError(t, db.Close()) })
	store, err := operatorsource.NewStore(db)
	operatorSourceRequireNoError(t, err)
	source, err := store.GetLatest(ctx)
	operatorSourceRequireNoError(t, err)
	if source.State != "active" || source.PrincipalRef != cfg.PrincipalRef || source.ScopeRef != cfg.ScopeRef {
		t.Fatalf("ready without configured active source: %+v", source)
	}
	operatorSourceRequireNoError(t, stopServeProcessE2E(t, first, nil))
	stopped, err := store.GetLatest(ctx)
	operatorSourceRequireNoError(t, err)
	assertOperatorStoppedSource(t, stopped, source)
	second := startServeE2E(t, ctx, binary, fixture)
	next, err := store.GetLatest(ctx)
	operatorSourceRequireNoError(t, err)
	if next.State != "active" || next.Generation != source.Generation+1 || next.SourceID == source.SourceID {
		t.Fatalf("restart did not advance source: %+v", next)
	}
	assertOperatorPriorBootRevoked(t, ctx, store, source)
	operatorSourceRequireNoError(t, stopServeProcessE2E(t, second, nil))
	final, err := store.GetLatest(ctx)
	operatorSourceRequireNoError(t, err)
	assertOperatorStoppedSource(t, final, next)
}

// assertOperatorPriorBootRevoked 旧代撤销应幂等且不可复活。
func assertOperatorPriorBootRevoked(t *testing.T, ctx context.Context, store *operatorsource.Store, source operatorsource.Source) {
	t.Helper()
	selector := operatorsource.Selector{PrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, AuthContext: source.AuthContext, SourceID: source.SourceID, SourceFenceRef: source.SourceFenceRef, Generation: source.Generation}
	old, err := store.Revoke(ctx, selector)
	operatorSourceRequireNoError(t, err)
	if old.State != "revoked" {
		t.Fatal("previous source resurrected")
	}

}

// assertOperatorStoppedSource 正常停止必须撤销精确来源。
func assertOperatorStoppedSource(t *testing.T, got, want operatorsource.Source) {
	t.Helper()
	if got.State != "revoked" || got.SourceID != want.SourceID {
		t.Fatalf("stop did not revoke exact source: %+v", got)
	}
}
