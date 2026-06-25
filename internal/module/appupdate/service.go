package appupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	appID = "super-dolphin"

	envUpdateEnabled       = "SUPER_DOLPHIN_UPDATE_ENABLED"
	envUpdateManifestURL   = "SUPER_DOLPHIN_UPDATE_MANIFEST_URL"
	envUpdateGitHubRepo    = "SUPER_DOLPHIN_UPDATE_GITHUB_REPO"
	envUpdatePublicKey     = "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"
	envUpdateChannel       = "SUPER_DOLPHIN_UPDATE_CHANNEL"
	envUpdateStageDir      = "SUPER_DOLPHIN_UPDATE_STAGE_DIR"
	envUpdateHelperPath    = "SUPER_DOLPHIN_UPDATE_HELPER_PATH"
	envUpdateTargetApp     = "SUPER_DOLPHIN_UPDATE_TARGET_APP_PATH"
	envUpdatePlatform      = "SUPER_DOLPHIN_UPDATE_PLATFORM"
	envUpdateVersion       = "SUPER_DOLPHIN_UPDATE_VERSION"
	envUpdateAllowUnsigned = "SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED"
	envRuntimeResources    = "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"
	envSuperDolphinHome    = "SUPER_DOLPHIN_HOME"
	envVersion             = "VERSION"

	selectedUpdateFilename = "selected-update.json"
	dmgFilename            = "Super-Dolphin-update.dmg"
	exeFilename            = "Super-Dolphin-update.exe"
	helperLogFilename      = "super-dolphin-updater.log"
	updaterHelperName      = "super-dolphin-updater"
	installQuitDelay       = 250 * time.Millisecond
)

type Config struct {
	Enabled        bool
	ManifestURL    string
	GitHubRepo     string
	PublicKey      []byte
	Channel        string
	StageDir       string
	HelperPath     string
	TargetAppPath  string
	Platform       string
	CurrentVersion string
	AllowUnsigned  bool
}

type RequestQuit func()

type Service interface {
	Check(context.Context) (CheckResult, error)
	Download(context.Context) (DownloadResult, error)
	Install(context.Context) (InstallResult, error)
	InstallLatest(context.Context) (InstallResult, error)
}

type CheckResult struct {
	Enabled   bool            `json:"enabled"`
	Available bool            `json:"available"`
	Version   string          `json:"version,omitempty"`
	Artifact  *UpdateArtifact `json:"artifact,omitempty"`
}

type DownloadResult struct {
	StagedManifestPath string `json:"stagedManifestPath"`
	ArtifactPath       string `json:"artifactPath"`
	DMGPath            string `json:"dmgPath"`
	Version            string `json:"version"`
	SHA256             string `json:"sha256"`
	Size               int64  `json:"size"`
}

type InstallResult struct {
	Started bool   `json:"started"`
	Helper  string `json:"helper"`
}

type selectedUpdate struct {
	Payload      ManifestPayload `json:"payload"`
	Artifact     UpdateArtifact  `json:"artifact"`
	ArtifactPath string          `json:"artifact_path,omitempty"`
	DMGPath      string          `json:"dmg_path"`
	DownloadedAt string          `json:"downloaded_at"`
}

// service 是 Service 接口的实现，封装 HTTP 客户端和更新配置。
type service struct {
	cfg         Config
	httpClient  *http.Client
	requestQuit RequestQuit
}

