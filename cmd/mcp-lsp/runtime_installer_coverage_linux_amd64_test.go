//go:build linux && amd64

package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type installerConfigLookup func(string) (installer.InstallerConfig, bool)

// TestLinuxAMD64InstallerCoversEveryRequiredLSPAdapter derives its producer set
// from the default adapter registry, then checks every installer registration.
// A new RequiresLSPClient adapter must update this guard before it can pass.
func TestLinuxAMD64InstallerCoversEveryRequiredLSPAdapter(t *testing.T) {
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("setupInstallerWithError: %v", err)
	}
	if err := assertLinuxAMD64InstallerCoverage(multilsp.NewDefaultLanguageAdapterRegistry(), provider.ConfigForLanguage); err != nil {
		t.Fatal(err)
	}
}

// TestLinuxAMD64InstallerCoverageGuardFailsFirstForAddedAdapter proves that a
// newly introduced producer language cannot silently bypass the installer map.
func TestLinuxAMD64InstallerCoverageGuardFailsFirstForAddedAdapter(t *testing.T) {
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("setupInstallerWithError: %v", err)
	}
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	base, ok := registry.AdapterForLanguage("go")
	if !ok {
		t.Fatal("default registry has no Go adapter to derive a required-LSP test adapter")
	}
	registry.Register(requiredLSPCoverageAdapter{
		LanguageAdapter: base,
		languageID:      "coverage-new-language",
	})

	err = assertLinuxAMD64InstallerCoverage(registry, provider.ConfigForLanguage)
	if err == nil || !strings.Contains(err.Error(), "coverage-new-language") {
		t.Fatalf("coverage guard did not fail for added adapter: %v", err)
	}
}

// TestLinuxAMD64InstallerCoverageGuardFailsFirstForDeletedConfig proves that
// removing one real registration is an explicit, test-observable failure.
func TestLinuxAMD64InstallerCoverageGuardFailsFirstForDeletedConfig(t *testing.T) {
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("setupInstallerWithError: %v", err)
	}
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	configs := make(map[string]installer.InstallerConfig)
	for _, languageID := range requiredLSPAdapterLanguageIDs(registry) {
		cfg, ok := provider.ConfigForLanguage(languageID)
		if !ok {
			t.Fatalf("baseline config %q is unexpectedly absent", languageID)
		}
		configs[languageID] = cfg
	}
	delete(configs, "go")

	err = assertLinuxAMD64InstallerCoverage(registry, func(languageID string) (installer.InstallerConfig, bool) {
		cfg, ok := configs[languageID]
		return cfg, ok
	})
	if err == nil || !strings.Contains(err.Error(), "go") {
		t.Fatalf("coverage guard did not fail for deleted config: %v", err)
	}
}

type requiredLSPCoverageAdapter struct {
	multilsp.LanguageAdapter
	languageID string
}

func (a requiredLSPCoverageAdapter) LanguageIDs() []string {
	return []string{a.languageID}
}

func requiredLSPAdapterLanguageIDs(registry *multilsp.LanguageAdapterRegistry) []string {
	ids := make([]string, 0)
	for _, languageID := range registry.LanguageIDs() {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if ok && adapter.CapabilityPolicy().RequiresLSPClient {
			ids = append(ids, languageID)
		}
	}
	return ids
}

