package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	projectmaptrusted "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRequestedTestTargetDefaultsAutoAndRejectsUnknownValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
		err  string
	}{
		{name: "default", want: "auto"},
		{name: "explicit local", args: []string{"--target", "local"}, want: "local"},
		{name: "explicit remote equals", args: []string{"--target=remote"}, want: "remote"},
		{name: "missing value", args: []string{"--target"}, err: "requires a value"},
		{name: "unknown", args: []string{"--target", "bogus"}, err: "invalid workload target"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := requestedTestTarget(test.args)
			if test.err == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got != test.want {
					t.Fatalf("target = %q, want %q", got, test.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("error = %v, want substring %q", err, test.err)
			}
		})
	}
}

func TestRunLocalTestInvocationDoesNotHandshakeBeforeCanonicalProducer(t *testing.T) {
	handshakeCalled := false
	wantErr := errors.New("canonical producer unavailable")
	adapter := localTestCLIAdapter{
		Prepare: func(context.Context, remoteRunOptions) (localTestCLIPlan, error) {
			return localTestCLIPlan{}, wantErr
		},
		RequireRemoteToken: func([]string, []string, io.Writer) (string, error) {
			handshakeCalled = true
			return "", nil
		},
	}
	err := runLocalTestInvocation(context.Background(), nil, remoteRunOptions{Target: "auto"}, io.Discard, adapter)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if handshakeCalled {
		t.Fatal("remote token handshake ran before canonical local producer")
	}
}

func TestEmitLocalTestAuthorityOutputReportsHitsAndExecutedEvidence(t *testing.T) {
	tests := []struct {
		name     string
		prepared gatecontract.LocalWorkloadScheduleResult
		outcome  string
		wantIDs  []string
	}{
		{
			name: "all local hit",
			prepared: gatecontract.LocalWorkloadScheduleResult{
				Hits:  []gatecontract.WorkloadPassEvidence{{Identity: gatecontract.WorkloadPassIdentity{WorkloadID: "local-hit"}}},
				Stats: gatecontract.LocalWorkloadScheduleStats{SelectedLocal: 1, LocalHits: 1},
			},
			outcome: "LOCAL_HIT",
			wantIDs: []string{"local-hit"},
		},
		{
			name: "green local miss",
			prepared: gatecontract.LocalWorkloadScheduleResult{
				Evidence: []gatecontract.WorkloadPassEvidence{{Identity: gatecontract.WorkloadPassIdentity{WorkloadID: "local-executed"}}},
				Stats:    gatecontract.LocalWorkloadScheduleStats{SelectedLocal: 1, LocalMisses: 1, LocalExecuted: 1},
			},
			outcome: "LOCAL_EXECUTED",
			wantIDs: []string{"local-executed"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := emitLocalTestAuthorityOutput(&stdout, "auto", test.prepared, nil); err != nil {
				t.Fatal(err)
			}
			output := decodeLocalTestAuthorityOutput(t, stdout.Bytes())
			if output.Authority != "LOCAL_NON_AUTHORITATIVE" || output.Target != "auto" || output.Status != "PASS" || output.LocalOutcome != test.outcome {
				t.Fatalf("local authority output = %#v", output)
			}
			if !slices.Equal(output.LocalEvidenceIDs, test.wantIDs) || output.Stats != test.prepared.Stats {
				t.Fatalf("local evidence/stats = %#v, want ids=%#v stats=%#v", output, test.wantIDs, test.prepared.Stats)
			}
		})
	}
}

