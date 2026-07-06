package multilsp

import (
	"slices"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

func defaultLanguageServiceNoiseDirSet() map[string]struct{} {
	return stringSetFromList(platformconfig.DefaultLSPConfig().NoiseDirNames)
}

// NewLanguageAdapterRegistryFromConfig 从配置创建语言适配器注册表。
func NewLanguageAdapterRegistryFromConfig(cfg contract.LSPConfig) *LanguageAdapterRegistry {
	cfg = lspConfigWithDefaults(cfg)
	return NewLanguageAdapterRegistry(
		goLanguageAdapter{directoryFilters: cfg.GoDirectoryFilters, noiseDirNames: cfg.NoiseDirNames},
		projectAdapterFromConfig(jstsAdapterDefaults(), cfg, contract.LSPServiceJSTS),
		projectAdapterFromConfig(pythonAdapterDefaults(), cfg, contract.LSPServicePython),
		projectAdapterFromConfig(rustAdapterDefaults(), cfg, contract.LSPServiceRust),
		projectAdapterFromConfig(javaAdapterDefaults(), cfg, contract.LSPServiceJava),
		projectAdapterFromConfig(cssAdapterDefaults(), cfg, contract.LSPServiceCSS),
		projectAdapterFromConfig(shellAdapterDefaults(), cfg, contract.LSPServiceShell),
		projectAdapterFromConfig(sqlAdapterDefaults(), cfg, contract.LSPServiceSQL),
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

func shellAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"shellscript"},
		command:     ServerCommand{Executable: "bash-language-server", Args: []string{"start"}},
		rootKind:    "shell_project",
	}
}

func sqlAdapterDefaults() projectLanguageAdapter {
	return projectLanguageAdapter{
		languageIDs: []string{"sql"},
		command:     ServerCommand{Executable: "sql-language-server", Args: []string{"up", "--method", "stdio"}},
		rootKind:    "sql_project",
	}
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
