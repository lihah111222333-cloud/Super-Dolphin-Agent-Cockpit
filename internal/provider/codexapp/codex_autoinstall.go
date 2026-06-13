package codexapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	codexGitHubRepoURL            = "https://github.com/openai/codex"
	codexReleaseAPIURL            = "https://api.github.com/repos/openai/codex/releases/latest"
	codexReleaseAPIURLEnv         = "SUPER_DOLPHIN_CODEX_RELEASE_API_URL"
	codexReleaseSHA256Env         = "SUPER_DOLPHIN_CODEX_RELEASE_SHA256"
	codexTrustedReleaseMirrorEnv  = "SUPER_DOLPHIN_CODEX_TRUSTED_RELEASE_MIRROR"
	codexInstallRootEnv           = "SUPER_DOLPHIN_CODEX_INSTALL_ROOT"
	codexRelayBootstrapTokenEnv   = "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
	requireBundledCodexEnv        = "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX"
	codexValidationTimeout        = 5 * time.Second
	maxCodexDownloadBytes         = 512 << 20
	defaultCodexExtractLimit      = 1 << 30
	defaultBootstrapModelProvider = "super-dolphin-relay"
)

type codexInstallState struct {
	mu            sync.Mutex
	maxFileBytes  int64
	maxTotalBytes int64
}

var codexInstall = &codexInstallState{maxFileBytes: defaultCodexExtractLimit, maxTotalBytes: defaultCodexExtractLimit}

func EnsureCLIAvailable(ctx context.Context) error {
	return ensureCodexCLIAvailable(ctx)
}

type CodexBootstrapConfig struct {
	Home                string
	RelayBaseURL        string
	RelayBootstrapToken string
	ModelProvider       string
}

func EnsureCodexBootstrap(ctx context.Context, cfg CodexBootstrapConfig) error {
	home := strings.TrimSpace(cfg.Home)
	baseURL := strings.TrimSpace(cfg.RelayBaseURL)
	bootstrapToken := strings.TrimSpace(cfg.RelayBootstrapToken)
	provider := strings.TrimSpace(cfg.ModelProvider)
	if provider == "" {
		provider = defaultBootstrapModelProvider
	}
	if err := validateCodexBootstrapConfig(home, baseURL, bootstrapToken); err != nil {
		return err
	}
	ctx = nonNilContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create Codex home: %w", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return fmt.Errorf("set Codex home mode: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	providerName := codexBootstrapProviderName(provider)
	content := strings.Join([]string{
		"model_provider = " + strconv.Quote(provider),
		"",
		"[model_providers." + strconv.Quote(provider) + "]",
		"name = " + strconv.Quote(providerName),
		"base_url = " + strconv.Quote(baseURL),
		"env_key = " + strconv.Quote(codexRelayBootstrapTokenEnv),
		"wire_api = " + strconv.Quote("responses"),
		"",
	}, "\n")
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write Codex config.toml: %w", err)
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		return fmt.Errorf("set Codex config.toml mode: %w", err)
	}
	return nil
}

func validateCodexBootstrapConfig(home, baseURL, bootstrapToken string) error {
	var problems []error
	if home == "" {
		problems = append(problems, errors.New("Codex home is required"))
	}
	if baseURL == "" {
		problems = append(problems, errors.New("Codex relay base URL is required"))
	}
	if bootstrapToken == "" {
		problems = append(problems, errors.New("Codex relay bootstrap token is required"))
	}
	return errors.Join(problems...)
}

func codexBootstrapProviderName(provider string) string {
	return map[bool]string{true: "Super Dolphin Relay", false: provider}[provider == defaultBootstrapModelProvider]
}

type codexGitHubRelease struct {
	TagName string             `json:"tag_name"`
	Assets  []codexGitHubAsset `json:"assets"`
}

type codexGitHubAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

func ensureCodexCLIAvailable(ctx context.Context) error {
	ctx = nonNilContext(ctx)
	ok, err := bundledOrPathCodexAvailable(ctx)
	if err != nil || ok {
		return err
	}
	codexInstall.mu.Lock()
	defer codexInstall.mu.Unlock()
	ok, err = bundledOrPathCodexAvailable(ctx)
	if err != nil || ok {
		return err
	}
	return ensureManagedCodexCLI(ctx)
}

