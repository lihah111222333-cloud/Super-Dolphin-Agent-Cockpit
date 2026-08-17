//go:build darwin

package toolbridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestSchemaHelperProductionPathDarwinIgnoresProjectRootAndEnvironment 验证 Darwin
// production helper 只服从宿主可执行文件布局，不接受项目根或环境变量覆盖。
func TestSchemaHelperProductionPathDarwinIgnoresProjectRootAndEnvironment(t *testing.T) {
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
	if filepath.Base(want) == "MacOS" && filepath.Base(filepath.Dir(want)) == "Contents" {
		want = filepath.Join(filepath.Dir(want), "Resources", "bin")
	}
	if dir != want {
		t.Fatalf("Darwin production helper dir = %q, want executable-owned %q", dir, want)
	}
}

// TestPackagedSchemaHelperDirectoryDarwinMapsAppBundle 锁定 macOS app bundle 的
// Contents/MacOS 到 Contents/Resources/bin 映射。
func TestPackagedSchemaHelperDirectoryDarwinMapsAppBundle(t *testing.T) {
	executable := filepath.Join(string(filepath.Separator), "Applications", "Super Dolphin.app", "Contents", "MacOS", "super-dolphin")
	want := filepath.Join(string(filepath.Separator), "Applications", "Super Dolphin.app", "Contents", "Resources", "bin")
	if got := packagedSchemaHelperDirectoryForExecutable(executable); got != want {
		t.Fatalf("Darwin app bundle helper dir = %q, want %q", got, want)
	}
}
