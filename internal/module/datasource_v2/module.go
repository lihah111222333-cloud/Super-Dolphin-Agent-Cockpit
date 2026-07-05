// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
package datasourcev2

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
	"go.uber.org/fx"
)

type promptProviderParams struct {
	fx.In

	Registry contract.DynamicSectionRegistrar `optional:"true"`
	Provider *PromptProvider                  `optional:"true"`
}

// Module 注册 datasource_v2 的 service、JSON-RPC handler 和 prompt 动态段。
var Module = fx.Module("datasource_v2",
	fx.Provide(
		newDatasourceV2StorePort,
		NewService,
		NewHandlers,
		NewPromptProvider,
	),
	fx.Invoke(registerPromptProvider),
)

type datasourceV2StorePort struct {
	store datasourcev2store.Store
}

// newDatasourceV2StorePort 把 store concrete 投影为 datasource_v2 模块消费的本地端口。
// store DTO 到模块 DTO 的转换集中在装配边界，避免 service/chunks 直接依赖 store 包。
func newDatasourceV2StorePort(store datasourcev2store.Store) datasourceV2Store {
	return datasourceV2StorePort{store: store}
}

// WithTx 在 store 事务内重新包一层模块端口，保证事务回调也不泄漏 concrete store。
func (p datasourceV2StorePort) WithTx(
	ctx context.Context,
	fn func(txStore datasourceV2Store) error,
) error {
	if err := p.requireStore(); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("datasource v2 store tx callback is required")
	}
	return p.store.WithTx(ctx, func(txStore datasourcev2store.Store) error {
		return fn(datasourceV2StorePort{store: txStore})
	})
}

// ListDocuments 查询文档列表，并把 store 文档 DTO 转为模块 DTO。
func (p datasourceV2StorePort) ListDocuments(
	ctx context.Context,
	params datasourceV2ListDocumentsParams,
) ([]datasourceV2Document, error) {
	if err := p.requireStore(); err != nil {
		return nil, err
	}
	docs, err := p.store.ListDocuments(ctx, datasourcev2store.ListDocumentsParams{
		Keyword: params.Keyword,
		Limit:   params.Limit,
	})
	if err != nil {
		return nil, err
	}
	return datasourceV2DocumentsFromStore(docs), nil
}

// GetDocument 读取单篇文档，并在装配边界完成 DTO 转换。
func (p datasourceV2StorePort) GetDocument(ctx context.Context, documentID int64) (*datasourceV2Document, error) {
	if err := p.requireStore(); err != nil {
		return nil, err
	}
	doc, err := p.store.GetDocument(ctx, documentID)
	if err != nil {
		return nil, err
	}
	converted := datasourceV2DocumentFromStore(*doc)
	return &converted, nil
}

// ListChunksPage 读取文档正文分块页，并隐藏 store 层的分块类型。
func (p datasourceV2StorePort) ListChunksPage(
	ctx context.Context,
	params datasourceV2ListChunksParams,
) (datasourceV2TextChunkPage, error) {
	if err := p.requireStore(); err != nil {
		return datasourceV2TextChunkPage{}, err
	}
	page, err := p.store.ListChunksPage(ctx, datasourcev2store.ListChunksParams{
		DocumentID: params.DocumentID,
		Limit:      params.Limit,
		Cursor:     params.Cursor,
	})
	if err != nil {
		return datasourceV2TextChunkPage{}, err
	}
	return datasourceV2TextChunkPage{
		Chunks:     datasourceV2TextChunksFromStore(page.Chunks),
		HasMore:    page.HasMore,
		NextCursor: page.NextCursor,
	}, nil
}

// SearchChunks 执行语义检索，并把带距离的分块结果转成模块 DTO。
func (p datasourceV2StorePort) SearchChunks(
	ctx context.Context,
	params datasourceV2SearchChunksParams,
) ([]datasourceV2SemanticChunk, error) {
	if err := p.requireStore(); err != nil {
		return nil, err
	}
	chunks, err := p.store.SearchChunks(ctx, datasourcev2store.SearchChunksParams{
		Embedding:      params.Embedding,
		EmbeddingModel: params.EmbeddingModel,
		EmbeddingDim:   params.EmbeddingDim,
		Limit:          params.Limit,
	})
	if err != nil {
		return nil, err
	}
	return datasourceV2SemanticChunksFromStore(chunks), nil
}

// UpsertImporting 写入或重置 importing 文档元数据，参数在边界转换为 store DTO。
func (p datasourceV2StorePort) UpsertImporting(
	ctx context.Context,
	params datasourceV2UpsertDocumentParams,
) (*datasourceV2Document, error) {
	if err := p.requireStore(); err != nil {
		return nil, err
	}
	doc, err := p.store.UpsertImporting(ctx, datasourcev2store.UpsertDocumentParams{
		SourcePath: params.SourcePath,
		FileName:   params.FileName,
		Extension:  params.Extension,
		SizeBytes:  params.SizeBytes,
	})
	if err != nil {
		return nil, err
	}
	converted := datasourceV2DocumentFromStore(*doc)
	return &converted, nil
}

