package remoteci

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteWorkerSemanticEnvironmentIsCanonicalAndOneHot(t *testing.T) {
	assignments := cicontract.CanonicalWorkerExecutionEnvironment()
	values, err := remoteWorkerSemanticEnvironmentValues(assignments)
	if err != nil {
		t.Fatalf("remoteWorkerSemanticEnvironmentValues() error = %v", err)
	}
	for key, want := range map[string]string{
		"CGO_ENABLED": "1",
		"GOOS":        "linux",
		"GOARCH":      "amd64",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
	} {
		if values[key] != want {
			t.Fatalf("canonical semantic env %s=%q, want %q", key, values[key], want)
		}
	}
	for _, assignment := range assignments {
		key, _, _ := strings.Cut(assignment, "=")
		forbidden := []string{"GOCACHE", "GOMODCACHE", "GOTMPDIR", "TMPDIR", "HOME", "XDG_CACHE_HOME", "PLAYWRIGHT_BROWSERS_PATH", ".CACHE/", "JOB", "AGENT", "TOKEN"}
		for _, fragment := range forbidden {
			if strings.Contains(strings.ToUpper(key), fragment) {
				t.Fatalf("semantic env contains non-correctness identity key %q", key)
			}
		}
	}
}

func TestRemoteWorkloadEnvironmentDigestSeparatesNormalAndRaceGoFlags(t *testing.T) {
	input := RunInput{Platform: "linux/amd64", PolicyDigest: "sha256:policy", ToolchainDigest: "sha256:toolchain", RuntimeSeedSHA256: "sha256:seed", WorkerExecutionSemanticDigest: "sha256:" + strings.Repeat("f", 64)}
	policy := testRemoteResourcePolicy()
	normal, err := remoteWorkloadEnvironmentDigestForGoFlags(input, 10*time.Minute, policy, gate.CanonicalGoFlags(false))
	if err != nil {
		t.Fatal(err)
	}
	race, err := remoteWorkloadEnvironmentDigestForGoFlags(input, 10*time.Minute, policy, gate.CanonicalGoFlags(true))
	if err != nil {
		t.Fatal(err)
	}
	if normal == race {
		t.Fatalf("normal and race PASS environment digests are equal: %q", normal)
	}
	if _, err := remoteWorkloadEnvironmentDigestForGoFlags(input, 10*time.Minute, policy, "-race -race -p=4"); err == nil {
		t.Fatal("duplicate race GoFlags were accepted into PASS identity")
	}
}

func TestRemoteWorkloadEnvironmentDigestRequiresCanonicalWorkerDigest(t *testing.T) {
	base := RunInput{Platform: "linux/amd64", PolicyDigest: "sha256:policy", ToolchainDigest: "sha256:toolchain", RuntimeSeedSHA256: "sha256:seed"}
	policy := testRemoteResourcePolicy()
	if _, err := remoteWorkloadEnvironmentDigestForGoFlags(base, 10*time.Minute, policy, gate.CanonicalGoFlags(false)); err == nil {
		t.Fatal("missing worker execution semantic digest was accepted")
	}
	base.WorkerExecutionSemanticDigest = "sha256:" + strings.Repeat("A", 64)
	if _, err := remoteWorkloadEnvironmentDigestForGoFlags(base, 10*time.Minute, policy, gate.CanonicalGoFlags(false)); err == nil {
		t.Fatal("non-canonical worker execution semantic digest was accepted")
	}
}

func TestRemoteWorkloadPassIdentityCrossMergeEquivalentTree(t *testing.T) {
	digest := func(fill byte) string { return "sha256:" + strings.Repeat(string(fill), 64) }
	base := RunInput{
		Platform: "linux/amd64", PolicyDigest: digest('a'), ToolchainDigest: digest('b'), RuntimeSeedSHA256: digest('c'),
		WorkerExecutionSemanticDigest: digest('d'), Tree: "tree-equivalent", Commit: "commit-branch-a", Base: "base-a",
		RunnerBaseCommit: "runner-commit-a", RunnerBaseTree: "runner-tree", RemoteName: "job-a", RemoteURL: "branch-a",
		RunnerIdentityDigest: digest('e'), ExecutionRunnerImage: "runner-a", ExecutionImageCacheSnapshotID: "snapshot-a",
	}
	workload := gate.Workload{ID: "backend:cross-merge", CommandDigest: strings.Repeat("f", 64), InputDigest: digest('1'), Shardable: true}
	identityFor := func(input RunInput, candidate gate.Workload) gate.WorkloadPassIdentity {
		environment, err := remoteWorkloadEnvironmentDigestForGoFlags(input, 10*time.Minute, testRemoteResourcePolicy(), gate.CanonicalGoFlags(false))
		if err != nil {
			t.Fatalf("remoteWorkloadEnvironmentDigestForGoFlags(): %v", err)
		}
		identity, err := remoteWorkloadPassIdentity(candidate, nil, environment)
		if err != nil {
			t.Fatalf("remoteWorkloadPassIdentity(): %v", err)
		}
		return identity
	}
	baseIdentity := identityFor(base, workload)
	mergeEquivalent := base
	mergeEquivalent.Tree = "tree-other"
	mergeEquivalent.Commit, mergeEquivalent.Base = "commit-branch-b", "base-b"
	mergeEquivalent.RemoteName, mergeEquivalent.RemoteURL = "job-b", "branch-b"
	mergeEquivalent.RunnerBaseCommit, mergeEquivalent.RunnerIdentityDigest = "runner-commit-b", digest('6')
	mergeEquivalent.ExecutionRunnerImage, mergeEquivalent.ExecutionImageCacheSnapshotID = "runner-b", "snapshot-b"
	mergeEquivalent.AgentTokenDigest = digest('5')
	if got := identityFor(mergeEquivalent, workload); got != baseIdentity {
		t.Fatalf("cross-merge equivalent tree changed PASS identity: got=%#v want=%#v", got, baseIdentity)
	}
	changedSource := workload
	changedSource.InputDigest = digest('7')
	if got := identityFor(base, changedSource); got == baseIdentity {
		t.Fatal("source observation/input change preserved PASS identity")
	}
	changedWorker := base
	changedWorker.WorkerExecutionSemanticDigest = digest('8')
	if got := identityFor(changedWorker, workload); got == baseIdentity {
		t.Fatal("worker execution closure change preserved PASS identity")
	}
}

