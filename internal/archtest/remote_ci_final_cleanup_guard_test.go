package archtest

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIFinalCleanupGuard keeps the final remote-CI deletion boundary
// narrow and executable.  The older contract guard owns broad concurrency,
// source-transport, and workload-reuse checks; this guard only covers names
// and repository artifacts that were explicitly retired during final cleanup.
func TestRemoteCIFinalCleanupGuard(t *testing.T) {
	root := findRepoRoot(t)
	assertRemoteCITopLevelRetiredArtifactsAbsent(t, root)
	for _, path := range remoteCIProductionFiles(t, root) {
		parsed := parseRemoteCIContractGuardFile(t, path)
		for _, violation := range remoteCIFinalCleanupViolations(parsed) {
			t.Errorf("%s retains final-cleanup remote CI compatibility marker %s", relativeRemoteCIContractPath(t, root, path), violation)
		}
	}
}

// assertRemoteCITopLevelRetiredArtifactsAbsent rejects the historical
// repository-root plan entrypoint and the retired ci_commit_guard script at
// either its old root location or scripts/ location.
func assertRemoteCITopLevelRetiredArtifactsAbsent(t *testing.T, root string) {
	t.Helper()
	present, err := remoteCITopLevelRetiredArtifacts(root)
	if err != nil {
		t.Fatalf("scan top-level retired remote-CI artifacts: %v", err)
	}
	for _, relative := range present {
		t.Errorf("retired top-level remote-CI artifact was reintroduced: %s", relative)
	}
}

func remoteCITopLevelRetiredArtifacts(root string) ([]string, error) {
	var present []string
	for _, relative := range []string{"plan", "ci_commit_guard", "ci_commit_guard.sh", "scripts/ci_commit_guard.sh"} {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if err == nil {
			present = append(present, relative)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat %s: %w", relative, err)
		}
	}
	return present, nil
}

// remoteCIFinalCleanupViolations deliberately does not call the broad legacy
// helpers.  Each marker below belongs to a deletion boundary that is otherwise
// easy to reintroduce under a renamed compatibility wrapper.
func remoteCIFinalCleanupViolations(file *ast.File) []string {
	violations := map[string]bool{}
	for identifier := range remoteCIForbiddenIdentifiers(file) {
		if remoteCIFinalCleanupIdentifier(identifier) {
			violations["identifier "+identifier] = true
		}
	}
	for _, literal := range remoteCIStringLiterals(file) {
		if remoteCIFinalCleanupLiteral(literal) {
			violations["literal "+literal] = true
		}
	}
	return remoteCIViolationList(violations)
}

func remoteCIFinalCleanupIdentifier(identifier string) bool {
	normalized := strings.ToLower(identifier)
	for _, marker := range []string{
		"retiredcontainerexecutionowner",
		"retiredcontainergatebinary",
		"retiredcommandidentityprefix",
		"matchesretiredgateexecution",
		"workloadpassevidencealias",
		"workloadpassevidencealiases",
		"durationledgerrawobservation",
		"genericprovider",
		"providerexecutor",
		"executorprovider",
		"genericexecutor",
		"genericremoteexecutor",
		"remoteprovider",
		"remoteproviderexecutor",
		"remoteexecutorprovider",
		"remoteciexecutor",
		"providerbackend",
		"remoteexecutionbackend",
		"dockerexecutor",
		"kubernetesexecutor",
		"githubactionsexecutor",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func remoteCIFinalCleanupLiteral(literal string) bool {
	normalized := strings.ToLower(literal)
	for _, marker := range []string{
		"container-executor",
		"/usr/local/bin/super-dolphin-gate-executor",
		"ci_workload_pass_evidence_aliases",
		"idx_ci_runs_reusable_pass",
		"idx_ci_workload_pass_evidence_migration",
		"idx_ci_workload_pass_evidence_alias_source",
		"duration_ledger_raw_events",
		"generic-provider",
		"provider-executor",
		"executor-provider",
		"remote-provider",
		"remote-ci-provider",
		"remote-executor-provider",
		"generic-remote-executor",
		"docker-executor",
		"kubernetes-executor",
		"github-actions-executor",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func TestRemoteCIFinalCleanupGuardCounterexamples(t *testing.T) {
	safe := remoteCIParseGuardFixture(t, `package fixture
const currentOwner = "container-worker"
const currentIndex = "idx_ci_runs_accepted_generation"
type ExecutorProgram struct{}
func run() { _ = currentOwner; _ = currentIndex; _ = ExecutorProgram{} }
`)
	if got := remoteCIFinalCleanupViolations(safe); len(got) != 0 {
		t.Fatalf("current ECI worker/index fixture has false final-cleanup violations: %v", got)
	}

	legacy := remoteCIParseGuardFixture(t, `package fixture
const retiredOwner = "container-executor"
const retiredBinary = "/usr/local/bin/super-dolphin-gate-executor"
const aliases = "ci_workload_pass_evidence_aliases"
const oldRunIndex = "idx_ci_runs_reusable_pass"
const oldEvidenceIndex = "idx_ci_workload_pass_evidence_migration"
const oldAliasIndex = "idx_ci_workload_pass_evidence_alias_source"
type GenericProviderExecutor struct{}
type RemoteExecutorProvider struct{}
type RemoteCIExecutor struct{}
func matchesRetiredGateExecution() {}
func run() {
	_ = retiredOwner; _ = retiredBinary; _ = aliases
	_ = oldRunIndex; _ = oldEvidenceIndex; _ = oldAliasIndex
	_ = GenericProviderExecutor{}; _ = RemoteExecutorProvider{}; _ = RemoteCIExecutor{}
}
`)
	violations := strings.Join(remoteCIFinalCleanupViolations(legacy), "\n")
	for _, required := range []string{
		"literal container-executor",
		"literal ci_workload_pass_evidence_aliases",
		"literal idx_ci_runs_reusable_pass",
		"literal idx_ci_workload_pass_evidence_migration",
		"literal idx_ci_workload_pass_evidence_alias_source",
		"identifier GenericProviderExecutor",
		"identifier RemoteExecutorProvider",
		"identifier RemoteCIExecutor",
		"identifier matchesRetiredGateExecution",
	} {
		if !strings.Contains(violations, required) {
			t.Fatalf("final-cleanup fixture violations = %q, missing %q", violations, required)
		}
	}
}

func TestRemoteCITopLevelRetiredArtifactsGuardCounterexample(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "scripts", "ci_commit_guard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// The helper reports both reintroduced artifacts through the test failure
	// path; exercise its predicate directly so the repository's clean root stays
	// green while this counterexample remains deterministic.
	got, err := remoteCITopLevelRetiredArtifacts(root)
	if err != nil {
		t.Fatalf("scan counterexample artifacts: %v", err)
	}
	for _, relative := range []string{"plan", "scripts/ci_commit_guard.sh"} {
		found := false
		for _, candidate := range got {
			if candidate == relative {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("counterexample artifact %s was not reported: %v", relative, got)
		}
	}
}
