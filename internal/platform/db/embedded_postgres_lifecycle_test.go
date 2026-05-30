package db

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx/fxtest"
)

func TestRegisterLifecycleStartsEmbeddedPostgresBeforePoolUse(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	cfg := &config.Config{}
	cfg.EmbeddedPostgres.Enabled = true
	cfg.EmbeddedPostgres.Owner = true
	cfg.EmbeddedPostgres.BinDir = filepath.Join(t.TempDir(), "missing-bin")
	cfg.EmbeddedPostgres.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.EmbeddedPostgres.RuntimeDir = filepath.Join(t.TempDir(), "runtime")
	cfg.EmbeddedPostgres.LogPath = filepath.Join(t.TempDir(), "postgres.log")
	cfg.EmbeddedPostgres.DatabaseName = "super_dolphin"
	cfg.EmbeddedPostgres.UserName = "super_dolphin"
	cfg.EmbeddedPostgres.Port = 55432

	registerLifecycle(lc, pkglogger.Get(), nil, cfg)

	err := lc.Start(context.Background())
	if err == nil {
		t.Fatal("Lifecycle Start() error = nil, want embedded postgres missing binary error")
	}
	if !strings.Contains(err.Error(), "embedded postgres binary missing") {
		t.Fatalf("Lifecycle Start() error = %v", err)
	}
}

func TestRegisterLifecycleStopsEmbeddedPostgresWhenStartupFailsAfterStart(t *testing.T) {
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share")
	dataDir := filepath.Join(temp, "postgres", "data")
	stopPath := filepath.Join(temp, "pg_ctl.stop")
	for _, dir := range []string{binDir, shareDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dataDir, err)
	}
	if err := os.WriteFile(filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("write postgres.bki: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatalf("write PG_VERSION: %v", err)
	}
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "initdb"), "#!/bin/sh\nexit 99\n")
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\n' /ignored\n")
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "pg_ctl"), `#!/bin/sh
running="$SUPER_DOLPHIN_TEST_RUNNING"
last=""
for arg in "$@"; do last="$arg"; done
case "$last" in
  status) test -f "$running" || exit 3; exit 0 ;;
  start) touch "$running"; exit 0 ;;
  fast) touch "$SUPER_DOLPHIN_TEST_STOP"; exit 0 ;;
esac
exit 0
`)
	t.Setenv("SUPER_DOLPHIN_TEST_RUNNING", filepath.Join(temp, "pg_ctl.running"))
	t.Setenv("SUPER_DOLPHIN_TEST_STOP", stopPath)

	poolCfg, err := pgxpool.ParseConfig("postgres://super_dolphin@localhost:55432/super_dolphin?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	defer pool.Close()
	cfg := &config.Config{
		DatabaseURL: "postgres://super_dolphin@localhost:55432/super_dolphin?sslmode=disable",
		ProjectRoot: t.TempDir(),
	}
	cfg.EmbeddedPostgres.Enabled = true
	cfg.EmbeddedPostgres.Owner = true
	cfg.EmbeddedPostgres.BinDir = binDir
	cfg.EmbeddedPostgres.ShareDir = shareDir
	cfg.EmbeddedPostgres.DataDir = dataDir
	cfg.EmbeddedPostgres.RuntimeDir = filepath.Join(temp, "runtime", "postgres")
	cfg.EmbeddedPostgres.LogPath = filepath.Join(temp, "logs", "postgres.log")
	cfg.EmbeddedPostgres.DatabaseName = "super_dolphin"
	cfg.EmbeddedPostgres.UserName = "super_dolphin"
	cfg.EmbeddedPostgres.Port = 55432

	lc := fxtest.NewLifecycle(t)
	registerLifecycle(lc, pkglogger.Get(), pool, cfg)
	err = lc.Start(context.Background())
	if err == nil {
		t.Fatal("Lifecycle Start() error = nil, want database connection failure")
	}
	if _, statErr := os.Stat(stopPath); statErr != nil {
		t.Fatalf("embedded postgres was not stopped after startup failure: %v", statErr)
	}
}

func TestRegisterLifecycleDoesNotStopAlreadyRunningEmbeddedPostgres(t *testing.T) {
	fixture := newDBLifecycleFixture(t, "postgres://super_dolphin@localhost:55432/super_dolphin?sslmode=disable", `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
case "$last" in
  status) exit 0 ;;
  start) touch "$SUPER_DOLPHIN_TEST_START"; exit 0 ;;
  fast) touch "$SUPER_DOLPHIN_TEST_STOP"; exit 0 ;;
esac
exit 0
`)
	t.Setenv("SUPER_DOLPHIN_TEST_STOP", fixture.stopPath)
	t.Setenv("SUPER_DOLPHIN_TEST_START", fixture.startPath)

	lc := fxtest.NewLifecycle(t)
	registerLifecycle(lc, pkglogger.Get(), fixture.pool, fixture.cfg)
	err := lc.Start(context.Background())
	if err == nil {
		t.Fatal("Lifecycle Start() error = nil, want already-running fail-fast")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Lifecycle Start() error = %v, want already-running guidance", err)
	}
	if _, statErr := os.Stat(fixture.stopPath); !os.IsNotExist(statErr) {
		t.Fatalf("embedded postgres stop was called for an already-running instance")
	}
	if _, statErr := os.Stat(fixture.startPath); !os.IsNotExist(statErr) {
		t.Fatalf("embedded postgres start was called for an already-running instance")
	}
}

