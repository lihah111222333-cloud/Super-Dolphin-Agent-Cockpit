package remoteci

import (
	"strings"
	"testing"
)

// TestRemoteGoWorkspaceModuleMappingsBindsEngineAndTarget 锁定 engine/target workspace 的显式 module 映射。
func TestRemoteGoWorkspaceModuleMappingsBindsEngineAndTarget(t *testing.T) {
	sources := map[string][]byte{
		"go.mod":                           []byte("module example.invalid/engine\n\ngo 1.26.5\n"),
		"go.work":                          []byte("go 1.26.5\n\nuse (\n\t.\n\t./portable-target/remote-ci\n)\n"),
		"portable-target/remote-ci/go.mod": []byte("module example.invalid/target/remote-ci\n\ngo 1.26.5\n"),
	}
	mappings, err := remoteGoModuleMappings(sources)
	if err != nil {
		t.Fatalf("remoteGoModuleMappings: %v", err)
	}
	if len(mappings) != 2 {
		t.Fatalf("module mapping count = %d, want 2", len(mappings))
	}
	want := map[string]string{
		"example.invalid/engine":           ".",
		"example.invalid/target/remote-ci": "portable-target/remote-ci",
	}
	for _, mapping := range mappings {
		if want[mapping.importPath] != mapping.directory {
			t.Fatalf("mapping %q = %q, want %q", mapping.importPath, mapping.directory, want[mapping.importPath])
		}
		delete(want, mapping.importPath)
	}
	if len(want) != 0 {
		t.Fatalf("missing workspace mappings: %v", want)
	}
}

// TestRemoteGoWorkspaceModuleMappingsRejectsUnboundWorkspace 锁定缺根、越界、重复与 workspace replace 拒绝。
func TestRemoteGoWorkspaceModuleMappingsRejectsUnboundWorkspace(t *testing.T) {
	tests := []struct {
		name    string
		work    string
		modules map[string]string
		want    string
	}{
		{name: "missing root", work: "go 1.26.5\nuse ./target\n", modules: map[string]string{"target/go.mod": "module example.invalid/target\n"}, want: "root module"},
		{name: "escape", work: "go 1.26.5\nuse (\n.\n../target\n)\n", want: "escapes"},
		{name: "duplicate module", work: "go 1.26.5\nuse (\n.\n./target\n)\n", modules: map[string]string{"target/go.mod": "module example.invalid/engine\n"}, want: "duplicate module path"},
		{name: "workspace replace", work: "go 1.26.5\nuse .\nreplace example.invalid/dependency => ./dependency\n", want: "workspace is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sources := map[string][]byte{"go.mod": []byte("module example.invalid/engine\n"), "go.work": []byte(test.work)}
			for name, data := range test.modules {
				sources[name] = []byte(data)
			}
			_, err := remoteGoModuleMappings(sources)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("remoteGoModuleMappings error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestInventoryFullTargetsIncludesOnlyModelledWorkspaceModules 锁定 workspace use 可见且普通 nested module 继续隔离。
func TestInventoryFullTargetsIncludesOnlyModelledWorkspaceModules(t *testing.T) {
	files := []string{
		"go.mod", "internal/engine/engine.go",
		"portable-target/remote-ci/go.mod", "portable-target/remote-ci/internal/devtools/gate/gate.go",
		"third_party/unmodelled/go.mod", "third_party/unmodelled/package.go",
	}
	targets, err := inventoryFullTargets(files, inventoryVitestSuitePolicy{}, []string{".", "portable-target/remote-ci"})
	if err != nil {
		t.Fatalf("inventoryFullTargets: %v", err)
	}
	want := map[string]bool{"./internal/engine": true, "./portable-target/remote-ci/internal/devtools/gate": true}
	for _, target := range targets.goPackages {
		if !want[target] {
			t.Fatalf("unexpected Go package target %q", target)
		}
		delete(want, target)
	}
	if len(want) != 0 {
		t.Fatalf("missing Go package targets: %v", want)
	}
}
