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
	// 打包运行时注入给 owner、sidecar 和 provider 进程的环境变量名称。
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

// runtimeDeps 收束可测试的系统依赖，避免测试直接改全局 os 函数。
type runtimeDeps struct {
	executable  func() (string, error)
	userHomeDir func() (string, error)
	setenv      func(string, string) error
}

// systemRuntimeDeps 返回 packaged 运行时配置所需的系统依赖。
func systemRuntimeDeps() runtimeDeps {
	return runtimeDeps{
		executable:  os.Executable,
		userHomeDir: os.UserHomeDir,
		setenv:      os.Setenv,
	}
}

// bundledSidecarNames 返回打包版必须携带的 peer sidecar 可执行文件。
func bundledSidecarNames() []string {
	return []string{"mcp-orch", "mcp-lsp", "mcp-ida"}
}

// PackagedRuntime 汇总 packaged owner/sidecar 共用的资源路径，字段必须来自已校验包根。
type PackagedRuntime struct {
	ResourcesDir  string // 包内 Resources 根目录。
	BinDir        string // 包内可执行文件目录。
	MigrationsDir string // 包内 SQLite migration 目录。
	AppDataDir    string // 应用自管用户数据目录。
}

// SidecarRuntimeInput 是 sidecar 启动 contract 解析输入。
type SidecarRuntimeInput struct {
	ExecutablePath string            // sidecar 可执行路径；保留给兼容调用方。
	Env            map[string]string // 父进程注入的环境变量。
}

// SidecarRuntimeContract 是 owner 传递给 sidecar 的最小运行时约束。
type SidecarRuntimeContract struct {
	Mode         string // dev 或 packaged。
	ResourcesDir string // dev 下为项目根，packaged 下为资源根。
}

// LSPBundle 是打包 LSP manifest 的解析结果，语言映射不能重复绑定 server。
type LSPBundle struct {
	BundleDir    string               // LSP bundle 根目录。
	ManifestPath string               // 已加载的 manifest 路径。
	Servers      map[string]LSPServer // 以 server id 索引的服务声明。
	Languages    map[string]LSPServer // 以 language id 索引的服务声明。
}

// LSPServer 保留单个打包 LSP server 的执行路径和语言列表，路径需位于 bundle 根内。
type LSPServer struct {
	ID        string   // 规范化后的 server id。
	Path      string   // 包内可执行文件路径。
	Languages []string // 规范化后的 language id 列表。
}

// lspBundleManifest 映射 lsp-manifest.json 顶层结构。
type lspBundleManifest struct {
	Servers map[string]lspServerManifest `json:"servers"`
}

// lspServerManifest 映射单个 LSP server 的清单声明。
type lspServerManifest struct {
	Path      string   `json:"path"`
	Version   string   `json:"version"`
	SHA256    string   `json:"sha256"`
	Languages []string `json:"languages"`
}

// ConfigurePackagedApp 在 packaged owner 进程中注入包内运行时环境。
// 开发模式直接返回；packaged 模式缺少 manifest、sidecar 或 LSP bundle 时立即报错。
func ConfigurePackagedApp() error {
	return configurePackagedApp(systemRuntimeDeps())
}

// configurePackagedApp 使用当前调用持有的系统依赖注入 packaged owner 运行时环境。
func configurePackagedApp(deps runtimeDeps) error {
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
	if err := applyPackagedRuntimeEnv(runtime, deps.setenv); err != nil {
		return fmt.Errorf("configure packaged runtime env: %w", err)
	}
	return nil
}

// ConfigureSidecarRuntime 根据父进程 contract 为 sidecar 注入项目根和打包工具链路径。
func ConfigureSidecarRuntime() error {
	contract, err := ResolveSidecarRuntimeContract(SidecarRuntimeInput{
		Env: environmentMap(os.Environ()),
	})
	if err != nil {
		return err
	}
	return applySidecarRuntimeContract(contract)
}

