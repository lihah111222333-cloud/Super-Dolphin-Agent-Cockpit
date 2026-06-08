package embeddedpg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestStartReturnsNilWhenCalledAgainForOwnedDataDir(t *testing.T) {
	cfg, startPath := newOwnedStartConfig(t)
	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start() first call error = %v", err)
	}
	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start() repeat call error = %v", err)
	}
	raw, err := os.ReadFile(startPath)
	if err != nil {
		t.Fatalf("read pg_ctl start calls: %v", err)
	}
	if got := strings.Count(string(raw), "start\n"); got != 1 {
		t.Fatalf("pg_ctl start calls = %d, want 1; calls:\n%s", got, string(raw))
	}
}

func TestStartRecoversRunningDataDirWhenPackagedRecoveryEnabled(t *testing.T) {
	cfg, stopPath, startPath := newRecoverRunningStartConfig(t)
	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start() error = %v, want recovery stop then start", err)
	}
	if _, err := os.Stat(stopPath); err != nil {
		t.Fatalf("pg_ctl stop was not called during recovery: %v", err)
	}
	if _, err := os.Stat(startPath); err != nil {
		t.Fatalf("pg_ctl start was not called after recovery: %v", err)
	}
	if !ownsPostgresDataDir(cfg.DataDir) {
		t.Fatal("recovered data dir was not marked owned after start")
	}
}

func TestStartReturnsPGCtlStatusError(t *testing.T) {
	cfg, startPath := newStatusErrorStartConfig(t)
	err := Start(context.Background(), cfg)
	if err == nil {
		t.Fatal("Start() error = nil, want pg_ctl status failure")
	}
	if !strings.Contains(err.Error(), "status exploded") || !strings.Contains(err.Error(), "status") {
		t.Fatalf("Start() error = %v, want pg_ctl status output", err)
	}
	if _, statErr := os.Stat(startPath); !os.IsNotExist(statErr) {
		t.Fatalf("pg_ctl start was called after status failure")
	}
}

func TestStopReturnsPGCtlStatusError(t *testing.T) {
	cfg, stopPath := newStatusErrorStopConfig(t)
	err := Stop(context.Background(), cfg)
	if err == nil {
		t.Fatal("Stop() error = nil, want pg_ctl status failure")
	}
	if !strings.Contains(err.Error(), "status exploded") || !strings.Contains(err.Error(), "status") {
		t.Fatalf("Stop() error = %v, want pg_ctl status output", err)
	}
	if _, statErr := os.Stat(stopPath); !os.IsNotExist(statErr) {
		t.Fatalf("pg_ctl stop was called after status failure")
	}
}

func newOwnedStartConfig(t *testing.T) (contract.EmbeddedPostgresConfig, string) {
	t.Helper()
	temp := t.TempDir()
	startPath := filepath.Join(temp, "pg_ctl.starts")
	runningPath := filepath.Join(temp, "pg_ctl.running")
	cfg := newInitializedRuntimeConfig(t, temp, `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
if [ "$last" = "status" ]; then
  if [ -f "$SUPER_DOLPHIN_TEST_RUNNING" ]; then
    exit 0
  fi
  exit 3
fi
if [ "$last" = "start" ]; then
  printf 'start\n' >> "$SUPER_DOLPHIN_TEST_STARTS"
  touch "$SUPER_DOLPHIN_TEST_RUNNING"
  exit 0
fi
exit 99
`)
	t.Setenv("SUPER_DOLPHIN_TEST_STARTS", startPath)
	t.Setenv("SUPER_DOLPHIN_TEST_RUNNING", runningPath)
	return cfg, startPath
}

