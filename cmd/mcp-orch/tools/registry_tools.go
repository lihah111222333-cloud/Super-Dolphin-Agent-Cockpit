package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools/modelregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

// T4.1 + T4.4 Registry / discovery 工具：
//   - list_models: 暴露 super-dolphin 支持的 provider→models
//   - shared_file_list: 列已存在的 sharedfile + 暴露允许写入的路径前缀，
//     防止 AI / UI 撞 “path prefix not in whitelist” 错误谌不透明
//     （骨架阶段吃狗粮 B-10 / PE-1）
//
// T4.2 prompt_list / T4.3 command_list 已在 prompt_tools.go / command_tools.go
// 通过 resourceToolDefinitions(...) 暴露，本文件不重复。

// =====================================================
// list_models (T4.1)
// =====================================================

// ListModelsInput 是 list_models 工具的可选过滤器。
type ListModelsInput struct {
	Provider string `json:"provider,omitempty"` // 可选：claude | codex；空表示全部
}

// ProviderModels 列出某 provider 支持的 model 名称集。
type ProviderModels = modelregistry.ProviderModels

// ListModelsResult 是 list_models 的返回结构。
type ListModelsResult struct {
	Providers []ProviderModels `json:"providers"`
	Data      []ProviderModels `json:"data"`
	Total     int              `json:"total"`
	Showing   int              `json:"showing"`
	Truncated bool             `json:"truncated"`
	Hint      string           `json:"hint,omitempty"`
}

type ListModelsOption func(*listModelsConfig)

type listModelsConfig struct {
	registry modelregistry.Registry
}

// WithModelRegistry 设置模型注册表。
func WithModelRegistry(registry modelregistry.Registry) ListModelsOption {
	return func(cfg *listModelsConfig) {
		cfg.registry = registry
	}
}

// HandleListModels 返回 super-dolphin 支持的 provider→models 列表。
func HandleListModels(opts ...ListModelsOption) ToolHandler {
	registry := listModelsRegistry(opts)
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		if registry == nil {
			return nil, fmt.Errorf("model registry is not configured")
		}
		var input ListModelsInput
		if err := shared.DecodeInput(raw, &input); err != nil {
			return nil, err
		}
		if input.Provider == "" {
			providers, err := registry.ListProviders()
			if err != nil {
				return nil, err
			}
			return newListModelsResult(providers), nil
		}
		provider, ok, err := registry.LookupProvider(input.Provider)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("model provider %q not found", input.Provider)
		}
		return newListModelsResult([]ProviderModels{provider}), nil
	}
}

func newListModelsResult(providers []ProviderModels) ListModelsResult {
	providers = markModelProviderAvailability(providers)
	env := newListEnvelope(providers, 0, "next: use provider/model values in launch_agent or DAG node config")
	return ListModelsResult{
		Providers: providers,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

func markModelProviderAvailability(providers []ProviderModels) []ProviderModels {
	out := make([]ProviderModels, len(providers))
	for i, provider := range providers {
		out[i] = provider
		available, reason := modelProviderAvailability(provider.Provider)
		out[i].Available = boolPtr(available)
		out[i].UnavailableReason = reason
	}
	return out
}

func modelProviderAvailability(provider string) (bool, string) {
	provider = strings.TrimSpace(provider)
	if provider == "codex" {
		return true, ""
	}
	return false, fmt.Sprintf("model provider %q is not supported in this packaged build", provider)
}

func boolPtr(value bool) *bool {
	return &value
}

func listModelsRegistry(opts []ListModelsOption) modelregistry.Registry {
	cfg := listModelsConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.registry
}

// =====================================================
// shared_file_list (T4.4)
// =====================================================

// SharedFileListInput 是 shared_file_list 工具的过滤器。
type SharedFileListInput struct {
	Prefix string `json:"prefix,omitempty"` // 可选前缀过滤
	Limit  int32  `json:"limit,omitempty"`  // 可选上限
}

// SharedFileEntry 是 shared_file_list 返回的单条记录（不含 content，避免泄漏大块数据）。
type SharedFileEntry struct {
	Path      string `json:"path"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

// SharedFileListResult 是 shared_file_list 的返回结构。
type SharedFileListResult struct {
	Files             []SharedFileEntry `json:"files"`
	Data              []SharedFileEntry `json:"data"`
	Total             int               `json:"total"`
	Showing           int               `json:"showing"`
	Truncated         bool              `json:"truncated"`
	Hint              string            `json:"hint,omitempty"`
	AllowedPrefixes   []string          `json:"allowed_prefixes"`
	AllowedPrefixHint string            `json:"allowed_prefix_hint"`
}

// HandleSharedFileList 列已存在的 sharedfile，并暴露允许写入的路径前缀。
func HandleSharedFileList(store sharedfilestore.Store) ToolHandler {
	return makeHandler(store, "shared file store", func(ctx context.Context, in SharedFileListInput) (SharedFileListResult, error) {
		rows, err := store.List(ctx, sharedfilestore.ListFilter{
			Prefix: in.Prefix,
			Limit:  in.Limit,
		})
		if err != nil {
			return SharedFileListResult{}, err
		}
		entries := make([]SharedFileEntry, 0, len(rows))
		for _, r := range rows {
			entries = append(entries, SharedFileEntry{
				Path:      r.Path,
				UpdatedBy: r.UpdatedBy,
				UpdatedAt: r.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			})
		}
		return newSharedFileListResult(entries, int(in.Limit)), nil
	})
}

func newSharedFileListResult(entries []SharedFileEntry, limit int) SharedFileListResult {
	env := newListEnvelope(entries, limit, "next: use shared_file_read pos=shared:<path> to read content")
	return SharedFileListResult{
		Files:             entries,
		Data:              env.Data,
		Total:             env.Total,
		Showing:           env.Showing,
		Truncated:         env.Truncated,
		Hint:              env.Hint,
		AllowedPrefixes:   sharedfilepath.WritePrefixes(),
		AllowedPrefixHint: "writes must start with one of allowed_prefixes",
	}
}

// registryToolDefinitions 是 T4.1 + T4.4 工具的注册聚合。
// T4.2 prompt_list / T4.3 command_list 已在各自包里通过 resourceToolDefinitions 注册，不重复。
func registryToolDefinitions(sharedFile sharedfilestore.Store, models modelregistry.Registry) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("list_models", "List super-dolphin supported provider→models. Optional 'provider' filter (claude | codex). AI 设计师可用此查 exec.model 字段允许的取值。", ObjectSchema(map[string]Schema{
			"provider": StringSchema("Optional provider filter ('claude' or 'codex'); omit for all."),
		}), HandleListModels(WithModelRegistry(models))),
		defineTool("shared_file_list", "List existing shared files + return allowed write prefixes. AI 设计 DAG 时用此查 outputs.to_sharedfile 允许的路径。", ObjectSchema(map[string]Schema{
			"prefix": StringSchema("Optional path prefix filter."),
			"limit":  IntegerSchema("Optional max rows."),
		}), HandleSharedFileList(sharedFile)),
	)
}