// ResolveSidecarRuntimeContract 校验 sidecar 必需的 mode 和资源根环境变量。
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

// PackagedRuntimeFromExecutable 为仍依赖旧 PackagedRuntime 结构的调用方解析打包运行时。
// 是否属于 packaged 由 ResolveRuntime 决定，避免只靠路径形态误判。
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

// packagedRuntimeFromResources 使用当前系统平台构造打包资源路径集合。
func packagedRuntimeFromResources(resources, userHome string) PackagedRuntime {
	return packagedRuntimeFromResourcesForOS(runtimeGOOS(), resources, userHome)
}

// packagedAppDataDir 返回当前系统平台的应用自管数据目录。
func packagedAppDataDir(userHome string) string {
	return packagedAppDataDirForOS(runtimeGOOS(), userHome)
}

// applyPackagedEnv 根据资源根和用户目录注入 packaged owner 环境。
func applyPackagedEnv(resources, userHome string) error {
	return applyPackagedRuntimeEnv(packagedRuntimeFromResources(resources, userHome), os.Setenv)
}

// LoadLSPBundleFromEnv 从环境变量加载 LSP bundle；两个变量必须同时存在。
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

// LoadLSPBundle 读取并校验打包 LSP bundle manifest。
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

// normalizeLSPBundle 规范化 server 和 language 映射，并拒绝重复 language 绑定。
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

// resolveLSPBundlePath 清理 manifest 中的相对路径，并阻止路径逃出 LSP bundle。
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

// defaultLSPLanguageSets 返回内置 server 的默认语言集合，调用方得到独立的描述符副本。
func defaultLSPLanguageSets() map[string][]string {
	return map[string][]string{
		"bash-language-server":         {"shellscript"},
		"clangd":                       {"c", "cpp", "objective-c", "objective-cpp"},
		"csharp-ls":                    {"csharp"},
		"dart":                         {"dart"},
		"docker-langserver":            {"dockerfile"},
		"gopls":                        {"go", "gomod", "gosum", "gowork"},
		"graphql-lsp":                  {"graphql"},
		"intelephense":                 {"php"},
		"jdtls":                        {"java"},
		"kotlin-language-server":       {"kotlin"},
		"lua-language-server":          {"lua"},
		"prisma-language-server":       {"prisma"},
		"pyright":                      {"python"},
		"rust-analyzer":                {"rust"},
		"solargraph":                   {"ruby"},
		"sourcekit-lsp":                {"swift"},
		"sqruff":                       {"sql"},
		"svelteserver":                 {"svelte"},
		"terraform-ls":                 {"terraform"},
		"typescript-language-server":   {"javascript", "javascriptreact", "typescript", "typescriptreact"},
		"vscode-langservers-extracted": {"css", "html", "json", "markdown"},
		"vue-language-server":          {"vue"},
		"yaml-language-server":         {"yaml"},
	}
}

// defaultLSPLanguages 为缺少 languages 字段的内置 server 提供默认语言集合。
func defaultLSPLanguages(serverID string) []string {
	return defaultLSPLanguageSets()[normalizeLSPKey(serverID)]
}

// normalizeLSPLanguages 去重、排序并清理 language id，保证映射稳定。
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

// normalizeLSPKey 统一 server 和 language id 的大小写与空白。
func normalizeLSPKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// ServerForLanguage 返回指定 language id 对应的打包 LSP server。
func (b LSPBundle) ServerForLanguage(languageID string) (LSPServer, bool) {
	server, ok := b.Languages[normalizeLSPKey(languageID)]
	return server, ok
}

// SemanticLanguages 返回 bundle 可提供语义能力的语言列表，顺序稳定。
func (b LSPBundle) SemanticLanguages() []string {
	languages := make([]string, 0, len(b.Languages))
	for languageID := range b.Languages {
		languages = append(languages, languageID)
	}
	slices.Sort(languages)
	return languages
}

