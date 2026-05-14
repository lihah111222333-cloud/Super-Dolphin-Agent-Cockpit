package logger

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestRelayHookCanBeInstalledAfterHandlerCreation(t *testing.T) {
	ClearRelayHook()
	t.Cleanup(ClearRelayHook)

	var got RelayPayload
	logger := slog.New(newHandler(Development, slog.LevelDebug, io.Discard))
	SetRelayHook(func(_ context.Context, payload RelayPayload) {
		got = payload
	})

	logger.Warn("relay ready", "key", "value")

	if got.Level != "WARN" {
		t.Fatalf("level = %q, want WARN", got.Level)
	}
	if got.Msg != "relay ready" {
		t.Fatalf("msg = %q, want relay ready", got.Msg)
	}
	if got.Fields["key"] != "value" {
		t.Fatalf("fields[key] = %#v, want value", got.Fields["key"])
	}
}

func TestRelayHookHonorsDisabledContext(t *testing.T) {
	ClearRelayHook()
	t.Cleanup(ClearRelayHook)

	var calls int
	logger := slog.New(newHandler(Development, slog.LevelDebug, io.Discard))
	SetRelayHook(func(context.Context, RelayPayload) {
		calls++
	})

	logger.WarnContext(WithRelayDisabled(context.Background()), "fallback log")

	if calls != 0 {
		t.Fatalf("relay calls = %d, want 0", calls)
	}
}
