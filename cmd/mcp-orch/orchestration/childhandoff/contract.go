// Package childhandoff 提供受管 child endpoint 凭据交接的隔离候选。
//
// 该包只拥有一次性 credential materialization 和临时配置文件生命周期，
// 不启动 provider、接入 Fx/Execution 或修改 LaunchRequest/managed DTO。
// 当前 provider wire 尚未具备安全的 process-start receipt，因此结果只能
// 作为待接线 candidate 使用；调用方必须显式持有返回的 opaque handle。
package childhandoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskcredential"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// Provider 是 child endpoint 支持的闭合集合。
type Provider string

const (
	ProviderClaude Provider = "claude"
	ProviderCodex  Provider = "codex"

	redacted = "[REDACTED]"
)

var (
	ErrInvalidConfig       = errors.New("child handoff config is invalid")
	ErrAlreadyMaterialized = errors.New("child handoff is already materialized")
	ErrClosed              = errors.New("child handoff is closed")
	ErrMaterialization     = errors.New("child endpoint materialization failed")
	ErrCleanup             = errors.New("child endpoint cleanup failed")
	ErrProviderDisabled    = errors.New("child provider wire is disabled")
)

// Revoker 是 credential lease 的最小撤销端口。实现不得把 bearer 写入日志或持久化。
type Revoker interface {
	Revoke(context.Context, string) error
}

// Config 是显式 child handoff 构造输入。TempRoot 必须是已存在的绝对目录；
// LeaseID 必须与 IssuedCredential.BoundLeaseID 精确一致。
type Config struct {
	Provider   Provider
	Endpoint   string
	LeaseID    string
	TempRoot   string
	Credential *taskcredential.IssuedCredential
	Revoker    Revoker
}

// MarshalJSON 仅输出非敏感 provider/endpoint 摘要，省略 lease、路径和凭据。
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Provider Provider `json:"provider"`
		Endpoint string   `json:"endpoint"`
	}{Provider: c.Provider, Endpoint: c.Endpoint})
}

// String 返回不含临时路径、lease 或 credential 的配置摘要。
func (c Config) String() string { return fmt.Sprintf("child handoff config provider=%s", c.Provider) }

// Materialized 是脱敏的 provider 配置描述；文件路径和 bearer 只保存在 owner 内存中。
type Materialized struct {
	Provider Provider `json:"provider"`
	Kind     string   `json:"kind"`
	secret   *secretValue
	path     string
	args     []string
	dir      string
}

// ConfigPath 返回临时配置路径。路径不参与 JSON、String 或错误文本。
func (m Materialized) ConfigPath() string { return m.path }

// Arguments 返回 provider 启动参数的副本。
func (m Materialized) Arguments() []string { return append([]string(nil), m.args...) }

// Environment 返回需要注入 child 的环境项副本；调用方必须仅用于 process start。
func (m Materialized) Environment() []string {
	if m.secret == nil {
		return nil
	}
	return m.secret.environment()
}

// MarshalJSON 永不暴露临时路径、环境值或 bearer。
func (m Materialized) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Provider Provider `json:"provider"`
		Kind     string   `json:"kind"`
	}{Provider: m.Provider, Kind: m.Kind})
}

// String 只返回脱敏摘要。
func (m Materialized) String() string {
	return fmt.Sprintf("child handoff provider=%s kind=%s", m.Provider, m.Kind)
}

// GoString 与 String 保持相同脱敏语义。
func (m Materialized) GoString() string { return m.String() }

// MarshalText 只返回脱敏摘要。
func (m Materialized) MarshalText() ([]byte, error) { return []byte(m.String()), nil }

type secretValue struct {
	mu       sync.Mutex
	key      string
	value    []byte
	envTaken bool
}

func (s *secretValue) environment() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.envTaken || len(s.value) == 0 {
		return nil
	}
	s.envTaken = true
	return []string{s.key + "=" + string(s.value)}
}

func (s *secretValue) discard() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.value)
	s.value = nil
	s.envTaken = true
}

// String 返回环境键的脱敏表示，不返回 bearer。
func (s *secretValue) String() string {
	if s == nil {
		return redacted
	}
	return s.key + "=" + redacted
}

// GoString 保证 %#v 不展开 bearer 字节。
func (s *secretValue) GoString() string { return s.String() }

// Format 覆盖所有 fmt 动词并固定返回脱敏文本。
func (s *secretValue) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, s.String())
}

