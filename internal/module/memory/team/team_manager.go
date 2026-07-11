package team

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	RootDirName         = "team"
	memoryIndexFileName = "MEMORY.md"
)

var (
	ErrTeamMemoryDisabled      = errors.New("team memory is disabled")
	ErrInvalidTeamMemWritePath = errors.New("invalid team memory write path")
	ErrInvalidTeamMemKey       = errors.New("invalid team memory key")
)

// GateSnapshot 是功能开关快照，用于判断团队记忆是否可用。
type GateSnapshot struct {
	AutoEnabled    bool
	TeamMemEnabled bool
	KairosActive   bool
}

// Config 是团队记忆配置接口，由外部适配器实现。
type Config interface {
	Gate(buildCtx contract.BuildCtx) GateSnapshot
	TeamRoot(buildCtx contract.BuildCtx) (string, error)
	ProjectRoot(buildCtx contract.BuildCtx) string
}

// disabledConfig 是 Config 的禁用实现，当外部未注入配置时作为零值兜底。
type disabledConfig struct{}

// Gate 返回禁用状态的开关快照。
func (disabledConfig) Gate(contract.BuildCtx) GateSnapshot { return GateSnapshot{} }

// TeamRoot 始终返回 ErrTeamMemoryDisabled。
func (disabledConfig) TeamRoot(contract.BuildCtx) (string, error) {
	return "", ErrTeamMemoryDisabled
}

// ProjectRoot 返回空字符串（禁用状态下无项目根目录）。
func (disabledConfig) ProjectRoot(contract.BuildCtx) string { return "" }

// TeamMemoryManager 管理团队记忆的启用状态和路径配置。
type TeamMemoryManager struct {
	cfg Config
}

var teamMemoryRuntimeReady atomic.Bool

// SetRuntimeReady 设置团队记忆运行时是否就绪；就绪状态通过原子变量在进程内共享。
func SetRuntimeReady(ready bool) {
	teamMemoryRuntimeReady.Store(ready)
}

// SwapRuntimeReadyFuncForTest 临时替换运行时就绪状态供测试使用，返回还原函数。
func SwapRuntimeReadyFuncForTest(fn func() bool) func() {
	if fn == nil {
		fn = func() bool { return false }
	}
	prev := teamMemoryRuntimeReady.Load()
	teamMemoryRuntimeReady.Store(fn())
	return func() {
		teamMemoryRuntimeReady.Store(prev)
	}
}

// NewTeamMemoryManager 创建 TeamMemoryManager，cfg 为 nil 时使用禁用配置。
func NewTeamMemoryManager(cfg Config) *TeamMemoryManager {
	if cfg == nil {
		cfg = disabledConfig{}
	}
	return &TeamMemoryManager{cfg: cfg}
}

// GetTeamMemEntrypoint 返回团队记忆的索引文件路径（MEMORY.md），未启用时返回空字符串。
func (m *TeamMemoryManager) GetTeamMemEntrypoint(buildCtx ...contract.BuildCtx) string {
	root := teamMemPath(m, firstTeamBuildCtx(buildCtx))
	if root == "" {
		return ""
	}
	return filepath.Join(root, memoryIndexFileName)
}

// teamMemPath 返回已启用的团队记忆根目录路径，未启用时返回空字符串。
func teamMemPath(m *TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if m == nil || !m.IsTeamMemoryEnabled(buildCtx) {
		return ""
	}
	root, err := configuredTeamMemPath(m, buildCtx)
	if err != nil {
		return ""
	}
	return root
}

// isTeamMemoryEnabled 检查团队记忆是否满足所有启用条件：功能开关、运行时就绪、路径可配置。
func isTeamMemoryEnabled(m *TeamMemoryManager, buildCtx contract.BuildCtx) bool {
	if m == nil {
		return false
	}
	gate := m.config().Gate(buildCtx)
	if !gate.AutoEnabled || !gate.TeamMemEnabled || gate.KairosActive {
		return false
	}
	if !teamMemoryRuntimeReady.Load() {
		return false
	}
	_, err := configuredTeamMemPath(m, buildCtx)
	return err == nil
}

// configuredTeamMemPath 从配置读取并验证团队记忆根目录路径，空值返回 ErrTeamMemoryDisabled。
func configuredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	if m == nil {
		return "", ErrTeamMemoryDisabled
	}
	root, err := m.config().TeamRoot(firstTeamBuildCtx(buildCtx))
	if err != nil {
		return "", err
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", ErrTeamMemoryDisabled
	}
	return root, nil
}

// ConfiguredTeamMemPath 是 configuredTeamMemPath 的导出包装，供外部包（如测试）调用。
func ConfiguredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	return configuredTeamMemPath(m, buildCtx...)
}

// validateTeamMemWriteRequest 校验写路径请求：先验证路径安全性，再确认功能已启用。
func validateTeamMemWriteRequest(m *TeamMemoryManager, raw string) error {
	if root, err := configuredTeamMemPath(m); err == nil {
		if _, err := validateTeamMemWritePath(root, raw); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrTeamMemoryDisabled) {
		return err
	} else if err := validateTeamMemWritePathBasic(raw); err != nil {
		return err
	}
	if !isTeamMemoryEnabled(m, contract.BuildCtx{}) {
		return ErrTeamMemoryDisabled
	}
	return nil
}

// validateTeamMemKeyRequest 校验键请求：先验证键安全性，再确认功能已启用。
func validateTeamMemKeyRequest(m *TeamMemoryManager, key string) error {
	if root, err := configuredTeamMemPath(m); err == nil {
		if _, err := validateTeamMemKey(root, key); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrTeamMemoryDisabled) {
		return err
	} else if _, err := sanitizePathKey(key); err != nil {
		return err
	}
	if !isTeamMemoryEnabled(m, contract.BuildCtx{}) {
		return ErrTeamMemoryDisabled
	}
	return nil
}

// firstTeamBuildCtx 从可变参数中取第一个 BuildCtx，无参数时返回零值。
func firstTeamBuildCtx(buildCtx []contract.BuildCtx) contract.BuildCtx {
	if len(buildCtx) == 0 {
		return contract.BuildCtx{}
	}
	return buildCtx[0]
}

// config 返回当前配置，nil 时降级为 disabledConfig。
func (m *TeamMemoryManager) config() Config {
	if m == nil || m.cfg == nil {
		return disabledConfig{}
	}
	return m.cfg
}

// GetTeamMemPath 返回团队记忆根目录路径，未启用时返回空字符串。
func (m *TeamMemoryManager) GetTeamMemPath(buildCtx ...contract.BuildCtx) string {
	return teamMemPath(m, firstTeamBuildCtx(buildCtx))
}

// IsTeamMemoryEnabled 判断当前上下文下团队记忆功能是否完全可用。
func (m *TeamMemoryManager) IsTeamMemoryEnabled(buildCtx ...contract.BuildCtx) bool {
	return isTeamMemoryEnabled(m, firstTeamBuildCtx(buildCtx))
}

// ValidateTeamMemWritePath 校验写操作目标路径是否合法且功能已启用。
func (m *TeamMemoryManager) ValidateTeamMemWritePath(path string) error {
	return validateTeamMemWriteRequest(m, path)
}

// ValidateTeamMemKey 校验团队记忆键是否合法且功能已启用。
func (m *TeamMemoryManager) ValidateTeamMemKey(key string) error {
	return validateTeamMemKeyRequest(m, key)
}
