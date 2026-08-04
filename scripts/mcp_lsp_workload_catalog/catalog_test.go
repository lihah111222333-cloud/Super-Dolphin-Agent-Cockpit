package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func canonicalReceipt(document Catalog) Receipt {
	workload := document.Workloads[0]
	return Receipt{
		Schema: ReceiptSchema, WorkloadID: workload.ID, CatalogDigest: document.CatalogDigest,
		RunnerTarget: workload.RunnerTarget, ProducerWorkflowPath: workload.ProducerWorkflowPath,
		ProducerArtifactName: workload.ProducerArtifactName, ExecutionOrigin: "local-runner",
		Platform: runtime.GOOS, TimeoutSeconds: workload.TimeoutSeconds, Command: workload.Command,
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
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("name: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	receiptRequired := true
	document := Catalog{
		Schema: Schema,
		Workloads: []Workload{{
			ID: "quick", ImplementationStatus: "implemented", RunnerTarget: "local-go-test", Platforms: []string{runtime.GOOS},
			TimeoutSeconds: 10, TriggerClass: "quick", ReceiptSchema: ReceiptSchema,
			ProducerWorkflowPath: ".github/workflows/ci.yml", ProducerArtifactName: "quick-receipt",
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
