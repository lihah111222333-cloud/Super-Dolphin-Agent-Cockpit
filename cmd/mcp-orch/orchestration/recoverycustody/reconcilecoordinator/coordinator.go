package reconcilecoordinator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// Coordinator 持久化并运行一个 service-owned reconcile 队列。
type Coordinator struct {
	db        *sql.DB
	authority Authority
	cfg       Config
	mu        sync.Mutex
	fenced    atomic.Bool
}

// Bootstrap 创建全新的 coordinator 数据库；拒绝已有 schema，绝不隐式迁移 authority 或产品库。
func Bootstrap(ctx context.Context, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := validatePrivateFilesystem(cfg.Path, true); err != nil {
		return err
	}
	if err := ensureFreshPath(cfg.Path); err != nil {
		return err
	}
	db, err := platformsqlite.Open(ctx, platformsqlite.OpenOptions{Path: filepath.Clean(cfg.Path)})
	if err != nil {
		return err
	}
	defer db.Close()
	// SQLite creates WAL/SHM lazily; validate again after opening so newly
	// created artifacts cannot escape the coordinator's private filesystem.
	if err := validatePrivateFilesystem(cfg.Path, false); err != nil {
		return err
	}
	current := clockNow(cfg)
	now := canonicalTime(current)
	ownerLease := canonicalTime(current.Add(cfg.ClaimTTL))
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("%w: create schema: %v", ErrSchemaMismatch, err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO coordinator_meta(singleton, schema_version, created_at, updated_at) VALUES(1, ?, ?, ?)`,
		SchemaVersion, now, now); err != nil {
		return fmt.Errorf("%w: seed metadata: %v", ErrSchemaMismatch, err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO coordinator_owner(singleton, owner_id, owner_epoch, owner_token, lease_expires_at, updated_at) VALUES(1, ?, ?, ?, ?, ?)`,
		cfg.OwnerID, cfg.OwnerEpoch, cfg.OwnerToken, ownerLease, now); err != nil {
		return fmt.Errorf("%w: seed owner: %v", ErrSchemaMismatch, err)
	}
	if err := validatePrivateFilesystem(cfg.Path, false); err != nil {
		return err
	}
	return nil
}

func ensureFreshPath(path string) error {
	info, err := os.Stat(path)
	if err == nil && info.Size() > 0 {
		return fmt.Errorf("%w: coordinator database already exists", ErrSchemaMismatch)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Do not expose the configured path or filesystem internals in a startup
		// error; callers can classify the stable schema mismatch sentinel.
		return fmt.Errorf("%w: inspect coordinator database", ErrSchemaMismatch)
	}
	return nil
}

// Open 打开已显式 Bootstrap 的 service-owned coordinator 数据库。
func Open(ctx context.Context, cfg Config, authority Authority) (*Coordinator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateOpenInputs(cfg.Path, authority); err != nil {
		return nil, err
	}
	if err := validatePrivateFilesystem(cfg.Path, false); err != nil {
		return nil, err
	}
	var err error
	db, err := platformsqlite.Open(ctx, platformsqlite.OpenOptions{Path: cfg.Path})
	if err != nil {
		return nil, err
	}
	// Recheck after the driver has opened/configured WAL. This closes the
	// sidecar-creation window between the preflight Lstat and SQLite startup.
	if err := validatePrivateFilesystem(cfg.Path, false); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := verifyMetadata(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := verifyOwnerConfig(ctx, db, cfg); err != nil {
		db.Close()
		return nil, err
	}
	return &Coordinator{db: db, authority: authority, cfg: cfg}, nil
}

func validateOpenInputs(path string, authority Authority) error {
	if authority == nil {
		return fmt.Errorf("%w: authority dependency is required", ErrInvalidConfig)
	}
	return nil
}

func verifyMetadata(ctx context.Context, db *sql.DB) error {
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT schema_version FROM coordinator_meta WHERE singleton = 1`).Scan(&version); err != nil || version != SchemaVersion {
		return fmt.Errorf("%w: metadata", ErrSchemaMismatch)
	}
	return nil
}

func verifyOwnerConfig(ctx context.Context, db *sql.DB, cfg Config) error {
	var ownerID, token string
	var epoch int64
	if err := db.QueryRowContext(ctx, `SELECT owner_id, owner_epoch, owner_token FROM coordinator_owner WHERE singleton = 1`).Scan(&ownerID, &epoch, &token); err != nil {
		return ErrOwnerFenced
	}
	if ownerID != cfg.OwnerID || epoch != cfg.OwnerEpoch || token != cfg.OwnerToken {
		return ErrOwnerFenced
	}
	return nil
}

// Close 释放 coordinator 数据库连接，不改写 durable jobs。
func (c *Coordinator) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func clockNow(cfg Config) time.Time {
	if cfg.Now != nil {
		return cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func canonicalTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

// StartupScan 在 runner 启动前将 authority 全部 active resolution durable 入队。
func (c *Coordinator) StartupScan(ctx context.Context) error {
	if c.fenced.Load() {
		return ErrOwnerFenced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.verifyOwner(ctx); err != nil {
		return err
	}
	items, err := c.authority.ListActiveResolutionHandles(ctx)
	if err != nil {
		return err
	}
	for _, handle := range items {
		if err := c.enqueue(ctx, handle); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) verifyOwner(ctx context.Context) error {
	var id, token string
	var epoch int64
	if err := c.db.QueryRowContext(ctx, `SELECT owner_id, owner_epoch, owner_token FROM coordinator_owner WHERE singleton=1`).Scan(&id, &epoch, &token); err != nil {
		return ErrSchemaMismatch
	}
	if id != c.cfg.OwnerID || epoch != c.cfg.OwnerEpoch || token != c.cfg.OwnerToken {
		return ErrOwnerFenced
	}
	return nil
}

func (c *Coordinator) enqueue(ctx context.Context, handle recoverycustody.SubmissionHandle) error {
	if c.fenced.Load() {
		return ErrOwnerFenced
	}
	if err := handle.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(handle)
	if err != nil {
		return err
	}
	now := canonicalTime(clockNow(c.cfg))
	jobID := "reconcile/" + handle.OperationID
	return c.withImmediate(ctx, func(conn *sql.Conn) error {
		var existing string
		err := conn.QueryRowContext(ctx, `SELECT handle_json FROM reconcile_jobs WHERE operation_id = ?`, handle.OperationID).Scan(&existing)
		if err == nil {
			if existing != string(raw) {
				return ErrJobConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO reconcile_jobs(job_id, operation_id, handle_json, state, attempts, next_run_at, created_at, updated_at) VALUES(?, ?, ?, 'queued', 0, ?, ?, ?)`, jobID, handle.OperationID, string(raw), now, now, now); err != nil {
			return err
		}
		return c.appendJournal(ctx, conn, jobID, "enqueued", StateQueued, "", "startup active-resolution scan", now)
	})
}

// Enqueue 为 service bootstrap 暴露幂等入队；只接受已持久化句柄，不调用 Submit 或 Resolve。
func (c *Coordinator) Enqueue(ctx context.Context, handle recoverycustody.SubmissionHandle) error {
	return c.enqueue(ctx, handle)
}

// Run 启动有界轮询 runner，在 context 取消或首个持久化/authority 错误时退出。
func (c *Coordinator) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.RunnerPeriod)
	defer ticker.Stop()
	for {
		_, err := c.RunOnce(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
