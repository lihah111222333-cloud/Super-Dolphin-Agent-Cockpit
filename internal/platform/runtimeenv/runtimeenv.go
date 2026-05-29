package runtimeenv

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	controlRPCAddrEnv   = "GO_AGENT_CTL_RPC_ADDR"
	httpAddrEnv         = "SUPER_DOLPHIN_HTTP_ADDR"
	peerBinDirEnv       = "GO_AGENT_PEER_BIN_DIR"
	sessionTokenEnv     = "GO_AGENT_CTL_SESSION_TOKEN"
	requireCodexEnv     = "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX"
	modelRegistryEnv    = "SUPER_DOLPHIN_MODEL_REGISTRY"
	modelRegistryBundle = "models.yaml"
	projectRootEnv      = "PROJECT_ROOT"
	superDolphinHomeEnv = "SUPER_DOLPHIN_HOME"
	codexHomeEnv        = "CODEX_HOME"
	packagedCodexEnv    = "SUPER_DOLPHIN_PACKAGED_CODEX_IDENTITY"
	lspBundleDirEnv     = "SUPER_DOLPHIN_LSP_BUNDLE_DIR"
	lspManifestEnv      = "SUPER_DOLPHIN_LSP_MANIFEST"
	lspBundleName       = "lsp"
	lspManifestName     = "lsp-manifest.json"
)

var (
	executablePathForRuntime = os.Executable
	userHomeDirForRuntime    = os.UserHomeDir
	setenvForRuntime         = os.Setenv
)

var bundledSidecarNames = []string{"mcp-orch", "mcp-lsp", "mcp-ida"}

// PackagedRuntime describes the resources and app-owned data directories for a
// macOS app bundle runtime.
type PackagedRuntime struct {
	ResourcesDir  string
	BinDir        string
	MigrationsDir string
	PostgresRoot  string
	AppDataDir    string
}

type LSPBundle struct {
	BundleDir    string
	ManifestPath string
	Servers      map[string]LSPServer
	Languages    map[string]LSPServer
}

type LSPServer struct {
	ID        string
	Path      string
	Languages []string
}

type lspBundleManifest struct {
	Servers map[string]lspServerManifest `json:"servers"`
}

type lspServerManifest struct {
	Path      string   `json:"path"`
	Version   string   `json:"version"`
	SHA256    string   `json:"sha256"`
	Languages []string `json:"languages"`
}

func ConfigurePackagedApp() error {
	exe, err := executablePathForRuntime()
	if err != nil {
		return fmt.Errorf("resolve executable for packaged runtime: %w", err)
	}
	resources := packagedResourcesDir(exe)
	if resources == "" {
		return nil
	}
	home, err := userHomeDirForRuntime()
	if err != nil {
		return fmt.Errorf("resolve packaged runtime home: %w", err)
	}
	runtime := packagedRuntimeFromResources(resources, home)
	if err := applyPackagedRuntimeEnv(runtime, home); err != nil {
		return fmt.Errorf("configure packaged runtime env: %w", err)
	}
	return nil
}

// PackagedRuntimeFromExecutable resolves the packaged runtime for a macOS app
// main binary or a bundled Resources/bin peer binary.
func PackagedRuntimeFromExecutable(executablePath, userHome string) (PackagedRuntime, bool) {
	executablePath = strings.TrimSpace(executablePath)
	userHome = strings.TrimSpace(userHome)
	if executablePath == "" || userHome == "" {
		return PackagedRuntime{}, false
	}
	resources := packagedResourcesDir(executablePath)
	if resources == "" {
		return PackagedRuntime{}, false
	}
	return packagedRuntimeFromResources(resources, userHome), true
}

func packagedRuntimeFromResources(resources, userHome string) PackagedRuntime {
	return PackagedRuntime{
		ResourcesDir:  resources,
		BinDir:        filepath.Join(resources, "bin"),
		MigrationsDir: filepath.Join(resources, "migrations"),
		PostgresRoot:  filepath.Join(resources, "postgres"),
		AppDataDir:    packagedAppDataDir(userHome),
	}
}

func packagedAppDataDir(userHome string) string {
	userHome = strings.TrimSpace(userHome)
	if userHome == "" {
		return ""
	}
	return filepath.Join(userHome, "Library", "Application Support", "Super Dolphin")
}

func packagedResourcesDir(executablePath string) string {
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return ""
	}
	exeDir := filepath.Dir(executablePath)
	if filepath.Base(exeDir) != "MacOS" {
		return resourcesDirFromPeerBin(exeDir)
	}
	contentsDir := filepath.Dir(exeDir)
	if filepath.Base(contentsDir) != "Contents" {
		return ""
	}
	return filepath.Join(contentsDir, "Resources")
}

