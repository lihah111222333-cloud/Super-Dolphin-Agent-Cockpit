package localci

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestDockerBuildxRunnerUsesFixedCommandAndCanonicalStdin(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	result, err := runner.Build(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.PlatformManifestDigest != validBuildxManifestDigest(t) || result.ConfigDigest != digest("4") {
		t.Fatalf("build result = %#v", result)
	}
	if len(executor.calls) != 12 {
		t.Fatalf("buildx calls = %d", len(executor.calls))
	}
	assertControlledBuildxLifecycle(t, executor.calls, request)
	call := executor.calls[6]
	if !bytes.Equal(call.stdin, request.ContextTar) {
		t.Fatal("buildx stdin did not receive the canonical context tar")
	}
	assertFixedBuildxArgs(t, call.args, request, root)
	if _, err := os.Stat(filepath.Dir(executor.path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("build temporary directory was not removed: %v", err)
	}
}

func TestCreatePrivateBuildxWorkspaceCleansUpAfterChmodFailure(t *testing.T) {
	chmodErr := errors.New("chmod denied")
	cleanupErr := errors.New("cleanup denied")
	const workspace = "workspace"
	removed := ""
	created, err := createPrivateBuildxWorkspace("", func(string, string) (string, error) { return workspace, nil }, func(string, os.FileMode) error { return chmodErr }, func(path string) error { removed = path; return cleanupErr })
	if created != "" || removed != workspace {
		t.Fatalf("created workspace = %q, removed workspace = %q", created, removed)
	}
	if !errors.Is(err, chmodErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("workspace error = %v", err)
	}
}

func TestDockerBuildxRunnerUsesOneSharedCacheForAllImages(t *testing.T) {
	request := validBuildxRequest(t)
	if request.CacheNamespace == request.RuntimeDepsInputDigest {
		t.Fatal("fixture must use different candidate and runtime dependency digests")
	}
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	cachePath := filepath.Join(root, "cache", buildxSharedCacheDirectory)
	writeBuildxCacheLayout(t, cachePath)
	if _, err := runner.Build(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	cacheFrom := "--cache-from=type=local,src=" + cachePath
	buildCall := recordedBuildxBuildCall(t, executor.calls)
	if !slices.Contains(buildCall.args, cacheFrom) {
		t.Fatalf("candidate image did not import the shared cache: %v", buildCall.args)
	}
	runtimeCalls := recordedRuntimeDepsBuildxCalls(executor.calls)
	if len(runtimeCalls) != 1 || !slices.Contains(runtimeCalls[0].args, cacheFrom) {
		t.Fatalf("runtime dependency image did not import the shared cache: %v", runtimeCalls)
	}
}

func TestBuildxSharedCachePathIsNodeConfiguredAndWorktreeIndependent(t *testing.T) {
	requests := []BuildKitBuildRequest{validBuildxRequest(t), validBuildxRequest(t)}
	requests[1].SourceTreeSHA = digest("b")
	requests[1].CacheNamespace = digest("c")
	requests[1].RuntimeDepsInputDigest = digest("d")
	for _, nodeRoot := range []string{privateTempRoot(t), privateTempRoot(t)} {
		runner, err := newDockerBuildxRunner(validBuildxExecutor(t, requests[0]), nodeRoot)
		if err != nil {
			t.Fatal(err)
		}
		cachePath := filepath.Join(runner.cacheRoot, buildxSharedCacheDirectory)
		wantCacheTo := "--cache-to=type=local,dest=" + cachePath + ",mode=max"
		for _, request := range requests {
			runtimeArgs := runtimeDepsBuildxArgs(request, "/tmp/runtime", "/tmp/runtime.json", cachePath, true, "builder")
			candidateArgs := runner.commandArgs(request, "/tmp/candidate.json", "candidate", true, "builder")
			for _, args := range [][]string{runtimeArgs, candidateArgs} {
				if !slices.Contains(args, wantCacheTo) {
					t.Fatalf("build does not use node-configured shared cache %q: %v", cachePath, args)
				}
			}
		}
	}
}

func TestDockerBuildxRunnerRetriesRuntimeDependenciesInSameBuilder(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	executor.runtimeBuildErrs = []error{errors.New("temporary runtime dependency download failure")}
	runner, _ := newTestDockerBuildxRunner(t, executor)
	if _, err := runner.Build(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	runtimeCalls := recordedRuntimeDepsBuildxCalls(executor.calls)
	if len(runtimeCalls) != 2 {
		t.Fatalf("runtime dependency build calls = %d, want 2", len(runtimeCalls))
	}
	firstBuilder := valueWithPrefix(runtimeCalls[0].args, "--builder=")
	secondBuilder := valueWithPrefix(runtimeCalls[1].args, "--builder=")
	if firstBuilder == "" || firstBuilder != secondBuilder {
		t.Fatalf("runtime dependency retry changed controlled builder: first=%q second=%q", firstBuilder, secondBuilder)
	}
	for _, call := range runtimeCalls {
		if !bytes.Equal(call.stdin, request.ContextTar) {
			t.Fatal("runtime dependency retry did not receive the canonical context tar")
		}
	}
}

func TestDockerBuildxRunnerStopsAfterTwoRuntimeDependencyFailures(t *testing.T) {
	request := validBuildxRequest(t)
	firstErr := errors.New("first runtime dependency failure")
	secondErr := errors.New("second runtime dependency failure")
	executor := validBuildxExecutor(t, request)
	executor.runtimeBuildErrs = []error{firstErr, secondErr}
	runner, _ := newTestDockerBuildxRunner(t, executor)
	_, err := runner.Build(context.Background(), request)
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("runtime dependency retry error = %v", err)
	}
	if runtimeCalls := recordedRuntimeDepsBuildxCalls(executor.calls); len(runtimeCalls) != 2 {
		t.Fatalf("runtime dependency build calls = %d, want 2", len(runtimeCalls))
	}
	for _, call := range executor.calls {
		if slices.Contains(call.args, "--file="+request.DockerfilePath) {
			t.Fatal("candidate build ran after runtime dependency retries were exhausted")
		}
	}
}

func recordedRuntimeDepsBuildxCalls(calls []buildxCommandCall) []buildxCommandCall {
	var matched []buildxCommandCall
	for _, call := range calls {
		if slices.Contains(call.args, "--file=build/gate/runtime-deps.Dockerfile") {
			matched = append(matched, call)
		}
	}
	return matched
}

func writeBuildxCacheLayout(t *testing.T, cachePath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(cachePath, "blobs", "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "index.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "oci-layout"), []byte("{\"imageLayoutVersion\":\"1.0.0\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewDockerBuildxRunnerRejectsTypedNilExecutor(t *testing.T) {
	var executor *recordingBuildxCommandExecutor
	runner, err := newDockerBuildxRunner(executor, privateTempRoot(t))
	if err == nil || runner != nil {
		t.Fatal("typed-nil buildx command executor was accepted")
	}
}

func TestNewDockerBuildxRunnerCreatesRealAdapter(t *testing.T) {
	runner, err := NewDockerBuildxRunner(privateTempRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil || buildxCommandExecutorIsNil(runner.executor) {
		t.Fatal("real docker buildx adapter was not constructed")
	}
}

func TestDockerBuildxRunnerLocalSmoke(t *testing.T) {
	if os.Getenv("LOCALCI_BUILDX_SMOKE") != "1" {
		t.Skip("set LOCALCI_BUILDX_SMOKE=1 to run the local Docker buildx smoke")
	}
	request := localBuildxSmokeRequest(t)
	runner, err := NewDockerBuildxRunner(privateTempRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Build(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDigest("smoke platform manifest digest", result.PlatformManifestDigest); err != nil {
		t.Fatal(err)
	}
	if err := validateDigest("smoke config digest", result.ConfigDigest); err != nil {
		t.Fatal(err)
	}
	imageReference, err := CandidateImageReference(result.PlatformManifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	createOutput, err := (execDockerRunner{}).Run(context.Background(), "create", imageReference, "true")
	if err != nil {
		t.Fatal(err)
	}
	containerID := strings.TrimSpace(createOutput)
	if !isContainerID(containerID) {
		t.Fatalf("docker create returned invalid container ID %q", containerID)
	}
	t.Cleanup(func() {
		if _, err := (execDockerRunner{}).Run(context.Background(), "rm", "--force", containerID); err != nil {
			t.Errorf("remove smoke container: %v", err)
		}
	})
}

func TestCandidateImageReferenceUsesFixedRepository(t *testing.T) {
	reference, err := CandidateImageReference(digest("5"))
	if err != nil {
		t.Fatal(err)
	}
	wanted := "docker.io/library/super-dolphin-gate-local@" + digest("5")
	if reference != wanted {
		t.Fatalf("candidate image reference = %q, want %q", reference, wanted)
	}
	if _, err := CandidateImageReference("attacker.invalid/image:latest"); err == nil {
		t.Fatal("mutable repository and tag input was accepted as a manifest digest")
	}
}

func TestDockerBuildxRunnerRejectsTypedNilRunner(t *testing.T) {
	var runner *DockerBuildxRunner
	_, err := runner.Build(context.Background(), validBuildxRequest(t))
	if err == nil {
		t.Fatal("typed-nil docker buildx runner was accepted")
	}
}

func TestDockerBuildxRunnerRejectsCanceledContextWithoutCommand(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, _ := newTestDockerBuildxRunner(t, executor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runner.Build(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled build error = %v", err)
	}
	if len(executor.calls) != 0 {
		t.Fatal("canceled build reached the command executor")
	}
}

func TestDockerBuildxRunnerRejectsSymlinkCacheEscape(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	outside := privateTempRoot(t)
	cachePath := filepath.Join(root, "cache", buildxSharedCacheDirectory)
	if err := os.Symlink(outside, cachePath); err != nil {
		t.Skipf("create cache symlink: %v", err)
	}
	if _, err := runner.Build(context.Background(), request); err == nil {
		t.Fatal("symlink cache namespace escape was accepted")
	}
	if len(executor.calls) != 0 {
		t.Fatal("symlink cache namespace reached the command executor")
	}
}

func TestDockerBuildxRunnerRejectsUnsafeRequestBeforeCommand(t *testing.T) {
	base := validBuildxRequest(t)
	tests := []struct {
		name   string
		mutate func(*BuildKitBuildRequest)
	}{
		{name: "duplicate build argument", mutate: func(request *BuildKitBuildRequest) {
			request.BuildArguments = append(request.BuildArguments, request.BuildArguments[0])
		}},
		{name: "out of order build arguments", mutate: func(request *BuildKitBuildRequest) {
			request.BuildArguments = []BuildArgument{{Name: "Z_IMAGE", Value: request.BuildArguments[0].Value}, request.BuildArguments[0]}
		}},
		{name: "missing source date epoch", mutate: func(request *BuildKitBuildRequest) {
			request.BuildArguments = slices.DeleteFunc(request.BuildArguments, func(argument BuildArgument) bool {
				return argument.Name == sourceDateEpochArgument
			})
		}},
		{name: "non-canonical source date epoch", mutate: func(request *BuildKitBuildRequest) {
			for index := range request.BuildArguments {
				if request.BuildArguments[index].Name == sourceDateEpochArgument {
					request.BuildArguments[index].Value = "00"
				}
			}
		}},
		{name: "Dockerfile path escape", mutate: func(request *BuildKitBuildRequest) {
			request.DockerfilePath = "../Dockerfile"
		}},
		{name: "cache namespace escape", mutate: func(request *BuildKitBuildRequest) {
			request.CacheNamespace = "../shared"
		}},
		{name: "host network", mutate: func(request *BuildKitBuildRequest) {
			request.NetworkPolicy = "host"
		}},
		{name: "missing policy digest", mutate: func(request *BuildKitBuildRequest) {
			request.PolicyDigest = ""
		}},
		{name: "unknown image schema", mutate: func(request *BuildKitBuildRequest) {
			request.ImageSchemaVersion = "2"
		}},
		{name: "mutable BuildKit image", mutate: func(request *BuildKitBuildRequest) {
			request.BuildKitImage = "moby/buildkit:v0.26.2"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := cloneBuildxRequest(base)
			test.mutate(&request)
			executor := validBuildxExecutor(t, base)
			runner, _ := newTestDockerBuildxRunner(t, executor)
			if _, err := runner.Build(context.Background(), request); err == nil {
				t.Fatal("unsafe BuildKit request was accepted")
			}
			if len(executor.calls) != 0 {
				t.Fatal("unsafe BuildKit request reached the command executor")
			}
		})
	}
}

func TestDockerBuildxRunnerRejectsParserDirectivesBeforeBuilderCreation(t *testing.T) {
	for _, directive := range []string{"# syntax=docker/dockerfile:1", "# escape=`", "# check=skip=JSONArgsRecommended", "# future-directive=value"} {
		t.Run(directive, func(t *testing.T) {
			entries := candidateEntries(directive + "\n" + validCandidateDockerfile())
			prepared, err := prepareCandidate(candidateRequest(entries, digest("f"), digest("e")))
			if err != nil {
				t.Fatal(err)
			}
			executor := validBuildxExecutor(t, prepared.buildRequest)
			runner, _ := newTestDockerBuildxRunner(t, executor)
			if _, err := runner.Build(context.Background(), prepared.buildRequest); err == nil {
				t.Fatal("Dockerfile parser directive was accepted")
			}
			if len(executor.calls) != 0 {
				t.Fatal("Dockerfile parser directive reached the command executor")
			}
		})
	}
}

func TestDockerBuildxRunnerRejectsBuilderVersionAndResourceDrift(t *testing.T) {
	request := validBuildxRequest(t)
	tests := []struct {
		name   string
		mutate func(*recordingBuildxCommandExecutor)
	}{
		{name: "BuildKit version", mutate: func(executor *recordingBuildxCommandExecutor) {
			executor.inspectOutput = "BuildKit version: v0.0.0\n"
		}},
		{name: "resource limits", mutate: func(executor *recordingBuildxCommandExecutor) {
			executor.containerOutput = request.BuildKitImage + "\n" + digest("a") + "\n0/0/0\n"
		}},
		{name: "container image reference", mutate: func(executor *recordingBuildxCommandExecutor) {
			executor.containerOutput = "docker.io/moby/buildkit@" + digest("b") + "\n" + digest("a") + "\n" + buildxBuilderCPUQuota + "/" + buildxBuilderCPUPeriod + "/" + buildxBuilderMemoryBytes + "/" + buildxBuilderPidsLimit + "\n"
		}},
		{name: "image repository digest", mutate: func(executor *recordingBuildxCommandExecutor) {
			executor.imageOutput = digest("a") + "\n" + "docker.io/moby/buildkit@" + digest("b") + "\n"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := validBuildxExecutor(t, request)
			test.mutate(executor)
			runner, _ := newTestDockerBuildxRunner(t, executor)
			if _, err := runner.Build(context.Background(), request); err == nil {
				t.Fatal("drifted controlled builder was accepted")
			}
			if len(executor.calls) < 4 || executor.calls[len(executor.calls)-4].args[1] != "rm" {
				t.Fatalf("controlled builder was not removed after validation failure: %v", executor.calls)
			}
			for _, call := range executor.calls {
				if len(call.args) >= 2 && call.args[0] == "buildx" && call.args[1] == "build" {
					t.Fatal("drifted controlled builder reached buildx build")
				}
			}
		})
	}
}

func TestDockerBuildxRunnerCleansUpWithBoundedContextAfterBuildCancellation(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	ctx, cancel := context.WithCancel(context.Background())
	executor.onBuild = cancel
	runner, _ := newTestDockerBuildxRunner(t, executor)
	_, err := runner.Build(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled build error = %v", err)
	}
	if len(executor.calls) != 11 || executor.calls[len(executor.calls)-4].args[1] != "rm" {
		t.Fatalf("controlled builder cleanup call = %v", executor.calls)
	}
	if cleanupErr := executor.contextErrs[len(executor.contextErrs)-1]; cleanupErr != nil {
		t.Fatalf("controlled builder cleanup reused canceled build context: %v", cleanupErr)
	}
}

func TestValidateBuildxInspectVersionRequiresOneRealFormatField(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		valid  bool
	}{
		{name: "real format", output: "Name: controlled\nBuildKit version: v0.26.2\n", valid: true},
		{name: "aligned real format", output: "Name: controlled\nBuildKit version:      v0.26.2\n", valid: true},
		{name: "tab aligned real format", output: "Name: controlled\nBuildKit version:\t v0.26.2\n", valid: true},
		{name: "legacy spelling", output: "Buildkit: v0.26.2\n"},
		{name: "missing", output: "Name: controlled\n"},
		{name: "missing separator", output: "BuildKit version:v0.26.2\n"},
		{name: "embedded whitespace", output: "BuildKit version: v0.26.2 invalid\n"},
		{name: "duplicate", output: "BuildKit version: v0.26.2\nBuildKit version: v0.26.2\n"},
		{name: "mismatch", output: "BuildKit version: v0.26.1\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateBuildxInspectVersion(test.output, "v0.26.2")
			if (err == nil) != test.valid {
				t.Fatalf("validate BuildKit version error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestValidateBuildxImageIdentityAcceptsOnlyLockedDockerHubNormalization(t *testing.T) {
	expected := "docker.io/moby/buildkit@" + digest("b")
	imageID := digest("a")
	for _, test := range []struct {
		name       string
		references []string
		valid      bool
	}{
		{name: "canonical", references: []string{expected}, valid: true},
		{name: "Docker Hub normalized", references: []string{strings.TrimPrefix(expected, "docker.io/")}, valid: true},
		{name: "wrong repository", references: []string{"attacker.invalid/moby/buildkit@" + digest("b")}},
		{name: "wrong digest", references: []string{"moby/buildkit@" + digest("c")}},
		{name: "duplicate equivalent references", references: []string{expected, strings.TrimPrefix(expected, "docker.io/")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := imageID + "\n" + strings.Join(test.references, "\n") + "\n"
			err := validateBuildxImageIdentity(output, expected, imageID)
			if (err == nil) != test.valid {
				t.Fatalf("validate image identity error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestDockerBuildxRunnerRejectsCommandFailureAndAcceptsPlainProgress(t *testing.T) {
	request := validBuildxRequest(t)
	t.Run("command failure", func(t *testing.T) {
		executor := validBuildxExecutor(t, request)
		executor.err = errors.New("buildx failed")
		runner, _ := newTestDockerBuildxRunner(t, executor)
		if _, err := runner.Build(context.Background(), request); err == nil {
			t.Fatal("failed buildx command was accepted")
		}
	})
	t.Run("plain progress", func(t *testing.T) {
		executor := validBuildxExecutor(t, request)
		executor.output = "#1 loading build definition\n#2 exporting to docker image format\n"
		runner, _ := newTestDockerBuildxRunner(t, executor)
		if _, err := runner.Build(context.Background(), request); err != nil {
			t.Fatalf("plain buildx progress was rejected: %v", err)
		}
	})
}

func TestDockerBuildxRunnerRecordsBeforeUnknownCreateSideEffectAndCleansIt(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	executor.createSideEffect = true
	executor.createErr = errors.New("create response lost")
	runner, _ := newTestDockerBuildxRunner(t, executor)
	if _, err := runner.Build(context.Background(), request); !errors.Is(err, executor.createErr) {
		t.Fatalf("unknown create side effect error = %v", err)
	}
	if names, err := runner.recordedControlledBuilderNames(); err != nil || len(names) != 0 {
		t.Fatalf("unknown create side effect retained ownership record: names=%v err=%v", names, err)
	}
	if executor.builders[executor.builderName] || executor.containers[executor.builderName] {
		t.Fatal("unknown create side effect retained controlled builder resources")
	}
}

func TestDockerBuildxRunnerRetainsOwnershipWhenRemovalFails(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	executor.createSideEffect = true
	executor.removeErr = errors.New("builder removal rejected")
	runner, _ := newTestDockerBuildxRunner(t, executor)
	if _, err := runner.Build(context.Background(), request); !errors.Is(err, executor.removeErr) {
		t.Fatalf("removal failure error = %v", err)
	}
	names, err := runner.recordedControlledBuilderNames()
	if err != nil || !slices.Equal(names, []string{executor.builderName}) {
		t.Fatalf("removal failure released controlled builder ownership: names=%v err=%v", names, err)
	}
}

func TestDockerBuildxRunnerReclaimsPersistedOwnerAfterRestart(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	builderName := buildxBuilderName(request.SourceTreeSHA, "candidate-restart")
	if err := runner.recordControlledBuilder(builderName, request); err != nil {
		t.Fatal(err)
	}
	executor.builders = map[string]bool{builderName: true}
	executor.containers = map[string]bool{builderName: true}
	restarted, err := newDockerBuildxRunner(executor, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverControlledBuilders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if names, err := restarted.recordedControlledBuilderNames(); err != nil || len(names) != 0 {
		t.Fatalf("restart reaper retained ownership record: names=%v err=%v", names, err)
	}
	if executor.builders[builderName] || executor.containers[builderName] {
		t.Fatal("restart reaper retained controlled builder resources")
	}
}

func TestParseControlledBuildxBuilderNamesAcceptsRealAndTestFormats(t *testing.T) {
	const builderName = "super-dolphin-gate-0123456789ab-candidate-restart"
	tests := []struct {
		name   string
		output string
		want   []string
	}{
		{name: "test fixture", output: builderName + "\n", want: []string{builderName}},
		{name: "real current builder", output: builderName + "*\n", want: []string{builderName}},
		{name: "unmanaged current builder", output: "default*\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			names, err := parseControlledBuildxBuilderNames(test.output, "builder")
			if err != nil || !slices.Equal(names, test.want) {
				t.Fatalf("parsed builder names = %v, err = %v, want %v", names, err, test.want)
			}
		})
	}
	for _, output := range []string{
		buildxBuilderNamePrefix + "invalid*\n",
		builderName + "**\n",
	} {
		if _, err := parseControlledBuildxBuilderNames(output, "builder"); err == nil {
			t.Fatalf("invalid controlled builder listing %q was accepted", output)
		}
	}
}

func TestParseControlledBuildxBuilderNamesAcceptsRealLegacyListing(t *testing.T) {
	const legacyBuilder = "super-dolphin-gate-candidate-2734113458"
	const legacyNode = legacyBuilder + "0"
	builderOutput := legacyBuilder + "\n" + legacyNode + "\ndesktop-linux\ndesktop-linux\ndefault\n"
	builderNames, err := parseControlledBuildxBuilderNames(builderOutput, "builder")
	if err != nil || !slices.Equal(builderNames, []string{legacyBuilder, legacyNode}) {
		t.Fatalf("legacy builder names = %v, err = %v", builderNames, err)
	}
	containerNames, err := parseControlledBuildxContainerNames("buildx_buildkit_" + legacyNode + "\n")
	if err != nil || !slices.Equal(containerNames, []string{legacyBuilder}) {
		t.Fatalf("legacy container builder names = %v, err = %v", containerNames, err)
	}
	for _, output := range []string{
		buildxBuilderNamePrefix + "candidate-not-numeric\n",
		buildxBuilderNamePrefix + "unknown-2734113458\n",
	} {
		if _, err := parseControlledBuildxBuilderNames(output, "builder"); err == nil {
			t.Fatalf("unknown controlled builder listing %q was accepted", output)
		}
	}
}

func TestDockerBuildxRunnerReclaimsLegacyBuilderAndNodeAfterRestart(t *testing.T) {
	const legacyBuilder = "super-dolphin-gate-candidate-2734113458"
	const legacyNode = legacyBuilder + "0"
	executor := &recordingBuildxCommandExecutor{
		builders:   map[string]bool{legacyBuilder: true, legacyNode: true},
		containers: map[string]bool{legacyBuilder: true},
	}
	runner, _ := newTestDockerBuildxRunner(t, executor)
	if err := runner.RecoverControlledBuilders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.builders[legacyBuilder] || executor.builders[legacyNode] || executor.containers[legacyBuilder] {
		t.Fatal("legacy restart reaper retained controlled builder resources")
	}
}

func cloneBuildxRequest(request BuildKitBuildRequest) BuildKitBuildRequest {
	request.ContextTar = append([]byte(nil), request.ContextTar...)
	request.BuildArguments = append([]BuildArgument(nil), request.BuildArguments...)
	return request
}

func validBuildxExecutor(t *testing.T, request BuildKitBuildRequest) *recordingBuildxCommandExecutor {
	t.Helper()
	return &recordingBuildxCommandExecutor{
		request: request,
		output:  validBuildxManifestDigest(t) + "\n",
	}
}

func newTestDockerBuildxRunner(t *testing.T, executor buildxCommandExecutor) (*DockerBuildxRunner, string) {
	t.Helper()
	root := privateTempRoot(t)
	runner, err := newDockerBuildxRunner(executor, root)
	if err != nil {
		t.Fatal(err)
	}
	return runner, runner.root
}

func privateTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertFixedBuildxArgs(t *testing.T, args []string, request BuildKitBuildRequest, root string) {
	t.Helper()
	assertBuildxCommandShape(t, args)
	assertControlledBuilderArgument(t, args)
	assertBuildxArgumentsContain(t, args, []string{"--output=type=docker,oci-mediatypes=false", "--progress=plain", "--provenance=false", "--platform=" + request.Platform, "--file=" + request.DockerfilePath, "--network=none"}, "buildx command")
	assertFixedBuildxTagAndCache(t, args, request)
	assertBuildxArgumentOrder(t, args, request)
	assertSharedBuildxCache(t, args, root)
	assertNoForbiddenBuildxArguments(t, args)
}

func assertBuildxCommandShape(t *testing.T, args []string) {
	if len(args) < 3 || args[0] != "buildx" || args[1] != "build" || args[len(args)-1] != "-" {
		t.Fatalf("buildx command shape = %v", args)
	}
}

func assertControlledBuilderArgument(t *testing.T, args []string) {
	builders := prefixedArguments(args, "--builder=")
	if len(builders) != 1 || !strings.HasPrefix(builders[0], "--builder="+buildxBuilderNamePrefix) {
		t.Fatalf("buildx controlled builder = %v", builders)
	}
}

func assertBuildxArgumentsContain(t *testing.T, args []string, required []string, subject string) {
	for _, argument := range required {
		if !slices.Contains(args, argument) {
			t.Fatalf("%s missing %q: %v", subject, argument, args)
		}
	}
}

func assertSharedBuildxCache(t *testing.T, args []string, root string) {
	cacheArgument := "--cache-to=type=local,dest=" + filepath.Join(root, "cache", buildxSharedCacheDirectory) + ",mode=max"
	if !slices.Contains(args, cacheArgument) {
		t.Fatalf("buildx command does not export the node shared cache: %v", args)
	}
}

func assertNoForbiddenBuildxArguments(t *testing.T, args []string) {
	forbidden := []string{"--secret", "--ssh", "--allow", "network=host", "security.insecure", "http_proxy", "https_proxy", "all_proxy", "docker.sock"}
	joined := strings.ToLower(strings.Join(args, "\n"))
	for _, fragment := range forbidden {
		if strings.Contains(joined, fragment) {
			t.Fatalf("buildx command contains forbidden fragment %q: %v", fragment, args)
		}
	}
}

func assertFixedBuildxTagAndCache(t *testing.T, args []string, request BuildKitBuildRequest) {
	wantedTag := "--tag=" + expectedCandidateImageTag(request)
	if actualTags := prefixedArguments(args, "--tag="); !slices.Equal(actualTags, []string{wantedTag}) {
		t.Fatalf("buildx candidate tags = %v, want %q", actualTags, wantedTag)
	}
	if cacheFrom := prefixedArguments(args, "--cache-from="); len(cacheFrom) != 0 {
		t.Fatalf("first build imported unexpected cache: %v", cacheFrom)
	}
}

func assertBuildxArgumentOrder(t *testing.T, args []string, request BuildKitBuildRequest) {
	actualBuildArguments := prefixedArguments(args, "--build-arg=")
	wantedBuildArguments := make([]string, len(request.BuildArguments))
	for index, argument := range request.BuildArguments {
		wantedBuildArguments[index] = "--build-arg=" + argument.Name + "=" + argument.Value
	}
	if !slices.Equal(actualBuildArguments, wantedBuildArguments) {
		t.Fatalf("build arguments = %v, want %v", actualBuildArguments, wantedBuildArguments)
	}
	actualLabels := prefixedArguments(args, "--label=")
	wantedLabels := buildxBindingLabels(request)
	if !slices.Equal(actualLabels, wantedLabels) {
		t.Fatalf("build labels = %v, want %v", actualLabels, wantedLabels)
	}
}

func prefixedArguments(args []string, prefix string) []string {
	var matched []string
	for _, argument := range args {
		if strings.HasPrefix(argument, prefix) {
			matched = append(matched, argument)
		}
	}
	return matched
}

func buildxBindingLabels(request BuildKitBuildRequest) []string {
	labels := []string{
		"--label=org.super-dolphin.context-digest=" + request.ContextDigest,
		"--label=org.super-dolphin.dockerfile-digest=" + request.DockerfileDigest,
		"--label=org.super-dolphin.image-input-digest=" + request.InputDigest,
		"--label=org.super-dolphin.platform=" + request.Platform,
		"--label=org.super-dolphin.policy-sha=" + request.PolicyDigest,
		"--label=org.super-dolphin.schema-version=" + request.ImageSchemaVersion,
		"--label=org.super-dolphin.source-tree-sha=" + request.SourceTreeSHA,
		"--label=org.super-dolphin.toolchain-digest=" + request.ToolchainDigest,
	}
	sort.Strings(labels)
	return labels
}

func buildxMetadataPath(args []string) string {
	const prefix = "--metadata-file="
	for _, argument := range args {
		if path, found := strings.CutPrefix(argument, prefix); found {
			return path
		}
	}
	return ""
}

const testBuildxBuilderPlaceholder = "__controlled_builder__"

const testBuildxHistoryRecordReference = "aaaaaaaaaaaaaaaaaaaaaaaaa"

func assertRuntimeDepsBuildxArgs(t *testing.T, args []string, request BuildKitBuildRequest) {
	t.Helper()
	assertBuildxCommandShape(t, args)
	assertControlledBuilderArgument(t, args)
	assertBuildxArgumentsContain(t, args, []string{"--progress=plain", "--platform=" + request.Platform, "--file=" + request.RuntimeDepsDockerfilePath, "--network=default"}, "runtime dependencies buildx command")
	if cacheTo := valueWithPrefix(args, "--cache-to="); !strings.HasPrefix(cacheTo, "--cache-to=type=local,dest=") || !strings.HasSuffix(cacheTo, ",mode=max") {
		t.Fatalf("runtime dependencies cache export = %q, want node-local mode=max", cacheTo)
	}
	if output := valueWithPrefix(args, "--output="); !strings.Contains(output, "/runtime-deps,tar=false") {
		t.Fatalf("runtime dependencies OCI layout output = %q", output)
	}
	actual := prefixedArguments(args, "--build-arg=")
	want := make([]string, len(request.RuntimeDepsBuildArguments))
	for index, argument := range request.RuntimeDepsBuildArguments {
		want[index] = "--build-arg=" + argument.Name + "=" + argument.Value
	}
	if !slices.Equal(actual, want) {
		t.Fatalf("runtime dependencies build arguments = %v, want %v", actual, want)
	}
	for _, forbidden := range []string{"--push", "--cache-from=type=registry", "--cache-to=type=registry", "--build-context=runtime-deps"} {
		if slices.Contains(args, forbidden) {
			t.Fatalf("runtime dependencies build contains forbidden cross-node argument %q", forbidden)
		}
	}
}

func valueWithPrefix(args []string, prefix string) string {
	for _, argument := range args {
		if strings.HasPrefix(argument, prefix) {
			return argument
		}
	}
	return ""
}
