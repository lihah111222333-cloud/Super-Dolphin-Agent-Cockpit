package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

const (
	sqliteDiagnosticsSource   = "mcp-lsp-sqlite"
	sqliteDiagnosticsMaxCache = 128
	sqliteSchemaCacheCapacity = 8
	sqliteSnapshotMaxAttempts = 3
)

type sqliteSchemaCacheEntry struct {
	db       *sql.DB
	refs     int
	lastUsed uint64
}

var sqlcParameterPattern = regexp.MustCompile(`sqlc\.(?:arg|narg|slice)\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)`)

// sqliteDiagnosticsState owns all mutable SQLite diagnostics caches for one client.
type sqliteDiagnosticsState struct {
	validationCache struct {
		sync.Mutex
		entries map[string][]protocol.Diagnostic
	}
	migrationChainCache struct {
		sync.Mutex
		entries map[string][]protocol.Diagnostic
	}
	schemaDBCache struct {
		sync.Mutex
		entries map[string]*sqliteSchemaCacheEntry
		clock   uint64
	}
}

func newSQLiteDiagnosticsState() *sqliteDiagnosticsState {
	return &sqliteDiagnosticsState{
		validationCache: struct {
			sync.Mutex
			entries map[string][]protocol.Diagnostic
		}{entries: make(map[string][]protocol.Diagnostic)},
		migrationChainCache: struct {
			sync.Mutex
			entries map[string][]protocol.Diagnostic
		}{entries: make(map[string][]protocol.Diagnostic)},
		schemaDBCache: struct {
			sync.Mutex
			entries map[string]*sqliteSchemaCacheEntry
			clock   uint64
		}{entries: make(map[string]*sqliteSchemaCacheEntry)},
	}
}

const sqliteDiagnosticsModule = "module github.com/lihah111222333-cloud/super-dolphin-agent"

// isSQLiteDiagnosticsWorkspace 只在当前产品仓库布局中启用 SQLite 组合诊断，避免影响 generic SQL peer。
func isSQLiteDiagnosticsWorkspace(root string) (bool, error) {
	moduleFile := filepath.Join(root, "go.mod")
	body, err := os.ReadFile(moduleFile)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read SQLite diagnostics module marker: %w", err)
	}
	if !strings.Contains(string(body), sqliteDiagnosticsModule) {
		return false, nil
	}
	required := []string{
		filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations", "001_baseline.sql"),
		filepath.Join(root, "cmd", "mcp-orch", "sql", "schema_sqlc_patch.sql"),
	}
	for _, path := range required {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false, fmt.Errorf("inspect SQLite diagnostics workspace marker %s: %w", path, statErr)
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("SQLite diagnostics workspace marker is not a regular file: %s", path)
		}
	}
	return true, nil
}

// isSQLiteDiagnosticsPath 判断路径是否属于仓库当前 SQLite SQL 真值面。
func isSQLiteDiagnosticsPath(root, path string) bool {
	root, err := canonicalSQLiteDiagnosticsPath(root)
	if err != nil {
		return false
	}
	path, err = canonicalSQLiteDiagnosticsPath(path)
	if err != nil {
		return false
	}
	rel, ok := sqliteDiagnosticsRelativePath(root, path)
	if !ok {
		return false
	}
	return strings.HasPrefix(rel, "internal/platform/db/sqlite/") ||
		strings.HasPrefix(rel, "cmd/mcp-orch/sql/") ||
		strings.HasPrefix(rel, "sql/queries/")
}

// sqliteDiagnosticsRelativePath 生成受根目录约束且扩展名为 SQL 的规范相对路径。
func sqliteDiagnosticsRelativePath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasSuffix(strings.ToLower(rel), ".sql") {
		return "", false
	}
	return rel, true
}

// canonicalSQLiteDiagnosticsPath 统一 macOS /var 物理别名和未落盘文件的父目录符号链接。
func canonicalSQLiteDiagnosticsPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute SQLite diagnostics path: %w", err)
	}
	current := abs
	missing := make([]string, 0, 4)
	for {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", fmt.Errorf("resolve SQLite diagnostics path aliases: %w", resolveErr)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve SQLite diagnostics path aliases: %w", resolveErr)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// validateSQLiteDocument 使用生产同款 SQLite 引擎校验迁移与 sqlc 查询。
