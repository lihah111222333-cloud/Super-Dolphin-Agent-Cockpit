package remoteci

import (
	"context"
	"strings"
	"testing"
)

func TestWorkerExecutionDigestTracksOnlyControlledExecutionContract(t *testing.T) {
	repository := newFingerprintRepository(t)
	initial := workerExecutionDigest(t, repository)
	for _, change := range workerDigestStableChanges() {
		commitFingerprintChange(t, repository, change.path, change.content)
		if got := workerExecutionDigest(t, repository); got != initial {
			t.Fatalf("%s changed worker execution digest", change.name)
		}
	}

	previous := initial
	for _, change := range workerDigestTrackedChanges() {
		commitFingerprintChange(t, repository, change.path, change.content)
		current := workerExecutionDigest(t, repository)
		if current == previous {
			t.Fatalf("%s did not change worker execution digest", change.name)
		}
		previous = current
	}
}

type workerDigestChange struct {
	name    string
	path    string
	content string
}

func workerDigestStableChanges() []workerDigestChange {
	return []workerDigestChange{
		{"coordinator-only source", "cmd/super-dolphin-gate/coordinator.go", "package main\n\nconst coordinatorOnly = 2\n"},
		{"CI query store source", "internal/devtools/gate/ci_query_store.go", "package gate\n\nconst queryStoreOnly = 2\n"},
		{"SQLite ledger source", "internal/devtools/gate/ledger_store_sqlite.go", "package gate\n\nconst sqliteLedgerOnly = 2\n"},
		{"fingerprint implementation", "internal/devtools/remoteci/workload_fingerprint.go", "package remoteci\n\nconst fingerprintOnly = 2\n"},
		{"worker digest algorithm", "internal/devtools/remoteci/worker_execution_contract.go", "package remoteci\n\nconst contractAlgorithmOnly = 2\n"},
		{"test-only source", "internal/devtools/gate/worker_test.go", "package gate\n\nconst testOnly = 2\n"},
		{"unrelated go.mod requirement", "go.mod", "module example.com/fingerprint\n\ngo 1.25\n\nrequire example.com/unrelated v1.0.0\n"},
		{"unrelated go.sum content", "go.sum", "example.com/unrelated v1.0.0 h1:fixture\n"},
		{"unselected Makefile target", "Makefile", fingerprintWorkerMakefile(1, 2)},
		{"Makefile workload target", "Makefile", fingerprintWorkerMakefile(2, 2)},
	}
}

func workerDigestTrackedChanges() []workerDigestChange {
	return []workerDigestChange{
		{"worker command contract", "internal/devtools/remoteci/coordinator_request.go", fingerprintWorkerRequestSource(2)},
		{"worker executor", "internal/devtools/gate/executor.go", fingerprintWorkerExecutorSource(2)},
		{"new-file worker helper", "internal/devtools/gate/executor_runtime.go", fingerprintWorkerExecutorRuntimeSource(2)},
		{"worker go:embed asset", "internal/devtools/gate/worker_asset.txt", "worker-v2\n"},
		{"worker self-command handler", "cmd/super-dolphin-gate/worker_tool.go", fingerprintWorkerToolSource(2)},
		{"worker report protocol", "internal/devtools/remoteci/protocol.go", fingerprintWorkerProtocolSource(2)},
		{"worker materializer", "cmd/super-dolphin-gate/remote_materialize.go", fingerprintWorkerMaterializerSource(2)},
		{"worker execution script", "scripts/test_with_guard.sh", fingerprintWorkerGuardScript(2)},
		{"transitive worker execution script", "scripts/worker_command.sh", "#!/bin/sh\nexit 2\n"},
		{"worker entry source", "cmd/super-dolphin-gate/main.go", fingerprintWorkerMainSource(2)},
	}
}
func TestWorkerExecutionDigestAcceptsReachableFunctionsWithoutResultsOrBodies(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "cmd/super-dolphin-gate/main.go", `package main

func runWorkerCLI() {
	workerPlatformCall()
}

func workerPlatformCall()
`)

	if digest := workerExecutionDigest(t, repository); digest == "" {
		t.Fatal("worker execution digest is empty")
	}
}

