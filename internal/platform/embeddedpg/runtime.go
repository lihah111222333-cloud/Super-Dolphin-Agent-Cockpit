package embeddedpg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	pgCtlTimeoutSeconds           = "30"
	pgCtlStatusNotRunningExitCode = 3
	postgresFixedTZ               = "UTC0"
)

var ownedPostgresDataDirs sync.Map

func Start(ctx context.Context, cfg contract.EmbeddedPostgresConfig) error {
	binaries, enabled, err := prepareStartRuntime(cfg)
	if err != nil || !enabled {
		return err
	}
	return startPreparedRuntime(ctx, cfg, binaries)
}

func startPreparedRuntime(ctx context.Context, cfg contract.EmbeddedPostgresConfig, binaries postgresBinaries) error {
	alreadyOwned, err := ensureStartableDataDir(ctx, cfg, binaries)
	if err != nil || alreadyOwned {
		return err
	}
	if err := writePostgresRuntimeConfig(cfg); err != nil {
		return err
	}
	if err := removeStalePostmasterPID(cfg.DataDir); err != nil {
		return err
	}
	if err := runPostgresStartCommand(ctx, binaries.pgCtl, cfg); err != nil {
		return err
	}
	markPostgresDataDirOwned(cfg.DataDir)
	return nil
}

func ensureStartableDataDir(ctx context.Context, cfg contract.EmbeddedPostgresConfig, binaries postgresBinaries) (bool, error) {
	if err := ensureStartedDataDir(ctx, cfg, binaries); err != nil {
		return false, err
	}
	running, err := postgresRunning(ctx, cfg, binaries.pgCtl)
	if err != nil {
		return false, err
	}
	if !running {
		return false, nil
	}
	if ownsPostgresDataDir(cfg.DataDir) {
		return true, nil
	}
	if !cfg.RecoverRunningDataDir {
		return false, fmt.Errorf("embedded postgres already running for data dir %q; refusing to reuse a server this process did not start", cfg.DataDir)
	}
	if err := recoverRunningDataDir(ctx, cfg, binaries.pgCtl); err != nil {
		return false, err
	}
	return false, nil
}

func recoverRunningDataDir(ctx context.Context, cfg contract.EmbeddedPostgresConfig, pgCtl string) error {
	if err := runCommand(ctx, pgCtl, "-D", cfg.DataDir, "-w", "-t", pgCtlTimeoutSeconds, "stop", "-m", "fast"); err != nil {
		return fmt.Errorf("recover embedded postgres running data dir %q: %w", cfg.DataDir, err)
	}
	forgetPostgresDataDirOwned(cfg.DataDir)
	running, err := postgresRunning(ctx, cfg, pgCtl)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("embedded postgres still running for data dir %q after recovery stop", cfg.DataDir)
	}
	return nil
}

func prepareStartRuntime(cfg contract.EmbeddedPostgresConfig) (postgresBinaries, bool, error) {
	if !cfg.Enabled || !cfg.Owner {
		return postgresBinaries{}, false, nil
	}
	if strings.TrimSpace(cfg.ResolveError) != "" {
		return postgresBinaries{}, false, fmt.Errorf("embedded postgres config: %s", cfg.ResolveError)
	}
	binaries, err := requiredBinaries(cfg.BinDir)
	if err != nil {
		return postgresBinaries{}, false, err
	}
	if err := ensureRuntimeDirs(cfg); err != nil {
		return postgresBinaries{}, false, err
	}
	if err := ensureShareDir(cfg.ShareDir); err != nil {
		return postgresBinaries{}, false, err
	}
	return binaries, true, nil
}

func ensureStartedDataDir(ctx context.Context, cfg contract.EmbeddedPostgresConfig, binaries postgresBinaries) error {
	if err := validateExistingPrivateDir(cfg.DataDir, "data dir"); err != nil {
		return err
	}
	if !dataDirInitialized(cfg.DataDir) {
		if err := ensureInitializedDataDir(ctx, cfg, binaries); err != nil {
			return err
		}
	}
	if err := validatePrivateDir(cfg.DataDir, "data dir"); err != nil {
		return err
	}
	return nil
}

func Stop(ctx context.Context, cfg contract.EmbeddedPostgresConfig) error {
	if !cfg.Enabled || !cfg.Owner {
		return nil
	}
	pgCtl := filepath.Join(cfg.BinDir, executableName("pg_ctl"))
	if _, err := os.Stat(pgCtl); err != nil {
		return fmt.Errorf("embedded postgres pg_ctl missing during stop: %w", err)
	}
	if !dataDirInitialized(cfg.DataDir) {
		return nil
	}
	running, err := postgresRunning(ctx, cfg, pgCtl)
	if err != nil {
		return err
	}
	if !running {
		forgetPostgresDataDirOwned(cfg.DataDir)
		return nil
	}
	if err := runCommand(ctx, pgCtl, "-D", cfg.DataDir, "-w", "-t", pgCtlTimeoutSeconds, "stop", "-m", "fast"); err != nil {
		return err
	}
	forgetPostgresDataDirOwned(cfg.DataDir)
	return nil
}

