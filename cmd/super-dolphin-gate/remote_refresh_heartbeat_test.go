package main

import (
	"context"
	"errors"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteBaselineRefreshLeaseOwnerHeartbeatFailureCancelsAndBlocksPromotion(t *testing.T) {
	want := errors.New("lease authority unavailable")
	failed := make(chan error, 1)
	ctx, owner := newRemoteBaselineRefreshLeaseOwner(context.Background(), gatecontract.RemoteBaselineRefreshLease{}, time.Millisecond,
		func(lease gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
			return lease, want
		},
		func(_ gatecontract.RemoteBaselineRefreshLease, failure error) error {
			failed <- failure
			return nil
		},
	)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("heartbeat failure did not cancel refresh context")
	}
	if !errors.Is(context.Cause(ctx), want) {
		t.Fatalf("heartbeat cause = %v, want %v", context.Cause(ctx), want)
	}
	select {
	case failure := <-failed:
		if !errors.Is(failure, want) {
			t.Fatalf("recorded failure = %v, want %v", failure, want)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat failure was not persisted")
	}
	if err := owner.apply(func(lease gatecontract.RemoteBaselineRefreshLease) (gatecontract.RemoteBaselineRefreshLease, error) {
		t.Fatal("promotion mutation ran after heartbeat failure")
		return lease, nil
	}); !errors.Is(err, want) {
		t.Fatalf("promotion mutation error = %v, want %v", err, want)
	}
	if err := owner.close(); !errors.Is(err, want) {
		t.Fatalf("owner close error = %v, want %v", err, want)
	}
}
