package main

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestProductionProvisionLauncherTargetsFixedCurrentCLI(t *testing.T) {
	t.Parallel()
	data, err := productionProvisionLauncherData("/private/bin/super-dolphin-gate", "/private/install/production.json", "/private/install/bootstrap-controller")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{".super-dolphin-gate-current", "_production-update", "_production-launcher"} {
		if !strings.Contains(text, want) {
			t.Fatalf("launcher missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "bootstrap-controller\" _production-launcher") {
		t.Fatalf("launcher retains versioned controller target:\n%s", text)
	}
}

func TestProductionLauncherDoesNotUseSelfUpdateMainInterception(t *testing.T) {
	t.Parallel()
	if !isProductionSelfUpdateCommand([]string{"_production-update"}) {
		t.Fatal("update command was not intercepted")
	}
	if isProductionSelfUpdateCommand([]string{"_production-launcher", "submit"}) {
		t.Fatal("production launcher was intercepted before its existing dispatcher")
	}
}

func TestProductionUpdateIgnoresLauncherArguments(t *testing.T) {
	t.Parallel()
	repository, err := productionSelfUpdateRepository([]string{"submit", "--repo", "/candidate-worktree"})
	if err != nil || repository != "" {
		t.Fatalf("repository=%q err=%v", repository, err)
	}
}

func TestParseProductionCLIIdentityRejectsDynamicUnknownField(t *testing.T) {
	t.Parallel()
	_, err := parseProductionCLIIdentity([]byte("gate_source_sha256=sha256:" + strings.Repeat("a", 64) + "\nplatform=" + runtimePlatform() + "\ntoolchain_digest=sha256:" + strings.Repeat("b", 64) + "\nextra=drift\n"))
	if err == nil || !strings.Contains(err.Error(), "unexpected fields") {
		t.Fatalf("err = %v, want unknown field rejection", err)
	}
}

func TestMaterializeProductionCLIEntriesRejectsTraversal(t *testing.T) {
	t.Parallel()
	err := materializeProductionCLIEntries(t.TempDir(), []sourceexport.TreeEntry{{Path: "../outside", Mode: "100644", Data: []byte("x")}})
	if err == nil {
		t.Fatal("materialize unsafe entry succeeded")
	}
}

func TestProductionCLIIdentityRejectsMalformedDigest(t *testing.T) {
	t.Parallel()
	err := validateProductionCLIIdentity("sha256:"+strings.Repeat("x", 64), "sha256:"+strings.Repeat("b", 64))
	if err == nil {
		t.Fatal("malformed digest succeeded")
	}
}

func TestProductionCurrentIdentityRequiresExactFields(t *testing.T) {
	t.Parallel()
	output := []byte("gate_source_sha256=sha256:" + strings.Repeat("a", 64) + "\nplatform=" + runtimePlatform() + "\ntoolchain_digest=sha256:" + strings.Repeat("b", 64) + "\n")
	matched, err := productionCurrentIdentityMatches(context.Background(), "binary", "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), func(context.Context, string, []string, string, []string) ([]byte, error) { return output, nil })
	if err != nil || !matched {
		t.Fatalf("matched=%t err=%v", matched, err)
	}
}

func TestBuildProductionCLICandidateVerifiesModulesWithSharedCache(t *testing.T) {
	t.Parallel()
	var calls [][]string
	var environments [][]string
	deps := productionSelfUpdateDeps{
		run: func(_ context.Context, _ string, args []string, _ string, environment []string) ([]byte, error) {
			calls = append(calls, append([]string(nil), args...))
			environments = append(environments, append([]string(nil), environment...))
			if len(args) > 0 && args[0] == "build" {
				for index, argument := range args {
					if argument == "-o" && index+1 < len(args) {
						if err := os.WriteFile(args[index+1], []byte("candidate"), 0o600); err != nil {
							return nil, err
						}
					}
				}
			}
			return nil, nil
		},
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	directory := t.TempDir()
	_, cleanup, err := buildProductionCLICandidate(
		context.Background(),
		directory,
		digest,
		digest,
		[]sourceexport.TreeEntry{{Path: "go.mod", Mode: "100644", Data: []byte("module example.invalid/gate\n")}},
		productionGoToolchain{
			Executable: "/trusted/go/bin/go", GoRoot: "/trusted/go",
			GoPath: "/shared/gopath", GoCache: "/shared/cache", GoModCache: "/shared/mod",
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		},
		deps,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cleanup() }()
	_, secondCleanup, err := buildProductionCLICandidate(
		context.Background(),
		directory,
		digest,
		digest,
		[]sourceexport.TreeEntry{{Path: "go.mod", Mode: "100644", Data: []byte("module example.invalid/gate\n")}},
		productionGoToolchain{
			Executable: "/trusted/go/bin/go", GoRoot: "/trusted/go",
			GoPath: "/shared/gopath", GoCache: "/shared/cache", GoModCache: "/shared/mod",
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		},
		deps,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secondCleanup() }()
	if len(calls) != 4 || !slices.Equal(calls[0], []string{"mod", "verify"}) ||
		len(calls[1]) == 0 || calls[1][0] != "build" ||
		!slices.Equal(calls[2], []string{"mod", "verify"}) || len(calls[3]) == 0 || calls[3][0] != "build" {
		t.Fatalf("build calls = %#v, want module verification before build", calls)
	}
	for _, environment := range environments {
		joined := "\n" + strings.Join(environment, "\n") + "\n"
		if !strings.Contains(joined, "\nGOCACHE=/shared/cache\n") {
			t.Fatalf("build environment did not retain shared GOCACHE: %q", environment)
		}
		if !strings.Contains(joined, "\nGOENV=off\n") {
			t.Fatalf("build environment did not disable GOENV: %q", environment)
		}
	}
}

func TestPublishProductionCurrentCLIRetainsPrevious(t *testing.T) {
	t.Parallel()
	fixture := newProductionSwitchFixture(t)
	if err := switchProductionCurrentCLI(
		fixture.candidate,
		fixture.current,
		fixture.statePath,
		fixture.state,
		liveProductionSwitchOps(),
	); err != nil {
		t.Fatal(err)
	}
	fixture.assertNewCurrent(t)
	fixture.assertOldCurrent(t, fixture.previous)
	loaded, err := loadProductionSelfUpdateState(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != fixture.state {
		t.Fatalf("state = %#v, want %#v", loaded, fixture.state)
	}
}

func runtimePlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }
