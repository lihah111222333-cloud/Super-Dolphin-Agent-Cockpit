package app

import (
	"context"
	"errors"
	"testing"

	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
)

type datasourceV2StoreTestState struct {
	withTx          func(context.Context, func(datasourcev2store.Store) error) error
	listDocuments   func(context.Context, datasourcev2store.ListDocumentsParams) ([]datasourcev2store.Document, error)
	getDocument     func(context.Context, int64) (*datasourcev2store.Document, error)
	listChunksPage  func(context.Context, datasourcev2store.ListChunksParams) (datasourcev2store.TextChunkPage, error)
	searchChunks    func(context.Context, datasourcev2store.SearchChunksParams) ([]datasourcev2store.SemanticChunk, error)
	upsertImporting func(context.Context, datasourcev2store.UpsertDocumentParams) (*datasourcev2store.Document, error)
	updateDocument  func(context.Context, datasourcev2store.UpdateDocumentParams) (*datasourcev2store.Document, error)
	deleteDocument  func(context.Context, int64) error
	deleteChunks    func(context.Context, int64) error
	insertChunk     func(context.Context, datasourcev2store.InsertChunkParams) error
	markReady       func(context.Context, datasourcev2store.MarkReadyParams) (*datasourcev2store.Document, error)
}

type datasourceV2DocumentStoreTestDouble struct{ state *datasourceV2StoreTestState }

func (s *datasourceV2DocumentStoreTestDouble) ListDocuments(
	ctx context.Context,
	params datasourcev2store.ListDocumentsParams,
) ([]datasourcev2store.Document, error) {
	if s.state.listDocuments != nil {
		return s.state.listDocuments(ctx, params)
	}
	return nil, nil
}

func (s *datasourceV2DocumentStoreTestDouble) GetDocument(
	ctx context.Context,
	documentID int64,
) (*datasourcev2store.Document, error) {
	if s.state.getDocument != nil {
		return s.state.getDocument(ctx, documentID)
	}
	return &datasourcev2store.Document{}, nil
}

func (s *datasourceV2DocumentStoreTestDouble) UpdateDocument(
	ctx context.Context,
	params datasourcev2store.UpdateDocumentParams,
) (*datasourcev2store.Document, error) {
	if s.state.updateDocument != nil {
		return s.state.updateDocument(ctx, params)
	}
	return &datasourcev2store.Document{}, nil
}

func (s *datasourceV2DocumentStoreTestDouble) DeleteDocument(ctx context.Context, documentID int64) error {
	if s.state.deleteDocument != nil {
		return s.state.deleteDocument(ctx, documentID)
	}
	return nil
}

type datasourceV2ChunkStoreTestDouble struct{ state *datasourceV2StoreTestState }

func (s *datasourceV2ChunkStoreTestDouble) ListChunksPage(
	ctx context.Context,
	params datasourcev2store.ListChunksParams,
) (datasourcev2store.TextChunkPage, error) {
	if s.state.listChunksPage != nil {
		return s.state.listChunksPage(ctx, params)
	}
	return datasourcev2store.TextChunkPage{}, nil
}

func (s *datasourceV2ChunkStoreTestDouble) SearchChunks(
	ctx context.Context,
	params datasourcev2store.SearchChunksParams,
) ([]datasourcev2store.SemanticChunk, error) {
	if s.state.searchChunks != nil {
		return s.state.searchChunks(ctx, params)
	}
	return nil, nil
}

type datasourceV2ImportStoreTestDouble struct{ state *datasourceV2StoreTestState }

func (s *datasourceV2ImportStoreTestDouble) UpsertImporting(
	ctx context.Context,
	params datasourcev2store.UpsertDocumentParams,
) (*datasourcev2store.Document, error) {
	if s.state.upsertImporting != nil {
		return s.state.upsertImporting(ctx, params)
	}
	return &datasourcev2store.Document{}, nil
}

func (s *datasourceV2ImportStoreTestDouble) DeleteChunks(ctx context.Context, documentID int64) error {
	if s.state.deleteChunks != nil {
		return s.state.deleteChunks(ctx, documentID)
	}
	return nil
}

func (s *datasourceV2ImportStoreTestDouble) InsertChunk(
	ctx context.Context,
	params datasourcev2store.InsertChunkParams,
) error {
	if s.state.insertChunk != nil {
		return s.state.insertChunk(ctx, params)
	}
	return nil
}

