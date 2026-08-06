package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestValidateRejectsCatalogMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Catalog)
		want   string
	}{
		{name: "schema", mutate: func(document *Catalog) { document.Schema = "wrong/v1" }, want: "schema"},
		{name: "digest", mutate: func(document *Catalog) { document.CatalogDigest = "sha256:" + strings.Repeat("0", 64) }, want: "digest mismatch"},
		{name: "receipt", mutate: func(document *Catalog) { document.Workloads[0].ReceiptRequired = nil }, want: "receipt schema/flag"},
		{name: "producer", mutate: func(document *Catalog) { document.Workloads[0].ProducerArtifactName = "" }, want: "producer coordinates"},
		{name: "implemented command", mutate: func(document *Catalog) { document.Workloads[0].Command = nil }, want: "catalog command"},
		{name: "unsafe command", mutate: func(document *Catalog) { document.Workloads[0].Command = []string{"../go"} }, want: "unsafe command"},
		{name: "missing command", mutate: func(document *Catalog) { document.Workloads[0].ImplementationStatus = "missing" }, want: "missing implementation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, raw, root := validFixture(t)
			test.mutate(&document)
			if test.name != "digest" {
				raw = encodeWithDigest(t, &document)
			}
			err := Validate(document, raw, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsUnavailableProducerWorkflow(t *testing.T) {
	document, raw, root := validFixture(t)
	document.Workloads[0].ProducerWorkflowPath = "missing.yml"
	raw = encodeWithDigest(t, &document)
	if err := Validate(document, raw, root); err == nil || !strings.Contains(err.Error(), "producer workflow path") {
		t.Fatalf("Validate() error = %v, want unavailable producer workflow", err)
	}
}

func TestValidateRejectsProducerCoordinateMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Catalog, string)
		want   string
	}{
		{name: "absolute workflow", mutate: func(document *Catalog, _ string) {
			document.Workloads[0].ProducerWorkflowPath = "/tmp/ci.yml"
		}, want: "producer workflow path"},
		{name: "parent workflow", mutate: func(document *Catalog, _ string) {
			document.Workloads[0].ProducerWorkflowPath = "../ci.yml"
		}, want: "producer workflow path"},
		{name: "nested parent workflow", mutate: func(document *Catalog, _ string) {
			document.Workloads[0].ProducerWorkflowPath = ".github/workflows/../../ci.yml"
		}, want: "producer workflow path"},
		{name: "windows absolute workflow", mutate: func(document *Catalog, _ string) {
			document.Workloads[0].ProducerWorkflowPath = `C:\ci.yml`
		}, want: "producer workflow path"},
		{name: "missing artifact declaration", mutate: func(document *Catalog, root string) {
			document.Workloads[0].ProducerArtifactName = "not-uploaded"
			_ = os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("name: test\n"), 0o644)
		}, want: "artifact"},
		{name: "split artifact coordinates", mutate: func(document *Catalog, root string) {
			workflow := "name: test\njobs:\n  receipt:\n    steps:\n      - uses: actions/upload-artifact@v4\n        with:\n          name: quick-receipt\n      - uses: actions/upload-artifact@v4\n        with:\n          path: receipt.json\n"
			_ = os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte(workflow), 0o644)
		}, want: "artifact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, raw, root := validFixture(t)
			test.mutate(&document, root)
			raw = encodeWithDigest(t, &document)
			if err := Validate(document, raw, root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsProducerWorkflowSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires platform support")
	}
	document, raw, root := validFixture(t)
	link := filepath.Join(root, ".github", "workflows", "linked.yml")
	if err := os.Symlink("ci.yml", link); err != nil {
		t.Fatal(err)
	}
	document.Workloads[0].ProducerWorkflowPath = ".github/workflows/linked.yml"
	raw = encodeWithDigest(t, &document)
	if err := Validate(document, raw, root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Validate() error = %v, want symlink rejection", err)
	}
}

