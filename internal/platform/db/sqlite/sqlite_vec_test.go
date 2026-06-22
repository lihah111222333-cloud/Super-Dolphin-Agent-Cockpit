package sqlite

import (
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestSQLiteVecExtensionRegistered(t *testing.T) {
	db, _ := openMigratedSQLiteDB(t, "vec-extension")
	defer db.Close()

	var version string
	if err := db.QueryRow("SELECT vec_version()").Scan(&version); err != nil {
		t.Fatalf("vec_version() error = %v", err)
	}
	if version == "" {
		t.Fatal("vec_version() returned empty version")
	}

	var dimensions int
	if err := db.QueryRow("SELECT vec_length(?)", []byte{0, 0, 128, 63, 0, 0, 0, 64}).Scan(&dimensions); err != nil {
		t.Fatalf("vec_length() error = %v", err)
	}
	if dimensions != 2 {
		t.Fatalf("vec_length() = %d, want 2", dimensions)
	}
}

func TestSQLiteMigrationsAddDatasourceV2ChunkEmbeddingColumns(t *testing.T) {
	db, _ := openMigratedSQLiteDB(t, "datasource-v2-vector-columns")
	defer db.Close()

	info := tableInfo(t, db, "datasource_v2_text_chunks")
	for _, column := range []string{"embedding", "embedding_model", "embedding_dim", "token_count"} {
		if _, ok := info[column]; !ok {
			t.Fatalf("datasource_v2_text_chunks missing column %q", column)
		}
	}
}

func TestDatasourceV2SemanticSearchUsesSQLiteVecDistance(t *testing.T) {
	db, _ := openMigratedSQLiteDB(t, "datasource-v2-semantic-search")
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
INSERT INTO datasource_v2_documents (
	id,
	source_path,
	file_name,
	extension,
	size_bytes,
	content_hash,
	chunk_count,
	total_chars,
	status
) VALUES (1, '/tmp/search.txt', 'search.txt', '.txt', 32, 'sha256:test', 2, 32, 'ready')
`); err != nil {
		t.Fatalf("insert datasource_v2 document: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO datasource_v2_text_chunks (
	id,
	document_id,
	chunk_index,
	content,
	char_count,
	byte_count,
	embedding,
	embedding_model,
	embedding_dim,
	token_count
) VALUES
	(10, 1, 0, 'near vector', 11, 11, ?, 'test-model', 2, 2),
	(11, 1, 1, 'far vector', 10, 10, ?, 'test-model', 2, 2)
`, sqliteVecTestVector(1, 0), sqliteVecTestVector(0, 1)); err != nil {
		t.Fatalf("insert datasource_v2 chunks: %v", err)
	}

	rows, err := sqlc.New(db).SearchDatasourceV2ChunksByEmbedding(ctx, sqlc.SearchDatasourceV2ChunksByEmbeddingParams{
		Embedding:      sqliteVecTestVector(1, 0),
		EmbeddingModel: "test-model",
		EmbeddingDim:   2,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("SearchDatasourceV2ChunksByEmbedding() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("SearchDatasourceV2ChunksByEmbedding() rows = %+v, want 2 rows", rows)
	}
	if rows[0].Content != "near vector" {
		t.Fatalf("first semantic chunk = %q, want near vector", rows[0].Content)
	}
	if rows[0].Distance > rows[1].Distance {
		t.Fatalf("distances not sorted ascending: %+v", rows)
	}
}

func sqliteVecTestVector(values ...float32) []byte {
	blob := make([]byte, len(values)*4)
	for i, value := range values {
		binary.LittleEndian.PutUint32(blob[i*4:], math.Float32bits(value))
	}
	return blob
}
