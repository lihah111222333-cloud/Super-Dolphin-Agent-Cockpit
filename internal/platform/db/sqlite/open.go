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
)

const (
	driverName            = "sqlite"
	busyTimeoutMillis     = 5000
	walAutoCheckpointPage = 1000
)

type OpenOptions struct {
	Path string
}

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

func prepareFilesystem(path string) error {
	clean := filepath.Clean(path)
	if err := validateExistingDatabasePath(clean); err != nil {
		return err
	}

	parent := filepath.Dir(clean)
	parentCreated, err := ensureParentDirectory(parent)
	if err != nil {
		return err
	}
	if parentCreated {
		return securefs.RestrictOwnerOnly(parent, 0o700)
	}
	return validateExistingParentDirectory(parent)
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
	return securefs.CheckExistingOwnerOnly(clean, info)
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

func OpenTest(ctx context.Context, path string) (*sql.DB, error) {
	ctx, cancel := platformconfig.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return Open(ctx, OpenOptions{Path: path})
}
