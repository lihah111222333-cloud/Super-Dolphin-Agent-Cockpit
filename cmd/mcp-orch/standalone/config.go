package standalone

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// Config 是独立服务的完整启动输入；不从 V3 环境或个人 CLI 配置补缺。
type Config struct {
	SchemaVersion  int        `json:"schema_version"`
	WorkspaceRoot  string     `json:"workspace_root"`
	StateDir       string     `json:"state_dir"`
	Listen         string     `json:"listen"`
	CredentialFile string     `json:"credential_file"`
	PrincipalRef   string     `json:"principal_ref"`
	ScopeRef       string     `json:"scope_ref"`
	Providers      []Provider `json:"providers"`
}

// Provider 声明官方 CLI 的显式执行路径和模型范围；配置存在不等于能力已验证。
type Provider struct {
	Name       string   `json:"name"`
	Executable string   `json:"executable"`
	Home       string   `json:"home"`
	Models     []string `json:"models"`
	Sandbox    string   `json:"sandbox,omitempty"`
	AuthMode   string   `json:"auth_mode,omitempty"`
}

const (
	// ClaudeAuthProviderHome 保持 Claude 配置与 provider home 隔离。
	ClaudeAuthProviderHome = "provider_home"
	// ClaudeAuthHostDefault 明确允许 Claude 使用宿主默认 keychain/config。
	ClaudeAuthHostDefault = "host_default"
)

const maxConfigBytes = 64 * 1024

// LoadConfig 有界读取并严格解码配置；不创建目录、不启动 CLI、不读取凭据正文。
func LoadConfig(path string) (cfg Config, err error) {
	if !filepath.IsAbs(path) {
		return cfg, errors.New("orch config path must be absolute")
	}
	file, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("open orch config: %w", securefs.WrapErrorForPath(err, path))
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return cfg, errors.Join(errors.New("orch config must be a regular file"), err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return cfg, fmt.Errorf("read orch config: %w", securefs.WrapErrorForPath(err, path))
	}
	if len(raw) > maxConfigBytes {
		return cfg, errors.New("orch config exceeds 64 KiB")
	}
	return ParseConfig(raw)
}

// ParseConfig 拒绝重复/未知字段、空值和尾随 JSON，再校验路径及显式策略。
func ParseConfig(raw []byte) (Config, error) {
	var cfg Config
	if len(raw) > maxConfigBytes {
		return cfg, errors.New("orch config exceeds 64 KiB")
	}
	if err := validateConfigJSON(raw); err != nil {
		return cfg, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, errors.New("orch config fields have invalid types")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate 校验显式配置；provider probe、credential 内容和锁由各自 owner 后续检查。
func (c Config) Validate() error {
	if c.SchemaVersion != 1 {
		return errors.New("orch config schema_version must be 1")
	}
	if err := c.ValidateServeIdentity(); err != nil {
		return err
	}
	for name, path := range map[string]string{"workspace_root": c.WorkspaceRoot, "state_dir": c.StateDir} {
		if err := requireAbsoluteDirectory(name, path); err != nil {
			return err
		}
	}
	if err := validateListen(c.Listen); err != nil {
		return err
	}
	if !filepath.IsAbs(c.CredentialFile) {
		return errors.New("orch credential_file must be absolute")
	}
	return c.validateProviders()
}

// validateProviders 校验非空、唯一且各自完整的 provider 配置。
func (c Config) validateProviders() error {
	if len(c.Providers) == 0 {
		return errors.New("orch providers must not be empty")
	}
	seen := make(map[string]struct{}, len(c.Providers))
	for _, provider := range c.Providers {
		if _, duplicate := seen[provider.Name]; duplicate {
			return errors.New("orch provider name is duplicated")
		}
		seen[provider.Name] = struct{}{}
		if err := provider.validate(); err != nil {
			return err
		}
	}
	return nil
}

// ValidateServeIdentity 是文件配置和真实 serve 的身份屏障。
// principal/scope 只接受有界 canonical 文本，不能留给凭据、路径或客户端 metadata 推导。
func (c Config) ValidateServeIdentity() error {
	if err := validateCanonicalRef("principal_ref", c.PrincipalRef); err != nil {
		return err
	}
	return validateCanonicalRef("scope_ref", c.ScopeRef)
}

// validateCanonicalRef 拒绝缺失、隐式 trim、NUL、非法 UTF-8 和超长身份引用。
func validateCanonicalRef(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 512 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("orch %s must be bounded canonical text", name)
	}
	return nil
}

// validateListen 限制独立本机接入只绑定 IP 字面量 loopback，不解析可漂移的主机名。
func validateListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("orch listen must be a loopback IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("orch listen must use a loopback IP literal")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("orch listen port is invalid")
	}
	return nil
}

// validate 检查 provider 声明，绝不以 shell、PATH 或其他 provider 作为兜底。
func (p Provider) validate() error {
	if p.Name != "codex" && p.Name != "claude" {
		return errors.New("orch provider name must be codex or claude")
	}
	if !filepath.IsAbs(p.Executable) {
		return errors.New("orch provider executable must be absolute")
	}
	if err := validateProviderExecutable(p.Executable); err != nil {
		return err
	}
	if err := requireAbsoluteDirectory("provider home", p.Home); err != nil {
		return err
	}
	if p.Name == "codex" {
		switch p.Sandbox {
		case "read-only", "workspace-write", "danger-full-access":
		default:
			return errors.New("orch codex sandbox must be explicitly read-only, workspace-write or danger-full-access")
		}
	} else if p.Sandbox != "" {
		return errors.New("orch claude provider does not accept codex sandbox")
	}
	if p.Name == "codex" && p.AuthMode != "" {
		return errors.New("orch codex provider does not accept auth_mode")
	}
	if p.Name == "claude" && p.AuthMode != "" && p.AuthMode != ClaudeAuthProviderHome && p.AuthMode != ClaudeAuthHostDefault {
		return errors.New("orch claude auth_mode must be provider_home or host_default")
	}
	return validateModels(p.Models)
}

// validateProviderExecutable 固定执行文件身份，拒绝目录、符号链接和 Unix 非可执行权限。
func validateProviderExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("orch provider executable must identify a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return errors.New("orch provider executable must be executable")
	}
	return nil
}

// validateModels 要求非空唯一模型集合，不推断或补齐默认模型。
func validateModels(models []string) error {
	if len(models) == 0 {
		return errors.New("orch provider models must not be empty")
	}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model == "" || model != strings.TrimSpace(model) {
			return errors.New("orch model must be a nonempty trimmed identifier")
		}
		if _, duplicate := seen[model]; duplicate {
			return errors.New("orch provider model is duplicated")
		}
		seen[model] = struct{}{}
	}
	return nil
}

// requireAbsoluteDirectory 只接受已存在的绝对目录，不把缺失路径补为当前目录。
func requireAbsoluteDirectory(name, path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("orch %s must be an absolute directory", name)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("orch %s must be an existing directory", name)
	}
	return nil
}
