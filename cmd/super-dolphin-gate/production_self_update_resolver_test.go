package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

func TestResolveProductionGoToolchainSkipsBrokenPATHCandidate(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["PATH"] = strings.Join(
		[]string{fixture.brokenDirectory, fixture.symlinkDirectory},
		string(os.PathListSeparator),
	)

	toolchain, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps())
	if err != nil {
		t.Fatal(err)
	}
	if toolchain.Executable != fixture.executable {
		t.Fatalf("executable = %q, want canonical %q", toolchain.Executable, fixture.executable)
	}
	assertProductionGoToolchainFixture(t, toolchain, fixture)
}

func TestResolveProductionGoToolchainFindsSystemInstallationWithoutPATH(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["PATH"] = fixture.brokenDirectory
	deps := fixture.deps()
	deps.systemCandidates = func() []string { return []string{fixture.executable} }

	toolchain, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if toolchain.Executable != fixture.executable {
		t.Fatalf("selected executable = %q, want system installation %q", toolchain.Executable, fixture.executable)
	}
}

func TestResolveProductionGoToolchainExplicitConfigurationWins(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["SUPER_DOLPHIN_GATE_GO"] = filepath.Join(fixture.symlinkDirectory, "go")
	fixture.environment["PATH"] = fixture.brokenDirectory

	toolchain, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps())
	if err != nil {
		t.Fatal(err)
	}
	if toolchain.Executable != fixture.executable {
		t.Fatalf("executable = %q, want %q", toolchain.Executable, fixture.executable)
	}
}

func TestResolveProductionGoToolchainFailsWithoutUsableCandidate(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["PATH"] = fixture.brokenDirectory

	if _, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps()); !errors.Is(err, errNoUsableProductionGoToolchain) {
		t.Fatalf("err = %v, want no usable Go toolchain", err)
	}
}

func TestResolveProductionGoToolchainInitializesPortableCacheOverrides(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	overrideRoot := filepath.Join(fixture.root, "portable cache overrides")
	fixture.environment["SUPER_DOLPHIN_GATE_GOPATH"] = filepath.Join(overrideRoot, "gopath")
	fixture.environment["SUPER_DOLPHIN_GATE_GOCACHE"] = filepath.Join(overrideRoot, "build")
	fixture.environment["SUPER_DOLPHIN_GATE_GOMODCACHE"] = filepath.Join(overrideRoot, "modules")

	toolchain, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps())
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"GOPATH":     toolchain.GoPath,
		"GOCACHE":    toolchain.GoCache,
		"GOMODCACHE": toolchain.GoModCache,
	} {
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("%s path %q was not initialized: %v", name, path, statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode = %o, want owner-only", name, info.Mode().Perm())
		}
	}
}

func TestResolveProductionGoToolchainPrefersCandidateTreeVersion(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["PATH"] = strings.Join(
		[]string{fixture.newerDirectory, fixture.symlinkDirectory},
		string(os.PathListSeparator),
	)

	toolchain, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps())
	if err != nil {
		t.Fatal(err)
	}
	if toolchain.Executable != fixture.executable {
		t.Fatalf("selected executable = %q, want candidate-tree version %q", toolchain.Executable, fixture.executable)
	}
}

func TestResolveProductionGoToolchainUsesNewerCompatibleFallback(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["PATH"] = fixture.newerDirectory

	toolchain, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps())
	if err != nil {
		t.Fatal(err)
	}
	if toolchain.Executable != fixture.newerExecutable {
		t.Fatalf("selected executable = %q, want compatible fallback %q", toolchain.Executable, fixture.newerExecutable)
	}
}

func TestResolveProductionGoToolchainRejectsExplicitOlderVersion(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	fixture.versions[fixture.executable] = "go1.24.9"
	fixture.environment["SUPER_DOLPHIN_GATE_GO"] = fixture.executable

	if _, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps()); err == nil {
		t.Fatal("explicit toolchain older than candidate go.mod was accepted")
	}
}

