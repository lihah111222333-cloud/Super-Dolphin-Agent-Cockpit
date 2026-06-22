package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/securefs"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

const (
	driverName            = "sqlite"
	busyTimeoutMillis     = 5000
	walAutoCheckpointPage = 1000
)

type OpenOptions struct {
	Path string
}

// Open 打开并校验 SQLite 数据库，启动期完成路径、权限和 PRAGMA 检查。
// 任何文件系统或持久化能力异常都直接返回错误，避免运行时才暴露写入失败。
func Open(ctx context.Context, opts OpenOptions) (*sql.DB, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return nil, fmt.Errorf("SQLite path is empty")
	}
	if err := prepareFilesystem(path); err != nil {
		return nil, err
	}
	if err := ensureDatabaseFile(path); err != nil {
		return nil, err
	}

	db, err := sql.Open(driverName, sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open SQLite database %s: %s", redactPath(path), securefs.SafeErrorForPath(err, path))
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping SQLite database %s: %s", redactPath(path), securefs.SafeErrorForPath(err, path))
	}
	if err := configureAndVerifyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure SQLite database %s: %w", redactPath(path), err)
	}
	if err := RestrictSidecarFilePermissions(path); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("restrict SQLite database sidecar permissions %s: %w", redactPath(path), err)
	}
	return db, nil
}

func sqliteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout=%d", busyTimeoutMillis))
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	q.Add("_pragma", "synchronous=FULL")
	q.Add("_pragma", fmt.Sprintf("wal_autocheckpoint=%d", walAutoCheckpointPage))
	return path + "?" + q.Encode()
}

func configureAndVerifyPragmas(ctx context.Context, db *sql.DB) error {
	if err := configurePragmas(ctx, db); err != nil {
		return err
	}
	return verifyPragmas(ctx, db)
}

// configurePragmas 写入运行期必须保持一致的 SQLite PRAGMA。
// 这些配置影响外键、WAL 和同步级别，任一失败都必须阻断数据库启动。
func configurePragmas(ctx context.Context, db *sql.DB) error {
	if err := execPragma(ctx, db, "PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	if err := execPragma(ctx, db, "PRAGMA journal_mode = WAL"); err != nil {
		return err
	}
	if err := execPragma(ctx, db, fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMillis)); err != nil {
		return err
	}
	if err := execPragma(ctx, db, "PRAGMA synchronous = FULL"); err != nil {
		return err
	}
	if err := execPragma(ctx, db, fmt.Sprintf("PRAGMA wal_autocheckpoint = %d", walAutoCheckpointPage)); err != nil {
		return err
	}
	return nil
}

// verifyPragmas 复核 SQLite 连接实际生效的 PRAGMA，防止驱动或环境忽略关键配置。
func verifyPragmas(ctx context.Context, db *sql.DB) error {
	if err := verifyIntPragma(ctx, db, "foreign_keys", 1); err != nil {
		return err
	}
	if err := verifyTextPragma(ctx, db, "journal_mode", "wal"); err != nil {
		return err
	}
	if err := verifyIntPragma(ctx, db, "busy_timeout", busyTimeoutMillis); err != nil {
		return err
	}
	if err := verifyIntPragma(ctx, db, "synchronous", 2); err != nil {
		return err
	}
	if err := verifyIntPragma(ctx, db, "wal_autocheckpoint", walAutoCheckpointPage); err != nil {
		return err
	}
	return nil
}

func execPragma(ctx context.Context, db *sql.DB, stmt string) error {
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("apply SQLite %s: %w", stmt, err)
	}
	return nil
}

func verifyIntPragma(ctx context.Context, db *sql.DB, name string, want int) error {
	var got int
	if err := db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
		return fmt.Errorf("verify SQLite PRAGMA %s: %w", name, err)
	}
	if got != want {
		return fmt.Errorf("verify SQLite PRAGMA %s: got %d, want %d", name, got, want)
	}
	return nil
}

func verifyTextPragma(ctx context.Context, db *sql.DB, name, want string) error {
	var got string
	if err := db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
		return fmt.Errorf("verify SQLite PRAGMA %s: %w", name, err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("verify SQLite PRAGMA %s: got %q, want %q", name, got, want)
	}
	return nil
}

