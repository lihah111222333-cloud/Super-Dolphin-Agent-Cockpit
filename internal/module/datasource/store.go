package datasource

import (
	"context"
	"errors"
	"strings"
	"sync"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DatasourceDocument struct {
	WorkspaceRoot string `json:"workspaceRoot"`
	Name          string `json:"name"`
	Extension     string `json:"extension"`
	Size          int64  `json:"size"`
	StoredPath    string `json:"storedPath"`
	Content       string `json:"content"`
}

type UpsertDatasourceDocumentParams struct {
	WorkspaceRoot string
	Name          string
	Extension     string
	Size          int64
	StoredPath    string
	Content       string
}

type DatasourceDocumentStore interface {
	UpsertDocument(context.Context, UpsertDatasourceDocumentParams) error
	ListDocuments(context.Context, string) ([]DatasourceDocument, error)
	DeleteDocument(context.Context, string, string) error
}

type documentStore struct {
	db platformdb.Queryable

	mu      sync.Mutex
	ensured bool
}

func NewDocumentStore(pool *pgxpool.Pool) DatasourceDocumentStore {
	if pool == nil {
		return nil
	}
	return &documentStore{db: pool}
}

func (s *documentStore) UpsertDocument(ctx context.Context, params UpsertDatasourceDocumentParams) error {
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

func (s *documentStore) ListDocuments(ctx context.Context, workspaceRoot string) ([]DatasourceDocument, error) {
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

	documents := make([]DatasourceDocument, 0)
	for rows.Next() {
		var doc DatasourceDocument
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

func validateUpsertDocumentParams(params UpsertDatasourceDocumentParams) error {
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

var _ DatasourceDocumentStore = (*documentStore)(nil)
