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

func TestConsumerDiscoveryRejectsIndirectShellCatalogCommands(t *testing.T) {
	for _, source := range []string{
		"runner=./scripts/run_mcp_lsp_workload.sh; exec \"$runner\" --tree deadbeef\n",
		"runners=(./scripts/run_mcp_lsp_workload.sh); exec \"${runners[0]}\"\n",
		"output=$(./scripts/check_mcp_lsp_workload_catalog.sh --tree deadbeef)\n",
		"eval './scripts/run_mcp_lsp_workload.sh --tree deadbeef'\n",
	} {
		if _, err := IsWorkloadConsumerSource("scripts/indirect.sh", []byte(source), nil); err == nil {
			t.Fatalf("IsWorkloadConsumerSource accepted indirect shell command %q", source)
		}
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
	document, _, root := validFixture(t)
	document.Workloads[0].ProducerImplementationStatus = "missing"
	receipt := canonicalReceipt(document)
	receipt.ExecutionOrigin = "ci"
	path := writeReceiptFixture(t, receipt)
	if err := ValidateReceiptAt(document, root, "quick", path); err == nil || !strings.Contains(err.Error(), "cannot be trusted as CI/release") {
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
	document, _, root := validFixture(t)
	path := writeReceiptFixture(t, canonicalReceipt(document))
	if err := ValidateReceiptAt(document, root, "quick", path); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceipt() error = %v, want local receipt authority rejection", err)
	}
}

func TestValidateReceiptAtCandidateRejectsRequestedTreeDrift(t *testing.T) {
	document, _, root := validFixture(t)
	runCatalogGit(t, root, "init", "--quiet")
	runCatalogGit(t, root, "config", "user.name", "catalog-test")
	runCatalogGit(t, root, "config", "user.email", "catalog-test@example.invalid")
	runCatalogGit(t, root, "add", ".")
	runCatalogGit(t, root, "commit", "--quiet", "-m", "候选")
	gitHead, tree, err := currentGitIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	receipt := canonicalReceipt(document)
	receipt.SourceTreeDigest = tree
	receipt.GitHead = gitHead
	path := writeReceiptFixture(t, receipt)
	if err := os.WriteFile(filepath.Join(root, "head-drift.txt"), []byte("new head\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCatalogGit(t, root, "add", "head-drift.txt")
	runCatalogGit(t, root, "commit", "--quiet", "-m", "HEAD 漂移")
	newHead, _, err := currentGitIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceiptAtCandidate(document, root, strings.Repeat("b", 40), "quick", path); err == nil || !strings.Contains(err.Error(), "source tree") {
		t.Fatalf("ValidateReceiptAtCandidate() error = %v, want requested tree mismatch", err)
	}
	if err := ValidateReceiptAtCandidate(document, root, tree, "quick", path); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceiptAtCandidate() error = %v, want identity-valid but non-authoritative receipt", err)
	}
	receipt.GitHead = newHead
	path = writeReceiptFixture(t, receipt)
	if err := ValidateReceiptAtCandidate(document, root, tree, "quick", path); err == nil || !strings.Contains(err.Error(), "not bound to candidate tree") {
		t.Fatalf("ValidateReceiptAtCandidate() error = %v, want git_head/tree binding rejection", err)
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
			document, _, root := validFixture(t)
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
			if err := ValidateReceiptAt(document, root, "quick", path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateReceipt() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateReceiptRejectsTrailingJSON(t *testing.T) {
	document, _, root := validFixture(t)
	raw, err := json.Marshal(canonicalReceipt(document))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, append(raw, []byte("{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReceiptAt(document, root, "quick", path); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("ValidateReceipt() error = %v, want trailing JSON rejection", err)
	}
}

func TestValidateReceiptRejectsMissingImplementation(t *testing.T) {
	document, _, root := validFixture(t)
	document.Workloads[0].ImplementationStatus = "missing"
	path := writeReceiptFixture(t, canonicalReceipt(document))
	if err := ValidateReceiptAt(document, root, "quick", path); err == nil || !strings.Contains(err.Error(), "implementation_status=missing") {
		t.Fatalf("ValidateReceipt() error = %v, want missing implementation rejection", err)
	}
}

func TestProjectRemoteTestCommandFromRawReusesCanonicalDigestAndParser(t *testing.T) {
	document := Catalog{
		Schema:        Schema,
		CatalogDigest: "sha256:" + strings.Repeat("0", 64),
		Workloads: []Workload{{
			ID:                   "mcp-lsp-idle-quick",
			ImplementationStatus: "implemented",
			Command:              []string{"go", "test", "./cmd/mcp-lsp/a", "-run", "^TestA$"},
		}},
	}
	raw := encodeWithDigest(t, &document)
	workloads, err := ParseImplementedRemoteTestsFromRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(workloads) != 1 || !slices.Equal(workloads[0].Command.Packages, []string{"./cmd/mcp-lsp/a"}) || !slices.Equal(workloads[0].Command.TestNames, []string{"TestA"}) {
		t.Fatalf("ParseImplementedRemoteTestsFromRaw() = %#v", workloads)
	}
	var tampered map[string]any
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["workloads"].([]any)[0].(map[string]any)["timeout_seconds"] = float64(241)
	raw, err = json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseImplementedRemoteTestsFromRaw(raw); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("ParseImplementedRemoteTestsFromRaw() error = %v, want digest mismatch", err)
	}
}

func TestProjectRemoteTestCommandRejectsUnknownDuplicateAndNonCanonicalFlags(t *testing.T) {
	for _, test := range []struct {
		name    string
		command []string
		want    string
	}{
		{name: "unknown", command: []string{"go", "test", "./pkg", "-shuffle=on"}, want: "unsupported flag"},
		{name: "unsupported count", command: []string{"go", "test", "./pkg", "-count=1"}, want: "not carried"},
		{name: "unsupported timeout", command: []string{"go", "test", "./pkg", "-timeout=-1s"}, want: "not carried"},
		{name: "duplicate run", command: []string{"go", "test", "./pkg", "-run=^TestAlpha$", "-run", "^TestBeta$"}, want: "repeats flag"},
		{name: "empty run", command: []string{"go", "test", "./pkg", "-run="}, want: "selector is empty"},
		{name: "non canonical test", command: []string{"go", "test", "./pkg", "-run=^Testalpha$"}, want: "canonical Test name"},
		{name: "duplicate test", command: []string{"go", "test", "./pkg", "-run=^Test(?:Alpha|Alpha)$"}, want: "repeats test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseRemoteTestCommand(test.command); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseRemoteTestCommand(%v) error = %v, want %q", test.command, err, test.want)
			}
		})
	}

	if _, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-run=^TestAlpha$", "-tags", "linux"}); err == nil || !strings.Contains(err.Error(), "not carried") {
		t.Fatalf("ParseRemoteTestCommand() error = %v, want unsupported tags rejection", err)
	}
}

func TestProjectRemoteTestCommandRejectsHostDerivedEnvironment(t *testing.T) {
	command, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-run", "^TestAlpha$"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ProjectRemoteTestCommand(command, RemoteTestEnvironment{GOOS: "darwin", GOARCH: "amd64"}, nil)
	if err == nil || !strings.Contains(err.Error(), "linux/amd64") {
		t.Fatalf("ProjectRemoteTestCommand() error = %v, want fixed remote platform rejection", err)
	}
}

func TestParseRemoteTestCommandRejectsUngroupedAlternation(t *testing.T) {
	if _, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-run", "^TestAlpha|Beta$"}); err == nil || !strings.Contains(err.Error(), "exact Test selector") {
		t.Fatalf("ParseRemoteTestCommand() error = %v, want ungrouped alternation rejection", err)
	}
	command, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-run", "^Test(?:Alpha|Beta)$"})
	if err != nil || !slices.Equal(command.TestNames, []string{"TestAlpha", "TestBeta"}) {
		t.Fatalf("ParseRemoteTestCommand(non-capturing alternation) = %#v, %v", command, err)
	}
}