func (s *datasourceV2ImportStoreTestDouble) MarkReady(
	ctx context.Context,
	params datasourcev2store.MarkReadyParams,
) (*datasourcev2store.Document, error) {
	if s.state.markReady != nil {
		return s.state.markReady(ctx, params)
	}
	return &datasourcev2store.Document{}, nil
}

type datasourceV2StoreTestDouble struct {
	*datasourceV2DocumentStoreTestDouble
	*datasourceV2ChunkStoreTestDouble
	*datasourceV2ImportStoreTestDouble
	state *datasourceV2StoreTestState
}

func newDatasourceV2StoreTestDouble(state *datasourceV2StoreTestState) *datasourceV2StoreTestDouble {
	if state == nil {
		state = &datasourceV2StoreTestState{}
	}
	return &datasourceV2StoreTestDouble{
		datasourceV2DocumentStoreTestDouble: &datasourceV2DocumentStoreTestDouble{state: state},
		datasourceV2ChunkStoreTestDouble:    &datasourceV2ChunkStoreTestDouble{state: state},
		datasourceV2ImportStoreTestDouble:   &datasourceV2ImportStoreTestDouble{state: state},
		state:                               state,
	}
}

func (s *datasourceV2StoreTestDouble) WithTx(
	ctx context.Context,
	fn func(datasourcev2store.Store) error,
) error {
	if s.state.withTx != nil {
		return s.state.withTx(ctx, fn)
	}
	return fn(s)
}

var _ datasourcev2store.Store = (*datasourceV2StoreTestDouble)(nil)

// TestDatasourceV2StoreAdapterRequiredConstructor 固定 datasource_v2 Store 在 App 边界为 required 依赖。
func TestDatasourceV2StoreAdapterRequiredConstructor(t *testing.T) {
	if _, err := provideDatasourceV2Store(nil); !errors.Is(err, datasourcev2.ErrStoreNotConfigured) {
		t.Fatalf("nil Store error = %v, want ErrStoreNotConfigured", err)
	}
	var typedNil *datasourceV2StoreTestDouble
	if _, err := provideDatasourceV2Store(typedNil); !errors.Is(err, datasourcev2.ErrStoreNotConfigured) {
		t.Fatalf("typed nil Store error = %v, want ErrStoreNotConfigured", err)
	}
}

// TestDatasourceV2StoreAdapterRequiresTxCallback 固定 nil transaction callback 在调用 Store 前直接失败。
func TestDatasourceV2StoreAdapterRequiresTxCallback(t *testing.T) {
	called := false
	store := newDatasourceV2StoreTestDouble(&datasourceV2StoreTestState{
		withTx: func(context.Context, func(datasourcev2store.Store) error) error {
			called = true
			return nil
		},
	})
	port := requireDatasourceV2StorePort(t, store)
	err := port.WithTx(context.Background(), nil)
	if !errors.Is(err, datasourcev2.ErrStoreTxCallbackRequired) || called {
		t.Fatalf("WithTx(nil) = %v, Store called=%v", err, called)
	}
}

// TestDatasourceV2StoreAdapterRejectsNilTxStore 固定底层 nil/typed nil tx Store 不会进入领域 callback。
func TestDatasourceV2StoreAdapterRejectsNilTxStore(t *testing.T) {
	tests := map[string]datasourcev2store.Store{"nil": nil}
	var typedNil *datasourceV2StoreTestDouble
	tests["typed_nil"] = typedNil
	for name, txStore := range tests {
		t.Run(name, func(t *testing.T) {
			callbackCalled := false
			root := newDatasourceV2StoreTestDouble(&datasourceV2StoreTestState{
				withTx: func(ctx context.Context, fn func(datasourcev2store.Store) error) error {
					return fn(txStore)
				},
			})
			err := requireDatasourceV2StorePort(t, root).WithTx(context.Background(), func(datasourcev2.Store) error {
				callbackCalled = true
				return nil
			})
			if !errors.Is(err, datasourcev2.ErrStoreNotConfigured) || callbackCalled {
				t.Fatalf("WithTx = %v, callback called=%v", err, callbackCalled)
			}
		})
	}
}

