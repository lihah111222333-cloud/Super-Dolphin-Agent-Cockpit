package datasource

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// errDatasourceContentEmpty 表示数据源文档解析后没有可检索内容，调用方应阻断写入。
var errDatasourceContentEmpty = errors.New("datasource: extracted content is empty")

type documentQuerier interface {
	UpsertDatasourceDocument(context.Context, sqlc.UpsertDatasourceDocumentParams) (int64, error)
	ListDatasourceDocuments(context.Context, sqlc.ListDatasourceDocumentsParams) ([]sqlc.ListDatasourceDocumentsRow, error)
	DeleteDatasourceDocument(context.Context, sqlc.DeleteDatasourceDocumentParams) (int64, error)
}

// documentStore 按 workspaceRoot/name 持久化数据源文档内容。
type documentStore struct {
	q documentQuerier
}

// NewDocumentStore 创建数据源文档存储；数据库句柄缺失时返回 nil 表示该可选能力未接入。
func NewDocumentStore(db platformdb.Queryable) contract.DatasourceDocumentStore {
	if db == nil {
		return nil
	}
	dbtx, ok := db.(sqlc.DBTX)
	if !ok {
		return &documentStore{}
	}
	return &documentStore{q: sqlc.New(dbtx)}
}

// UpsertDocument 新增或覆盖当前工作区的数据源文档内容。
func (s *documentStore) UpsertDocument(ctx context.Context, params contract.UpsertDatasourceDocumentParams) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := validateUpsertDocumentParams(params); err != nil {
		return err
	}
	_, err := s.q.UpsertDatasourceDocument(ctx, sqlc.UpsertDatasourceDocumentParams{
		WorkspaceRoot: strings.TrimSpace(params.WorkspaceRoot),
		Name:          strings.TrimSpace(params.Name),
		Extension:     strings.TrimSpace(params.Extension),
		SizeBytes:     params.Size,
		StoredPath:    strings.TrimSpace(params.StoredPath),
		Content:       strings.TrimSpace(params.Content),
	})
	return wrapDatasourceStoreError(err, "upsert")
}

// ListDocuments 按工作区列出已持久化的数据源文档。
func (s *documentStore) ListDocuments(ctx context.Context, workspaceRoot string) ([]contract.DatasourceDocument, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil, errors.New("datasource: workspaceRoot is required")
	}
	rows, err := s.q.ListDatasourceDocuments(ctx, sqlc.ListDatasourceDocumentsParams{WorkspaceRoot: workspaceRoot})
	if err != nil {
		return nil, wrapDatasourceStoreError(err, "list")
	}
	documents := make([]contract.DatasourceDocument, 0, len(rows))
	for _, row := range rows {
		documents = append(documents, contract.DatasourceDocument{
			WorkspaceRoot: row.WorkspaceRoot,
			Name:          row.Name,
			Extension:     row.Extension,
			Size:          row.SizeBytes,
			StoredPath:    row.StoredPath,
			Content:       row.Content,
		})
	}
	return documents, nil
}

// DeleteDocument 删除指定工作区里对应名称的数据源文档记录。
func (s *documentStore) DeleteDocument(ctx context.Context, workspaceRoot, name string) error {
	if err := s.validate(); err != nil {
		return err
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	name = strings.TrimSpace(name)
	if workspaceRoot == "" {
		return errors.New("datasource: workspaceRoot is required")
	}
	if name == "" {
		return errors.New("datasource: name is required")
	}
	_, err := s.q.DeleteDatasourceDocument(ctx, sqlc.DeleteDatasourceDocumentParams{
		WorkspaceRoot: workspaceRoot,
		Name:          name,
	})
	return wrapDatasourceStoreError(err, "delete")
}

// validate 确认 store 已接入迁移后的 sqlc 查询。
func (s *documentStore) validate() error {
	if s == nil || s.q == nil {
		return errors.New("datasource: document store is not configured")
	}
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
	case params.Size < 0:
		return errors.New("datasource: size must be non-negative")
	default:
		return nil
	}
}

// wrapDatasourceStoreError 统一包装数据库错误，保留具体操作名便于排查。
func wrapDatasourceStoreError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "datasource_document")
}

// 编译期确认 documentStore 满足跨模块数据源文档存储接口。
var _ contract.DatasourceDocumentStore = (*documentStore)(nil)
