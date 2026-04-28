package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type stubRegistryMemoryService struct {
	read func(context.Context, contract.MemoryReadRequest) (contract.MemoryReadResult, error)
}

func (s stubRegistryMemoryService) Read(ctx context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	if s.read == nil {
		return contract.MemoryReadResult{}, nil
	}
	return s.read(ctx, req)
}

func TestNewRegistryIncludesMemoryTool(t *testing.T) {
	var gotReq contract.MemoryReadRequest
	registry := newRegistry(nil, nil, nil, nil, nil, stubRegistryMemoryService{read: func(_ context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
		gotReq = req
		return contract.MemoryReadResult{IndexHit: true}, nil
	}}, nil)

	tool, ok := registry.Lookup("memory_read")
	if !ok {
		t.Fatal("memory_read tool is not registered")
	}
	raw, err := json.Marshal(map[string]any{"name": "alpha", "scope": "user"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	value, err := tool.Handler(context.Background(), raw)
	if err != nil {
		t.Fatalf("tool.Handler() error = %v", err)
	}
	result, ok := value.(contract.MemoryReadResult)
	if !ok {
		t.Fatalf("tool.Handler() type = %T, want contract.MemoryReadResult", value)
	}
	if gotReq.Scope != contract.MemoryScopeUser || gotReq.Name != "alpha" {
		t.Fatalf("request = %#v, want parsed memory read request", gotReq)
	}
	if !result.IndexHit {
		t.Fatalf("result = %#v, want IndexHit=true", result)
	}
}

func TestBuildBootstrapConfigAdvertisesMemoryCapability(t *testing.T) {
	cfg := buildBootstrapConfig(nil, nil, newRegistry(nil, nil, nil, nil, nil, stubRegistryMemoryService{}, nil))
	if !slices.Contains(cfg.Capabilities, "tools/memory") {
		t.Fatalf("Capabilities = %#v, want tools/memory", cfg.Capabilities)
	}
}
