package claudewire

import (
	"slices"
	"testing"
)

// TestClaudeNativeAckLaunchFlag 固定官方输入重放确认开关，防止退化为 stdin 成功。
func TestClaudeNativeAckLaunchFlag(t *testing.T) {
	args := claudeLaunchArgs("model", "session", "instructions")
	if !slices.Contains(args, "--replay-user-messages") {
		t.Fatal("native user acknowledgement flag missing")
	}
}

// TestClaudeReplayWireField 锁住官方确认字段大小写并拒绝把缺失字段解作确认。
func TestClaudeReplayWireField(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		replay bool
	}{
		{raw: `{"type":"user","isReplay":true}`, replay: true},
		{raw: `{"type":"user","isReplay":false}`},
		{raw: `{"type":"user"}`},
	} {
		event, err := DecodeEvent([]byte(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		if event.IsReplay != tc.replay {
			t.Fatalf("replay mapping mismatch: %s", tc.raw)
		}
	}
}
