package datasourcev2

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestDatasourceQualityMigrationIsolatesLegacyReadyRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustExecDatasourceSQL(t, db, string(mustReadDatasourceFile(t, "../../platform/db/sqlite/migrations/001_baseline.sql")))
	mustExecDatasourceSQL(t, db, `INSERT INTO datasource_v2_documents(source_path,file_name,extension,size_bytes,content_hash,status) VALUES('/old.pdf','old.pdf','.pdf',1,'sha256:old','ready')`)
	mustExecDatasourceSQL(t, db, string(mustReadDatasourceFile(t, "../../platform/db/sqlite/migrations/119_datasource_v2_pdf_quality.sql")))
	var quality string
	if err := db.QueryRow(`SELECT quality_status FROM datasource_v2_documents WHERE source_path='/old.pdf'`).Scan(&quality); err != nil {
		t.Fatal(err)
	}
	if quality != QualityUnknown {
		t.Fatalf("legacy quality = %q, want unknown", quality)
	}
	var searchable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM datasource_v2_documents WHERE status='ready' AND quality_status='passed'`).Scan(&searchable); err != nil {
		t.Fatal(err)
	}
	if searchable != 0 {
		t.Fatalf("searchable legacy rows = %d, want 0", searchable)
	}
	query := mustReadDatasourceFile(t, "../../../sql/queries/datasource_v2.sql")
	if !strings.Contains(string(query), "d.status = 'ready'\n  AND d.quality_status = 'passed'") {
		t.Fatal("semantic search query must require ready and passed quality")
	}
}

func mustReadDatasourceFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func mustExecDatasourceSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

func TestInsertChunkRejectsForbiddenRunesBeforeQuerier(t *testing.T) {
	called := false
	s := &store{q: &datasourceV2QuerierStub{insertChunkFn: func(context.Context, sqlc.InsertDatasourceV2ChunkParams) error {
		called = true
		return nil
	}}}
	params := InsertChunkParams{
		DocumentID: 1, Content: "bad\x00text", CharCount: 8, ByteCount: 8,
		Embedding: make([]byte, 4), EmbeddingModel: "test", EmbeddingDim: 1, TokenCount: 1,
	}
	if err := s.InsertChunk(context.Background(), params); err == nil {
		t.Fatal("InsertChunk() error = nil")
	}
	if called {
		t.Fatal("querier called for forbidden chunk")
	}
}
