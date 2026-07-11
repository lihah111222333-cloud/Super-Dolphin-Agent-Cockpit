package datasource

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// TestDocumentStoreDoesNotCreateDatasourceTable 固定 datasource store 不能在运行时自建表。
func TestDocumentStoreDoesNotCreateDatasourceTable(t *testing.T) {
	db := &recordingDocumentDB{}
	store := NewDocumentStore(db)
	params := contract.UpsertDatasourceDocumentParams{
		WorkspaceRoot: "D:\\project",
		Name:          "notes.txt",
		Extension:     ".txt",
		Size:          12,
		StoredPath:    "D:\\project\\.agent\\datasources\\uploads\\notes.txt",
		Content:       "hello world",
	}

	if err := store.UpsertDocument(context.Background(), params); err != nil {
		t.Fatalf("UpsertDocument() error = %v", err)
	}

	if len(db.execSQL) != 1 {
		t.Fatalf("Exec calls = %d, want only upsert SQL", len(db.execSQL))
	}
	for _, sql := range db.execSQL {
		if strings.Contains(strings.ToUpper(sql), "CREATE TABLE") {
			t.Fatalf("document store executed runtime DDL: %q", sql)
		}
	}
}

// TestListPromptDocumentsRejectsOverCountBeforeContentQuery 验证数量超限时不会再读取正文。
func TestListPromptDocumentsRejectsOverCountBeforeContentQuery(t *testing.T) {
	query := &promptDocumentQuery{
		metadataRows: []sqlc.ListDatasourceDocumentPromptMetadataRow{
			promptMetadataRow("doc-a.txt", 10),
			promptMetadataRow("doc-b.txt", 10),
			promptMetadataRow("doc-c.txt", 10),
		},
	}
	store := &documentStore{q: query}

	_, err := store.ListPromptDocuments(context.Background(), "/workspace", 2, 100, 100)

	if err == nil || !strings.Contains(err.Error(), "exceed count cap") {
		t.Fatalf("ListPromptDocuments() error = %v, want count cap", err)
	}
	if query.contentCalls != 0 {
		t.Fatalf("content query calls = %d, want 0 before count cap failure", query.contentCalls)
	}
}

// TestListPromptDocumentsRejectsSingleDocumentBeforeContentQuery 验证单文档超限时不会读取正文。
func TestListPromptDocumentsRejectsSingleDocumentBeforeContentQuery(t *testing.T) {
	query := &promptDocumentQuery{
		metadataRows: []sqlc.ListDatasourceDocumentPromptMetadataRow{
			promptMetadataRow("large.txt", 101),
		},
	}
	store := &documentStore{q: query}

	_, err := store.ListPromptDocuments(context.Background(), "/workspace", 2, 200, 100)

	if err == nil || !strings.Contains(err.Error(), "large.txt") || !strings.Contains(err.Error(), "exceeds byte cap") {
		t.Fatalf("ListPromptDocuments() error = %v, want single document byte cap", err)
	}
	if query.contentCalls != 0 {
		t.Fatalf("content query calls = %d, want 0 before single-document cap failure", query.contentCalls)
	}
}

// TestListPromptDocumentsRejectsTotalBytesBeforeContentQuery 验证总字节超限时停在元数据阶段。
func TestListPromptDocumentsRejectsTotalBytesBeforeContentQuery(t *testing.T) {
	query := &promptDocumentQuery{
		metadataRows: []sqlc.ListDatasourceDocumentPromptMetadataRow{
			promptMetadataRow("doc-a.txt", 70),
			promptMetadataRow("doc-b.txt", 70),
		},
	}
	store := &documentStore{q: query}

	_, err := store.ListPromptDocuments(context.Background(), "/workspace", 2, 100, 100)

	if err == nil || !strings.Contains(err.Error(), "documents exceed byte cap") {
		t.Fatalf("ListPromptDocuments() error = %v, want total byte cap", err)
	}
	if query.contentCalls != 0 {
		t.Fatalf("content query calls = %d, want 0 before total cap failure", query.contentCalls)
	}
}