func TestParseRemoteTestCommandRejectsGoIdentifierMetacharactersAndEmptyTags(t *testing.T) {
	for _, selector := range []string{"^Test.Alpha$", "^Test*Alpha$", "^Test[Alpha]$", `^Test\u0041$`} {
		if _, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-run", selector}); err == nil {
			t.Fatalf("ParseRemoteTestCommand accepted Go identifier metacharacter selector %q", selector)
		}
	}
	if _, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-run", "^Test(Alpha|Beta)$"}); err == nil {
		t.Fatal("ParseRemoteTestCommand accepted capturing-group alternation")
	}
	for _, tags := range []string{"foo,,bar", ",foo", "foo,", "foo,\tbar", "foo/bar"} {
		if _, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-tags", tags}); err == nil {
			t.Fatalf("ParseRemoteTestCommand accepted invalid raw tag list %q", tags)
		}
	}
}

func TestProjectRemoteTestCommandRejectsInvalidTopLevelTestSignature(t *testing.T) {
	command, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg", "-run", "^TestAlpha$"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ProjectRemoteTestCommand(command, RemoteTestEnvironment{GOOS: "linux", GOARCH: "amd64"}, []RemoteTestPackageFiles{{
		Package: "./pkg",
		Files:   []RemoteTestFile{{Path: "pkg/pkg_test.go", Name: "pkg_test.go", Source: []byte("package pkg\nimport \"testing\"\nfunc TestAlpha() {}\n")}},
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("ProjectRemoteTestCommand() error = %v, want invalid Go test signature", err)
	}
}

