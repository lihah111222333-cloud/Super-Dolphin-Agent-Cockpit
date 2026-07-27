package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// affectedGoPackages 对普通变更只保留当前树中仍存在的直接包；全局输入、删除整包或无法定位包时使用广域回归集合。
func affectedGoPackages(repoRoot string, files []string) ([]string, error) {
	packages := map[string]bool{}
	for _, file := range files {
		pkg, ok := changedGoPackage(file)
		if !ok || pkg == "./internal/archtest" {
			continue
		}
		exists, err := currentGoPackageExists(repoRoot, pkg)
		if err != nil {
			return nil, err
		}
		if exists {
			packages[pkg] = true
		}
	}
	fullRegression, err := requiresFullWorkspaceRegression(repoRoot, files)
	if err != nil {
		return nil, err
	}
	if fullRegression || len(packages) == 0 {
		for _, pkg := range coreBackendGatePackages {
			packages[pkg] = true
		}
	}
	return sortedKeys(packages), nil
}

// requiresFullWorkspaceRegression 在全局输入变化或任一旧 Go 包已从当前树消失时要求全工作区回归。
func requiresFullWorkspaceRegression(repoRoot string, files []string) (bool, error) {
	if requiresBroadBackendRegression(files) {
		return true, nil
	}
	for _, file := range files {
		pkg, ok := changedGoPackage(file)
		if !ok {
			continue
		}
		exists, err := currentGoPackageExists(repoRoot, pkg)
		if err != nil {
			return false, err
		}
		if !exists {
			return true, nil
		}
	}
	return false, nil
}

// requiresBroadBackendRegression 判断变更是否会影响整个 Go 工作区的构建或门禁行为。
func requiresBroadBackendRegression(files []string) bool {
	for _, file := range files {
		if goModuleFile(file) {
			return true
		}
		switch file {
		case "Makefile", "scripts/forbid_raw_go_test.sh", "scripts/real_go_resolver.sh",
			"scripts/test_with_guard.sh", "scripts/test_with_guard.ps1":
			return true
		}
	}
	return false
}

// changedGoPackage 将受支持的 Go 源码路径映射到最近的生产包所有者。
func changedGoPackage(file string) (string, bool) {
	if !strings.HasSuffix(file, ".go") {
		return "", false
	}
	switch {
	case strings.HasPrefix(file, "cmd/"),
		strings.HasPrefix(file, "internal/"),
		strings.HasPrefix(file, "pkg/"),
		strings.HasPrefix(file, "scripts/"):
		dir := filepath.ToSlash(filepath.Dir(file))
		if marker := strings.Index("/"+dir+"/", "/testdata/"); marker >= 0 {
			dir = strings.TrimPrefix(("/" + dir + "/")[:marker], "/")
		}
		if dir == "." || dir == "" {
			return "", false
		}
		return "./" + dir, true
	default:
		return "", false
	}
}

// currentGoPackageExists 确认候选包在当前工作树仍包含 Go 源码。
func currentGoPackageExists(repoRoot, pkg string) (bool, error) {
	dir := filepath.Join(repoRoot, filepath.FromSlash(strings.TrimPrefix(pkg, "./")))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read affected Go package %q: %w", pkg, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			return true, nil
		}
	}
	return false, nil
}