func TestValidateAllowsProducerPathWithBasenameDirectory(t *testing.T) {
	document, raw, root := validFixture(t)
	directory := filepath.Join(root, ".github", "workflows", "nested.yml")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := "name: test\njobs:\n  receipt:\n    steps:\n      - uses: actions/upload-artifact@v4\n        with:\n          name: quick-receipt\n          path: receipt.json\n"
	workflowPath := filepath.Join(directory, "nested.yml")
	if err := os.WriteFile(workflowPath, []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	document.Workloads[0].ProducerWorkflowPath = ".github/workflows/nested.yml/nested.yml"
	raw = encodeWithDigest(t, &document)
	if err := Validate(document, raw, root); err != nil {
		t.Fatalf("Validate() error = %v, want basename directory accepted", err)
	}
}

func TestValidateRejectsProducerWorkflowIntermediateSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires platform support")
	}
	document, raw, root := validFixture(t)
	outside := t.TempDir()
	outsideWorkflow := filepath.Join(outside, "ci.yml")
	if err := os.WriteFile(outsideWorkflow, []byte("name: outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(root, ".github", "workflows", "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	document.Workloads[0].ProducerWorkflowPath = ".github/workflows/escape/ci.yml"
	raw = encodeWithDigest(t, &document)
	if err := Validate(document, raw, root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Validate() error = %v, want intermediate symlink rejection", err)
	}
}

func TestValidateRejectsMissingProducerWithoutReleaseBlocking(t *testing.T) {
	document, raw, root := validFixture(t)
	document.Workloads[0].ProducerImplementationStatus = "missing"
	document.Workloads[0].ReleaseBlocking = false
	raw = encodeWithDigest(t, &document)
	if err := Validate(document, raw, root); err == nil || !strings.Contains(err.Error(), "release_blocking") {
		t.Fatalf("Validate() error = %v, want release_blocking rejection", err)
	}
}

func TestValidateReceiptMissingProducerCannotClaimCIReleaseAuthority(t *testing.T) {
	document, _, _ := validFixture(t)
	document.Workloads[0].ProducerImplementationStatus = "missing"
	receipt := canonicalReceipt(document)
	receipt.ExecutionOrigin = "ci"
	path := writeReceiptFixture(t, receipt)
	if err := ValidateReceipt(document, "quick", path); err == nil || !strings.Contains(err.Error(), "cannot be trusted as CI/release") {
		t.Fatalf("ValidateReceipt() error = %v, want CI/release authority rejection", err)
	}
}

func TestValidateRejectsTimeoutDurationOverflow(t *testing.T) {
	document, raw, root := validFixture(t)
	document.Workloads[0].TimeoutSeconds = int(^uint(0) >> 1)
	raw = encodeWithDigest(t, &document)
	if err := Validate(document, raw, root); err == nil || !strings.Contains(err.Error(), "timeout_seconds") {
		t.Fatalf("Validate() error = %v, want timeout overflow rejection", err)
	}
}

func TestValidateReceiptAcceptsCanonicalReceipt(t *testing.T) {
	document, _, _ := validFixture(t)
	path := writeReceiptFixture(t, canonicalReceipt(document))
	if err := ValidateReceipt(document, "quick", path); err != nil {
		t.Fatalf("ValidateReceipt() error = %v", err)
	}
}

func TestLoadRejectsTrailingCatalogJSON(t *testing.T) {
	document, raw, root := validFixture(t)
	if document.Schema == "" {
		t.Fatal("fixture schema is required")
	}
	path := filepath.Join(root, filepath.FromSlash(Path))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("Load() error = %v, want trailing JSON rejection", err)
	}
}

func TestValidateReceiptRejectsReceiptMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "extra", mutate: func(value map[string]any) { value["unexpected"] = true }, want: "decode workload receipt"},
		{name: "missing origin", mutate: func(value map[string]any) { delete(value, "execution_origin") }, want: "execution origin"},
		{name: "remote origin", mutate: func(value map[string]any) { value["execution_origin"] = "remote-ci" }, want: "unsupported execution origin"},
		{name: "platform", mutate: func(value map[string]any) { value["platform"] = "windows" }, want: "platform"},
		{name: "timeout", mutate: func(value map[string]any) { value["timeout_seconds"] = float64(11) }, want: "timeout mismatch"},
		{name: "command", mutate: func(value map[string]any) { value["command"] = []string{"go", "vet"} }, want: "command mismatch"},
		{name: "time order", mutate: func(value map[string]any) { value["finished_at"] = "2026-08-03T23:59:59Z" }, want: "precedes"},
		{name: "duration", mutate: func(value map[string]any) { value["finished_at"] = "2026-08-04T00:00:11Z" }, want: "exceeds timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, _, _ := validFixture(t)
			raw, err := json.Marshal(canonicalReceipt(document))
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			mutated, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "receipt.json")
			if err := os.WriteFile(path, mutated, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ValidateReceipt(document, "quick", path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateReceipt() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateReceiptRejectsTrailingJSON(t *testing.T) {
	document, _, _ := validFixture(t)
	raw, err := json.Marshal(canonicalReceipt(document))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, append(raw, []byte("{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceipt(document, "quick", path); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("ValidateReceipt() error = %v, want trailing JSON rejection", err)
	}
}

func TestValidateReceiptRejectsMissingImplementation(t *testing.T) {
	document, _, _ := validFixture(t)
	document.Workloads[0].ImplementationStatus = "missing"
	path := writeReceiptFixture(t, canonicalReceipt(document))
	if err := ValidateReceipt(document, "quick", path); err == nil || !strings.Contains(err.Error(), "implementation_status=missing") {
		t.Fatalf("ValidateReceipt() error = %v, want missing implementation rejection", err)
	}
}

func TestRemoteTestSelectorsProjectsCatalogGoTestCommand(t *testing.T) {
	selectors, err := RemoteTestSelectors([]string{
		"go", "test", "./cmd/mcp-lsp", "-tags=e2e",
		"-run", "^Test(Alpha|Beta)$", "-count=1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./cmd/mcp-lsp#TestAlpha", "./cmd/mcp-lsp#TestBeta"}
	if !slices.Equal(selectors, want) {
		t.Fatalf("RemoteTestSelectors() = %v, want %v", selectors, want)
	}
}

func TestRemoteTestSelectorsRejectsShellOrRegexCommand(t *testing.T) {
	for _, command := range [][]string{
		{"sh", "-c", "go test ./cmd/mcp-lsp"},
		{"go", "test", "./cmd/mcp-lsp", "-run", "Test.*"},
	} {
		if _, err := RemoteTestSelectors(command); err == nil {
			t.Fatalf("RemoteTestSelectors(%v) unexpectedly succeeded", command)
		}
	}
}

func TestDefault15mReceiptRemainsNVWithoutRemoteAuthority(t *testing.T) {
	document, root, receipt, completionPath := default15mReceiptFixture(t)
	if err := AttachCompletionProvenance(&receipt, root, completionPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("AttachCompletionProvenance() error = %v, want remote authority N/V", err)
	}
	receiptPath := filepath.Join(root, "receipt.json")
	writeReceiptJSON(t, receiptPath, receipt)
	if err := ValidateReceiptAt(document, root, document.Workloads[0].ID, receiptPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceiptAt() error = %v, want remote authority N/V", err)
	}
	if err := ValidateCompletionReceipt(root, completionPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateCompletionReceipt() error = %v, want remote authority N/V", err)
	}
}

func TestDefault15mReceiptRejectsCompletionChainAndGitDrift(t *testing.T) {
	document, root, receipt, completionPath := default15mReceiptFixture(t)
	if err := AttachCompletionProvenance(&receipt, root, completionPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("AttachCompletionProvenance() error = %v, want remote authority N/V", err)
	}
	receiptPath := filepath.Join(root, "receipt.json")
	writeReceiptJSON(t, receiptPath, receipt)
	if err := ValidateReceiptAt(document, root, document.Workloads[0].ID, receiptPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceiptAt() error = %v, want remote authority N/V", err)
	}
	badCompletion := filepath.Join(root, "completion-bad.json")
	proof := defaultCompletionProof(t, root)
	proof["action_order"] = []string{"mark_draining", "completed"}
	writeJSON(t, badCompletion, proof)
	if err := AttachCompletionProvenance(&receipt, root, badCompletion); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("AttachCompletionProvenance() error = %v, want remote authority N/V", err)
	}
}

func TestRequireRemoteCompletionAuthorityFailsBeforeExecution(t *testing.T) {
	workload := Workload{ID: default15mWorkloadID, ImplementationStatus: "implemented", ProducerImplementationStatus: "implemented"}
	if err := RequireRemoteCompletionAuthority(workload); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("RequireRemoteCompletionAuthority() error = %v, want fail-closed authority error", err)
	}
	short := Workload{ID: "mcp-lsp-idle-quick", TriggerClass: "quick", ImplementationStatus: "implemented", ProducerImplementationStatus: "implemented"}
	if err := RequireRemoteCompletionAuthority(short); err != nil {
		t.Fatalf("RequireRemoteCompletionAuthority(short) error = %v, want nil", err)
	}
}

func TestAttachCompletionProvenanceMapsAndComparesRemoteAuthority(t *testing.T) {
	_, root, receipt, completionPath := default15mReceiptFixture(t)
	receipt.WorkloadID = "mcp-lsp-idle-quick"
	proof := mustDecodeCompletionProof(t, completionPath)
	if err := AttachCompletionProvenance(&receipt, root, completionPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("AttachCompletionProvenance() error = %v, want final authority N/V", err)
	}
	assertRemoteAuthorityMapping(t, receipt, proof)
	assertRemoteAuthorityMismatches(t, receipt, proof)
}

func mustDecodeCompletionProof(t *testing.T, path string) completionProof {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := decodeCompletionProof(raw)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func assertRemoteAuthorityMapping(t *testing.T, receipt Receipt, proof completionProof) {
	t.Helper()
	if receipt.RemoteRunID != proof.RemoteRunID || receipt.RemoteJobID != proof.RemoteJobID ||
		receipt.RemoteArtifactName != proof.RemoteArtifactName || receipt.RemoteArtifactDigest != proof.RemoteArtifactDigest {
		t.Fatalf("AttachCompletionProvenance() remote fields = (%q, %q, %q, %q), want proof values", receipt.RemoteRunID, receipt.RemoteJobID, receipt.RemoteArtifactName, receipt.RemoteArtifactDigest)
	}
}

func assertRemoteAuthorityMismatches(t *testing.T, receipt Receipt, proof completionProof) {
	t.Helper()
	mutations := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "run", mutate: func(value *Receipt) { value.RemoteRunID = "other-run" }},
		{name: "job", mutate: func(value *Receipt) { value.RemoteJobID = "other-job" }},
		{name: "artifact name", mutate: func(value *Receipt) { value.RemoteArtifactName = "other-artifact" }},
		{name: "artifact digest", mutate: func(value *Receipt) { value.RemoteArtifactDigest = "sha256:" + strings.Repeat("9", 64) }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			candidate := receipt
			test.mutate(&candidate)
			if err := validateCompletionProof(proof, candidate, candidate.WorkloadID); err == nil || !strings.Contains(err.Error(), "provenance chain mismatch") {
				t.Fatalf("validateCompletionProof() error = %v, want remote field mismatch", err)
			}
		})
	}
}

