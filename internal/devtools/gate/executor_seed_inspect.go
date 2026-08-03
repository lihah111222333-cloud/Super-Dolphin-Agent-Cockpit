package gate

import (
	"errors"
	"io"
)

// executeRuntimeSeedInspectCommand 只输出重算后的运行时内容清单，供增量刷新诊断字段漂移。
func executeRuntimeSeedInspectCommand(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: super-dolphin-gate worker runtime-seed-inspect <snapshot-root> <runtime-root>")
	}
	manifest, err := BuildRuntimeSeedManifest(args[0], args[1])
	if err != nil {
		return err
	}
	return EncodeRuntimeSeedManifest(stdout, manifest)
}

// runtimeSeedManifestDriftFields 返回内容身份发生变化的字段名，不泄露摘要值。
func runtimeSeedManifestDriftFields(tracked RuntimeSeedManifest, actual RuntimeSeedManifest) []string {
	fields := make([]string, 0, 10)
	for _, field := range []struct {
		name    string
		drifted bool
	}{
		{"schema_version", tracked.SchemaVersion != actual.SchemaVersion},
		{"go_sum_sha256", tracked.GoSumSHA256 != actual.GoSumSHA256},
		{"module_proxy_lock_sha256", tracked.ModuleProxyLockSHA256 != actual.ModuleProxyLockSHA256},
		{"module_proxy_tree_sha256", tracked.ModuleProxyTreeSHA256 != actual.ModuleProxyTreeSHA256},
		{"go_mod_cache_tree_sha256", tracked.GoModCacheTreeSHA256 != actual.GoModCacheTreeSHA256},
		{"package_lock_sha256", tracked.PackageLockSHA256 != actual.PackageLockSHA256},
		{"node_modules_tree_sha256", tracked.NodeModulesTreeSHA256 != actual.NodeModulesTreeSHA256},
		{"npm_cache_tree_sha256", tracked.NPMCacheTreeSHA256 != actual.NPMCacheTreeSHA256},
		{"vite_cache_tree_sha256", tracked.ViteCacheTreeSHA256 != actual.ViteCacheTreeSHA256},
		{"ripgrep_sha256", tracked.RipgrepSHA256 != actual.RipgrepSHA256},
		{"sqruff_sha256", tracked.SqruffSHA256 != actual.SqruffSHA256},
	} {
		if field.drifted {
			fields = append(fields, field.name)
		}
	}
	return fields
}
