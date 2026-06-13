//go:build legacy_embeddedpg

package embeddedpg

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestStartNoopsWhenDisabled(t *testing.T) {
	if err := Start(context.Background(), contract.EmbeddedPostgresConfig{}); err != nil {
		t.Fatalf("Start(disabled) error = %v", err)
	}
}

func TestStartNoopsWhenNotOwner(t *testing.T) {
	cfg := contract.EmbeddedPostgresConfig{
		Enabled: true,
		Owner:   false,
		BinDir:  filepath.Join(t.TempDir(), "missing-bin"),
	}
	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start(non-owner) error = %v", err)
	}
}

func TestStopNoopsWhenNotOwner(t *testing.T) {
	cfg := contract.EmbeddedPostgresConfig{
		Enabled: true,
		Owner:   false,
		BinDir:  filepath.Join(t.TempDir(), "missing-bin"),
	}
	if err := Stop(context.Background(), cfg); err != nil {
		t.Fatalf("Stop(non-owner) error = %v", err)
	}
}

func TestStartPassesShareDirToInitDB(t *testing.T) {
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share", "postgresql@16")
	recordPath := filepath.Join(temp, "initdb.args")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatalf("mkdir share dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write postgres.bki: %v", err)
	}
	writeFakeExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_ctl"), fakePGCtlNotRunningScript())
	writeFakeExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\\n' \"$SUPER_DOLPHIN_TEST_SHARE_DIR\"\n")
	writeFakeExecutable(t, filepath.Join(binDir, "initdb"), `#!/bin/sh
printf '%s\n' "$@" > "$SUPER_DOLPHIN_TEST_INITDB_ARGS"
data=""
found=0
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-D" ]; then
    shift
    data="$1"
  fi
  arg="$1"
  if [ "$arg" = "$SUPER_DOLPHIN_TEST_SHARE_DIR" ]; then
    found=1
  fi
  shift
done
if [ "$found" -ne 1 ]; then
  echo "missing expected share dir"
  exit 42
fi
mkdir -p "$data"
printf '16\n' > "$data/PG_VERSION"
exit 0
`)
	t.Setenv("SUPER_DOLPHIN_TEST_INITDB_ARGS", recordPath)
	t.Setenv("SUPER_DOLPHIN_TEST_SHARE_DIR", shareDir)

	cfg := contract.EmbeddedPostgresConfig{
		Enabled:      true,
		Owner:        true,
		BinDir:       binDir,
		ShareDir:     shareDir,
		DataDir:      filepath.Join(temp, "postgres", "data"),
		RuntimeDir:   filepath.Join(temp, "runtime", "postgres"),
		LogPath:      filepath.Join(temp, "logs", "postgres.log"),
		DatabaseName: "super_dolphin",
		UserName:     "super_dolphin",
		Port:         55432,
	}

	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	args, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read initdb args: %v", err)
	}
	if !strings.Contains(string(args), "-L\n"+shareDir+"\n") {
		t.Fatalf("initdb args missing -L share dir:\n%s", string(args))
	}
	for _, want := range []string{"-c\nlog_timezone=UTC0\n", "-c\ntimezone=UTC0\n"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("initdb args missing %q:\n%s", want, string(args))
		}
	}
	if !strings.Contains(string(args), "--locale=C\n") {
		t.Fatalf("initdb args missing deterministic locale:\n%s", string(args))
	}
}

func TestStartDoesNotRejectCompiledShareDirWhenPackagedShareDirIsExplicit(t *testing.T) {
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share", "postgresql@16")
	initDBCalledPath := filepath.Join(temp, "initdb.called")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatalf("mkdir share dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write postgres.bki: %v", err)
	}
	writeFakeExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_ctl"), fakePGCtlNotRunningScript())
	writeFakeExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\\n' '/opt/homebrew/opt/postgresql@16/share/postgresql@16'\n")
	writeFakeExecutable(t, filepath.Join(binDir, "initdb"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-D" ]; then
    shift
    data="$1"
    break
  fi
  shift
done
mkdir -p "$data"
printf '16\n' > "$data/PG_VERSION"
touch "$SUPER_DOLPHIN_TEST_INITDB_CALLED"
exit 0
`)
	t.Setenv("SUPER_DOLPHIN_TEST_INITDB_CALLED", initDBCalledPath)

	cfg := contract.EmbeddedPostgresConfig{
		Enabled:      true,
		Owner:        true,
		BinDir:       binDir,
		ShareDir:     shareDir,
		DataDir:      filepath.Join(temp, "postgres", "data"),
		RuntimeDir:   filepath.Join(temp, "runtime", "postgres"),
		LogPath:      filepath.Join(temp, "logs", "postgres.log"),
		DatabaseName: "super_dolphin",
		UserName:     "super_dolphin",
		Port:         55432,
	}

	err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, statErr := os.Stat(initDBCalledPath); statErr != nil {
		t.Fatalf("initdb was not called with explicit share dir: %v", statErr)
	}
}

func writeFakeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake executable %s: %v", path, err)
	}
}

func fakePGCtlNotRunningScript() string {
	return `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
case "$last" in
  status) exit 3 ;;
  start) exit 0 ;;
