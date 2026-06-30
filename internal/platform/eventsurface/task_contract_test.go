package eventsurface

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var skeletonTaskMethodRE = regexp.MustCompile("-> `([^`]+)`")

func TestTaskEventSurfaceMatchesSkeletonContract(t *testing.T) {
	root := repoRootForEventSurfaceTest(t)
	wantMethods := skeletonTaskMethods(t, root)

	assertStringSetEqual(t, "task wire methods", wantMethods, taskWireMethods(AllTypedWireMethods()))
	assertStringSetEqual(t, "task event DTO types", []string{"TaskNodeStatusChanged"}, goNamesWithPrefix(
		t,
		filepath.Join(root, "internal/dto/task/event.go"),
		token.TYPE,
		"Task",
	))
	assertStringSetEqual(t, "task event type constants", []string{"EventTypeTaskNodeStatusChanged"}, goNamesWithPrefix(
		t,
		filepath.Join(root, "internal/dto/shared/event.go"),
		token.CONST,
		"EventTypeTask",
	))
}

func skeletonTaskMethods(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "docs/架构/skeleton-event.md"))
	if err != nil {
		t.Fatalf("read skeleton event contract: %v", err)
	}
	matches := skeletonTaskMethodRE.FindAllSubmatch(raw, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		method := string(match[1])
		if strings.HasPrefix(method, "task/") {
			out = append(out, method)
		}
	}
	return out
}

func taskWireMethods(methods []string) []string {
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		if strings.HasPrefix(method, "task/") {
			out = append(out, method)
		}
	}
	return out
}

func goNamesWithPrefix(t *testing.T, filePath string, kind token.Token, prefix string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), filePath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filePath, err)
	}
	out := []string{}
	for _, decl := range parsed.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != kind {
			continue
		}
		for _, spec := range genDecl.Specs {
			out = append(out, prefixedSpecNames(spec, prefix)...)
		}
	}
	return out
}

func prefixedSpecNames(spec ast.Spec, prefix string) []string {
	switch typed := spec.(type) {
	case *ast.TypeSpec:
		if strings.HasPrefix(typed.Name.Name, prefix) {
			return []string{typed.Name.Name}
		}
	case *ast.ValueSpec:
		out := []string{}
		for _, name := range typed.Names {
			if strings.HasPrefix(name.Name, prefix) {
				out = append(out, name.Name)
			}
		}
		return out
	}
	return nil
}