// applyPackagedRuntimeEnv 校验包内 sidecar 和 LSP 后注入 owner 进程环境变量。
func applyPackagedRuntimeEnv(runtime PackagedRuntime, setenv func(string, string) error) error {
	if err := requireBundledSidecars(runtime.BinDir); err != nil {
		return err
	}
	lspBundle, err := LoadLSPBundle(filepath.Join(runtime.ResourcesDir, lspBundleName), filepath.Join(runtime.ResourcesDir, lspBundleName, lspManifestName))
	if err != nil {
		return err
	}
	return runEnvSetters(
		func() error { return setControlledEnvPath(setenv, "PATH", packagedPathEntries(runtime)...) },
		func() error { return setEnv(setenv, peerBinDirEnv, runtime.BinDir) },
		func() error { return setEnv(setenv, lspBundleDirEnv, lspBundle.BundleDir) },
		func() error { return setEnv(setenv, lspManifestEnv, lspBundle.ManifestPath) },
		func() error { return setEnvIfEmpty(setenv, controlRPCAddrEnv, "127.0.0.1:0") },
		func() error { return setEnvIfEmpty(setenv, httpAddrEnv, "127.0.0.1:0") },
		func() error { return setEnvIfEmpty(setenv, sessionTokenEnv, newSessionToken()) },
		func() error { return setEnv(setenv, projectRootEnv, runtime.ResourcesDir) },
		func() error { return setEnv(setenv, runtimeModeEnv, "packaged") },
		func() error { return setEnv(setenv, runtimeResourcesEnv, runtime.ResourcesDir) },
		func() error { return setEnv(setenv, requireCodexEnv, "1") },
		func() error { return setEnvIfEmpty(setenv, superDolphinHomeEnv, runtime.AppDataDir) },
		func() error {
			return setEnvIfEmpty(setenv, codexHomeEnv, filepath.Join(runtime.AppDataDir, "providers", "codex"))
		},
		func() error { return setEnv(setenv, packagedCodexEnv, "1") },
		func() error {
			return setIfDir(setenv, "GIT_EXEC_PATH", filepath.Join(runtime.ResourcesDir, "libexec", "git-core"))
		},
		func() error {
			return setIfDir(setenv, "GIT_TEMPLATE_DIR", filepath.Join(runtime.ResourcesDir, "share", "git-core", "templates"))
		},
		func() error {
			return setIfFile(setenv, modelRegistryEnv, filepath.Join(runtime.ResourcesDir, modelRegistryBundle))
		},
	)
}

// LoadVideoEnv 从 $SUPER_DOLPHIN_HOME/video.env 加载 KEY=VALUE 到环境变量。
// 文件缺失视为未配置；读取或设置失败会返回错误，避免错误凭据被静默吞掉。
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
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("video.env:%d malformed KEY=VALUE line", lineNo+1)
		}
		key := strings.TrimSpace(k)
		if key == "" {
			return fmt.Errorf("video.env:%d empty key", lineNo+1)
		}
		if err := os.Setenv(key, strings.TrimSpace(v)); err != nil {
			return err
		}
	}
	return nil
}

// WriteVideoEnv 把 SiliconFlow API key 写入应用自管 video.env，文件权限仅允许当前用户读写。
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

// videoEnvPath 返回 video.env 路径；未设置 SUPER_DOLPHIN_HOME 时使用 packaged app data。
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

