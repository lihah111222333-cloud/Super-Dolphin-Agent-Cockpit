package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// TestDiagnosticsToolErrorConsumesV1LineProtocol 锁定 diagnostics gate 只信 ERROR header 的 retryable 字段。
func TestDiagnosticsToolErrorConsumesV1LineProtocol(t *testing.T) {
	t.Run("single parser source guard", func(t *testing.T) {
		source, err := os.ReadFile("main.go")
		if err != nil {
			t.Fatalf("read gate source: %v", err)
		}
		if strings.Count(string(source), "lineprotocol.Parse(") != 1 {
			t.Fatalf("lineprotocol.Parse owner count = %d, want 1", strings.Count(string(source), "lineprotocol.Parse("))
		}
		if strings.Contains(string(source), `strings.Contains(text, "retryable=1")`) {
			t.Fatal("diagnostics gate retained loose retryable substring parser")
		}
	})

	t.Run("retryable header", func(t *testing.T) {
		err := diagnosticsToolError(newDiagnosticsErrorResult(
			"ERROR code=lsp_timeout retryable=1\n" + lineprotocol.TextRecord("MESSAGE", "timed out"),
		))
		if _, ok := errors.AsType[*retryableDiagnosticsError](err); !ok {
			t.Fatalf("diagnosticsToolError() = %T %v, want retryable error", err, err)
		}
	})

	t.Run("record cannot spoof retryable header", func(t *testing.T) {
		text := "ERROR code=invalid_params retryable=0\n" +
			lineprotocol.TextRecord("MESSAGE", "permanent failure") + "\n" +
			lineprotocol.FieldsRecord("ATTR", lineprotocol.Field{Key: "retryable", Value: "1"})
		err := diagnosticsToolError(newDiagnosticsErrorResult(text))
		if _, ok := errors.AsType[*retryableDiagnosticsError](err); ok {
			t.Fatalf("diagnosticsToolError() trusted spoofed record over ERROR header: %v", err)
		}
	})

	t.Run("malformed error protocol fails closed", func(t *testing.T) {
		err := diagnosticsToolError(newDiagnosticsErrorResult("ERROR code=x retryable=0\nMESSAGE\tbad\\q"))
		if err == nil || !strings.Contains(err.Error(), "malformed diagnostics ERROR line protocol") {
			t.Fatalf("diagnosticsToolError() = %v, want malformed protocol error", err)
		}
	})

	t.Run("OK header cannot masquerade as tool error", func(t *testing.T) {
		err := diagnosticsToolError(newDiagnosticsErrorResult(
			lineprotocol.HeaderLine(0, 0, false, "diagnostic") + "\n" + lineprotocol.TextRecord("MESSAGE", "not an error"),
		))
		if err == nil || !strings.Contains(err.Error(), "expected ERROR header") {
			t.Fatalf("diagnosticsToolError() = %v, want ERROR-header rejection", err)
		}
	})
}

func newDiagnosticsErrorResult(text string) toolResult {
	result := toolResult{IsError: true}
	result.Content = make([]struct {
		Text string `json:"text"`
	}, 1)
	result.Content[0].Text = text
	return result
}
