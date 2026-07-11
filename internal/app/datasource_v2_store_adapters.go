package app

import (
	"context"
	"errors"

	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
)

var errDatasourceV2StoreReturnedNilDocument = errors.New("datasource v2 Store returned nil document")

type datasourceV2StoreAdapter struct {
	*datasourceV2DocumentStoreAdapter
	*datasourceV2ChunkStoreAdapter
	*datasourceV2ImportStoreAdapter
	store datasourcev2store.Store
}

type datasourceV2DocumentStoreAdapter struct {
	store datasourcev2store.Store
}

type datasourceV2ChunkStoreAdapter struct {
	store datasourcev2store.Store
}

type datasourceV2ImportStoreAdapter struct {
	store datasourcev2store.Store
}

var _ datasourcev2.Store = (*datasourceV2StoreAdapter)(nil)

// provideDatasourceV2Store 把 required Store 投影为 datasource_v2-owned 端口。
func provideDatasourceV2Store(store datasourcev2store.Store) (datasourcev2.Store, error) {
	if err := requireDatasourceV2StoreAdapterStore(store); err != nil {
		return nil, err
	}
	return makeDatasourceV2StoreAdapter(store), nil
}

func makeDatasourceV2StoreAdapter(store datasourcev2store.Store) *datasourceV2StoreAdapter {
	return &datasourceV2StoreAdapter{
		datasourceV2DocumentStoreAdapter: &datasourceV2DocumentStoreAdapter{store: store},
		datasourceV2ChunkStoreAdapter:    &datasourceV2ChunkStoreAdapter{store: store},
		datasourceV2ImportStoreAdapter:   &datasourceV2ImportStoreAdapter{store: store},
		store:                            store,
	}
}

// WithTx 在同一底层事务 Store 上重建领域 adapter。
func (a *datasourceV2StoreAdapter) WithTx(
	ctx context.Context,
	fn func(txStore datasourcev2.Store) error,
) error {
	if fn == nil {
		return datasourcev2.ErrStoreTxCallbackRequired
	}
	if a == nil {
		return datasourcev2.ErrStoreNotConfigured
	}
	if err := requireDatasourceV2StoreAdapterStore(a.store); err != nil {
		return err
	}
	return a.store.WithTx(ctx, func(txStore datasourcev2store.Store) error {
		if err := requireDatasourceV2StoreAdapterStore(txStore); err != nil {
			return err
		}
		return fn(makeDatasourceV2StoreAdapter(txStore))
	})
}

// ListDocuments 查询文档列表并返回独立的领域切片。
func (a *datasourceV2DocumentStoreAdapter) ListDocuments(
	ctx context.Context,
	params datasourcev2.ListDocumentsParams,
) ([]datasourcev2.Document, error) {
	if err := a.requireStore(); err != nil {
		return nil, err
	}
	docs, err := a.store.ListDocuments(ctx, toStoreDatasourceV2ListDocumentsParams(params))
	if err != nil {
		return nil, err
	}
	return fromStoreDatasourceV2Documents(docs), nil
}

// GetDocument 读取单篇文档并拒绝 Store 的 nil 成功结果。
func (a *datasourceV2DocumentStoreAdapter) GetDocument(
	ctx context.Context,
	documentID int64,
) (*datasourcev2.Document, error) {
	if err := a.requireStore(); err != nil {
		return nil, err
	}
	doc, err := a.store.GetDocument(ctx, documentID)
	return datasourceV2DocumentResult(doc, err)
}

// UpdateDocument 更新文档元数据并拒绝 Store 的 nil 成功结果。
func (a *datasourceV2DocumentStoreAdapter) UpdateDocument(
	ctx context.Context,
	params datasourcev2.UpdateDocumentParams,
) (*datasourcev2.Document, error) {
	if err := a.requireStore(); err != nil {
		return nil, err
	}
	doc, err := a.store.UpdateDocument(ctx, toStoreDatasourceV2UpdateDocumentParams(params))
	return datasourceV2DocumentResult(doc, err)
}

// DeleteDocument 删除文档并保留 Store 错误身份。
func (a *datasourceV2DocumentStoreAdapter) DeleteDocument(ctx context.Context, documentID int64) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	return a.store.DeleteDocument(ctx, documentID)
}

// ListChunksPage 查询有界分页并复制 page 及其分块切片。
func (a *datasourceV2ChunkStoreAdapter) ListChunksPage(
	ctx context.Context,
	params datasourcev2.ListChunksParams,
) (datasourcev2.TextChunkPage, error) {
	if err := a.requireStore(); err != nil {
		return datasourcev2.TextChunkPage{}, err
	}
	page, err := a.store.ListChunksPage(ctx, toStoreDatasourceV2ListChunksParams(params))
	if err != nil {
		return datasourcev2.TextChunkPage{}, err
	}
	return fromStoreDatasourceV2TextChunkPage(page), nil
}

