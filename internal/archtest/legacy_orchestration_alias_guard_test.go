package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestLegacyOrchestrationStringsStayInToolbridgeAliasIsolation 防止旧 orch 名重新散落到 toolbridge 生产代码。
// toolbridge 只能通过 handler_peer_alias.go 包装 contract 里的 legacy peer realName / deny-only 名称。
func TestLegacyOrchestrationStringsStayInToolbridgeAliasIsolation(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	dir := filepath.Join(root, "internal/platform/toolbridge")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Name() == "handler_peer_alias.go" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "orchestration_") {
			t.Errorf("%s contains legacy orchestration literal; route it through handler_peer_alias.go", path)
		}
	}
}

func TestProductionCodeDoesNotDefineLegacyOrchestrationLiteralLists(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	var violations []legacyOrchestrationLiteralListViolation
	for _, scope := range legacyOrchestrationLiteralListScopes() {
		paths := legacyOrchestrationLiteralListSourceFiles(t, root, scope.relPath)
		for _, path := range paths {
			fileViolations, err := legacyOrchestrationLiteralListViolations(path)
			if err != nil {
				t.Fatalf("scan legacy orchestration literals in %s: %v", path, err)
			}
			for _, violation := range fileViolations {
				violation.owner = scope.owner
				violation.reason = scope.reason
				violations = append(violations, violation)
			}
		}
	}
	if len(violations) == 0 {
		return
	}
	for _, violation := range violations {
		t.Errorf("%s:%d defines legacy orchestration literal list %v (owner=%s reason=%s); use contract OrchestrationToolCanonicalNames/OrchestrationToolLegacyPeerRealNames/OrchestrationToolHiddenAliases helpers instead",
			violation.relPath, violation.line, violation.names, violation.owner, violation.reason)
	}
}

func TestProductionCodeDoesNotOwnLegacyOrchestrationStringLiterals(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	forbidden := legacyOrchestrationForbiddenLiteralNames()
	var violations []legacyOrchestrationLiteralListViolation
	for _, relPath := range []string{"internal", "cmd"} {
		paths := legacyOrchestrationLiteralListSourceFiles(t, root, relPath)
		for _, path := range paths {
			if legacyOrchestrationLiteralAllowedSource(root, path) {
				continue
			}
			fileViolations, err := legacyOrchestrationStringLiteralViolations(path, forbidden)
			if err != nil {
				t.Fatalf("scan legacy orchestration literals in %s: %v", path, err)
			}
			violations = append(violations, fileViolations...)
		}
	}
	if len(violations) == 0 {
		return
	}
	for _, violation := range violations {
		t.Errorf("%s:%d owns legacy orchestration literal(s) %v; consume internal/contract orchestration registry helpers instead",
			violation.relPath, violation.line, violation.names)
	}
}

func TestLegacyOrchestrationLiteralListGuardCatchesDuplicateFactSource(t *testing.T) {
	t.Parallel()

	src := `package fixture

var tools = []string{"launch_agent", "orchestration_launch_agent"}
`
	violations, err := legacyOrchestrationLiteralListViolationsFromSource("internal/module/prompt/fixture.go", []byte(src))
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("violations = %#v, want one duplicate fact source violation", violations)
	}
	if violations[0].line != 3 || !sameStringSet(violations[0].names, []string{"orchestration_launch_agent"}) {
		t.Fatalf("violation = %#v, want line 3 orchestration_launch_agent", violations[0])
	}
}

func TestLegacyOrchestrationLiteralGuardCatchesSingleFactSource(t *testing.T) {
	t.Parallel()

	src := `package fixture

var launch = "orchestration_launch_agent"
`
	violations, err := legacyOrchestrationStringLiteralViolationsFromSource("internal/provider/fixture.go", []byte(src), legacyOrchestrationForbiddenLiteralNames())
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("violations = %#v, want one single literal violation", violations)
	}
	if violations[0].line != 3 || !sameStringSet(violations[0].names, []string{"orchestration_launch_agent"}) {
		t.Fatalf("violation = %#v, want line 3 orchestration_launch_agent", violations[0])
	}
}

type legacyOrchestrationLiteralListScope struct {
	relPath string
	owner   string
	reason  string
}

type legacyOrchestrationLiteralListViolation struct {
	relPath string
	line    int
	names   []string
	owner   string
	reason  string
}

