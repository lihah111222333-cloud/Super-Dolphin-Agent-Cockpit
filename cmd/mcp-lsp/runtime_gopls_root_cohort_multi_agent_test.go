package main

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

type runtimeGoplsMultiAgentLease struct {
	repository int
	controller multilsp.GoplsRootCohortController
	lease      multilsp.GoplsRootCohortLease
}

// TestRuntimeDurableGoplsRootCohortAllowsTenCompatibleAgentsAcrossTwoRepositories
// 用十个独立 controller 覆盖两个仓库各五个 worktree agent 同时持有 lease 的边界。
// super-dolphin-ci: compile-group-exclusive
func TestRuntimeDurableGoplsRootCohortAllowsTenCompatibleAgentsAcrossTwoRepositories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	configs := []multilsp.GoplsRootCohortConfig{
		runtimeGoplsMultiAgentRepositoryConfig(0),
		runtimeGoplsMultiAgentRepositoryConfig(1),
	}
	agents := runtimeGoplsAcquireMultiAgentLeases(t, configs)
	runtimeGoplsAssertMultiAgentCohorts(t, agents, configs, 5, multilsp.GoplsRootCohortStateAdmitted)
	runtimeGoplsReleaseMultiAgentLeases(t, agents)
	runtimeGoplsAssertMultiAgentCohorts(t, agents, configs, 0, multilsp.GoplsRootCohortStateIdle)
}

// runtimeGoplsAcquireMultiAgentLeases 为两个仓库各创建五个独立 controller 并持有兼容 lease。
func runtimeGoplsAcquireMultiAgentLeases(t *testing.T, configs []multilsp.GoplsRootCohortConfig) []runtimeGoplsMultiAgentLease {
	t.Helper()
	agents := make([]runtimeGoplsMultiAgentLease, 0, 10)
	for agent := range 10 {
		repository := agent / 5
		controller, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(time.Second)
		if err != nil {
			t.Fatalf("new agent %d controller: %v", agent, err)
		}
		lease, err := controller.AcquireLease(configs[repository])
		if err != nil {
			t.Fatalf("agent %d AcquireLease for repository %d: %v", agent, repository, err)
		}
		agents = append(agents, runtimeGoplsMultiAgentLease{repository: repository, controller: controller, lease: lease})
	}
	return agents
}

// runtimeGoplsAssertMultiAgentCohorts 校验两个仓库的成员数与 cohort 状态。
func runtimeGoplsAssertMultiAgentCohorts(t *testing.T, agents []runtimeGoplsMultiAgentLease, configs []multilsp.GoplsRootCohortConfig, members int, state multilsp.GoplsRootCohortState) {
	t.Helper()
	for repository := range 2 {
		snapshot, ok := agents[repository*5].controller.Snapshot(configs[repository])
		if !ok || snapshot.ActiveMembers != members || snapshot.State != state {
			t.Fatalf("repository %d snapshot = (%+v, %v), want members=%d state=%s", repository, snapshot, ok, members, state)
		}
	}
}

// runtimeGoplsReleaseMultiAgentLeases 显式释放全部 agent lease。
func runtimeGoplsReleaseMultiAgentLeases(t *testing.T, agents []runtimeGoplsMultiAgentLease) {
	t.Helper()
	for agent, holder := range agents {
		if err := holder.lease.ReleaseWithOwner(func() error { return nil }); err != nil {
			t.Fatalf("release agent %d repository %d lease: %v", agent, holder.repository, err)
		}
	}
}

// runtimeGoplsMultiAgentRepositoryConfig 返回两个隔离仓库各自的稳定 root cohort 配置。
func runtimeGoplsMultiAgentRepositoryConfig(repository int) multilsp.GoplsRootCohortConfig {
	if repository == 0 {
		return runtimeDurableGoplsRootCohortTestConfig("repository-a")
	}
	config := runtimeDurableGoplsRootCohortTestConfig("repository-b")
	config.RepositoryInstanceProof = multilsp.GoplsRepositoryInstanceProof{
		CanonicalRootDigest: "canonical-root-digest-b",
		FilesystemIdentity:  "dev:1:ino:3",
		GitMarkerDigest:     "git-marker-digest-b",
		InstanceNonce:       "instance-nonce-b",
	}
	return config
}
