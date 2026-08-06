package main

import (
	"strings"
	"testing"

	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestResolveCompletionReceiptPathRequiresExplicitDefault15mPath(t *testing.T) {
	workload := catalog.Workload{ID: "mcp-lsp-default-15m", TriggerClass: "default-15m-source-e2e"}
	if _, err := resolveCompletionReceiptPath(workload, ""); err == nil || !strings.Contains(err.Error(), "explicit --completion-receipt") {
		t.Fatalf("resolveCompletionReceiptPath() error = %v, want explicit path rejection", err)
	}
	if _, err := resolveCompletionReceiptPath(workload, "relative.json"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("resolveCompletionReceiptPath() error = %v, want absolute path rejection", err)
	}
}

func TestResolveCompletionReceiptPathDoesNotInferShortWorkloadReceipt(t *testing.T) {
	workload := catalog.Workload{ID: "mcp-lsp-idle-quick", TriggerClass: "quick"}
	path, err := resolveCompletionReceiptPath(workload, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("resolveCompletionReceiptPath() = %q, want empty", path)
	}
}
