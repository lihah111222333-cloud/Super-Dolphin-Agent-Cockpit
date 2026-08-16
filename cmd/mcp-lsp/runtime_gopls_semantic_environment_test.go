//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func runtimeServerFakeGoEnvScript(identity string) string {
	return `#!/bin/sh
# identity: ` + identity + `
if [ "$1" = "env" ] && [ "$2" = "-json" ]; then
	printf '{"AR":"%s","CC":"%s","CXX":"%s","FC":"%s","GCCGO":"%s","GOCACHE":"/go/cache","GOMODCACHE":"/go/path/pkg/mod","GOPATH":"/go/path","GOROOT":"/go/root","PKG_CONFIG":"%s"}\n' \
		"${AR:-ar}" "${CC:-gcc}" "${CXX:-g++}" "${FC:-}" "${GCCGO:-gccgo}" "${PKG_CONFIG:-pkg-config}"
	exit 0
fi
exit 0
`
}

func TestRuntimeServerArgsIgnoresVolatilePathEntriesWithResolvedGoDefaults(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("stable"))
	toolchainDir := filepath.Dir(goBinary)
	volatileFirst := filepath.Join(t.TempDir(), ".codex", "tmp", "arg0", "first")
	volatileSecond := filepath.Join(t.TempDir(), ".codex", "tmp", "arg0", "second")
	if err := os.MkdirAll(volatileFirst, 0o700); err != nil {
		t.Fatalf("create first volatile PATH entry: %v", err)
	}
	if err := os.MkdirAll(volatileSecond, 0o700); err != nil {
		t.Fatalf("create second volatile PATH entry: %v", err)
	}
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	t.Setenv("PATH", strings.Join([]string{volatileFirst, toolchainDir, toolchainDir}, string(os.PathListSeparator)))
	first := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	t.Setenv("PATH", strings.Join([]string{volatileSecond, toolchainDir}, string(os.PathListSeparator)))
	second := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if runtimeServerGoplsRemoteID(first) != runtimeServerGoplsRemoteID(second) {
		t.Fatalf("volatile PATH entries split one semantic Go toolchain cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerArgsIgnoresExplicitGoEnvironmentDefaults(t *testing.T) {
	for _, key := range runtimeServerGoplsDefaultableEnvironmentKeys() {
		value, present := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s for test: %v", key, err)
		}
		t.Cleanup(func() {
			if present {
				if err := os.Setenv(key, value); err != nil {
					t.Errorf("restore %s after test: %v", key, err)
				}
				return
			}
			if err := os.Unsetenv(key); err != nil {
				t.Errorf("clear %s after test: %v", key, err)
			}
		})
	}
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("defaults"))
	t.Setenv("PATH", filepath.Dir(goBinary))
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	implicit := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	explicit := mustRuntimeServerArgs(t, command, goplsBinary, []string{
		"GOOS=darwin", "GOARCH=arm64", "GOCACHE=/go/cache", "GOMODCACHE=/go/path/pkg/mod", "GOPATH=/go/path", "GOROOT=/go/root",
	})
	custom := mustRuntimeServerArgs(t, command, goplsBinary, []string{
		"GOOS=darwin", "GOARCH=arm64", "GOCACHE=/custom/cache", "GOMODCACHE=/go/path/pkg/mod", "GOPATH=/go/path", "GOROOT=/go/root",
	})
	if runtimeServerGoplsRemoteID(implicit) != runtimeServerGoplsRemoteID(explicit) {
		t.Fatalf("explicit Go defaults split one semantic toolchain cohort: implicit=%v explicit=%v", implicit, explicit)
	}
	if runtimeServerGoplsRemoteID(implicit) == runtimeServerGoplsRemoteID(custom) {
		t.Fatalf("custom Go environment reused the default cohort: implicit=%v custom=%v", implicit, custom)
	}
}

func TestRuntimeServerArgsFailsFastWhenGoDefaultsCannotBeResolved(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", "#!/bin/sh\nexit 23\n")
	t.Setenv("PATH", filepath.Dir(goBinary))
	command := multilsp.ServerCommand{Executable: "gopls", Args: []string{"-remote=auto;sdmcp2"}}
	if _, err := runtimeServerGoplsAutoDaemonArgs(command, goplsBinary, []string{"GOCACHE=/go/cache"}); err == nil || !strings.Contains(err.Error(), "resolve default Go environment") {
		t.Fatalf("runtimeServerGoplsAutoDaemonArgs() error = %v, want default Go environment failure", err)
	}
}

