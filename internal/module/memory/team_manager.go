package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const teamMemoryRootDirName = "team"

var teamMemoryRuntimeReadyFunc = func() bool {
	return false
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
	root, err := configuredTeamMemPath(m)
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
	_, err := configuredTeamMemPath(m)
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
	return cfg.Enabled && cfg.Features.TeamMemory && strings.TrimSpace(cfg.ProjectRoot) != ""
}

func configuredTeamMemRoot(cfg *Config) (string, error) {
	cfg = memoryConfig(cfg)
	projectRoot := strings.TrimSpace(cfg.ProjectRoot)
	if projectRoot == "" {
		return "", ErrInvalidProjectDir
	}
	canonical, err := FindCanonicalGitRoot(context.Background(), projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProjectDir, err)
	}
	cleaned, err := cleanAbsolutePath(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProjectDir, err)
	}
	return filepath.Join(cleaned, teamMemoryRootDirName), nil
}

func configuredTeamMemPath(m *TeamMemoryManager) (string, error) {
	if m == nil {
		return "", ErrTeamMemoryDisabled
	}
	cfg := m.config()
	if !teamMemoryConfigured(cfg) {
		return "", ErrTeamMemoryDisabled
	}
	return configuredTeamMemRoot(&cfg)
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
