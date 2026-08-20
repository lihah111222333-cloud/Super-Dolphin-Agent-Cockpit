package heterociexec

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

const (
	sqliteReceiptSchemaVersion = 1
	sqliteReceiptTable         = "heteroci_batch_receipts"
)

const sqliteReceiptTableDDL = `CREATE TABLE heteroci_batch_receipts (
    schema_version TEXT NOT NULL CHECK(schema_version = 'heteroci-batch-receipt/v1'),
    batch_id TEXT NOT NULL CHECK(length(batch_id) = 64 AND batch_id NOT GLOB '*[^0-9a-f]*'),
    material_digest TEXT NOT NULL CHECK(length(material_digest) = 71 AND substr(material_digest, 1, 7) = 'sha256:' AND substr(material_digest, 8) NOT GLOB '*[^0-9a-f]*'),
    command_digest TEXT NOT NULL CHECK(length(command_digest) = 71 AND substr(command_digest, 1, 7) = 'sha256:' AND substr(command_digest, 8) NOT GLOB '*[^0-9a-f]*'),
    state TEXT NOT NULL CHECK(state IN ('claimed', 'terminal')),
    claim_json BLOB NOT NULL,
    claim_digest TEXT NOT NULL CHECK(length(claim_digest) = 71 AND substr(claim_digest, 1, 7) = 'sha256:' AND substr(claim_digest, 8) NOT GLOB '*[^0-9a-f]*'),
    result_json BLOB,
    result_digest TEXT,
    PRIMARY KEY(batch_id, material_digest),
    CHECK((state = 'claimed' AND result_json IS NULL AND result_digest IS NULL) OR
          (state = 'terminal' AND result_json IS NOT NULL AND result_digest IS NOT NULL AND
           length(result_digest) = 71 AND substr(result_digest, 1, 7) = 'sha256:' AND substr(result_digest, 8) NOT GLOB '*[^0-9a-f]*'))
) WITHOUT ROWID`

// ErrSQLiteReceiptStore 表示独立本地 receipt SQLite authority 的输入或持久化状态无效。
var ErrSQLiteReceiptStore = errors.New("heteroci SQLite receipt store is invalid")

// ErrSQLiteReceiptConflict 表示同一 batch material 已被不同 command 或 terminal 结果占用。
var ErrSQLiteReceiptConflict = errors.New("heteroci SQLite receipt identity conflict")

// ErrSQLiteReceiptStoreClosed 表示调用方在 receipt authority 关闭后继续使用。
var ErrSQLiteReceiptStoreClosed = errors.New("heteroci SQLite receipt store is closed")

// SQLiteReceiptStore 是独立于 owner 与远程 duration ledger 的本地非权威 receipt authority。
type SQLiteReceiptStore struct {
	mu     sync.RWMutex
	db     *sql.DB
	path   string
	closed bool
}

// OpenSQLiteReceiptStore 从显式绝对路径打开并严格校验独立 receipt SQLite authority。
func OpenSQLiteReceiptStore(ctx context.Context, path string) (*SQLiteReceiptStore, error) {
	if err := validateSQLiteReceiptContext(ctx); err != nil {
		return nil, err
	}
	if err := validateSQLiteReceiptPath(path); err != nil {
		return nil, err
	}
	database, err := platformsqlite.Open(ctx, platformsqlite.OpenOptions{Path: path})
	if err != nil {
		return nil, fmt.Errorf("%w: open authority: %w", ErrSQLiteReceiptStore, err)
	}
	if err := ensureSQLiteReceiptSchema(ctx, database); err != nil {
		return nil, errors.Join(err, database.Close())
	}
	return &SQLiteReceiptStore{db: database, path: path}, nil
}

// AuthorityPath 返回构造时已校验的独立 authority 路径，供 composition root 做路径分离检查。
func (store *SQLiteReceiptStore) AuthorityPath() (string, error) {
	if store == nil {
		return "", ErrSQLiteReceiptStore
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.path == "" {
		return "", ErrSQLiteReceiptStore
	}
	return store.path, nil
}

// Close 关闭 store 拥有的数据库；重复关闭和底层关闭失败均显式返回错误。
func (store *SQLiteReceiptStore) Close() error {
	if store == nil {
		return ErrSQLiteReceiptStore
	}
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return ErrSQLiteReceiptStoreClosed
	}
	database := store.db
	store.db = nil
	store.closed = true
	store.mu.Unlock()
	if database == nil {
		return ErrSQLiteReceiptStore
	}
	if err := database.Close(); err != nil {
		return fmt.Errorf("%w: close authority: %w", ErrSQLiteReceiptStore, err)
	}
	return nil
}

// databaseHandle 返回当前可用数据库；关闭状态不能退化为 nil handle 或默认实现。
func (store *SQLiteReceiptStore) databaseHandle(ctx context.Context) (*sql.DB, error) {
	if err := validateSQLiteReceiptContext(ctx); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ErrSQLiteReceiptStore
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return nil, ErrSQLiteReceiptStoreClosed
	}
	if store.db == nil {
		return nil, ErrSQLiteReceiptStore
	}
	return store.db, nil
}