func resourcesDirFromPeerBin(exeDir string) string {
	if filepath.Base(exeDir) != "bin" {
		return ""
	}
	resources := filepath.Dir(exeDir)
	if filepath.Base(resources) != "Resources" {
		return ""
	}
	contentsDir := filepath.Dir(resources)
	if filepath.Base(contentsDir) != "Contents" {
		return ""
	}
	return resources
}

func applyPackagedEnv(resources, userHome string) error {
	return applyPackagedRuntimeEnv(packagedRuntimeFromResources(resources, userHome), userHome)
}

func LoadLSPBundleFromEnv() (LSPBundle, bool, error) {
	bundleDir := strings.TrimSpace(os.Getenv(lspBundleDirEnv))
	manifestPath := strings.TrimSpace(os.Getenv(lspManifestEnv))
	if bundleDir == "" && manifestPath == "" {
		return LSPBundle{}, false, nil
	}
	if bundleDir == "" || manifestPath == "" {
		return LSPBundle{}, true, fmt.Errorf("packaged LSP bundle env requires both %s and %s", lspBundleDirEnv, lspManifestEnv)
	}
	bundle, err := LoadLSPBundle(bundleDir, manifestPath)
	return bundle, true, err
}

func LoadLSPBundle(bundleDir, manifestPath string) (LSPBundle, error) {
	bundleDir = strings.TrimSpace(bundleDir)
	manifestPath = strings.TrimSpace(manifestPath)
	if bundleDir == "" {
		return LSPBundle{}, fmt.Errorf("missing bundled LSP dir")
	}
	if manifestPath == "" {
		return LSPBundle{}, fmt.Errorf("missing bundled LSP manifest")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return LSPBundle{}, fmt.Errorf("missing bundled LSP manifest %s: %w", manifestPath, err)
	}
	var manifest lspBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return LSPBundle{}, fmt.Errorf("parse bundled LSP manifest %s: %w", manifestPath, err)
	}
	if len(manifest.Servers) == 0 {
		return LSPBundle{}, fmt.Errorf("bundled LSP manifest %s has no servers", manifestPath)
	}
	return normalizeLSPBundle(bundleDir, manifestPath, manifest)
}

func normalizeLSPBundle(bundleDir, manifestPath string, manifest lspBundleManifest) (LSPBundle, error) {
	bundle := LSPBundle{
		BundleDir:    bundleDir,
		ManifestPath: manifestPath,
		Servers:      make(map[string]LSPServer, len(manifest.Servers)),
		Languages:    map[string]LSPServer{},
	}
	for id, server := range manifest.Servers {
		serverID := normalizeLSPKey(id)
		if serverID == "" {
			return LSPBundle{}, fmt.Errorf("bundled LSP manifest %s has an empty server id", manifestPath)
		}
		serverPath, err := resolveLSPBundlePath(bundleDir, server.Path)
		if err != nil {
			return LSPBundle{}, fmt.Errorf("bundled LSP server %s path: %w", serverID, err)
		}
		if err := requireExecutableFile(serverPath); err != nil {
			return LSPBundle{}, fmt.Errorf("missing bundled LSP server %s: %w", serverPath, err)
		}
		languages := normalizeLSPLanguages(server.Languages)
		if len(languages) == 0 {
			languages = defaultLSPLanguages(serverID)
		}
		if len(languages) == 0 {
			return LSPBundle{}, fmt.Errorf("bundled LSP server %s has no languages", serverID)
		}
		resolved := LSPServer{ID: serverID, Path: serverPath, Languages: languages}
		bundle.Servers[serverID] = resolved
		for _, languageID := range languages {
			if _, exists := bundle.Languages[languageID]; exists {
				return LSPBundle{}, fmt.Errorf("bundled LSP language %s is declared by multiple servers", languageID)
			}
			bundle.Languages[languageID] = resolved
		}
	}
	return bundle, nil
}

func resolveLSPBundlePath(bundleDir, relativePath string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("path must be relative: %s", relativePath)
	}
	clean := filepath.Clean(relativePath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes bundled LSP dir: %s", relativePath)
	}
	parts := strings.Split(clean, string(filepath.Separator))
	if len(parts) > 1 && parts[0] == lspBundleName {
		clean = filepath.Join(parts[1:]...)
	}
	return filepath.Join(bundleDir, clean), nil
}

func defaultLSPLanguages(serverID string) []string {
	switch normalizeLSPKey(serverID) {
	case "gopls":
		return []string{"go", "gomod", "gosum", "gowork"}
	case "typescript-language-server":
		return []string{"javascript", "javascriptreact", "typescript", "typescriptreact"}
	case "pyright":
		return []string{"python"}
	case "vscode-langservers-extracted":
		return []string{"css"}
	case "rust-analyzer":
		return []string{"rust"}
	case "jdtls":
		return []string{"java"}
	}
	return nil
}