func TestRuntimeServerArgsSeparatesDifferentResolvedGoToolchainsWithResolvedDefaults(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	firstGo := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("first"))
	secondGo := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("second"))
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	t.Setenv("PATH", filepath.Dir(firstGo))
	first := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	t.Setenv("PATH", filepath.Dir(secondGo))
	second := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if runtimeServerGoplsRemoteID(first) == runtimeServerGoplsRemoteID(second) {
		t.Fatalf("different resolved Go toolchains reused one cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerArgsSeparatesDifferentResolvedAuxiliaryTools(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("auxiliary"))
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	writeRuntimeExecutable(t, filepath.Join(firstDir, "cc"), "#!/bin/sh\nexit 0\n")
	writeRuntimeExecutable(t, filepath.Join(secondDir, "cc"), "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	t.Setenv("PATH", strings.Join([]string{firstDir, filepath.Dir(goBinary)}, string(os.PathListSeparator)))
	first := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64", "CC=cc"})
	t.Setenv("PATH", strings.Join([]string{secondDir, filepath.Dir(goBinary)}, string(os.PathListSeparator)))
	second := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64", "CC=cc"})
	if runtimeServerGoplsRemoteID(first) == runtimeServerGoplsRemoteID(second) {
		t.Fatalf("different resolved C compilers reused one cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerArgsIgnoresUnusedDefaultGCCGO(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("gc-with-default-gccgo"))
	t.Setenv("PATH", filepath.Dir(goBinary))
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	withoutDefault := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=linux", "GOARCH=amd64", "GCCGO="})
	withDefault := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=linux", "GOARCH=amd64", "GCCGO=gccgo"})
	if runtimeServerGoplsRemoteID(withoutDefault) != runtimeServerGoplsRemoteID(withDefault) {
		t.Fatalf("unused default GCCGO split gc cohort: without=%v with=%v", withoutDefault, withDefault)
	}
}

func TestRuntimeServerArgsIgnoresUnavailableGoDefaultAuxiliaryTools(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("default-auxiliary-tools"))
	t.Setenv("PATH", filepath.Dir(goBinary))
	t.Setenv("CC", "gcc")
	t.Setenv("CXX", "g++")
	t.Setenv("GCCGO", "gccgo")
	t.Setenv("PKG_CONFIG", "pkg-config")
	t.Setenv("GOFLAGS", "-p=4")
	command := multilsp.ServerCommand{Executable: "gopls", Args: []string{"-remote=auto;sdmcp2"}}
	if _, err := runtimeServerGoplsAutoDaemonArgs(command, goplsBinary, []string{"GOOS=linux", "GOARCH=amd64"}); err != nil {
		t.Fatalf("runtimeServerGoplsAutoDaemonArgs() resolved unused Go defaults: %v", err)
	}
}

func TestRuntimeServerArgsRequiresSelectedGCCGO(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	goBinary := writeRuntimeServerCacheFixture(t, "go", runtimeServerFakeGoEnvScript("selected-gccgo"))
	t.Setenv("PATH", filepath.Dir(goBinary))
	command := multilsp.ServerCommand{Executable: "gopls", Args: []string{"-remote=auto;sdmcp2"}}
	_, err := runtimeServerGoplsAutoDaemonArgs(command, goplsBinary, []string{
		"GOOS=linux", "GOARCH=amd64", "GOFLAGS=-compiler=gccgo", "GCCGO=gccgo",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve GCCGO tool for gopls cohort") {
		t.Fatalf("runtimeServerGoplsAutoDaemonArgs() error = %v, want selected GCCGO resolution failure", err)
	}
}

func TestRuntimeServerArgsIgnoresUnrelatedGOPrefixedEnvironment(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args:       []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"},
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "first.json"))
	first := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "second.json"))
	second := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if runtimeServerGoplsRemoteID(first) != runtimeServerGoplsRemoteID(second) {
		t.Fatalf("unrelated GO-prefixed environment split one cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerArgsBindsAmbientGOFLAGSGlobally(t *testing.T) {
	goplsBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{Executable: "gopls", Args: []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"}}
	t.Setenv("GOFLAGS", "-mod=readonly")
	first := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	t.Setenv("GOFLAGS", "-mod=mod")
	second := mustRuntimeServerArgs(t, command, goplsBinary, []string{"GOOS=darwin", "GOARCH=arm64"})
	if runtimeServerGoplsRemoteID(first) == runtimeServerGoplsRemoteID(second) {
		t.Fatalf("different ambient GOFLAGS reused one cohort: first=%v second=%v", first, second)
	}
}

func TestRuntimeServerCommandExecutable(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
		wantErr bool
	}{
		{name: "plain", command: "clang -O2", want: "clang"},
		{name: "quoted path", command: `"/opt/tool chain/clang" -O2`, want: "/opt/tool chain/clang"},
		{name: "escaped space", command: `/opt/tool\ chain/clang -O2`, want: "/opt/tool chain/clang"},
		{name: "single quoted path", command: `'/opt/tool chain/clang' -O2`, want: "/opt/tool chain/clang"},
		{name: "empty", command: "  ", wantErr: true},
		{name: "unterminated quote", command: `"clang`, wantErr: true},
		{name: "unterminated escape", command: `clang\`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runtimeServerCommandExecutable(tc.command)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("runtimeServerCommandExecutable(%q) = (%q, %v), want (%q, error=%v)", tc.command, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestRuntimeServerGoplsRootCohortConfigBindsGoplsRealpath(t *testing.T) {
	root := t.TempDir()
	firstBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	secondBinary := writeRuntimeServerCacheFixture(t, "gopls", "#!/bin/sh\nexit 0\n")
	command := multilsp.ServerCommand{Executable: "gopls", Args: []string{"-remote=auto;sdmcp2", "-remote.listen.timeout=1m"}}
	first, err := runtimeServerGoplsRootCohortConfig(command, firstBinary, root, []string{"GOOS=darwin"})
	if err != nil {
		t.Fatalf("runtimeServerGoplsRootCohortConfig(first) error = %v", err)
	}
	second, err := runtimeServerGoplsRootCohortConfig(command, secondBinary, root, []string{"GOOS=darwin"})
	if err != nil {
		t.Fatalf("runtimeServerGoplsRootCohortConfig(second) error = %v", err)
	}
	if first.EffectiveConfigDigest == second.EffectiveConfigDigest || first.CohortID == second.CohortID {
		t.Fatalf("different gopls realpaths reused root config: first=%#v second=%#v", first, second)
	}
}