func bundledOrPathCodexAvailable(ctx context.Context) (bool, error) {
	path, err := bundledCodexCLI(ctx)
	if err != nil {
		return false, err
	}
	if path != "" {
		prependDirToPATH(filepath.Dir(path))
		return true, nil
	}
	if bundledCodexRequired() {
		return false, fmt.Errorf(
			"codexapp: bundled Codex CLI is required but was not found in %s=\"%s\"; packaged asset is missing",
			peerBinDirEnv,
			strings.Join(bundledCodexPeerBinDirs(), string(os.PathListSeparator)),
		)
	}
	return codexPathAvailable(ctx), nil
}

func bundledCodexRequired() bool {
	return strings.TrimSpace(os.Getenv(requireBundledCodexEnv)) == "1"
}
func bundledCodexPeerBinDirs() []string {
	dirs := filepath.SplitList(os.Getenv(peerBinDirEnv))
	if bundledCodexRequired() && len(dirs) > 1 {
		return dirs[:1]
	}
	return dirs
}

func ensureManagedCodexCLI(ctx context.Context) error {
	root, err := codexManagedInstallRoot()
	if err != nil {
		return fmt.Errorf("codexapp: resolve managed Codex install root: %w", err)
	}
	checksum, err := codexReleaseChecksum()
	if err != nil {
		return err
	}
	path, err := findManagedCodexBinary(ctx, root, checksum)
	if err != nil {
		return err
	}
	if path != "" {
		prependDirToPATH(filepath.Dir(path))
		return nil
	}
	path, err = installManagedCodexCLI(ctx, root, checksum)
	if err != nil {
		return fmt.Errorf(
			"codexapp: codex CLI not found in PATH and automatic install from official OpenAI GitHub failed (%s): %w",
			codexGitHubRepoURL,
			err,
		)
	}
	prependDirToPATH(filepath.Dir(path))
	return nil
}

func bundledCodexCLI(ctx context.Context) (string, error) {
	for _, dir := range bundledCodexPeerBinDirs() {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, codexExecutableFileName())
		info, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("codexapp: inspect bundled Codex CLI %s: %w", candidate, err)
		}
		if info.IsDir() || !isExecutable(candidate) {
			return "", fmt.Errorf("codexapp: bundled Codex CLI is not executable: %s; packaged asset is damaged", candidate)
		}
		if err := verifyBundledCodexInstall(ctx, candidate); err != nil {
			return "", fmt.Errorf("codexapp: %w; packaged asset is damaged", err)
		}
		return candidate, nil
	}
	return "", nil
}

func codexPathAvailable(ctx context.Context) bool {
	path, err := exec.LookPath(codexBinaryName)
	return err == nil && validCodexCLI(ctx, path)
}

func validCodexCLI(ctx context.Context, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || !isExecutable(path) {
		return false
	}
	ctx = nonNilContext(ctx)
	checkCtx, cancel := withTimeout(context.WithoutCancel(ctx), codexValidationTimeout)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, path, codexAppServerCommand, "--help")
	cmd.Env = contract.ScrubDatabaseEnv(os.Environ())
	setCodexProcessAttrs(cmd)
	return cmd.Run() == nil
}
func codexManagedInstallRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv(codexInstallRootEnv)); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("%s must be an absolute path", codexInstallRootEnv)
		}
		return filepath.Clean(root), nil
	}
	home, err := providershared.AppManagedProviderHome(providershared.ProviderCodex)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "runtime", "openai-codex"), nil
}

func findManagedCodexBinary(ctx context.Context, root, sourceSHA256 string) (string, error) {
	entries, err := readManagedCodexInstallRoot(root)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return compareCodexInstallNames(names[i], names[j]) > 0
	})
	for _, name := range names {
		target := filepath.Join(root, name)
		path := filepath.Join(target, "codex_cli_bin", "bin", codexExecutableFileName())
		if !isExecutable(path) {
			continue
		}
		path, err := verifyManagedCodexInstall(ctx, target, sourceSHA256)
		if err != nil || path != "" {
			return path, err
		}
	}
	return "", nil
}
func readManagedCodexInstallRoot(root string) ([]os.DirEntry, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read managed Codex install root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("read managed Codex install root %s: not a directory", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read managed Codex install root %s: %w", root, err)
	}
	return entries, nil
}
func installManagedCodexCLI(ctx context.Context, root, checksum string) (string, error) {
	release, err := fetchCodexRelease(ctx)
	if err != nil {
		return "", err
	}
	asset, err := selectCodexReleaseAsset(release.Assets)
	if err != nil {
		return "", err
	}
	target, codexPath, err := managedCodexInstallTarget(root, release.TagName)
	if err != nil {
		return "", err
	}
	if isExecutable(codexPath) {
		return verifyManagedCodexInstall(ctx, target, checksum)
	}
	workDir, err := prepareCodexInstallWorkDir(root)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	if err := installCodexAsset(ctx, asset, checksum, workDir, target); err != nil {
		return "", err
	}
	if !isExecutable(codexPath) {
		return "", fmt.Errorf("installed Codex binary is not executable: %s", codexPath)
	}
	if err := writeManagedCodexManifest(target, release.TagName, asset.Name, checksum, codexPath); err != nil {
		return "", err
	}
	path, err := verifyManagedCodexInstall(ctx, target, checksum)
	if err != nil {
		return "", err
	}
	pkglogger.Info("codexapp: installed Codex CLI from official GitHub release", "tag", release.TagName, "asset", asset.Name, "path", path)
	return path, nil
}

