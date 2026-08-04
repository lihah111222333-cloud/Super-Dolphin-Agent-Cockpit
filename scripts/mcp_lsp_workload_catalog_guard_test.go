package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	mcpLSPWorkloadCatalogPath   = "scripts/mcp_lsp_workload_catalog.json"
	mcpLSPWorkloadRunnerPath    = "scripts/run_mcp_lsp_workload.sh"
	mcpLSPWorkloadCatalogSchema = "super-dolphin/mcp-lsp-workload-catalog/v1"
)

type mcpLSPWorkloadCatalogGuardDocument struct {
	Schema        string                               `json:"schema"`
	CatalogDigest string                               `json:"catalog_digest"`
	Workloads     []mcpLSPWorkloadCatalogGuardWorkload `json:"workloads"`
}

type mcpLSPWorkloadCatalogGuardWorkload struct {
	ID                           string   `json:"id"`
	ImplementationStatus         string   `json:"implementation_status"`
	ProducerImplementationStatus string   `json:"producer_implementation_status"`
	RunnerTarget                 string   `json:"runner_target"`
	Platforms                    []string `json:"platforms"`
	TimeoutSeconds               int      `json:"timeout_seconds"`
	TriggerClass                 string   `json:"trigger_class"`
	ReceiptSchema                string   `json:"receipt_schema"`
	ProducerWorkflowPath         string   `json:"producer_workflow_path"`
	ProducerArtifactName         string   `json:"producer_artifact_name"`
	T6Blocking                   bool     `json:"t6_blocking"`
	ReleaseBlocking              bool     `json:"release_blocking"`
	ReceiptRequired              *bool    `json:"receipt_required"`
	Command                      []string `json:"command"`
}

func TestMcpLSPWorkloadCatalogSchemaAndCanonicalIDs(t *testing.T) {
	document := readMcpLSPWorkloadCatalogGuardDocument(t)
	assertMcpLSPWorkloadCatalogSchema(t, document)
	assertMcpLSPWorkloadCatalogIDs(t, document)
	assertMcpLSPWorkloadCatalogWorkloads(t, document)
}

func assertMcpLSPWorkloadCatalogSchema(t *testing.T, document mcpLSPWorkloadCatalogGuardDocument) {
	t.Helper()
	if document.Schema != mcpLSPWorkloadCatalogSchema {
		t.Fatalf("catalog schema = %q, want %q", document.Schema, mcpLSPWorkloadCatalogSchema)
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(document.CatalogDigest) {
		t.Fatalf("catalog digest = %q, want sha256:<64 lowercase hex>", document.CatalogDigest)
	}
	if len(document.Workloads) == 0 {
		t.Fatal("catalog must contain workloads")
	}
}

func assertMcpLSPWorkloadCatalogIDs(t *testing.T, document mcpLSPWorkloadCatalogGuardDocument) {
	t.Helper()
	wantIDs := []string{
		"mcp-lsp-idle-quick",
		"mcp-lsp-native-process-tree",
		"mcp-lsp-default-15m",
		"mcp-lsp-100-workspace-soak",
		"mcp-lsp-release-artifact",
	}
	ids := make([]string, 0, len(document.Workloads))
	seen := map[string]bool{}
	for _, workload := range document.Workloads {
		if seen[workload.ID] {
			t.Fatalf("duplicate workload ID %q", workload.ID)
		}
		seen[workload.ID] = true
		ids = append(ids, workload.ID)
	}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("catalog IDs = %v, want %v", ids, wantIDs)
	}
}

func assertMcpLSPWorkloadCatalogWorkloads(t *testing.T, document mcpLSPWorkloadCatalogGuardDocument) {
	t.Helper()
	for _, workload := range document.Workloads {
		assertMcpLSPWorkloadMetadata(t, workload)
		assertMcpLSPWorkloadImplementation(t, workload)
	}
}