func TestRemoteWorkerSemanticEnvironmentRejectsMalformedOrDuplicate(t *testing.T) {
	for name, assignments := range map[string][]string{
		"missing value": {"GOOS="},
		"duplicate":     {"GOOS=linux", "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"},
		"missing key":   {"linux"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := remoteWorkerSemanticEnvironmentValues(assignments); err == nil {
				t.Fatalf("remoteWorkerSemanticEnvironmentValues(%v) unexpectedly succeeded", assignments)
			}
		})
	}
}

func TestRemoteWorkloadEnvironmentDigestBindsCanonicalMaterialAndWorkerProvenance(t *testing.T) {
	environment := cicontract.CanonicalWorkerExecutionEnvironment()
	base := remoteWorkloadEnvironment{
		SchemaVersion:                 cicontract.WorkloadPassEnvironmentSchemaVersion,
		Platform:                      "linux/amd64",
		PolicyDigest:                  "sha256:policy",
		ToolchainDigest:               "sha256:toolchain",
		RuntimeSeedSHA256:             "sha256:seed",
		CGOEnabled:                    "1",
		GOOS:                          "linux",
		GOARCH:                        "amd64",
		GoFlags:                       "-p=4",
		SemanticEnvironmentSchema:     cicontract.WorkerExecutionEnvironmentSchemaVersion,
		SemanticEnvironment:           environment,
		WorkerExecutionProvenance:     cicontract.WorkerExecutionProvenanceID,
		WorkerExecutionSemanticDigest: "sha256:" + strings.Repeat("a", 64),
	}
	marshal := func(material remoteWorkloadEnvironment) string {
		encoded, err := json.Marshal(material)
		if err != nil {
			t.Fatalf("marshal environment material: %v", err)
		}
		return remoteWorkloadEnvironmentSHA256(encoded)
	}
	baseDigest := marshal(base)
	mutations := map[string]func(*remoteWorkloadEnvironment){
		"cgo":    func(value *remoteWorkloadEnvironment) { value.CGOEnabled = "0" },
		"goos":   func(value *remoteWorkloadEnvironment) { value.GOOS = "darwin" },
		"goarch": func(value *remoteWorkloadEnvironment) { value.GOARCH = "arm64" },
		"env member": func(value *remoteWorkloadEnvironment) {
			value.SemanticEnvironment = append(append([]string(nil), value.SemanticEnvironment...), "LANG=C")
		},
		"provenance": func(value *remoteWorkloadEnvironment) { value.WorkerExecutionProvenance = "other-provenance/v1" },
		"worker digest": func(value *remoteWorkloadEnvironment) {
			value.WorkerExecutionSemanticDigest = "sha256:" + strings.Repeat("b", 64)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if got := marshal(changed); got == baseDigest {
				t.Fatalf("environment mutation did not change digest: %q", got)
			}
		})
	}
}

// TestRemoteWorkloadEnvironmentDigestExcludesSchedulingInputs 锁定资源策略与 worker 超时不污染 PASS 环境身份。
func TestRemoteWorkloadEnvironmentDigestExcludesSchedulingInputs(t *testing.T) {
	input := RunInput{
		Platform:                      "linux/amd64",
		PolicyDigest:                  "sha256:" + strings.Repeat("a", 64),
		ToolchainDigest:               "sha256:" + strings.Repeat("b", 64),
		RuntimeSeedSHA256:             "sha256:" + strings.Repeat("c", 64),
		WorkerExecutionSemanticDigest: "sha256:" + strings.Repeat("d", 64),
	}
	basePolicy := testRemoteResourcePolicy()
	base, err := remoteWorkloadEnvironmentDigestForGoFlags(input, 10*time.Minute, basePolicy, gate.CanonicalGoFlags(false))
	if err != nil {
		t.Fatalf("base environment digest: %v", err)
	}
	changedPolicy := basePolicy
	changedPolicy.HeadroomPercent = 50
	changedPolicy.MinSamplesToDownsize = 10
	changed, err := remoteWorkloadEnvironmentDigestForGoFlags(input, 30*time.Minute, changedPolicy, gate.CanonicalGoFlags(false))
	if err != nil {
		t.Fatalf("changed scheduling inputs environment digest: %v", err)
	}
	if changed != base {
		t.Fatalf("scheduling inputs changed PASS environment digest: got %q want %q", changed, base)
	}
}