func TestResolveProductionGoToolchainRejectsUnsafeEnvironmentPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		override string
		value    func(*testing.T, *productionGoResolverFixture) string
	}{
		{name: "relative", override: "SUPER_DOLPHIN_GATE_GOCACHE", value: func(*testing.T, *productionGoResolverFixture) string { return "relative" }},
		{name: "missing GOROOT", override: "SUPER_DOLPHIN_GATE_GOROOT", value: func(_ *testing.T, fixture *productionGoResolverFixture) string {
			return filepath.Join(fixture.root, "missing-goroot")
		}},
		{name: "cache is file", override: "SUPER_DOLPHIN_GATE_GOCACHE", value: func(t *testing.T, fixture *productionGoResolverFixture) string {
			t.Helper()
			path := filepath.Join(fixture.root, "cache-file")
			if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{name: "cache is writable by others", override: "SUPER_DOLPHIN_GATE_GOCACHE", value: func(t *testing.T, fixture *productionGoResolverFixture) string {
			t.Helper()
			path := filepath.Join(fixture.root, "unsafe-cache")
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o777); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newProductionGoResolverFixture(t)
			fixture.environment[test.override] = test.value(t, fixture)
			if _, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps()); err == nil {
				t.Fatalf("unsafe %s path %q was accepted", test.override, fixture.environment[test.override])
			}
		})
	}
}

func TestResolveProductionGoToolchainRejectsGroupWritableAncestor(t *testing.T) {
	fixture := newProductionGoResolverFixture(t)
	fixture.environment["SUPER_DOLPHIN_GATE_GO"] = fixture.executable
	if err := os.Chmod(fixture.root, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps()); err == nil ||
		!strings.Contains(err.Error(), "group- or other-writable") {
		t.Fatalf("group-writable toolchain ancestor error = %v", err)
	}
}

func TestResolveProductionGoToolchainRejectsToolDirectoryOutsideGoRoot(t *testing.T) {
	t.Parallel()
	fixture := newProductionGoResolverFixture(t)
	outside := filepath.Join(fixture.root, "outside", "tool")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "compile"), []byte("compiler"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.goToolDir = outside
	fixture.environment["SUPER_DOLPHIN_GATE_GO"] = fixture.executable

	if _, err := resolveProductionGoToolchainWithDeps(fixture.requirement(), fixture.deps()); err == nil ||
		!strings.Contains(err.Error(), "does not match GOROOT platform tool directory") {
		t.Fatalf("external GOTOOLDIR error = %v", err)
	}
}

func TestResolveProductionGoToolchainLiveSmoke(t *testing.T) {
	goMod, readErr := os.ReadFile("../../go.mod")
	if readErr != nil {
		t.Fatal(readErr)
	}
	requirement, parseErr := productionGoRequirementFromEntries([]sourceexport.TreeEntry{{
		Path: productionGoModPath,
		Data: goMod,
	}})
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	toolchain, err := resolveProductionGoToolchain(requirement)
	if os.Getenv("SUPER_DOLPHIN_GATE_GO") == "" && errors.Is(err, errNoUsableProductionGoToolchain) {
		t.Skip("runtime does not expose a usable Go toolchain through PATH")
	}
	if err != nil {
		t.Fatal(err)
	}
	assertProductionGoToolchainUsable(t, toolchain)
}

