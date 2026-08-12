package gate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func localExecutorTestClock() func() time.Time {
	fixed := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return fixed }
}

func TestLocalExecutorTimingUsesInjectedClock(t *testing.T) {
	started := time.Date(2026, time.August, 11, 1, 2, 3, 0, time.UTC)
	bodyStarted := started.Add(11 * time.Millisecond)
	completed := bodyStarted.Add(29 * time.Millisecond)
	times := []time.Time{started, bodyStarted, completed}
	next := 0
	nowFunc := func() time.Time {
		value := times[next]
		next++
		return value
	}

	observation := runLocalExecutorSteps(context.Background(), nowFunc, GateIDWhitespaceCheck, nil, nil, "", "")
	if observation.runErr == nil {
		t.Fatal("zero-step observation unexpectedly succeeded")
	}
	if !observation.started.Equal(started) || !observation.completed.Equal(completed) {
		t.Fatalf("observation timestamps = %s..%s, want %s..%s", observation.started, observation.completed, started, completed)
	}
	if observation.timing == nil || observation.timing.setupMS != 11 || observation.timing.bodyMS != 29 || observation.timing.totalMS != 40 {
		t.Fatalf("observation timing = %#v, want setup=11 body=29 total=40", observation.timing)
	}
}

func TestLocalExecutorSandboxRequiresNetworkIsolation(t *testing.T) {
	path, err := localNetworkSandboxPath()
	if runtime.GOOS == "darwin" {
		if err != nil || path != "/usr/bin/sandbox-exec" {
			t.Fatalf("local sandbox = %q err=%v, want validated macOS sandbox-exec", path, err)
		}
		return
	}
	if err == nil {
		t.Fatal("local sandbox accepted unsupported platform")
	}
}

func TestLocalExecutorPreparationCleansDependenciesAfterInitializationFailure(t *testing.T) {
	causes := []struct {
		name string
		fail func(*localExecutorPreparationHooks, error)
	}{
		{name: "toolchain", fail: func(hooks *localExecutorPreparationHooks, cause error) {
			hooks.toolchain = func() (string, string, error) { return "", "", cause }
		}},
		{name: "steps", fail: func(hooks *localExecutorPreparationHooks, cause error) {
			hooks.steps = func(string, string, ExecutorProgram) ([]resolvedStep, error) { return nil, cause }
		}},
		{name: "environment", fail: func(hooks *localExecutorPreparationHooks, cause error) {
			hooks.environment = func(executorLayout, string, string, string) ([]string, error) { return nil, cause }
		}},
		{name: "profile", fail: func(hooks *localExecutorPreparationHooks, cause error) {
			hooks.profile = func(string, executorLayout, LocalExecutorDependencyInputs, string, []string) (string, error) {
				return "", cause
			}
		}},
	}
	for _, tc := range causes {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New(tc.name + " initialization failed")
			cleanupErr := errors.New("dependency cleanup failed")
			cleanupCalls := 0
			hooks := localExecutorPreparationHooks{
				toolchain: func() (string, string, error) { return "/toolchain", "/bin/go", nil },
				steps: func(string, string, ExecutorProgram) ([]resolvedStep, error) {
					return []resolvedStep{{binary: "/bin/sh"}}, nil
				},
				environment: func(executorLayout, string, string, string) ([]string, error) {
					return []string{"CGO_ENABLED=0"}, nil
				},
				profile: func(string, executorLayout, LocalExecutorDependencyInputs, string, []string) (string, error) {
					return "(profile)", nil
				},
			}
			tc.fail(&hooks, cause)
			_, _, _, _, _, err := prepareLocalExecutorPostDependencies(
				"/source", executorLayout{}, ExecutorProgram{}, LocalExecutorDependencyInputs{}, "/sandbox-exec",
				func() error {
					cleanupCalls++
					return cleanupErr
				}, hooks,
			)
			if err == nil || !errors.Is(err, cause) || !errors.Is(err, cleanupErr) {
				t.Fatalf("preparation error = %v, want initialization and cleanup errors", err)
			}
			if cleanupCalls != 1 {
				t.Fatalf("dependency cleanup calls = %d, want 1", cleanupCalls)
			}
		})
	}
}

