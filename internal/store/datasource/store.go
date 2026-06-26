package datasource

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// errDatasourceContentEmpty 表示数据源文档解析后没有可检索内容，调用方应阻断写入。
var errDatasourceContentEmpty = errors.New("datasource: extracted content is empty")

// documentStore 按 workspaceRoot/name 持久化数据源文档内容，ensureTable 由锁保护只执行一次。
type documentStore struct {
	db platformdb.Queryable

	mu      sync.Mutex
	ensured bool
}

// NewDocumentStore 创建数据源文档存储；数据库句柄缺失时返回 nil 表示该可选能力未接入。
func NewDocumentStore(db platformdb.Queryable) contract.DatasourceDocumentStore {
	if db == nil {
		return nil
	}
	return &documentStore{db: db}
}

// UpsertDocument 新增或覆盖当前工作区的数据源文档内容。
func (s *documentStore) UpsertDocument(ctx context.Context, params contract.UpsertDatasourceDocumentParams) error {
	if err := validateUpsertDocumentParams(params); err != nil {
		return err
	}
	if err := s.ensureTable(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO datasource_documents (
			workspace_root, name, extension, size_bytes, stored_path, content, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
		ON CONFLICT (workspace_root, name) DO UPDATE SET
			extension = EXCLUDED.extension,
			size_bytes = EXCLUDED.size_bytes,
			stored_path = EXCLUDED.stored_path,
			content = EXCLUDED.content,
			updated_at = CAST(strftime('%s','now') AS INTEGER) * 1000
	`, params.WorkspaceRoot, params.Name, params.Extension, params.Size, params.StoredPath, params.Content)
	return wrapDatasourceStoreError(err, "upsert")
}

// ListDocuments 按工作区列出已持久化的数据源文档。
func (s *documentStore) ListDocuments(ctx context.Context, workspaceRoot string) ([]contract.DatasourceDocument, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil, errors.New("datasource: workspaceRoot is required")
	}
	if err := s.ensureTable(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT workspace_root, name, extension, size_bytes, stored_path, content
		FROM datasource_documents
		WHERE workspace_root = ?
		ORDER BY name ASC
	`, workspaceRoot)
	if err != nil {
		return nil, wrapDatasourceStoreError(err, "list")
	}
	defer rows.Close()

	documents := make([]contract.DatasourceDocument, 0)
	for rows.Next() {
		var doc contract.DatasourceDocument
		if err := rows.Scan(&doc.WorkspaceRoot, &doc.Name, &doc.Extension, &doc.Size, &doc.StoredPath, &doc.Content); err != nil {
			return nil, wrapDatasourceStoreError(err, "list.scan")
		}
		documents = append(documents, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDatasourceStoreError(err, "list.rows")
	}
	return documents, nil
}

// DeleteDocument 删除指定工作区里对应名称的数据源文档记录。
func (s *documentStore) DeleteDocument(ctx context.Context, workspaceRoot, name string) error {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	name = strings.TrimSpace(name)
	if workspaceRoot == "" {
		return errors.New("datasource: workspaceRoot is required")
	}
	if name == "" {
		return errors.New("datasource: name is required")
	}
	if err := s.ensureTable(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM datasource_documents
		WHERE workspace_root = ? AND name = ?
	`, workspaceRoot, name)
	return wrapDatasourceStoreError(err, "delete")
}

// ensureTable 首次使用时建表，并用锁避免并发请求重复建表。
func (s *documentStore) ensureTable(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("datasource: document store is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ensured {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, createDatasourceDocumentsTableSQL); err != nil {
		return wrapDatasourceStoreError(err, "ensure_table")
	}
	s.ensured = true
	return nil
}

// validateUpsertDocumentParams 检查落库必需字段，避免写入不可检索的空文档。
func validateUpsertDocumentParams(params contract.UpsertDatasourceDocumentParams) error {
	switch {
	case strings.TrimSpace(params.WorkspaceRoot) == "":
		return errors.New("datasource: workspaceRoot is required")
	case strings.TrimSpace(params.Name) == "":
		return errors.New("datasource: name is required")
	case strings.TrimSpace(params.Extension) == "":
		return errors.New("datasource: extension is required")
	case strings.TrimSpace(params.StoredPath) == "":
		return errors.New("datasource: storedPath is required")
	case strings.TrimSpace(params.Content) == "":
		return errDatasourceContentEmpty
	default:
		return nil
	}
}

// wrapDatasourceStoreError 统一包装数据库错误，保留具体操作名便于排查。
func wrapDatasourceStoreError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "datasource_document")
}

// createDatasourceDocumentsTableSQL 是 datasource_documents 的自建表 SQL，首次使用时由 ensureTable 执行。
const createDatasourceDocumentsTableSQL = `
CREATE TABLE IF NOT EXISTS datasource_documents (
	workspace_root TEXT NOT NULL,
	name TEXT NOT NULL,
	extension TEXT NOT NULL,
	size_bytes INTEGER NOT NULL,
	stored_path TEXT NOT NULL,
	content TEXT NOT NULL,
	created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	PRIMARY KEY (workspace_root, name),
	CHECK (workspace_root <> ''),
	CHECK (name <> ''),
	CHECK (extension <> ''),
	CHECK (stored_path <> ''),
	CHECK (content <> '')
);
`

// 编译期确认 documentStore 满足跨模块数据源文档存储接口。
var _ contract.DatasourceDocumentStore = (*documentStore)(nil)