func assertLinuxAMD64InstallerCoverage(registry *multilsp.LanguageAdapterRegistry, lookup installerConfigLookup) error {
	if registry == nil {
		return fmt.Errorf("installer coverage registry is nil")
	}
	if lookup == nil {
		return fmt.Errorf("installer coverage lookup is nil")
	}
	groups, err := linuxAMD64InstallerCoverageGroups()
	if err != nil {
		return err
	}
	required := requiredLSPAdapterLanguageIDs(registry)
	if len(required) == 0 {
		return fmt.Errorf("default adapter registry has no RequiresLSPClient languages")
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, languageID := range required {
		if err := validateRequiredLinuxInstaller(languageID, groups, lookup); err != nil {
			return err
		}
		requiredSet[languageID] = struct{}{}
	}
	for _, languageID := range sortedInstallerCoverageLanguageIDs(groups) {
		if _, ok := requiredSet[languageID]; !ok {
			return fmt.Errorf("installer group %q has stale language %q without RequiresLSPClient adapter", groups[languageID], languageID)
		}
	}
	return nil
}

func validateRequiredLinuxInstaller(languageID string, groups map[string]string, lookup installerConfigLookup) error {
	group, ok := groups[languageID]
	if !ok {
		return fmt.Errorf("RequiresLSPClient language %q has no explicit installer group", languageID)
	}
	cfg, ok := lookup(languageID)
	if !ok {
		return fmt.Errorf("RequiresLSPClient language %q has no installer config", languageID)
	}
	return validateLinuxAMD64InstallerConfig(languageID, group, cfg)
}

func linuxAMD64InstallerCoverageGroups() (map[string]string, error) {
	groups := make(map[string]string)
	add := func(group string, languages map[string]string) error {
		for _, languageID := range sortedStringMapKeys(languages) {
			if previous, exists := groups[languageID]; exists {
				return fmt.Errorf("language %q is assigned to both %q and %q installer groups", languageID, previous, group)
			}
			groups[languageID] = group
		}
		return nil
	}

	if err := add("native-managed", linuxAMD64NativeInstallerBinaries()); err != nil {
		return nil, err
	}
	if err := add("npm", linuxAMD64NPMInstallerBinaries()); err != nil {
		return nil, err
	}
	if err := add("go", map[string]string{
		"go": "gopls", "gomod": "gopls", "gosum": "gopls", "gowork": "gopls",
	}); err != nil {
		return nil, err
	}
	if err := add("shell", map[string]string{"shellscript": "bash-language-server"}); err != nil {
		return nil, err
	}
	return groups, nil
}

func linuxAMD64NativeInstallerBinaries() map[string]string {
	native := make(map[string]string, len(contract.ClangdLanguageIDs())+12)
	for _, languageID := range contract.ClangdLanguageIDs() {
		// registry 在索引前将所有 MQL 别名规范化为 cpp，守卫必须对齐该生产真值。
		if contract.IsMQLLanguageID(languageID) {
			languageID = "cpp"
		}
		native[languageID] = "clangd"
	}
	for languageID, binaryName := range map[string]string{
		"swift":     "sourcekit-lsp",
		"csharp":    "csharp-ls",
		"ruby":      "solargraph",
		"kotlin":    "kotlin-language-server",
		"dart":      "dart",
		"rust":      "rust-analyzer",
		"java":      "jdtls",
		"proto":     "buf",
		"lua":       "lua-language-server",
		"terraform": "terraform-ls",
		"sql":       "sqruff",
	} {
		native[languageID] = binaryName
	}
	return native
}

func linuxAMD64NPMInstallerBinaries() map[string]string {
	return map[string]string{
		"javascript":      "typescript-language-server",
		"javascriptreact": "typescript-language-server",
		"typescript":      "typescript-language-server",
		"typescriptreact": "typescript-language-server",
		"python":          "pyright-langserver",
		"css":             "vscode-css-language-server",
		"html":            "vscode-html-language-server",
		"json":            "vscode-json-language-server",
		"yaml":            "yaml-language-server",
		"markdown":        "vscode-markdown-language-server",
		"vue":             "vue-language-server",
		"svelte":          "svelteserver",
		"php":             "intelephense",
		"dockerfile":      "docker-langserver",
		"graphql":         "graphql-lsp",
		"prisma":          "prisma-language-server",
	}
}

func validateLinuxAMD64InstallerConfig(languageID, group string, cfg installer.InstallerConfig) error {
	if cfg.BinaryName != expectedLinuxAMD64InstallerBinary(languageID, group) {
		return fmt.Errorf("language %q installer binary %q does not match %q group mapping", languageID, cfg.BinaryName, group)
	}
	if !cfg.AllowInstallCommand {
		return fmt.Errorf("language %q installer does not allow its declared install source", languageID)
	}
	switch group {
	case "native-managed":
		return validateLinuxNativeManagedInstaller(languageID, cfg)
	case "npm":
		return validateLinuxCommandInstaller(languageID, "NPM", "npm", cfg)
	case "go":
		return validateLinuxCommandInstaller(languageID, "Go", "go", cfg)
	case "shell":
		return validateLinuxCommandInstaller(languageID, "shell", "npm", cfg)
	default:
		return fmt.Errorf("language %q has unknown installer group %q", languageID, group)
	}
	return nil
}

func validateLinuxNativeManagedInstaller(languageID string, cfg installer.InstallerConfig) error {
	if cfg.ManagedInstall == nil {
		return fmt.Errorf("native language %q must use ManagedInstall", languageID)
	}
	if strings.TrimSpace(cfg.ManagedBinaryPath) == "" || !filepath.IsAbs(cfg.ManagedBinaryPath) {
		return fmt.Errorf("native language %q must use an absolute ManagedBinaryPath, got %q", languageID, cfg.ManagedBinaryPath)
	}
	if strings.TrimSpace(cfg.InstallCmd) != "" {
		return fmt.Errorf("native language %q must not configure InstallCmd %q", languageID, cfg.InstallCmd)
	}
	return nil
}

func validateLinuxCommandInstaller(languageID, label, command string, cfg installer.InstallerConfig) error {
	if cfg.ManagedInstall != nil || strings.TrimSpace(cfg.ManagedBinaryPath) != "" || cfg.InstallCmd != command {
		return fmt.Errorf("%s language %q must use InstallCmd=%s without managed artifact", label, languageID, command)
	}
	return nil
}

func expectedLinuxAMD64InstallerBinary(languageID, group string) string {
	if group == "native-managed" {
		return linuxAMD64NativeInstallerBinaries()[languageID]
	}
	if group == "npm" {
		return linuxAMD64NPMInstallerBinaries()[languageID]
	}
	if group == "go" {
		return "gopls"
	}
	if group == "shell" {
		return "bash-language-server"
	}
	return ""
}

func sortedInstallerCoverageLanguageIDs(groups map[string]string) []string {
	ids := make([]string, 0, len(groups))
	for languageID := range groups {
		ids = append(ids, languageID)
	}
	sort.Strings(ids)
	return ids
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