func assertProductionGoToolchainUsable(t *testing.T, toolchain productionGoToolchain) {
	t.Helper()
	for name, path := range map[string]string{
		"executable": toolchain.Executable,
		"GOROOT":     toolchain.GoRoot,
		"GOPATH":     toolchain.GoPath,
		"GOCACHE":    toolchain.GoCache,
		"GOMODCACHE": toolchain.GoModCache,
		"GOTOOLDIR":  toolchain.GoToolDir,
	} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatalf("%s path is not canonical and absolute: %q", name, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s path %q is unavailable: %v", name, path, err)
		}
	}
	if toolchain.GOOS != runtime.GOOS || toolchain.GOARCH != runtime.GOARCH {
		t.Fatalf("platform = %s/%s, want %s/%s", toolchain.GOOS, toolchain.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
}

func TestProductionLocalToolchainDigestChangesWithIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	binary := filepath.Join(root, "go")
	if err := os.WriteFile(binary, []byte("one"), 0o700); err != nil {
		t.Fatal(err)
	}
	toolDirectory := filepath.Join(root, "pkg", "tool")
	if err := os.MkdirAll(toolDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	compiler := filepath.Join(toolDirectory, "compile")
	if err := os.WriteFile(compiler, []byte("compiler-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	goRoot := filepath.Join(root, "goroot")
	for _, directory := range []string{
		filepath.Join(goRoot, "src", "runtime"),
		filepath.Join(goRoot, "lib"),
		filepath.Join(goRoot, "pkg", "include"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimeSource := filepath.Join(goRoot, "src", "runtime", "runtime.go")
	for path, data := range map[string]string{
		filepath.Join(goRoot, "VERSION"):                    "go1.25.7\n",
		filepath.Join(goRoot, "go.env"):                     "GOTOOLCHAIN=local\n",
		runtimeSource:                                       "package runtime\n",
		filepath.Join(goRoot, "pkg", "include", "go_asm.h"): "#define GO 1\n",
		filepath.Join(goRoot, "lib", "time.txt"):            "time\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := productionGoToolchain{
		Executable: binary,
		Version:    "go version go1.25.7 test/arch",
		GoRoot:     goRoot,
		GoToolDir:  toolDirectory,
		GOOS:       "test",
		GOARCH:     "arch",
	}
	lockDigest := "sha256:" + strings.Repeat("a", 64)
	first, err := productionLocalToolchainDigest(lockDigest, base)
	if err != nil {
		t.Fatal(err)
	}

	variants := map[string]func(*productionGoToolchain) string{
		"lock": func(*productionGoToolchain) string {
			return "sha256:" + strings.Repeat("b", 64)
		},
		"binary": func(toolchain *productionGoToolchain) string {
			if err := os.WriteFile(toolchain.Executable, []byte("two"), 0o700); err != nil {
				t.Fatal(err)
			}
			return lockDigest
		},
		"version": func(toolchain *productionGoToolchain) string {
			toolchain.Version = "go version go1.26.0 test/arch"
			return lockDigest
		},
		"goos": func(toolchain *productionGoToolchain) string {
			toolchain.GOOS = "other"
			return lockDigest
		},
		"goarch": func(toolchain *productionGoToolchain) string {
			toolchain.GOARCH = "other"
			return lockDigest
		},
		"compiler": func(*productionGoToolchain) string {
			if err := os.WriteFile(compiler, []byte("compiler-two"), 0o700); err != nil {
				t.Fatal(err)
			}
			return lockDigest
		},
		"stdlib": func(*productionGoToolchain) string {
			if err := os.WriteFile(runtimeSource, []byte("package runtime\nvar changed = true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return lockDigest
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidateLock := mutate(&candidate)
			digest, digestErr := productionLocalToolchainDigest(candidateLock, candidate)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			if digest == first {
				t.Fatalf("digest ignored %s identity", name)
			}
		})
	}
}

func TestControlledProductionGoEnvironmentDisablesUserConfiguration(t *testing.T) {
	t.Setenv("PATH", "/host/bin")
	t.Setenv("HOME", "/host/home")
	environment := controlledProductionGoEnvironment(productionGoToolchain{
		Executable: "/trusted/go/bin/go",
		GoRoot:     "/trusted/go", GoPath: "/private/gopath",
		GoCache: "/private/cache", GoModCache: "/private/mod",
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	})
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, required := range []string{"\nHOME=\n", "\nPATH=/trusted/go/bin\n", "\nGOENV=off\n", "\nGOWORK=off\n"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("controlled Go environment is missing %q: %q", required, environment)
		}
	}
	if strings.Contains(joined, "/host/bin") || strings.Contains(joined, "/host/home") {
		t.Fatalf("controlled Go environment inherited host state: %q", environment)
	}
}

func TestProductionGoProbeEnvironmentIgnoresCallerToolPaths(t *testing.T) {
	t.Setenv("PATH", "/attacker/bin")
	t.Setenv("HOME", "/attacker/home")
	t.Setenv("GOROOT", "/attacker/goroot")
	t.Setenv("GOENV", "/attacker/goenv")
	environment := productionGoProbeEnvironment("/usr/local/go/bin/go", "/private/tmp/production-go-home")
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, forbidden := range []string{"/attacker/bin", "/attacker/home", "/attacker/goroot", "/attacker/goenv"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("Go probe environment inherited %q: %q", forbidden, environment)
		}
	}
	if !strings.Contains(joined, "\nHOME=/private/tmp/production-go-home\n") ||
		!strings.Contains(joined, "\nPATH=/usr/local/go/bin"+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin\n") ||
		!strings.Contains(joined, "\nGOENV=off\n") || !strings.Contains(joined, "\nGOTOOLCHAIN=local\n") {
		t.Fatalf("Go probe environment is incomplete: %q", environment)
	}
}

func TestProductionGoProbeHomeDoesNotUseHostHome(t *testing.T) {
	t.Setenv("HOME", "/attacker/home")
	home, err := productionGoProbeHome()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || strings.Contains(home, "/attacker/home") {
		t.Fatalf("probe home = %q, want canonical temporary path independent from HOME", home)
	}
}

func TestCanonicalProductionGitExecutableRejectsOwnerControlledBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(path, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalProductionGitExecutable(path); err == nil ||
		!strings.Contains(err.Error(), "owned by root") {
		t.Fatalf("owner-controlled Git error = %v", err)
	}
}

func TestControlledProductionGitEnvironmentDisablesHostConfiguration(t *testing.T) {
	t.Setenv("PATH", "/host/bin")
	t.Setenv("HOME", "/host/home")
	environment := controlledProductionGitEnvironment("/usr/bin/git")
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, required := range []string{
		"\nHOME=\n", "\nPATH=/usr/bin\n", "\nGIT_CONFIG_NOSYSTEM=1\n",
		"\nGIT_CONFIG_GLOBAL=/dev/null\n", "\nGIT_TERMINAL_PROMPT=0\n",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("controlled Git environment is missing %q: %q", required, environment)
		}
	}
	if strings.Contains(joined, "/host/bin") || strings.Contains(joined, "/host/home") {
		t.Fatalf("controlled Git environment inherited host state: %q", environment)
	}
}

func TestControlledProductionGitEnvironmentIncludesPortableRuntimeDependencies(t *testing.T) {
	runtimeRoot := "/opt/super-dolphin-gate/runtime"
	gitExecutable := filepath.Join(runtimeRoot, "bin", "portable-tool")
	environment := controlledProductionGitEnvironment(gitExecutable)
	wantPath := strings.Join([]string{
		filepath.Join(runtimeRoot, "bin"),
		filepath.Join(runtimeRoot, "rootfs", "usr", "bin"),
		filepath.Join(runtimeRoot, "rootfs", "bin"),
	}, string(os.PathListSeparator))
	if !slices.Contains(environment, "PATH="+wantPath) {
		t.Fatalf("portable Git environment = %q, want runtime-owned dependency PATH %q", environment, wantPath)
	}
}

type productionGoResolverFixture struct {
	root             string
	executable       string
	newerExecutable  string
	brokenExecutable string
	symlinkDirectory string
	newerDirectory   string
	brokenDirectory  string
	goRoot           string
	goPath           string
	goCache          string
	goModCache       string
	goToolDir        string
	environment      map[string]string
	versions         map[string]string
}

func newProductionGoResolverFixture(t *testing.T) *productionGoResolverFixture {
	t.Helper()
	canonicalTemp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(canonicalTemp, "portable resolver fixture")
	fixture := &productionGoResolverFixture{
		root:             root,
		executable:       filepath.Join(root, "tool chain", "go-real"),
		newerExecutable:  filepath.Join(root, "newer tool chain", "go"),
		brokenExecutable: filepath.Join(root, "broken", "go"),
		symlinkDirectory: filepath.Join(root, "path link"),
		newerDirectory:   filepath.Join(root, "newer tool chain"),
		brokenDirectory:  filepath.Join(root, "broken"),
		goRoot:           filepath.Join(root, "go root"),
		goPath:           filepath.Join(root, "go path"),
		goCache:          filepath.Join(root, "build cache"),
		goModCache:       filepath.Join(root, "module cache"),
		goToolDir:        filepath.Join(root, "go root", "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH),
		environment:      make(map[string]string),
		versions:         make(map[string]string),
	}
	for _, directory := range []string{
		filepath.Dir(fixture.executable),
		fixture.newerDirectory,
		fixture.brokenDirectory,
		fixture.symlinkDirectory,
		fixture.goRoot,
		fixture.goPath,
		fixture.goCache,
		fixture.goModCache,
		fixture.goToolDir,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, executable := range []string{fixture.executable, fixture.newerExecutable, fixture.brokenExecutable} {
		if err := os.WriteFile(executable, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.goToolDir, "compile"), []byte("compiler"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.goToolDir, "link"), []byte("linker"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fixture.executable, filepath.Join(fixture.symlinkDirectory, "go")); err != nil {
		t.Fatal(err)
	}
	fixture.versions[fixture.executable] = "go1.25.7"
	fixture.versions[fixture.newerExecutable] = "go1.26.5"
	fixture.environment["PATH"] = fixture.symlinkDirectory
	return fixture
}

func (fixture *productionGoResolverFixture) requirement() productionGoRequirement {
	return productionGoRequirement{Minimum: "go1.25.7", Preferred: "go1.25.7"}
}

func (fixture *productionGoResolverFixture) deps() productionGoResolverDeps {
	return productionGoResolverDeps{
		systemCandidates: func() []string { return nil },
		getenv: func(name string) string {
			return fixture.environment[name]
		},
		run: func(program string, args ...string) ([]byte, error) {
			version, exists := fixture.versions[program]
			if !exists {
				return nil, errors.New("broken Go candidate")
			}
			switch strings.Join(args, "\x00") {
			case "version":
				return []byte("go version " + version + " " + runtime.GOOS + "/" + runtime.GOARCH + "\n"), nil
			case "env\x00GOROOT\x00GOPATH\x00GOCACHE\x00GOMODCACHE\x00GOTOOLDIR\x00GOOS\x00GOARCH":
				return fmt.Appendf(nil,
					"%s\n%s\n%s\n%s\n%s\n%s\n%s\n",
					fixture.goRoot,
					fixture.goPath,
					fixture.goCache,
					fixture.goModCache,
					fixture.goToolDir,
					runtime.GOOS,
					runtime.GOARCH,
				), nil
			default:
				return nil, fmt.Errorf("unexpected fake Go arguments %q", args)
			}
		},
	}
}

func assertProductionGoToolchainFixture(t *testing.T, toolchain productionGoToolchain, fixture *productionGoResolverFixture) {
	t.Helper()
	if toolchain.GoRoot != fixture.goRoot ||
		toolchain.GoPath != fixture.goPath ||
		toolchain.GoCache != fixture.goCache ||
		toolchain.GoModCache != fixture.goModCache ||
		toolchain.GoToolDir != fixture.goToolDir {
		t.Fatalf("toolchain paths = %#v, want fixture paths", toolchain)
	}
	if toolchain.GOOS != runtime.GOOS || toolchain.GOARCH != runtime.GOARCH {
		t.Fatalf("platform = %s/%s, want %s/%s", toolchain.GOOS, toolchain.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
}