// TestDatasourceV2StoreAdapterUsesTransactionStore 固定领域 callback 操作的是底层传入的同一 tx Store。
func TestDatasourceV2StoreAdapterUsesTransactionStore(t *testing.T) {
	txDeleteCalled := false
	txStore := newDatasourceV2StoreTestDouble(&datasourceV2StoreTestState{
		deleteDocument: func(_ context.Context, documentID int64) error {
			txDeleteCalled = documentID == 41
			return nil
		},
	})
	root := newDatasourceV2StoreTestDouble(&datasourceV2StoreTestState{
		withTx: func(_ context.Context, fn func(datasourcev2store.Store) error) error {
			return fn(txStore)
		},
		deleteDocument: func(context.Context, int64) error {
			return errors.New("root Store must not serve tx callback")
		},
	})
	err := requireDatasourceV2StorePort(t, root).WithTx(context.Background(), func(tx datasourcev2.Store) error {
		return tx.DeleteDocument(context.Background(), 41)
	})
	if err != nil || !txDeleteCalled {
		t.Fatalf("WithTx = %v, tx delete called=%v", err, txDeleteCalled)
	}
}

func requireDatasourceV2StorePort(t *testing.T, store datasourcev2store.Store) datasourcev2.Store {
	t.Helper()
	port, err := provideDatasourceV2Store(store)
	if err != nil {
		t.Fatalf("provide datasource_v2 Store: %v", err)
	}
	return port
}

// TestDatasourceV2StoreAdapterFieldCoverage 用 one-hot 输入覆盖全部参数及核心返回 DTO。
func TestDatasourceV2StoreAdapterFieldCoverage(t *testing.T) {
	t.Run("list_documents", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, toStoreDatasourceV2ListDocumentsParams)
	})
	t.Run("list_chunks", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, toStoreDatasourceV2ListChunksParams)
	})
	t.Run("search_chunks", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, toStoreDatasourceV2SearchChunksParams)
	})
	t.Run("upsert_document", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, toStoreDatasourceV2UpsertDocumentParams)
	})
	t.Run("update_document", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, toStoreDatasourceV2UpdateDocumentParams)
	})
	t.Run("insert_chunk", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, toStoreDatasourceV2InsertChunkParams)
	})
	t.Run("mark_ready", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, toStoreDatasourceV2MarkReadyParams)
	})
	t.Run("document", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, fromStoreDatasourceV2Document)
	})
	t.Run("text_chunk", func(t *testing.T) {
		assertDatasourceV2FieldsMap(t, fromStoreDatasourceV2TextChunk)
	})
}

func assertDatasourceV2FieldsMap[Source, Target any](t *testing.T, mapper func(Source) Target) {
	t.Helper()
	assertBusinessStoreAdapterFieldsMap(t, func(source Source) (Target, error) {
		return mapper(source), nil
	})
}

// TestDatasourceV2SemanticChunkMapping 固定 semantic metadata 与嵌入的 exported TextChunk 一并映射。
func TestDatasourceV2SemanticChunkMapping(t *testing.T) {
	stored := datasourcev2store.SemanticChunk{
		TextChunk:  datasourcev2store.TextChunk{ID: 41, Content: "body", Embedding: []byte{1, 2, 3}},
		SourcePath: "/workspace/source.txt",
		FileName:   "source.txt",
		Distance:   0.25,
	}
	got := fromStoreDatasourceV2SemanticChunk(stored)
	if got.ID != 41 || got.Content != "body" || got.SourcePath != stored.SourcePath ||
		got.FileName != stored.FileName || got.Distance != stored.Distance {
		t.Fatalf("semantic chunk mapping = %#v", got)
	}
}

