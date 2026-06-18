package toolstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const maxLimit = 500

var (
	ErrStoreNotConfigured  = errors.New("skill tool store is not configured")
	ErrIDRequired          = errors.New("skill tool: id is required")
	ErrLimitRequired       = errors.New("skill tool: limit must be positive")
	ErrMethodNameRequired  = errors.New("skill tool: methodName is required")
	ErrMethodNameInvalid   = errors.New("skill tool: methodName is invalid")
	ErrDescriptionRequired = errors.New("skill tool: description is required")
	ErrEnabledRequired     = errors.New("skill tool: enabled is required")
	ErrNotFound            = errors.New("skill tool not found")
)

// Store 管理 skill_tools 表；首次 CRUD 调用会自动建表。
type Store struct {
	db    *sql.DB
	mu    sync.Mutex
	ready bool
}

// MutationParams 是新增或更新 Skill 工具的入参。
type MutationParams struct {
	CWD             string `json:"cwd"`
	MethodName      string `json:"methodName"`
	MethodNameSnake string `json:"method_name,omitempty"`
	Description     string `json:"description"`
	Enabled         *bool  `json:"enabled"`
}

// UpdateParams 是更新 Skill 工具的入参。
type UpdateParams struct {
	ID int64 `json:"id"`
	MutationParams
}

// IDParams 用项目路径和 id 定位一个 Skill 工具。
type IDParams struct {
	CWD string `json:"cwd"`
	ID  int64  `json:"id"`
}

// MethodParams 用项目路径和方法名定位一个 Skill 工具。
type MethodParams struct {
	CWD        string `json:"cwd"`
	MethodName string `json:"methodName"`
}

// ListParams 是 Skill 工具列表查询入参。
type ListParams struct {
	CWD     string `json:"cwd"`
	Keyword string `json:"keyword"`
	Limit   int32  `json:"limit"`
}