func normalizeLSPLanguages(values []string) []string {
	seen := map[string]struct{}{}
	languages := make([]string, 0, len(values))
	for _, value := range values {
		languageID := normalizeLSPKey(value)
		if languageID == "" {
			continue
		}
		if _, ok := seen[languageID]; ok {
			continue
		}
		seen[languageID] = struct{}{}
		languages = append(languages, languageID)
	}
	slices.Sort(languages)
	return languages
}

func normalizeLSPKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (b LSPBundle) ServerForLanguage(languageID string) (LSPServer, bool) {
	server, ok := b.Languages[normalizeLSPKey(languageID)]
	return server, ok
}

func (b LSPBundle) SemanticLanguages() []string {
	languages := make([]string, 0, len(b.Languages))
	for languageID := range b.Languages {
		languages = append(languages, languageID)
	}
	slices.Sort(languages)
	return languages
}

func applyPackagedRuntimeEnv(runtime PackagedRuntime, userHome string) error {
	if err := requireBundledSidecars(runtime.BinDir); err != nil {
		return err
	}
	lspBundle, err := LoadLSPBundle(filepath.Join(runtime.ResourcesDir, lspBundleName), filepath.Join(runtime.ResourcesDir, lspBundleName, lspManifestName))
	if err != nil {
		return err
	}
	return runEnvSetters(
		func() error { return setControlledEnvPath("PATH", packagedPathEntries(runtime)...) },
		func() error { return setEnv(peerBinDirEnv, runtime.BinDir) },
		func() error { return setEnv(lspBundleDirEnv, lspBundle.BundleDir) },
		func() error { return setEnv(lspManifestEnv, lspBundle.ManifestPath) },
		func() error { return setEnvIfEmpty(controlRPCAddrEnv, "127.0.0.1:0") },
		func() error { return setEnvIfEmpty(httpAddrEnv, "127.0.0.1:0") },
		func() error { return setEnvIfEmpty(sessionTokenEnv, newSessionToken()) },
		func() error { return setEnv(projectRootEnv, runtime.ResourcesDir) },
		func() error { return setEnv(requireCodexEnv, "1") },
		func() error { return setEnvIfEmpty(superDolphinHomeEnv, runtime.AppDataDir) },
		func() error {
			return setEnvIfEmpty(codexHomeEnv, filepath.Join(runtime.AppDataDir, "providers", "codex"))
		},
		func() error { return setEnv(packagedCodexEnv, "1") },
		func() error {
			return setIfDir("GIT_EXEC_PATH", filepath.Join(runtime.ResourcesDir, "libexec", "git-core"))
		},
		func() error {
			return setIfDir("GIT_TEMPLATE_DIR", filepath.Join(runtime.ResourcesDir, "share", "git-core", "templates"))
		},
		func() error {
			return setIfFile(modelRegistryEnv, filepath.Join(runtime.ResourcesDir, modelRegistryBundle))
		},
	)
}

func runEnvSetters(setters ...func() error) error {
	for _, set := range setters {
		if err := set(); err != nil {
			return err
		}
	}
	return nil
}

func requireBundledSidecars(binDir string) error {
	for _, name := range bundledSidecarNames {
		path := filepath.Join(binDir, name)
		if err := requireExecutableFile(path); err != nil {
			return fmt.Errorf("missing bundled sidecar %s: %w", path, err)
		}
	}
	return nil
}

func requireExecutableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("not an executable file")
	}
	return nil
}

func newSessionToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("generate packaged control-plane session token: " + err.Error())
	}
	return "sd-" + hex.EncodeToString(raw[:])
}

func packagedPathEntries(runtime PackagedRuntime) []string {
	lspDir := filepath.Join(runtime.ResourcesDir, lspBundleName)
	return []string{
		runtime.BinDir,
		filepath.Join(lspDir, "bin"),
		filepath.Join(lspDir, "node", "bin"),
		filepath.Join(lspDir, "node_modules", ".bin"),
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
}

func setControlledEnvPath(key string, entries ...string) error {
	seen := map[string]bool{}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		appendPathEntry(&out, seen, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return setEnv(key, strings.Join(out, string(os.PathListSeparator)))
}

func appendPathEntry(out *[]string, seen map[string]bool, entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" || seen[entry] {
		return
	}
	seen[entry] = true
	*out = append(*out, entry)
}

func setIfDir(key, dir string) error {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return setEnv(key, dir)
	}
	return nil
}

func setIfFile(key, path string) error {
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return nil
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return setEnv(key, path)
	}
	return nil
}

func setEnvIfEmpty(key, value string) error {
	if strings.TrimSpace(os.Getenv(key)) != "" || strings.TrimSpace(value) == "" {
		return nil
	}
	return setEnv(key, value)
}

func setEnv(key, value string) error {
	if err := setenvForRuntime(key, value); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}
