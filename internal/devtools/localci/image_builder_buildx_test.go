package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
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
	if len(executor.calls) != 11 {
		t.Fatalf("buildx calls = %d", len(executor.calls))
	}
	assertControlledBuildxLifecycle(t, executor.calls, request)
	call := executor.calls[5]
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

func TestDockerBuildxRunnerUsesExistingIsolatedCache(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	cachePath := filepath.Join(root, "cache", strings.TrimPrefix(request.CacheNamespace, "sha256:"))
	writeBuildxCacheLayout(t, cachePath)
	if _, err := runner.Build(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	cacheFrom := "--cache-from=type=local,src=" + cachePath
	buildCall := recordedBuildxBuildCall(t, executor.calls)
	if !slices.Contains(buildCall.args, cacheFrom) {
		t.Fatalf("existing isolated cache was not imported: %v", buildCall.args)
	}
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
	cachePath := filepath.Join(root, "cache", strings.TrimPrefix(request.CacheNamespace, "sha256:"))
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
	if len(executor.calls) != 10 || executor.calls[len(executor.calls)-4].args[1] != "rm" {
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

func TestDockerBuildxRunnerRejectsCommandFailureAndTrailingOutput(t *testing.T) {
	request := validBuildxRequest(t)
	tests := []struct {
		name   string
		output string
		err    error
	}{
		{name: "command failure", err: errors.New("buildx failed")},
		{name: "trailing output", output: digest("4") + "\nwarning\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := validBuildxExecutor(t, request)
			executor.output = test.output
			executor.err = test.err
			runner, _ := newTestDockerBuildxRunner(t, executor)
			if _, err := runner.Build(context.Background(), request); err == nil {
				t.Fatal("invalid buildx command result was accepted")
			}
		})
	}
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

func TestSanitizedBuildxEnvironmentPreservesDockerConfigAndLocksControls(t *testing.T) {
	environment := []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://proxy.invalid",
		"https_proxy=http://proxy.invalid",
		"BUILDX_METADATA_PROVENANCE=disabled",
		"BUILDX_METADATA_WARNINGS=1",
		"BUILDX_BUILDER=attacker-builder",
		"DOCKER_CONTEXT=attacker-context",
		"DOCKER_HOST=tcp://attacker.invalid:2376",
		"DOCKER_CONFIG=/tmp/docker-config",
	}
	actual := sanitizedBuildxEnvironment(environment)
	wanted := []string{
		"PATH=/usr/bin",
		"DOCKER_CONFIG=/tmp/docker-config",
		"BUILDX_METADATA_PROVENANCE=max",
		"BUILDX_METADATA_WARNINGS=0",
		"BUILDX_NO_DEFAULT_ATTESTATIONS=1",
	}
	if !slices.Equal(actual, wanted) {
		t.Fatalf("buildx environment = %v, want %v", actual, wanted)
	}
}

func TestExecBuildxCommandExecutorReportsActualCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (execBuildxCommandExecutor{}).Run(
		context.Background(), bytes.NewReader(nil), "buildx", "ls", "--format", "{{.Name}}",
	)
	if err == nil || !strings.Contains(err.Error(), "docker buildx ls:") {
		t.Fatalf("buildx list error = %v", err)
	}
}

func validBuildxRequest(t *testing.T) BuildKitBuildRequest {
	t.Helper()
	prepared, err := prepareCandidate(candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.buildRequest
}

func localBuildxSmokeRequest(t *testing.T) BuildKitBuildRequest {
	t.Helper()
	var lock toolchainLock
	if err := decodeStrictJSON(readRepoFile(t, toolchainLockPath), &lock); err != nil {
		t.Fatal(err)
	}
	reference := ""
	for _, image := range lock.BaseImages {
		if image.Name == "GO_IMAGE" {
			reference = image.Reference
			break
		}
	}
	if reference == "" {
		t.Fatal("toolchain lock is missing GO_IMAGE")
	}
	dockerfile := strings.Replace(validCandidateDockerfile(), lockedGoImageReference(), reference, 1)
	entries := candidateEntries(dockerfile)
	replaceEntryText(t, entries, toolchainLockPath, lockedGoImageReference(), reference)
	replaceEntryText(t, entries, toolchainLockPath, "docker.io/moby/buildkit@"+digest("c"), lock.BuildKitImage)
	changeEntry(t, entries, "go.sum", "")
	changeEntry(t, entries, "cmd/super-dolphin-gate/main.go", "package main\n\nfunc main() {}\n")
	refreshRuntimeDepsLock(t, entries)
	bindLocalSmokeRuntimeImages(t, entries)
	prepared, err := prepareCandidate(candidateRequest(entries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.buildRequest
}

func bindLocalSmokeRuntimeImages(t *testing.T, entries []sourceexport.TreeEntry) {
	t.Helper()
	var fixture runtimeDepsLock
	if err := decodeStrictJSON(candidateEntry(t, entries, runtimeDepsLockPath).Data, &fixture); err != nil {
		t.Fatal(err)
	}
	var actual runtimeDepsLock
	if err := decodeStrictJSON(readRepoFile(t, runtimeDepsLockPath), &actual); err != nil {
		t.Fatal(err)
	}
	fixture.Images = actual.Images
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	changeEntry(t, entries, runtimeDepsLockPath, string(data)+"\n")
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
	assertBuildxArgumentsContain(t, args, []string{"--output=type=docker,oci-mediatypes=false", "--progress=quiet", "--provenance=false", "--platform=" + request.Platform, "--file=" + request.DockerfilePath, "--network=none"}, "buildx command")
	assertFixedBuildxTagAndCache(t, args, request)
	assertBuildxArgumentOrder(t, args, request)
	assertIsolatedBuildxCache(t, args, request, root)
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

func assertIsolatedBuildxCache(t *testing.T, args []string, request BuildKitBuildRequest, root string) {
	cacheArgument := "--cache-to=type=local,dest=" + filepath.Join(root, "cache", strings.TrimPrefix(request.CacheNamespace, "sha256:")) + ",mode=min"
	if !slices.Contains(args, cacheArgument) {
		t.Fatalf("buildx cache namespace is not isolated: %v", args)
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

func assertControlledBuildxLifecycle(t *testing.T, calls []buildxCommandCall, request BuildKitBuildRequest) {
	t.Helper()
	if len(calls) != 11 {
		t.Fatalf("controlled buildx lifecycle call count = %d", len(calls))
	}
	name := assertControlledBuilderCreate(t, calls[0].args, request)
	assertExactBuildxCommand(t, calls[1].args, []string{"buildx", "inspect", "--builder", name, "--bootstrap"}, "controlled builder inspect command")
	assertExactBuildxCommand(t, calls[2].args, []string{"container", "update", "--pids-limit", buildxBuilderPidsLimit, controlledBuildxContainerName(name)}, "controlled builder PIDs update command")
	assertExactBuildxCommand(t, calls[3].args, []string{"container", "inspect", "--format", "{{.Config.Image}}\n{{.Image}}\n{{.HostConfig.CpuQuota}}/{{.HostConfig.CpuPeriod}}/{{.HostConfig.Memory}}/{{.HostConfig.PidsLimit}}", controlledBuildxContainerName(name)}, "controlled builder resource inspect command")
	assertExactBuildxCommand(t, calls[4].args, []string{"image", "inspect", "--format", "{{.Id}}\n{{range .RepoDigests}}{{println .}}{{end}}", request.BuildKitImage}, "controlled builder image inspect command")
	assertExactBuildxCommand(t, calls[6].args, []string{"buildx", "history", "inspect", "attachment", "--builder", name, testBuildxHistoryRecordReference, validBuildxManifestDigest(t)}, "controlled build record attachment command")
	assertExactBuildxCommand(t, calls[7].args, []string{"buildx", "rm", "--force", name}, "controlled builder cleanup command")
	assertExactBuildxCommand(t, calls[8].args, []string{"container", "rm", "--force", controlledBuildxContainerName(name)}, "controlled builder container cleanup command")
	assertExactBuildxCommand(t, calls[9].args, []string{"buildx", "ls", "--format", "{{.Name}}"}, "controlled builder absence witness command")
	assertExactBuildxCommand(t, calls[10].args, []string{"container", "ls", "--all", "--filter", "name=^/buildx_buildkit_" + buildxBuilderNamePrefix, "--format", "{{.Names}}"}, "controlled builder container absence witness command")
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
		if len(call.args) >= 2 && call.args[0] == "buildx" && call.args[1] == "build" {
			return call
		}
	}
	t.Fatalf("buildx build command was not recorded: %v", calls)
	return buildxCommandCall{}
}