// applySidecarRuntimeContract 把 owner 传入的 sidecar contract 转成进程环境。
func applySidecarRuntimeContract(contract SidecarRuntimeContract) error {
	setters := []func() error{
		func() error { return setEnvIfEmpty(os.Setenv, projectRootEnv, contract.ResourcesDir) },
	}
	if contract.Mode == "packaged" {
		runtime := packagedRuntimeFromResources(contract.ResourcesDir, "")
		setters = append(setters,
			func() error { return setControlledEnvPath(os.Setenv, "PATH", packagedSidecarPathEntries(runtime)...) },
			func() error { return setEnv(os.Setenv, peerBinDirEnv, runtime.BinDir) },
			func() error {
				return setEnvIfEmpty(os.Setenv, lspBundleDirEnv, filepath.Join(runtime.ResourcesDir, lspBundleName))
			},
			func() error {
				return setEnvIfEmpty(os.Setenv, lspManifestEnv, filepath.Join(runtime.ResourcesDir, lspBundleName, lspManifestName))
			},
		)
	}
	return runEnvSetters(setters...)
}

// runEnvSetters 按顺序执行环境写入，遇到第一处失败立即返回。
func runEnvSetters(setters ...func() error) error {
	for _, set := range setters {
		if err := set(); err != nil {
			return err
		}
	}
	return nil
}

// requireBundledSidecars 校验发行包内所有 peer sidecar 均可执行。
func requireBundledSidecars(binDir string) error {
	for _, name := range executableNamesForOS(runtimeGOOS(), bundledSidecarNames()) {
		path := filepath.Join(binDir, name)
		if err := requireExecutableFile(path); err != nil {
			return fmt.Errorf("missing bundled sidecar %s: %w", path, err)
		}
	}
	return nil
}

// requireExecutableFile 使用当前系统规则校验可执行文件。
func requireExecutableFile(path string) error {
	return requireExecutableFileForOS(runtimeGOOS(), path)
}

// newSessionToken 生成 owner 控制面会话 token；熵源失败属于不可恢复启动错误。
func newSessionToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// archguard:ignore panic_count -- 会话 token 熵源失败时无法安全继续启动。
		panic("generate packaged control-plane session token: " + err.Error())
	}
	return "sd-" + hex.EncodeToString(raw[:])
}

// packagedPathEntries 返回当前系统 owner 进程使用的 PATH 条目。
func packagedPathEntries(runtime PackagedRuntime) []string {
	return packagedPathEntriesForOS(runtimeGOOS(), runtime)
}

// packagedSidecarPathEntries 返回当前系统 sidecar 进程使用的 PATH 条目。
func packagedSidecarPathEntries(runtime PackagedRuntime) []string {
	return packagedSidecarPathEntriesForOS(runtimeGOOS(), runtime)
}

// setControlledEnvPath 用去重后的受控条目覆盖指定 PATH 类环境变量。
func setControlledEnvPath(setenv func(string, string) error, key string, entries ...string) error {
	seen := map[string]bool{}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		appendPathEntry(&out, seen, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return setEnv(setenv, key, strings.Join(out, string(os.PathListSeparator)))
}

// appendPathEntry 去重追加非空路径条目。
func appendPathEntry(out *[]string, seen map[string]bool, entry string) {
	entry = strings.TrimSpace(entry)
	if entry == "" || seen[entry] {
		return
	}
	seen[entry] = true
	*out = append(*out, entry)
}

// setIfDir 仅当目录存在时写入环境变量。
func setIfDir(setenv func(string, string) error, key, dir string) error {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return setEnv(setenv, key, dir)
	}
	return nil
}

// setIfFile 仅当目标文件存在且变量未设置时写入环境变量。
func setIfFile(setenv func(string, string) error, key, path string) error {
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return nil
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return setEnv(setenv, key, path)
	}
	return nil
}

// setEnvIfEmpty 只在变量为空且 value 非空时写入，保留调用方显式配置。
func setEnvIfEmpty(setenv func(string, string) error, key, value string) error {
	if strings.TrimSpace(os.Getenv(key)) != "" || strings.TrimSpace(value) == "" {
		return nil
	}
	return setEnv(setenv, key, value)
}

// setEnv 写入环境变量，并把 key 带入错误便于定位失败项。
func setEnv(setenv func(string, string) error, key, value string) error {
	if err := setenv(key, value); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}