// 基础设施错误直接返回，SQL 错误转换成 Error diagnostics。
func (s *sqliteDiagnosticsState) validateSQLiteDocument(
	ctx context.Context,
	root string,
	uri string,
	text string,
) ([]protocol.Diagnostic, error) {
	path, err := format.AbsolutePathFromURI(uri)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite diagnostics URI: %w", err)
	}
	root, err = canonicalSQLiteDiagnosticsPath(root)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite diagnostics root: %w", err)
	}
	path, err = canonicalSQLiteDiagnosticsPath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite diagnostics path: %w", err)
	}
	if !isSQLiteDiagnosticsPath(root, path) {
		return nil, fmt.Errorf("SQLite diagnostics path is outside supported roots: %s", path)
	}
	fingerprint, err := sqliteValidationFingerprint(root, path, text)
	if err != nil {
		return nil, err
	}
	if cached, ok := s.loadSQLiteValidationCache(fingerprint); ok {
		return cached, nil
	}

	diagnostics, err := s.runSQLiteDocumentValidation(ctx, root, path, text)
	if err != nil {
		return nil, err
	}
	s.storeSQLiteValidationCache(fingerprint, diagnostics)
	return cloneSQLiteDiagnostics(diagnostics), nil
}

// runSQLiteDocumentValidation 按 SQL 资产类型选择对应的真实 SQLite 校验上下文。
func (s *sqliteDiagnosticsState) runSQLiteDocumentValidation(
	ctx context.Context,
	root string,
	path string,
	text string,
) ([]protocol.Diagnostic, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite diagnostics relative path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	switch {
	case strings.HasPrefix(rel, "internal/platform/db/sqlite/migrations/"):
		return s.validateSQLiteMigration(ctx, root, path, text)
	case strings.HasPrefix(rel, "sql/queries/"), strings.HasPrefix(rel, "cmd/mcp-orch/sql/queries/"):
		return s.validateSQLiteQueries(ctx, root, text)
	case rel == "cmd/mcp-orch/sql/schema_sqlc_patch.sql":
		return validateSQLiteSchemaPatch(ctx, root, text)
	case strings.HasPrefix(rel, "internal/platform/db/sqlite/testdata/"):
		return validateSQLiteFixture(ctx, root, text)
	default:
		return validateSQLiteStandaloneSQL(ctx, text)
	}
}

// validateSQLiteMigration 对未改磁盘迁移复用整链结果；编辑态文件仍在临时整链中验证。
func (s *sqliteDiagnosticsState) validateSQLiteMigration(
	ctx context.Context,
	root string,
	path string,
	text string,
) ([]protocol.Diagnostic, error) {
	sourceDir := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations")
	if filepath.Base(path) == "112_system_logs_trace_span.sql" {
		diagnostics, validateErr := validateSQLiteDeclarativeSpecialMigration(ctx, sourceDir, text)
		if validateErr != nil || len(diagnostics) > 0 {
			return diagnostics, validateErr
		}
	}
	unchanged, err := sqliteMigrationTextMatchesDisk(path, text)
	if err != nil {
		return nil, err
	}
	if unchanged {
		return s.validateSQLiteMigrationChainCached(ctx, sourceDir)
	}
	return validateEditedSQLiteMigration(ctx, sourceDir, path, text)
}

func sqliteMigrationTextMatchesDisk(path, text string) (bool, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read SQLite diagnostics target migration %s: %w", path, err)
	}
	return bytes.Equal(body, []byte(text)), nil
}

