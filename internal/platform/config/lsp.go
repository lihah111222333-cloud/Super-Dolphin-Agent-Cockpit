package config

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/configutil"
)

// DefaultLSPConfig 返回内置 LSP 配置。
// 默认值会过滤大型或生成目录，并按语言声明 root marker，避免初始索引扫进无关工作区。
func DefaultLSPConfig() contract.LSPConfig {
	return contract.LSPConfig{
		NoiseDirNames:                    defaultLSPNoiseDirNames(),
		GoDirectoryFilters:               defaultLSPGoDirectoryFilters(),
		ProjectAdapters:                  defaultLSPProjectAdapters(),
		DocumentFallbackLanguageIDs:      nil,
		DisableInitialWorkspaceBootstrap: true,
	}
}

func defaultLSPNoiseDirNames() []string {
	return []string{
		".agent", ".agents", ".claude", ".git", ".workspace", ".worktrees",
		"coverage", "dist", "docs", "node_modules", "vendor",
	}
}

func defaultLSPGoDirectoryFilters() []string {
	return []string{
		"-.agent", "-.agents", "-.claude", "-.git", "-.workspace", "-.worktrees",
		"-coverage", "-dist", "-docs", "-node_modules", "-vendor",
		"-**/.agent", "-**/.agents", "-**/.claude", "-**/.git", "-**/.workspace",
		"-**/.worktrees", "-**/coverage", "-**/dist", "-**/docs", "-**/node_modules",
		"-**/vendor",
	}
}

type lspProjectAdapterEntry struct {
	service string
	cfg     contract.LSPProjectAdapterConfig
}

func defaultLSPProjectAdapters() map[string]contract.LSPProjectAdapterConfig {
	adapters := map[string]contract.LSPProjectAdapterConfig{}
	addLSPProjectAdapters(adapters, defaultCoreLSPProjectAdapters())
	addLSPProjectAdapters(adapters, defaultWebDocumentLSPProjectAdapters())
	addLSPProjectAdapters(adapters, defaultNativeLSPProjectAdapters())
	addLSPProjectAdapters(adapters, defaultInfraLSPProjectAdapters())
	return adapters
}

func addLSPProjectAdapters(adapters map[string]contract.LSPProjectAdapterConfig, entries []lspProjectAdapterEntry) {
	for _, entry := range entries {
		adapters[entry.service] = entry.cfg
	}
}

func lspProjectAdapter(markers, ignored, extensions []string) contract.LSPProjectAdapterConfig {
	return contract.LSPProjectAdapterConfig{
		RootMarkers:           markers,
		IgnoredDirNames:       ignored,
		FirstSourceExtensions: extensions,
	}
}

func commonLSPIgnoredDirs() []string {
	return []string{".build-cache", ".git", ".workspace", "dist", "node_modules", "vendor"}
}

func defaultCoreLSPProjectAdapters() []lspProjectAdapterEntry {
	return []lspProjectAdapterEntry{
		{contract.LSPServiceJSTS, lspProjectAdapter(
			[]string{"tsconfig.json", "jsconfig.json", "package.json"},
			commonLSPIgnoredDirs(), []string{".js", ".jsx", ".ts", ".tsx"})},
		{contract.LSPServicePython, lspProjectAdapter(
			[]string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile"},
			[]string{".build-cache", ".git", ".mypy_cache", ".pytest_cache", ".ruff_cache", ".venv", ".workspace", "__pycache__", "node_modules", "vendor"},
			[]string{".py", ".pyi"})},
		{contract.LSPServiceRust, lspProjectAdapter(
			[]string{"Cargo.toml"},
			[]string{".build-cache", ".git", ".workspace", "node_modules", "target", "vendor"},
			[]string{".rs"})},
		{contract.LSPServiceJava, lspProjectAdapter(
			[]string{"pom.xml", "build.gradle", "build.gradle.kts"},
			[]string{".build-cache", ".git", ".gradle", ".idea", ".workspace", "build", "node_modules", "target", "vendor"},
			[]string{".java"})},
		{contract.LSPServiceShell, lspProjectAdapter(
			[]string{".git", "package.json", "go.mod", "Makefile"},
			commonLSPIgnoredDirs(), []string{".sh", ".bash", ".zsh", ".ksh", ".bats"})},
		{contract.LSPServiceSQL, lspProjectAdapter(
			[]string{".sqllsrc.json", "sqlc.yaml", "sqlc.yml", "go.mod", "package.json"},
			commonLSPIgnoredDirs(), []string{".sql"})},
	}
}

func defaultWebDocumentLSPProjectAdapters() []lspProjectAdapterEntry {
	return []lspProjectAdapterEntry{
		{contract.LSPServiceCSS, lspProjectAdapter([]string{"package.json"}, commonLSPIgnoredDirs(), []string{".css"})},
		{contract.LSPServiceHTML, lspProjectAdapter([]string{"package.json", "index.html"}, commonLSPIgnoredDirs(), []string{".html", ".htm"})},
		{contract.LSPServiceJSON, lspProjectAdapter([]string{"package.json"}, commonLSPIgnoredDirs(), []string{".json", ".jsonc"})},
		{contract.LSPServiceYAML, lspProjectAdapter([]string{".yamllint", ".yaml-language-server", "package.json"}, commonLSPIgnoredDirs(), []string{".yaml", ".yml"})},
		{contract.LSPServiceMarkdown, lspProjectAdapter([]string{"README.md", "readme.md", "package.json", ".git"}, commonLSPIgnoredDirs(), []string{".md", ".markdown"})},
		{contract.LSPServiceVue, lspProjectAdapter([]string{"package.json", "tsconfig.json", "jsconfig.json", "vite.config.ts", "vite.config.js"}, commonLSPIgnoredDirs(), []string{".vue"})},
		{contract.LSPServiceSvelte, lspProjectAdapter([]string{"package.json", "svelte.config.js", "svelte.config.ts", "vite.config.ts", "vite.config.js"}, commonLSPIgnoredDirs(), []string{".svelte"})},
	}
}

