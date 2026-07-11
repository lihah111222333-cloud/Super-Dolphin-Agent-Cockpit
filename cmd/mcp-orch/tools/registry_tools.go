package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools/modelregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilepath"
)

// Registry/discovery 工具集中暴露模型列表和 sharedfile 列表。
// prompt_list 与 command_list 在各自文件中注册，本文件只负责跨资源发现入口。

const (
	defaultSharedFileListLimit = int32(50)
	maxSharedFileListLimit     = int32(200)
)

// ----- list_models -----

// ListModelsInput 是 list_models 工具的可选过滤器。
type ListModelsInput struct {
	Provider string `json:"provider,omitempty"` // 可选：claude | codex；空表示全部
}

// ProviderModels 复用模型注册表 DTO，保持 list_models 的 wire shape 与注册表单源一致。
type ProviderModels = modelregistry.ProviderModels

// ListModelsResult 同时保留 providers 与通用 data 字段，兼容旧调用方和列表 envelope。
type ListModelsResult struct {
	Providers []ProviderModels `json:"providers"`
	Data      []ProviderModels `json:"data"`
	Total     int              `json:"total"`
	Showing   int              `json:"showing"`
	Truncated bool             `json:"truncated"`
	Hint      string           `json:"hint,omitempty"`
}

// ListModelsOption 调整 list_models handler 的依赖注入，测试可替换模型注册表。
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

// HandleListModels 返回当前打包版本可发现的 provider→models 列表；未知 provider 直接报错。
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

// newListModelsResult 构造 list_models envelope，并标记当前打包版本是否支持对应 provider。
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

// markModelProviderAvailability 给 provider 快照补充可用性字段，不修改注册表原始数据。
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

// modelProviderAvailability 描述当前 mcp-orch 打包版本是否能直接启动该 provider。
func modelProviderAvailability(provider string) (bool, string) {
	provider = strings.TrimSpace(provider)
	if provider == "codex" {
		return true, ""
	}
	return false, fmt.Sprintf("model provider %q is not supported in this packaged build", provider)
}

// boolPtr 返回 bool 指针，供 JSON omitempty 区分“明确不可用”和“旧注册表未声明”。
func boolPtr(value bool) *bool {
	return &value
}

// listModelsRegistry 应用测试/生产注入的注册表选项；未配置时由 handler fail-fast。
func listModelsRegistry(opts []ListModelsOption) modelregistry.Registry {
	cfg := listModelsConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg.registry
}

// ----- shared_file_list -----

// SharedFileListInput 是 shared_file_list 工具的过滤器。
type SharedFileListInput struct {
	Prefix string `json:"prefix,omitempty"` // 可选前缀过滤
	Limit  int32  `json:"limit,omitempty"`  // 可选上限
}

// SharedFileEntry 是 shared_file_list 的轻量 wire 记录，不含 content，避免列表接口泄漏大块正文。
type SharedFileEntry struct {
	Path      string `json:"path"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

// SharedFileListResult 同时暴露 files 与 data 字段，并返回允许写入前缀供模型继续操作。
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

// HandleSharedFileList 列出已存在 sharedfile；内容读取必须走 shared_file_read，列表只给路径元数据。
func HandleSharedFileList(store sharedfilestore.Store) ToolHandler {
	return makeHandler(store, "shared file store", func(ctx context.Context, in SharedFileListInput) (SharedFileListResult, error) {
		limit, err := normalizeSharedFileListLimit(in.Limit)
		if err != nil {
			return SharedFileListResult{}, err
		}
		rows, err := store.List(ctx, sharedfilestore.ListFilter{
			Prefix: strings.TrimSpace(in.Prefix),
			Limit:  limit,
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
		return newSharedFileListResult(entries, int(limit)), nil
	})
}

func normalizeSharedFileListLimit(limit int32) (int32, error) {
	switch {
	case limit < 0:
		return 0, fmt.Errorf("limit must be non-negative")
	case limit == 0:
		return defaultSharedFileListLimit, nil
	case limit > maxSharedFileListLimit:
		return maxSharedFileListLimit, nil
	default:
		return limit, nil
	}
}

// newSharedFileListResult 构造 shared_file_list envelope，并附带允许写入的路径前缀。
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

// registryToolDefinitions 注册跨资源发现工具，避免各资源工具文件互相依赖。
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