// validateEditedSQLiteMigration 在临时整链中替换编辑态迁移并调用生产迁移器。
func validateEditedSQLiteMigration(ctx context.Context, sourceDir, path, text string) ([]protocol.Diagnostic, error) {
	tempDir, err := os.MkdirTemp("", "mcp-lsp-sqlite-migrations-")
	if err != nil {
		return nil, fmt.Errorf("create SQLite diagnostics migration directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := stageSQLiteMigrations(sourceDir, tempDir, path, text); err != nil {
		return nil, err
	}
	return runSQLiteMigrationChain(ctx, tempDir)
}

// validateSQLiteMigrationChainCached 按迁移目录内容指纹只执行一次未改整链，避免全仓诊断 O(N²)。
func (s *sqliteDiagnosticsState) validateSQLiteMigrationChainCached(ctx context.Context, sourceDir string) ([]protocol.Diagnostic, error) {
	s.migrationChainCache.Lock()
	defer s.migrationChainCache.Unlock()
	for attempt := 0; attempt < sqliteSnapshotMaxAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		key, err := sqliteMigrationChainFingerprint(sourceDir)
		if err != nil {
			return nil, err
		}
		if diagnostics, ok := s.migrationChainCache.entries[key]; ok {
			return cloneSQLiteDiagnostics(diagnostics), nil
		}
		diagnostics, err := runSQLiteMigrationChain(ctx, sourceDir)
		if err != nil {
			return nil, err
		}
		verifiedKey, err := sqliteMigrationChainFingerprint(sourceDir)
		if err != nil {
			return nil, err
		}
		if verifiedKey != key {
			continue
		}
		if len(s.migrationChainCache.entries) >= sqliteSchemaCacheCapacity {
			clear(s.migrationChainCache.entries)
		}
		s.migrationChainCache.entries[key] = cloneSQLiteDiagnostics(diagnostics)
		return diagnostics, nil
	}
	return nil, fmt.Errorf("SQLite migration chain changed during %d validation attempts", sqliteSnapshotMaxAttempts)
}

func sqliteMigrationChainFingerprint(sourceDir string) (string, error) {
	hash := sha256.New()
	if err := writeSQLiteDependencyFingerprint(hash, sourceDir); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func runSQLiteMigrationChain(ctx context.Context, migrationsDir string) ([]protocol.Diagnostic, error) {
	db, err := openSQLiteValidationDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	migrationErr := sqliteruntime.RunMigrations(ctx, db, migrationsDir)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return sqliteDiagnosticsForError(0, migrationErr), nil
}

// validateSQLiteDeclarativeSpecialMigration 校验由 Go 迁移器执行、同时作为 schema 契约保存的 SQL 正文。
func validateSQLiteDeclarativeSpecialMigration(ctx context.Context, stagedDir, text string) ([]protocol.Diagnostic, error) {
	prefixDir, err := os.MkdirTemp("", "mcp-lsp-sqlite-prefix-")
	if err != nil {
		return nil, fmt.Errorf("create SQLite diagnostics prefix directory: %w", err)
	}
	defer os.RemoveAll(prefixDir)
	if err := copySQLiteMigrationPrefix(stagedDir, prefixDir, "112_system_logs_trace_span.sql"); err != nil {
		return nil, err
	}

	db, err := openSQLiteValidationDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := sqliteruntime.RunMigrations(ctx, db, prefixDir); err != nil {
		return nil, fmt.Errorf("initialize SQLite diagnostics special migration schema: %w", err)
	}
	return executeSQLiteDiagnosticSegments(ctx, db, text)
}

// copySQLiteMigrationPrefix 复制特殊迁移之前的完整 schema 前缀。
func copySQLiteMigrationPrefix(sourceDir, targetDir, stopName string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("read SQLite diagnostics staged migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() >= stopName {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if readErr != nil {
			return fmt.Errorf("read SQLite diagnostics prefix migration %s: %w", entry.Name(), readErr)
		}
		if writeErr := os.WriteFile(filepath.Join(targetDir, entry.Name()), body, 0o600); writeErr != nil {
			return fmt.Errorf("stage SQLite diagnostics prefix migration %s: %w", entry.Name(), writeErr)
		}
	}
	return nil
}

// executeSQLiteDiagnosticSegments 按迁移分段执行编辑态 SQL，并把语法错误转换成 LSP Error。
func executeSQLiteDiagnosticSegments(ctx context.Context, db *sql.DB, text string) ([]protocol.Diagnostic, error) {
	for segment := range strings.SplitSeq(text, "-- SPLIT --") {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		_, execErr := db.ExecContext(ctx, segment)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		diagnostics := sqliteDiagnosticsForError(0, execErr)
		if len(diagnostics) > 0 {
			return diagnostics, nil
		}
	}
	return nil, nil
}

