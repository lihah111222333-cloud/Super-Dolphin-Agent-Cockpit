package localci

import (
	"slices"
	"testing"
)

func assertControlledBuildxLifecycle(t *testing.T, calls []buildxCommandCall, request BuildKitBuildRequest) {
	t.Helper()
	if len(calls) != 12 {
		t.Fatalf("controlled buildx lifecycle call count = %d", len(calls))
	}
	name := assertControlledBuilderCreate(t, calls[0].args, request)
	assertExactBuildxCommand(t, calls[1].args, []string{"buildx", "inspect", "--builder", name, "--bootstrap"}, "controlled builder inspect command")
	assertExactBuildxCommand(t, calls[2].args, []string{"container", "update", "--pids-limit", buildxBuilderPidsLimit, controlledBuildxContainerName(name)}, "controlled builder PIDs update command")
	assertExactBuildxCommand(t, calls[3].args, []string{"container", "inspect", "--format", "{{.Config.Image}}\n{{.Image}}\n{{.HostConfig.CpuQuota}}/{{.HostConfig.CpuPeriod}}/{{.HostConfig.Memory}}/{{.HostConfig.PidsLimit}}", controlledBuildxContainerName(name)}, "controlled builder resource inspect command")
	assertExactBuildxCommand(t, calls[4].args, []string{"image", "inspect", "--format", "{{.Id}}\n{{range .RepoDigests}}{{println .}}{{end}}", request.BuildKitImage}, "controlled builder image inspect command")
	assertRuntimeDepsBuildxArgs(t, calls[5].args, request)
	assertExactBuildxCommand(t, calls[7].args, []string{"buildx", "history", "inspect", "attachment", "--builder", name, testBuildxHistoryRecordReference, validBuildxManifestDigest(t)}, "controlled build record attachment command")
	assertExactBuildxCommand(t, calls[8].args, []string{"buildx", "rm", "--force", name}, "controlled builder cleanup command")
	assertExactBuildxCommand(t, calls[9].args, []string{"container", "rm", "--force", controlledBuildxContainerName(name)}, "controlled builder container cleanup command")
	assertExactBuildxCommand(t, calls[10].args, []string{"buildx", "ls", "--format", "{{.Name}}"}, "controlled builder absence witness command")
	assertExactBuildxCommand(t, calls[11].args, []string{"container", "ls", "--all", "--filter", "name=^/buildx_buildkit_" + buildxBuilderNamePrefix, "--format", "{{.Names}}"}, "controlled builder container absence witness command")
}

func assertControlledBuilderCreate(t *testing.T, create []string, request BuildKitBuildRequest) string {
	if len(create) < 2 || create[0] != "buildx" || create[1] != "create" {
		t.Fatalf("controlled builder create command = %v", create)
	}
	name := valueAfter(create, "--name")
	assertBuildxArgumentsContain(t, create, []string{"--driver", "docker-container", "--driver-opt=image=" + request.BuildKitImage, "--driver-opt=cpu-quota=" + buildxBuilderCPUQuota, "--driver-opt=cpu-period=" + buildxBuilderCPUPeriod, "--driver-opt=memory=" + buildxBuilderMemory}, "controlled builder create command")
	if pidsOptions := prefixedArguments(create, "--driver-opt=pids-limit="); len(pidsOptions) != 0 {
		t.Fatalf("controlled builder create command contains unsupported PIDs driver option: %v", pidsOptions)
	}
	return name
}

func assertExactBuildxCommand(t *testing.T, actual []string, expected []string, subject string) {
	if !slices.Equal(actual, expected) {
		t.Fatalf("%s = %v", subject, actual)
	}
}

func valueAfter(arguments []string, flag string) string {
	for index, argument := range arguments {
		if argument == flag && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func recordedBuildxBuildCall(t *testing.T, calls []buildxCommandCall) buildxCommandCall {
	for _, call := range calls {
		if len(call.args) >= 2 && call.args[0] == "buildx" && call.args[1] == "build" &&
			!slices.Contains(call.args, "--file=build/gate/runtime-deps.Dockerfile") {
			return call
		}
	}
	t.Fatalf("buildx build command was not recorded: %v", calls)
	return buildxCommandCall{}
}
