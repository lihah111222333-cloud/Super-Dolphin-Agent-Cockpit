package skilltool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrStoreNotConfigured indicates that the SQLite dependency was not supplied.
	ErrStoreNotConfigured = errors.New("skill tool store is not configured")
	// ErrNotFound indicates that the requested Skill tool row does not exist.
	ErrNotFound = errors.New("skill tool not found")
)

// Store owns the skill_tools SQLite table and creates it on first use.
type Store struct {
	db    *sql.DB
	mu    sync.Mutex
	ready bool
}

// Mutation is the persistence input for creating or replacing a Skill tool.
type Mutation struct {
	CWD         string
	MethodName  string
	Description string
	Enabled     bool
}

// Update identifies a row and supplies its replacement values.
type Update struct {
	ID int64
	Mutation
}

// ByID identifies a row inside one project.
type ByID struct {
	CWD string
	ID  int64
}

// ByMethod identifies a row by project and method name.
type ByMethod struct {
	CWD        string
	MethodName string
}

// ListQuery filters Skill tools inside one project.
type ListQuery struct {
	CWD     string
	Keyword string
	Limit   int32
}

// Tool is the store-owned persistence DTO.
type Tool struct {
	ID          int64
	CWD         string
	MethodName  string
	Description string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// New 创建 SQLite Store；数据库为空时返回 nil，让组合根 fail-fast。
func New(db *sql.DB) *Store {
	if db == nil {
		return nil
	}
	return &Store{db: db}
}

// Create 持久化一个已经由 Skill 层校验的工具。
func (s *Store) Create(ctx context.Context, p Mutation) (Tool, error) {
	if err := s.ensure(ctx); err != nil {
		return Tool{}, err
	}
	return s.scanOne(ctx, insertSQL, p.CWD, p.MethodName, p.Description, boolToSQLite(p.Enabled))
}

// List 按项目和关键词返回 Skill 工具持久化记录。
func (s *Store) List(ctx context.Context, p ListQuery) ([]Tool, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	pattern := "%" + p.Keyword + "%"
	rows, err := s.db.QueryContext(ctx, listSQL, p.CWD, pattern, pattern, pattern, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("list skill tools: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

// Get 按项目和行 ID 读取一个 Skill 工具。
func (s *Store) Get(ctx context.Context, p ByID) (Tool, error) {
	if err := s.ensure(ctx); err != nil {
		return Tool{}, err
	}
	return s.scanOne(ctx, getSQL, p.CWD, p.ID)
}

// GetByMethod 按项目和方法名读取一个 Skill 工具。
func (s *Store) GetByMethod(ctx context.Context, p ByMethod) (Tool, error) {
	if err := s.ensure(ctx); err != nil {
		return Tool{}, err
	}
	return s.scanOne(ctx, getByMethodSQL, p.CWD, p.MethodName)
}

// Update 覆盖一个 Skill 工具并返回更新后的记录。
func (s *Store) Update(ctx context.Context, p Update) (Tool, error) {
	if err := s.ensure(ctx); err != nil {
		return Tool{}, err
	}
	return s.scanOne(ctx, updateSQL, p.MethodName, p.Description, boolToSQLite(p.Enabled), p.CWD, p.ID)
}

// Delete 删除一个 Skill 工具，不存在时显式返回 ErrNotFound。
func (s *Store) Delete(ctx context.Context, p ByID) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, deleteSQL, p.CWD, p.ID)
	if err != nil {
		return fmt.Errorf("delete skill tool: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read skill tool affected rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ensure 懒创建 skill_tools 表；失败结果不会缓存，后续调用可重试。
func (s *Store) ensure(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("create skill_tools table: %w", err)
	}
	s.ready = true
	return nil
}

func (s *Store) scanOne(ctx context.Context, query string, args ...any) (Tool, error) {
	tool, err := scan(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Tool{}, ErrNotFound
	}
	if err != nil {
		return Tool{}, err
	}
	return tool, nil
}

func scanRows(rows *sql.Rows) ([]Tool, error) {
	out := make([]Tool, 0)
	for rows.Next() {
		tool, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skill tools: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(source scanner) (Tool, error) {
	var tool Tool
	var enabled int64
	err := source.Scan(&tool.ID, &tool.CWD, &tool.MethodName, &tool.Description, &enabled, &tool.CreatedAt, &tool.UpdatedAt)
	if err != nil {
		return Tool{}, err
	}
	tool.Enabled = enabled != 0
	return tool, nil
}

func boolToSQLite(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

const createTableSQL = `
CREATE TABLE IF NOT EXISTS skill_tools (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	cwd TEXT NOT NULL,
	method_name TEXT NOT NULL,
	description TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(cwd, method_name),
	CHECK (cwd <> ''),
	CHECK (method_name <> ''),
	CHECK (description <> ''),
	CHECK (enabled IN (0, 1))
)`

const columns = `
id, cwd, method_name, description, enabled, created_at, updated_at`

const insertSQL = `
INSERT INTO skill_tools (cwd, method_name, description, enabled)
VALUES (?, ?, ?, ?)
RETURNING ` + columns

const listSQL = `
SELECT ` + columns + `
FROM skill_tools
WHERE cwd = ?
	AND (? = '%%' OR method_name LIKE ? OR description LIKE ?)
ORDER BY updated_at DESC, id DESC
LIMIT ?`

const getSQL = `
SELECT ` + columns + `
FROM skill_tools
WHERE cwd = ? AND id = ?`

const getByMethodSQL = `
SELECT ` + columns + `
FROM skill_tools
WHERE cwd = ? AND method_name = ?`

const updateSQL = `
UPDATE skill_tools
SET method_name = ?, description = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
WHERE cwd = ? AND id = ?
RETURNING ` + columns

const deleteSQL = `
DELETE FROM skill_tools
WHERE cwd = ? AND id = ?`
