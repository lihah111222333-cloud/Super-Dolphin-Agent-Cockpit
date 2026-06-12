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
	runtimeResourcesEnv = "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"
	lspBundleName       = "lsp"
	lspManifestName     = "lsp-manifest.json"
)

type runtimeDeps struct {
	executable  func() (string, error)
	userHomeDir func() (string, error)
	setenv      func(string, string) error
}

var deps = runtimeDeps{
	executable:  os.Executable,
	userHomeDir: os.UserHomeDir,
	setenv:      os.Setenv,
}

var bundledSidecarNames = []string{"mcp-orch", "mcp-lsp", "mcp-ida"}

// PackagedRuntime describes the resources and app-owned data directories for a
// packaged desktop runtime.
type PackagedRuntime struct {
	ResourcesDir  string
	BinDir        string
	MigrationsDir string
	PostgresRoot  string
	AppDataDir    string
}

type SidecarRuntimeInput struct {
	ExecutablePath string
	Env            map[string]string
}

type SidecarRuntimeContract struct {
	Mode         string
	ResourcesDir string
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
	exe, err := deps.executable()
	if err != nil {
		return fmt.Errorf("resolve executable for packaged runtime: %w", err)
	}
	resolved, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           runtimeGOOS(),
		GOARCH:         runtimeGOARCH(),
		Env:            environmentMap(os.Environ()),
		ExecutablePath: exe,
	})
	if err != nil {
		return err
	}
	if resolved.RuntimeMode != RuntimeModePackaged || resolved.PackagedRuntime == nil {
		return nil
	}
	home, err := deps.userHomeDir()
	if err != nil {
		return fmt.Errorf("resolve packaged runtime home: %w", err)
	}
	runtime := *resolved.PackagedRuntime
	runtime.AppDataDir = packagedAppDataDir(home)
	if err := applyPackagedRuntimeEnv(runtime); err != nil {
		return fmt.Errorf("configure packaged runtime env: %w", err)
	}
	return nil
}

func ConfigureSidecarRuntime() error {
	contract, err := ResolveSidecarRuntimeContract(SidecarRuntimeInput{
		Env: environmentMap(os.Environ()),
	})
	if err != nil {
		return err
	}
	return applySidecarRuntimeContract(contract)
}

func ResolveSidecarRuntimeContract(input SidecarRuntimeInput) (SidecarRuntimeContract, error) {
	mode := strings.TrimSpace(input.Env[runtimeModeEnv])
	resources := strings.TrimSpace(input.Env[runtimeResourcesEnv])
	if mode == "" || resources == "" {
		return SidecarRuntimeContract{}, fmt.Errorf(
			"parent launch contract missing: sidecar requires %s and %s",
			runtimeModeEnv,
			runtimeResourcesEnv,
		)
	}
	switch mode {
	case string(RuntimeModeDev), string(RuntimeModePackaged):
	default:
		return SidecarRuntimeContract{}, fmt.Errorf("parent launch contract invalid: %s must be dev or packaged", runtimeModeEnv)
	}
	return SidecarRuntimeContract{Mode: mode, ResourcesDir: filepath.Clean(resources)}, nil
}

// PackagedRuntimeFromExecutable resolves the packaged runtime for callers that
// still need the legacy PackagedRuntime shape. It delegates packaged/dev
// classification to ResolveRuntime so path shape alone cannot select packaged.
func PackagedRuntimeFromExecutable(executablePath, userHome string) (PackagedRuntime, bool) {
	executablePath = strings.TrimSpace(executablePath)
	userHome = strings.TrimSpace(userHome)
	if executablePath == "" || userHome == "" {
		return PackagedRuntime{}, false
	}
	resolved, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           runtimeGOOS(),
		GOARCH:         runtimeGOARCH(),
		ExecutablePath: executablePath,
		UserHome:       userHome,
	})
	if err != nil || resolved.RuntimeMode != RuntimeModePackaged || resolved.PackagedRuntime == nil {
		return PackagedRuntime{}, false
	}
	return *resolved.PackagedRuntime, true
}