func TestEmitLocalTestInvocationOutcomeKeepsRemoteResultSeparate(t *testing.T) {
	prepared := gatecontract.LocalWorkloadScheduleResult{
		Evidence: []gatecontract.WorkloadPassEvidence{{Identity: gatecontract.WorkloadPassIdentity{WorkloadID: "local-only-evidence"}}},
		Remote:   []gatecontract.GateID{"remote-workload"},
		Stats:    gatecontract.LocalWorkloadScheduleStats{SelectedLocal: 1, LocalExecuted: 1, SelectedRemote: 1, RemoteInvocations: 1},
	}
	remote := localRemoteSubsetOutcome{Called: true, Result: remoteci.RunResult{SchemaVersion: remoteci.RunResultSchemaVersion, JobID: "remote-subset-job"}}
	var stdout bytes.Buffer
	if err := emitLocalTestInvocationOutcome(&stdout, "hybrid", prepared, remote, nil); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&stdout)
	var local localTestAuthorityOutput
	if err := decoder.Decode(&local); err != nil {
		t.Fatal(err)
	}
	var result remoteci.RunResult
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if local.Authority != "LOCAL_NON_AUTHORITATIVE" || !slices.Equal(local.LocalEvidenceIDs, []string{"local-only-evidence"}) {
		t.Fatalf("local output = %#v", local)
	}
	if result.JobID != "remote-subset-job" {
		t.Fatalf("remote result = %#v", result)
	}
	encodedRemote, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRemote), "local-only-evidence") {
		t.Fatalf("remote receipt retained local evidence: %s", encodedRemote)
	}
}

type frozenLocalRemoteFailureCase struct {
	name             string
	remote           localRemoteSubsetOutcome
	remoteErr        error
	wantRemoteResult bool
}

func TestEmitLocalTestInvocationOutcomeKeepsFrozenLocalPassWhenRemoteStageFails(t *testing.T) {
	prepared := gatecontract.LocalWorkloadScheduleResult{
		Evidence: []gatecontract.WorkloadPassEvidence{{Identity: gatecontract.WorkloadPassIdentity{WorkloadID: "local-only-evidence"}}},
		Remote:   []gatecontract.GateID{"remote-workload"},
		Stats:    gatecontract.LocalWorkloadScheduleStats{SelectedLocal: 1, LocalExecuted: 1, SelectedRemote: 1, RemoteInvocations: 1},
	}
	tests := []frozenLocalRemoteFailureCase{
		{
			name:             "remote callback failure",
			remote:           localRemoteSubsetOutcome{Called: true, Result: remoteci.RunResult{SchemaVersion: remoteci.RunResultSchemaVersion, JobID: "remote-failed-job"}},
			remoteErr:        errors.New("remote callback failed"),
			wantRemoteResult: true,
		},
		{
			name:      "remote token prerequisite failure",
			remoteErr: errors.New("remote token is required"),
		},
		{
			name:      "remote config prerequisite failure",
			remoteErr: errors.New("remote subset requires --config"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertFrozenLocalPassWithRemoteFailure(t, prepared, test)
		})
	}
}

func assertFrozenLocalPassWithRemoteFailure(t *testing.T, prepared gatecontract.LocalWorkloadScheduleResult, test frozenLocalRemoteFailureCase) {
	t.Helper()
	var stdout bytes.Buffer
	err := emitLocalTestInvocationOutcome(&stdout, "hybrid", prepared, test.remote, test.remoteErr)
	if err == nil || !strings.Contains(err.Error(), test.remoteErr.Error()) {
		t.Fatalf("command error = %v, want remote error %v", err, test.remoteErr)
	}
	decoder := json.NewDecoder(&stdout)
	var local localTestAuthorityOutput
	if err := decoder.Decode(&local); err != nil {
		t.Fatal(err)
	}
	if local.Authority != "LOCAL_NON_AUTHORITATIVE" || local.Status != "PASS" || local.Error != "" {
		t.Fatalf("frozen local output = %#v", local)
	}
	assertRemoteFailureOutput(t, decoder, test)
}

func assertRemoteFailureOutput(t *testing.T, decoder *json.Decoder, test frozenLocalRemoteFailureCase) {
	t.Helper()
	if !test.wantRemoteResult {
		assertNoRemoteResult(t, decoder)
		return
	}
	var remoteResult remoteci.RunResult
	if err := decoder.Decode(&remoteResult); err != nil {
		t.Fatal(err)
	}
	if remoteResult.JobID != test.remote.Result.JobID {
		t.Fatalf("remote result = %#v, want job %q", remoteResult, test.remote.Result.JobID)
	}
}

func assertNoRemoteResult(t *testing.T, decoder *json.Decoder) {
	t.Helper()
	if err := decoder.Decode(&remoteci.RunResult{}); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected remote result error = %v", err)
	}
}

func TestRunLocalRemoteSubsetPreservesRemoteEmitterMaterial(t *testing.T) {
	prepared := gatecontract.LocalWorkloadScheduleResult{Remote: []gatecontract.GateID{"remote-workload"}}
	called := false
	adapter := localTestCLIAdapter{
		RequireRemoteToken: func([]string, []string, io.Writer) (string, error) { return "sha256:remote-token", nil },
		ExecuteRemoteSubset: func(_ context.Context, _ remoteRunOptions, ids []gatecontract.GateID, digest string) (localRemoteSubsetOutcome, error) {
			called = true
			if !slices.Equal(ids, []gatecontract.GateID{"remote-workload"}) || digest != "sha256:remote-token" {
				t.Fatalf("remote subset invocation = ids=%#v digest=%q", ids, digest)
			}
			return localRemoteSubsetOutcome{Input: remoteci.RunInput{}, Result: remoteci.RunResult{SchemaVersion: remoteci.RunResultSchemaVersion, JobID: "preserved-remote-job"}}, nil
		},
	}
	outcome, err := runLocalRemoteSubset(context.Background(), nil, remoteRunOptions{ConfigPath: "remote-ci.json"}, io.Discard, adapter, &gatecontract.LocalWorkloadSchedulerInput{}, &prepared)
	if err != nil {
		t.Fatal(err)
	}
	if !called || !outcome.Called || outcome.Result.JobID != "preserved-remote-job" || prepared.Stats.RemoteInvocations != 1 {
		t.Fatalf("remote subset outcome = %#v stats=%#v called=%v", outcome, prepared.Stats, called)
	}
}

func TestEmitLocalTestAuthorityOutputFailureIsNotReportedAsPass(t *testing.T) {
	var stdout bytes.Buffer
	runErr := errors.New("local execution failed")
	prepared := gatecontract.LocalWorkloadScheduleResult{Stats: gatecontract.LocalWorkloadScheduleStats{SelectedLocal: 1, LocalHits: 1}}
	err := emitFrozenLocalTestInvocationOutcome(&stdout, "local", prepared, localTestInvocationOutcome{LocalErr: runErr})
	if !errors.Is(err, runErr) {
		t.Fatalf("command error = %v, want local error %v", err, runErr)
	}
	output := decodeLocalTestAuthorityOutput(t, stdout.Bytes())
	if output.Status != "FAILED" || !strings.Contains(output.Error, runErr.Error()) {
		t.Fatalf("failed local output = %#v", output)
	}
}