func codexReleaseChecksum() (string, error) {
	checksum := strings.ToLower(strings.TrimSpace(os.Getenv(codexReleaseSHA256Env)))
	if checksum == "" {
		return "", fmt.Errorf("Codex fallback download requires pinned SHA-256 checksum; set %s", codexReleaseSHA256Env)
	}
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("%s must be a 64-character hex SHA-256 checksum", codexReleaseSHA256Env)
	}
	return checksum, nil
}

func managedCodexInstallTarget(root, tagName string) (string, string, error) {
	tag := sanitizeCodexReleaseTag(tagName)
	if tag == "" {
		return "", "", errors.New("release tag_name is empty")
	}
	target := filepath.Join(root, tag)
	codexPath := filepath.Join(target, "codex_cli_bin", "bin", codexExecutableFileName())
	return target, codexPath, nil
}

func prepareCodexInstallWorkDir(root string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create Codex install root: %w", err)
	}
	workDir, err := os.MkdirTemp(root, ".install-*")
	if err != nil {
		return "", fmt.Errorf("create Codex install temp dir: %w", err)
	}
	return workDir, nil
}

func installCodexAsset(ctx context.Context, asset codexGitHubAsset, checksum, workDir, target string) error {
	name := strings.TrimSpace(asset.Name)
	switch {
	case strings.HasSuffix(name, ".whl"):
		return installCodexWheel(ctx, asset.DownloadURL, checksum, workDir, target)
	case strings.HasSuffix(name, ".tar.gz"):
		return installCodexTarGz(ctx, asset.DownloadURL, checksum, workDir, target)
	default:
		return fmt.Errorf("unsupported Codex release asset format: %s", name)
	}
}

func installCodexWheel(ctx context.Context, downloadURL, checksum, workDir, target string) error {
	return installCodexArchive(ctx, downloadURL, checksum, workDir, target, "codex.whl", extractCodexWheel)
}

func installCodexTarGz(ctx context.Context, downloadURL, checksum, workDir, target string) error {
	return installCodexArchive(ctx, downloadURL, checksum, workDir, target, "codex.tar.gz", extractCodexTarGz)
}

func installCodexArchive(ctx context.Context, downloadURL, checksum, workDir, target, archiveName string, extract func(string, string) error) error {
	archivePath := filepath.Join(workDir, archiveName)
	if err := downloadCodexAsset(ctx, downloadURL, checksum, archivePath); err != nil {
		return err
	}
	extractDir := filepath.Join(workDir, "extract")
	if err := extract(archivePath, extractDir); err != nil {
		return err
	}
	codexPath := filepath.Join(target, "codex_cli_bin", "bin", codexExecutableFileName())
	if err := ensureCodexInstallLayout(extractDir); err != nil {
		return err
	}
	return promoteCodexInstall(extractDir, target, codexPath)
}

func promoteCodexInstall(extractDir, target, codexPath string) error {
	if isExecutable(codexPath) {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("remove incomplete Codex install target: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect Codex install target: %w", err)
	}
	if err := os.Rename(extractDir, target); err != nil {
		return fmt.Errorf("install Codex release: %w", err)
	}
	return nil
}