func packagedRuntimeFromResources(resources, userHome string) PackagedRuntime {
	return packagedRuntimeFromResourcesForOS(runtimeGOOS(), resources, userHome)
}

func packagedAppDataDir(userHome string) string {
	return packagedAppDataDirForOS(runtimeGOOS(), userHome)
}

func packagedResourcesDir(executablePath string) string {
	return packagedResourcesDirForOS(runtimeGOOS(), executablePath)
}

func applyPackagedEnv(resources, userHome string) error {
	return applyPackagedRuntimeEnv(packagedRuntimeFromResources(resources, userHome))
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
	case "bash-language-server":
		return []string{"shellscript"}
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

func applyPackagedRuntimeEnv(runtime PackagedRuntime) error {
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
		func() error { return setEnv(runtimeModeEnv, "packaged") },
		func() error { return setEnv(runtimeResourcesEnv, runtime.ResourcesDir) },
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

// LoadVideoEnv reads $SUPER_DOLPHIN_HOME/video.env and sets any KEY=VALUE
// pairs it finds as environment variables. Missing file is silently ignored.
func LoadVideoEnv() error {
	path, err := videoEnvPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if err := os.Setenv(strings.TrimSpace(k), strings.TrimSpace(v)); err != nil {
			return err
		}
	}
	return nil
}

func WriteVideoEnv(apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("SILICONFLOW_API_KEY is required")
	}
	path, err := videoEnvPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create video env directory: %w", err)
	}
	content := "SILICONFLOW_API_KEY=" + apiKey + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write video env: %w", err)
	}
	return nil
}

func videoEnvPath() (string, error) {
	home := strings.TrimSpace(os.Getenv(superDolphinHomeEnv))
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for video env: %w", err)
		}
		home = packagedAppDataDir(home)
	}
	return filepath.Join(home, "video.env"), nil
}

func applySidecarRuntimeContract(contract SidecarRuntimeContract) error {
	setters := []func() error{
		func() error { return setEnvIfEmpty(projectRootEnv, contract.ResourcesDir) },
	}
	if contract.Mode == "packaged" {
		runtime := packagedRuntimeFromResources(contract.ResourcesDir, "")
		setters = append(setters,
			func() error { return setControlledEnvPath("PATH", packagedSidecarPathEntries(runtime)...) },
			func() error { return setEnv(peerBinDirEnv, runtime.BinDir) },
			func() error {
				return setEnvIfEmpty(lspBundleDirEnv, filepath.Join(runtime.ResourcesDir, lspBundleName))
			},
			func() error {
				return setEnvIfEmpty(lspManifestEnv, filepath.Join(runtime.ResourcesDir, lspBundleName, lspManifestName))
			},
		)
	}
	return runEnvSetters(setters...)
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
	for _, name := range executableNamesForOS(runtimeGOOS(), bundledSidecarNames) {
		path := filepath.Join(binDir, name)
		if err := requireExecutableFile(path); err != nil {
			return fmt.Errorf("missing bundled sidecar %s: %w", path, err)
		}
	}
	return nil
}

func requireExecutableFile(path string) error {
	return requireExecutableFileForOS(runtimeGOOS(), path)
}

func newSessionToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// archguard:ignore panic_count -- cryptographic entropy failure is unrecoverable for session-token generation.
		panic("generate packaged control-plane session token: " + err.Error())
	}
	return "sd-" + hex.EncodeToString(raw[:])
}

func packagedPathEntries(runtime PackagedRuntime) []string {
	return packagedPathEntriesForOS(runtimeGOOS(), runtime)
}

func packagedSidecarPathEntries(runtime PackagedRuntime) []string {
	return packagedSidecarPathEntriesForOS(runtimeGOOS(), runtime)
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
	if err := deps.setenv(key, value); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}
