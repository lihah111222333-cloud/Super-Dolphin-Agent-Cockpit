//go:build !darwin

package toolbridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestSchemaHelperProductionPathNonDarwinIgnoresProjectRootAndEnvironment 验证非 Darwin
// production helper 只从宿主可执行文件同目录解析，不接受项目根或环境变量覆盖。
func TestSchemaHelperProductionPathNonDarwinIgnoresProjectRootAndEnvironment(t *testing.T) {
	t.Setenv("PROJECT_ROOT", filepath.Join(t.TempDir(), "attacker"))
	dir, err := schemaHelperDirectory(filepath.Join(t.TempDir(), "controlled"), contract.DependencyProfileProduction)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Dir(executable)
	if dir != want {
		t.Fatalf("non-Darwin production helper dir = %q, want executable-owned %q", dir, want)
	}
}

// TestPackagedSchemaHelperDirectoryNonDarwinDoesNotInterpretAppBundle 证明 Windows、Linux
// 与其他非 Darwin 目标不会把普通 MacOS/Contents 字样解释为 app bundle 发布布局。
func TestPackagedSchemaHelperDirectoryNonDarwinDoesNotInterpretAppBundle(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "Contents", "MacOS", "super-dolphin")
	want := filepath.Dir(executable)
	if got := packagedSchemaHelperDirectoryForExecutable(executable); got != want {
		t.Fatalf("non-Darwin helper dir = %q, want executable directory %q", got, want)
	}
}
