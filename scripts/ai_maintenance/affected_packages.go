package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

const goListDependencyTemplate = `{{.ImportPath}}{{"\t"}}{{join .Imports ","}}{{"\t"}}{{join .TestImports ","}}{{"\t"}}{{join .XTestImports ","}}`

// resolveDirectReverseDependentPackages 只追加直接生产导入方和测试导入方，避免递归扩大测试面。
func resolveDirectReverseDependentPackages(directPackages []string) ([]string, error) {
	if len(directPackages) == 0 {
		return nil, fmt.Errorf("backend gate has no directly affected Go packages")
	}
	modulePath, err := goCommandOutput("list", "-m", "-f", "{{.Path}}")
	if err != nil {
		return nil, fmt.Errorf("resolve Go module path: %w", err)
	}
	listing, err := goCommandOutput("list", "-f", goListDependencyTemplate, "./...")
	if err != nil {
		return nil, fmt.Errorf("resolve direct reverse Go dependencies: %w", err)
	}
	packages, err := selectDirectReverseDependentPackages(strings.TrimSpace(string(modulePath)), directPackages, listing)
	if err != nil {
		return nil, err
	}
	filtered, err := excludeDeferredE2EGoPackages(packages, "scripts/ai_maintenance/deferred_e2e_packages.txt")
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(filtered)+len(directPackages))
	for _, pkg := range filtered {
		selected[pkg] = true
	}
	for _, pkg := range directPackages {
		selected[pkg] = true
	}
	return sortedKeys(selected), nil
}

func goCommandOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Env = gateCommandEnvironment("go")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr.Bytes())
	}
	return stdout.Bytes(), nil
}

// selectDirectReverseDependentPackages 从 go list 行中选择直接变更包和一级反向依赖。
func selectDirectReverseDependentPackages(modulePath string, directPackages []string, listing []byte) ([]string, error) {
	if modulePath == "" {
		return nil, fmt.Errorf("Go module path is empty")
	}
	directImports, selected, err := directPackageSets(modulePath, directPackages)
	if err != nil {
		return nil, err
	}
	for lineNumber, line := range strings.Split(string(listing), "\n") {
		pkg, include, err := reverseDependentPackageForRow(modulePath, line, lineNumber+1, directImports)
		if err != nil {
			return nil, err
		}
		if include {
			selected[pkg] = true
		}
	}
	return sortedKeys(selected), nil
}

func directPackageSets(modulePath string, directPackages []string) (map[string]bool, map[string]bool, error) {
	directImports := make(map[string]bool, len(directPackages))
	selected := make(map[string]bool, len(directPackages))
	for _, pkg := range directPackages {
		if pkg != "." && !strings.HasPrefix(pkg, "./") {
			return nil, nil, fmt.Errorf("affected Go package must be relative: %q", pkg)
		}
		directImports[moduleImportPath(modulePath, pkg)] = true
		selected[pkg] = true
	}
	return directImports, selected, nil
}

// reverseDependentPackageForRow 严格解析一行依赖信息并判断是否属于受影响模块包。
func reverseDependentPackageForRow(modulePath, line string, lineNumber int, directImports map[string]bool) (string, bool, error) {
	line = strings.TrimSuffix(line, "\r")
	if line == "" {
		return "", false, nil
	}
	fields := strings.Split(line, "\t")
	if len(fields) != 4 {
		return "", false, fmt.Errorf("malformed go list dependency row %d: got %d fields", lineNumber, len(fields))
	}
	pkg, ok := relativeModulePackage(modulePath, fields[0])
	if !ok || pkg == "./internal/archtest" || strings.Contains(pkg, "/testdata/") {
		return "", false, nil
	}
	include := directImports[fields[0]] || importsAnyDirectPackage(fields[1:], directImports)
	return pkg, include, nil
}

func moduleImportPath(modulePath, pkg string) string {
	if pkg == "." {
		return modulePath
	}
	return modulePath + "/" + strings.TrimPrefix(pkg, "./")
}

func relativeModulePackage(modulePath, importPath string) (string, bool) {
	if importPath == modulePath {
		return ".", true
	}
	prefix := modulePath + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", false
	}
	return "./" + strings.TrimPrefix(importPath, prefix), true
}

// importsAnyDirectPackage 检查生产、同包测试或外部测试导入是否直接命中变更包。
func importsAnyDirectPackage(importFields []string, directImports map[string]bool) bool {
	for _, field := range importFields {
		for imported := range strings.SplitSeq(field, ",") {
			if directImports[imported] {
				return true
			}
		}
	}
	return false
}
