package config

import (
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
		},
		DocumentFallbackLanguageIDs:      []string{"markdown", "json", "yaml"},
		DisableInitialWorkspaceBootstrap: true,
	}
}

func lspConfigFromEnv() contract.LSPConfig {
	cfg := cloneLSPConfig(DefaultLSPConfig())
	cfg.NoiseDirNames = envStringSliceOr("LSP_NOISE_DIRS", cfg.NoiseDirNames)
	cfg.GoDirectoryFilters = envStringSliceOr("LSP_GO_DIRECTORY_FILTERS", cfg.GoDirectoryFilters)
	cfg.DocumentFallbackLanguageIDs = envStringSliceOr("LSP_DOCUMENT_FALLBACK_LANGUAGES", cfg.DocumentFallbackLanguageIDs)
	cfg.DisableInitialWorkspaceBootstrap = envBoolOr("LSP_DISABLE_INITIAL_WORKSPACE_BOOTSTRAP", cfg.DisableInitialWorkspaceBootstrap)
	applyProjectAdapterEnv(cfg.ProjectAdapters, contract.LSPServiceJSTS, "LSP_JSTS")
	applyProjectAdapterEnv(cfg.ProjectAdapters, contract.LSPServicePython, "LSP_PYTHON")
	applyProjectAdapterEnv(cfg.ProjectAdapters, contract.LSPServiceRust, "LSP_RUST")
	applyProjectAdapterEnv(cfg.ProjectAdapters, contract.LSPServiceJava, "LSP_JAVA")
	applyProjectAdapterEnv(cfg.ProjectAdapters, contract.LSPServiceCSS, "LSP_CSS")
	applyProjectAdapterEnv(cfg.ProjectAdapters, contract.LSPServiceShell, "LSP_SHELL")
	return cfg
}

func applyProjectAdapterEnv(adapters map[string]contract.LSPProjectAdapterConfig, service, prefix string) {
	cfg := adapters[service]
	cfg.RootMarkers = envStringSliceOr(prefix+"_ROOT_MARKERS", cfg.RootMarkers)
	cfg.IgnoredDirNames = envStringSliceOr(prefix+"_IGNORED_DIRS", cfg.IgnoredDirNames)
	cfg.FirstSourceExtensions = envStringSliceOr(prefix+"_FIRST_SOURCE_EXTENSIONS", cfg.FirstSourceExtensions)
	adapters[service] = cfg
}

func envStringSliceOr(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return slices.Clone(fallback)
	}
	values := configutil.SplitConfigStringSlice(value)
	if len(values) == 0 {
		return slices.Clone(fallback)
	}
	return values
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
