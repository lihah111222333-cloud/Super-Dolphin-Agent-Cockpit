package main

import (
	"runtime"
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

func TestValidateRunnerWorkloadClassifiesLocalAndRemoteProducerGates(t *testing.T) {
	local := catalog.Workload{ID: "mcp-lsp-idle-quick", ImplementationStatus: "implemented", ProducerImplementationStatus: "missing", RunnerTarget: "local-go-test", Platforms: []string{runtime.GOOS}, TriggerClass: "quick"}
	if err := validateRunnerWorkload(local); err != nil {
		t.Fatalf("validateRunnerWorkload(local) error = %v, want quick local workload allowed", err)
	}
	remote := local
	remote.ID = "remote-soak"
	remote.RunnerTarget = "remote-gate-test"
	remote.TriggerClass = "soak"
	if err := validateRunnerWorkload(remote); err == nil || !strings.Contains(err.Error(), "producer_implementation_status=missing") {
		t.Fatalf("validateRunnerWorkload(remote) error = %v, want producer N/V", err)
	}
}
