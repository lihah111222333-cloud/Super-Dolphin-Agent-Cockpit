package gate

import (
	"context"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverLocalExecutorDependencyInputsUsesGateOwnedTrustedGo(t *testing.T) {
	cacheRoot := t.TempDir()
	shadowRoot := t.TempDir()
	shadowGo := filepath.Join(shadowRoot, "go")
	if err := os.WriteFile(shadowGo, []byte("not a Go binary"), 0o700); err != nil {
		t.Fatalf("write shadow go: %v", err)
	}
	t.Setenv("PATH", shadowRoot)
	var requestedBinary string
	hooks := localExecutorDependencyInputsDiscoveryHooks{
		eligibility: EvaluateLocalWorkloadExecutionEligibility,
		program:     executorProgramForWorkload,
		trustedGoBinary: func() (string, error) {
			return "/gate-owned/trusted/go", nil
		},
		goEnvironment: func(_ context.Context, binary string) (localExecutorTrustedGoEnvironment, error) {
			requestedBinary = binary
			return localExecutorTrustedGoEnvironment{GoModuleCache: cacheRoot, CGOEnabled: "1"}, nil
		},
		canonicalDependencyRoot: canonicalLocalSandboxPath,
		frontendEmbedRoot:       func() (string, error) { return cacheRoot, nil },
	}
	canonicalCacheRoot, err := canonicalLocalSandboxPath(cacheRoot, "test cache root")
	if err != nil {
		t.Fatalf("canonical test cache root: %v", err)
	}
	inputs, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{GateIDCodemapCheck}, hooks)
	if err != nil {
		t.Fatalf("discover local executor dependency inputs: %v", err)
	}
	if requestedBinary != "/gate-owned/trusted/go" {
		t.Fatalf("trusted Go binary = %q, want gate-owned binary", requestedBinary)
	}
	if inputs.GoModuleCache != canonicalCacheRoot || inputs.CGOEnabled != "1" {
		t.Fatalf("dependency inputs = %#v", inputs)
	}
	requireNoCallerReceipts(t, inputs)
	again, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{GateIDCodemapCheck}, hooks)
	if err != nil {
		t.Fatalf("discover stable dependency inputs: %v", err)
	}
	requireStableLocalExecutorDependencyInputs(t, inputs, again)
}

func TestLocalExecutorTrustedGoRootCanonicalizesBuildContextRoot(t *testing.T) {
	wantRoot, err := filepath.EvalSymlinks(build.Default.GOROOT)
	if err != nil {
		t.Fatalf("canonicalize build-context Go root: %v", err)
	}
	wantRoot = filepath.Clean(wantRoot)

	root, err := localExecutorTrustedGoRoot()
	if err != nil {
		t.Fatalf("resolve gate-owned trusted Go root: %v", err)
	}
	if root != wantRoot {
		t.Fatalf("gate-owned trusted Go root = %q, want canonical build-context root %q", root, wantRoot)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat gate-owned trusted Go root: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("gate-owned trusted Go root %q is not a directory", root)
	}
}

func requireNoCallerReceipts(t *testing.T, inputs LocalExecutorDependencyInputs) {
	t.Helper()
	if inputs.GoModuleCacheReceipt != "" || inputs.FrontendDependencyReceipt != "" || inputs.FrontendEmbedReceipt != "" {
		t.Fatalf("factory must not accept caller receipt strings: %#v", inputs)
	}
}

func requireStableLocalExecutorDependencyInputs(t *testing.T, first, second LocalExecutorDependencyInputs) {
	t.Helper()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("dependency inputs are unstable: first=%#v second=%#v", first, second)
	}
}

func TestDiscoverLocalExecutorDependencyInputsRejectsInvalidCacheRoots(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	linkedTarget := t.TempDir()
	linked := filepath.Join(t.TempDir(), "cache-link")
	if err := os.Symlink(linkedTarget, linked); err != nil {
		t.Fatalf("create cache symlink: %v", err)
	}
	for name, cacheRoot := range map[string]string{
		"missing":    missing,
		"relative":   "relative/cache",
		"broad-root": "/tmp",
		"symlink":    linked,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{GateIDCodemapCheck}, localExecutorDependencyInputsTestHooks(cacheRoot, "1"))
			if err == nil {
				t.Fatal("discover local executor dependency inputs unexpectedly succeeded")
			}
		})
	}
}

