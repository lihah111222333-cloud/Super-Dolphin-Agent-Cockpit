package datasourcev2adapter_test

import (
	"context"

	datasourcev2 "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/datasource_v2"
)

type externalDatasourceV2DocumentStore struct{}

func (externalDatasourceV2DocumentStore) ListDocuments(
	context.Context,
	datasourcev2.ListDocumentsParams,
) ([]datasourcev2.Document, error) {
	return nil, nil
}

func (externalDatasourceV2DocumentStore) GetDocument(
	context.Context,
	int64,
) (*datasourcev2.Document, error) {
	return nil, nil
}

func (externalDatasourceV2DocumentStore) UpdateDocument(
	context.Context,
	datasourcev2.UpdateDocumentParams,
) (*datasourcev2.Document, error) {
	return nil, nil
}

func (externalDatasourceV2DocumentStore) DeleteDocument(context.Context, int64) error {
	return nil
}

type externalDatasourceV2ChunkStore struct{}

func (externalDatasourceV2ChunkStore) ListChunksPage(
	context.Context,
	datasourcev2.ListChunksParams,
) (datasourcev2.TextChunkPage, error) {
	return datasourcev2.TextChunkPage{}, nil
}

func (externalDatasourceV2ChunkStore) SearchChunks(
	context.Context,
	datasourcev2.SearchChunksParams,
) ([]datasourcev2.SemanticChunk, error) {
	return nil, nil
}

type externalDatasourceV2ImportStore struct{}

func (externalDatasourceV2ImportStore) UpsertImporting(
	context.Context,
	datasourcev2.UpsertDocumentParams,
) (*datasourcev2.Document, error) {
	return nil, nil
}

func (externalDatasourceV2ImportStore) DeleteChunks(context.Context, int64) error {
	return nil
}

func (externalDatasourceV2ImportStore) InsertChunk(
	context.Context,
	datasourcev2.InsertChunkParams,
) error {
	return nil
}

func (externalDatasourceV2ImportStore) MarkReady(
	context.Context,
	datasourcev2.MarkReadyParams,
) (*datasourcev2.Document, error) {
	return nil, nil
}

type externalDatasourceV2Store struct {
	externalDatasourceV2DocumentStore
	externalDatasourceV2ChunkStore
	externalDatasourceV2ImportStore
}

func (s externalDatasourceV2Store) WithTx(
	ctx context.Context,
	fn func(datasourcev2.Store) error,
) error {
	return fn(s)
}

var _ datasourcev2.Store = externalDatasourceV2Store{}
var _ = datasourcev2.ErrStoreNotConfigured
var _ = datasourcev2.ErrStoreTxCallbackRequired