func TestProjectRemoteTestCommandProducesExactPairsWithoutCartesianExpansion(t *testing.T) {
	command, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg/a", "./pkg/b", "-run", "^Test(?:Alpha|Beta)$"})
	if err != nil {
		t.Fatal(err)
	}
	packages := []RemoteTestPackageFiles{
		{Package: "./pkg/a", Files: []RemoteTestFile{{Path: "pkg/a/a_test.go", Name: "a_test.go", Source: []byte("package a\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\n")}}},
		{Package: "./pkg/b", Files: []RemoteTestFile{{Path: "pkg/b/b_test.go", Name: "b_test.go", Source: []byte("package b\nimport \"testing\"\nfunc TestBeta(t *testing.T) {}\n")}}},
	}
	projection, err := ProjectRemoteTestCommand(command, RemoteTestEnvironment{GOOS: "linux", GOARCH: "amd64"}, packages)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./pkg/a#TestAlpha", "./pkg/b#TestBeta"}
	if !slices.Equal(projection.SelectorStrings(), want) {
		t.Fatalf("ProjectRemoteTestCommand() = %v, want %v", projection.SelectorStrings(), want)
	}
}

func TestParseRemoteTestCommandRejectsShellOrRegexCommand(t *testing.T) {
	for _, command := range [][]string{
		{"sh", "-c", "go test ./cmd/mcp-lsp"},
		{"go", "test", "./cmd/mcp-lsp", "-run", "Test.*"},
	} {
		if _, err := ParseRemoteTestCommand(command); err == nil {
			t.Fatalf("ParseRemoteTestCommand(%v) unexpectedly succeeded", command)
		}
	}
}

func TestProjectRemoteTestCommandRejectsNonCanonicalPackagePaths(t *testing.T) {
	for _, packagePath := range []string{
		"./cmd/mcp-lsp/fake/",
		"./cmd/mcp-lsp/fake/.",
		"./cmd//mcp-lsp/fake",
	} {
		t.Run(packagePath, func(t *testing.T) {
			command := []string{"go", "test", packagePath, "-run", "^Test(Fake)$"}
			if _, err := ParseRemoteTestCommand(command); err == nil {
				t.Fatalf("ParseRemoteTestCommand accepted non-canonical package path %q", packagePath)
			}
		})
	}
}