// TestListPromptDocumentsReadsContentAfterBoundsPass 验证通过上限检查后才读取正文。
func TestListPromptDocumentsReadsContentAfterBoundsPass(t *testing.T) {
	query := &promptDocumentQuery{
		metadataRows: []sqlc.ListDatasourceDocumentPromptMetadataRow{
			promptMetadataRow("doc-a.txt", 10),
			promptMetadataRow("doc-b.txt", 10),
		},
		contentRows: []sqlc.ListDatasourcePromptDocumentsRow{
			promptContentRow("doc-a.txt", "alpha"),
			promptContentRow("doc-b.txt", "bravo"),
		},
	}
	store := &documentStore{q: query}

	docs, err := store.ListPromptDocuments(context.Background(), "/workspace", 2, 100, 100)

	if err != nil {
		t.Fatalf("ListPromptDocuments() error = %v", err)
	}
	if query.metadataCalls != 1 || query.contentCalls != 1 {
		t.Fatalf("query calls metadata=%d content=%d, want 1/1", query.metadataCalls, query.contentCalls)
	}
	if len(docs) != 2 || docs[0].Content != "alpha" || docs[1].Content != "bravo" {
		t.Fatalf("ListPromptDocuments() docs = %#v, want bounded content rows", docs)
	}
}

type recordingDocumentDB struct {
	execSQL []string
}

func (db *recordingDocumentDB) ExecContext(_ context.Context, sql string, _ ...any) (sql.Result, error) {
	db.execSQL = append(db.execSQL, sql)
	return recordingSQLResult(1), nil
}

// PrepareContext satisfies sqlc.DBTX; datasource queries should execute without prepared statements.
func (db *recordingDocumentDB) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("unexpected Prepare call")
}

func (db *recordingDocumentDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (db *recordingDocumentDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

type recordingSQLResult int64

func (r recordingSQLResult) LastInsertId() (int64, error) { return 0, nil }

func (r recordingSQLResult) RowsAffected() (int64, error) { return int64(r), nil }

// promptDocumentQuery 伪造 sqlc 查询并记录 prompt 读取是否进入正文阶段。
type promptDocumentQuery struct {
	metadataRows  []sqlc.ListDatasourceDocumentPromptMetadataRow
	contentRows   []sqlc.ListDatasourcePromptDocumentsRow
	metadataCalls int
	contentCalls  int
}

// UpsertDatasourceDocument 防止 prompt 读取测试误走写入路径。
func (q *promptDocumentQuery) UpsertDatasourceDocument(context.Context, sqlc.UpsertDatasourceDocumentParams) (int64, error) {
	return 0, errors.New("unexpected UpsertDatasourceDocument call")
}

// ListDatasourceDocuments 防止 prompt 读取测试误走旧列表路径。
func (q *promptDocumentQuery) ListDatasourceDocuments(context.Context, sqlc.ListDatasourceDocumentsParams) ([]sqlc.ListDatasourceDocumentsRow, error) {
	return nil, errors.New("unexpected ListDatasourceDocuments call")
}

// ListDatasourceDocumentPromptMetadata 返回不含正文的边界检查行。
func (q *promptDocumentQuery) ListDatasourceDocumentPromptMetadata(context.Context, sqlc.ListDatasourceDocumentPromptMetadataParams) ([]sqlc.ListDatasourceDocumentPromptMetadataRow, error) {
	q.metadataCalls++
	return q.metadataRows, nil
}

// ListDatasourcePromptDocuments 记录正文读取，证明它只在边界检查通过后执行。
func (q *promptDocumentQuery) ListDatasourcePromptDocuments(context.Context, sqlc.ListDatasourcePromptDocumentsParams) ([]sqlc.ListDatasourcePromptDocumentsRow, error) {
	q.contentCalls++
	return q.contentRows, nil
}

// DeleteDatasourceDocument 防止 prompt 读取测试误走删除路径。
func (q *promptDocumentQuery) DeleteDatasourceDocument(context.Context, sqlc.DeleteDatasourceDocumentParams) (int64, error) {
	return 0, errors.New("unexpected DeleteDatasourceDocument call")
}

// promptMetadataRow 构造只含大小信息的 prompt 元数据行。
func promptMetadataRow(name string, contentBytes int64) sqlc.ListDatasourceDocumentPromptMetadataRow {
	return sqlc.ListDatasourceDocumentPromptMetadataRow{
		WorkspaceRoot: "/workspace",
		Name:          name,
		Extension:     ".txt",
		SizeBytes:     contentBytes,
		StoredPath:    "/workspace/.agent/datasources/uploads/" + name,
		ContentBytes:  &contentBytes,
	}
}

// promptContentRow 构造通过边界检查后返回的正文行。
func promptContentRow(name string, content string) sqlc.ListDatasourcePromptDocumentsRow {
	return sqlc.ListDatasourcePromptDocumentsRow{
		WorkspaceRoot: "/workspace",
		Name:          name,
		Extension:     ".txt",
		SizeBytes:     int64(len(content)),
		StoredPath:    "/workspace/.agent/datasources/uploads/" + name,
		Content:       content,
	}
}
