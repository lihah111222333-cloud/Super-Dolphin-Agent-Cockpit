package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ownedGateRunners 构造 release、工作流、夜间协议所有者的 runner。
func ownedGateRunners() map[string]gateRunner {
	return map[string]gateRunner{
		"backend:test-integrity": {run: func() error {
			return runCommand("", "go", "test", "./internal/guards", "-count=1")
		}},
		"workflow:actionlint": {run: func() error {
			return runCommand("", "make", "actionlint")
		}},
		"release:semantic-guards": {run: func() error {
			return runCommand("", "go", "test", "./scripts", "-count=1")
		}},
		"nightly-protocol:check": {run: func() error {
			return runCommand("", "go", "run", "./scripts/nightly_protocol_validator")
		}},
		"mcp-lsp:catalog": {run: func() error {
			return runCommand("", "./scripts/check_mcp_lsp_workload_catalog.sh")
		}},
		"mcp-lsp:idle-quick": {run: func() error {
			return runMcpLSPQuickRoundTrip()
		}},
	}
}

// runMcpLSPQuickRoundTrip 执行本地 quick workload 并立即用 catalog guard 验证回执。
func runMcpLSPQuickRoundTrip() error {
	receiptDir, err := os.MkdirTemp("", "super-dolphin-quick-roundtrip-")
	if err != nil {
		return fmt.Errorf("create mcp-lsp quick roundtrip directory: %w", err)
	}
	receipt := filepath.Join(receiptDir, "receipt.json")
	runErr := runCommand("", "./scripts/run_mcp_lsp_workload.sh", "--id", "mcp-lsp-idle-quick", "--receipt", receipt)
	if runErr != nil {
		if cleanupErr := os.RemoveAll(receiptDir); cleanupErr != nil {
			return fmt.Errorf("mcp-lsp quick workload failed: %w; cleanup failed: %v", runErr, cleanupErr)
		}
		return runErr
	}
	guardArgs := mcpLSPReceiptGuardArgs(receipt)
	guardErr := runCommand("", guardArgs[0], guardArgs[1:]...)
	cleanupErr := os.RemoveAll(receiptDir)
	if guardErr != nil {
		if cleanupErr != nil {
			return fmt.Errorf("mcp-lsp quick receipt guard failed: %w; cleanup failed: %v", guardErr, cleanupErr)
		}
		return guardErr
	}
	if cleanupErr != nil {
		return fmt.Errorf("cleanup mcp-lsp quick roundtrip directory: %w", cleanupErr)
	}
	return nil
}

func mcpLSPReceiptGuardArgs(receipt string) []string {
	return []string{"./scripts/check_mcp_lsp_workload_catalog.sh", "--receipt-only", "--receipt", receipt, "--id", "mcp-lsp-idle-quick"}
}