func TestDiscoverLocalExecutorDependencyInputsRejectsIneligibleAndZeroStepWorkloads(t *testing.T) {
	t.Parallel()
	t.Run("frontend", func(t *testing.T) {
		t.Parallel()
		_, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{GateIDFrontendLint}, localExecutorDependencyInputsTestHooks(t.TempDir(), "1"))
		if err == nil {
			t.Fatal("frontend workload unexpectedly accepted")
		}
	})
	t.Run("known unmapped", func(t *testing.T) {
		t.Parallel()
		_, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{GateID("backend:nilness::go-package::example.com/project/pkg")}, localExecutorDependencyInputsTestHooks(t.TempDir(), "1"))
		if err == nil {
			t.Fatal("known unmapped workload unexpectedly accepted")
		}
	})
	t.Run("zero step", func(t *testing.T) {
		t.Parallel()
		hooks := localExecutorDependencyInputsTestHooks(t.TempDir(), "0")
		hooks.eligibility = func(id GateID) (LocalWorkloadExecutionEligibility, error) {
			return LocalWorkloadExecutionEligibility{WorkloadID: id, CanonicalID: id, Strategy: ExecutorStrategyCommands, Eligible: true}, nil
		}
		hooks.program = func(id GateID) (GateID, ExecutorProgram, error) {
			return id, ExecutorProgram{Strategy: ExecutorStrategyCommands}, nil
		}
		_, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{GateID("test:zero-step")}, hooks)
		if err == nil {
			t.Fatal("zero-step workload unexpectedly accepted")
		}
	})
}

func TestDiscoverLocalExecutorDependencyInputsBindsCGOWithoutGoSeedAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	t.Run("no Go seed", func(t *testing.T) {
		t.Parallel()
		inputs, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{GateIDProjectMapCheck}, localExecutorDependencyInputsTestHooks("", "0"))
		if err != nil {
			t.Fatalf("discover no-Go dependency inputs: %v", err)
		}
		if inputs.GoModuleCache != "" || inputs.CGOEnabled != "0" {
			t.Fatalf("no-Go dependency inputs = %#v", inputs)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		hooks := localExecutorDependencyInputsTestHooks(t.TempDir(), "1")
		hooks.goEnvironment = func(context.Context, string) (localExecutorTrustedGoEnvironment, error) {
			calls++
			return localExecutorTrustedGoEnvironment{}, errors.New("must not run")
		}
		_, err := discoverLocalExecutorDependencyInputs(ctx, []GateID{GateIDCodemapCheck}, hooks)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled discover error = %v, want context.Canceled", err)
		}
		if calls != 0 {
			t.Fatalf("go environment calls = %d, want 0", calls)
		}
	})
}

func TestDiscoverLocalExecutorDependencyInputsBindsEmbedForBackendAndRace(t *testing.T) {
	embedRoot := t.TempDir()
	cacheRoot := t.TempDir()
	for _, id := range []GateID{GateIDBackendTestWithGuard, GateIDBackendTestGuardWithRace} {
		t.Run(string(id), func(t *testing.T) {
			hooks := localExecutorDependencyInputsTestHooks(cacheRoot, "1")
			hooks.frontendEmbedRoot = func() (string, error) { return embedRoot, nil }
			inputs, err := discoverLocalExecutorDependencyInputs(context.Background(), []GateID{id}, hooks)
			if err != nil {
				t.Fatalf("discover %s dependencies: %v", id, err)
			}
			canonicalEmbedRoot, err := canonicalLocalSandboxPath(embedRoot, "test frontend embed root")
			if err != nil {
				t.Fatal(err)
			}
			if inputs.FrontendEmbedRoot != canonicalEmbedRoot {
				t.Fatalf("%s frontend embed root = %q, want gate-owned %q", id, inputs.FrontendEmbedRoot, canonicalEmbedRoot)
			}
		})
	}
}

func localExecutorDependencyInputsTestHooks(cacheRoot, cgoEnabled string) localExecutorDependencyInputsDiscoveryHooks {
	return localExecutorDependencyInputsDiscoveryHooks{
		eligibility: EvaluateLocalWorkloadExecutionEligibility,
		program:     executorProgramForWorkload,
		trustedGoBinary: func() (string, error) {
			return "/gate-owned/trusted/go", nil
		},
		goEnvironment: func(context.Context, string) (localExecutorTrustedGoEnvironment, error) {
			return localExecutorTrustedGoEnvironment{GoModuleCache: cacheRoot, CGOEnabled: cgoEnabled}, nil
		},
		canonicalDependencyRoot: canonicalLocalSandboxPath,
		frontendEmbedRoot:       func() (string, error) { return cacheRoot, nil },
	}
}
