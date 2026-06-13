package team

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

type GateSnapshot struct {
	AutoEnabled    bool
	TeamMemEnabled bool
	KairosActive   bool
}

type Config interface {
	Gate(buildCtx contract.BuildCtx) GateSnapshot
	TeamRoot(buildCtx contract.BuildCtx) (string, error)
	ProjectRoot(buildCtx contract.BuildCtx) string
}

type disabledConfig struct{}

// Gate 处理gate。
func (disabledConfig) Gate(contract.BuildCtx) GateSnapshot { return GateSnapshot{} }

// TeamRoot 处理team根目录。
func (disabledConfig) TeamRoot(contract.BuildCtx) (string, error) {
	return "", ErrTeamMemoryDisabled
}

// ProjectRoot 处理项目根目录。
func (disabledConfig) ProjectRoot(contract.BuildCtx) string { return "" }

type TeamMemoryManager struct {
	cfg Config
}

var teamMemoryRuntimeReady atomic.Bool

// SetRuntimeReady 设置运行时ready。
func SetRuntimeReady(ready bool) {
	teamMemoryRuntimeReady.Store(ready)
}

// SwapRuntimeReadyFuncForTest 为test处理swap运行时readyfunc。
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

// NewTeamMemoryManager 创建team记忆manager。
func NewTeamMemoryManager(cfg Config) *TeamMemoryManager {
	if cfg == nil {
		cfg = disabledConfig{}
	}
	return &TeamMemoryManager{cfg: cfg}
}

// GetTeamMemEntrypoint 读取teammementrypoint。
func (m *TeamMemoryManager) GetTeamMemEntrypoint(buildCtx ...contract.BuildCtx) string {
	root := teamMemPath(m, firstTeamBuildCtx(buildCtx))
	if root == "" {
		return ""
	}
	return filepath.Join(root, memoryIndexFileName)
}

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

// isTeamMemoryEnabled 判断team记忆enabled是否可用。
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

// ConfiguredTeamMemPath 处理configuredteammem路径。
func ConfiguredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	return configuredTeamMemPath(m, buildCtx...)
}

// validateTeamMemWriteRequest 校验teammemwrite请求。
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

// validateTeamMemKeyRequest 校验teammem键请求。
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

func firstTeamBuildCtx(buildCtx []contract.BuildCtx) contract.BuildCtx {
	if len(buildCtx) == 0 {
		return contract.BuildCtx{}
	}
	return buildCtx[0]
}

func (m *TeamMemoryManager) config() Config {
	if m == nil || m.cfg == nil {
		return disabledConfig{}
	}
	return m.cfg
}

// GetTeamMemPath 读取teammem路径。
func (m *TeamMemoryManager) GetTeamMemPath(buildCtx ...contract.BuildCtx) string {
	return teamMemPath(m, firstTeamBuildCtx(buildCtx))
}

// IsTeamMemoryEnabled 判断team记忆enabled是否可用。
func (m *TeamMemoryManager) IsTeamMemoryEnabled(buildCtx ...contract.BuildCtx) bool {
	return isTeamMemoryEnabled(m, firstTeamBuildCtx(buildCtx))
}

// ValidateTeamMemWritePath 校验teammemwrite路径。
func (m *TeamMemoryManager) ValidateTeamMemWritePath(path string) error {
	return validateTeamMemWriteRequest(m, path)
}

// ValidateTeamMemKey 校验teammem键。
func (m *TeamMemoryManager) ValidateTeamMemKey(key string) error {
	return validateTeamMemKeyRequest(m, key)
}
