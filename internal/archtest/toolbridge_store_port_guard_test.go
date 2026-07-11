package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestToolbridgeHandlerNoDirectStoreImport is the P22 P4 S3d import-
// direction guard for internal/platform/toolbridge. P4 plan §48 lists
// handler.go as directly depending on internal/store/binding and
// internal/store/thread; the S3d refactor moves those concrete stores
// to module.go (assembly seam) behind narrow ports defined in
// ports.go — handler.go, proxy.go, and any other non-assembly file in
// toolbridge must not re-introduce the store imports.
//
// The guard scans every non-test .go file in internal/platform/toolbridge
// and fails if a store-package import appears in any file other than
// module.go (the legitimate assembly entry point where the concrete
// adapters live). Test files are out of scope — they legitimately build
// fixtures using store types.
func TestToolbridgeHandlerNoDirectStoreImport(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	dir := filepath.Join(root, "internal", "platform", "toolbridge")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	forbiddenImports := []string{
		`"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"`,
		`"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"`,
	}
	// module.go is the assembly entry point — it legitimately wires the
	// concrete store types through the narrow adapters defined there.
	// Every other production file must use the ports.go narrow interfaces.
	const assemblyFile = "module.go"

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == assemblyFile {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		for _, forbidden := range forbiddenImports {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s reintroduced forbidden store import %s (P4 §S3d: only module.go may import concrete store packages; other files must use ports.go narrow interfaces)", name, forbidden)
			}
		}
	}
}
