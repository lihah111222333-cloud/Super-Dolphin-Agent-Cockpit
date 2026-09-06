package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	"go.uber.org/fx"
)

type serveOptions struct{ configPath, readyPath string }

const instanceStateCheckInterval = 100 * time.Millisecond

// runCommand 仅处理显式独立子命令；未知参数不会落回 V3 sidecar 模式。
func runCommand(args []string) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("unknown orch command; expected serve --config <absolute path> --ready-file <absolute path>")
	}
	options, err := parseServeOptions(args[1:])
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServe(ctx, options)
}

// parseServeOptions 拒绝未知参数、重复参数和隐式配置路径。
func parseServeOptions(args []string) (serveOptions, error) {
	var options serveOptions
	if err := validateServeFlagUniqueness(args); err != nil {
		return options, err
	}
	flags := flag.NewFlagSet("orch serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.configPath, "config", "", "absolute config path")
	flags.StringVar(&options.readyPath, "ready-file", "", "absolute readiness path")
	if err := flags.Parse(args); err != nil {
		return options, fmt.Errorf("orch serve flag: %w", err)
	}
	if flags.NArg() != 0 {
		return options, errors.New("orch serve does not accept positional arguments")
	}
	if !filepath.IsAbs(options.configPath) || !filepath.IsAbs(options.readyPath) {
		return options, errors.New("orch serve config and ready-file must both be absolute paths")
	}
	return options, nil
}

// validateServeFlagUniqueness 拒绝配置参数重复，避免后写值覆盖先前身份。
func validateServeFlagUniqueness(args []string) error {
	seen := make(map[string]bool)
	for _, arg := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") || (name != "config" && name != "ready-file") {
			continue
		}
		if seen[name] {
			return errors.New("duplicate orch serve flag")
		}
		seen[name] = true
	}
	return nil
}

// runServe 按配置、凭据、唯一 owner、数据库、监听顺序启动，锁最后释放。
func runServe(ctx context.Context, options serveOptions) (err error) {
	cfg, err := standalone.LoadConfig(options.configPath)
	if err != nil {
		return err
	}
	issuer, err := standalone.NewConfiguredPrincipalIssuer(cfg)
	if err != nil {
		return err
	}
	credential, err := standalone.ReadCredential(cfg.CredentialFile)
	if err != nil {
		return err
	}
	lock, err := standalone.AcquireInstance(cfg.StateDir)
	if err != nil {
		return err
	}
	// The lock resolves and pins the physical state root. Bind the config to
	// that exact path before opening SQLite so an alias cannot be swapped to a
	// different directory between instance acquisition and database startup.
	cfg.StateDir = lock.CanonicalStateDir()
	defer func() { err = errors.Join(err, lock.Close()) }()
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, err := openStandaloneDatabaseWithLock(startCtx, cfg, lock)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	operatorSource, err := activateStandaloneOperatorSource(startCtx, db, issuer)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		err = errors.Join(err, operatorSource.Close(closeCtx))
	}()
	app := newStandaloneAppWithPrincipalIssuer(cfg, credential, options.readyPath, db, operatorSource, lock)
	if err := app.Err(); err != nil {
		return err
	}
	if err := app.Start(startCtx); err != nil {
		return err
	}
	return waitForStandaloneShutdown(ctx, app, lock)
}

// waitForStandaloneShutdown 等待根 context、应用退出或状态根漂移，然后按统一超时停止应用。
func waitForStandaloneShutdown(ctx context.Context, app *fx.App, lock *standalone.InstanceLock) error {
	stateGuardCtx, stopStateGuard := context.WithCancel(context.Background())
	stateGuardErr := monitorInstanceState(stateGuardCtx, lock, instanceStateCheckInterval)
	select {
	case <-ctx.Done():
	case <-app.Wait():
	case err := <-stateGuardErr:
		// A Unix directory flock remains attached to the renamed inode. Stop
		// the daemon promptly if the configured path is replaced so a new
		// owner cannot run against a different state root concurrently.
		err = fmt.Errorf("orch instance state root drift: %w", err)
		stopStateGuard()
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return errors.Join(err, app.Stop(stopCtx))
	}
	stopStateGuard()
	stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	return app.Stop(stopCtx)
}

// monitorInstanceState 检查实例锁绑定的状态根身份，漂移时向调用方报告致命错误。
func monitorInstanceState(ctx context.Context, lock *standalone.InstanceLock, interval time.Duration) <-chan error {
	result := make(chan error, 1)
	if lock == nil {
		result <- errors.New("orch instance state monitor requires the instance lock")
		return result
	}
	if interval <= 0 {
		result <- errors.New("orch instance state monitor interval must be positive")
		return result
	}
	if ctx == nil {
		result <- errors.New("orch instance state monitor requires context")
		return result
	}
	safego.Go(ctx, nil, "mcp-orch.serve.instance-state-monitor", func(monitorCtx context.Context) {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				if err := lock.ValidateStateDir(); err != nil {
					result <- err
					return
				}
			}
		}
	})
	return result
}

// openStandaloneDatabase 在交给根入口前完成迁移；任一初始化错误均释放已打开的句柄。
func openStandaloneDatabase(ctx context.Context, cfg standalone.Config) (*sql.DB, error) {
	return openStandaloneDatabaseWithLock(ctx, cfg, nil)
}

// openStandaloneDatabaseWithLock 在 InstanceLock 绑定的物理状态根内完成
// SQLite 打开、迁移和 schema 校验；锁前后任一目录项身份漂移都会阻断启动。
func openStandaloneDatabaseWithLock(ctx context.Context, cfg standalone.Config, lock *standalone.InstanceLock) (*sql.DB, error) {
	var db *sql.DB
	open := func() error {
		var err error
		db, err = newStandaloneDB(newStandalonePlatformConfig(cfg))
		if err != nil {
			return err
		}
		if err := sqliteruntime.RunEmbeddedMigrations(ctx, db); err != nil {
			return errors.Join(err, db.Close())
		}
		if err := platformdb.VerifyMinSchemaVersion(ctx, db); err != nil {
			return errors.Join(err, db.Close())
		}
		return nil
	}
	if lock != nil {
		if err := lock.WithDatabasePath(filepath.Join(cfg.StateDir, "orch.sqlite"), open); err != nil {
			if db != nil {
				_ = db.Close()
			}
			return nil, err
		}
		return db, nil
	}
	db, err := newStandaloneDB(newStandalonePlatformConfig(cfg))
	if err != nil {
		return nil, err
	}
	if err := sqliteruntime.RunEmbeddedMigrations(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := platformdb.VerifyMinSchemaVersion(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}