type postgresBinaries struct {
	postgres string
	initdb   string
	pgCtl    string
	pgConfig string
}

func requiredBinaries(binDir string) (postgresBinaries, error) {
	binDir = strings.TrimSpace(binDir)
	if binDir == "" {
		return postgresBinaries{}, errors.New("embedded postgres binary missing: BinDir is empty; package postgres under Contents/Resources/postgres/<platform>/bin or set SUPER_DOLPHIN_POSTGRES_BIN_DIR")
	}
	binaries := postgresBinaries{
		postgres: filepath.Join(binDir, executableName("postgres")),
		initdb:   filepath.Join(binDir, executableName("initdb")),
		pgCtl:    filepath.Join(binDir, executableName("pg_ctl")),
		pgConfig: filepath.Join(binDir, executableName("pg_config")),
	}
	for _, path := range []string{binaries.postgres, binaries.initdb, binaries.pgCtl, binaries.pgConfig} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return postgresBinaries{}, fmt.Errorf("embedded postgres binary missing: %s; package postgres, initdb, pg_ctl, and pg_config under %s or set SUPER_DOLPHIN_POSTGRES_BIN_DIR", filepath.Base(path), binDir)
		}
	}
	return binaries, nil
}

func executableName(name string) string {
	if runtimeGOOS() == "windows" {
		return name + ".exe"
	}
	return name
}

func ensureRuntimeDirs(cfg contract.EmbeddedPostgresConfig) error {
	for _, dir := range []privateDir{
		{path: filepath.Dir(cfg.DataDir), label: "data parent"},
		{path: cfg.RuntimeDir, label: "runtime dir"},
		{path: filepath.Dir(cfg.LogPath), label: "log parent"},
	} {
		if err := os.MkdirAll(dir.path, 0o700); err != nil {
			return fmt.Errorf("create embedded postgres %s %q: %w", dir.label, dir.path, err)
		}
		if err := validatePrivateDir(dir.path, dir.label); err != nil {
			return err
		}
	}
	return nil
}

type privateDir struct {
	path  string
	label string
}

func validateExistingPrivateDir(path, label string) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect embedded postgres %s %q: %w", label, path, err)
	}
	return validatePrivateDir(path, label)
}

func validatePrivateDir(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect embedded postgres %s %q: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("embedded postgres %s %q is not a directory", label, path)
	}
	if runtimeGOOS() == "windows" {
		return nil
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return fmt.Errorf("embedded postgres %s %q has permissions %s; require private permissions with group/other bits clear", label, path, formatPermission(mode))
	}
	return nil
}

func formatPermission(mode os.FileMode) string {
	return "0" + strconv.FormatUint(uint64(mode.Perm()), 8)
}

func ensureShareDir(shareDir string) error {
	shareDir = strings.TrimSpace(shareDir)
	if shareDir == "" {
		return errors.New("embedded postgres share dir is empty; package postgres.bki under Contents/Resources/postgres/<platform>/share/postgresql@16 or set SUPER_DOLPHIN_POSTGRES_SHARE_DIR")
	}
	if info, err := os.Stat(filepath.Join(shareDir, "postgres.bki")); err != nil || info.IsDir() {
		return fmt.Errorf("embedded postgres share file missing: %s; package PostgreSQL share files or set SUPER_DOLPHIN_POSTGRES_SHARE_DIR", filepath.Join(shareDir, "postgres.bki"))
	}
	return nil
}

func dataDirInitialized(dataDir string) bool {
	info, err := os.Stat(filepath.Join(dataDir, "PG_VERSION"))
	return err == nil && !info.IsDir()
}

func ensureInitializedDataDir(ctx context.Context, cfg contract.EmbeddedPostgresConfig, binaries postgresBinaries) error {
	if err := quarantinePartialDataDir(cfg.DataDir); err != nil {
		return err
	}
	return initDataDirAtomic(ctx, cfg, binaries)
}

func quarantinePartialDataDir(dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect embedded postgres data dir: %w", err)
	}
	if len(entries) == 0 {
		return os.Remove(dataDir)
	}
	incompleteDir := dataDir + ".incomplete"
	if err := os.RemoveAll(incompleteDir); err != nil {
		return fmt.Errorf("remove previous embedded postgres incomplete data dir: %w", err)
	}
	if err := os.Rename(dataDir, incompleteDir); err != nil {
		return fmt.Errorf("quarantine incomplete embedded postgres data dir: %w", err)
	}
	return nil
}