// ProvideConfig 提供配置。
func ProvideConfig(_ *platformconfig.Config) (Config, error) {
	enabled := envTruthy(os.Getenv(envUpdateEnabled))
	if !enabled {
		return Config{}, nil
	}
	cfg := Config{
		Enabled:        true,
		ManifestURL:    strings.TrimSpace(os.Getenv(envUpdateManifestURL)),
		GitHubRepo:     strings.TrimSpace(os.Getenv(envUpdateGitHubRepo)),
		Channel:        strings.TrimSpace(os.Getenv(envUpdateChannel)),
		StageDir:       strings.TrimSpace(os.Getenv(envUpdateStageDir)),
		HelperPath:     strings.TrimSpace(os.Getenv(envUpdateHelperPath)),
		TargetAppPath:  strings.TrimSpace(os.Getenv(envUpdateTargetApp)),
		Platform:       strings.TrimSpace(os.Getenv(envUpdatePlatform)),
		CurrentVersion: strings.TrimSpace(os.Getenv(envVersion)),
		AllowUnsigned:  envTruthy(os.Getenv(envUpdateAllowUnsigned)),
	}
	if cfg.Channel == "" {
		cfg.Channel = "gray"
	}
	if cfg.Platform == "" {
		cfg.Platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	if cfg.CurrentVersion == "" {
		cfg.CurrentVersion = strings.TrimSpace(os.Getenv(envUpdateVersion))
	}
	applyPackagedDefaults(&cfg)
	if cfg.CurrentVersion == "" && cfg.TargetAppPath != "" {
		version, err := currentVersionFromInfoPlist(cfg.TargetAppPath)
		if err != nil {
			return Config{}, err
		}
		cfg.CurrentVersion = version
	}
	publicKey, err := decodePublicKey(os.Getenv(envUpdatePublicKey))
	if err != nil {
		return Config{}, err
	}
	cfg.PublicKey = publicKey
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyPackagedDefaults 应用packageddefaults。
func applyPackagedDefaults(cfg *Config) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	if cfg.StageDir == "" {
		if home := strings.TrimSpace(os.Getenv(envSuperDolphinHome)); home != "" {
			cfg.StageDir = filepath.Join(home, "updates")
		}
	}
	resources := strings.TrimSpace(os.Getenv(envRuntimeResources))
	if resources == "" {
		return
	}
	if cfg.HelperPath == "" {
		cfg.HelperPath = filepath.Join(resources, "bin", updaterHelperName)
	}
	if cfg.TargetAppPath == "" {
		cfg.TargetAppPath = appPathFromResourcesDir(resources)
	}
}

// appPathFromResourcesDir 从 Resources 目录向上推导 .app 路径，不符合结构时返回空串。
func appPathFromResourcesDir(resources string) string {
	resources = filepath.Clean(strings.TrimSpace(resources))
	if filepath.Base(resources) != "Resources" {
		return ""
	}
	contents := filepath.Dir(resources)
	if filepath.Base(contents) != "Contents" {
		return ""
	}
	app := filepath.Dir(contents)
	if !strings.EqualFold(filepath.Ext(app), ".app") {
		return ""
	}
	return app
}

// NewService 创建服务。
func NewService(p serviceParams) Service {
	return newService(p.Config, http.DefaultClient, p.RequestQuit)
}

// newService 用于测试：注入自定义 httpClient 和 requestQuit。
func newService(cfg Config, client *http.Client, requestQuit RequestQuit) *service {
	if client == nil {
		client = http.DefaultClient
	}
	return &service{cfg: cfg, httpClient: client, requestQuit: requestQuit}
}

// Check 处理check。
func (s *service) Check(ctx context.Context) (CheckResult, error) {
	if !s.cfg.Enabled {
		return CheckResult{Enabled: false, Available: false}, nil
	}
	payload, artifact, err := s.fetchManifest(ctx)
	if errors.Is(err, ErrNoUpdate) {
		return CheckResult{Enabled: true, Available: false}, nil
	}
	if err != nil {
		return CheckResult{}, err
	}
	return CheckResult{
		Enabled:   true,
		Available: true,
		Version:   payload.Version,
		Artifact:  &artifact,
	}, nil
}

// Download 处理download。
func (s *service) Download(ctx context.Context) (DownloadResult, error) {
	payload, artifact, err := s.fetchManifest(ctx)
	if err != nil {
		return DownloadResult{}, err
	}
	if err := os.MkdirAll(s.cfg.StageDir, 0o700); err != nil {
		return DownloadResult{}, fmt.Errorf("create app update stage dir: %w", err)
	}
	artifactPath, err := stagedArtifactPathFor(s.cfg.StageDir, artifact)
	if err != nil {
		return DownloadResult{}, err
	}
	if err := s.downloadArtifact(ctx, artifact, artifactPath); err != nil {
		return DownloadResult{}, err
	}
	staged := selectedUpdate{
		Payload:      payload,
		Artifact:     artifact,
		ArtifactPath: artifactPath,
		DownloadedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if updatePlatformOS(artifact.Platform) == "darwin" {
		staged.DMGPath = artifactPath
	}
	stagedPath := s.stagedManifestPath()
	if err := writeSelectedUpdate(stagedPath, staged); err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{
		StagedManifestPath: stagedPath,
		ArtifactPath:       artifactPath,
		DMGPath:            staged.DMGPath,
		Version:            payload.Version,
		SHA256:             artifact.SHA256,
		Size:               artifact.Size,
	}, nil
}

// Install 处理安装。
func (s *service) Install(ctx context.Context) (InstallResult, error) {
	_ = ctx
	if s.requestQuit == nil {
		return InstallResult{}, errors.New("app update request quit callback is not configured")
	}
	staged, err := readSelectedUpdate(s.stagedManifestPath())
	if err != nil {
		return InstallResult{}, err
	}
	if err := validateStagedUpdate(staged); err != nil {
		return InstallResult{}, err
	}
	cmd, helper, err := s.installCommand(staged)
	if err != nil {
		return InstallResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return InstallResult{}, fmt.Errorf("start app update helper: %w", err)
	}
	if cmd.Process == nil {
		return InstallResult{}, errors.New("start app update helper: process is nil")
	}
	if err := cmd.Process.Release(); err != nil {
		return InstallResult{}, fmt.Errorf("release app update helper: %w", err)
	}
	s.scheduleRequestQuit()
	return InstallResult{Started: true, Helper: helper}, nil
}

// InstallLatest 处理安装latest。
func (s *service) InstallLatest(ctx context.Context) (InstallResult, error) {
	if _, err := s.Download(ctx); err != nil {
		return InstallResult{}, err
	}
	return s.Install(ctx)
}

// scheduleRequestQuit 在短暂延迟后调用 requestQuit，让 RPC 响应先行发出。
func (s *service) scheduleRequestQuit() {
	quit := s.requestQuit
	safego.Go(context.Background(), pkglogger.Get(), "appupdate.scheduleRequestQuit", func(context.Context) {
		// Let the RPC bridge flush InstallResult before the app closes.
		time.Sleep(installQuitDelay)
		quit()
	})
}

// fetchManifest 处理fetchmanifest。
func (s *service) fetchManifest(ctx context.Context) (ManifestPayload, UpdateArtifact, error) {
	if !s.cfg.Enabled {
		return ManifestPayload{}, UpdateArtifact{}, ErrNoUpdate
	}
	if strings.TrimSpace(s.cfg.GitHubRepo) != "" {
		return s.fetchGitHubLatestManifest(ctx)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.ManifestURL, nil)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, fmt.Errorf("create app update manifest request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, fmt.Errorf("fetch app update manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ManifestPayload{}, UpdateArtifact{}, fmt.Errorf("fetch app update manifest: status %s", resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, fmt.Errorf("read app update manifest: %w", err)
	}
	return VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      s.cfg.PublicKey,
		AppID:          appID,
		Channel:        s.cfg.Channel,
		Platform:       s.cfg.Platform,
		CurrentVersion: s.cfg.CurrentVersion,
	})
}

// downloadArtifact 处理download产物。
func (s *service) downloadArtifact(ctx context.Context, artifact UpdateArtifact, targetPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return fmt.Errorf("create app update artifact request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download app update artifact: %w", err)
	}
	defer resp.Body.Close()
	if err := requireSuccessStatus("download app update artifact", resp); err != nil {
		return err
	}
	tmpPath := targetPath + ".tmp"
	if err := writeVerifiedArtifact(tmpPath, resp.Body, artifact); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("stage app update artifact: %w", err)
	}
	return nil
}

