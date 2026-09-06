package runtimeeventwriter

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCandidateIngressAdapterRemainsUnwired 防止 candidate adapter 被生产入口注册或接入 provider/Fx/tool。
func TestCandidateIngressAdapterRemainsUnwired(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "../../../../"))
	owned := filepath.Dir(current)
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		err := scanAdapterProductionTree(filepath.Join(root, tree), owned)
		if err != nil {
			t.Fatalf("production wiring in %s: %v", tree, err)
		}
	}
}

func scanAdapterProductionTree(root, owned string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skipAdapterGuardPath(path, entry, owned) {
			return nil
		}
		return checkAdapterGuardSource(path)
	})
}

func skipAdapterGuardPath(path string, entry os.DirEntry, owned string) bool {
	return entry.IsDir() || filepath.Ext(path) != ".go" ||
		strings.HasPrefix(filepath.Clean(path), owned+string(filepath.Separator)) || strings.HasSuffix(path, "_test.go")
}

func checkAdapterGuardSource(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(contents)
	if strings.Contains(text, "NewCandidateIngressAdapter(") || strings.Contains(text, "NewTaskCandidateIngressAdapter(") {
		return &candidateIngressAdapterWiringViolation{path: path}
	}
	return nil
}

type candidateIngressAdapterWiringViolation struct{ path string }

func (v *candidateIngressAdapterWiringViolation) Error() string {
	return "candidate runtime event ingress adapter referenced by " + v.path
}