esac
exit 0
`
}

func TestStartRequiresPackagedPostgresBinaries(t *testing.T) {
	cfg := contract.EmbeddedPostgresConfig{
		Enabled:      true,
		Owner:        true,
		BinDir:       filepath.Join(t.TempDir(), "missing-bin"),
		DataDir:      filepath.Join(t.TempDir(), "data"),
		RuntimeDir:   filepath.Join(t.TempDir(), "runtime"),
		LogPath:      filepath.Join(t.TempDir(), "postgres.log"),
		DatabaseName: "super_dolphin",
		UserName:     "super_dolphin",
		Port:         55432,
	}

	err := Start(context.Background(), cfg)
	if err == nil {
		t.Fatal("Start() error = nil, want missing binary guidance")
	}
	msg := err.Error()
	for _, want := range []string{
		"embedded postgres binary missing",
		"postgres",
		"initdb",
		"pg_ctl",
		"pg_config",
		"SUPER_DOLPHIN_POSTGRES_BIN_DIR",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("Start() error missing %q:\n%s", want, msg)
		}
	}
}

func TestStartRejectsPermissiveRuntimeDir(t *testing.T) {
	cfg := newStartPermissionTestConfig(t)
	if err := os.MkdirAll(cfg.RuntimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
	if err := os.Chmod(cfg.RuntimeDir, 0o755); err != nil {
		t.Fatalf("chmod runtime dir: %v", err)
	}

	err := Start(context.Background(), cfg)
	if err == nil {
		t.Fatal("Start() error = nil, want permissive runtime dir failure")
	}
	assertPermissionError(t, err, cfg.RuntimeDir, "0755")
}

func TestStartRejectsPermissiveExistingDataDir(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o770} {
		t.Run(mode.String(), func(t *testing.T) {
			cfg := newStartPermissionTestConfig(t)
			mustMkdirEmbeddedPGDir(t, filepath.Dir(cfg.DataDir), 0o700)
			if err := os.Mkdir(cfg.DataDir, mode); err != nil {
				t.Fatalf("mkdir data dir: %v", err)
			}
			if err := os.Chmod(cfg.DataDir, mode); err != nil {
				t.Fatalf("chmod data dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(cfg.DataDir, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
				t.Fatalf("write PG_VERSION: %v", err)
			}

			err := Start(context.Background(), cfg)
			if err == nil {
				t.Fatal("Start() error = nil, want permissive data dir failure")
			}
			assertPermissionError(t, err, cfg.DataDir, permissionString(mode))
		})
	}
}

func TestStartRejectsPermissivePrivateDirectoryParents(t *testing.T) {
	tests := []struct {
		name      string
		configure func(t *testing.T, cfg *contract.EmbeddedPostgresConfig) string
	}{
		{
			name: "data parent",
			configure: func(t *testing.T, cfg *contract.EmbeddedPostgresConfig) string {
				parent := filepath.Join(t.TempDir(), "data-parent")
				makePermissiveDir(t, parent, 0o755)
				cfg.DataDir = filepath.Join(parent, "data")
				return parent
			},
		},
		{
			name: "runtime dir",
			configure: func(t *testing.T, cfg *contract.EmbeddedPostgresConfig) string {
				cfg.RuntimeDir = filepath.Join(t.TempDir(), "runtime")
				makePermissiveDir(t, cfg.RuntimeDir, 0o755)
				return cfg.RuntimeDir
			},
		},
		{
			name: "log parent",
			configure: func(t *testing.T, cfg *contract.EmbeddedPostgresConfig) string {
				parent := filepath.Join(t.TempDir(), "logs")
				makePermissiveDir(t, parent, 0o755)
				cfg.LogPath = filepath.Join(parent, "postgres.log")
				return parent
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newStartPermissionTestConfig(t)
			path := tt.configure(t, &cfg)

			err := Start(context.Background(), cfg)
			if err == nil {
				t.Fatal("Start() error = nil, want permissive private directory failure")
			}
			assertPermissionError(t, err, path, "0755")
		})
	}
}

func TestValidatePrivateDirSkipsPOSIXPermissionBitsOnWindows(t *testing.T) {
	prev := pgDeps
	pgDeps = embeddedPGDeps{goos: func() string { return "windows" }}
	t.Cleanup(func() { pgDeps = prev })

	dir := filepath.Join(t.TempDir(), "postgres")
	makePermissiveDir(t, dir, 0o777)
	if err := validatePrivateDir(dir, "data parent"); err != nil {
		t.Fatalf("validatePrivateDir() on windows error = %v, want nil for POSIX mode bits", err)
	}

	file := filepath.Join(t.TempDir(), "postgres-file")
	mustWriteEmbeddedPGFile(t, file, []byte("not a dir"), 0o666)
	err := validatePrivateDir(file, "data parent")
	if err == nil {
		t.Fatal("validatePrivateDir() on windows file error = nil, want not-a-directory failure")
	}
	if !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("validatePrivateDir() error = %v, want not-a-directory failure", err)
	}
}

func TestStartFailsFastWhenDataDirAlreadyRunningWithoutRewritingRuntimeConfig(t *testing.T) {
	cfg, statusPath, startPath := newAlreadyRunningStartConfig(t)
	configPath := filepath.Join(cfg.DataDir, "postgresql.auto.conf")
	before, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read preexisting runtime config: %v", readErr)
	}
	err := Start(context.Background(), cfg)
	if err == nil {
		t.Fatal("Start() error = nil, want already-running fail-fast")
	}
	if !strings.Contains(err.Error(), "already running") || !strings.Contains(err.Error(), cfg.DataDir) {
		t.Fatalf("Start() error = %v, want already-running data dir guidance", err)
	}
	if _, err := os.Stat(statusPath); err != nil {
		t.Fatalf("pg_ctl status was not called: %v", err)
	}
	if _, err := os.Stat(startPath); !os.IsNotExist(err) {
		t.Fatalf("pg_ctl start was called despite running server")
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read runtime config after failed Start: %v", readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("Start() rewrote runtime config before already-running fail-fast:\nBefore:\n%s\nAfter:\n%s", string(before), string(after))
	}
}

func newAlreadyRunningStartConfig(t *testing.T) (contract.EmbeddedPostgresConfig, string, string) {
	t.Helper()
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share")
	dataDir := filepath.Join(temp, "postgres", "data")
	statusPath := filepath.Join(temp, "pg_ctl.status")
	startPath := filepath.Join(temp, "pg_ctl.start")
	mustMkdirEmbeddedPGDir(t, binDir, 0o755)
	mustMkdirEmbeddedPGDir(t, shareDir, 0o755)
	mustMkdirEmbeddedPGDir(t, dataDir, 0o700)
	mustWriteEmbeddedPGFile(t, filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o644)
	mustWriteEmbeddedPGFile(t, filepath.Join(dataDir, "PG_VERSION"), []byte("16\n"), 0o600)
	mustWriteEmbeddedPGFile(t, filepath.Join(dataDir, "postgresql.auto.conf"), []byte("# external runtime config\n"), 0o600)
	writeFakeExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeFakeExecutable(t, filepath.Join(binDir, "initdb"), "#!/bin/sh\nexit 99\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\n' /ignored\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_ctl"), `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
