package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type stubMemoryService struct {
	read func(context.Context, contract.MemoryReadRequest) (contract.MemoryReadResult, error)
}

func (s stubMemoryService) Read(ctx context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	if s.read == nil {
		return contract.MemoryReadResult{}, nil
	}
	return s.read(ctx, req)
}

func TestMemoryReadToolReturnsContent(t *testing.T) {
	updatedAt := time.Unix(1700000000, 0).UTC()
	want := contract.MemoryReadResult{
		Entry: &contract.MemoryEntry{
			Name:       "Alpha",
			Type:       contract.MemoryTypeReference,
			Content:    "hello",
			UpdatedAt:  updatedAt,
			SourcePath: "reference/alpha.md",
		},
		SourcePath: "reference/alpha.md",
		IndexHit:   true,
	}
	var gotReq contract.MemoryReadRequest
	handler := HandleMemoryRead(stubMemoryService{read: func(_ context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
		gotReq = req
		return want, nil
	}})

	value, err := handler(context.Background(), mustRawInput(t, memoryReadInput{
		Name:  "Alpha",
		Path:  "reference/alpha.md",
		Scope: "user",
		Type:  "reference",
	}))
	if err != nil {
		t.Fatalf("HandleMemoryRead() error = %v", err)
	}
	result, ok := value.(contract.MemoryReadResult)
	if !ok {
		t.Fatalf("HandleMemoryRead() type = %T, want contract.MemoryReadResult", value)
	}
	if gotReq.Name != "Alpha" || gotReq.Path != "reference/alpha.md" {
		t.Fatalf("request = %#v, want name/path preserved", gotReq)
	}
	if gotReq.Scope != contract.MemoryScopeUser {
		t.Fatalf("Scope = %q, want %q", gotReq.Scope, contract.MemoryScopeUser)
	}
	if gotReq.Type != contract.MemoryTypeReference {
		t.Fatalf("Type = %q, want %q", gotReq.Type, contract.MemoryTypeReference)
	}
	if result.Entry == nil || result.Entry.Content != "hello" {
		t.Fatalf("result = %#v, want populated entry", result)
	}
	if result.SourcePath != want.SourcePath || !result.IndexHit {
		t.Fatalf("result = %#v, want source path %q and index hit", result, want.SourcePath)
	}
}

func TestHandleMemoryReadNilGuard(t *testing.T) {
	handler := HandleMemoryRead(nil)
	_, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil || err.Error() != "memory service is not configured" {
		t.Fatalf("HandleMemoryRead() error = %v", err)
	}
}

func TestMemoryToolDefinitionsExposeOnlyRead(t *testing.T) {
	registry := NewRegistry(Dependencies{Memory: stubMemoryService{}})
	if _, ok := registry.Lookup("memory_read"); !ok {
		t.Fatal("memory_read tool is missing")
	}
	if _, ok := registry.Lookup("memory_write"); ok {
		t.Fatal("memory_write tool must not be exposed in this branch")
	}
}
