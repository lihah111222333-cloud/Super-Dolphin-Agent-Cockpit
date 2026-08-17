//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerPrepareVueBridge 只为归属于当前产品根的 Vue server 注入受管 cohort 参数。
// 外部 binary 保持原始参数与生命周期，避免 marker 碰撞污染第三方进程。
func runtimeServerPrepareVueBridge(adapter multilsp.LanguageAdapter, serverBinary string, args []string) ([]string, *runtimeVueTSBridgeSpec, error) {
	prepared := append([]string(nil), args...)
	if !adapterSupportsLanguage(adapter, "vue") {
		return prepared, nil, nil
	}
	_, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return nil, nil, err
	}
	if !owned {
		return prepared, nil, nil
	}
	spec, err := runtimeServerWindowsVueTSBridgeSpec(serverBinary)
	if err != nil {
		return nil, nil, err
	}
	prepared = append(prepared, "--tsdk="+filepath.Join(spec.typescriptModuleRoot, "lib"))
	return prepared, &spec, nil
}

// runtimeServerWindowsVueTSBridgeSpec 从 Vue shim 所在 npm prefix 解析同 cohort 的真实依赖。
// 所有文件必须已存在且位于产品根内；该 helper 不查 PATH、不联网、不写 cache。
func runtimeServerWindowsVueTSBridgeSpec(serverBinary string) (runtimeVueTSBridgeSpec, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	if !owned {
		return runtimeVueTSBridgeSpec{}, fmt.Errorf("Vue server is outside the owned Windows product root: %q", serverBinary)
	}
	prefix, err := runtimeServerWindowsNPMPrefixFromBinary(serverBinary)
	if err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	if err := runtimeServerWindowsRequireWithin(productRoot, prefix, "Vue npm prefix"); err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	typescriptRoot := filepath.Join(prefix, "node_modules", "typescript")
	if err := requireRuntimeRegularFile(productRoot, filepath.Join(typescriptRoot, "lib", "tsserver.js"), "managed TypeScript tsserver.js"); err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	typescriptBinary := filepath.Join(prefix, "node_modules", ".bin", runtimeNPMExecutableNameForPlatform("windows", "typescript-language-server"))
	if err := requireRuntimeRegularFile(productRoot, typescriptBinary, "managed TypeScript language server"); err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	vuePluginLocation := filepath.Join(prefix, "node_modules", "@vue", "language-server")
	if err := requireRuntimeDirectory(productRoot, vuePluginLocation, "managed Vue language-server plugin location"); err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	vueTypeScriptPlugin := filepath.Join(prefix, "node_modules", "@vue", "typescript-plugin")
	if err := requireRuntimeDirectory(productRoot, vueTypeScriptPlugin, "managed Vue TypeScript plugin package"); err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	if err := requireRuntimeRegularFile(productRoot, filepath.Join(vueTypeScriptPlugin, "index.js"), "managed Vue TypeScript plugin entrypoint"); err != nil {
		return runtimeVueTSBridgeSpec{}, err
	}
	return runtimeVueTSBridgeSpec{
		typescriptBinary:     typescriptBinary,
		typescriptModuleRoot: typescriptRoot,
		vuePluginLocation:    vuePluginLocation,
	}, nil
}

func runtimeServerWindowsNPMPrefixFromBinary(serverBinary string) (string, error) {
	resolved := filepath.Clean(strings.TrimSpace(serverBinary))
	if target, ok := installer.CommandShimTarget(resolved); ok {
		resolved = filepath.Clean(target)
	}
	lower := strings.ToLower(filepath.ToSlash(resolved))
	marker := "/node_modules/"
	index := strings.Index(lower, marker)
	if index <= 0 {
		return "", fmt.Errorf("derive managed npm prefix from Vue server binary %q: node_modules marker is missing", serverBinary)
	}
	prefix := filepath.Clean(filepath.FromSlash(resolved[:index]))
	if !filepath.IsAbs(prefix) {
		return "", fmt.Errorf("managed Vue npm prefix is not absolute: %q", prefix)
	}
	return prefix, nil
}

func runtimeServerWindowsRequireWithin(root, candidate, label string) error {
	if _, err := installer.WindowsShortProcessPathWithinRoot(root, candidate); err != nil {
		return fmt.Errorf("%s failed Windows product-root path guard: root=%q candidate=%q: %w", label, root, candidate, err)
	}
	return nil
}

// runtimeServerPrepareVueTSCompanionEnvironment 为同一 repo cohort 的 TypeScript companion 选举独立 secondary lease。
// Vue 主 client 和 companion 共享 cohort 预算但不共享 lease 文件，避免任一 client Close 两次删除同一 primary。
func runtimeServerPrepareVueTSCompanionEnvironment(env []string) ([]string, error) {
	root, err := runtimeServerCacheRoot()
	if err != nil {
		return nil, err
	}
	cohortID := runtimeServerEnvValue(env, multilsp.ResourceRepositoryCohortIDEnv)
	if strings.TrimSpace(cohortID) == "" {
		return nil, errors.New("Vue TypeScript companion requires a repository cohort ID")
	}
	role, leasePath, err := runtimeServerAcquireResourceLease(root, cohortID)
	if err != nil {
		return nil, fmt.Errorf("acquire Vue TypeScript companion resource lease: %w", err)
	}
	leaseEnv := appendRuntimeServerEnvironment(env, []string{
		multilsp.ResourceCohortRoleEnv + "=" + role,
		multilsp.ResourceCohortLeaseEnv + "=" + leasePath,
	})
	releaseLease := func() error {
		return multilsp.ReleaseResourceCohortLease(leaseEnv)
	}
	if role != multilsp.ResourceCohortRoleSecondary {
		return nil, errors.Join(
			fmt.Errorf("Vue TypeScript companion lease role = %q, want %q", role, multilsp.ResourceCohortRoleSecondary),
			releaseLease(),
		)
	}
	limits, err := runtimeServerResolveResourceLimits(env)
	if err != nil {
		return nil, errors.Join(err, releaseLease())
	}
	return appendRuntimeServerEnvironment(leaseEnv, []string{
		multilsp.ResourceProcessRSSLimitMBEnv + "=" + strconv.Itoa(limits.secondaryRSSMB),
		"NODE_OPTIONS=" + runtimeServerNodeOptions(env, limits.secondaryNodeHeapMB),
	}), nil
}

func requireRuntimeRegularFile(root, path, label string) error {
	if _, err := installer.WindowsShortProcessPathWithinRoot(root, path); err != nil {
		return fmt.Errorf("%s failed locked-path guard: %w", label, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s is unavailable at locked path %q: %w", label, path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%s is not a non-empty regular file at locked path %q", label, path)
	}
	return nil
}

func requireRuntimeDirectory(root, path, label string) error {
	if _, err := installer.WindowsShortProcessPathWithinRoot(root, path); err != nil {
		return fmt.Errorf("%s failed locked-path guard: %w", label, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s is unavailable at locked path %q: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory at locked path %q", label, path)
	}
	return nil
}
