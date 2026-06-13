package datasource

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

var errDatasourceContentEmpty = errors.New("datasource: extracted content is empty")

type documentStore struct {
	db platformdb.Queryable

	mu      sync.Mutex
	ensured bool
}

// NewDocumentStore 创建数据源文档存储；没有数据库句柄时返回 nil，让调用方按可选依赖处理。
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
	_, err := s.db.Exec(ctx, `
		INSERT INTO public.datasource_documents (
			workspace_root, name, extension, size_bytes, stored_path, content, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (workspace_root, name) DO UPDATE SET
			extension = EXCLUDED.extension,
			size_bytes = EXCLUDED.size_bytes,
			stored_path = EXCLUDED.stored_path,
			content = EXCLUDED.content,
			updated_at = now()
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
	rows, err := s.db.Query(ctx, `
		SELECT workspace_root, name, extension, size_bytes, stored_path, content
		FROM public.datasource_documents
		WHERE workspace_root = $1
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
	_, err := s.db.Exec(ctx, `
		DELETE FROM public.datasource_documents
		WHERE workspace_root = $1 AND name = $2
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
	if _, err := s.db.Exec(ctx, createDatasourceDocumentsTableSQL); err != nil {
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

const createDatasourceDocumentsTableSQL = `
CREATE TABLE IF NOT EXISTS public.datasource_documents (
	workspace_root text NOT NULL,
	name text NOT NULL,
	extension text NOT NULL,
	size_bytes bigint NOT NULL,
	stored_path text NOT NULL,
	content text NOT NULL,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	updated_at timestamp with time zone DEFAULT now() NOT NULL,
	PRIMARY KEY (workspace_root, name),
	CHECK (workspace_root <> ''),
	CHECK (name <> ''),
	CHECK (extension <> ''),
	CHECK (stored_path <> ''),
	CHECK (content <> '')
);
`

var _ contract.DatasourceDocumentStore = (*documentStore)(nil)
