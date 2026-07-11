package datasourcev2

import (
	"context"
	"strings"
	"testing"
)

// TestDatasourceV2GetReturnsFirstPageOnly 固定 get 只返回首个 chunk 页及后续游标。
func TestDatasourceV2GetReturnsFirstPageOnly(t *testing.T) {
	t.Parallel()

	store := newReadyDatasourceV2Store()
	store.document.ChunkCount = int32(datasourceV2DefaultChunkPageLimit + 1)
	store.chunks = datasourceV2StoreChunks(101, datasourceV2SequentialChunkContents(datasourceV2DefaultChunkPageLimit+1)...)
	svc := newDatasourceV2TestService(store)

	got, err := svc.GetDocument(context.Background(), GetDocumentRequest{DocumentID: 101})

	if err != nil {
		t.Fatalf("GetDocument() error = %v", err)
	}
	if len(got.Chunks) != datasourceV2DefaultChunkPageLimit {
		t.Fatalf("GetDocument() chunks = %d, want first page size %d", len(got.Chunks), datasourceV2DefaultChunkPageLimit)
	}
	wantCursor := int32(datasourceV2DefaultChunkPageLimit - 1)
	if !got.HasMore || got.NextCursor != wantCursor {
		t.Fatalf("GetDocument() cursor = hasMore:%v next:%d, want true/%d", got.HasMore, got.NextCursor, wantCursor)
	}
}

// TestDatasourceV2ListChunksUsesCursorAndLimit 固定 list_chunks 需要显式 limit 和 cursor。
func TestDatasourceV2ListChunksUsesCursorAndLimit(t *testing.T) {
	t.Parallel()

	store := newReadyDatasourceV2Store()
	store.chunks = datasourceV2StoreChunks(101, "chunk-0", "chunk-1", "chunk-2")
	svc := newDatasourceV2TestService(store)
	cursor := int32(0)

	got, err := svc.ListChunks(context.Background(), ListChunksRequest{
		DocumentID: 101,
		Limit:      1,
		Cursor:     &cursor,
	})

	if err != nil {
		t.Fatalf("ListChunks() error = %v", err)
	}
	if len(got.Chunks) != 1 || got.Chunks[0].Content != "chunk-1" {
		t.Fatalf("ListChunks() chunks = %+v, want chunk-1 only", got.Chunks)
	}
	if !got.HasMore || got.NextCursor != 1 {
		t.Fatalf("ListChunks() cursor = hasMore:%v next:%d, want true/1", got.HasMore, got.NextCursor)
	}
}

// TestDatasourceV2GetCapsResponseBytes 固定 get 响应在返回前执行字节上限。
func TestDatasourceV2GetCapsResponseBytes(t *testing.T) {
	t.Parallel()

	store := newReadyDatasourceV2Store()
	store.document.ChunkCount = 1
	store.chunks = datasourceV2StoreChunks(101, strings.Repeat("x", 256*1024))
	svc := newDatasourceV2TestService(store)

	_, err := svc.GetDocument(context.Background(), GetDocumentRequest{DocumentID: 101})

	if err == nil || !strings.Contains(err.Error(), "response byte cap") {
		t.Fatalf("GetDocument() error = %v, want response byte cap", err)
	}
}

// datasourceV2StoreChunks builds ready text chunks for datasource_v2 service tests.
func datasourceV2StoreChunks(documentID int64, contents ...string) []TextChunk {
	chunks := make([]TextChunk, 0, len(contents))
	for i, content := range contents {
		chunks = append(chunks, TextChunk{
			ID:             int64(i + 1),
			DocumentID:     documentID,
			ChunkIndex:     int32(i),
			Content:        content,
			CharCount:      int32(len([]rune(content))),
			ByteCount:      int32(len([]byte(content))),
			EmbeddingModel: datasourceV2EmbeddingModel,
			EmbeddingDim:   datasourceV2EmbeddingDimension,
		})
	}
	return chunks
}

// datasourceV2SequentialChunkContents returns stable chunk bodies for page tests.
func datasourceV2SequentialChunkContents(count int) []string {
	contents := make([]string, 0, count)
	for i := range count {
		contents = append(contents, "chunk-"+string(rune('a'+i%26)))
	}
	return contents
}