func TestRegisterLifecycleStopsEmbeddedPostgresWithFreshContextWhenStartupContextCanceled(t *testing.T) {
	listener, startCtx := cancelStartContextAfterDBConnect(t)
	databaseURL := "postgres://super_dolphin@" + listener.Addr().String() + "/super_dolphin?sslmode=disable"
	fixture := newDBLifecycleFixture(t, databaseURL, `#!/bin/sh
running="$SUPER_DOLPHIN_TEST_RUNNING"
last=""
for arg in "$@"; do last="$arg"; done
case "$last" in
  status) test -f "$running" || exit 3; exit 0 ;;
  start) touch "$running"; exit 0 ;;
  fast) touch "$SUPER_DOLPHIN_TEST_STOP"; exit 0 ;;
esac
exit 0
`)
	t.Setenv("SUPER_DOLPHIN_TEST_RUNNING", filepath.Join(fixture.temp, "pg_ctl.running"))
	t.Setenv("SUPER_DOLPHIN_TEST_STOP", fixture.stopPath)

	lc := fxtest.NewLifecycle(t)
	registerLifecycle(lc, pkglogger.Get(), fixture.pool, fixture.cfg)
	err := lc.Start(startCtx)
	if err == nil {
		t.Fatal("Lifecycle Start() error = nil, want database connection failure")
	}
	if _, statErr := os.Stat(fixture.stopPath); statErr != nil {
		t.Fatalf("embedded postgres stop did not use a fresh shutdown context: %v", statErr)
	}
}

type dbLifecycleFixture struct {
	cfg       *config.Config
	pool      *pgxpool.Pool
	temp      string
	stopPath  string
	startPath string
}

func cancelStartContextAfterDBConnect(t *testing.T) (net.Listener, context.Context) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	startCtx, cancelStart := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancelStart()
		_ = listener.Close()
	})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		cancelStart()
		_ = conn.Close()
		_ = listener.Close()
	}()
	return listener, startCtx
}

func newDBLifecycleFixture(t *testing.T, databaseURL, pgCtlScript string) dbLifecycleFixture {
	t.Helper()
	temp := t.TempDir()
	binDir := filepath.Join(temp, "bin")
	shareDir := filepath.Join(temp, "share")
	dataDir := filepath.Join(temp, "postgres", "data")
	for _, dir := range []string{binDir, shareDir, dataDir} {
		mustMkdirDBLifecycleDir(t, dir, 0o700)
	}
	mustWriteDBLifecycleFile(t, filepath.Join(shareDir, "postgres.bki"), []byte("fake"), 0o600)
	mustWriteDBLifecycleFile(t, filepath.Join(dataDir, "PG_VERSION"), []byte("16\n"), 0o600)
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "postgres"), "#!/bin/sh\nexit 0\n")
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "initdb"), "#!/bin/sh\nexit 99\n")
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "pg_config"), "#!/bin/sh\nprintf '%s\n' /ignored\n")
	writeDBLifecycleExecutable(t, filepath.Join(binDir, "pg_ctl"), pgCtlScript)
	pool := newDBLifecyclePool(t, databaseURL)
	t.Cleanup(pool.Close)
	cfg := &config.Config{
		DatabaseURL: databaseURL,
		ProjectRoot: t.TempDir(),
	}
	cfg.EmbeddedPostgres.Enabled = true
	cfg.EmbeddedPostgres.Owner = true
	cfg.EmbeddedPostgres.BinDir = binDir
	cfg.EmbeddedPostgres.ShareDir = shareDir
	cfg.EmbeddedPostgres.DataDir = dataDir
	cfg.EmbeddedPostgres.RuntimeDir = filepath.Join(temp, "runtime", "postgres")
	cfg.EmbeddedPostgres.LogPath = filepath.Join(temp, "logs", "postgres.log")
	cfg.EmbeddedPostgres.DatabaseName = "super_dolphin"
	cfg.EmbeddedPostgres.UserName = "super_dolphin"
	cfg.EmbeddedPostgres.Port = 55432
	return dbLifecycleFixture{
		cfg:       cfg,
		pool:      pool,
		temp:      temp,
		stopPath:  filepath.Join(temp, "pg_ctl.stop"),
		startPath: filepath.Join(temp, "pg_ctl.start"),
	}
}

func newDBLifecyclePool(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	return pool
}

func mustMkdirDBLifecycleDir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

func mustWriteDBLifecycleFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeDBLifecycleExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake executable %s: %v", path, err)
	}
}
