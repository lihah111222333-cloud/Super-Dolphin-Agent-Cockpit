package multilsp

import (
	"context"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

func defaultLanguageServiceNoiseDirSet() map[string]struct{} {
	return stringSetFromList(platformconfig.DefaultLSPConfig().NoiseDirNames)
}

// NewLanguageAdapterRegistryFromConfig 从配置创建语言适配器注册表。
func NewLanguageAdapterRegistryFromConfig(cfg contract.LSPConfig) *LanguageAdapterRegistry {
	cfg = lspConfigWithDefaults(cfg)
	return NewLanguageAdapterRegistry(
		goLanguageAdapter{
			caches:           &goResolverCaches{},
			directoryFilters: cfg.GoDirectoryFilters,
			noiseDirNames:    cfg.NoiseDirNames,
			idleTimeout:      cfg.IdleTimeout,
		},
		projectAdapterFromConfig(jstsAdapterDefaults(), cfg, contract.LSPServiceJSTS),
		pythonAdapterFromConfig(cfg),
		projectAdapterFromConfig(rustAdapterDefaults(), cfg, contract.LSPServiceRust),
		projectAdapterFromConfig(javaAdapterDefaults(), cfg, contract.LSPServiceJava),
		projectAdapterFromConfig(cssAdapterDefaults(), cfg, contract.LSPServiceCSS),
		projectAdapterFromConfig(htmlAdapterDefaults(), cfg, contract.LSPServiceHTML),
		projectAdapterFromConfig(jsonAdapterDefaults(), cfg, contract.LSPServiceJSON),
		projectAdapterFromConfig(yamlAdapterDefaults(), cfg, contract.LSPServiceYAML),
		projectAdapterFromConfig(markdownAdapterDefaults(), cfg, contract.LSPServiceMarkdown),
		projectAdapterFromConfig(vueAdapterDefaults(), cfg, contract.LSPServiceVue),
		projectAdapterFromConfig(svelteAdapterDefaults(), cfg, contract.LSPServiceSvelte),
		clangdAdapterFromConfig(cfg),
		projectAdapterFromConfig(swiftAdapterDefaults(), cfg, contract.LSPServiceSwift),
		projectAdapterFromConfig(csharpAdapterDefaults(), cfg, contract.LSPServiceCSharp),
		projectAdapterFromConfig(phpAdapterDefaults(), cfg, contract.LSPServicePHP),
		projectAdapterFromConfig(rubyAdapterDefaults(), cfg, contract.LSPServiceRuby),
		projectAdapterFromConfig(kotlinAdapterDefaults(), cfg, contract.LSPServiceKotlin),
		projectAdapterFromConfig(dartAdapterDefaults(), cfg, contract.LSPServiceDart),
		projectAdapterFromConfig(luaAdapterDefaults(), cfg, contract.LSPServiceLua),
		projectAdapterFromConfig(dockerAdapterDefaults(), cfg, contract.LSPServiceDocker),
		projectAdapterFromConfig(terraformAdapterDefaults(), cfg, contract.LSPServiceTerraform),
		projectAdapterFromConfig(graphqlAdapterDefaults(), cfg, contract.LSPServiceGraphQL),
		projectAdapterFromConfig(prismaAdapterDefaults(), cfg, contract.LSPServicePrisma),
		projectAdapterFromConfig(shellAdapterDefaults(), cfg, contract.LSPServiceShell),
		projectAdapterFromConfig(protoAdapterDefaults(), cfg, contract.LSPServiceProto),
		sqliteSQLAdapterFromConfig(cfg),
		documentFallbackAdapter{languageIDs: slices.Clone(cfg.DocumentFallbackLanguageIDs)},
	)
}

func lspConfigWithDefaults(cfg contract.LSPConfig) contract.LSPConfig {
	defaults := platformconfig.DefaultLSPConfig()
	merged := cloneLSPConfig(cfg)
	if len(merged.NoiseDirNames) == 0 {
		merged.NoiseDirNames = slices.Clone(defaults.NoiseDirNames)
	}
	if len(merged.GoDirectoryFilters) == 0 {
		merged.GoDirectoryFilters = slices.Clone(defaults.GoDirectoryFilters)
	}
	if len(merged.DocumentFallbackLanguageIDs) == 0 {
		merged.DocumentFallbackLanguageIDs = slices.Clone(defaults.DocumentFallbackLanguageIDs)
	}
	merged.ProjectAdapters = mergeLSPProjectAdapters(defaults.ProjectAdapters, merged.ProjectAdapters)
	return merged
}

func mergeLSPProjectAdapters(
	defaults map[string]contract.LSPProjectAdapterConfig,
	overrides map[string]contract.LSPProjectAdapterConfig,
) map[string]contract.LSPProjectAdapterConfig {
	merged := cloneLSPProjectAdapters(defaults)
	for key, override := range overrides {
		base := merged[key]
		if len(override.RootMarkers) > 0 {
			base.RootMarkers = slices.Clone(override.RootMarkers)
		}
		if len(override.IgnoredDirNames) > 0 {
			base.IgnoredDirNames = slices.Clone(override.IgnoredDirNames)
		}
		if len(override.FirstSourceExtensions) > 0 {
			base.FirstSourceExtensions = slices.Clone(override.FirstSourceExtensions)
		}
		merged[key] = base
	}
	return merged
}

func projectAdapterFromConfig(defaults projectLanguageAdapter, cfg contract.LSPConfig, service string) projectLanguageAdapter {
	project := cfg.ProjectAdapters[service]
	defaults.rootMarkers = slices.Clone(project.RootMarkers)
	defaults.firstSourceExtensions = slices.Clone(project.FirstSourceExtensions)
	defaults.ignoredDirNames = stringSetFromList(append(slices.Clone(project.IgnoredDirNames), cfg.NoiseDirNames...))
	return defaults
}

func jstsAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs:                    []string{"javascript", "typescript", "javascriptreact", "typescriptreact"},
		command:                        ServerCommand{Executable: "typescript-language-server", Args: []string{"--stdio"}},
		rootKind:                       "jsts_project",
		retryEmptyCallHierarchyPrepare: true,
	}
}

func pythonAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"python"},
		command:     ServerCommand{Executable: "pyright-langserver", Args: []string{"--stdio"}},
		rootKind:    "python_project",
	}
}

type pythonLanguageAdapter struct {
	projectLanguageAdapter
}

func pythonAdapterFromConfig(cfg contract.LSPConfig) pythonLanguageAdapter {
	return pythonLanguageAdapter{
		projectLanguageAdapter: projectAdapterFromConfig(pythonAdapterDefaults(), cfg, contract.LSPServicePython),
	}
}

// BootstrapPolicy 要求 Pyright 明确发布诊断快照，避免把尚未到达的慢诊断提前判为空。
func (a pythonLanguageAdapter) BootstrapPolicy(scope ResolvedLanguageScope) BootstrapPolicy {
	policy := a.projectLanguageAdapter.BootstrapPolicy(scope)
	policy.TreatMissingDiagnosticsAsEmpty = false
	return policy
}

func rustAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"rust"},
		command:     ServerCommand{Executable: "rust-analyzer"},
		rootKind:    "rust_project",
	}
}

func javaAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"java"},
		command:     ServerCommand{Executable: "jdtls"},
		rootKind:    "java_project",
		initOptions: defaultJDTLSInitOptions(),
	}
}

func cssAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"css"},
		command:     ServerCommand{Executable: "vscode-css-language-server", Args: []string{"--stdio"}},
		rootKind:    "css_project",
	}
}

func htmlAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"html"},
		command:     ServerCommand{Executable: "vscode-html-language-server", Args: []string{"--stdio"}},
		rootKind:    "html_project",
	}
}

func jsonAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"json"},
		command:     ServerCommand{Executable: "vscode-json-language-server", Args: []string{"--stdio"}},
		rootKind:    "json_project",
	}
}

func yamlAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"yaml"},
		command:     ServerCommand{Executable: "yaml-language-server", Args: []string{"--stdio"}},
		rootKind:    "yaml_project",
	}
}

func markdownAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"markdown"},
		command:     ServerCommand{Executable: "vscode-markdown-language-server", Args: []string{"--stdio"}},
		rootKind:    "markdown_project",
	}
}

func vueAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"vue"},
		command:     ServerCommand{Executable: "vue-language-server", Args: []string{"--stdio"}},
		rootKind:    "vue_project",
	}
}

func svelteAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"svelte"},
		command:     ServerCommand{Executable: "svelteserver", Args: []string{"--stdio"}},
		rootKind:    "svelte_project",
	}
}

func clangdAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: contract.ClangdLanguageIDs(),
		command:     ServerCommand{Executable: "clangd"},
		rootKind:    "clangd_project",
	}
}

func swiftAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"swift"},
		command:     ServerCommand{Executable: "sourcekit-lsp"},
		rootKind:    "swift_project",
	}
}

func csharpAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"csharp"},
		command:     ServerCommand{Executable: "csharp-ls"},
		rootKind:    "csharp_project",
		envPolicy:   dotnetRootEnvPolicy,
	}
}

func phpAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"php"},
		command:     ServerCommand{Executable: "intelephense", Args: []string{"--stdio"}},
		rootKind:    "php_project",
	}
}

func rubyAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"ruby"},
		command:     ServerCommand{Executable: "solargraph", Args: []string{"stdio"}},
		rootKind:    "ruby_project",
	}
}

func kotlinAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"kotlin"},
		command:     ServerCommand{Executable: "kotlin-language-server"},
		rootKind:    "kotlin_project",
	}
}

func dartAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"dart"},
		command:     ServerCommand{Executable: "dart", Args: []string{"language-server", "--protocol=lsp"}},
		rootKind:    "dart_project",
	}
}

func luaAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"lua"},
		command:     ServerCommand{Executable: "lua-language-server"},
		rootKind:    "lua_project",
	}
}

func dockerAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"dockerfile"},
		command:     ServerCommand{Executable: "docker-langserver", Args: []string{"--stdio"}},
		rootKind:    "docker_project",
	}
}

func terraformAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"terraform"},
		command:     ServerCommand{Executable: "terraform-ls", Args: []string{"serve"}},
		rootKind:    "terraform_project",
	}
}

func graphqlAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"graphql"},
		command:     ServerCommand{Executable: "graphql-lsp", Args: []string{"server", "-m", "stream"}},
		rootKind:    "graphql_project",
	}
}

func prismaAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"prisma"},
		command:     ServerCommand{Executable: "prisma-language-server", Args: []string{"--stdio"}},
		rootKind:    "prisma_project",
	}
}

func shellAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"shellscript"},
		command:     ServerCommand{Executable: "bash-language-server", Args: []string{"start"}},
		rootKind:    "shell_project",
	}
}

func protoAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"proto"},
		command:     ServerCommand{Executable: "buf", Args: []string{"lsp", "serve"}},
		rootKind:    "proto_project",
	}
}

type sqliteSQLLanguageAdapter struct {
	projectLanguageAdapter
}

func sqliteSQLAdapterFromConfig(cfg contract.LSPConfig) sqliteSQLLanguageAdapter {
	adapter := projectAdapterFromConfig(projectLanguageAdapter{
		languageIDs: []string{"sql"},
		command:     ServerCommand{Executable: "sqruff", Args: []string{"lsp"}},
		rootKind:    "sqlite_sql_project",
	}, cfg, contract.LSPServiceSQL)
	return sqliteSQLLanguageAdapter{projectLanguageAdapter: adapter}
}

// ResolveRoot 复用 SQL 项目根解析，但把内部路由 ID 规范化为 LSP 文档语言 sql。
func (a sqliteSQLLanguageAdapter) ResolveRoot(ctx context.Context, scope LSPToolScope, target string) (ResolvedLanguageScope, error) {
	resolved, err := a.projectLanguageAdapter.ResolveRoot(ctx, scope, target)
	if err != nil {
		return ResolvedLanguageScope{}, err
	}
	resolved.LanguageID = "sql"
	return resolved, nil
}

func cloneLSPConfig(cfg contract.LSPConfig) contract.LSPConfig {
	return contract.LSPConfig{
		NoiseDirNames:                    slices.Clone(cfg.NoiseDirNames),
		GoDirectoryFilters:               slices.Clone(cfg.GoDirectoryFilters),
		ProjectAdapters:                  cloneLSPProjectAdapters(cfg.ProjectAdapters),
		DocumentFallbackLanguageIDs:      slices.Clone(cfg.DocumentFallbackLanguageIDs),
		DisableInitialWorkspaceBootstrap: cfg.DisableInitialWorkspaceBootstrap,
		IdleTimeout:                      cfg.IdleTimeout,
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

func stringSetFromList(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := strings.TrimSpace(value); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}
