package archtest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIEngineBuilderSimulationAcceptedContract 冻结 additive builder-simulation 子契约。
func TestRemoteCIEngineBuilderSimulationAcceptedContract(t *testing.T) {
	root := findRepoRoot(t)
	contract := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(cicontract.DocumentPath)))
	block := remoteCIBuilderSimulationContractBlock(t, contract)
	for _, marker := range remoteCIBuilderSimulationRequiredMarkers() {
		if !strings.Contains(block, marker) {
			t.Errorf("accepted remote CI contract is missing builder-simulation marker %q", marker)
		}
	}
	for _, marker := range remoteCIBuilderSimulationForbiddenMarkers() {
		if strings.Contains(block, marker) {
			t.Errorf("accepted remote CI contract contains forbidden builder-simulation marker %q", marker)
		}
	}
}

func remoteCIBuilderSimulationRequiredMarkers() []string {
	return []string{
		"remote-ci-engine-builder-simulation/v1", "builder-simulate",
		"remote-ci-builder-simulation-request/v1", "remote-ci-builder-simulation-result/v1", "remote-ci-builder-simulation-event/v1",
		"always NOT_VERIFIED", "builder-simulation Accepted", "validate-only remains side-effect free", "正常执行仍未接线",
		"engine.repository_root", "engine.commit_sha", "engine.tree_sha",
		"target.repository_root", "target.commit_sha", "target.tree_sha",
		"workload_manifest.path", "workload_manifest.sha256", "workload_manifest.abi_digest", "artifact.prefix",
		"expected_builder_digest", "observed_builder_digest", "expected_toolchain_digest", "observed_toolchain_digest",
		"protocol_abi_digest", "workload_manifest_abi_digest",
		"create-only OSS staging", "exactly one aliyun-eci/v1 container group",
		"deterministic group name", "request/input digest tags", "container_group_absent", "input_staging_absent",
		"execute/workload/Coordinator/plan/PASS/SQLite", "normal runtime", "second artifact builder",
		"cwd/HEAD/default/fallback", "host manifest", "provider raw log",
		"exit code 0", "exit code 1", "exit code 2", "exit code 3", "exit code 4",
		"V3/WJBoot", "same engine", "different target", "实云证据", "逐卡",
	}
}

func remoteCIBuilderSimulationForbiddenMarkers() []string {
	return []string{
		"builder-simulate 产出 PASS", "builder-simulate 写入 SQLite", "builder-simulate 执行 workload",
		"builder-simulate 使用 normal runtime", "builder-simulate 使用第二 artifact builder",
		"builder-simulate 从 cwd/HEAD/default 推断", "builder-simulate 输出 provider raw log",
		"builder-simulation 已有实云证据",
	}
}

// remoteCIBuilderSimulationContractBlock 只接受唯一成对的 additive 子契约标记块。
func remoteCIBuilderSimulationContractBlock(t *testing.T, contract string) string {
	t.Helper()
	const begin = "<!-- future-engine-builder-simulation:begin -->"
	const end = "<!-- future-engine-builder-simulation:end -->"
	if strings.Count(contract, begin) != 1 || strings.Count(contract, end) != 1 {
		t.Fatalf("accepted remote CI contract must contain one builder-simulation marker block")
	}
	start := strings.Index(contract, begin) + len(begin)
	remainder := contract[start:]
	finish := strings.Index(remainder, end)
	if finish < 0 {
		t.Fatalf("accepted remote CI contract builder-simulation marker block is malformed")
	}
	return remainder[:finish]
}
