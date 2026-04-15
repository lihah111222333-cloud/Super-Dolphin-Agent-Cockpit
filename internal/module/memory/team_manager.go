package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const teamMemoryRootDirName = "team"

var teamMemoryRuntimeReady atomic.Bool

var teamMemoryRuntimeReadyFunc = func() bool {
	return teamMemoryRuntimeReady.Load()
}

func setTeamMemoryRuntimeReady(ready bool) {
	teamMemoryRuntimeReady.Store(ready)
}

func (m *TeamMemoryManager) GetTeamMemEntrypoint(buildCtx ...contract.BuildCtx) string {
	root := teamMemPath(m, firstTeamBuildCtx(buildCtx))
	if root == "" {
		return ""
	}
	return memoryIndexPath(root)
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
	if !teamMemoryRuntimeAvailable(buildCtx, m.config()) {
		return false
	}
	_, err := configuredTeamMemPath(m, buildCtx)
	return err == nil
}

func teamMemoryRuntimeAvailable(buildCtx contract.BuildCtx, cfg Config) bool {
	if !teamMemoryConfigured(cfg) {
		return false
	}
	gate := ResolveMemoryGate(buildCtx, &cfg)
	if !gate.AutoEnabled || !gate.TeamMemEnabled || gate.KairosActive {
		return false
	}
	return teamMemoryRuntimeReadyFunc()
}

func teamMemoryConfigured(cfg Config) bool {
	return cfg.Enabled && cfg.Features.TeamMemory
}

func configuredTeamMemRoot(cfg *Config, buildCtx ...contract.BuildCtx) (string, error) {
	cfg = memoryConfig(cfg)
	projectRoot := teamMemoryProjectRoot(cfg, firstTeamBuildCtx(buildCtx))
	if projectRoot == "" && strings.TrimSpace(cfg.AutoMemPathOverride) == "" {
		return "", ErrInvalidProjectDir
	}
	root, err := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return "", err
	}
	cleaned, err := cleanAbsolutePath(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	return filepath.Join(cleaned, teamMemoryRootDirName), nil
}

func teamMemoryProjectRoot(cfg *Config, buildCtx contract.BuildCtx) string {
	cfg = memoryConfig(cfg)
	for _, candidate := range []string{
		strings.TrimSpace(buildCtx.GitRoot),
		strings.TrimSpace(buildCtx.CWD),
		strings.TrimSpace(cfg.ProjectRoot),
	} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func configuredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	if m == nil {
		return "", ErrTeamMemoryDisabled
	}
	cfg := m.config()
	if !teamMemoryConfigured(cfg) {
		return "", ErrTeamMemoryDisabled
	}
	return configuredTeamMemRoot(&cfg, firstTeamBuildCtx(buildCtx))
}

func validateTeamMemWriteRequest(m *TeamMemoryManager, raw string) error {
	if root, ok := teamMemValidationRoot(m); ok {
		if _, err := validateTeamMemWritePath(root, raw); err != nil {
			return err
		}
	} else if err := validateTeamMemWritePathBasic(raw); err != nil {
		return err
	}
	if !isTeamMemoryEnabled(m, contract.BuildCtx{}) {
		return ErrTeamMemoryDisabled
	}
	return nil
}

func validateTeamMemKeyRequest(m *TeamMemoryManager, key string) error {
	if root, ok := teamMemValidationRoot(m); ok {
		if _, err := validateTeamMemKey(root, key); err != nil {
			return err
		}
	} else if _, err := sanitizePathKey(key); err != nil {
		return err
	}
	if !isTeamMemoryEnabled(m, contract.BuildCtx{}) {
		return ErrTeamMemoryDisabled
	}
	return nil
}

func teamMemValidationRoot(m *TeamMemoryManager) (string, bool) {
	root, err := configuredTeamMemPath(m)
	if err != nil {
		return "", false
	}
	return root, true
}

func firstTeamBuildCtx(buildCtx []contract.BuildCtx) contract.BuildCtx {
	if len(buildCtx) == 0 {
		return contract.BuildCtx{}
	}
	return buildCtx[0]
}

func (m *TeamMemoryManager) config() Config {
	if m == nil || m.cfg == nil {
		return Config{}
	}
	return *m.cfg
}
