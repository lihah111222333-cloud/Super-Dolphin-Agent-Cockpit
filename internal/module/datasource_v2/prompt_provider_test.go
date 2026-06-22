package datasourcev2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
)

func TestPromptAssemblyIncludesDatasourceV2SemanticChunksForCurrentRequest(t *testing.T) {
	t.Parallel()

	store := &promptDatasourceV2Store{
		semanticChunks: []datasourcev2store.SemanticChunk{
			{
				TextChunk: datasourcev2store.TextChunk{
					ID:             501,
					DocumentID:     101,
					ChunkIndex:     7,
					Content:        "semantic launch answer",
					CharCount:      22,
					ByteCount:      22,
					EmbeddingModel: "local-token-hash-v1",
					EmbeddingDim:   384,
					TokenCount:     3,
				},
				SourcePath: "/tmp/launch-notes.txt",
				FileName:   "launch-notes.txt",
				Distance:   0.01,
			},
			{
				TextChunk: datasourcev2store.TextChunk{
					ID:             502,
					DocumentID:     202,
					ChunkIndex:     2,
					Content:        "secondary rollout detail",
					CharCount:      23,
					ByteCount:      23,
					EmbeddingModel: "local-token-hash-v1",
					EmbeddingDim:   384,
					TokenCount:     3,
				},
				SourcePath: "/tmp/rollout.txt",
				FileName:   "rollout.txt",
				Distance:   0.02,
			},
		},
	}

	var assembly contract.PromptAssemblyService
	app := fxtest.New(t,
		fx.Supply(&contract.Config{}),
		fx.Supply(fx.Annotate(store, fx.As(new(datasourcev2store.Store)))),
		prompt.Module,
		datasourcev2.Module,
		fx.Populate(&assembly),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	start, err := assembly.AssembleStart(context.Background(), contract.StartInput{
		CWD:    t.TempDir(),
		Prompt: "When is the semantic launch?",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	content := promptSectionContent(start.ResolvedSections, contract.DynamicSectionDatasource)
	for _, want := range []string{
		"## " + contract.DynamicSectionDatasource,
		"### 1. launch-notes.txt [chunk 7]",
		"semantic launch answer",
		"### 2. rollout.txt [chunk 2]",
		"secondary rollout detail",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("datasource section missing %q:\n%s", want, content)
		}
		if !strings.Contains(start.BaseInstructions, want) {
			t.Fatalf("BaseInstructions missing %q:\n%s", want, start.BaseInstructions)
		}
	}
	if store.capturedSearch.Limit != 10 {
		t.Fatalf("semantic datasource search limit = %d, want 10", store.capturedSearch.Limit)
	}
	if store.capturedSearch.EmbeddingModel != "local-token-hash-v1" ||
		store.capturedSearch.EmbeddingDim != 384 ||
		len(store.capturedSearch.Embedding) != 384*4 {
		t.Fatalf("semantic datasource search vector params = %+v", store.capturedSearch)
	}
}

type promptDatasourceV2Store struct {
	semanticChunks []datasourcev2store.SemanticChunk
	capturedSearch datasourcev2store.SearchChunksParams
}

func (s *promptDatasourceV2Store) WithTx(context.Context, func(datasourcev2store.Store) error) error {
	return errors.New("unexpected datasource_v2 prompt test write transaction")
}

func (s *promptDatasourceV2Store) ListDocuments(
	context.Context,
	datasourcev2store.ListDocumentsParams,
) ([]datasourcev2store.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test list documents")
}

func (s *promptDatasourceV2Store) GetDocument(context.Context, int64) (*datasourcev2store.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test get document")
}

func (s *promptDatasourceV2Store) ListChunks(context.Context, int64) ([]datasourcev2store.TextChunk, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test list chunks")
}

func (s *promptDatasourceV2Store) SearchChunks(
	_ context.Context,
	params datasourcev2store.SearchChunksParams,
) ([]datasourcev2store.SemanticChunk, error) {
	s.capturedSearch = params
	return append([]datasourcev2store.SemanticChunk(nil), s.semanticChunks...), nil
}

func (s *promptDatasourceV2Store) UpsertImporting(
	context.Context,
	datasourcev2store.UpsertDocumentParams,
) (*datasourcev2store.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test import")
}

func (s *promptDatasourceV2Store) UpdateDocument(
	context.Context,
	datasourcev2store.UpdateDocumentParams,
) (*datasourcev2store.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test update")
}

func (s *promptDatasourceV2Store) DeleteDocument(context.Context, int64) error {
	return errors.New("unexpected datasource_v2 prompt test delete")
}

func (s *promptDatasourceV2Store) DeleteChunks(context.Context, int64) error {
	return errors.New("unexpected datasource_v2 prompt test delete chunks")
}

func (s *promptDatasourceV2Store) InsertChunk(context.Context, datasourcev2store.InsertChunkParams) error {
	return errors.New("unexpected datasource_v2 prompt test insert chunk")
}

func (s *promptDatasourceV2Store) MarkReady(
	context.Context,
	datasourcev2store.MarkReadyParams,
) (*datasourcev2store.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test mark ready")
}

func promptSectionContent(sections []contract.ResolvedPromptSection, name string) string {
	for _, section := range sections {
		if section.Name == name {
			return section.Content
		}
	}
	return ""
}
