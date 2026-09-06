package contract

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRoomCapabilityGateFailClosed 验证 Room disabled gate 不把缺失祖先当作 noop 成功。
func TestRoomCapabilityGateFailClosed(t *testing.T) {
	gate := DisabledRoomCapabilityGate{}
	err := gate.Require(context.Background(), RoomBroadcastCapability)
	if !errors.Is(err, ErrRoomCapabilityNotSupported) {
		t.Fatalf("Require(room.broadcast)=%v, want capability_not_supported", err)
	}
	var typed RoomCapabilityDisabledError
	if !errors.As(err, &typed) || typed.Capability != RoomBroadcastCapability {
		t.Fatalf("typed error=%#v, want RoomBroadcastError for room.broadcast", typed)
	}
	if strings.Contains(err.Error(), "room_ancestors_unclosed") == false {
		t.Fatalf("disabled error=%q, want stable reason", err)
	}
}

// TestRoomCapabilityGateRejectsUnknownAndCancellation 验证未知能力和取消都 fail-fast。
func TestRoomCapabilityGateRejectsUnknownAndCancellation(t *testing.T) {
	gate := DisabledRoomCapabilityGate{}
	if err := gate.Require(context.Background(), RoomCapability("room.inject")); !errors.Is(err, ErrRoomCapabilityNotSupported) {
		t.Fatalf("unknown capability error=%v, want sentinel", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.Require(ctx, RoomGetCapability); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Require error=%v, want context.Canceled", err)
	}
}