// Result 是 Skill 工具的 JSON 返回形态。
type Result struct {
	ID          int64     `json:"id"`
	CWD         string    `json:"cwd"`
	MethodName  string    `json:"methodName"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ListResult 返回当前项目可用的 Skill 工具。
type ListResult struct {
	Tools []Result `json:"tools"`
}

// DeleteResult 返回删除确认。
type DeleteResult struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

type CWDResolver func(string) (string, error)
type ErrorMapper func(error) error
type ContentReader func(context.Context, string, string) (string, error)

type normalizedMutation struct {
	CWD         string
	MethodName  string
	Description string
	Enabled     bool
}

// New 创建 Store；db 为空时保留 nil，让调用方 fail-fast 报配置错误。
func New(db *sql.DB) *Store {
	if db == nil {
		return nil
	}
	return &Store{db: db}
}

// InvalidParamsError 判断错误是否应映射为 RPC invalid params。
func InvalidParamsError(err error) bool {
	return errors.Is(err, ErrIDRequired) ||
		errors.Is(err, ErrLimitRequired) ||
		errors.Is(err, ErrMethodNameRequired) ||
		errors.Is(err, ErrMethodNameInvalid) ||
		errors.Is(err, ErrDescriptionRequired) ||
		errors.Is(err, ErrEnabledRequired)
}

// Handlers 返回 Skill 工具 CRUD 的 host RPC handler 集合。
func Handlers(store *Store, resolve CWDResolver, mapError ErrorMapper) handler.Map {
	return handler.Map{
		"skills/tools/create": platformrpc.StrictHandler(createHandler(store, resolve, mapError)),
		"skills/tools/list":   platformrpc.StrictHandler(listHandler(store, resolve, mapError)),
		"skills/tools/get":    platformrpc.StrictHandler(getHandler(store, resolve, mapError)),
		"skills/tools/update": platformrpc.StrictHandler(updateHandler(store, resolve, mapError)),
		"skills/tools/delete": platformrpc.StrictHandler(deleteHandler(store, resolve, mapError)),
	}
}

// ListForSurface 返回当前项目启用的 Skill 工具定义，供动态工具面使用。
func ListForSurface(ctx context.Context, store *Store, resolve CWDResolver, cwd string) ([]contract.SkillToolSurfaceTool, error) {
	if store == nil {
		return nil, ErrStoreNotConfigured
	}
	resolved := strings.TrimSpace(cwd)
	if err := resolveParamCWD(&resolved, resolve); err != nil {
		return nil, err
	}
	result, err := store.List(ctx, ListParams{CWD: resolved, Limit: maxLimit})
	if err != nil {
		return nil, err
	}
	tools := make([]contract.SkillToolSurfaceTool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if tool.Enabled {
			tools = append(tools, contract.SkillToolSurfaceTool{Name: strings.TrimSpace(tool.MethodName), Description: strings.TrimSpace(tool.Description)})
		}
	}
	return tools, nil
}

// CallForSurface 解析启用的 Skill 工具并通过调用方提供的读取器返回全文。
func CallForSurface(ctx context.Context, store *Store, resolve CWDResolver, read ContentReader, call contract.SkillToolCall) (string, error) {
	if store == nil || read == nil {
		return "", ErrStoreNotConfigured
	}
	resolved := strings.TrimSpace(call.CWD)
	if err := resolveParamCWD(&resolved, resolve); err != nil {
		return "", err
	}
	tool, err := store.GetByMethod(ctx, MethodParams{CWD: resolved, MethodName: call.Name})
	if err != nil {
		return "", err
	}
	if !tool.Enabled {
		return "", ErrNotFound
	}
	return read(ctx, resolved, tool.MethodName)
}

func createHandler(store *Store, resolve CWDResolver, mapError ErrorMapper) func(context.Context, MutationParams) (Result, error) {
	return func(ctx context.Context, p MutationParams) (Result, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		result, err := store.Create(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func listHandler(store *Store, resolve CWDResolver, mapError ErrorMapper) func(context.Context, ListParams) (ListResult, error) {
	return func(ctx context.Context, p ListParams) (ListResult, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return ListResult{}, mapHandlerError(err, mapError)
		}
		result, err := store.List(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func getHandler(store *Store, resolve CWDResolver, mapError ErrorMapper) func(context.Context, IDParams) (Result, error) {
	return func(ctx context.Context, p IDParams) (Result, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		result, err := store.Get(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func updateHandler(store *Store, resolve CWDResolver, mapError ErrorMapper) func(context.Context, UpdateParams) (Result, error) {
	return func(ctx context.Context, p UpdateParams) (Result, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		result, err := store.Update(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func deleteHandler(store *Store, resolve CWDResolver, mapError ErrorMapper) func(context.Context, IDParams) (DeleteResult, error) {
	return func(ctx context.Context, p IDParams) (DeleteResult, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return DeleteResult{}, mapHandlerError(err, mapError)
		}
		result, err := store.Delete(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func resolveParamCWD(cwd *string, resolve CWDResolver) error {
	if resolve == nil {
		return ErrStoreNotConfigured
	}
	resolved, err := resolve(*cwd)
	if err != nil {
		return err
	}
	*cwd = resolved
	return nil
}

func mapHandlerError(err error, mapError ErrorMapper) error {
	if err == nil {
		return nil
	}
	if mapError == nil {
		return err
	}
	return mapError(err)
}

// Create 校验并插入一个 Skill 工具。
func (s *Store) Create(ctx context.Context, p MutationParams) (Result, error) {
	normalized, err := normalizeMutation(p)
	if err != nil {
		return Result{}, err
	}
	if err := s.ensure(ctx); err != nil {
		return Result{}, err
	}
	return s.scanOne(ctx, insertSQL,
		normalized.CWD,
		normalized.MethodName,
		normalized.Description,
		boolToSQLite(normalized.Enabled),
	)
}

// List 返回当前项目的 Skill 工具；keyword 匹配方法名和描述。
func (s *Store) List(ctx context.Context, p ListParams) (ListResult, error) {
	if err := validateListParams(p); err != nil {
		return ListResult{}, err
	}
	tools, err := s.list(ctx, strings.TrimSpace(p.CWD), strings.TrimSpace(p.Keyword), normalizeLimit(p.Limit))
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Tools: tools}, nil
}

// Get 按项目路径和 id 读取一个 Skill 工具。
func (s *Store) Get(ctx context.Context, p IDParams) (Result, error) {
	if err := validateIDParams(p); err != nil {
		return Result{}, err
	}
	if err := s.ensure(ctx); err != nil {
		return Result{}, err
	}
	return s.scanOne(ctx, getSQL, strings.TrimSpace(p.CWD), p.ID)
}

// GetByMethod 按项目路径和方法名读取一个 Skill 工具。
func (s *Store) GetByMethod(ctx context.Context, p MethodParams) (Result, error) {
	methodName := strings.TrimSpace(p.MethodName)
	if err := validateMethodName(methodName); err != nil {
		return Result{}, err
	}
	cwd := strings.TrimSpace(p.CWD)
	if cwd == "" {
		return Result{}, ErrStoreNotConfigured
	}
	if err := s.ensure(ctx); err != nil {
		return Result{}, err
	}
	return s.scanOne(ctx, getByMethodSQL, cwd, methodName)
}

// Update 覆盖一个 Skill 工具的可编辑字段。
func (s *Store) Update(ctx context.Context, p UpdateParams) (Result, error) {
	if p.ID <= 0 {
		return Result{}, ErrIDRequired
	}
	normalized, err := normalizeMutation(p.MutationParams)
	if err != nil {
		return Result{}, err
	}
	if err := s.ensure(ctx); err != nil {
		return Result{}, err
	}
	return s.update(ctx, p.ID, normalized)
}

// Delete 删除当前项目中的 Skill 工具。
func (s *Store) Delete(ctx context.Context, p IDParams) (DeleteResult, error) {
	if err := validateIDParams(p); err != nil {
		return DeleteResult{}, err
	}
	if err := s.delete(ctx, strings.TrimSpace(p.CWD), p.ID); err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{ID: p.ID, Deleted: true}, nil
}

func (s *Store) list(ctx context.Context, cwd, keyword string, limit int32) ([]Result, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	pattern := "%" + keyword + "%"
	rows, err := s.db.QueryContext(ctx, listSQL, cwd, pattern, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("list skill tools: %w", err)
	}
	defer rows.Close()
	return scanRows(rows)
}

func (s *Store) update(ctx context.Context, id int64, p normalizedMutation) (Result, error) {
	return s.scanOne(ctx, updateSQL, p.MethodName, p.Description, boolToSQLite(p.Enabled), p.CWD, id)
}

func (s *Store) delete(ctx context.Context, cwd string, id int64) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, deleteSQL, cwd, id)
	if err != nil {
		return fmt.Errorf("delete skill tool: %w", err)
	}
	return requireAffected(result)
}

// ensure 创建表并缓存成功状态；失败不会被缓存，下一次调用可以继续重试。
func (s *Store) ensure(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	if s.ready {
		return nil
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

func (s *Store) scanOne(ctx context.Context, query string, args ...any) (Result, error) {
	tool, err := scan(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}
	return tool, nil
}

func normalizeMutation(p MutationParams) (normalizedMutation, error) {
	cwd := strings.TrimSpace(p.CWD)
	if cwd == "" {
		return normalizedMutation{}, ErrStoreNotConfigured
	}
	methodName := strings.TrimSpace(firstNonEmpty(p.MethodName, p.MethodNameSnake))
	if err := validateMethodName(methodName); err != nil {
		return normalizedMutation{}, err
	}
	description := strings.TrimSpace(p.Description)
	if description == "" {
		return normalizedMutation{}, ErrDescriptionRequired
	}
	if p.Enabled == nil {
		return normalizedMutation{}, ErrEnabledRequired
	}
	return normalizedMutation{
		CWD:         cwd,
		MethodName:  methodName,
		Description: description,
		Enabled:     *p.Enabled,
	}, nil
}

func validateListParams(p ListParams) error {
	if strings.TrimSpace(p.CWD) == "" {
		return ErrStoreNotConfigured
	}
	if p.Limit <= 0 {
		return ErrLimitRequired
	}
	return nil
}

func validateIDParams(p IDParams) error {
	if strings.TrimSpace(p.CWD) == "" {
		return ErrStoreNotConfigured
	}
	if p.ID <= 0 {
		return ErrIDRequired
	}
	return nil
}

func validateMethodName(methodName string) error {
	methodName = strings.TrimSpace(methodName)
	if methodName == "" {
		return ErrMethodNameRequired
	}
	if strings.ContainsAny(methodName, " \t\r\n/\\") {
		return fmt.Errorf("%w: %s", ErrMethodNameInvalid, methodName)
	}
	return nil
}

func normalizeLimit(limit int32) int32 {
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func scanRows(rows *sql.Rows) ([]Result, error) {
	out := make([]Result, 0)
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

func scan(source scanner) (Result, error) {
	var tool Result
	var enabled int64
	err := source.Scan(
		&tool.ID,
		&tool.CWD,
		&tool.MethodName,
		&tool.Description,
		&enabled,
		&tool.CreatedAt,
		&tool.UpdatedAt,
	)
	if err != nil {
		return Result{}, err
	}
	tool.Enabled = enabled != 0
	return tool, nil
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read skill tool affected rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
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
