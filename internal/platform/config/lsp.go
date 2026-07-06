package config

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
)

// DefaultLSPConfig 返回内置 LSP 配置。
// 默认值会过滤大型或生成目录，并按语言声明 root marker，避免初始索引扫进无关工作区。
func DefaultLSPConfig() contract.LSPConfig {
	return contract.LSPConfig{
		NoiseDirNames: []string{
			".agent", ".agents", ".claude", ".git", ".workspace", ".worktrees",
			"coverage", "dist", "docs", "node_modules", "vendor",
		},
		GoDirectoryFilters: []string{
			"-.agent",
			"-.agents",
			"-.claude",
			"-.git",
			"-.workspace",
			"-.worktrees",
			"-coverage",
			"-dist",
			"-docs",
			"-node_modules",
			"-vendor",
			"-**/.agent",
			"-**/.agents",
			"-**/.claude",
			"-**/.git",
			"-**/.workspace",
			"-**/.worktrees",
			"-**/coverage",
			"-**/dist",
			"-**/docs",
			"-**/node_modules",
			"-**/vendor",
		},
		ProjectAdapters: map[string]contract.LSPProjectAdapterConfig{
			contract.LSPServiceJSTS: {
				RootMarkers:           []string{"tsconfig.json", "jsconfig.json", "package.json"},
				IgnoredDirNames:       []string{".build-cache", ".git", ".workspace", "dist", "node_modules", "vendor"},
				FirstSourceExtensions: []string{".js", ".jsx", ".ts", ".tsx"},
			},
			contract.LSPServicePython: {
				RootMarkers: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile"},
				IgnoredDirNames: []string{
					".build-cache", ".git", ".mypy_cache", ".pytest_cache", ".ruff_cache",
					".venv", ".workspace", "__pycache__", "node_modules", "vendor",
				},
				FirstSourceExtensions: []string{".py", ".pyi"},
			},
			contract.LSPServiceRust: {
				RootMarkers:           []string{"Cargo.toml"},
				IgnoredDirNames:       []string{".build-cache", ".git", ".workspace", "node_modules", "target", "vendor"},
				FirstSourceExtensions: []string{".rs"},
			},
			contract.LSPServiceJava: {
				RootMarkers:           []string{"pom.xml", "build.gradle", "build.gradle.kts"},
				IgnoredDirNames:       []string{".build-cache", ".git", ".gradle", ".idea", ".workspace", "build", "node_modules", "target", "vendor"},
				FirstSourceExtensions: []string{".java"},
			},
			contract.LSPServiceCSS: {
				RootMarkers:           []string{"package.json"},
				IgnoredDirNames:       []string{".build-cache", ".git", ".workspace", "dist", "node_modules", "vendor"},
				FirstSourceExtensions: []string{".css"},
			},
			contract.LSPServiceShell: {
				RootMarkers:           []string{".git", "package.json", "go.mod", "Makefile"},
				IgnoredDirNames:       []string{".build-cache", ".git", ".workspace", "dist", "node_modules", "vendor"},
				FirstSourceExtensions: []string{".sh", ".bash", ".zsh", ".ksh", ".bats"},
			},
			contract.LSPServiceSQL: {
				RootMarkers:           []string{".sqllsrc.json", "sqlc.yaml", "sqlc.yml", "go.mod", "package.json"},
				IgnoredDirNames:       []string{".build-cache", ".git", ".workspace", "dist", "node_modules", "vendor"},
				FirstSourceExtensions: []string{".sql"},
			},
		},
		DocumentFallbackLanguageIDs:      []string{"markdown", "json", "yaml"},
		DisableInitialWorkspaceBootstrap: true,
	}
}