func assertMcpLSPWorkloadMetadata(t *testing.T, workload mcpLSPWorkloadCatalogGuardWorkload) {
	t.Helper()
	if workload.RunnerTarget == "" {
		t.Errorf("workload %q has no runner_target", workload.ID)
	}
	if len(workload.Platforms) == 0 {
		t.Errorf("workload %q has no platforms", workload.ID)
	}
	if workload.TimeoutSeconds <= 0 {
		t.Errorf("workload %q timeout_seconds = %d, want positive", workload.ID, workload.TimeoutSeconds)
	}
	if workload.TriggerClass == "" || workload.ReceiptSchema == "" {
		t.Errorf("workload %q missing trigger_class or receipt_schema", workload.ID)
	}
	if workload.ProducerWorkflowPath == "" || workload.ProducerArtifactName == "" {
		t.Errorf("workload %q missing producer coordinates", workload.ID)
	}
	if workload.ReceiptRequired == nil || !*workload.ReceiptRequired {
		t.Errorf("workload %q must require receipt", workload.ID)
	}
}

func assertMcpLSPWorkloadImplementation(t *testing.T, workload mcpLSPWorkloadCatalogGuardWorkload) {
	t.Helper()
	assertMcpLSPProducerImplementation(t, workload)
	assertMcpLSPDefaultTimeout(t, workload)
	assertMcpLSPImplementationCommand(t, workload)
}

// assertMcpLSPProducerImplementation 校验生产者状态及发布阻断标记。
func assertMcpLSPProducerImplementation(t *testing.T, workload mcpLSPWorkloadCatalogGuardWorkload) {
	if workload.ProducerImplementationStatus != "implemented" && workload.ProducerImplementationStatus != "missing" {
		t.Errorf("workload %q has unsupported producer_implementation_status %q", workload.ID, workload.ProducerImplementationStatus)
	}
	if workload.ProducerImplementationStatus == "missing" && !workload.ReleaseBlocking {
		t.Errorf("workload %q missing producer implementation must block release", workload.ID)
	}
}

// assertMcpLSPDefaultTimeout 校验默认 15 分钟 workload 的最小预算。
func assertMcpLSPDefaultTimeout(t *testing.T, workload mcpLSPWorkloadCatalogGuardWorkload) {
	if workload.ID == "mcp-lsp-default-15m" && workload.TimeoutSeconds < 1500 {
		t.Errorf("default-15m timeout_seconds = %d, want at least 1500", workload.TimeoutSeconds)
	}
}

// assertMcpLSPImplementationCommand 校验实现状态与 command/T6/release 约束。
func assertMcpLSPImplementationCommand(t *testing.T, workload mcpLSPWorkloadCatalogGuardWorkload) {
	if workload.ImplementationStatus == "implemented" {
		if len(workload.Command) == 0 {
			t.Errorf("implemented workload %q has no catalog command", workload.ID)
		}
		return
	}
	if workload.ImplementationStatus == "missing" && (!workload.T6Blocking || !workload.ReleaseBlocking || len(workload.Command) != 0) {
		t.Errorf("missing workload %q must block T6/release and be commandless", workload.ID)
	}
}

func TestMcpLSPWorkloadCatalogDigestMatchesCanonicalPayload(t *testing.T) {
	path := filepath.Join(repoRootForMcpLSPWorkloadCatalogGuard(t), mcpLSPWorkloadCatalogPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	digestValue, ok := value["catalog_digest"].(string)
	if !ok || digestValue == "" {
		t.Fatal("catalog_digest is required")
	}
	delete(value, "catalog_digest")
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal digest payload: %v", err)
	}
	digest := sha256.Sum256(append(canonical, '\n'))
	want := "sha256:" + hex.EncodeToString(digest[:])
	if digestValue != want {
		t.Fatalf("catalog digest = %q, want %q", digestValue, want)
	}
}

func TestMcpLSPWorkloadRunnerConsumesIDsAndFailsClosed(t *testing.T) {
	repoRoot := repoRootForMcpLSPWorkloadCatalogGuard(t)
	runner := filepath.Join(repoRoot, mcpLSPWorkloadRunnerPath)
	if _, err := os.Stat(runner); err != nil {
		t.Fatalf("runner missing: %v", err)
	}
	for _, id := range []string{"mcp-lsp-idle-quick", "mcp-lsp-native-process-tree"} {
		cmd := exec.Command("bash", runner, "--id", id, "--print-plan")
		cmd.Dir = repoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("runner --id %s failed: %v\n%s", id, err, output)
		}
	}
	for _, args := range [][]string{{"--id", "missing-id", "--print-plan"}, {"--print-plan"}} {
		cmd := exec.Command("bash", append([]string{runner}, args...)...)
		cmd.Dir = repoRoot
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("runner args %v unexpectedly succeeded: %s", args, output)
		}
	}
}

