package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
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

func TestLoggingIncludesSafeTimeoutFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, _ = Logging(logger, "xref")(func(context.Context, json.RawMessage) (any, error) {
		return nil, newToolTimeoutError(80*time.Second, context.DeadlineExceeded)
	})(context.Background(), nil)

	for _, field := range []string{
		`"error_code":"lsp_timeout"`, `"retryable":true`, `"timeout_ms":80000`,
	} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("timeout log missing %s: %s", field, output.String())
		}
	}
}

func TestLoggingIncludesDocumentSymbolCapabilityMethod(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, _ = Logging(logger, "structure")(func(context.Context, json.RawMessage) (any, error) {
		return nil, &common.CodedToolError{
			Err:       errors.New("unsupported capability"),
			Code:      "capability_unsupported",
			Retryable: false,
			Meta: map[string]any{
				"lsp_method":          "textDocument/documentSymbol",
				"server_executable":   "sqruff",
				"server_name":         "sqruff",
				"server_version":      "0.23.1",
				"server_pid":          12345,
				"capabilities_known":  true,
				"capability_snapshot": "document_symbol=false,diagnostic=true",
			},
		}
	})(context.Background(), nil)

	for _, field := range []string{
		`"error_code":"capability_unsupported"`, `"retryable":false`, `"lsp_method":"textDocument/documentSymbol"`,
		`"server_executable":"sqruff"`, `"server_name":"sqruff"`, `"server_version":"0.23.1"`,
		`"server_pid":12345`, `"capabilities_known":true`, `"capability_snapshot":"document_symbol=false,diagnostic=true"`,
	} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("capability log missing %s: %s", field, output.String())
		}
	}
}

func TestLoggingIncludesLanguageUnsupportedAttribution(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, _ = Logging(logger, "structure")(func(context.Context, json.RawMessage) (any, error) {
		return nil, &common.CodedToolError{
			Err:       errors.New("unsupported language"),
			Code:      "language_unsupported",
			Retryable: false,
			Meta: map[string]any{
				"requested_language": "proto",
				"detected_language":  "unknown",
				"resolved_language":  "proto",
				"file_extension":     ".unknown",
				"adapter_status":     "registry_lookup_miss",
			},
		}
	})(context.Background(), nil)

	for _, field := range []string{
		`"error_code":"language_unsupported"`, `"retryable":false`,
		`"requested_language":"proto"`, `"detected_language":"unknown"`,
		`"resolved_language":"proto"`, `"file_extension":".unknown"`,
		`"adapter_status":"registry_lookup_miss"`,
	} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("language attribution log missing %s: %s", field, output.String())
		}
	}
}
