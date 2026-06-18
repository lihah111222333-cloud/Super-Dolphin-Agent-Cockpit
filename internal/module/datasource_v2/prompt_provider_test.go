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

func TestPromptAssemblyIncludesDatasourceV2ReadyDocumentChunks(t *testing.T) {
	t.Parallel()

	store := &promptDatasourceV2Store{
		document: datasourcev2store.Document{
			ID:          101,
			SourcePath:  "/tmp/launch-notes.txt",
			FileName:    "launch-notes.txt",
			Extension:   ".txt",
			Status:      datasourcev2store.StatusReady,
			ChunkCount:  2,
			TotalChars:  44,
			ContentHash: "sha256:abc",
		},
		chunks: []datasourcev2store.TextChunk{
			{
				ID:         501,
				DocumentID: 101,
				ChunkIndex: 0,
				Content:    "first datasource line\n",
				CharCount:  22,
				ByteCount:  22,
			},
			{
				ID:         502,
				DocumentID: 101,
				ChunkIndex: 1,
				Content:    "second datasource line",
				CharCount:  22,
				ByteCount:  22,
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

	start, err := assembly.AssembleStart(context.Background(), contract.StartInput{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	content := promptSectionContent(start.ResolvedSections, contract.DynamicSectionDatasource)
	for _, want := range []string{
		"## " + contract.DynamicSectionDatasource,
		"### launch-notes.txt",
		"first datasource line\nsecond datasource line",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("datasource section missing %q:\n%s", want, content)
		}
		if !strings.Contains(start.BaseInstructions, want) {
			t.Fatalf("BaseInstructions missing %q:\n%s", want, start.BaseInstructions)
		}
	}
}

type promptDatasourceV2Store struct {
	document datasourcev2store.Document
	chunks   []datasourcev2store.TextChunk
}

func (s *promptDatasourceV2Store) WithTx(context.Context, func(datasourcev2store.Store) error) error {
	return errors.New("unexpected datasource_v2 prompt test write transaction")
}

func (s *promptDatasourceV2Store) ListDocuments(
	context.Context,
	datasourcev2store.ListDocumentsParams,
) ([]datasourcev2store.Document, error) {
	return []datasourcev2store.Document{s.document}, nil
}

func (s *promptDatasourceV2Store) GetDocument(context.Context, int64) (*datasourcev2store.Document, error) {
	doc := s.document
	return &doc, nil
}

func (s *promptDatasourceV2Store) ListChunks(context.Context, int64) ([]datasourcev2store.TextChunk, error) {
	return append([]datasourcev2store.TextChunk(nil), s.chunks...), nil
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