func TestMcpLSPWorkloadRunnerNVMissingFailsClosedWithExitOne(t *testing.T) {
	repoRoot := repoRootForMcpLSPWorkloadCatalogGuard(t)
	runner := filepath.Join(repoRoot, mcpLSPWorkloadRunnerPath)
	for _, id := range []string{"mcp-lsp-default-15m", "mcp-lsp-100-workspace-soak", "mcp-lsp-release-artifact"} {
		assertMcpLSPWorkloadNVMissing(t, repoRoot, runner, id)
	}
}

func TestMcpLSPWorkloadRunnerReceiptRoundTrip(t *testing.T) {
	repoRoot := repoRootForMcpLSPWorkloadCatalogGuard(t)
	runner := filepath.Join(repoRoot, mcpLSPWorkloadRunnerPath)
	guard := filepath.Join(repoRoot, "scripts", "check_mcp_lsp_workload_catalog.sh")
	receipt := filepath.Join(t.TempDir(), "quick-receipt.json")
	run := exec.Command("bash", runner, "--id", "mcp-lsp-idle-quick", "--receipt", receipt)
	run.Dir = repoRoot
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("quick runner failed: %v\n%s", err, output)
	}
	check := exec.Command("bash", guard, "--receipt", receipt, "--id", "mcp-lsp-idle-quick")
	check.Dir = repoRoot
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("quick receipt guard failed: %v\n%s", err, output)
	}
}

func assertMcpLSPWorkloadNVMissing(t *testing.T, repoRoot, runner, id string) {
	t.Helper()
	cmd := exec.Command("bash", runner, "--id", id)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("N/V workload %s unexpectedly succeeded: %s", id, output)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("N/V workload %s exit = %v, want exit 1; output=%s", id, err, output)
	}
	for _, want := range []string{"implementation_status=missing", "t6_blocking=true", "release_blocking=true"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("N/V output for %s missing %q: %s", id, want, output)
		}
	}
}

func TestMcpLSPWorkloadCatalogConsumersReferenceOnlyIDs(t *testing.T) {
	repoRoot := repoRootForMcpLSPWorkloadCatalogGuard(t)
	consumerFiles := []string{"Makefile", "scripts/ai_maintenance/main.go", "scripts/ai_maintenance/owned_gate_execution.go", ".githooks/README.md"}
	for _, relative := range consumerFiles {
		raw, err := os.ReadFile(filepath.Join(repoRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if relative == "Makefile" {
			legacy := "$(TEST_WITH_GUARD) --quick-guard -tags=e2e ./cmd/mcp-lsp -run '^TestMcpLSPBinary(LinkedWorktreesResourceCohortRecycleAndRecover|ResourceCohortMalformedReportQuarantine)_E2E$$' -v -timeout 240s -count=1"
			text = strings.ReplaceAll(text, legacy, "")
		}
		if strings.Contains(text, "go test ./cmd/mcp-lsp") || strings.Contains(text, "-tags=e2e ./cmd/mcp-lsp") {
			t.Fatalf("%s copies mcp-lsp command truth instead of catalog ID", relative)
		}
	}
}

func readMcpLSPWorkloadCatalogGuardDocument(t *testing.T) mcpLSPWorkloadCatalogGuardDocument {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRootForMcpLSPWorkloadCatalogGuard(t), mcpLSPWorkloadCatalogPath))
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	var document mcpLSPWorkloadCatalogGuardDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	return document
}

func repoRootForMcpLSPWorkloadCatalogGuard(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return filepath.Clean(filepath.Join(wd, ".."))
	}
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal(fmt.Errorf("repository root not found from %q", wd))
		}
		root = parent
	}
}
