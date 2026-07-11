package team

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type testConfig struct {
	gate        GateSnapshot
	teamRoot    string
	projectRoot string
	teamRootErr error
}

func newTestConfig(teamRoot string) testConfig {
	return testConfig{
		gate:        GateSnapshot{AutoEnabled: true, TeamMemEnabled: true},
		teamRoot:    teamRoot,
		projectRoot: filepath.Dir(teamRoot),
	}
}

func (c testConfig) Gate(buildCtx contract.BuildCtx) GateSnapshot {
	gate := c.gate
	if buildCtx.SessionFlags["memory_kairos"] || buildCtx.SessionFlags["kairos"] {
		gate.KairosActive = true
	}
	return gate
}

func (c testConfig) TeamRoot(contract.BuildCtx) (string, error) {
	if c.teamRootErr != nil {
		return "", c.teamRootErr
	}
	if c.teamRoot == "" {
		return "", ErrTeamMemoryDisabled
	}
	return c.teamRoot, nil
}

func (c testConfig) ProjectRoot(buildCtx contract.BuildCtx) string {
	for _, candidate := range []string{buildCtx.GitRoot, buildCtx.CWD, c.projectRoot} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

const teamMemoryRootDirName = RootDirName

func memoryIndexPath(root string) string {
	return filepath.Join(root, memoryIndexFileName)
}

func withTeamMemoryRuntimeReady(t *testing.T, ready bool) {
	t.Helper()
	restore := SwapRuntimeReadyFuncForTest(func() bool { return ready })
	t.Cleanup(restore)
}