// requireSuccessStatus 检查 HTTP 响应状态码是否为 2xx，否则返回描述性错误。
func requireSuccessStatus(operation string, resp *http.Response) error {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: status %s", operation, resp.Status)
	}
	return nil
}

// writeVerifiedArtifact 写入verified产物。
func writeVerifiedArtifact(tmpPath string, body io.Reader, artifact UpdateArtifact) error {
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create app update artifact file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hash), body)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("write app update artifact: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close app update artifact: %w", closeErr)
	}
	if written != artifact.Size {
		return fmt.Errorf("app update artifact size = %d, want %d", written, artifact.Size)
	}
	actualSHA := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualSHA, artifact.SHA256) {
		return fmt.Errorf("app update artifact sha256 = %s, want %s", actualSHA, artifact.SHA256)
	}
	return nil
}

// stagedManifestPath 返回 staged manifest JSON 的完整路径。
func (s *service) stagedManifestPath() string {
	return filepath.Join(s.cfg.StageDir, selectedUpdateFilename)
}

// helperLogPath 返回 updater helper 日志文件的完整路径。
func (s *service) helperLogPath() string {
	return filepath.Join(s.cfg.StageDir, helperLogFilename)
}

// installCommand 按平台构造安装命令，macOS 使用 helper，Windows 直接运行安装包。
func (s *service) installCommand(staged selectedUpdate) (*exec.Cmd, string, error) {
	artifactPath := selectedArtifactPath(staged)
	switch updatePlatformOS(staged.Artifact.Platform) {
	case "darwin":
		args := []string{"-dmg", artifactPath, "-target", s.cfg.TargetAppPath, "-restart", "-wait-pid", strconv.Itoa(os.Getpid()), "-log", s.helperLogPath()}
		if s.cfg.AllowUnsigned {
			args = append(args, "-allow-unsigned")
		}
		return detachedHelperCommand(s.helperLogPath(), s.cfg.HelperPath, args), s.cfg.HelperPath, nil
	case "windows":
		return exec.Command(artifactPath, "/S"), artifactPath, nil
	default:
		return nil, "", fmt.Errorf("unsupported app update platform %q", staged.Artifact.Platform)
	}
}