// SearchChunks 执行语义检索并复制查询向量及结果集合。
func (a *datasourceV2ChunkStoreAdapter) SearchChunks(
	ctx context.Context,
	params datasourcev2.SearchChunksParams,
) ([]datasourcev2.SemanticChunk, error) {
	if err := a.requireStore(); err != nil {
		return nil, err
	}
	chunks, err := a.store.SearchChunks(ctx, toStoreDatasourceV2SearchChunksParams(params))
	if err != nil {
		return nil, err
	}
	return fromStoreDatasourceV2SemanticChunks(chunks), nil
}

// UpsertImporting 写入 importing 文档并拒绝 Store 的 nil 成功结果。
func (a *datasourceV2ImportStoreAdapter) UpsertImporting(
	ctx context.Context,
	params datasourcev2.UpsertDocumentParams,
) (*datasourcev2.Document, error) {
	if err := a.requireStore(); err != nil {
		return nil, err
	}
	doc, err := a.store.UpsertImporting(ctx, toStoreDatasourceV2UpsertDocumentParams(params))
	return datasourceV2DocumentResult(doc, err)
}

// DeleteChunks 清理旧分块并保留 Store 错误身份。
func (a *datasourceV2ImportStoreAdapter) DeleteChunks(ctx context.Context, documentID int64) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	return a.store.DeleteChunks(ctx, documentID)
}

// InsertChunk 写入单个分块并复制向量字节。
func (a *datasourceV2ImportStoreAdapter) InsertChunk(
	ctx context.Context,
	params datasourcev2.InsertChunkParams,
) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	return a.store.InsertChunk(ctx, toStoreDatasourceV2InsertChunkParams(params))
}

// MarkReady 完成导入并拒绝 Store 的 nil 成功结果。
func (a *datasourceV2ImportStoreAdapter) MarkReady(
	ctx context.Context,
	params datasourcev2.MarkReadyParams,
) (*datasourcev2.Document, error) {
	if err := a.requireStore(); err != nil {
		return nil, err
	}
	doc, err := a.store.MarkReady(ctx, toStoreDatasourceV2MarkReadyParams(params))
	return datasourceV2DocumentResult(doc, err)
}

func (a *datasourceV2DocumentStoreAdapter) requireStore() error {
	if a == nil {
		return datasourcev2.ErrStoreNotConfigured
	}
	return requireDatasourceV2StoreAdapterStore(a.store)
}

func (a *datasourceV2ChunkStoreAdapter) requireStore() error {
	if a == nil {
		return datasourcev2.ErrStoreNotConfigured
	}
	return requireDatasourceV2StoreAdapterStore(a.store)
}

func (a *datasourceV2ImportStoreAdapter) requireStore() error {
	if a == nil {
		return datasourcev2.ErrStoreNotConfigured
	}
	return requireDatasourceV2StoreAdapterStore(a.store)
}

func requireDatasourceV2StoreAdapterStore(store datasourcev2store.Store) error {
	if isNilBusinessStore(store) {
		return datasourcev2.ErrStoreNotConfigured
	}
	return nil
}

func datasourceV2DocumentResult(
	doc *datasourcev2store.Document,
	err error,
) (*datasourcev2.Document, error) {
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errDatasourceV2StoreReturnedNilDocument
	}
	converted := fromStoreDatasourceV2Document(*doc)
	return &converted, nil
}

func toStoreDatasourceV2ListDocumentsParams(params datasourcev2.ListDocumentsParams) datasourcev2store.ListDocumentsParams {
	return datasourcev2store.ListDocumentsParams{Keyword: params.Keyword, Limit: params.Limit}
}

func toStoreDatasourceV2ListChunksParams(params datasourcev2.ListChunksParams) datasourcev2store.ListChunksParams {
	return datasourcev2store.ListChunksParams{DocumentID: params.DocumentID, Limit: params.Limit, Cursor: params.Cursor}
}

func toStoreDatasourceV2SearchChunksParams(params datasourcev2.SearchChunksParams) datasourcev2store.SearchChunksParams {
	return datasourcev2store.SearchChunksParams{
		Embedding:      copyDatasourceV2Embedding(params.Embedding),
		EmbeddingModel: params.EmbeddingModel,
		EmbeddingDim:   params.EmbeddingDim,
		Limit:          params.Limit,
	}
}

func toStoreDatasourceV2UpsertDocumentParams(params datasourcev2.UpsertDocumentParams) datasourcev2store.UpsertDocumentParams {
	return datasourcev2store.UpsertDocumentParams{
		SourcePath: params.SourcePath,
		FileName:   params.FileName,
		Extension:  params.Extension,
		SizeBytes:  params.SizeBytes,
	}
}

