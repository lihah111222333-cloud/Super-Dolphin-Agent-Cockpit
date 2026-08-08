package archtest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRemoteCIExecutorGoBuildCacheUsesOnlyImageLayerSeed 拒绝多代目录和第二缓存入口回流。
func TestRemoteCIExecutorGoBuildCacheUsesOnlyImageLayerSeed(t *testing.T) {
	root := findRepoRoot(t)
	paths := map[string]string{
		"Dockerfile":                      filepath.Join(root, "build", "gate", "Dockerfile"),
		"executor_workspace.go":           filepath.Join(root, "internal", "devtools", "gate", "executor_workspace.go"),
		"executor_plan.go":                filepath.Join(root, "internal", "devtools", "gate", "executor_plan.go"),
		"executor_plan_report_profile.go": filepath.Join(root, "internal", "devtools", "gate", "executor_plan_report_profile.go"),
		"executor_cache_metrics.go":       filepath.Join(root, "internal", "devtools", "gate", "executor_cache_metrics.go"),
		"executor.go":                     filepath.Join(root, "internal", "devtools", "gate", "executor.go"),
	}
	assertRemoteCIGoBuildCachePathIdentity(t)
	workspace := readRemoteCIContractGuardFile(t, paths["executor_workspace.go"])
	plan := readRemoteCIContractGuardFile(t, paths["executor_plan.go"])
	assertRemoteCIGoBuildCacheNoRetiredPaths(t, paths)
	assertRemoteCIGoBuildCachePlan(t, plan)
	assertRemoteCIGoBuildCacheWorkspace(t, workspace, paths["executor_workspace.go"])
	executor := readRemoteCIContractGuardFile(t, paths["executor.go"])
	assertRemoteCIGoBuildCacheExecutor(t, executor)
	metrics := readRemoteCIContractGuardFile(t, paths["executor_cache_metrics.go"])
	assertRemoteCIGoBuildCacheMetrics(t, metrics)
}

func assertRemoteCIGoBuildCachePathIdentity(t *testing.T) {
	t.Helper()
	if cicontract.GoBuildCachePathID != "accepted-imagecache-single-readonly-go-build-seed-private-delta/v2" {
		t.Fatalf("remote CI Go build-cache path identity drifted: %q", cicontract.GoBuildCachePathID)
	}
	if cicontract.ImageSeedValidationPathID != "image-build-full-tree-normal-shard-manifest-roots-readonly-mount/v1" {
		t.Fatalf("remote CI image seed validation path identity drifted: %q", cicontract.ImageSeedValidationPathID)
	}
}

func assertRemoteCIGoBuildCacheNoRetiredPaths(t *testing.T, paths map[string]string) {
	t.Helper()
	for name, path := range paths {
		source := readRemoteCIContractGuardFile(t, path)
		for _, forbidden := range []string{
			"ExecutorGoBuildCacheSeedsRoot", "ExecutorRemoteGoBuildCacheSeedRoots", "goBuildCacheSeedRoots",
			"executorGoBuildCacheSeedRoots", "discoverExecutorGoBuildCacheSeedRoots", "trustedExecutorGoBuildCacheSeeds",
			"seedExecutorGoBuildCacheSeeds", "executorGoBuildCacheSeedOverlaps", "trustedExecutorGoBuildCacheGeneration",
			"goBuildCacheProxyMaxSeedRoots", "cache-seeds", "BaselineHitByGeneration", "baseline_hit_by_generation",
		} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s retains retired multi-generation Go build-cache path %q", name, forbidden)
			}
		}
	}
}

func assertRemoteCIGoBuildCachePlan(t *testing.T, plan string) {
	t.Helper()
	for _, required := range []string{
		"ExecutorOCIProjectGoBuildCacheSeedRoot", "seedExecutorGoBuildCache(seedRoot, cacheRoot)",
		"(string, string, error)", "goBuildCacheSeedRoot string",
	} {
		if !strings.Contains(plan, required) {
			t.Fatal("executor plan must bind the single immutable OCI image-layer Go build-cache seed")
		}
	}
}

func assertRemoteCIGoBuildCacheWorkspace(t *testing.T, workspace string, path string) {
	t.Helper()
	for _, required := range []string{"validateExecutorOCIProjectGoBuildCacheSeed", "config.seedRoot = args[index+1]"} {
		if !strings.Contains(workspace, required) {
			t.Fatal("OCI image-layer Go build-cache seed must retain read-only physical-tree validation")
		}
	}
	file := parseRemoteCIContractGuardFile(t, path)
	if remoteCIFunctionByName(file, "validateExecutorOCIProjectGoBuildCacheSeed") == nil {
		t.Fatal("OCI image-layer Go build-cache mount validator is missing")
	}
	if remoteCIFunctionHasSelector(file, "validateExecutorOCIProjectGoBuildCacheSeed", "filepath", "WalkDir") ||
		!remoteCIFunctionContainsIdentifier(file, "validateExecutorOCIProjectGoBuildCacheSeed", "validateReadOnlyOCIImagePath") {
		t.Fatal("normal shard Go build-cache validation must be constant-time and bind the read-only image mount")
	}
}

// TestRemoteCIImageBuildOwnsFullGoCacheTreeValidation 拒绝把完整树扫描放回 normal shard 热路径。
func TestRemoteCIImageBuildOwnsFullGoCacheTreeValidation(t *testing.T) {
	root := findRepoRoot(t)
	dockerfile := readRemoteCIContractGuardFile(t, filepath.Join(root, "build", "gate", "Dockerfile"))
	for _, required := range []string{
		`find /opt/super-dolphin/cache/go-build -type l -print -quit`,
		`find /opt/super-dolphin/cache/go-build -perm /222 -print -quit`,
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("immutable image build is missing full Go cache tree validation %q", required)
		}
	}
}

func assertRemoteCIGoBuildCacheExecutor(t *testing.T, executor string) {
	t.Helper()
	if !strings.Contains(executor, "executorGoBuildCacheProxyCommand(launcher string, seedRoot string, privateRoot string)") ||
		strings.Contains(executor, "for _, seedRoot") {
		t.Fatal("executor Go build-cache proxy must emit exactly one --seed argument")
	}
}

func assertRemoteCIGoBuildCacheMetrics(t *testing.T, metrics string) {
	t.Helper()
	if !strings.Contains(metrics, "len(metrics.SeedRoots) != 1") || strings.Contains(metrics, "len(metrics.SeedRoots) >") {
		t.Fatal("Go build-cache metrics must retain exactly one JSON seed root")
	}
}