// UpdateDocument 更新文档元数据，并保持正文分块不经由该路径改写。
func (p datasourceV2StorePort) UpdateDocument(
	ctx context.Context,
	params datasourceV2UpdateDocumentParams,
) (*datasourceV2Document, error) {
	if err := p.requireStore(); err != nil {
		return nil, err
	}
	doc, err := p.store.UpdateDocument(ctx, datasourcev2store.UpdateDocumentParams{
		DocumentID: params.DocumentID,
		SourcePath: params.SourcePath,
		FileName:   params.FileName,
		Extension:  params.Extension,
		SizeBytes:  params.SizeBytes,
	})
	if err != nil {
		return nil, err
	}
	converted := datasourceV2DocumentFromStore(*doc)
	return &converted, nil
}

// DeleteDocument 删除文档，级联清理由 store 层维持。
func (p datasourceV2StorePort) DeleteDocument(ctx context.Context, documentID int64) error {
	if err := p.requireStore(); err != nil {
		return err
	}
	return p.store.DeleteDocument(ctx, documentID)
}

// DeleteChunks 清理指定文档旧分块，供同事务导入流程重写正文。
func (p datasourceV2StorePort) DeleteChunks(ctx context.Context, documentID int64) error {
	if err := p.requireStore(); err != nil {
		return err
	}
	return p.store.DeleteChunks(ctx, documentID)
}

// InsertChunk 写入单个文本分块，并在边界转换向量和统计字段。
func (p datasourceV2StorePort) InsertChunk(ctx context.Context, params datasourceV2InsertChunkParams) error {
	if err := p.requireStore(); err != nil {
		return err
	}
	return p.store.InsertChunk(ctx, datasourcev2store.InsertChunkParams{
		DocumentID:     params.DocumentID,
		ChunkIndex:     params.ChunkIndex,
		Content:        params.Content,
		CharCount:      params.CharCount,
		ByteCount:      params.ByteCount,
		Embedding:      params.Embedding,
		EmbeddingModel: params.EmbeddingModel,
		EmbeddingDim:   params.EmbeddingDim,
		TokenCount:     params.TokenCount,
	})
}

// MarkReady 在导入事务末尾写入摘要字段，并把文档推进为 ready。
func (p datasourceV2StorePort) MarkReady(
	ctx context.Context,
	params datasourceV2MarkReadyParams,
) (*datasourceV2Document, error) {
	if err := p.requireStore(); err != nil {
		return nil, err
	}
	doc, err := p.store.MarkReady(ctx, datasourcev2store.MarkReadyParams{
		DocumentID:  params.DocumentID,
		ContentHash: params.ContentHash,
		ChunkCount:  params.ChunkCount,
		TotalChars:  params.TotalChars,
	})
	if err != nil {
		return nil, err
	}
	converted := datasourceV2DocumentFromStore(*doc)
	return &converted, nil
}

func (p datasourceV2StorePort) requireStore() error {
	if p.store == nil {
		return errDatasourceV2StoreNotConfigured
	}
	return nil
}

func datasourceV2DocumentsFromStore(docs []datasourcev2store.Document) []datasourceV2Document {
	results := make([]datasourceV2Document, 0, len(docs))
	for _, doc := range docs {
		results = append(results, datasourceV2DocumentFromStore(doc))
	}
	return results
}

func datasourceV2DocumentFromStore(doc datasourcev2store.Document) datasourceV2Document {
	return datasourceV2Document{
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

func datasourceV2TextChunksFromStore(chunks []datasourcev2store.TextChunk) []datasourceV2TextChunk {
	results := make([]datasourceV2TextChunk, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, datasourceV2TextChunkFromStore(chunk))
	}
	return results
}

func datasourceV2TextChunkFromStore(chunk datasourcev2store.TextChunk) datasourceV2TextChunk {
	return datasourceV2TextChunk{
		ID:             chunk.ID,
		DocumentID:     chunk.DocumentID,
		ChunkIndex:     chunk.ChunkIndex,
		Content:        chunk.Content,
		CharCount:      chunk.CharCount,
		ByteCount:      chunk.ByteCount,
		Embedding:      chunk.Embedding,
		EmbeddingModel: chunk.EmbeddingModel,
		EmbeddingDim:   chunk.EmbeddingDim,
		TokenCount:     chunk.TokenCount,
		CreatedAt:      chunk.CreatedAt,
	}
}

func datasourceV2SemanticChunksFromStore(chunks []datasourcev2store.SemanticChunk) []datasourceV2SemanticChunk {
	results := make([]datasourceV2SemanticChunk, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, datasourceV2SemanticChunkFromStore(chunk))
	}
	return results
}

func datasourceV2SemanticChunkFromStore(chunk datasourcev2store.SemanticChunk) datasourceV2SemanticChunk {
	return datasourceV2SemanticChunk{
		datasourceV2TextChunk: datasourceV2TextChunkFromStore(chunk.TextChunk),
		SourcePath:            chunk.SourcePath,
		FileName:              chunk.FileName,
		Distance:              chunk.Distance,
	}
}

// registerPromptProvider 将 datasource_v2 检索 provider 接入 prompt 动态段系统。
// 依赖在精简运行模式下可以缺失；此时只跳过 prompt 注入，不影响导入和查询 RPC。
func registerPromptProvider(p promptProviderParams) error {
	if p.Registry == nil || p.Provider == nil {
		return nil
	}
	return p.Registry.RegisterDynamicProvider(p.Provider)
}