// stageSQLiteMigrations 复制完整迁移链，并用编辑态文本替换目标文件。
func stageSQLiteMigrations(sourceDir, targetDir, editedPath, editedText string) error {
	sourceDir = filepath.Clean(sourceDir)
	editedPath = filepath.Clean(editedPath)
	if filepath.Dir(editedPath) != sourceDir {
		return fmt.Errorf("SQLite diagnostics migration must be directly under %s: %s", sourceDir, editedPath)
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("read SQLite diagnostics migrations: %w", err)
	}
	stagedEditedFile := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") {
			continue
		}
		staged, stageErr := stageSQLiteMigrationFile(sourceDir, targetDir, entry.Name(), editedPath, editedText)
		if stageErr != nil {
			return stageErr
		}
		stagedEditedFile = stagedEditedFile || staged
	}
	if !stagedEditedFile {
		name := filepath.Base(editedPath)
		if writeErr := os.WriteFile(filepath.Join(targetDir, name), []byte(editedText), 0o600); writeErr != nil {
			return fmt.Errorf("stage new SQLite diagnostics migration %s: %w", name, writeErr)
		}
	}
	return nil
}

// stageSQLiteMigrationFile 复制单个迁移文件，并在命中编辑目标时替换为内存文本。
func stageSQLiteMigrationFile(sourceDir, targetDir, name, editedPath, editedText string) (bool, error) {
	body, err := os.ReadFile(filepath.Join(sourceDir, name))
	if err != nil {
		return false, fmt.Errorf("read SQLite diagnostics migration %s: %w", name, err)
	}
	stagedEditedFile := filepath.Clean(filepath.Join(sourceDir, name)) == editedPath
	if stagedEditedFile {
		body = []byte(editedText)
	}
	if err := os.WriteFile(filepath.Join(targetDir, name), body, 0o600); err != nil {
		return false, fmt.Errorf("stage SQLite diagnostics migration %s: %w", name, err)
	}
	return stagedEditedFile, nil
}

// validateSQLiteQueries 在完整迁移 schema 上逐个 prepare sqlc 查询块。
func (s *sqliteDiagnosticsState) validateSQLiteQueries(ctx context.Context, root, text string) ([]protocol.Diagnostic, error) {
	db, release, err := s.sqliteDiagnosticsSchemaDB(ctx, root)
	if err != nil {
		return nil, err
	}
	defer release()

	blocks := splitSQLiteQueryBlocks(text)
	diagnostics := make([]protocol.Diagnostic, 0)
	for _, block := range blocks {
		statement := normalizeSQLCParameters(block.text)
		if strings.TrimSpace(stripSQLiteLineComments(statement)) == "" {
			continue
		}
		prepared, prepareErr := db.PrepareContext(ctx, statement)
		if prepareErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			if errors.Is(prepareErr, context.Canceled) || errors.Is(prepareErr, context.DeadlineExceeded) {
				return nil, prepareErr
			}
			diagnostics = append(diagnostics, sqliteErrorDiagnostic(block.startLine, prepareErr))
			continue
		}
		if closeErr := prepared.Close(); closeErr != nil {
			return nil, fmt.Errorf("close SQLite diagnostics statement: %w", closeErr)
		}
	}
	return diagnostics, nil
}

// sqliteDiagnosticsSchemaDB 按迁移内容指纹复用只读 prepare schema，避免每个查询重放整链。
func (s *sqliteDiagnosticsState) sqliteDiagnosticsSchemaDB(ctx context.Context, root string) (*sql.DB, func(), error) {
	migrationsDir := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations")
	for attempt := 0; attempt < sqliteSnapshotMaxAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		key, err := sqliteMigrationChainFingerprint(migrationsDir)
		if err != nil {
			return nil, nil, err
		}
		if db, release := s.acquireCachedSQLiteSchemaDB(key); db != nil {
			return db, release, nil
		}
		db, err := openSQLiteValidationDB()
		if err != nil {
			return nil, nil, err
		}
		if err := sqliteruntime.RunMigrations(ctx, db, migrationsDir); err != nil {
			_ = db.Close()
			return nil, nil, fmt.Errorf("initialize SQLite diagnostics schema: %w", err)
		}
		verifiedKey, err := sqliteMigrationChainFingerprint(migrationsDir)
		if err != nil {
			_ = db.Close()
			return nil, nil, err
		}
		if verifiedKey != key {
			_ = db.Close()
			continue
		}
		cachedDB, release := s.cacheSQLiteSchemaDB(key, db)
		return cachedDB, release, nil
	}
	return nil, nil, fmt.Errorf("SQLite migrations changed during %d schema build attempts", sqliteSnapshotMaxAttempts)
}