func fetchCodexRelease(ctx context.Context) (codexGitHubRelease, error) {
	releaseURL, err := codexReleaseAPIRequestURL()
	if err != nil {
		return codexGitHubRelease{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return codexGitHubRelease{}, fmt.Errorf("build Codex release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Super-Dolphin")
	resp, err := codexHTTPClient().Do(req)
	if err != nil {
		return codexGitHubRelease{}, fmt.Errorf("fetch Codex latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return codexGitHubRelease{}, fmt.Errorf("fetch Codex latest release: unexpected HTTP status %s", resp.Status)
	}
	var release codexGitHubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&release); err != nil {
		return codexGitHubRelease{}, fmt.Errorf("decode Codex latest release: %w", err)
	}
	return release, nil
}

func selectCodexReleaseAsset(assets []codexGitHubAsset) (codexGitHubAsset, error) {
	candidates := codexReleaseAssetCandidates()
	if len(candidates) == 0 {
		return codexGitHubAsset{}, fmt.Errorf("unsupported Codex auto-install platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	for _, candidate := range candidates {
		for _, asset := range assets {
			if candidate(strings.TrimSpace(asset.Name)) && strings.TrimSpace(asset.DownloadURL) != "" {
				return asset, nil
			}
		}
	}
	return codexGitHubAsset{}, fmt.Errorf("no compatible OpenAI Codex release asset for %s/%s in %s", runtime.GOOS, runtime.GOARCH, codexGitHubRepoURL)
}

func codexReleaseAssetCandidates() []func(string) bool {
	var out []func(string) bool
	if target, err := codexRustReleaseTarget(); err == nil {
		for _, exact := range []string{
			"codex-package-" + target + ".tar.gz",
			"codex-" + target + ".tar.gz",
		} {
			name := exact
			out = append(out, func(assetName string) bool { return assetName == name })
		}
	}
	if platform, err := codexWheelReleasePlatform(); err == nil {
		out = append(out, func(name string) bool {
			return strings.HasPrefix(name, "openai_codex_cli_bin-") &&
				strings.HasSuffix(name, ".whl") &&
				strings.Contains(name, platform)
		})
	}
	return out
}

func codexRustReleaseTarget() (string, error) {
	return codexSupportedPlatformValue(map[string]string{
		"darwin/arm64":  "aarch64-apple-darwin",
		"darwin/amd64":  "x86_64-apple-darwin",
		"linux/arm64":   "aarch64-unknown-linux-musl",
		"linux/amd64":   "x86_64-unknown-linux-musl",
		"windows/arm64": "aarch64-pc-windows-msvc",
		"windows/amd64": "x86_64-pc-windows-msvc",
	})
}

func codexWheelReleasePlatform() (string, error) {
	return codexSupportedPlatformValue(map[string]string{
		"darwin/arm64":  "macosx_11_0_arm64",
		"darwin/amd64":  "macosx_10_9_x86_64",
		"linux/arm64":   "manylinux_2_17_aarch64",
		"linux/amd64":   "manylinux_2_17_x86_64",
		"windows/arm64": "win_arm64",
		"windows/amd64": "win_amd64",
	})
}

func codexSupportedPlatformValue(values map[string]string) (string, error) {
	if value := values[runtime.GOOS+"/"+runtime.GOARCH]; value != "" {
		return value, nil
	}
	return "", fmt.Errorf("unsupported Codex auto-install platform %s/%s", runtime.GOOS, runtime.GOARCH)
}

func isExecutable(path string) bool {
	info, err := os.Stat(filepath.Clean(path))
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		header := make([]byte, 2)
		f, err := os.Open(filepath.Clean(path))
		if err != nil {
			return false
		}
		defer f.Close()
		if _, err := io.ReadFull(f, header); err != nil {
			return false
		}
		return string(header) == "MZ"
	}
	return info.Mode().Perm()&0o111 != 0
}

func codexExecutableFileName() string {
	return codexExecutableFileNameFor(codexBinaryName)
}

func codexExecutableFileNameFor(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func prependDirToPATH(dir string) {
	dir = strings.TrimSpace(filepath.Clean(dir))
	if dir == "" {
		return
	}
	parts := filepath.SplitList(os.Getenv("PATH"))
	for _, part := range parts {
		if filepath.Clean(part) == dir {
			return
		}
	}
	newPath := dir
	if oldPath := os.Getenv("PATH"); strings.TrimSpace(oldPath) != "" {
		newPath += string(os.PathListSeparator) + oldPath
	}
	_ = os.Setenv("PATH", newPath)
}

func sanitizeCodexReleaseTag(tag string) string {
	tag = strings.TrimSpace(tag)
	var b strings.Builder
	for _, r := range tag {
		if isCodexReleaseTagRune(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

func isCodexReleaseTagRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_'
}

func codexHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute}
}