func TestWorkerExecutionDigestHonorsLocalShadowingOfImportNames(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "cmd/super-dolphin-gate/main.go", `package main

import gate "example.com/fingerprint/internal/devtools/gate"

var _ = gate.ExecuteExecutor

type workerHandle struct{}

func (workerHandle) Close() {}

func runWorkerCLI(gate workerHandle) {
	gate.Close()
}

func dispatchWorkerTool(command string) int {
	switch command {
	case "worker-tool":
		return runWorkerTool()
	default:
		return 0
	}
}
`)

	if digest := workerExecutionDigest(t, repository); digest == "" {
		t.Fatal("worker execution digest is empty")
	}
}

func TestWorkerExecutionDigestTracksConstGroupIotaContext(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/executor_runtime.go",
		fingerprintWorkerExecutorIotaSource(false),
	)
	initial := workerExecutionDigest(t, repository)

	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/executor_runtime.go",
		fingerprintWorkerExecutorIotaSource(true),
	)
	if got := workerExecutionDigest(t, repository); got == initial {
		t.Fatal("preceding const spec changed iota semantics without changing worker execution digest")
	}
}

func TestWorkerExecutionDigestTracksSelectedExternalModuleIdentity(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/executor_runtime.go",
		fingerprintWorkerExternalModuleSource(),
	)
	commitFingerprintChange(
		t,
		repository,
		"go.mod",
		fingerprintWorkerModuleFile("v1.0.0", false),
	)
	commitFingerprintChange(
		t,
		repository,
		"go.sum",
		fingerprintWorkerModuleSums("worker-v1", false, false),
	)
	initial := workerExecutionDigest(t, repository)

	commitFingerprintChange(
		t,
		repository,
		"go.mod",
		fingerprintWorkerModuleFile("v1.0.0", true),
	)
	commitFingerprintChange(
		t,
		repository,
		"go.sum",
		fingerprintWorkerModuleSums("worker-v1", true, false),
	)
	if got := workerExecutionDigest(t, repository); got != initial {
		t.Fatal("unselected external module changed worker execution digest")
	}

	commitFingerprintChange(
		t,
		repository,
		"go.sum",
		fingerprintWorkerModuleSums("worker-v1-checksum-change", true, false),
	)
	checksumChanged := workerExecutionDigest(t, repository)
	if checksumChanged == initial {
		t.Fatal("selected external module checksum did not change worker execution digest")
	}

	commitFingerprintChange(
		t,
		repository,
		"go.sum",
		fingerprintWorkerModuleSums("worker-v1-checksum-change", true, true),
	)
	if got := workerExecutionDigest(t, repository); got != checksumChanged {
		t.Fatal("unselected future module checksum changed worker execution digest")
	}

	commitFingerprintChange(
		t,
		repository,
		"go.mod",
		fingerprintWorkerModuleFile("v1.1.0", true),
	)
	if got := workerExecutionDigest(t, repository); got == checksumChanged {
		t.Fatal("selected external module version did not change worker execution digest")
	}
}