func TestAttachCompletionProvenanceRejectsMissingRemoteAuthorityFields(t *testing.T) {
	_, root, receipt, _ := default15mReceiptFixture(t)
	receipt.WorkloadID = "mcp-lsp-idle-quick"
	for _, field := range []string{"remote_run_id", "remote_job_id", "remote_artifact_name", "remote_artifact_digest"} {
		t.Run(field, func(t *testing.T) {
			proof := defaultCompletionProof(t, root)
			delete(proof, field)
			path := filepath.Join(t.TempDir(), field+".json")
			writeJSON(t, path, proof)
			candidate := receipt
			err := AttachCompletionProvenance(&candidate, root, path)
			want := fmt.Sprintf("completion receipt field %q is required", field)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("AttachCompletionProvenance() error = %v, want missing-field error %q", err, want)
			}
		})
	}
}

func TestLoadAtBindsResolvedGitTreeDespiteWorkingTreeDrift(t *testing.T) {
	document, raw, root := validFixture(t)
	document.Workloads[0].Command = []string{"go", "test", "./cmd/mcp-lsp", "-run", "^TestFoo$"}
	raw = encodeWithDigest(t, &document)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, Path)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, Path), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	runCatalogGit(t, root, "init", "--quiet")
	runCatalogGit(t, root, "config", "user.name", "catalog-test")
	runCatalogGit(t, root, "config", "user.email", "catalog-test@example.invalid")
	runCatalogGit(t, root, "add", ".")
	runCatalogGit(t, root, "commit", "--quiet", "-m", "候选目录")
	_, tree, err := currentGitIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	candidateID := document.Workloads[0].ID
	// Working tree drift is valid JSON with a new digest, but must not change
	// the catalog decision already bound to the candidate tree.
	drift := document
	drift.Workloads = append([]Workload(nil), document.Workloads...)
	drift.Workloads[0].ID = "working-tree-drift"
	driftRaw := encodeWithDigest(t, &drift)
	if err := os.WriteFile(filepath.Join(root, Path), driftRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("name: worktree-drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err := LoadAt(root, tree)
	if err != nil {
		t.Fatalf("LoadAt() error = %v", err)
	}
	if candidate.Workloads[0].ID != candidateID {
		t.Fatalf("LoadAt() workload ID = %q, want candidate %q", candidate.Workloads[0].ID, candidateID)
	}
	_, err = Load(root)
	if err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("Load() drifted catalog error = %v, want candidate workflow/artifact rejection", err)
	}
}

