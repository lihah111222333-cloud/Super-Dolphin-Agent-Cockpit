package archtest

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"golang.org/x/tools/go/ssa"
)

// TestHeterociManifestFileProductionSignature 冻结唯一显式文件 loader 的公开签名。
// package-wide producer 与调用闭包由 SSA guard 按 go/types 对象身份判定。
func TestHeterociManifestFileProductionSignature(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "internal", "devtools", "heteroci", "manifest_file.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse manifest file loader source: %v", err)
	}
	var definitions []*ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "LoadManifestFile" {
			definitions = append(definitions, function)
		}
	}
	if len(definitions) != 1 {
		t.Fatalf("LoadManifestFile definitions in manifest_file.go = %d, want 1", len(definitions))
	}
	assertHeterociManifestFileSignature(t, definitions[0])
}

func assertHeterociManifestFileSignature(t *testing.T, function *ast.FuncDecl) {
	t.Helper()
	if function.Recv != nil || function.Type.Params == nil || heterociManifestFileFieldCount(function.Type.Params.List) != 1 ||
		heterociManifestFileTypeName(function.Type.Params.List[0].Type) != "string" {
		t.Fatal("LoadManifestFile parameter boundary is not exact func(string) (Manifest, error)")
	}
	if function.Type.Results == nil || heterociManifestFileFieldCount(function.Type.Results.List) != 2 || len(function.Type.Results.List) != 2 {
		t.Fatal("LoadManifestFile result arity is not exact func(string) (Manifest, error)")
	}
	if heterociManifestFileTypeName(function.Type.Results.List[0].Type) != "Manifest" ||
		heterociManifestFileTypeName(function.Type.Results.List[1].Type) != "error" {
		t.Fatal("LoadManifestFile result types are not exact func(string) (Manifest, error)")
	}
}

// TestHeterociManifestFilePathAndStrictDecodeBehavior 绑定绝对常规文件与严格 decoder。
func TestHeterociManifestFilePathAndStrictDecodeBehavior(t *testing.T) {
	want := heterociManifestFileFixture(t)
	root := t.TempDir()
	path := filepath.Join(root, "manifest.json")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		t.Fatalf("test manifest path is not canonical absolute: %q", path)
	}
	valid := heterociManifestFileJSON(t, want)
	writeHeterociManifestFile(t, path, valid)

	got, err := heteroci.LoadManifestFile(path)
	if err != nil {
		t.Fatalf("LoadManifestFile(%q): %v", path, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadManifestFile(%q) = %#v, want %#v", path, got, want)
	}
	if _, err := heteroci.LoadManifestFile(root); err == nil {
		t.Fatal("LoadManifestFile unexpectedly accepted a directory")
	}

	malformed := [][]byte{
		heterociManifestFileAddUnknownField(t, valid),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
	}
	for index, raw := range malformed {
		malformedPath := filepath.Join(root, "malformed-"+string(rune('a'+index))+".json")
		writeHeterociManifestFile(t, malformedPath, raw)
		if _, err := heteroci.LoadManifestFile(malformedPath); err == nil {
			t.Fatalf("LoadManifestFile accepted malformed strict document %q", malformedPath)
		}
	}
}

// TestHeterociManifestFileRejectsImplicitPathDiscovery 固定无默认路径的显式输入边界。
func TestHeterociManifestFileRejectsImplicitPathDiscovery(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "manifest.json")
	writeHeterociManifestFile(t, canonical, heterociManifestFileJSON(t, heterociManifestFileFixture(t)))
	dirty := filepath.Dir(canonical) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(canonical)
	for _, name := range []string{
		"HETEROCI_MANIFEST_PATH",
		"HETEROCI_CONFIG_PATH",
		"SUPER_DOLPHIN_HETEROCI_MANIFEST_PATH",
		"SUPER_DOLPHIN_MANIFEST_PATH",
		"SUPER_DOLPHIN_HETEROCI_CONFIG_PATH",
	} {
		t.Setenv(name, canonical)
	}
	for _, path := range []string{"", "manifest.json", dirty} {
		if _, err := heteroci.LoadManifestFile(path); err == nil {
			t.Fatalf("LoadManifestFile(%q) unexpectedly accepted implicit or dirty path", path)
		}
	}
}

