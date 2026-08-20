package archtest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestHeterociContractPlatformHeader 区分权威远程 ECI 平台与本地非权威异构拓扑。
func TestHeterociContractPlatformHeader(t *testing.T) {
	root := findRepoRoot(t)
	document := readRemoteCIContractGuardFile(t, filepath.Join(root, "docs/契约/remote-ci-eci-imagecache-contract.md"))
	lines := strings.Split(document, "\n")
	scopeIndex, _ := heterociContractUniqueLine(t, lines, "适用范围：")
	remoteIndex, remote := heterociContractUniqueLine(t, lines, "权威远程 ECI 目标平台：")
	localIndex, local := heterociContractUniqueLine(t, lines, "本地非权威异构拓扑平台：")
	if remote != "权威远程 ECI 目标平台：`linux/amd64`" {
		t.Fatalf("remote ECI platform header = %q", remote)
	}
	if local != "本地非权威异构拓扑平台：Linux、Windows、Darwin/macOS" {
		t.Fatalf("local heterogeneous platform header = %q", local)
	}
	if remoteIndex != scopeIndex+1 || localIndex != remoteIndex+1 {
		t.Fatalf("platform metadata positions scope=%d remote=%d local=%d must be adjacent", scopeIndex, remoteIndex, localIndex)
	}
	if localIndex+1 >= len(lines) || lines[localIndex+1] != "" {
		t.Fatal("platform metadata must end before the first contract body paragraph")
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "目标平台：") {
			t.Fatal("heterogeneous CI contract still uses one ambiguous target-platform header")
		}
	}
}

// TestHeterociContractCanonicalRule 冻结 R11 精确采集语义在唯一 cicontract 映射中的存在性。
func TestHeterociContractCanonicalRule(t *testing.T) {
	canonical := cicontract.CanonicalMarkdown()
	_, rule := heterociContractUniqueLine(t, strings.Split(canonical, "\n"), "| `8.3` |")
	for _, marker := range []string{
		"| `8.3` | §8 |",
		"LoadManifestFile 与 AcquireSnapshot",
		"一次 64 节点 opaque snapshot",
		"whole-zero SnapshotSet",
		"仅 effectiveRoutingSnapshot 扣当前 book reservation",
	} {
		if !strings.Contains(rule, marker) {
			t.Fatalf("heterogeneous CI canonical 8.3 rule is missing %q", marker)
		}
	}
}

func heterociContractUniqueLine(t *testing.T, lines []string, prefix string) (int, string) {
	t.Helper()
	foundIndex := -1
	for index, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if foundIndex >= 0 {
			t.Fatalf("heterogeneous CI contract line prefix %q appears more than once", prefix)
		}
		foundIndex = index
	}
	if foundIndex < 0 {
		t.Fatalf("heterogeneous CI contract line prefix %q is missing", prefix)
	}
	return foundIndex, lines[foundIndex]
}
