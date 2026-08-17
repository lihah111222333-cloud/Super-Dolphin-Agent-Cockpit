//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

var runtimeWindowsVCLibsFixtureMu sync.Mutex

// configureRuntimeManagerLanguageServerFixtures 为 Windows manager 测试构造最小锁定
// Node/npm cohort。它不读取用户 cache、不走 PATH，也不联网，因而测试只证明 resolver 接线。
func configureRuntimeManagerLanguageServerFixtures(t *testing.T, productRoot string, languageIDs []string) {
	t.Helper()
	productRoot, err := filepath.Abs(productRoot)
	if err != nil {
		t.Fatalf("resolve Windows runtime fixture product root: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("protect Windows runtime fixture product root: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	configureRuntimeWindowsVCLibsResolverFixture(t, productRoot)
	nodeRuntime, err := lspinstaller.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("create Windows runtime fixture resolver: %v", err)
	}
	paths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve Windows runtime fixture paths: %v", err)
	}
	if err := os.MkdirAll(paths.BinDir, 0o700); err != nil {
		t.Fatalf("create Windows runtime fixture bin directory: %v", err)
	}

	writtenBinaries := make(map[string]struct{})
	writtenPackages := make(map[string]struct{})
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		if !runtimeTestSpecMatchesAnyLanguage(spec, languageIDs) {
			continue
		}
		if _, ok := writtenBinaries[spec.binaryName]; !ok {
			writeRuntimeTestFile(t, filepath.Join(paths.BinDir, spec.binaryName), "@echo off\r\nexit /b 0\r\n")
			writtenBinaries[spec.binaryName] = struct{}{}
		}
		packages, packagesErr := runtimeNPMExactPackages(spec.args)
		if packagesErr != nil {
			t.Fatalf("resolve exact Windows runtime fixture packages for %s: %v", spec.binaryName, packagesErr)
		}
		for _, exactPackage := range packages {
			name, version := splitRuntimeExactNPMPackageForTest(t, exactPackage)
			key := name + "@" + version
			if _, ok := writtenPackages[key]; ok {
				continue
			}
			metadata, marshalErr := json.Marshal(map[string]string{"name": name, "version": version})
			if marshalErr != nil {
				t.Fatalf("encode Windows runtime fixture package %s: %v", name, marshalErr)
			}
			writeRuntimeTestFile(t, filepath.Join(paths.Prefix, "node_modules", filepath.FromSlash(name), "package.json"), string(metadata))
			writtenPackages[key] = struct{}{}
		}
	}
	if len(writtenBinaries) == 0 || len(writtenPackages) == 0 {
		t.Fatalf("incomplete Windows locked runtime fixture for languages %v: binaries=%d packages=%d", languageIDs, len(writtenBinaries), len(writtenPackages))
	}
}

// configureRuntimeWindowsVCLibsResolverFixture 只为 resolver 接线单测提供真实目录；
// 官方 Appx/SHA/完整树与 ACL 由独立生产 E2E 证明。本 helper 没有生产环境开关，
// 并序列化 resolver 替换，防止测试间共享可变依赖或误用用户缓存。
func configureRuntimeWindowsVCLibsResolverFixture(t *testing.T, productRoot string) {
	t.Helper()
	runtimeWindowsVCLibsFixtureMu.Lock()
	t.Cleanup(func() { runtimeWindowsVCLibsFixtureMu.Unlock() })
	runtimeRoot := filepath.Join(productRoot, "fixture-msvc-runtime")
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		t.Fatalf("create Windows VCLibs resolver fixture: %v", err)
	}
	runtimeWindowsVCLibsResolverMu.Lock()
	previous := runtimeWindowsVCLibsProcessPathResolver
	runtimeWindowsVCLibsProcessPathResolver = func(candidateRoot string) (string, error) {
		if strings.EqualFold(filepath.Clean(candidateRoot), filepath.Clean(productRoot)) {
			return runtimeRoot, nil
		}
		return previous(candidateRoot)
	}
	runtimeWindowsVCLibsResolverMu.Unlock()
	t.Cleanup(func() {
		runtimeWindowsVCLibsResolverMu.Lock()
		runtimeWindowsVCLibsProcessPathResolver = previous
		runtimeWindowsVCLibsResolverMu.Unlock()
	})
}

// runtimeTestSpecMatchesAnyLanguage 保持 Windows fixture 选择与生产注册表的语言集合一致。
func runtimeTestSpecMatchesAnyLanguage(spec runtimeInstallerSpec, languageIDs []string) bool {
	for _, registered := range spec.languages {
		for _, requested := range languageIDs {
			if registered == requested {
				return true
			}
		}
	}
	return false
}

func splitRuntimeExactNPMPackageForTest(t *testing.T, specification string) (string, string) {
	t.Helper()
	separator := strings.LastIndexByte(specification, '@')
	if separator <= 0 || separator == len(specification)-1 {
		t.Fatalf("invalid exact npm package fixture %q", specification)
	}
	return specification[:separator], specification[separator+1:]
}