// detachedHelperCommand 构造通过 nohup 脱离终端运行 helper 的 shell 命令。
func detachedHelperCommand(logPath string, helperPath string, args []string) *exec.Cmd {
	script := `log_path=$1; shift; nohup "$@" >"$log_path" 2>&1 &`
	shellArgs := []string{"-c", script, "super-dolphin-updater-launcher", logPath, helperPath}
	shellArgs = append(shellArgs, args...)
	return exec.Command("/bin/sh", shellArgs...)
}

// validateConfig 校验已启用的更新配置完整性。
func validateConfig(cfg Config) error {
	if !cfg.Enabled {
		return nil
	}
	if err := validateUpdateSourceConfig(cfg); err != nil {
		return err
	}
	if len(cfg.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("app update public key length = %d, want %d", len(cfg.PublicKey), ed25519.PublicKeySize)
	}
	required, err := requiredUpdateConfigValues(cfg)
	if err != nil {
		return err
	}
	return requireConfigValues(required)
}

// validateUpdateSourceConfig 校验更新源配置（GitHub Repo 或 ManifestURL 二选一）。
func validateUpdateSourceConfig(cfg Config) error {
	if cfg.ManifestURL == "" && cfg.GitHubRepo == "" {
		return fmt.Errorf("%s or %s is required when app update is enabled", envUpdateGitHubRepo, envUpdateManifestURL)
	}
	if err := validateLegacyManifestURL(cfg.ManifestURL); err != nil {
		return err
	}
	if cfg.GitHubRepo == "" {
		return nil
	}
	return validateGitHubRepo(cfg.GitHubRepo)
}

// validateLegacyManifestURL 校验直连 manifest URL 必须为 HTTPS。
func validateLegacyManifestURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	manifestURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse app update manifest URL: %w", err)
	}
	if manifestURL.Scheme != "https" || manifestURL.Host == "" {
		return fmt.Errorf("app update manifest URL must be HTTPS with host: %q", rawURL)
	}
	return nil
}

// requiredUpdateConfigValues 根据平台收集必填配置项，不支持的平台返回错误。
func requiredUpdateConfigValues(cfg Config) (map[string]string, error) {
	required := map[string]string{
		envUpdateChannel:  cfg.Channel,
		envUpdateStageDir: cfg.StageDir,
		envUpdatePlatform: cfg.Platform,
		envVersion:        cfg.CurrentVersion,
	}
	switch updatePlatformOS(cfg.Platform) {
	case "darwin":
		required[envUpdateHelperPath] = cfg.HelperPath
		required[envUpdateTargetApp] = cfg.TargetAppPath
	case "windows":
	default:
		return nil, fmt.Errorf("unsupported app update platform %q", cfg.Platform)
	}
	return required, nil
}