func TestWorkerExecutionDigestTracksLinuxPackageBuildInputs(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/executor_runtime.go",
		fingerprintWorkerCgoSource(),
	)
	inputs := map[string]string{
		"internal/devtools/gate/worker_linux_amd64.c":     "int worker_native(void) { return 1; }\n",
		"internal/devtools/gate/worker_linux_amd64.s":     "TEXT workerAsmV1(SB),$0\n",
		"internal/devtools/gate/worker_linux_amd64.syso":  "worker-object-v1\n",
		"internal/devtools/gate/native/libworker_linux.a": "worker-library-v1\n",
		"internal/devtools/gate/worker_darwin_arm64.s":    "TEXT workerDarwinV1(SB),$0\n",
	}
	for filePath, content := range inputs {
		commitFingerprintChange(t, repository, filePath, content)
	}
	initial := workerExecutionDigest(t, repository)

	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/worker_darwin_arm64.s",
		"TEXT workerDarwinV2(SB),$0\n",
	)
	if got := workerExecutionDigest(t, repository); got != initial {
		t.Fatal("darwin/arm64 package input changed linux/amd64 worker execution digest")
	}

	previous := initial
	changes := []struct {
		path    string
		content string
		name    string
	}{
		{
			path:    "internal/devtools/gate/worker_linux_amd64.c",
			content: "int worker_native(void) { return 2; }\n",
			name:    "C source",
		},
		{
			path:    "internal/devtools/gate/worker_linux_amd64.s",
			content: "TEXT workerAsmV2(SB),$0\n",
			name:    "assembly source",
		},
		{
			path:    "internal/devtools/gate/worker_linux_amd64.syso",
			content: "worker-object-v2\n",
			name:    "link object",
		},
		{
			path:    "internal/devtools/gate/native/libworker_linux.a",
			content: "worker-library-v2\n",
			name:    "cgo linked library",
		},
	}
	for _, change := range changes {
		commitFingerprintChange(t, repository, change.path, change.content)
		current := workerExecutionDigest(t, repository)
		if current == previous {
			t.Fatalf("%s did not change worker execution digest", change.name)
		}
		previous = current
	}
}

func TestWorkerExecutionDigestTracksExplicitGoRunFileWithIgnoreTag(t *testing.T) {
	repository := newFingerprintRepository(t)
	executorSource := strings.Replace(
		fingerprintWorkerExecutorSource(1),
		"_ = workerSelfCommand",
		"_ = workerSelfCommand\n\t_ = workerIgnoredGoCommand",
		1,
	)
	commitFingerprintChange(t, repository, "internal/devtools/gate/executor.go", executorSource)
	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/executor_mapping.go",
		fingerprintWorkerExecutorMappingSource("./scripts/test_with_guard.sh")+
			"\nvar workerIgnoredGoCommand = []string{\"go\", \"run\", \"./scripts/ignored_worker.go\", \"-test=false\", \"--\", \"./...\"}\n",
	)
	commitFingerprintChange(
		t,
		repository,
		"scripts/ignored_worker.go",
		"//go:build ignore\n\npackage main\n\nconst marker = 1\n",
	)
	initial := workerExecutionDigest(t, repository)

	commitFingerprintChange(
		t,
		repository,
		"scripts/ignored_worker.go",
		"//go:build ignore\n\npackage main\n\nconst marker = 2\n",
	)
	if got := workerExecutionDigest(t, repository); got == initial {
		t.Fatal("explicit go run file did not change worker execution digest")
	}
}

func TestWorkerExecutionDigestDoesNotExpandGoTestTargets(t *testing.T) {
	repository := newFingerprintRepository(t)
	executorSource := strings.Replace(
		fingerprintWorkerExecutorSource(1),
		"_ = workerSelfCommand",
		"_ = workerSelfCommand\n\t_ = workerGoTestCommand",
		1,
	)
	commitFingerprintChange(t, repository, "internal/devtools/gate/executor.go", executorSource)
	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/executor_mapping.go",
		fingerprintWorkerExecutorMappingSource("./scripts/test_with_guard.sh")+
			"\nvar workerGoTestCommand = []string{\"go\", \"test\", \"./...\"}\n",
	)
	initial := workerExecutionDigest(t, repository)

	commitFingerprintChange(
		t,
		repository,
		"internal/a/a.go",
		"package a\n\nimport \"example.com/fingerprint/internal/b\"\n\nvar Value = b.Value + 1\n",
	)
	if got := workerExecutionDigest(t, repository); got != initial {
		t.Fatal("go test target source changed worker execution digest")
	}
}

func TestWorkerExecutionDigestRejectsMissingStaticCommandAsset(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(
		t,
		repository,
		"internal/devtools/gate/executor_mapping.go",
		fingerprintWorkerExecutorMappingSource("./scripts/missing-worker-command.sh"),
	)
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	if _, err := ResolveWorkerExecutionDigest(context.Background(), repository, tree); err == nil {
		t.Fatal("missing static worker command asset was accepted")
	}
}