func newRecoverRunningStartConfig(t *testing.T) (contract.EmbeddedPostgresConfig, string, string) {
	t.Helper()
	temp := t.TempDir()
	runningPath := filepath.Join(temp, "pg_ctl.running")
	stopPath := filepath.Join(temp, "pg_ctl.stop")
	startPath := filepath.Join(temp, "pg_ctl.start")
	cfg := newInitializedRuntimeConfig(t, temp, `#!/bin/sh
	mode=""
	for arg in "$@"; do
	  case "$arg" in
	    status|stop|start) mode="$arg" ;;
	  esac
	done
	if [ "$mode" = "status" ]; then
	  if [ -f "$SUPER_DOLPHIN_TEST_RUNNING" ]; then
	    exit 0
	  fi
	  exit 3
	fi
	if [ "$mode" = "stop" ]; then
	  touch "$SUPER_DOLPHIN_TEST_STOP"
	  rm -f "$SUPER_DOLPHIN_TEST_RUNNING"
	  exit 0
	fi
	if [ "$mode" = "start" ]; then
	  touch "$SUPER_DOLPHIN_TEST_START"
	  touch "$SUPER_DOLPHIN_TEST_RUNNING"
	  exit 0
fi
exit 99
`)
	t.Setenv("SUPER_DOLPHIN_TEST_RUNNING", runningPath)
	t.Setenv("SUPER_DOLPHIN_TEST_STOP", stopPath)
	t.Setenv("SUPER_DOLPHIN_TEST_START", startPath)
	mustWriteEmbeddedPGFile(t, runningPath, []byte("running\n"), 0o600)
	cfg.RecoverRunningDataDir = true
	return cfg, stopPath, startPath
}

func newStatusErrorStartConfig(t *testing.T) (contract.EmbeddedPostgresConfig, string) {
	t.Helper()
	temp := t.TempDir()
	startPath := filepath.Join(temp, "pg_ctl.start")
	cfg := newInitializedRuntimeConfig(t, temp, `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
if [ "$last" = "status" ]; then
  echo "status exploded"
  exit 42
fi
if [ "$last" = "start" ]; then
  touch "$SUPER_DOLPHIN_TEST_START"
  exit 0
fi
exit 99
`)
	t.Setenv("SUPER_DOLPHIN_TEST_START", startPath)
	return cfg, startPath
}

func newStatusErrorStopConfig(t *testing.T) (contract.EmbeddedPostgresConfig, string) {
	t.Helper()
	temp := t.TempDir()
	stopPath := filepath.Join(temp, "pg_ctl.stop")
	cfg := newInitializedRuntimeConfig(t, temp, `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
if [ "$last" = "status" ]; then
  echo "status exploded"
  exit 42
fi
if [ "$last" = "stop" ]; then
  touch "$SUPER_DOLPHIN_TEST_STOP"
  exit 0
fi
exit 99
`)
	t.Setenv("SUPER_DOLPHIN_TEST_STOP", stopPath)
	return cfg, stopPath
}

func newInitializedRuntimeConfig(t *testing.T, temp, pgCtlScript string) contract.EmbeddedPostgresConfig {
	t.Helper()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share")
	dataDir := filepath.Join(temp, "postgres", "data")
	mustMkdirEmbeddedPGDir(t, binDir, 0o755)
	mustMkdirEmbeddedPGDir(t, shareDir, 0o755)
	mustMkdirEmbeddedPGDir(t, dataDir, 0o700)
	mustWriteEmbeddedPGFile(t, filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o644)
	mustWriteEmbeddedPGFile(t, filepath.Join(dataDir, "PG_VERSION"), []byte("16\n"), 0o600)
	mustWriteEmbeddedPGFile(t, filepath.Join(dataDir, "postgresql.auto.conf"), []byte("# external runtime config\n"), 0o600)
	writeFakeExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeFakeExecutable(t, filepath.Join(binDir, "initdb"), "#!/bin/sh\nexit 99\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\n' /ignored\n")
	writeFakeExecutable(t, filepath.Join(binDir, "pg_ctl"), pgCtlScript)
	return contract.EmbeddedPostgresConfig{
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
}
