package tools

import (
	"context"
	"encoding/json"
	"testing"

	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/prompt"
)

type stubPromptStore struct {
	promptstore.Store
	get                      func(context.Context, string) (*promptstore.PromptTemplate, error)
	list                     func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error)
	listSectionsByTemplateID func(context.Context, int64) ([]promptstore.PromptTemplateSection, error)
}

// Get 返回测试注入的单个 prompt 模板。
func (s stubPromptStore) Get(ctx context.Context, key string) (*promptstore.PromptTemplate, error) {
	if s.get == nil {
		return nil, nil
	}
	return s.get(ctx, key)
}

// List 返回测试注入的 prompt 列表。
func (s stubPromptStore) List(
	ctx context.Context,
	filter promptstore.ListFilter,
) ([]promptstore.PromptTemplate, error) {
	if s.list == nil {
		return nil, nil
	}
	return s.list(ctx, filter)
}

// ListSectionsByTemplateID 返回测试注入的 prompt section 列表。
func (s stubPromptStore) ListSectionsByTemplateID(
	ctx context.Context,
	templateID int64,
) ([]promptstore.PromptTemplateSection, error) {
	if s.listSectionsByTemplateID == nil {
		return nil, nil
	}
	return s.listSectionsByTemplateID(ctx, templateID)
}

func mustRawInput(t *testing.T, input any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func promptToolTestContext() context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{CWD: "/repo/a"})
}