func (s *sqliteDiagnosticsState) acquireCachedSQLiteSchemaDB(key string) (*sql.DB, func()) {
	s.schemaDBCache.Lock()
	defer s.schemaDBCache.Unlock()
	entry := s.schemaDBCache.entries[key]
	if entry == nil {
		return nil, nil
	}
	s.schemaDBCache.clock++
	entry.refs++
	entry.lastUsed = s.schemaDBCache.clock
	return entry.db, s.sqliteSchemaRelease(key, entry)
}

func (s *sqliteDiagnosticsState) cacheSQLiteSchemaDB(key string, db *sql.DB) (*sql.DB, func()) {
	s.schemaDBCache.Lock()
	defer s.schemaDBCache.Unlock()
	if existing := s.schemaDBCache.entries[key]; existing != nil {
		_ = db.Close()
		s.schemaDBCache.clock++
		existing.refs++
		existing.lastUsed = s.schemaDBCache.clock
		return existing.db, s.sqliteSchemaRelease(key, existing)
	}
	s.evictSQLiteSchemaDBLocked()
	if len(s.schemaDBCache.entries) >= sqliteSchemaCacheCapacity {
		return db, sync.OnceFunc(func() { _ = db.Close() })
	}
	s.schemaDBCache.clock++
	entry := &sqliteSchemaCacheEntry{db: db, refs: 1, lastUsed: s.schemaDBCache.clock}
	s.schemaDBCache.entries[key] = entry
	return db, s.sqliteSchemaRelease(key, entry)
}

// evictSQLiteSchemaDBLocked 在容量满时关闭并移除最久未使用且无引用的 schema。
func (s *sqliteDiagnosticsState) evictSQLiteSchemaDBLocked() {
	if len(s.schemaDBCache.entries) < sqliteSchemaCacheCapacity {
		return
	}
	var oldestKey string
	var oldest *sqliteSchemaCacheEntry
	for key, entry := range s.schemaDBCache.entries {
		if entry.refs == 0 && (oldest == nil || entry.lastUsed < oldest.lastUsed) {
			oldestKey, oldest = key, entry
		}
	}
	if oldest != nil {
		delete(s.schemaDBCache.entries, oldestKey)
		_ = oldest.db.Close()
	}
}

func (s *sqliteDiagnosticsState) sqliteSchemaRelease(key string, entry *sqliteSchemaCacheEntry) func() {
	return sync.OnceFunc(func() {
		s.schemaDBCache.Lock()
		defer s.schemaDBCache.Unlock()
		if s.schemaDBCache.entries[key] == entry && entry.refs > 0 {
			entry.refs--
		}
	})
}

func validateSQLiteSchemaPatch(ctx context.Context, _ string, text string) ([]protocol.Diagnostic, error) {
	db, err := openSQLiteValidationDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "CREATE TABLE task_dag_nodes (_mcp_lsp_seed INTEGER)"); err != nil {
		return nil, fmt.Errorf("initialize SQLite diagnostics schema patch: %w", err)
	}
	_, execErr := db.ExecContext(ctx, normalizeSQLCParameters(text))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return sqliteDiagnosticsForError(0, execErr), nil
}

func validateSQLiteFixture(ctx context.Context, root, text string) ([]protocol.Diagnostic, error) {
	db, err := openSQLiteValidationDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := sqliteruntime.RunMigrations(
		ctx,
		db,
		filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations"),
	); err != nil {
		return nil, fmt.Errorf("initialize SQLite diagnostics fixture schema: %w", err)
	}
	_, execErr := db.ExecContext(ctx, normalizeSQLCParameters(text))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return sqliteDiagnosticsForError(0, execErr), nil
}

func validateSQLiteStandaloneSQL(ctx context.Context, text string) ([]protocol.Diagnostic, error) {
	db, err := openSQLiteValidationDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	_, execErr := db.ExecContext(ctx, normalizeSQLCParameters(text))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return sqliteDiagnosticsForError(0, execErr), nil
}

func openSQLiteValidationDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open SQLite diagnostics database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping SQLite diagnostics database: %w", err)
	}
	return db, nil
}

type sqliteQueryBlock struct {
	startLine int
	text      string
}

