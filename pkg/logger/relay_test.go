package logger

import (
	"context"
	"io"
	"log/slog"
	"strings"
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

func TestRelayHookRedactsSensitiveAttrs(t *testing.T) {
	ClearRelayHook()
	t.Cleanup(ClearRelayHook)

	var got RelayPayload
	logger := slog.New(newHandler(Production, slog.LevelInfo, io.Discard))
	SetRelayHook(func(_ context.Context, payload RelayPayload) {
		got = payload
	})

	logger.Warn("relay redaction",
		"api_key", "sk-123456789",
		slog.Group("auth",
			slog.String("token", "plain-token"),
			slog.String("password", "secret-password"),
		),
		"details", "Authorization: Bearer abc.def token=plain",
	)

	for _, key := range []string{"api_key", "auth.token", "auth.password"} {
		if got.Fields[key] != redactedValue {
			t.Fatalf("fields[%s] = %#v, want redacted", key, got.Fields[key])
		}
	}
	details, ok := got.Fields["details"].(string)
	if !ok {
		t.Fatalf("fields[details] = %#v, want string", got.Fields["details"])
	}
	for _, forbidden := range []string{"abc.def", "token=plain"} {
		if strings.Contains(details, forbidden) {
			t.Fatalf("fields[details] leaked %q: %s", forbidden, details)
		}
	}
}