func TestLocalExecutorSandboxDeniesNetworkAndDependencyWrites(t *testing.T) {
	fixture := newLocalSandboxTestFixture(t)
	profile, err := localSandboxProfile(fixture.source, fixture.layout, LocalExecutorDependencyInputs{GoModuleCache: fixture.dependency}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"(deny default)", "(deny network*)", "(deny file-write*", fixture.source, fixture.dependency, filepath.Join(fixture.source, ".git")} {
		if !strings.Contains(profile, expected) {
			t.Fatalf("sandbox profile = %q, missing %q", profile, expected)
		}
	}
	if strings.Contains(profile, "(allow file-write* (subpath "+localSandboxString(fixture.dependency)+"))") {
		t.Fatalf("sandbox profile grants dependency write: %q", profile)
	}
}

func TestLocalExecutorSandboxAllowsExactCloneObjectAlternatesReadOnly(t *testing.T) {
	fixture := newLocalSandboxTestFixture(t)
	objectRoot := filepath.Join(canonicalLocalSandboxTempDir(t), "objects")
	if err := os.MkdirAll(filepath.Join(objectRoot, "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	alternates := filepath.Join(fixture.source, ".git", "objects", "info", "alternates")
	if err := os.MkdirAll(filepath.Dir(alternates), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alternates, []byte(objectRoot+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := localSandboxProfile(fixture.source, fixture.layout, LocalExecutorDependencyInputs{}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, "(allow file-read* (subpath "+localSandboxString(objectRoot)+"))") {
		t.Fatalf("sandbox profile does not allow exact-clone object root: %q", profile)
	}
	if strings.Contains(profile, "(allow file-write* (subpath "+localSandboxString(objectRoot)+"))") {
		t.Fatalf("sandbox profile grants exact-clone object root write: %q", profile)
	}
}

func TestLocalExecutorSandboxCanonicalizesExistingAliasRoots(t *testing.T) {
	rawRoot := t.TempDir()
	source := filepath.Join(rawRoot, "source")
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	layout := newExecutorLayout(filepath.Join(rawRoot, "work"))
	if err := makeLocalExecutorDirectories(layout); err != nil {
		t.Fatal(err)
	}
	dependencyRoot := t.TempDir()
	dependency := filepath.Join(dependencyRoot, "dependency")
	if err := os.Mkdir(dependency, 0o700); err != nil {
		t.Fatal(err)
	}
	profile, err := localSandboxProfile(source, layout, LocalExecutorDependencyInputs{GoModuleCache: dependency}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, localSandboxString(canonicalSource)) {
		t.Fatalf("sandbox profile did not use canonical source root %q: %q", canonicalSource, profile)
	}
	if canonicalSource != source && strings.Contains(profile, localSandboxString(source)) {
		t.Fatalf("sandbox profile retained non-canonical source root %q: %q", source, profile)
	}
}

func TestLocalExecutorSandboxWritesOnlyExactTemporaryRoots(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec is the local authority capability")
	}
	fixture := newLocalSandboxTestFixture(t)
	profile, err := localSandboxProfile(fixture.source, fixture.layout, LocalExecutorDependencyInputs{GoModuleCache: fixture.dependency}, "", []string{"/bin/sh", "/usr/bin/nc"})
	if err != nil {
		t.Fatal(err)
	}
	allowedSource := filepath.Join(fixture.source, "allowed.txt")
	allowedSession := filepath.Join(fixture.layout.tmp, "allowed.txt")
	assertSandboxWriteAllowed(t, profile, fixture.source, allowedSource, "allowed-source")
	assertSandboxWriteAllowed(t, profile, fixture.source, allowedSession, "allowed-session")

	externalRoot := canonicalLocalSandboxTempDir(t)
	externalTemp := filepath.Join(externalRoot, "external.txt")
	assertSandboxWriteDenied(t, profile, fixture.source, externalTemp)
	assertSandboxWriteDenied(t, profile, fixture.source, filepath.Join(fixture.source, ".git", "blocked"))
	assertSandboxWriteDenied(t, profile, fixture.source, filepath.Join(fixture.dependency, "blocked"))
	dirtyRepo := filepath.Join(externalRoot, "dirty-repo")
	if err := os.MkdirAll(filepath.Join(dirtyRepo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dirtyRepo, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("test-only-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSandboxReadDenied(t, profile, fixture.source, secretPath)

	assertSandboxNetworkDenied(t, profile, fixture.source, []string{"-z", "127.0.0.1", "9"})
	assertSandboxNetworkDenied(t, profile, fixture.source, []string{"-l", "127.0.0.1", "45678"})
}

func TestLocalExecutorSessionReattachesFrontendOverlaysAfterRestore(t *testing.T) {
	fixture := newLocalExecutorFrontendOverlayFixture(t)
	program := ExecutorProgram{NeedsFrontendSeed: true, NeedsFrontendEmbedSeed: true}
	dependencies := LocalExecutorDependencyInputs{
		FrontendNodeModules: fixture.nodeModules,
		FrontendNPMCache:    fixture.npmCache,
		FrontendViteCache:   fixture.viteCache,
		FrontendEmbedRoot:   fixture.embedRoot,
	}
	for workload := range 2 {
		if err := reattachLocalExecutorSessionDependencies(fixture.source, fixture.layout, program, dependencies); err != nil {
			t.Fatalf("frontend overlay workload %d: %v", workload, err)
		}
		if _, err := os.Lstat(filepath.Join(fixture.source, "frontend-app", "node_modules")); err != nil {
			t.Fatalf("frontend node_modules workload %d: %v", workload, err)
		}
		if _, err := os.Stat(filepath.Join(fixture.source, "cmd", "agent-terminal", "web-dist", "index.html")); err != nil {
			t.Fatalf("frontend embed workload %d: %v", workload, err)
		}
		if err := cleanupLocalExecutorOverlayTargets([]string{filepath.Join(fixture.source, "frontend-app", "node_modules"), filepath.Join(fixture.source, "cmd", "agent-terminal", "web-dist")}); err != nil {
			t.Fatalf("restore exact tree after workload %d: %v", workload, err)
		}
	}
}

func TestLocalExecutorSandboxDependencyClosureIgnoresUnrelatedCacheFiles(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	dependency := filepath.Join(root, "go-mod-cache")
	if err := os.Mkdir(dependency, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := LocalExecutorDependencyInputs{GoModuleCache: dependency, GoModuleCacheReceipt: digestForWorkloadPass("go-module-receipt")}
	first, err := LocalExecutorDependencyClosureDigest(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "unrelated-cache-file"), []byte("cache drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := LocalExecutorDependencyClosureDigest(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("unrelated dependency cache changed closure digest: %q != %q", first, second)
	}
}

func TestLocalExecutorPreparationFailureCleansDependencies(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec is the local authority capability")
	}
	fixture := newLocalExecutorPreparationFailureFixture(t)
	program := ExecutorProgram{Strategy: ExecutorStrategyCommands, NeedsFrontendSeed: true, Steps: []ExecutorStep{{}}}
	dependencies := LocalExecutorDependencyInputs{
		FrontendNodeModules: fixture.nodeModules,
		FrontendNPMCache:    fixture.npmCache,
		FrontendViteCache:   fixture.viteCache,
	}
	_, _, _, _, cleanup, err := prepareLocalExecutorExecution(fixture.source, fixture.layout, program, dependencies)
	if err == nil || !strings.Contains(err.Error(), "local executor program is invalid") {
		t.Fatalf("prepare error = %v, want fail-fast local program validation", err)
	}
	if cleanup != nil {
		t.Fatal("failed preparation returned a cleanup callback after already cleaning dependencies")
	}
	for _, target := range []string{filepath.Join(fixture.source, "frontend-app", "node_modules"), filepath.Join(fixture.layout.tmp, ".vite-temp")} {
		if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
			t.Fatalf("failed preparation leaked dependency overlay %q: %v", target, statErr)
		}
	}
}

func TestLocalExecutorPreparationErrorJoinsCleanupError(t *testing.T) {
	cause := errors.New("initialization failed")
	cleanup := errors.New("dependency cleanup failed")
	called := false
	joined := joinLocalExecutorPreparationError(cause, func() error {
		called = true
		return cleanup
	})
	if !called {
		t.Fatal("dependency cleanup was not invoked")
	}
	if !errors.Is(joined, cause) || !errors.Is(joined, cleanup) {
		t.Fatalf("joined preparation error = %v, want both causes", joined)
	}
}

func TestLocalExecutorRunnerSemanticDigestIgnoresAbsoluteSourceRoots(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS sandbox-exec is the local authority capability")
	}
	root := canonicalLocalSandboxTempDir(t)
	digests := make([]string, 0, 2)
	for _, name := range []string{"source-a", "source-b"} {
		source := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(source, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(source, "scripts"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, script := range []string{"test_with_guard.sh", "check_nested_go_modules.sh"} {
			if err := os.WriteFile(filepath.Join(source, "scripts", script), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		digest, err := LocalExecutorRunnerSemanticDigest(source, GateIDBackendTestWithGuard)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	if digests[0] != digests[1] {
		t.Fatalf("runner semantic digest depends on absolute source root: %q != %q", digests[0], digests[1])
	}
}

func TestLocalExecutorDependencyClosureAndPassEnvironmentIgnoreAbsoluteRoots(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	dependencyRoots := []string{filepath.Join(root, "dependency-a"), filepath.Join(root, "dependency-b")}
	closures := make([]string, 0, 2)
	receipt := digestForWorkloadPass("same-dependency-receipt")
	for _, dependencyRoot := range dependencyRoots {
		if err := os.MkdirAll(dependencyRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		closure, err := LocalExecutorDependencyClosureDigest(LocalExecutorDependencyInputs{GoModuleCache: dependencyRoot, GoModuleCacheReceipt: receipt})
		if err != nil {
			t.Fatal(err)
		}
		closures = append(closures, closure)
	}
	if closures[0] != closures[1] {
		t.Fatalf("dependency closure digest depends on absolute root: %q != %q", closures[0], closures[1])
	}
	environments := make([]LocalWorkloadPassEnvironment, 0, 2)
	for _, closure := range closures {
		environments = append(environments, LocalWorkloadPassEnvironment{
			Platform:                 runtime.GOOS + "/" + runtime.GOARCH,
			GOOS:                     runtime.GOOS,
			GOARCH:                   runtime.GOARCH,
			CGOEnabled:               "0",
			GoFlags:                  CanonicalGoFlags(false),
			ToolchainClosureDigest:   closure,
			RunnerSemanticPolicy:     LocalWorkloadRunnerSemanticPolicy,
			BaseRunnerSemanticDigest: digestForWorkloadPass("same-runner"),
			RunnerSemanticDigest:     digestForWorkloadPass("same-runner"),
		})
	}
	passDigests := make([]string, 0, 2)
	for _, environment := range environments {
		digest, err := LocalWorkloadPassEnvironmentDigest(environment)
		if err != nil {
			t.Fatal(err)
		}
		passDigests = append(passDigests, digest)
	}
	if passDigests[0] != passDigests[1] {
		t.Fatalf("local PASS environment identity depends on absolute dependency root: %q != %q", passDigests[0], passDigests[1])
	}
}

func TestLocalExecutorRunnerSemanticDigestTracksToolContentWithoutPaths(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	firstPath := filepath.Join(root, "root-a", "tool")
	secondPath := filepath.Join(root, "root-b", "tool")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("tool-v1"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	first, err := digestLocalRunnerPaths([]string{firstPath})
	if err != nil {
		t.Fatal(err)
	}
	second, err := digestLocalRunnerPaths([]string{secondPath})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("runner semantic digest depends on absolute tool path: %q != %q", first, second)
	}
	if err := os.WriteFile(secondPath, []byte("tool-v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	drifted, err := digestLocalRunnerPaths([]string{secondPath})
	if err != nil {
		t.Fatal(err)
	}
	if drifted == first {
		t.Fatalf("runner semantic digest ignored tool content drift: %q", drifted)
	}
}

func TestLocalExecutorSearchPathDoesNotInheritFakeHostShadow(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	fake := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fake, "go"), []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	searchPath := localExecutorSearchPath("/trusted/bin")
	if strings.Contains(searchPath, fake) || strings.Contains(searchPath, os.Getenv("PATH")) {
		t.Fatalf("local executor search path inherited host shadow: %q", searchPath)
	}
	if _, err := resolveExecutable("go", searchPath); err == nil {
		t.Fatal("local executor search path unexpectedly resolved a fake host shadow")
	}
}

type localExecutorFrontendOverlayFixture struct {
	source      string
	layout      executorLayout
	nodeModules string
	npmCache    string
	viteCache   string
	embedRoot   string
}

func newLocalExecutorFrontendOverlayFixture(t *testing.T) localExecutorFrontendOverlayFixture {
	t.Helper()
	root := canonicalLocalSandboxTempDir(t)
	source := filepath.Join(root, "source")
	mustLocalExecutorMkdirAll(t, filepath.Join(source, "frontend-app"))
	mustLocalExecutorMkdirAll(t, filepath.Join(source, "cmd", "agent-terminal"))
	layout := newExecutorLayout(filepath.Join(root, "work"))
	mustLocalExecutorDirectories(t, layout)
	nodeModules := filepath.Join(root, "node-modules")
	npmCache := filepath.Join(root, "npm-cache")
	viteCache := filepath.Join(root, "vite-cache")
	embedRoot := filepath.Join(root, "embed")
	for _, path := range []string{nodeModules, npmCache, viteCache, embedRoot} {
		mustLocalExecutorMkdirAll(t, path)
	}
	mustLocalExecutorWriteFile(t, filepath.Join(nodeModules, "tool.js"), "tool")
	mustLocalExecutorMkdir(t, filepath.Join(viteCache, "deps"))
	mustLocalExecutorWriteFile(t, filepath.Join(embedRoot, "index.html"), "embed")
	return localExecutorFrontendOverlayFixture{
		source: source, layout: layout, nodeModules: nodeModules,
		npmCache: npmCache, viteCache: viteCache, embedRoot: embedRoot,
	}
}

type localExecutorPreparationFailureFixture struct {
	source      string
	layout      executorLayout
	nodeModules string
	npmCache    string
	viteCache   string
}

func newLocalExecutorPreparationFailureFixture(t *testing.T) localExecutorPreparationFailureFixture {
	t.Helper()
	root := canonicalLocalSandboxTempDir(t)
	source := filepath.Join(root, "source")
	mustLocalExecutorMkdirAll(t, filepath.Join(source, "frontend-app"))
	layout := newExecutorLayout(filepath.Join(root, "work"))
	mustLocalExecutorDirectories(t, layout)
	dependencyRoot := filepath.Join(root, "dependencies")
	nodeModules := filepath.Join(dependencyRoot, "node-modules")
	npmCache := filepath.Join(dependencyRoot, "npm-cache")
	viteCache := filepath.Join(dependencyRoot, "vite-cache")
	for _, path := range []string{nodeModules, npmCache, filepath.Join(viteCache, "deps")} {
		mustLocalExecutorMkdirAll(t, path)
	}
	mustLocalExecutorWriteFile(t, filepath.Join(nodeModules, "tool.js"), "tool")
	return localExecutorPreparationFailureFixture{
		source: source, layout: layout, nodeModules: nodeModules,
		npmCache: npmCache, viteCache: viteCache,
	}
}

func mustLocalExecutorMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustLocalExecutorMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustLocalExecutorDirectories(t *testing.T, layout executorLayout) {
	t.Helper()
	if err := makeLocalExecutorDirectories(layout); err != nil {
		t.Fatal(err)
	}
}

func mustLocalExecutorWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type localSandboxTestFixture struct {
	source     string
	layout     executorLayout
	dependency string
}

func newLocalSandboxTestFixture(t *testing.T) localSandboxTestFixture {
	t.Helper()
	root := canonicalLocalSandboxTempDir(t)
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	layout := newExecutorLayout(filepath.Join(root, "work"))
	if err := makeLocalExecutorDirectories(layout); err != nil {
		t.Fatal(err)
	}
	dependencyRoot := canonicalLocalSandboxTempDir(t)
	dependency := filepath.Join(dependencyRoot, "dependency")
	if err := os.Mkdir(dependency, 0o700); err != nil {
		t.Fatal(err)
	}
	return localSandboxTestFixture{source: source, layout: layout, dependency: dependency}
}

func canonicalLocalSandboxTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertSandboxWriteAllowed(t *testing.T, profile, directory, target, content string) {
	t.Helper()
	command := sandboxShellCommand(profile, directory, "printf %s \"$LOCAL_CONTENT\" > \"$LOCAL_TARGET\"", map[string]string{"LOCAL_TARGET": target, "LOCAL_CONTENT": content})
	if err := command.Run(); err != nil {
		t.Fatalf("sandbox allowed write %q: %v", target, err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != content {
		t.Fatalf("sandbox allowed write %q = %q err=%v", target, actual, err)
	}
}

func assertSandboxWriteDenied(t *testing.T, profile, directory, target string) {
	t.Helper()
	_ = os.Remove(target)
	command := sandboxShellCommand(profile, directory, "printf denied > \"$LOCAL_TARGET\"", map[string]string{"LOCAL_TARGET": target})
	if err := command.Run(); err == nil {
		t.Fatalf("sandbox write %q unexpectedly succeeded", target)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("sandbox denied write %q created target: %v", target, err)
	}
}

func assertSandboxReadDenied(t *testing.T, profile, directory, target string) {
	t.Helper()
	command := sandboxShellCommand(profile, directory, "cat \"$LOCAL_TARGET\" >/dev/null", map[string]string{"LOCAL_TARGET": target})
	if err := command.Run(); err == nil {
		t.Fatalf("sandbox read %q unexpectedly succeeded", target)
	}
}

func assertSandboxNetworkDenied(t *testing.T, profile, directory string, args []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", profile, "/usr/bin/nc"}, args...)...)
	command.Dir = directory
	command.Env = []string{"PATH=/usr/bin:/bin"}
	if err := command.Run(); err == nil {
		t.Fatalf("sandbox network operation %v unexpectedly succeeded", args)
	}
}

func sandboxShellCommand(profile, directory, script string, values map[string]string) *exec.Cmd {
	args := []string{"-p", profile, "/bin/sh", "-c", script}
	command := exec.Command("/usr/bin/sandbox-exec", args...)
	command.Dir = directory
	command.Env = []string{"PATH=/usr/bin:/bin"}
	for key, value := range values {
		command.Env = append(command.Env, key+"="+value)
	}
	return command
}

func TestCanonicalLocalExecutorSourceRootGOTMPDIRBoundaries(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	defaultTempRoot := filepath.Join(root, "default-temp")
	goTempRoot := filepath.Join(root, "go-temp")
	mustLocalExecutorMkdirAll(t, defaultTempRoot)
	mustLocalExecutorMkdirAll(t, goTempRoot)
	t.Setenv("TMPDIR", defaultTempRoot)

	t.Setenv("GOTMPDIR", "")
	defaultSource := makeLocalExecutorSourceRootTestFixture(t, filepath.Join(defaultTempRoot, "default-source"))
	assertCanonicalLocalExecutorSourceRoot(t, defaultSource, "default os.TempDir child")

	t.Setenv("GOTMPDIR", goTempRoot)
	goChild := makeLocalExecutorSourceRootTestFixture(t, filepath.Join(goTempRoot, "child"))
	assertCanonicalLocalExecutorSourceRoot(t, goChild, "GOTMPDIR child")

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "root", path: makeLocalExecutorSourceRootTestFixture(t, goTempRoot)},
		{name: "sibling", path: makeLocalExecutorSourceRootTestFixture(t, filepath.Join(root, "go-temp-sibling"))},
		{name: "prefix", path: makeLocalExecutorSourceRootTestFixture(t, goTempRoot+"-prefix")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertLocalExecutorSourceRootRejected(t, tc.path, "source root boundary")
		})
	}

	fallbackSource := makeLocalExecutorSourceRootTestFixture(t, filepath.Join(defaultTempRoot, "fallback-source"))
	symlinkPath := filepath.Join(root, "go-temp-link")
	mustLocalExecutorSymlink(t, goTempRoot, symlinkPath)
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "relative", value: "relative/go-temp"},
		{name: "unclean", value: goTempRoot + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(goTempRoot)},
		{name: "nonexistent", value: filepath.Join(root, "missing-go-temp")},
		{name: "symlink-escape", value: symlinkPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOTMPDIR", tc.value)
			assertLocalExecutorSourceRootRejected(t, fallbackSource, "invalid GOTMPDIR")
		})
	}
}

func makeLocalExecutorSourceRootTestFixture(t *testing.T, path string) string {
	t.Helper()
	mustLocalExecutorMkdirAll(t, filepath.Join(path, ".git"))
	return path
}

func assertCanonicalLocalExecutorSourceRoot(t *testing.T, sourceRoot, label string) {
	t.Helper()
	got, err := canonicalLocalExecutorSourceRoot(sourceRoot)
	want, evalErr := filepath.EvalSymlinks(sourceRoot)
	if err != nil || evalErr != nil || got != want {
		t.Fatalf("%s = %q, eval=%q, err=%v, evalErr=%v; want canonical child", label, got, want, err, evalErr)
	}
}

func assertLocalExecutorSourceRootRejected(t *testing.T, sourceRoot, label string) {
	t.Helper()
	if _, err := canonicalLocalExecutorSourceRoot(sourceRoot); err == nil {
		t.Fatalf("%s %q unexpectedly accepted", label, sourceRoot)
	}
}

func mustLocalExecutorSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestLocalExecutorBuiltInStrategiesAreExplicitlyIneligible(t *testing.T) {
	ids := []GateID{
		GateIDSQLCVerify,
		GateIDWhitespaceCheck,
		GateIDReleaseLayeredCheck,
	}
	for _, id := range ids {
		t.Run(string(id), func(t *testing.T) {
			assertLocalExecutorBuiltInStrategyIneligible(t, id)
		})
	}
}

func assertLocalExecutorBuiltInStrategyIneligible(t *testing.T, id GateID) {
	t.Helper()
	_, program, err := executorProgramForWorkload(id)
	if err != nil {
		t.Fatalf("executorProgramForWorkload(%q): %v", id, err)
	}
	if program.Strategy == ExecutorStrategyCommands || len(program.Steps) != 0 {
		t.Fatalf("built-in program = %#v, want zero-step non-command strategy", program)
	}
	if err := validateLocalExecutorProgramSupport(program); err == nil || !strings.Contains(err.Error(), "ineligible") {
		t.Fatalf("local built-in support error = %v, want explicit ineligible failure", err)
	}
	assertLocalExecutorZeroStepDoesNotPass(t, id, program)
}

func assertLocalExecutorZeroStepDoesNotPass(t *testing.T, id GateID, program ExecutorProgram) {
	t.Helper()
	observation := runLocalExecutorSteps(context.Background(), localExecutorTestClock(), id, nil, nil, "", "")
	if observation.runErr == nil {
		t.Fatal("zero-step built-in execution returned nil error")
	}
	if len(observation.log) != 0 {
		t.Fatalf("zero-step built-in execution log = %q, want no command output", observation.log)
	}
	started := time.Now().UTC()
	result, finishErr := finishLocalGateExecution(id, program, localExecutorObservation{
		started:   started,
		completed: started.Add(time.Millisecond),
		timing:    &executorExecutionTiming{setupMS: 1, bodyMS: 1, totalMS: 2},
		runErr:    observation.runErr,
	}, nil)
	if finishErr == nil {
		t.Fatal("zero-step built-in finish returned nil error")
	}
	if result.Status == ResultStatusPassed || result.ExitCode == 0 {
		t.Fatalf("zero-step built-in result = status=%s exit=%d, must not pass", result.Status, result.ExitCode)
	}
}

func TestLocalExecutorSupportKeepsNormalCommandProgramsEligible(t *testing.T) {
	program := ExecutorProgram{
		Strategy: ExecutorStrategyCommands,
		Steps:    []ExecutorStep{{Argv: []string{"true"}}},
	}
	if err := validateLocalExecutorProgramSupport(program); err != nil {
		t.Fatalf("normal command program support error = %v", err)
	}
}

func TestLocalExecutorReceiptProgramsBindMappedIneligibleWorkloadsButSessionsReject(t *testing.T) {
	workloadIDs := []GateID{GateIDSQLCVerify, GateIDWhitespaceCheck, GateIDReleaseLayeredCheck}
	programs, err := resolveLocalExecutorReceiptPrograms(workloadIDs)
	if err != nil {
		t.Fatalf("receipt program preflight error = %v", err)
	}
	environment := localPassTestEnvironment(false)
	host := LocalWorkloadPassHostContext{Platform: environment.Platform, GOOS: environment.GOOS, GOARCH: environment.GOARCH, CGOEnabled: environment.CGOEnabled, ToolchainClosureDigest: environment.ToolchainClosureDigest, RunnerSemanticPolicy: environment.RunnerSemanticPolicy, RunnerSemanticDigest: environment.BaseRunnerSemanticDigest}
	environments, err := localReceiptEnvironments(host, programs, TrustedSelfBinary{})
	if err != nil {
		t.Fatalf("receipt environments error = %v", err)
	}
	receipt := newLocalSchedulerTestReceipt(environments)
	for _, workloadID := range workloadIDs {
		if _, ok := environments[workloadID]; !ok {
			t.Fatalf("receipt has no environment for mapped ineligible workload %q", workloadID)
		}
		if _, err := NewLocalExecutorSessionWithReceipt(t.TempDir(), localExecutorTestClock(), []GateID{workloadID}, LocalExecutorDependencyInputs{}, receipt); err == nil || !strings.Contains(err.Error(), "ineligible") {
			t.Fatalf("session for mapped ineligible workload %q error = %v, want strict ineligible rejection", workloadID, err)
		}
		if _, err := ExecuteLocalGateWorkloadWithDependencies(context.Background(), localExecutorTestClock(), t.TempDir(), workloadID, LocalExecutorDependencyInputs{}); err == nil || !strings.Contains(err.Error(), "ineligible") {
			t.Fatalf("direct execution for mapped ineligible workload %q error = %v, want zero-step ineligible rejection", workloadID, err)
		}
	}
}

func TestLocalExecutorFrontendProgramsAreExplicitlyIneligible(t *testing.T) {
	program := ExecutorProgram{
		Strategy:          ExecutorStrategyCommands,
		Steps:             []ExecutorStep{{Argv: []string{"true"}}},
		NeedsFrontendSeed: true,
	}
	if err := validateLocalExecutorProgramSupport(program); err == nil || !strings.Contains(err.Error(), "explicitly ineligible") {
		t.Fatalf("frontend local executor support error = %v, want explicit ineligible failure", err)
	}
}