func toStoreDatasourceV2UpdateDocumentParams(params datasourcev2.UpdateDocumentParams) datasourcev2store.UpdateDocumentParams {
	return datasourcev2store.UpdateDocumentParams{
		DocumentID: params.DocumentID,
		SourcePath: params.SourcePath,
		FileName:   params.FileName,
		Extension:  params.Extension,
		SizeBytes:  params.SizeBytes,
	}
}

func toStoreDatasourceV2InsertChunkParams(params datasourcev2.InsertChunkParams) datasourcev2store.InsertChunkParams {
	return datasourcev2store.InsertChunkParams{
		DocumentID:     params.DocumentID,
		ChunkIndex:     params.ChunkIndex,
		Content:        params.Content,
		CharCount:      params.CharCount,
		ByteCount:      params.ByteCount,
		Embedding:      copyDatasourceV2Embedding(params.Embedding),
		EmbeddingModel: params.EmbeddingModel,
		EmbeddingDim:   params.EmbeddingDim,
		TokenCount:     params.TokenCount,
	}
}

func toStoreDatasourceV2MarkReadyParams(params datasourcev2.MarkReadyParams) datasourcev2store.MarkReadyParams {
	return datasourcev2store.MarkReadyParams{
		DocumentID:  params.DocumentID,
		ContentHash: params.ContentHash,
		ChunkCount:  params.ChunkCount,
		TotalChars:  params.TotalChars,
	}
}

func fromStoreDatasourceV2Documents(docs []datasourcev2store.Document) []datasourcev2.Document {
	results := make([]datasourcev2.Document, len(docs))
	for index, doc := range docs {
		results[index] = fromStoreDatasourceV2Document(doc)
	}
	return results
}

func fromStoreDatasourceV2Document(doc datasourcev2store.Document) datasourcev2.Document {
	return datasourcev2.Document{
		ID:           doc.ID,
		SourcePath:   doc.SourcePath,
		FileName:     doc.FileName,
		Extension:    doc.Extension,
		SizeBytes:    doc.SizeBytes,
		ContentHash:  doc.ContentHash,
		ChunkCount:   doc.ChunkCount,
		TotalChars:   doc.TotalChars,
		Status:       doc.Status,
		ErrorMessage: doc.ErrorMessage,
		CreatedAt:    doc.CreatedAt,
		UpdatedAt:    doc.UpdatedAt,
	}
}

func fromStoreDatasourceV2TextChunks(chunks []datasourcev2store.TextChunk) []datasourcev2.TextChunk {
	results := make([]datasourcev2.TextChunk, len(chunks))
	for index, chunk := range chunks {
		results[index] = fromStoreDatasourceV2TextChunk(chunk)
	}
	return results
}

func fromStoreDatasourceV2TextChunk(chunk datasourcev2store.TextChunk) datasourcev2.TextChunk {
	return datasourcev2.TextChunk{
		ID:             chunk.ID,
		DocumentID:     chunk.DocumentID,
		ChunkIndex:     chunk.ChunkIndex,
		Content:        chunk.Content,
		CharCount:      chunk.CharCount,
		ByteCount:      chunk.ByteCount,
		Embedding:      copyDatasourceV2Embedding(chunk.Embedding),
		EmbeddingModel: chunk.EmbeddingModel,
		EmbeddingDim:   chunk.EmbeddingDim,
		TokenCount:     chunk.TokenCount,
		CreatedAt:      chunk.CreatedAt,
	}
}

func fromStoreDatasourceV2TextChunkPage(page datasourcev2store.TextChunkPage) datasourcev2.TextChunkPage {
	return datasourcev2.TextChunkPage{
		Chunks:     fromStoreDatasourceV2TextChunks(page.Chunks),
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}
}

func fromStoreDatasourceV2SemanticChunks(chunks []datasourcev2store.SemanticChunk) []datasourcev2.SemanticChunk {
	results := make([]datasourcev2.SemanticChunk, len(chunks))
	for index, chunk := range chunks {
		results[index] = fromStoreDatasourceV2SemanticChunk(chunk)
	}
	return results
}

func fromStoreDatasourceV2SemanticChunk(chunk datasourcev2store.SemanticChunk) datasourcev2.SemanticChunk {
	return datasourcev2.SemanticChunk{
		TextChunk:  fromStoreDatasourceV2TextChunk(chunk.TextChunk),
		SourcePath: chunk.SourcePath,
		FileName:   chunk.FileName,
		Distance:   chunk.Distance,
	}
}

func copyDatasourceV2Embedding(embedding []byte) []byte {
	return append([]byte(nil), embedding...)
}
