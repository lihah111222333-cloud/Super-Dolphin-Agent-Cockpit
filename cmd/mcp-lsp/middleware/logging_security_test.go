package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggingDoesNotExposePayloadOrErrorDetails(t *testing.T) {
	const secret = "top-secret-source-patch-/private/workspace"
	for _, tc := range []struct {
		name    string
		handler Handler
	}{
		{name: "success", handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]string{"source": secret}, nil
		}},
		{name: "failure", handler: func(context.Context, json.RawMessage) (any, error) {
			return nil, errors.New(secret)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
			_, _ = Logging(logger, "inspect")(tc.handler)(context.Background(), json.RawMessage(`{"secret":"`+secret+`"}`))
			if strings.Contains(output.String(), secret) {
				t.Fatalf("log exposed request, response, or error detail: %s", output.String())
			}
			for _, field := range []string{"tool", "status"} {
				if !strings.Contains(output.String(), `"`+field+`"`) {
					t.Fatalf("log missing %q: %s", field, output.String())
				}
			}
		})
	}
}