// lspConfigFromEnv 从环境变量覆盖默认 LSP 配置。
// 每个显式覆盖都必须能解析出有效值，否则启动失败，避免 LSP 扫描边界被坏配置放宽。
func lspConfigFromEnv() (contract.LSPConfig, error) {
	cfg := cloneLSPConfig(DefaultLSPConfig())
	var err error
	if cfg.NoiseDirNames, err = envStringSliceOr("LSP_NOISE_DIRS", cfg.NoiseDirNames); err != nil {
		return contract.LSPConfig{}, err
	}
	if cfg.GoDirectoryFilters, err = envStringSliceOr("LSP_GO_DIRECTORY_FILTERS", cfg.GoDirectoryFilters); err != nil {
		return contract.LSPConfig{}, err
	}
	if cfg.DocumentFallbackLanguageIDs, err = envStringSliceOr("LSP_DOCUMENT_FALLBACK_LANGUAGES", cfg.DocumentFallbackLanguageIDs); err != nil {
		return contract.LSPConfig{}, err
	}
	if cfg.DisableInitialWorkspaceBootstrap, err = envBoolOr("LSP_DISABLE_INITIAL_WORKSPACE_BOOTSTRAP", cfg.DisableInitialWorkspaceBootstrap); err != nil {
		return contract.LSPConfig{}, err
	}
	for _, adapter := range []struct {
		service string
		prefix  string
	}{
		{service: contract.LSPServiceJSTS, prefix: "LSP_JSTS"},
		{service: contract.LSPServicePython, prefix: "LSP_PYTHON"},
		{service: contract.LSPServiceRust, prefix: "LSP_RUST"},
		{service: contract.LSPServiceJava, prefix: "LSP_JAVA"},
		{service: contract.LSPServiceCSS, prefix: "LSP_CSS"},
		{service: contract.LSPServiceShell, prefix: "LSP_SHELL"},
		{service: contract.LSPServiceSQL, prefix: "LSP_SQL"},
	} {
		if err := applyProjectAdapterEnv(cfg.ProjectAdapters, adapter.service, adapter.prefix); err != nil {
			return contract.LSPConfig{}, err
		}
	}
	return cfg, nil
}

func applyProjectAdapterEnv(adapters map[string]contract.LSPProjectAdapterConfig, service, prefix string) error {
	cfg := adapters[service]
	var err error
	if cfg.RootMarkers, err = envStringSliceOr(prefix+"_ROOT_MARKERS", cfg.RootMarkers); err != nil {
		return err
	}
	if cfg.IgnoredDirNames, err = envStringSliceOr(prefix+"_IGNORED_DIRS", cfg.IgnoredDirNames); err != nil {
		return err
	}
	if cfg.FirstSourceExtensions, err = envStringSliceOr(prefix+"_FIRST_SOURCE_EXTENSIONS", cfg.FirstSourceExtensions); err != nil {
		return err
	}
	adapters[service] = cfg
	return nil
}

// envStringSliceOr 解析逗号分隔配置；显式非空但没有有效条目时直接报错。
func envStringSliceOr(key string, fallback []string) ([]string, error) {
	raw, ok := os.LookupEnv(key)
	value := strings.TrimSpace(raw)
	if !ok || value == "" {
		return slices.Clone(fallback), nil
	}
	values := configutil.SplitConfigStringSlice(value)
	if len(values) == 0 {
		return nil, fmt.Errorf("%s must contain at least one non-empty value", key)
	}
	return values, nil
}

func cloneLSPConfig(cfg contract.LSPConfig) contract.LSPConfig {
	return contract.LSPConfig{
		NoiseDirNames:                    slices.Clone(cfg.NoiseDirNames),
		GoDirectoryFilters:               slices.Clone(cfg.GoDirectoryFilters),
		ProjectAdapters:                  cloneLSPProjectAdapters(cfg.ProjectAdapters),
		DocumentFallbackLanguageIDs:      slices.Clone(cfg.DocumentFallbackLanguageIDs),
		DisableInitialWorkspaceBootstrap: cfg.DisableInitialWorkspaceBootstrap,
	}
}

func cloneLSPProjectAdapters(input map[string]contract.LSPProjectAdapterConfig) map[string]contract.LSPProjectAdapterConfig {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]contract.LSPProjectAdapterConfig, len(input))
	for key, cfg := range input {
		output[key] = contract.LSPProjectAdapterConfig{
			RootMarkers:           slices.Clone(cfg.RootMarkers),
			IgnoredDirNames:       slices.Clone(cfg.IgnoredDirNames),
			FirstSourceExtensions: slices.Clone(cfg.FirstSourceExtensions),
		}
	}
	return output
}