func splitSQLiteQueryBlocks(text string) []sqliteQueryBlock {
	lines := strings.SplitAfter(text, "\n")
	blocks := make([]sqliteQueryBlock, 0, 1)
	var current strings.Builder
	startLine := 0
	for lineNumber, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "-- name:") && current.Len() > 0 {
			blocks = append(blocks, sqliteQueryBlock{startLine: startLine, text: current.String()})
			current.Reset()
			startLine = lineNumber
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		blocks = append(blocks, sqliteQueryBlock{startLine: startLine, text: current.String()})
	}
	return blocks
}

func normalizeSQLCParameters(statement string) string {
	return sqlcParameterPattern.ReplaceAllString(statement, ":$1")
}

func stripSQLiteLineComments(statement string) string {
	var out strings.Builder
	for _, line := range strings.SplitAfter(statement, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		out.WriteString(line)
	}
	return out.String()
}

func sqliteErrorDiagnostic(line int, err error) protocol.Diagnostic {
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: line},
			End:   protocol.Position{Line: line, Character: 1},
		},
		Severity: protocol.SeverityError,
		Source:   sqliteDiagnosticsSource,
		Message:  err.Error(),
	}
}

// sqliteDiagnosticsForError 把确定的 SQL 引擎错误转成 LSP Error diagnostic。
func sqliteDiagnosticsForError(line int, err error) []protocol.Diagnostic {
	if err == nil {
		return nil
	}
	return []protocol.Diagnostic{sqliteErrorDiagnostic(line, err)}
}

// sqliteValidationFingerprint 汇总目标文本与迁移依赖内容，生成缓存失效键。
func sqliteValidationFingerprint(root, path, text string) (string, error) {
	hash := sha256.New()
	_, _ = hash.Write([]byte(filepath.Clean(root)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(filepath.Clean(path)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(text))
	if info, err := os.Stat(path); err == nil {
		_, _ = fmt.Fprintf(hash, "\x00target\x00%d\x00%d", info.Size(), info.ModTime().UnixNano())
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat SQLite diagnostics target %s: %w", path, err)
	}

	dependency := filepath.Join(root, "internal", "platform", "db", "sqlite", "migrations")
	if err := writeSQLiteDependencyFingerprint(hash, dependency); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// writeSQLiteDependencyFingerprint 按稳定路径顺序写入迁移元数据与内容。
func writeSQLiteDependencyFingerprint(hash interface{ Write([]byte) (int, error) }, dependency string) error {
	var files []string
	err := filepath.WalkDir(dependency, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") {
			files = append(files, candidate)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("fingerprint SQLite diagnostics dependencies: %w", err)
	}
	sort.Strings(files)
	for _, candidate := range files {
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			return fmt.Errorf("stat SQLite diagnostics dependency %s: %w", candidate, statErr)
		}
		_, _ = fmt.Fprintf(hash, "\x00%s\x00%d\x00%d", candidate, info.Size(), info.ModTime().UnixNano())
		body, readErr := os.ReadFile(candidate)
		if readErr != nil {
			return fmt.Errorf("read SQLite diagnostics dependency %s: %w", candidate, readErr)
		}
		_, _ = hash.Write(body)
	}
	return nil
}

func (s *sqliteDiagnosticsState) loadSQLiteValidationCache(key string) ([]protocol.Diagnostic, bool) {
	s.validationCache.Lock()
	defer s.validationCache.Unlock()
	diagnostics, ok := s.validationCache.entries[key]
	return cloneSQLiteDiagnostics(diagnostics), ok
}

func (s *sqliteDiagnosticsState) storeSQLiteValidationCache(key string, diagnostics []protocol.Diagnostic) {
	s.validationCache.Lock()
	defer s.validationCache.Unlock()
	if len(s.validationCache.entries) >= sqliteDiagnosticsMaxCache {
		clear(s.validationCache.entries)
	}
	s.validationCache.entries[key] = cloneSQLiteDiagnostics(diagnostics)
}

func cloneSQLiteDiagnostics(diagnostics []protocol.Diagnostic) []protocol.Diagnostic {
	if diagnostics == nil {
		return nil
	}
	cloned := make([]protocol.Diagnostic, len(diagnostics))
	copy(cloned, diagnostics)
	return cloned
}
