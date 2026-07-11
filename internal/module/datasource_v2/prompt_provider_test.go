package datasourcev2_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

func TestPromptAssemblyIncludesDatasourceV2SemanticChunksForCurrentRequest(t *testing.T) {
	t.Parallel()

	store := &promptDatasourceV2Store{
		semanticChunks: []datasourcev2.SemanticChunk{
			{
				TextChunk: datasourcev2.TextChunk{
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
				TextChunk: datasourcev2.TextChunk{
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
		fx.Supply(fx.Annotate(store, fx.As(new(datasourcev2.Store)))),
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
	content := promptSectionContent(start.ResolvedSections, contract.DynamicSectionDatasourceV2)
	for _, want := range []string{
		"## " + contract.DynamicSectionDatasourceV2,
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

func TestPromptAssemblyDoesNotLeakDatasourceV2SourcePathWhenFileNameMissing(t *testing.T) {
	t.Parallel()

	secretPath := "/Users/alice/private/strategy.txt"
	chunk := promptSemanticChunkForTest(303, 1, "", "source path should not appear")
	chunk.SourcePath = secretPath

	start, _, err := assembleDatasourceV2PromptForTest(t, []datasourcev2.SemanticChunk{chunk})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	content := promptSectionContent(start.ResolvedSections, contract.DynamicSectionDatasourceV2)
	for _, body := range []string{content, start.BaseInstructions} {
		if strings.Contains(body, secretPath) {
			t.Fatalf("datasource_v2 prompt leaked source path %q:\n%s", secretPath, body)
		}
		if !strings.Contains(body, "### 1. document 303 [chunk 1]") {
			t.Fatalf("datasource_v2 prompt missing document fallback title:\n%s", body)
		}
	}
}

func TestPromptAssemblyTruncatesDatasourceV2ChunkAndReportsDiagnostic(t *testing.T) {
	t.Parallel()

	chunk := strings.Repeat("A", 64*1024) + "TAIL_SHOULD_NOT_APPEAR"
	start, _, err := assembleDatasourceV2PromptForTest(t, []datasourcev2.SemanticChunk{
		promptSemanticChunkForTest(1, 0, "large.txt", chunk),
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	content := promptSectionContent(start.ResolvedSections, contract.DynamicSectionDatasourceV2)
	if len(content) >= len(chunk) {
		t.Fatalf("datasource_v2 prompt length = %d, want capped below original chunk length %d", len(content), len(chunk))
	}
	if strings.Contains(content, "TAIL_SHOULD_NOT_APPEAR") {
		t.Fatalf("datasource_v2 prompt kept content past chunk byte cap")
	}
	if !strings.Contains(strings.ToLower(content), "truncated") {
		t.Fatalf("datasource_v2 prompt missing truncation diagnostic:\n%s", content)
	}
}

func TestPromptAssemblyTruncatesDatasourceV2TotalBudgetAndReportsDiagnostic(t *testing.T) {
	t.Parallel()

	chunks := make([]datasourcev2.SemanticChunk, 0, 10)
	for i := range 10 {
		chunks = append(chunks, promptSemanticChunkForTest(int64(i+1), int32(i), fmt.Sprintf("chunk-%02d.txt", i), strings.Repeat(fmt.Sprintf("%d", i), 12*1024)))
	}
	start, _, err := assembleDatasourceV2PromptForTest(t, chunks)
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	content := promptSectionContent(start.ResolvedSections, contract.DynamicSectionDatasourceV2)
	if len(content) >= 120*1024 {
		t.Fatalf("datasource_v2 prompt length = %d, want total byte budget below full chunk set", len(content))
	}
	if strings.Contains(content, "chunk-09.txt") {
		t.Fatalf("datasource_v2 prompt included final chunk despite total byte cap:\n%s", content)
	}
	if !strings.Contains(strings.ToLower(content), "truncated") {
		t.Fatalf("datasource_v2 prompt missing total truncation diagnostic:\n%s", content)
	}
}

func TestPromptAssemblyFailsWhenDatasourceV2PromptBudgetRemovesEveryChunk(t *testing.T) {
	t.Parallel()

	_, _, err := assembleDatasourceV2PromptForTest(t, []datasourcev2.SemanticChunk{
		promptSemanticChunkForTest(1, 0, "empty.txt", strings.Repeat(" ", 64)),
	})
	if err == nil {
		t.Fatal("AssembleStart() error = nil, want critical datasource_v2 prompt error")
	}
	if !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("AssembleStart() error = %T %[1]v, want critical prompt section error", err)
	}
}

type promptDatasourceV2Store struct {
	promptDatasourceV2UnusedStore

	semanticChunks []datasourcev2.SemanticChunk
	capturedSearch datasourcev2.SearchChunksParams
}

type promptDatasourceV2UnusedStore struct{}

func (promptDatasourceV2UnusedStore) WithTx(context.Context, func(datasourcev2.Store) error) error {
	return errors.New("unexpected datasource_v2 prompt test write transaction")
}

func (promptDatasourceV2UnusedStore) ListDocuments(
	context.Context,
	datasourcev2.ListDocumentsParams,
) ([]datasourcev2.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test list documents")
}

func (promptDatasourceV2UnusedStore) GetDocument(context.Context, int64) (*datasourcev2.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test get document")
}

func (promptDatasourceV2UnusedStore) ListChunksPage(
	context.Context,
	datasourcev2.ListChunksParams,
) (datasourcev2.TextChunkPage, error) {
	return datasourcev2.TextChunkPage{}, errors.New("unexpected datasource_v2 prompt test list chunks")
}

func (s *promptDatasourceV2Store) SearchChunks(
	_ context.Context,
	params datasourcev2.SearchChunksParams,
) ([]datasourcev2.SemanticChunk, error) {
	s.capturedSearch = params
	return append([]datasourcev2.SemanticChunk(nil), s.semanticChunks...), nil
}

func assembleDatasourceV2PromptForTest(
	t *testing.T,
	chunks []datasourcev2.SemanticChunk,
) (contract.StartAssembly, *promptDatasourceV2Store, error) {
	t.Helper()

	store := &promptDatasourceV2Store{semanticChunks: chunks}
	var assembly contract.PromptAssemblyService
	app := fxtest.New(t,
		fx.Supply(&contract.Config{}),
		fx.Supply(fx.Annotate(store, fx.As(new(datasourcev2.Store)))),
		prompt.Module,
		datasourcev2.Module,
		fx.Populate(&assembly),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	start, err := assembly.AssembleStart(context.Background(), contract.StartInput{
		CWD:    t.TempDir(),
		Prompt: "Find datasource evidence",
	})
	return start, store, err
}

func promptSemanticChunkForTest(documentID int64, chunkIndex int32, fileName, content string) datasourcev2.SemanticChunk {
	return datasourcev2.SemanticChunk{
		TextChunk: datasourcev2.TextChunk{
			ID:             documentID*100 + int64(chunkIndex),
			DocumentID:     documentID,
			ChunkIndex:     chunkIndex,
			Content:        content,
			CharCount:      int32(len([]rune(content))),
			ByteCount:      int32(len(content)),
			EmbeddingModel: "local-token-hash-v1",
			EmbeddingDim:   384,
			TokenCount:     3,
		},
		SourcePath: "/tmp/" + fileName,
		FileName:   fileName,
		Distance:   0.01,
	}
}

func (promptDatasourceV2UnusedStore) UpsertImporting(
	context.Context,
	datasourcev2.UpsertDocumentParams,
) (*datasourcev2.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test import")
}

func (promptDatasourceV2UnusedStore) UpdateDocument(
	context.Context,
	datasourcev2.UpdateDocumentParams,
) (*datasourcev2.Document, error) {
	return nil, errors.New("unexpected datasource_v2 prompt test update")
}

func (promptDatasourceV2UnusedStore) DeleteDocument(context.Context, int64) error {
	return errors.New("unexpected datasource_v2 prompt test delete")
}

func (promptDatasourceV2UnusedStore) DeleteChunks(context.Context, int64) error {
	return errors.New("unexpected datasource_v2 prompt test delete chunks")
}

func (promptDatasourceV2UnusedStore) InsertChunk(context.Context, datasourcev2.InsertChunkParams) error {
	return errors.New("unexpected datasource_v2 prompt test insert chunk")
}

func (promptDatasourceV2UnusedStore) MarkReady(
	context.Context,
	datasourcev2.MarkReadyParams,
) (*datasourcev2.Document, error) {
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