if [ "$last" = "status" ]; then
  touch "$SUPER_DOLPHIN_TEST_STATUS"
  exit 0
fi
if [ "$last" = "start" ]; then
  touch "$SUPER_DOLPHIN_TEST_START"
  exit 42
fi
exit 0
`)
	t.Setenv("SUPER_DOLPHIN_TEST_STATUS", statusPath)
	t.Setenv("SUPER_DOLPHIN_TEST_START", startPath)

	cfg := contract.EmbeddedPostgresConfig{
		Enabled:      true,
		Owner:        true,
		BinDir:       binDir,
		ShareDir:     shareDir,
		DataDir:      dataDir,
		RuntimeDir:   filepath.Join(temp, "runtime", "postgres"),
		LogPath:      filepath.Join(temp, "logs", "postgres.log"),
		DatabaseName: "super_dolphin",
		UserName:     "super_dolphin",
		Port:         55432,
	}
	return cfg, statusPath, startPath
}

func newStartPermissionTestConfig(t *testing.T) contract.EmbeddedPostgresConfig {
	t.Helper()
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.MkdirAll(shareDir, 0o700); err != nil {
		t.Fatalf("mkdir share dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o600); err != nil {
		t.Fatalf("write postgres.bki: %v", err)
	}
	writeFakeExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\n' /ignored\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_ctl"), `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
case "$last" in
  status) exit 3 ;;
  start) exit 0 ;;