// ensureSQLiteReceiptSchema 创建空库或精确复核 v1 单表 schema，拒绝未知对象和漂移。
func ensureSQLiteReceiptSchema(ctx context.Context, database *sql.DB) error {
	if database == nil {
		return ErrSQLiteReceiptStore
	}
	return platformdb.WithImmediateTx(ctx, database, func(tx *sql.Tx) error {
		objects, err := inspectSQLiteReceiptObjects(ctx, tx)
		if err != nil {
			return err
		}
		if len(objects) == 0 {
			version, err := readSQLiteReceiptSchemaVersion(ctx, tx)
			if err != nil {
				return err
			}
			if version != 0 {
				return fmt.Errorf("%w: empty authority schema version drifted", ErrSQLiteReceiptStore)
			}
			if _, err := tx.ExecContext(ctx, sqliteReceiptTableDDL); err != nil {
				return fmt.Errorf("%w: create schema: %w", ErrSQLiteReceiptStore, err)
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", sqliteReceiptSchemaVersion)); err != nil {
				return fmt.Errorf("%w: set schema version: %w", ErrSQLiteReceiptStore, err)
			}
			return nil
		}
		if len(objects) != 1 || objects[sqliteReceiptTable] != "table" {
			return fmt.Errorf("%w: authority contains unknown schema objects", ErrSQLiteReceiptStore)
		}
		return validateSQLiteReceiptSchema(ctx, tx)
	})
}

// inspectSQLiteReceiptObjects 返回全部用户对象，并把 rows 关闭失败并入结果。
func inspectSQLiteReceiptObjects(ctx context.Context, tx *sql.Tx) (objects map[string]string, returnErr error) {
	rows, err := tx.QueryContext(ctx, "SELECT name, type FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("%w: inspect schema objects: %w", ErrSQLiteReceiptStore, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("%w: close schema rows: %w", ErrSQLiteReceiptStore, err))
		}
	}()
	objects = make(map[string]string)
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, fmt.Errorf("%w: scan schema object: %w", ErrSQLiteReceiptStore, err)
		}
		objects[name] = kind
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate schema objects: %w", ErrSQLiteReceiptStore, err)
	}
	return objects, nil
}

// validateSQLiteReceiptSchema 校验 user_version 与完整 CREATE TABLE 约束文本。
func validateSQLiteReceiptSchema(ctx context.Context, tx *sql.Tx) error {
	version, err := readSQLiteReceiptSchemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	if version != sqliteReceiptSchemaVersion {
		return fmt.Errorf("%w: schema version drifted", ErrSQLiteReceiptStore)
	}
	var definition string
	if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", sqliteReceiptTable).Scan(&definition); err != nil {
		return fmt.Errorf("%w: read table definition: %w", ErrSQLiteReceiptStore, err)
	}
	if normalizeSQLiteReceiptDDL(definition) != normalizeSQLiteReceiptDDL(sqliteReceiptTableDDL) {
		return fmt.Errorf("%w: table definition drifted", ErrSQLiteReceiptStore)
	}
	return nil
}

// readSQLiteReceiptSchemaVersion 读取独立 receipt authority 的 schema 版本。
func readSQLiteReceiptSchemaVersion(ctx context.Context, tx *sql.Tx) (int, error) {
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("%w: read schema version: %w", ErrSQLiteReceiptStore, err)
	}
	return version, nil
}

func normalizeSQLiteReceiptDDL(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// validateSQLiteReceiptContext 拒绝 nil 或已取消 context，避免发生任何持久化副作用。
func validateSQLiteReceiptContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrSQLiteReceiptStore)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context: %w", ErrSQLiteReceiptStore, err)
	}
	return nil
}

// validateSQLiteReceiptPath 只接受 clean 绝对路径，并拒绝现存 symlink 或非 regular 文件。
func validateSQLiteReceiptPath(path string) error {
	if path == "" || strings.TrimSpace(path) != path || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: authority path is invalid", ErrSQLiteReceiptStore)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%w: authority path must be absolute and clean", ErrSQLiteReceiptStore)
	}
	prefixes := sqliteReceiptPathPrefixes(path)
	for index, prefix := range prefixes {
		final := index == len(prefixes)-1
		if err := validateSQLiteReceiptPathComponent(prefix, final); err != nil {
			return err
		}
	}
	return nil
}

// sqliteReceiptPathPrefixes 返回从文件系统根到 SQLite authority 文件的 clean 路径前缀。
func sqliteReceiptPathPrefixes(path string) []string {
	separator := string(filepath.Separator)
	root := filepath.VolumeName(path) + separator
	prefixes := []string{root}
	current := root
	for component := range strings.SplitSeq(strings.TrimPrefix(path, root), separator) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		prefixes = append(prefixes, current)
	}
	return prefixes
}

// validateSQLiteReceiptPathComponent 拒绝路径组件中的链接、非目录父级和非普通最终文件。
func validateSQLiteReceiptPathComponent(path string, final bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && final {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: authority path component cannot be inspected", ErrSQLiteReceiptStore)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: authority path contains symbolic link", ErrSQLiteReceiptStore)
	}
	if final && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: authority path is not a regular file", ErrSQLiteReceiptStore)
	}
	if !final && !info.IsDir() {
		return fmt.Errorf("%w: authority parent is not a directory", ErrSQLiteReceiptStore)
	}
	return nil
}