func TestDefault15mReceiptRemainsNVWithoutRemoteAuthority(t *testing.T) {
	document, root, receipt := default15mReceiptFixture(t)
	receiptPath := filepath.Join(root, "receipt.json")
	writeReceiptJSON(t, receiptPath, receipt)
	if err := ValidateReceiptAt(document, root, document.Workloads[0].ID, receiptPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceiptAt() error = %v, want remote authority N/V", err)
	}
}

func TestDefault15mReceiptRejectsCompletionChainAndGitDrift(t *testing.T) {
	document, root, receipt := default15mReceiptFixture(t)
	receiptPath := filepath.Join(root, "receipt.json")
	writeReceiptJSON(t, receiptPath, receipt)
	if err := ValidateReceiptAt(document, root, document.Workloads[0].ID, receiptPath); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceiptAt() error = %v, want remote authority N/V", err)
	}
}

func TestRequireRemoteCompletionAuthorityFailsBeforeExecution(t *testing.T) {
	workload := Workload{ID: default15mWorkloadID, ImplementationStatus: "implemented", ProducerImplementationStatus: "implemented"}
	if err := RequireRemoteCompletionAuthority(workload); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("RequireRemoteCompletionAuthority() error = %v, want fail-closed authority error", err)
	}
	for _, test := range []struct {
		name     string
		workload Workload
		want     bool
	}{
		{name: "quick local", workload: Workload{ID: "mcp-lsp-idle-quick", RunnerTarget: "local-go-test", TriggerClass: "quick"}},
		{name: "native local", workload: Workload{ID: "mcp-lsp-native-process-tree", RunnerTarget: "local-go-test", TriggerClass: "native"}},
		{name: "remote target", workload: Workload{ID: "remote-soak", RunnerTarget: "remote-gate-test", TriggerClass: "soak"}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsRemoteAuthoritativeWorkload(test.workload); got != test.want {
				t.Fatalf("IsRemoteAuthoritativeWorkload() = %t, want %t for %#v", got, test.want, test.workload)
			}
			err := RequireRemoteCompletionAuthority(test.workload)
			if test.want && (err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority")) {
				t.Fatalf("RequireRemoteCompletionAuthority() error = %v, want authority N/V", err)
			}
			if !test.want && err != nil {
				t.Fatalf("RequireRemoteCompletionAuthority() error = %v, want local host execution allowed", err)
			}
		})
	}
}

func TestRequireIdleQuickReleaseRoleFreezesEveryAuthorityField(t *testing.T) {
	document, err := Load(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := document.Find(idleQuickWorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireIdleQuickReleaseRole(canonical); err != nil {
		t.Fatalf("canonical idle-quick role: %v", err)
	}
	for _, mutate := range []func(*Workload){
		func(value *Workload) { value.TriggerClass = "other" },
		func(value *Workload) { value.TimeoutSeconds++ },
		func(value *Workload) { value.Platforms = []string{"linux"} },
		func(value *Workload) { value.Command = append(slices.Clone(value.Command), "-v") },
		func(value *Workload) { value.ProducerWorkflowPath = ".github/workflows/release.yml" },
		func(value *Workload) { value.ProducerArtifactName = "other" },
	} {
		mutated := canonical
		mutate(&mutated)
		if err := RequireIdleQuickReleaseRole(mutated); err == nil {
			t.Fatal("idle-quick release role accepted an authority-field mutation")
		}
	}
}

func TestConsumerDiscoveryFindsIndirectOwnerCallsWithoutCatalogImport(t *testing.T) {
	raw := []byte("package main\nfunc indirect(options *remoteRunOptions) error { return bindMcpLSPWorkloadSelectors(options) }\n")
	found, err := IsWorkloadConsumerSource("cmd/super-dolphin-gate/new_indirect.go", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("consumer discovery omitted an indirect call into the canonical selector owner")
	}
}

func TestWorkloadGateInputPathsUseCandidateProducerAndModuleClosure(t *testing.T) {
	paths := WorkloadGateInputPaths(Catalog{Workloads: []Workload{
		{ImplementationStatus: "implemented", ProducerImplementationStatus: "implemented", ProducerWorkflowPath: ".github/workflows/candidate.yml"},
		{ImplementationStatus: "implemented", ProducerImplementationStatus: "missing", ProducerWorkflowPath: ".github/workflows/missing.yml"},
		{ImplementationStatus: "missing", ProducerImplementationStatus: "implemented", ProducerWorkflowPath: ".github/workflows/stale.yml"},
	}})
	for _, want := range []string{"go.mod", "go.sum", ".github/workflows/candidate.yml"} {
		if !slices.Contains(paths, want) {
			t.Fatalf("WorkloadGateInputPaths() omitted %q", want)
		}
	}
	if slices.Contains(paths, ".github/workflows/missing.yml") || slices.Contains(paths, ".github/workflows/stale.yml") || slices.Contains(paths, ".github/workflows/release.yml") {
		t.Fatalf("WorkloadGateInputPaths() contains non-candidate workflow: %v", paths)
	}
}

func TestIsLocalRunnerWorkloadOnlyMatchesLocalTargets(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
		want   bool
	}{
		{name: "quick local", target: "local-go-test", want: true},
		{name: "native local", target: "local-process-test", want: true},
		{name: "remote", target: "remote-gate-test"},
		{name: "missing", target: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsLocalRunnerWorkload(Workload{RunnerTarget: test.target}); got != test.want {
				t.Fatalf("IsLocalRunnerWorkload(%q) = %t, want %t", test.target, got, test.want)
			}
		})
	}
}