func decodeLocalTestAuthorityOutput(t *testing.T, data []byte) localTestAuthorityOutput {
	t.Helper()
	var output localTestAuthorityOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

func TestParseAutoTestRunOptionsLocalAcceptsRepeatedWorkloadsAndDefaultsAuto(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "local.sqlite")
	manifest := filepath.Join(t.TempDir(), "workloads.json")
	if err := os.WriteFile(manifest, []byte(`["catalog-id-2"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	options, err := parseAutoTestRunOptions([]string{
		"--ledger", ledger,
		"--gate-workload", "catalog-id-1",
		"--gate-workload-manifest", manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Target != "auto" || !slices.Equal(options.GateWorkloadIDs, []string{"catalog-id-1", "catalog-id-2"}) {
		t.Fatalf("local selection = target=%q workloads=%#v", options.Target, options.GateWorkloadIDs)
	}
}

func TestParseAutoTestRunOptionsDefersRawAgentTokenUntilRemoteSubset(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "local.sqlite")
	options, err := parseAutoTestRunOptions([]string{
		"--ledger", ledger,
		"--gate-workload", "catalog-id-1",
		"--agent-token=raw-token-must-not-be-digested",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.AgentTokenDigest != "" {
		t.Fatalf("non-remote parser bound token digest %q", options.AgentTokenDigest)
	}
}

func TestParseAutoTestRunOptionsExplicitRemoteGateWorkloadBindsToken(t *testing.T) {
	_, err := parseAutoTestRunOptions([]string{
		"--target=remote",
		"--config", filepath.Join(t.TempDir(), "remote-ci.json"),
		"--gate-workload", "catalog-id-1",
		"--agent-token", "raw-token-must-not-be-digested",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid prefix") {
		t.Fatalf("explicit remote token error = %v, want strict token validation", err)
	}
}

func TestParseAutoTestRunOptionsNonRemoteRejectsLegacyTestSelector(t *testing.T) {
	_, err := parseAutoTestRunOptions([]string{
		"--ledger", filepath.Join(t.TempDir(), "local.sqlite"),
		"--test", "./cmd/super-dolphin-gate",
	})
	if err == nil || !strings.Contains(err.Error(), "requires --gate-workload") {
		t.Fatalf("error = %v, want non-remote gate selector rejection", err)
	}
}

func TestLocalWorkloadSelectionRejectsDuplicateAndUnknownIDs(t *testing.T) {
	document := gatecontract.WorkloadCatalog{Workloads: []gatecontract.Workload{{ID: "known", Shardable: true}}}
	if err := validateLocalWorkloadSelection([]string{"known", "known"}, document); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate selection error = %v", err)
	}
	if err := validateLocalWorkloadSelection([]string{"unknown"}, document); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown selection error = %v", err)
	}
}

func TestLocalExactTreeMaterializerIgnoresDirtyWorktree(t *testing.T) {
	root := t.TempDir()
	runMcpLSPTestGit(t, root, "init", "--quiet")
	runMcpLSPTestGit(t, root, "config", "user.name", "gate-test")
	runMcpLSPTestGit(t, root, "config", "user.email", "gate-test@example.invalid")
	path := filepath.Join(root, "candidate.txt")
	if err := os.WriteFile(path, []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("tracked-ignored.txt\nuntracked-ignored.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runMcpLSPTestGit(t, root, "add", "candidate.txt")
	runMcpLSPTestGit(t, root, "add", ".gitignore")
	runMcpLSPTestGit(t, root, "commit", "--quiet", "-m", "候选")
	if err := os.WriteFile(filepath.Join(root, "tracked-ignored.txt"), []byte("tracked ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runMcpLSPTestGit(t, root, "add", "--force", "tracked-ignored.txt")
	runMcpLSPTestGit(t, root, "commit", "--quiet", "-m", "受管忽略文件")
	tree := mcpLSPTestGitOutput(t, root, "rev-parse", "HEAD^{tree}")
	if err := os.WriteFile(path, []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked-ignored.txt"), []byte("dirty tracked ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked-ignored.txt"), []byte("dirty untracked ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trustedGit, err := gatecontract.ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Fatalf("resolve trusted Git: %v", err)
	}
	materialized, err := materializeLocalExactTree(root, tree, trustedGit)
	if err != nil {
		t.Fatal(err)
	}
	requireLocalExactTreeSnapshot(t, materialized, tree)
	requireLocalExactTreeCleanup(t, materialized)
}

func requireLocalExactTreeSnapshot(t *testing.T, materialized projectmaptrusted.ExactTree, tree string) {
	t.Helper()
	if materialized.SourceTreeSHA != tree {
		t.Fatalf("materialized tree = %q, want %q", materialized.SourceTreeSHA, tree)
	}
	if _, err := os.Stat(filepath.Join(materialized.SourceRoot, ".git")); err != nil {
		t.Fatalf("materialized Git metadata missing: %v", err)
	}
	if got := mcpLSPTestGitOutput(t, materialized.SourceRoot, "rev-parse", "HEAD^{tree}"); got != tree {
		t.Fatalf("materialized HEAD tree = %q, want %q", got, tree)
	}
	content, err := os.ReadFile(filepath.Join(materialized.SourceRoot, "candidate.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "candidate\n" {
		t.Fatalf("materialized content = %q, want candidate tree", content)
	}
	trackedIgnored, err := os.ReadFile(filepath.Join(materialized.SourceRoot, "tracked-ignored.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(trackedIgnored) != "tracked ignored\n" {
		t.Fatalf("materialized tracked ignored content = %q, want committed tree", trackedIgnored)
	}
	if _, err := os.Stat(filepath.Join(materialized.SourceRoot, "untracked-ignored.txt")); !os.IsNotExist(err) {
		t.Fatalf("materialized untracked ignored file exists: %v", err)
	}
}

func requireLocalExactTreeCleanup(t *testing.T, materialized projectmaptrusted.ExactTree) {
	t.Helper()
	if err := materialized.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(materialized.SourceRoot); !os.IsNotExist(err) {
		t.Fatalf("materialized root remains after cleanup: %v", err)
	}
}

// TestProductionLocalPlanCleanAuthorityDoesNotRequireRemoteBaseline proves
// the local PASS lookup can start from a clean authority without loading a
// remote accepted baseline, runner identity, or ImageCache state.
func TestProductionLocalPlanCleanAuthorityDoesNotRequireRemoteBaseline(t *testing.T) {
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tree := mcpLSPTestGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	options := remoteRunOptions{
		RepositoryRoot: repository,
		Commit:         "HEAD",
		Scenario:       "commit",
		Target:         "local",
		LedgerPath:     filepath.Join(t.TempDir(), "clean-local.sqlite"),
		GateWorkloadIDs: []string{
			string(gatecontract.GateIDCodemapCheck),
		},
	}
	plan, err := prepareProductionLocalTestCLIPlan(context.Background(), options)
	if err != nil {
		t.Fatalf("prepare production local plan with clean authority: %v", err)
	}
	if plan.Input.SourceTreeSHA != tree {
		t.Fatalf("local plan source tree = %q, want exact %q", plan.Input.SourceTreeSHA, tree)
	}
	if _, err := gatecontract.PrepareLocalWorkloadSchedule(context.Background(), plan.Store, plan.Input); err != nil {
		t.Fatalf("prepare local PASS lookup from clean authority: %v", err)
	}
}

// TestProductionLocalPlanReadsExactTreeFromVerifiedPrivateCandidateODB proves a
// production-shaped local PASS lookup can seal a staged candidate whose tree
// and changed blob exist only in its private object database. The shared
// repository object database must not be written as part of this setup.
func TestProductionLocalPlanReadsExactTreeFromVerifiedPrivateCandidateODB(t *testing.T) {
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	privateRoot := t.TempDir()
	privateObjects := filepath.Join(privateRoot, "objects")
	if err := os.MkdirAll(privateObjects, 0o700); err != nil {
		t.Fatal(err)
	}
	privateIndex := filepath.Join(privateRoot, "candidate.index")
	sharedObjects := filepath.Join(mcpLSPTestGitOutput(t, repository, "rev-parse", "--path-format=absolute", "--git-common-dir"), "objects")
	privateEnv := append(os.Environ(),
		"GIT_INDEX_FILE="+privateIndex,
		"GIT_OBJECT_DIRECTORY="+privateObjects,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES="+sharedObjects,
	)
	localPrivateCandidateGit(t, repository, privateEnv, "read-tree", "HEAD")
	blob := localPrivateCandidateGitOutput(t, repository, privateEnv, []byte("private candidate receipt object\n"), "hash-object", "-w", "--stdin")
	localPrivateCandidateGit(t, repository, privateEnv, "update-index", "--add", "--cacheinfo", "100644,"+blob+",README.md")
	tree := localPrivateCandidateGitOutput(t, repository, privateEnv, nil, "write-tree")
	if command := exec.Command("git", "-C", repository, "cat-file", "-e", tree+"^{tree}"); command.Run() == nil {
		t.Fatalf("candidate tree %q unexpectedly exists in shared repository ODB", tree)
	}
	t.Setenv("GIT_INDEX_FILE", privateIndex)
	t.Setenv("GIT_OBJECT_DIRECTORY", privateObjects)
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", sharedObjects)
	options := remoteRunOptions{
		RepositoryRoot: repository,
		Tree:           tree,
		ParentCommit:   "HEAD",
		Scenario:       "commit",
		Target:         "local",
		LedgerPath:     filepath.Join(t.TempDir(), "private-odb-local.sqlite"),
		GateWorkloadIDs: []string{
			string(gatecontract.GateIDCodemapCheck),
		},
	}
	plan, err := prepareProductionLocalTestCLIPlan(context.Background(), options)
	if err != nil {
		t.Fatalf("prepare production local plan from private candidate ODB: %v", err)
	}
	if plan.Input.SourceTreeSHA != tree {
		t.Fatalf("local plan source tree = %q, want private candidate %q", plan.Input.SourceTreeSHA, tree)
	}
	if _, err := gatecontract.PrepareLocalWorkloadSchedule(context.Background(), plan.Store, plan.Input); err != nil {
		t.Fatalf("prepare local PASS lookup from private candidate ODB: %v", err)
	}
}

func localPrivateCandidateGit(t *testing.T, repository string, environment []string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("private candidate git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

func localPrivateCandidateGitOutput(t *testing.T, repository string, environment []string, stdin []byte, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	command.Env = environment
	command.Stdin = bytes.NewReader(stdin)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("private candidate git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output))
}

type localPrivateCandidate struct {
	tree, index, objects, alternates string
}

func newLocalPrivateCandidate(t *testing.T, repository string) localPrivateCandidate {
	t.Helper()
	root := t.TempDir()
	candidate := localPrivateCandidate{index: filepath.Join(root, "candidate.index"), objects: filepath.Join(root, "objects"), alternates: filepath.Join(mcpLSPTestGitOutput(t, repository, "rev-parse", "--path-format=absolute", "--git-common-dir"), "objects")}
	if err := os.MkdirAll(candidate.objects, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "GIT_INDEX_FILE="+candidate.index, "GIT_OBJECT_DIRECTORY="+candidate.objects, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+candidate.alternates)
	localPrivateCandidateGit(t, repository, environment, "read-tree", "HEAD")
	blob := localPrivateCandidateGitOutput(t, repository, environment, []byte("private candidate receipt object\n"), "hash-object", "-w", "--stdin")
	localPrivateCandidateGit(t, repository, environment, "update-index", "--add", "--cacheinfo", "100644,"+blob+",README.md")
	candidate.tree = localPrivateCandidateGitOutput(t, repository, environment, nil, "write-tree")
	return candidate
}

func setLocalPrivateCandidateEnvironment(t *testing.T, candidate localPrivateCandidate) {
	t.Helper()
	t.Setenv("GIT_INDEX_FILE", candidate.index)
	t.Setenv("GIT_OBJECT_DIRECTORY", candidate.objects)
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", candidate.alternates)
}

// TestProductionLocalPlanPrivateODBPathDoesNotChangePASSIdentity proves ODB
// location is a receipt-only proof rather than Local PASS identity material.
func TestProductionLocalPlanPrivateODBPathDoesNotChangePASSIdentity(t *testing.T) {
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	first, second := newLocalPrivateCandidate(t, repository), newLocalPrivateCandidate(t, repository)
	requireSamePrivateCandidateTree(t, first, second)
	firstPlan := prepareLocalPrivateCandidatePlan(t, repository, first)
	secondPlan := prepareLocalPrivateCandidatePlan(t, repository, second)
	requireSamePrivateCandidatePASSIdentity(t, firstPlan, secondPlan)
	requireDistinctPrivateCandidateReceiptDigests(t, firstPlan, secondPlan)
	requireTamperedPrivateCandidateAuthorityFails(t, repository, first)
}

func requireSamePrivateCandidateTree(t *testing.T, first, second localPrivateCandidate) {
	t.Helper()
	if first.tree != second.tree || first.objects == second.objects {
		t.Fatalf("private candidates = tree %q/%q objects %q/%q", first.tree, second.tree, first.objects, second.objects)
	}
}

func prepareLocalPrivateCandidatePlan(t *testing.T, repository string, candidate localPrivateCandidate) localTestCLIPlan {
	t.Helper()
	setLocalPrivateCandidateEnvironment(t, candidate)
	plan, err := prepareProductionLocalTestCLIPlan(context.Background(), localPrivateCandidateOptions(repository, candidate, filepath.Join(t.TempDir(), "local.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func localPrivateCandidateOptions(repository string, candidate localPrivateCandidate, ledger string) remoteRunOptions {
	return remoteRunOptions{RepositoryRoot: repository, Tree: candidate.tree, ParentCommit: "HEAD", Scenario: "commit", Target: "local", LedgerPath: ledger, GateWorkloadIDs: []string{string(gatecontract.GateIDCodemapCheck)}}
}

func requireSamePrivateCandidatePASSIdentity(t *testing.T, first, second localTestCLIPlan) {
	t.Helper()
	if len(first.Input.Items) != 1 || len(second.Input.Items) != 1 {
		t.Fatalf("local plan item counts = %d/%d, want one", len(first.Input.Items), len(second.Input.Items))
	}
	firstItem, secondItem := first.Input.Items[0], second.Input.Items[0]
	if firstItem.LocalIdentity.EnvironmentDigest != secondItem.LocalIdentity.EnvironmentDigest || firstItem.LocalIdentity.ExecutionDigest != secondItem.LocalIdentity.ExecutionDigest || firstItem.LocalKey != secondItem.LocalKey || firstItem.LocalIdentity != secondItem.LocalIdentity {
		t.Fatalf("private ODB path changed local PASS identity: %#v != %#v", firstItem, secondItem)
	}
}

func requireDistinctPrivateCandidateReceiptDigests(t *testing.T, first, second localTestCLIPlan) {
	t.Helper()
	firstDigest, err := first.Input.Receipt.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Input.Receipt.Digest()
	if err != nil || firstDigest == secondDigest {
		t.Fatalf("receipt authority digests = %q/%q, err = %v", firstDigest, secondDigest, err)
	}
}

func requireTamperedPrivateCandidateAuthorityFails(t *testing.T, repository string, candidate localPrivateCandidate) {
	t.Helper()
	if err := os.RemoveAll(candidate.objects); err != nil {
		t.Fatal(err)
	}
	setLocalPrivateCandidateEnvironment(t, candidate)
	if _, err := prepareProductionLocalTestCLIPlan(context.Background(), localPrivateCandidateOptions(repository, candidate, filepath.Join(t.TempDir(), "tampered.sqlite"))); err == nil {
		t.Fatal("tampered private candidate object authority unexpectedly passed")
	}
}

// TestProductionLocalExecutorReceiptSeparatesBoundAndExecutableIDs proves the
// production producer seals mapped-ineligible environment material without
// creating an execution-capable local session.
func TestProductionLocalExecutorReceiptSeparatesBoundAndExecutableIDs(t *testing.T) {
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tree := mcpLSPTestGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	deps, receipt, receiptBoundIDs, executionIDs, err := prepareProductionLocalExecutorReceipt(context.Background(), repository, tree, gatecontract.CandidateObjectAuthority{}, []gatecontract.GateID{gatecontract.GateIDReleaseLayeredCheck})
	if err != nil {
		t.Fatalf("prepareProductionLocalExecutorReceipt() error = %v", err)
	}
	if !slices.Equal(receiptBoundIDs, []gatecontract.GateID{gatecontract.GateIDReleaseLayeredCheck}) || len(executionIDs) != 0 {
		t.Fatalf("production receipt/execution IDs = bound=%#v execution=%#v, want mapped bound only", receiptBoundIDs, executionIDs)
	}
	if deps != (gatecontract.LocalExecutorDependencyInputs{}) || receipt == nil || !gatecontract.LocalExecutorSessionReceiptIncludesWorkload(receipt, gatecontract.GateIDReleaseLayeredCheck) {
		t.Fatalf("production mapped-ineligible receipt = deps=%#v receipt=%T, want sealed real receipt without execution dependencies", deps, receipt)
	}
}