// MarshalJSON 只序列化环境键和固定脱敏值。
func (s *secretValue) MarshalJSON() ([]byte, error) {
	if s == nil {
		return json.Marshal(redacted)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Marshal(map[string]string{"name": s.key, "value": redacted})
}

// MarshalText 提供同样的脱敏文本合同。
func (s *secretValue) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Owner 是 child endpoint handoff 的唯一生命周期 owner。
type Owner struct {
	mu          sync.Mutex
	config      Config
	material    *Materialized
	credential  *taskcredential.IssuedCredential
	tempPath    string
	tempDir     string
	terminal    bool
	active      bool
	closing     bool
	activeDone  chan struct{}
	cleanupOnce sync.Once
	cleanupMu   sync.Mutex
	cleanupErr  error
	cleanupCfg  Config
	revokeDone  bool
	rootID      pathIdentity
	tempDirID   *pathIdentity
	tempPathID  *pathIdentity
}

// pathIdentity 绑定 canonical 路径与底层文件对象，防止路径被替换后继续使用。
type pathIdentity struct {
	canonical string
	info      os.FileInfo
}

var _ interface{ Close() error } = (*Owner)(nil)
var _ json.Marshaler = Materialized{}
var _ fmt.Stringer = Materialized{}

// New 构造未 materialize 的 owner；缺少撤销端口或临时根目录时 fail-closed。
func New(config Config) (*Owner, error) {
	if err := validateConfig(config); err != nil {
		if config.Credential != nil {
			config.Credential.Discard()
		}
		return nil, err
	}
	rootID, err := inspectTempRoot(config.TempRoot)
	if err != nil {
		config.Credential.Discard()
		return nil, ErrInvalidConfig
	}
	config.TempRoot = rootID.canonical
	return &Owner{config: config, credential: config.Credential, activeDone: make(chan struct{}), rootID: rootID}, nil
}

// validateConfig 检查 provider、lease、endpoint、凭据和隔离目录的全部前置条件。
func validateConfig(config Config) error {
	if err := validateProvider(config.Provider); err != nil {
		return err
	}
	if config.Credential == nil || isNil(config.Revoker) {
		return ErrInvalidConfig
	}
	if !validLeaseID(config.LeaseID) || !validEndpoint(config.Endpoint) {
		return ErrInvalidConfig
	}
	if config.Credential.BoundLeaseID() != config.LeaseID {
		return ErrInvalidConfig
	}
	return validateTempRoot(config.TempRoot)
}

func validateProvider(provider Provider) error {
	if provider != ProviderClaude && provider != ProviderCodex {
		return ErrInvalidConfig
	}
	return nil
}

// validateTempRoot 要求已存在、规范、严格 owner-only 的真实目录作为隔离根。
func validateTempRoot(path string) error {
	_, err := inspectTempRoot(path)
	return err
}

// inspectTempRoot 校验临时根为 canonical、真实且严格 owner-only 的目录。
func inspectTempRoot(path string) (pathIdentity, error) {
	if !filepath.IsAbs(path) {
		return pathIdentity{}, ErrInvalidConfig
	}
	info, err := os.Lstat(path)
	if err != nil || !isRealDirectory(info) {
		return pathIdentity{}, ErrInvalidConfig
	}
	canonical, canonicalInfo, err := canonicalTempRoot(path, info)
	if err != nil {
		return pathIdentity{}, ErrInvalidConfig
	}
	if err := securefs.CheckPrivateOwnerOnly(canonical, canonicalInfo); err != nil {
		return pathIdentity{}, ErrInvalidConfig
	}
	return pathIdentity{canonical: canonical, info: canonicalInfo}, nil
}

// isRealDirectory 判断文件信息是否为非符号链接目录。
func isRealDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// canonicalTempRoot 解析临时根并确认解析前后的文件对象没有变化。
func canonicalTempRoot(path string, info os.FileInfo) (string, os.FileInfo, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	canonical = filepath.Clean(canonical)
	canonicalInfo, err := os.Lstat(canonical)
	if err != nil || !isRealDirectory(canonicalInfo) || !os.SameFile(info, canonicalInfo) {
		return "", nil, ErrInvalidConfig
	}
	return canonical, canonicalInfo, nil
}

func validLeaseID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 160 && !strings.ContainsAny(value, "\r\n")
}

// validEndpoint 仅接受显式端口的 loopback HTTP 地址，拒绝凭据、查询和片段。
func validEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Hostname() != "127.0.0.1" {
		return false
	}
	port := parsed.Port()
	if port == "" {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1 && number <= 65535
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