func TestLocalCompletionFieldsCannotCreateRemoteAuthority(t *testing.T) {
	document, _, root := validFixture(t)
	receipt := canonicalReceipt(document)
	receipt.RemoteRunID = "local-run"
	receipt.RemoteJobID = "local-job"
	receipt.RemoteArtifactName = "local-artifact"
	receipt.RemoteArtifactDigest = "sha256:" + strings.Repeat("9", 64)
	path := writeReceiptFixture(t, receipt)
	if err := ValidateReceiptAt(document, root, "quick", path); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceiptAt() error = %v, want untrusted local authority rejection", err)
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

func TestRemoteTestSelectorsAtReadsResolvedTreeNotWorkingTree(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "cmd", "mcp-lsp", "fake")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	treeSource := "package fake\n\nimport \"testing\"\nfunc TestTree(t *testing.T) {}\n"
	path := filepath.Join(packageRoot, "fake_test.go")
	if err := os.WriteFile(path, []byte(treeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	runCatalogGit(t, root, "init", "--quiet")
	runCatalogGit(t, root, "config", "user.name", "catalog-test")
	runCatalogGit(t, root, "config", "user.email", "catalog-test@example.invalid")
	runCatalogGit(t, root, "add", ".")
	runCatalogGit(t, root, "commit", "--quiet", "-m", "tree selector")
	_, tree, err := currentGitIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package fake\n\nimport \"testing\"\nfunc TestWorktree(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selectors, err := RemoteTestSelectorsAt(root, tree, []string{"go", "test", "./cmd/mcp-lsp/fake", "-run", "^TestTree$"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(selectors, []string{"./cmd/mcp-lsp/fake#TestTree"}) {
		t.Fatalf("RemoteTestSelectorsAt() = %v, want tree-bound selector", selectors)
	}
}

func TestValidateReceiptAtCandidateRejectsUnboundLocalAuthority(t *testing.T) {
	document, root, receipt := default15mReceiptFixture(t)
	gitHead, tree, err := currentGitIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	receipt.GitHead = gitHead
	receipt.SourceTreeDigest = tree
	path := writeReceiptFixture(t, receipt)
	if err := ValidateReceiptAtCandidate(document, root, tree, receipt.WorkloadID, path); err == nil || !strings.Contains(err.Error(), "remote run/job/artifact authority") {
		t.Fatalf("ValidateReceiptAtCandidate() error = %v, want remote authority N/V", err)
	}
}

func default15mReceiptFixture(t *testing.T) (Catalog, string, Receipt) {
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
	return document, root, receipt
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