// TestDatasourceV2StoreAdapterCopiesEmbeddings 固定向量在两侧边界都不共享可变字节。
func TestDatasourceV2StoreAdapterCopiesEmbeddings(t *testing.T) {
	t.Run("search_domain_to_store", func(t *testing.T) {
		source := datasourcev2.SearchChunksParams{Embedding: []byte{1, 2, 3}}
		mapped := toStoreDatasourceV2SearchChunksParams(source)
		mapped.Embedding[0] = 9
		if source.Embedding[0] != 1 {
			t.Fatalf("search embedding shared: %v", source.Embedding)
		}
	})
	t.Run("insert_domain_to_store", func(t *testing.T) {
		source := datasourcev2.InsertChunkParams{Embedding: []byte{1, 2, 3}}
		mapped := toStoreDatasourceV2InsertChunkParams(source)
		mapped.Embedding[0] = 9
		if source.Embedding[0] != 1 {
			t.Fatalf("insert embedding shared: %v", source.Embedding)
		}
	})
	t.Run("text_chunk_store_to_domain", func(t *testing.T) {
		source := datasourcev2store.TextChunk{Embedding: []byte{1, 2, 3}}
		mapped := fromStoreDatasourceV2TextChunk(source)
		mapped.Embedding[0] = 9
		if source.Embedding[0] != 1 {
			t.Fatalf("text chunk embedding shared: %v", source.Embedding)
		}
	})
	t.Run("semantic_store_to_domain", func(t *testing.T) {
		source := datasourcev2store.SemanticChunk{TextChunk: datasourcev2store.TextChunk{Embedding: []byte{1, 2, 3}}}
		mapped := fromStoreDatasourceV2SemanticChunk(source)
		mapped.Embedding[0] = 9
		if source.Embedding[0] != 1 {
			t.Fatalf("semantic embedding shared: %v", source.Embedding)
		}
	})
}

// TestDatasourceV2StoreAdapterReturnsIndependentCollections 固定 list/page 结果不共享 Store backing array。
func TestDatasourceV2StoreAdapterReturnsIndependentCollections(t *testing.T) {
	documents := []datasourcev2store.Document{{ID: 1}}
	chunks := []datasourcev2store.TextChunk{{ID: 2, Embedding: []byte{2}}}
	semantic := []datasourcev2store.SemanticChunk{{TextChunk: datasourcev2store.TextChunk{ID: 3, Embedding: []byte{3}}}}
	store := newDatasourceV2StoreTestDouble(&datasourceV2StoreTestState{
		listDocuments: func(context.Context, datasourcev2store.ListDocumentsParams) ([]datasourcev2store.Document, error) {
			return documents, nil
		},
		listChunksPage: func(context.Context, datasourcev2store.ListChunksParams) (datasourcev2store.TextChunkPage, error) {
			return datasourcev2store.TextChunkPage{Chunks: chunks}, nil
		},
		searchChunks: func(context.Context, datasourcev2store.SearchChunksParams) ([]datasourcev2store.SemanticChunk, error) {
			return semantic, nil
		},
	})
	port := requireDatasourceV2StorePort(t, store)
	docs, err := port.ListDocuments(context.Background(), datasourcev2.ListDocumentsParams{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	page, err := port.ListChunksPage(context.Background(), datasourcev2.ListChunksParams{})
	if err != nil {
		t.Fatalf("ListChunksPage: %v", err)
	}
	found, err := port.SearchChunks(context.Background(), datasourcev2.SearchChunksParams{})
	if err != nil {
		t.Fatalf("SearchChunks: %v", err)
	}
	docs[0].ID, page.Chunks[0].ID, found[0].ID = 9, 9, 9
	page.Chunks[0].Embedding[0], found[0].Embedding[0] = 9, 9
	if documents[0].ID != 1 || chunks[0].ID != 2 || chunks[0].Embedding[0] != 2 ||
		semantic[0].ID != 3 || semantic[0].Embedding[0] != 3 {
		t.Fatalf("Store collections mutated: docs=%#v chunks=%#v semantic=%#v", documents, chunks, semantic)
	}
}

// TestDatasourceV2StoreAdapterRejectsNilDocuments 固定四条指针返回路径在 Store 返回 nil 时显式失败。
func TestDatasourceV2StoreAdapterRejectsNilDocuments(t *testing.T) {
	state := &datasourceV2StoreTestState{
		getDocument: func(context.Context, int64) (*datasourcev2store.Document, error) { return nil, nil },
		upsertImporting: func(context.Context, datasourcev2store.UpsertDocumentParams) (*datasourcev2store.Document, error) {
			return nil, nil
		},
		updateDocument: func(context.Context, datasourcev2store.UpdateDocumentParams) (*datasourcev2store.Document, error) {
			return nil, nil
		},
		markReady: func(context.Context, datasourcev2store.MarkReadyParams) (*datasourcev2store.Document, error) {
			return nil, nil
		},
	}
	port := requireDatasourceV2StorePort(t, newDatasourceV2StoreTestDouble(state))
	tests := map[string]func() error{
		"get": func() error { _, err := port.GetDocument(context.Background(), 1); return err },
		"upsert": func() error {
			_, err := port.UpsertImporting(context.Background(), datasourcev2.UpsertDocumentParams{})
			return err
		},
		"update": func() error {
			_, err := port.UpdateDocument(context.Background(), datasourcev2.UpdateDocumentParams{})
			return err
		},
		"mark_ready": func() error {
			_, err := port.MarkReady(context.Background(), datasourcev2.MarkReadyParams{})
			return err
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, errDatasourceV2StoreReturnedNilDocument) {
				t.Fatalf("error = %v, want nil document error", err)
			}
		})
	}
}