func legacyOrchestrationLiteralListScopes() []legacyOrchestrationLiteralListScope {
	return []legacyOrchestrationLiteralListScope{
		{
			relPath: "internal/provider/toolfilter",
			owner:   "prompt-tool-consumer-guard",
			reason:  "toolfilter presets must consume the contract/toolbridge alias registry instead of owning orchestration string lists",
		},
		{
			relPath: "internal/contract/prompt.go",
			owner:   "prompt-contract-guard",
			reason:  "read-only prompt contract must append the orchestration alias registry instead of duplicating legacy peer names",
		},
		{
			relPath: "internal/module/prompt",
			owner:   "prompt-consumer-guard",
			reason:  "prompt consumers must normalize through registry-aware helpers instead of local orchestration lists",
		},
		{
			relPath: "internal/module/thread",
			owner:   "thread-consumer-guard",
			reason:  "thread consumers must not grow their own orchestration callable alias list",
		},
		{
			relPath: "internal/module/turn",
			owner:   "turn-consumer-guard",
			reason:  "turn consumers must not duplicate orchestration alias facts",
		},
		{
			relPath: "internal/module/uistate",
			owner:   "uistate-consumer-guard",
			reason:  "UI state classification must not reintroduce legacy orchestration callable alias lists",
		},
	}
}

func legacyOrchestrationLiteralListSourceFiles(t *testing.T, root, relPath string) []string {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			return []string{path}
		}
		return nil
	}

	var paths []string
	if err := filepath.WalkDir(path, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", path, err)
	}
	return paths
}

func legacyOrchestrationLiteralListViolations(path string) ([]legacyOrchestrationLiteralListViolation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return legacyOrchestrationLiteralListViolationsFromSource(path, data)
}

func legacyOrchestrationLiteralListViolationsFromSource(path string, data []byte) ([]legacyOrchestrationLiteralListViolation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var violations []legacyOrchestrationLiteralListViolation
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		names := legacyOrchestrationStringLiterals(lit)
		if len(names) == 0 {
			return true
		}
		violations = append(violations, legacyOrchestrationLiteralListViolation{
			relPath: filepath.ToSlash(path),
			line:    fset.Position(lit.Pos()).Line,
			names:   names,
		})
		return true
	})
	return violations, nil
}

func legacyOrchestrationForbiddenLiteralNames() map[string]struct{} {
	names := append(contract.OrchestrationToolLegacyPeerRealNames(), contract.OrchestrationToolHiddenAliases()...)
	forbidden := make(map[string]struct{}, len(names))
	for _, name := range names {
		forbidden[name] = struct{}{}
	}
	return forbidden
}

func legacyOrchestrationLiteralAllowedSource(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	switch filepath.ToSlash(rel) {
	case "internal/contract/orchestration.go":
		return true
	default:
		return false
	}
}

func legacyOrchestrationStringLiteralViolations(path string, forbidden map[string]struct{}) ([]legacyOrchestrationLiteralListViolation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return legacyOrchestrationStringLiteralViolationsFromSource(path, data, forbidden)
}

func legacyOrchestrationStringLiteralViolationsFromSource(path string, data []byte, forbidden map[string]struct{}) ([]legacyOrchestrationLiteralListViolation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, data, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var violations []legacyOrchestrationLiteralListViolation
	ast.Inspect(file, func(node ast.Node) bool {
		basic, ok := node.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(basic.Value)
		if err != nil {
			return true
		}
		if _, ok := forbidden[value]; !ok {
			return true
		}
		violations = append(violations, legacyOrchestrationLiteralListViolation{
			relPath: filepath.ToSlash(path),
			line:    fset.Position(basic.Pos()).Line,
			names:   []string{value},
		})
		return true
	})
	return violations, nil
}

func legacyOrchestrationStringLiterals(expr ast.Expr) []string {
	var names []string
	seen := map[string]struct{}{}
	ast.Inspect(expr, func(node ast.Node) bool {
		basic, ok := node.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(basic.Value)
		if err != nil || !strings.HasPrefix(value, "orchestration_") {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
		names = append(names, value)
		return true
	})
	return names
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		seen[value]--
		if seen[value] < 0 {
			return false
		}
	}
	return true
}
