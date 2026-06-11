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

func (disabledConfig) Gate(contract.BuildCtx) GateSnapshot { return GateSnapshot{} }
func (disabledConfig) TeamRoot(contract.BuildCtx) (string, error) {
	return "", ErrTeamMemoryDisabled
}
func (disabledConfig) ProjectRoot(contract.BuildCtx) string { return "" }

type TeamMemoryManager struct {
	cfg Config
}

var teamMemoryRuntimeReady atomic.Bool

func SetRuntimeReady(ready bool) {
	teamMemoryRuntimeReady.Store(ready)
}

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

func NewTeamMemoryManager(cfg Config) *TeamMemoryManager {
	if cfg == nil {
		cfg = disabledConfig{}
	}
	return &TeamMemoryManager{cfg: cfg}
}

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

func ConfiguredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	return configuredTeamMemPath(m, buildCtx...)
}

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

func (m *TeamMemoryManager) GetTeamMemPath(buildCtx ...contract.BuildCtx) string {
	return teamMemPath(m, firstTeamBuildCtx(buildCtx))
}

func (m *TeamMemoryManager) IsTeamMemoryEnabled(buildCtx ...contract.BuildCtx) bool {
	return isTeamMemoryEnabled(m, firstTeamBuildCtx(buildCtx))
}

func (m *TeamMemoryManager) ValidateTeamMemWritePath(path string) error {
	return validateTeamMemWriteRequest(m, path)
}

func (m *TeamMemoryManager) ValidateTeamMemKey(key string) error {
	return validateTeamMemKeyRequest(m, key)
}