// requireConfigValues 校验所有必填配置项不为空。
func requireConfigValues(required map[string]string) error {
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required when app update is enabled", name)
		}
	}
	return nil
}

// decodePublicKey 解码 base64 编码的 ed25519 公钥。
func decodePublicKey(raw string) ([]byte, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("%s is required when app update is enabled", envUpdatePublicKey)
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", envUpdatePublicKey, err)
	}
	return key, nil
}

// writeSelectedUpdate 将已选更新信息序列化写入 staged manifest 文件。
func writeSelectedUpdate(path string, staged selectedUpdate) error {
	raw, err := json.MarshalIndent(staged, "", "  ")
	if err != nil {
		return fmt.Errorf("encode selected app update: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write selected app update: %w", err)
	}
	return nil
}

// readSelectedUpdate 从 staged manifest 文件反序列化已选更新信息。
func readSelectedUpdate(path string) (selectedUpdate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return selectedUpdate{}, fmt.Errorf("read selected app update: %w", err)
	}
	var staged selectedUpdate
	if err := json.Unmarshal(raw, &staged); err != nil {
		return selectedUpdate{}, fmt.Errorf("decode selected app update: %w", err)
	}
	return staged, nil
}

// validateStagedUpdate 校验已下载的更新产物是否存在且 artifact 元数据合法。
func validateStagedUpdate(staged selectedUpdate) error {
	artifactPath := selectedArtifactPath(staged)
	if strings.TrimSpace(artifactPath) == "" {
		return errors.New("selected app update artifact_path is required")
	}
	if info, err := os.Stat(artifactPath); err != nil {
		return fmt.Errorf("selected app update artifact is not available: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("selected app update artifact must be a file: %s", artifactPath)
	}
	if err := validateArtifact(staged.Artifact); err != nil {
		return err
	}
	return nil
}

// selectedArtifactPath 优先返回 ArtifactPath，为空时回退到 DMGPath。
func selectedArtifactPath(staged selectedUpdate) string {
	if strings.TrimSpace(staged.ArtifactPath) != "" {
		return staged.ArtifactPath
	}
	return staged.DMGPath
}

// stagedArtifactPathFor 按平台返回下载产物的目标路径（dmg/exe），不支持的平台返回错误。
func stagedArtifactPathFor(stageDir string, artifact UpdateArtifact) (string, error) {
	switch updatePlatformOS(artifact.Platform) {
	case "darwin":
		return filepath.Join(stageDir, dmgFilename), nil
	case "windows":
		return filepath.Join(stageDir, exeFilename), nil
	default:
		return "", fmt.Errorf("unsupported app update platform %q", artifact.Platform)
	}
}

// updatePlatformOS 从 platform 字符串（如 darwin-amd64）提取 OS 名称。
func updatePlatformOS(platform string) string {
	osName, _, ok := strings.Cut(strings.TrimSpace(platform), "-")
	if !ok {
		return ""
	}
	return osName
}

// envTruthy 判断环境变量字符串是否表示"真值"（1/true/yes/on）。
func envTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// currentVersionFromInfoPlist 从 macOS .app bundle 的 Info.plist 读取版本号。
func currentVersionFromInfoPlist(targetAppPath string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(targetAppPath, "Contents", "Info.plist"))
	if err != nil {
		return "", fmt.Errorf("read app update Info.plist version: %w", err)
	}
	version, err := plistStringValue(string(raw), "CFBundleShortVersionString")
	if err != nil {
		return "", fmt.Errorf("read app update Info.plist version: %w", err)
	}
	return version, nil
}

// plistStringValue 处理pliststring值。
func plistStringValue(raw, key string) (string, error) {
	keyToken := "<key>" + key + "</key>"
	keyIndex := strings.Index(raw, keyToken)
	if keyIndex < 0 {
		return "", fmt.Errorf("missing %s", key)
	}
	afterKey := raw[keyIndex+len(keyToken):]
	start := strings.Index(afterKey, "<string>")
	end := strings.Index(afterKey, "</string>")
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("missing string value for %s", key)
	}
	value := strings.TrimSpace(afterKey[start+len("<string>") : end])
	if value == "" {
		return "", fmt.Errorf("empty string value for %s", key)
	}
	return value, nil
}
