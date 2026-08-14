package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestValidateCatalogTestSelectorsRejectsRenamedTest(t *testing.T) {
	repoRoot := t.TempDir()
	packageDirectory := filepath.Join(repoRoot, "cmd", "mcp-lsp", "fake")
	if err := os.MkdirAll(packageDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	testSource := "package fake\n\nfunc TestExisting() {}\n"
	if err := os.WriteFile(filepath.Join(packageDirectory, "fake_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	document := catalog.Catalog{Workloads: []catalog.Workload{{
		ID: "mcp-lsp-mutated", ImplementationStatus: "implemented",
		Command: []string{"go", "test", "./cmd/mcp-lsp/fake", "-run", "^Test(Renamed)$"},
	}}}
	err := validateCatalogTestSelectors(repoRoot, document)
	if err == nil || !strings.Contains(err.Error(), "missing test \"TestRenamed\"") {
		t.Fatalf("validateCatalogTestSelectors() error = %v, want missing renamed test", err)
	}
}

func TestReceiptOnlyGuardSkipsStaticCatalogValidationAndRequiresPairedAbsoluteReceipt(t *testing.T) {
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	request := assertReceiptOnlyRequestParsing(t, receiptPath)
	assertReceiptOnlyValidationOrder(t, request)
}

func assertReceiptOnlyRequestParsing(t *testing.T, receiptPath string) guardRequest {
	t.Helper()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing receipt and id", args: []string{"--receipt-only"}, want: "--receipt-only requires --receipt and --id"},
		{name: "missing id", args: []string{"--receipt-only", "--receipt", receiptPath}, want: "--receipt-only requires --receipt and --id"},
		{name: "missing receipt", args: []string{"--receipt-only", "--id", "mcp-lsp-idle-quick"}, want: "--receipt-only requires --receipt and --id"},
		{name: "relative receipt", args: []string{"--receipt-only", "--receipt", "receipt.json", "--id", "mcp-lsp-idle-quick"}, want: "--receipt-only requires an absolute --receipt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseGuardRequest(test.args); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseGuardRequest(%v) error = %v, want %q", test.args, err, test.want)
			}
		})
	}

	request, err := parseGuardRequest([]string{"--receipt-only", "--receipt", receiptPath, "--id", "mcp-lsp-idle-quick"})
	if err != nil {
		t.Fatalf("parse receipt-only request: %v", err)
	}
	if !request.receiptOnly || request.receipt != receiptPath || request.id != "mcp-lsp-idle-quick" {
		t.Fatalf("receipt-only request = %#v, want receipt-only absolute request", request)
	}
	return request
}

func assertReceiptOnlyValidationOrder(t *testing.T, request guardRequest) {
	t.Helper()
	document := catalog.Catalog{Workloads: []catalog.Workload{{
		ID: "mcp-lsp-idle-quick", ImplementationStatus: "implemented",
	}}}
	err := validateGuardRequest(t.TempDir(), document, request)
	if err == nil || !strings.Contains(err.Error(), "read workload receipt") {
		t.Fatalf("receipt-only validation error = %v, want receipt validation after skipping static catalog checks", err)
	}

	request.receiptOnly = false
	err = validateGuardRequest(t.TempDir(), document, request)
	if err == nil || !strings.Contains(err.Error(), "catalog workload count") {
		t.Fatalf("ordinary validation error = %v, want canonical catalog validation before receipt", err)
	}
}