// TestHeterociManifestFileMissingErrorIsRedacted 保留 errors.Is 根因且不泄漏路径。
func TestHeterociManifestFileMissingErrorIsRedacted(t *testing.T) {
	const secret = "heteroci-manifest-secret-5c4a"
	path := filepath.Join(t.TempDir(), secret+".json")
	_, err := heteroci.LoadManifestFile(path)
	if err == nil {
		t.Fatalf("LoadManifestFile(%q) unexpectedly succeeded", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadManifestFile(%q) error = %v, want os.ErrNotExist", path, err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("LoadManifestFile(%q) leaked secret path component: %v", path, err)
	}
}

func heterociManifestFileTypeName(expression ast.Expr) string {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func heterociManifestFileFieldCount(fields []*ast.Field) int {
	count := 0
	for _, field := range fields {
		if len(field.Names) == 0 {
			count++
			continue
		}
		count += len(field.Names)
	}
	return count
}

func heterociManifestFileFixture(t *testing.T) heteroci.Manifest {
	t.Helper()
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Nodes: []heteroci.NodeConfig{{
			NodeID:   "manifest-file-guard-node",
			Endpoint: "https://manifest-file-guard.invalid/runner", ProbeIdentity: "sha256:" + strings.Repeat("a", 64),
			Platform:     heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("b", 64)}},
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest fixture validation: %v", err)
	}
	return manifest
}

func heterociManifestFileJSON(t *testing.T, manifest heteroci.Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest fixture: %v", err)
	}
	return raw
}

func heterociManifestFileAddUnknownField(t *testing.T, valid []byte) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(valid, &fields); err != nil {
		t.Fatalf("unmarshal valid manifest fixture: %v", err)
	}
	fields["unknown_guard_field"] = json.RawMessage(`true`)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal malformed manifest fixture: %v", err)
	}
	return raw
}

func writeHeterociManifestFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write manifest file %q: %v", path, err)
	}
}

func dedupeHeterociManifestSSAStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func assertHeterociManifestSSAFinding(t *testing.T, findings []string, fragments ...string) {
	t.Helper()
	for _, finding := range findings {
		matched := true
		for _, fragment := range fragments {
			matched = matched && strings.Contains(finding, fragment)
		}
		if matched {
			return
		}
	}
	t.Fatalf("SSA findings %q do not contain %q", findings, fragments)
}

func TestHeterociManifestFileSSARejectsInterfaceInvokeEnvironmentHelper(t *testing.T) {
	overlay := heterociManifestSSALoaderOverlay(t, `package heteroci
import (
	stdos "os"
	"strings"
)
type pathSource interface { Path() string }
type environmentPathSource struct{}
func (environmentPathSource) Path() string { return stdos.Getenv("HETEROCI_MANIFEST_PATH") }
func LoadManifestFile(path string) (Manifest, error) {
	var source pathSource = environmentPathSource{}
	_ = source.Path()
	manifest, err := DecodeManifest(strings.NewReader(path))
	return finishManifestFileLoad(manifest, err, func() error { return nil })
}
`)
	assertHeterociManifestSSAFinding(t, loadHeterociManifestSSAGuard(t, overlay).findings(), "interface-invoke", "Path")
}

func heterociManifestSSAInvokeFindings(
	guard *heterociManifestSSAGuard,
	function *ssa.Function,
) []string {
	var findings []string
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil || !call.Common().IsInvoke() {
				continue
			}
			if function.Object() == guard.loader && guard.allowedInvoke[call.Common().Method] {
				continue
			}
			method := "<unknown>"
			if call.Common().Method != nil {
				method = call.Common().Method.Name()
			}
			findings = append(findings, "interface-invoke "+guard.functionIdentity(function)+" dispatches "+method)
		}
	}
	return findings
}

func heterociManifestSSAValueSeen(value ssa.Value, seen map[ssa.Value]bool) bool {
	return value == nil || seen[value]
}

func findHeterociManifestSSATypesPackage(
	root *types.Package,
	path string,
	seen map[*types.Package]bool,
) *types.Package {
	if root == nil || seen[root] {
		return nil
	}
	seen[root] = true
	if root.Path() == path {
		return root
	}
	for _, imported := range root.Imports() {
		if found := findHeterociManifestSSATypesPackage(imported, path, seen); found != nil {
			return found
		}
	}
	return nil
}