esac
exit 0
`)
	writeFakeExecutable(t, filepath.Join(binDir, "initdb"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-D" ]; then
    shift
    data="$1"
    break
  fi
  shift
done
mkdir -p "$data"
printf '16\n' > "$data/PG_VERSION"
exit 0
`)
	return contract.EmbeddedPostgresConfig{
		Enabled:      true,
		Owner:        true,
		BinDir:       binDir,
		ShareDir:     shareDir,
		DataDir:      filepath.Join(temp, "postgres", "data"),
		RuntimeDir:   filepath.Join(temp, "runtime", "postgres"),
		LogPath:      filepath.Join(temp, "logs", "postgres.log"),
		DatabaseName: "super_dolphin",
		UserName:     "super_dolphin",
		Port:         55432,
	}
}

func mustMkdirEmbeddedPGDir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteEmbeddedPGFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func makePermissiveDir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatalf("mkdir permissive dir %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod permissive dir %s: %v", path, err)
	}
}

func assertPermissionError(t *testing.T, err error, path, mode string) {
	t.Helper()
	msg := err.Error()
	if !strings.Contains(msg, path) || !strings.Contains(msg, mode) {
		t.Fatalf("Start() error = %v, want path %q and mode %s", err, path, mode)
	}
}

func permissionString(mode os.FileMode) string {
	return "0" + strconv.FormatUint(uint64(mode.Perm()), 8)
}

func TestStartRecoversPartialDataDirWithAtomicInit(t *testing.T) {
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share")
	dataDir := filepath.Join(temp, "postgres", "data")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatalf("mkdir share dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write postgres.bki: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "partial"), []byte("junk"), 0o600); err != nil {
		t.Fatalf("write partial data: %v", err)
	}
	writeFakeExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_ctl"), fakePGCtlNotRunningScript())
	writeFakeExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\n' /ignored\n")
	writeFakeExecutable(t, filepath.Join(binDir, "initdb"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-D" ]; then
    shift
    data="$1"
    break
  fi
  shift
done
mkdir -p "$data"
printf '16\n' > "$data/PG_VERSION"
exit 0
`)

	cfg := contract.EmbeddedPostgresConfig{
		Enabled:      true,
		Owner:        true,
		BinDir:       binDir,
		ShareDir:     shareDir,
		DataDir:      dataDir,
		RuntimeDir:   filepath.Join(temp, "runtime", "postgres"),
		LogPath:      filepath.Join(temp, "logs", "postgres.log"),
		DatabaseName: "super_dolphin",
		UserName:     "super_dolphin",
		Port:         55432,
	}
	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "PG_VERSION")); err != nil {
		t.Fatalf("new data dir missing PG_VERSION: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir+".incomplete", "partial")); err != nil {
		t.Fatalf("partial data dir was not preserved under .incomplete: %v", err)
	}
}

func TestPostgresConfigStringEscapesBackslashAndQuote(t *testing.T) {
	got := postgresConfigString(`/tmp/sd\home/it's`)
	want := `/tmp/sd\\home/it''s`
	if got != want {
		t.Fatalf("postgresConfigString() = %q, want %q", got, want)
	}
}

func TestWritePostgresRuntimeConfigUsesTCPOnWindows(t *testing.T) {
	prev := pgDeps
	pgDeps = embeddedPGDeps{goos: func() string { return "windows" }}
	t.Cleanup(func() { pgDeps = prev })

	cfg := contract.EmbeddedPostgresConfig{
		DataDir:    t.TempDir(),
		RuntimeDir: `C:\Users\tester\AppData\Local\Temp\sd-pg-55432`,
		Port:       55432,
	}
	if err := writePostgresRuntimeConfig(cfg); err != nil {
		t.Fatalf("writePostgresRuntimeConfig() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "postgresql.auto.conf"))
	if err != nil {
		t.Fatalf("read postgresql.auto.conf: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "listen_addresses = '127.0.0.1'") {
		t.Fatalf("postgresql.auto.conf missing TCP listen address:\n%s", text)
	}
	if strings.Contains(text, "unix_socket_directories") {
		t.Fatalf("postgresql.auto.conf contains unix socket config on windows:\n%s", text)
	}
}

func TestWritePostgresRuntimeConfigUsesUnixSocketOffWindows(t *testing.T) {
	prev := pgDeps
	pgDeps = embeddedPGDeps{goos: func() string { return "linux" }}
	t.Cleanup(func() { pgDeps = prev })

	cfg := contract.EmbeddedPostgresConfig{
		DataDir:    t.TempDir(),
		RuntimeDir: `/tmp/sd-pg-test`,
		Port:       55432,
	}
	if err := writePostgresRuntimeConfig(cfg); err != nil {
		t.Fatalf("writePostgresRuntimeConfig() error = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "postgresql.auto.conf"))
	if err != nil {
		t.Fatalf("read postgresql.auto.conf: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"listen_addresses = ''",
		"unix_socket_directories = '/tmp/sd-pg-test'",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("postgresql.auto.conf missing %q:\n%s", want, text)
		}
	}
}
