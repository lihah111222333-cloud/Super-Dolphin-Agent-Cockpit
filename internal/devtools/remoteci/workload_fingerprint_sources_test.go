package remoteci

import (
	"fmt"
	"strings"
)

func fingerprintWorkerRequestSource(marker int) string {
	return fmt.Sprintf(`package remoteci

const remoteCurrentGateBootstrapSH = "fixture-%d"

type Coordinator struct{}

func (*Coordinator) createRequest() []string {
	return remoteWorkerSupervisorCommand(remoteCurrentGateBootstrapSH)
}
`, marker)
}

func fingerprintWorkerMainSource(marker int) string {
	return fmt.Sprintf(`package main

import "example.com/fingerprint/internal/devtools/gate"

func runWorkerCLI() int {
	return gate.ExecuteExecutor() + %d
}

func dispatchWorkerTool(command string) int {
	switch command {
	case "worker-tool":
		return runWorkerTool()
	default:
		return 0
	}
}
`, marker)
}

func fingerprintWorkerExecutorSource(marker int) string {
	return fmt.Sprintf(`package gate

func ExecuteExecutor() int {
	_ = workerScriptCommand
	_ = workerMakeCommand
	_ = workerSelfCommand
	return executeProgram() + %d
}
`, marker)
}

func fingerprintWorkerExecutorRuntimeSource(marker int) string {
	return fmt.Sprintf(`package gate

import _ "embed"

//go:embed worker_asset.txt
var workerAsset string

func executeProgram() int {
	return len(workerAsset) + %d
}
`, marker)
}

func fingerprintWorkerExecutorIotaSource(extraSpec bool) string {
	extra := ""
	if extraSpec {
		extra = "\tignoredWorkerIotaOffset\n"
	}
	return fmt.Sprintf(`package gate

import _ "embed"

//go:embed worker_asset.txt
var workerAsset string

const (
	ignoredWorkerIota = iota
%s	workerIota
)

func executeProgram() int {
	return len(workerAsset) + workerIota
}
`, extra)
}

func fingerprintWorkerExternalModuleSource() string {
	return `package gate

import (
	_ "embed"

	workerruntime "example.com/worker/runtime"
)

//go:embed worker_asset.txt
var workerAsset string

func executeProgram() int {
	return len(workerAsset) + workerruntime.Value()
}
`
}

func fingerprintWorkerModuleFile(workerVersion string, includeUnrelated bool) string {
	unrelated := ""
	if includeUnrelated {
		unrelated = "\n\texample.com/unrelated v1.0.0"
	}
	return fmt.Sprintf(`module example.com/fingerprint

go 1.25

require (
	example.com/worker %s%s
)
`, workerVersion, unrelated)
}

func fingerprintWorkerModuleSums(
	workerChecksum string,
	includeUnrelated bool,
	includeFuture bool,
) string {
	var content strings.Builder
	fmt.Fprintf(&content, "example.com/worker v1.0.0 h1:%s\n", workerChecksum)
	fmt.Fprintln(&content, "example.com/worker v1.0.0/go.mod h1:worker-mod-v1")
	if includeFuture {
		fmt.Fprintln(&content, "example.com/worker v1.1.0 h1:worker-v11")
		fmt.Fprintln(&content, "example.com/worker v1.1.0/go.mod h1:worker-mod-v11")
	}
	if includeUnrelated {
		fmt.Fprintln(&content, "example.com/unrelated v1.0.0 h1:unrelated")
	}
	return content.String()
}

func fingerprintWorkerCgoSource() string {
	return `package gate

/*
int worker_native(void);
*/
import "C"

import _ "embed"

//go:embed worker_asset.txt
var workerAsset string

func executeProgram() int {
	return len(workerAsset) + int(C.worker_native())
}
`
}

func fingerprintWorkerExecutorMappingSource(script string) string {
	return fmt.Sprintf(`package gate

var workerScriptCommand = []string{%q}
var workerMakeCommand = []string{"make", "worker-target"}
var workerSelfCommand = []string{"super-dolphin-gate", "worker-tool"}
`, script)
}

func fingerprintWorkerToolSource(marker int) string {
	return fmt.Sprintf(`package main

func runWorkerTool() int {
	return %d
}
`, marker)
}

func fingerprintWorkerProtocolSource(marker int) string {
	return fmt.Sprintf(`package remoteci

const workerProtocol = %d

func DecodeShardRequest() int {
	return workerProtocol
}
`, marker)
}

func fingerprintWorkerMaterializerSource(marker int) string {
	return fmt.Sprintf(`package main

import "example.com/fingerprint/internal/devtools/remoteci"

func runRemoteMaterialize() int {
	return remoteci.DecodeShardRequest() + materializeWorker()
}

func materializeWorker() int {
	return %d
}
`, marker)
}

func fingerprintWorkerGuardScript(marker int) string {
	return fmt.Sprintf(`#!/bin/sh
source "$ROOT_DIR/scripts/real_go_resolver.sh"
./scripts/worker_command.sh --marker=%d
`, marker)
}

func fingerprintWorkerMakefile(workerMarker int, unrelatedMarker int) string {
	return fmt.Sprintf(`worker-target:
	./scripts/worker_command.sh --make-marker=%d

unrelated:
	echo %d
`, workerMarker, unrelatedMarker)
}
