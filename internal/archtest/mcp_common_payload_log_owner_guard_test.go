package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestMCPCommonPayloadLogSequenceOwnedByServer 防止 MCP 载荷日志序号退回为包级运行时 owner。
func TestMCPCommonPayloadLogSequenceOwnedByServer(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	payloadFile := mcpPayloadParseFile(t, root, "internal/mcpserver/common/tool_payload_log.go")
	serverFile := mcpPayloadParseFile(t, root, "internal/mcpserver/common/server.go")
	httpFile := mcpPayloadParseFile(t, root, "internal/mcpserver/common/http_transport.go")
	violations := mcpPayloadSequenceViolations(payloadFile)
	if !mcpPayloadLoggerUsesExclusiveCreate(payloadFile) {
		violations = append(violations, "internal/mcpserver/common/tool_payload_log.go: toolPayloadLogger.write must use os.O_CREATE|os.O_EXCL")
	}
	for _, check := range []struct {
		file       *ast.File
		structName string
	}{
		{file: serverFile, structName: "Server"},
		{file: httpFile, structName: "HTTPServer"},
	} {
		if !mcpPayloadStructHasLogger(check.file, check.structName) {
			violations = append(violations, fmt.Sprintf("internal/mcpserver/common: %s must own payloadLogger toolPayloadLogger", check.structName))
		}
	}
	if len(violations) > 0 {
		t.Fatalf("MCP common payload log owner violations:\n%s", strings.Join(violations, "\n"))
	}
}

func mcpPayloadParseFile(t *testing.T, root, rel string) *ast.File {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func mcpPayloadSequenceViolations(file *ast.File) []string {
	var violations []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if ok && mcpPayloadValueSpecHasAtomicUint64(value) {
				violations = append(violations, "internal/mcpserver/common/tool_payload_log.go: package-level atomic.Uint64 runtime owner is forbidden")
			}
		}
	}
	return violations
}

func mcpPayloadValueSpecHasAtomicUint64(spec *ast.ValueSpec) bool {
	if mcpPayloadIsAtomicUint64(spec.Type) {
		return true
	}
	for _, value := range spec.Values {
		literal, ok := value.(*ast.CompositeLit)
		if ok && mcpPayloadIsAtomicUint64(literal.Type) {
			return true
		}
	}
	return false
}

func mcpPayloadIsAtomicUint64(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Uint64" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "atomic"
}

func mcpPayloadStructHasLogger(file *ast.File, structName string) bool {
	for _, decl := range file.Decls {
		if mcpPayloadDeclHasLogger(decl, structName) {
			return true
		}
	}
	return false
}

func mcpPayloadDeclHasLogger(decl ast.Decl, structName string) bool {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.TYPE {
		return false
	}
	for _, spec := range gen.Specs {
		if mcpPayloadTypeSpecHasLogger(spec, structName) {
			return true
		}
	}
	return false
}

func mcpPayloadTypeSpecHasLogger(spec ast.Spec, structName string) bool {
	typeSpec, ok := spec.(*ast.TypeSpec)
	if !ok || typeSpec.Name.Name != structName {
		return false
	}
	structType, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		return false
	}
	for _, field := range structType.Fields.List {
		ident, typeOK := field.Type.(*ast.Ident)
		if typeOK && ident.Name == "toolPayloadLogger" && mcpPayloadFieldNamed(field, "payloadLogger") {
			return true
		}
	}
	return false
}

func mcpPayloadFieldNamed(field *ast.Field, target string) bool {
	for _, name := range field.Names {
		if name.Name == target {
			return true
		}
	}
	return false
}

func mcpPayloadLoggerUsesExclusiveCreate(file *ast.File) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "write" || fn.Body == nil {
			continue
		}
		usesExclusiveCreate := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !mcpPayloadIsOSOpenFile(call.Fun) || len(call.Args) < 3 {
				return true
			}
			if mcpPayloadExpressionContainsOSExclusive(call.Args[1]) && mcpPayloadExpressionContainsOSCreate(call.Args[1]) {
				usesExclusiveCreate = true
			}
			return true
		})
		return usesExclusiveCreate
	}
	return false
}

func mcpPayloadIsOSOpenFile(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OpenFile" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "os"
}

func mcpPayloadExpressionContainsOSExclusive(expr ast.Expr) bool {
	return mcpPayloadExpressionContainsOSFlag(expr, "O_EXCL")
}

func mcpPayloadExpressionContainsOSCreate(expr ast.Expr) bool {
	return mcpPayloadExpressionContainsOSFlag(expr, "O_CREATE")
}

func mcpPayloadExpressionContainsOSFlag(expr ast.Expr, want string) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != want {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if ok && ident.Name == "os" {
			found = true
		}
		return true
	})
	return found
}

func TestMCPPayloadSequenceGuardDetectsPackageAtomicOwner(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "payload.go", `package common
import "sync/atomic"
var toolPayloadLogSeq atomic.Uint64
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse package atomic fixture: %v", err)
	}
	if violations := mcpPayloadSequenceViolations(file); len(violations) == 0 {
		t.Fatal("mcpPayloadSequenceViolations() did not reject package atomic owner")
	}
}

func TestMCPPayloadExclusiveCreateGuardRejectsWriteFile(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "payload.go", `package common
import "os"
func (l *toolPayloadLogger) write() { _ = os.WriteFile("snapshot", nil, 0o600) }
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse non-exclusive fixture: %v", err)
	}
	if mcpPayloadLoggerUsesExclusiveCreate(file) {
		t.Fatal("mcpPayloadLoggerUsesExclusiveCreate() accepted os.WriteFile")
	}
}