func defaultNativeLSPProjectAdapters() []lspProjectAdapterEntry {
	return []lspProjectAdapterEntry{
		{contract.LSPServiceClangd, lspProjectAdapter(
			[]string{"compile_commands.json", "compile_flags.txt", "CMakeLists.txt", ".clangd"},
			[]string{".build-cache", ".git", ".workspace", "build", "dist", "node_modules", "vendor"},
			[]string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".m", ".mm"})},
		{contract.LSPServiceSwift, lspProjectAdapter(
			[]string{"Package.swift", ".swiftpm"},
			[]string{".build", ".build-cache", ".git", ".workspace", "DerivedData", "node_modules", "vendor"},
			[]string{".swift"})},
		{contract.LSPServiceCSharp, lspProjectAdapter(
			[]string{"global.json", "Directory.Build.props", "Directory.Build.targets"},
			[]string{".build-cache", ".git", ".workspace", "bin", "node_modules", "obj", "vendor"},
			[]string{".cs"})},
		{contract.LSPServicePHP, lspProjectAdapter([]string{"composer.json", "index.php"},
			[]string{".build-cache", ".git", ".workspace", "node_modules", "vendor"}, []string{".php", ".phtml"})},
		{contract.LSPServiceRuby, lspProjectAdapter([]string{"Gemfile", ".ruby-version", ".solargraph.yml"},
			[]string{".bundle", ".build-cache", ".git", ".workspace", "node_modules", "vendor"}, []string{".rb", ".rake"})},
		{contract.LSPServiceKotlin, lspProjectAdapter(
			[]string{"settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts", "pom.xml"},
			[]string{".build-cache", ".git", ".gradle", ".workspace", "build", "node_modules", "target", "vendor"},
			[]string{".kt", ".kts"})},
		{contract.LSPServiceDart, lspProjectAdapter([]string{"pubspec.yaml"},
			[]string{".build-cache", ".dart_tool", ".git", ".workspace", "build", "node_modules", "vendor"}, []string{".dart"})},
		{contract.LSPServiceLua, lspProjectAdapter([]string{".luarc.json", ".luarc.jsonc", "stylua.toml"},
			[]string{".build-cache", ".git", ".workspace", "node_modules", "vendor"}, []string{".lua"})},
	}
}

func defaultInfraLSPProjectAdapters() []lspProjectAdapterEntry {
	return []lspProjectAdapterEntry{
		{contract.LSPServiceDocker, lspProjectAdapter([]string{"Dockerfile", "Containerfile", "docker-compose.yml", "docker-compose.yaml"}, commonLSPIgnoredDirs(), []string{".dockerfile"})},
		{contract.LSPServiceTerraform, lspProjectAdapter([]string{".terraform", "main.tf", "versions.tf"}, []string{".build-cache", ".git", ".terraform", ".workspace", "node_modules", "vendor"}, []string{".tf", ".tfvars"})},
		{contract.LSPServiceGraphQL, lspProjectAdapter([]string{".graphqlrc", ".graphqlrc.json", ".graphqlrc.yml", "graphql.config.js", "package.json"}, commonLSPIgnoredDirs(), []string{".graphql", ".gql"})},
		{contract.LSPServicePrisma, lspProjectAdapter([]string{"schema.prisma", "package.json"}, commonLSPIgnoredDirs(), []string{".prisma"})},
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
		{service: contract.LSPServiceHTML, prefix: "LSP_HTML"},
		{service: contract.LSPServiceJSON, prefix: "LSP_JSON"},
		{service: contract.LSPServiceYAML, prefix: "LSP_YAML"},
		{service: contract.LSPServiceMarkdown, prefix: "LSP_MARKDOWN"},
		{service: contract.LSPServiceVue, prefix: "LSP_VUE"},
		{service: contract.LSPServiceSvelte, prefix: "LSP_SVELTE"},
		{service: contract.LSPServiceClangd, prefix: "LSP_CLANGD"},
		{service: contract.LSPServiceSwift, prefix: "LSP_SWIFT"},
		{service: contract.LSPServiceCSharp, prefix: "LSP_CSHARP"},
		{service: contract.LSPServicePHP, prefix: "LSP_PHP"},
		{service: contract.LSPServiceRuby, prefix: "LSP_RUBY"},
		{service: contract.LSPServiceKotlin, prefix: "LSP_KOTLIN"},
		{service: contract.LSPServiceDart, prefix: "LSP_DART"},
		{service: contract.LSPServiceLua, prefix: "LSP_LUA"},
		{service: contract.LSPServiceDocker, prefix: "LSP_DOCKER"},
		{service: contract.LSPServiceTerraform, prefix: "LSP_TERRAFORM"},
		{service: contract.LSPServiceGraphQL, prefix: "LSP_GRAPHQL"},
		{service: contract.LSPServicePrisma, prefix: "LSP_PRISMA"},
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