func initDataDirAtomic(ctx context.Context, cfg contract.EmbeddedPostgresConfig, binaries postgresBinaries) error {
	initDir := cfg.DataDir + ".init"
	if err := os.RemoveAll(initDir); err != nil {
		return fmt.Errorf("remove previous embedded postgres init dir: %w", err)
	}
	if err := initDataDir(ctx, cfg, binaries, initDir); err != nil {
		_ = os.RemoveAll(initDir)
		return err
	}
	if !dataDirInitialized(initDir) {
		_ = os.RemoveAll(initDir)
		return fmt.Errorf("embedded postgres initdb completed without PG_VERSION in %s", initDir)
	}
	if err := os.Rename(initDir, cfg.DataDir); err != nil {
		return fmt.Errorf("promote embedded postgres data dir: %w", err)
	}
	return nil
}

func initDataDir(ctx context.Context, cfg contract.EmbeddedPostgresConfig, binaries postgresBinaries, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create embedded postgres data dir: %w", err)
	}
	return runCommand(ctx, binaries.initdb,
		"-D", dataDir,
		"-U", cfg.UserName,
		"-L", cfg.ShareDir,
		"-c", "log_timezone="+postgresFixedTZ,
		"-c", "timezone="+postgresFixedTZ,
		"--locale=C",
		"--auth=trust",
		"--encoding=UTF8",
	)
}

func writePostgresRuntimeConfig(cfg contract.EmbeddedPostgresConfig) error {
	lines := []string{
		"port = " + strconv.Itoa(cfg.Port),
		"log_timezone = '" + postgresFixedTZ + "'",
		"timezone = '" + postgresFixedTZ + "'",
		"",
	}
	if runtimeGOOS() == "windows" {
		lines = append([]string{"listen_addresses = '127.0.0.1'"}, lines...)
	} else {
		lines = append([]string{
			"listen_addresses = ''",
			"unix_socket_directories = '" + postgresConfigString(cfg.RuntimeDir) + "'",
		}, lines...)
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "postgresql.auto.conf"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write embedded postgres runtime config: %w", err)
	}
	return nil
}

func postgresConfigString(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, `\\`)
	return strings.ReplaceAll(value, "'", "''")
}

func postgresRunning(ctx context.Context, cfg contract.EmbeddedPostgresConfig, pgCtl string) (bool, error) {
	args := []string{"-D", cfg.DataDir, "status"}
	cmd := exec.CommandContext(ctx, pgCtl, args...)
	configurePostgresCommand(cmd)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == pgCtlStatusNotRunningExitCode {
		return false, nil
	}
	return false, commandError(pgCtl, args, err, output)
}

func markPostgresDataDirOwned(dataDir string) {
	ownedPostgresDataDirs.Store(filepath.Clean(dataDir), struct{}{})
}

func ownsPostgresDataDir(dataDir string) bool {
	_, ok := ownedPostgresDataDirs.Load(filepath.Clean(dataDir))
	return ok
}

func forgetPostgresDataDirOwned(dataDir string) {
	ownedPostgresDataDirs.Delete(filepath.Clean(dataDir))
}

func removeStalePostmasterPID(dataDir string) error {
	pidPath := filepath.Join(dataDir, "postmaster.pid")
	raw, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read embedded postgres postmaster.pid: %w", err)
	}
	pid, ok := parsePostmasterPID(raw)
	if ok && processExists(pid) {
		return nil
	}
	if err := os.Remove(pidPath); err != nil {
		return fmt.Errorf("remove stale embedded postgres postmaster.pid: %w", err)
	}
	return nil
}

func parsePostmasterPID(raw []byte) (int, bool) {
	line, _, _ := strings.Cut(string(raw), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	return pid, err == nil && pid > 0
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	configurePostgresCommand(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return commandError(name, args, err, output)
	}
	return nil
}

func runPostgresStartCommand(ctx context.Context, pgCtl string, cfg contract.EmbeddedPostgresConfig) error {
	args := []string{"-D", cfg.DataDir, "-l", cfg.LogPath, "-w", "-t", pgCtlTimeoutSeconds, "start"}
	cmd := exec.CommandContext(ctx, pgCtl, args...)
	configurePostgresCommand(cmd)
	if err := cmd.Run(); err != nil {
		return commandError(pgCtl, args, err, nil)
	}
	return nil
}

func commandError(name string, args []string, err error, output []byte) error {
	return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
}

var runtimeGOOS = func() string { return runtime.GOOS }
