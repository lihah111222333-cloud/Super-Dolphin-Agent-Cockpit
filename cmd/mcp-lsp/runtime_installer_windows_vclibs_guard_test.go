//go:build windows

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestWindowsProductionInstallActionsProvisionVCLibsBeforeProduct 守卫三类生产
// 安装和 ARM64 EmmyLua 都必须先供应 VC++ runtime，再触碰目标 runtime/server。
func TestWindowsProductionInstallActionsProvisionVCLibsBeforeProduct(t *testing.T) {
	file := parseWindowsRuntimeInstallerGuardFile(t)
	targets := map[string]string{
		"windowsNodeInstallAction":              "installer.NewWindowsNodeRuntime",
		"windowsEmmyLuaInstallAction":           "installer.ProvisionWindowsEmmyLua",
		"windowsNativeInstallAction":            "installer.NewWindowsLSPAssetCache",
		"windowsRuntimeDependencyInstallAction": "installer.ProvisionWindowsRuntimeDependency",
	}
	for functionName, targetCall := range targets {
		declaration := windowsGuardFunction(t, file, functionName)
		vclibsPosition := windowsGuardCallPosition(declaration, "ensureWindowsVCLibsForInstall")
		targetPosition := windowsGuardCallPosition(declaration, targetCall)
		if !vclibsPosition.IsValid() || !targetPosition.IsValid() {
			t.Fatalf("%s calls: VCLibs=%v target(%s)=%v", functionName, vclibsPosition, targetCall, targetPosition)
		}
		if vclibsPosition >= targetPosition {
			t.Fatalf("%s provisions product before Windows VCLibs: VCLibs=%v target=%v", functionName, vclibsPosition, targetPosition)
		}
	}
}

// TestWindowsProductResolversRequireReadOnlyVCLibs 守卫重启后的产品 cache resolver
// 在返回 server 路径前只读复验 VC++ cache，不能依赖首次安装进程的 PATH 副作用。
func TestWindowsProductResolversRequireReadOnlyVCLibs(t *testing.T) {
	file := parseWindowsRuntimeInstallerGuardFile(t)
	for _, functionName := range []string{
		"windowsNodeBinaryPathResolver",
		"windowsEmmyLuaBinaryPathResolver",
		"windowsNativeBinaryPathResolver",
		"windowsRuntimeDependencyBinaryPathResolver",
	} {
		declaration := windowsGuardFunction(t, file, functionName)
		if position := windowsGuardCallPosition(declaration, "validateWindowsVCLibsForResolver"); !position.IsValid() {
			t.Fatalf("%s does not require read-only Windows VCLibs validation", functionName)
		}
	}
}

// parseWindowsRuntimeInstallerGuardFile 解析 Windows 生产安装器用于 AST 架构守卫。
func parseWindowsRuntimeInstallerGuardFile(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "runtime_installer_windows.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse runtime_installer_windows.go: %v", err)
	}
	return file
}

// windowsGuardFunction 按精确函数名返回生产声明。
func windowsGuardFunction(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return function
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

// windowsGuardCallPosition 返回目标函数内第一次指定调用的位置。
func windowsGuardCallPosition(function *ast.FuncDecl, name string) token.Pos {
	position := token.NoPos
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || windowsGuardCallName(call) != name {
			return true
		}
		if !position.IsValid() || call.Pos() < position {
			position = call.Pos()
		}
		return true
	})
	return position
}

// windowsGuardCallName 规范化同包调用和单层包选择器调用名称。
func windowsGuardCallName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		if qualifier, ok := function.X.(*ast.Ident); ok {
			return qualifier.Name + "." + function.Sel.Name
		}
	}
	return ""
}