// TestDatasourceV2StoreAdapterPreservesErrors 固定每个 Store 方法的普通错误对象和 errors.Is 身份。
func TestDatasourceV2StoreAdapterPreservesErrors(t *testing.T) {
	wantErr := errors.New("datasource v2 Store failed")
	port := requireDatasourceV2StorePort(t, datasourceV2FailingStore(wantErr))
	tests := datasourceV2FailingOperations(port)
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			gotErr := run()
			if gotErr != wantErr || !errors.Is(gotErr, wantErr) {
				t.Fatalf("error = %v, want identical %v", gotErr, wantErr)
			}
		})
	}
}

func datasourceV2FailingStore(wantErr error) datasourcev2store.Store {
	return newDatasourceV2StoreTestDouble(&datasourceV2StoreTestState{
		withTx: func(context.Context, func(datasourcev2store.Store) error) error { return wantErr },
		listDocuments: func(context.Context, datasourcev2store.ListDocumentsParams) ([]datasourcev2store.Document, error) {
			return nil, wantErr
		},
		getDocument: func(context.Context, int64) (*datasourcev2store.Document, error) { return nil, wantErr },
		listChunksPage: func(context.Context, datasourcev2store.ListChunksParams) (datasourcev2store.TextChunkPage, error) {
			return datasourcev2store.TextChunkPage{}, wantErr
		},
		searchChunks: func(context.Context, datasourcev2store.SearchChunksParams) ([]datasourcev2store.SemanticChunk, error) {
			return nil, wantErr
		},
		upsertImporting: func(context.Context, datasourcev2store.UpsertDocumentParams) (*datasourcev2store.Document, error) {
			return nil, wantErr
		},
		updateDocument: func(context.Context, datasourcev2store.UpdateDocumentParams) (*datasourcev2store.Document, error) {
			return nil, wantErr
		},
		deleteDocument: func(context.Context, int64) error { return wantErr },
		deleteChunks:   func(context.Context, int64) error { return wantErr },
		insertChunk: func(context.Context, datasourcev2store.InsertChunkParams) error {
			return wantErr
		},
		markReady: func(context.Context, datasourcev2store.MarkReadyParams) (*datasourcev2store.Document, error) {
			return nil, wantErr
		},
	})
}

func datasourceV2FailingOperations(store datasourcev2.Store) map[string]func() error {
	ctx := context.Background()
	return map[string]func() error{
		"with_tx":         func() error { return store.WithTx(ctx, func(datasourcev2.Store) error { return nil }) },
		"list_documents":  func() error { _, err := store.ListDocuments(ctx, datasourcev2.ListDocumentsParams{}); return err },
		"get_document":    func() error { _, err := store.GetDocument(ctx, 1); return err },
		"list_chunks":     func() error { _, err := store.ListChunksPage(ctx, datasourcev2.ListChunksParams{}); return err },
		"search_chunks":   func() error { _, err := store.SearchChunks(ctx, datasourcev2.SearchChunksParams{}); return err },
		"upsert":          func() error { _, err := store.UpsertImporting(ctx, datasourcev2.UpsertDocumentParams{}); return err },
		"update":          func() error { _, err := store.UpdateDocument(ctx, datasourcev2.UpdateDocumentParams{}); return err },
		"delete_document": func() error { return store.DeleteDocument(ctx, 1) },
		"delete_chunks":   func() error { return store.DeleteChunks(ctx, 1) },
		"insert_chunk":    func() error { return store.InsertChunk(ctx, datasourcev2.InsertChunkParams{}) },
		"mark_ready":      func() error { _, err := store.MarkReady(ctx, datasourcev2.MarkReadyParams{}); return err },
	}
}