// prepareFilesystem 在打开数据库前准备并校验父目录和已有 DB 文件。
// 父路径错误优先定位到父目录；已有 DB 文件必须在启动期通过读写探测。
func prepareFilesystem(path string) error {
	clean := filepath.Clean(path)
	parent := filepath.Dir(clean)
	parentCreated, err := ensureParentDirectory(parent)
	if err != nil {
		return err
	}
	if parentCreated {
		if err := securefs.RestrictOwnerOnly(parent, 0o700); err != nil {
			return err
		}
	} else if err := validateExistingParentDirectory(parent); err != nil {
		return err
	}
	return validateExistingDatabasePath(clean)
}

func validateExistingDatabasePath(clean string) error {
	info, err := os.Stat(clean)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect SQLite database path %s: %s", redactPath(clean), securefs.SafeErrorForPath(err, clean))
	}
	if info.IsDir() {
		return fmt.Errorf("SQLite database path points to a directory: %s", redactPath(clean))
	}
	if err := probeExistingDatabaseWritable(clean); err != nil {
		return err
	}
	return securefs.CheckExistingOwnerOnly(clean, info)
}

// probeExistingDatabaseWritable 在打开 SQLite 前验证已有 DB 文件可由当前进程读写。
// 这里不创建、不截断、不改权限；失败必须立即阻断，避免应用带着半可用持久化能力启动。
func probeExistingDatabaseWritable(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("SQLite database file is not writable: %s: %s", redactPath(path), securefs.SafeErrorForPath(err, path))
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close SQLite database file %s: %s", redactPath(path), securefs.SafeErrorForPath(err, path))
	}
	return nil
}

func ensureParentDirectory(parent string) (bool, error) {
	info, err := os.Stat(parent)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return false, fmt.Errorf("create SQLite parent directory %s: %s", redactPath(parent), securefs.SafeErrorForPath(err, parent))
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect SQLite parent directory %s: %s", redactPath(parent), securefs.SafeErrorForPath(err, parent))
	}
	if !info.IsDir() {
		return false, fmt.Errorf("SQLite database parent is not a directory: %s", redactPath(parent))
	}
	return false, nil
}

func validateExistingParentDirectory(parent string) error {
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect SQLite parent directory %s: %s", redactPath(parent), securefs.SafeErrorForPath(err, parent))
	}
	if err := securefs.CheckExistingOwnerOnly(parent, info); err != nil {
		return err
	}
	if err := securefs.ProbeWritableDir(parent); err != nil {
		return fmt.Errorf("SQLite parent directory is not writable: %s", redactPath(parent))
	}
	return nil
}

// ensureDatabaseFile 只在 DB 文件不存在时创建它，并立即收紧为 owner-only 权限。
// 已存在文件的可写性和权限在 prepareFilesystem 中完成，避免这里静默放行半可用状态。
func ensureDatabaseFile(path string) error {
	clean := filepath.Clean(path)
	if _, err := os.Stat(clean); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect SQLite database path %s: %s", redactPath(clean), securefs.SafeErrorForPath(err, clean))
	}
	file, err := os.OpenFile(clean, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create SQLite database file %s: %s", redactPath(clean), securefs.SafeErrorForPath(err, clean))
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close SQLite database file %s: %s", redactPath(clean), securefs.SafeErrorForPath(err, clean))
	}
	return securefs.RestrictOwnerOnly(clean, 0o600)
}

// RestrictSidecarFilePermissions 检查 SQLite 主文件和 WAL/SHM sidecar 的权限边界。
// 已存在的 sidecar 不会被静默改权限；权限不满足 owner-only 时直接失败。
func RestrictSidecarFilePermissions(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect SQLite file %s: %s", redactPath(candidate), securefs.SafeErrorForPath(err, candidate))
		}
		if info.IsDir() {
			return fmt.Errorf("SQLite file path points to a directory: %s", redactPath(candidate))
		}
		if err := securefs.CheckExistingOwnerOnly(candidate, info); err != nil {
			return err
		}
	}
	return nil
}

func redactPath(path string) string {
	return securefs.RedactPath(path)
}

// OpenTest 用测试固定超时打开 SQLite，避免测试因外部上下文缺失而无限等待。
func OpenTest(ctx context.Context, path string) (*sql.DB, error) {
	ctx, cancel := platformconfig.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return Open(ctx, OpenOptions{Path: path})
}