func TestValidateCompletionReceiptForCandidateRejectsWorkingTreeIdentity(t *testing.T) {
	_, root, _, completionPath := default15mReceiptFixture(t)
	gitHead, tree, err := currentGitIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompletionReceiptForCandidate(gitHead, tree, completionPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateCompletionReceiptForCandidate() error = %v, want remote authority N/V", err)
	}
	if err := ValidateCompletionReceiptForCandidate(strings.Repeat("0", 40), tree, completionPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateCompletionReceiptForCandidate() error = %v, want remote authority N/V", err)
	}
}

func default15mReceiptFixture(t *testing.T) (Catalog, string, Receipt, string) {
	t.Helper()
	root := t.TempDir()
	runCatalogGit(t, root, "init", "--quiet")
	runCatalogGit(t, root, "config", "user.name", "catalog-test")
	runCatalogGit(t, root, "config", "user.email", "catalog-test@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCatalogGit(t, root, "add", "source.txt")
	runCatalogGit(t, root, "commit", "--quiet", "-m", "初始化")
	receiptRequired := true
	workload := Workload{ID: "mcp-lsp-default-15m", ImplementationStatus: "implemented", ProducerImplementationStatus: "implemented", RunnerTarget: "remote-gate-test", Platforms: []string{runtime.GOOS}, TimeoutSeconds: 1500, TriggerClass: default15mTriggerClass, ReceiptSchema: ReceiptSchema, ProducerWorkflowPath: ".github/workflows/ci.yml", ProducerArtifactName: "mcp-lsp-default-15m-receipt", T6Blocking: true, ReleaseBlocking: true, ReceiptRequired: &receiptRequired, Command: []string{"go", "test", "./cmd/mcp-lsp", "-run", "^TestFoo$"}}
	document := Catalog{Schema: Schema, Workloads: []Workload{workload}}
	raw := encodeWithDigest(t, &document)
	_ = raw
	receipt := Receipt{Schema: ReceiptSchema, WorkloadID: workload.ID, CatalogDigest: document.CatalogDigest, RunnerTarget: workload.RunnerTarget, ProducerWorkflowPath: workload.ProducerWorkflowPath, ProducerArtifactName: workload.ProducerArtifactName, ProducerImplementationStatus: workload.ProducerImplementationStatus, ExecutionOrigin: "local-runner", Platform: runtime.GOOS, TimeoutSeconds: workload.TimeoutSeconds, Command: workload.Command, StartedAt: "2026-08-07T00:00:00.000000000Z", FinishedAt: "2026-08-07T00:00:01.000000000Z", WorkloadStartedAt: "2026-08-07T00:00:00.000000000Z", WorkloadFinishedAt: "2026-08-07T00:00:01.000000000Z", Status: "pass", ExitCode: 0}
	completionPath := filepath.Join(root, "completion.json")
	writeJSON(t, completionPath, defaultCompletionProof(t, root))
	return document, root, receipt, completionPath
}

func defaultCompletionProof(t *testing.T, root string) map[string]any {
	t.Helper()
	gitHead, tree, err := currentGitIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"git_head": gitHead, "source_tree_digest": tree, "cohort_id": "sha256:" + strings.Repeat("1", 64), "repository_instance_proof_hash": "sha256:" + strings.Repeat("2", 64), "epoch": uint64(3), "daemon_owner_receipt_hash": "sha256:" + strings.Repeat("3", 64), "remote_run_id": "run-1", "remote_job_id": "job-1", "remote_artifact_name": "mcp-lsp-default-15m-receipt", "remote_artifact_digest": "sha256:" + strings.Repeat("4", 64), "action_order": completionActionOrder, "forwarder_count_after": 0, "daemon_observed_after": false, "telemetry_identities_gone": true, "endpoint_unreachable": true, "native_owner_released": true, "quiet_window_verified": true, "next_epoch": uint64(4), "status": "completed",
	}
}

func runCatalogGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeReceiptJSON(t *testing.T, path string, value Receipt) {
	t.Helper()
	if value.WorkloadID == "" {
		t.Fatal(fmt.Errorf("receipt workload ID is empty"))
	}
	writeJSON(t, path, value)
}

func canonicalReceipt(document Catalog) Receipt {
	workload := document.Workloads[0]
	return Receipt{
		Schema: ReceiptSchema, WorkloadID: workload.ID, CatalogDigest: document.CatalogDigest,
		RunnerTarget: workload.RunnerTarget, ProducerWorkflowPath: workload.ProducerWorkflowPath,
		ProducerArtifactName: workload.ProducerArtifactName, ProducerImplementationStatus: workload.ProducerImplementationStatus,
		ExecutionOrigin: "local-runner",
		Platform:        runtime.GOOS, TimeoutSeconds: workload.TimeoutSeconds, Command: workload.Command,
		StartedAt: "2026-08-04T00:00:00.000000000Z", FinishedAt: "2026-08-04T00:00:01.000000000Z",
		Status: "pass", ExitCode: 0,
	}
}

func writeReceiptFixture(t *testing.T, value Receipt) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validFixture(t *testing.T) (Catalog, []byte, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := "name: test\njobs:\n  receipt:\n    steps:\n      - uses: actions/upload-artifact@v4\n        with:\n          name: quick-receipt\n          path: receipt.json\n"
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	receiptRequired := true
	document := Catalog{
		Schema: Schema,
		Workloads: []Workload{{
			ID: "quick", ImplementationStatus: "implemented", RunnerTarget: "local-go-test", Platforms: []string{runtime.GOOS},
			TimeoutSeconds: 10, TriggerClass: "quick", ReceiptSchema: ReceiptSchema,
			ProducerImplementationStatus: "implemented",
			ProducerWorkflowPath:         ".github/workflows/ci.yml", ProducerArtifactName: "quick-receipt",
			T6Blocking: true, ReleaseBlocking: true, ReceiptRequired: &receiptRequired, Command: []string{"go", "test"},
		}},
	}
	raw := encodeWithDigest(t, &document)
	return document, raw, root
}

func encodeWithDigest(t *testing.T, document *Catalog) []byte {
	t.Helper()
	document.CatalogDigest = "sha256:" + strings.Repeat("0", 64)
	without, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := CanonicalDigest(without)
	if err != nil {
		t.Fatal(err)
	}
	document.CatalogDigest = digest
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
