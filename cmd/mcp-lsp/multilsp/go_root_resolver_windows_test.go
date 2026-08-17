//go:build windows

package multilsp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeFakeGoOutput 是 Windows 专用 Go resolver fixture；
// 使用真实 go.exe 构造固定输出，避免公共测试依赖 POSIX shell。
func writeFakeGoOutput(t *testing.T, root, name, output string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	path := filepath.Join(dir, "go.exe")
	buildWindowsFakeGo(t, path, fmt.Sprintf("package main\nimport \"fmt\"\nfunc main() { fmt.Print(%q) }\n", output))
	return dir
}

// writeCWDDependentFakeGoVersion 是 Windows 专用 cwd/toolchain fixture；
// 它保留 GOTOOLCHAIN=auto 与模块目录匹配语义。
func writeCWDDependentFakeGoVersion(t *testing.T, root, name, requiredDir, matchingOutput, fallbackOutput string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	path := filepath.Join(dir, "go.exe")
	body := fmt.Sprintf(`package main
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)
func main() {
	cwd, _ := os.Getwd()
	if strings.EqualFold(filepath.Clean(cwd), filepath.Clean(%q)) && strings.EqualFold(os.Getenv("GOTOOLCHAIN"), "auto") {
		fmt.Print(%q)
		return
	}
	fmt.Print(%q)
}
`, requiredDir, matchingOutput, fallbackOutput)
	buildWindowsFakeGo(t, path, body)
	return dir
}

// buildWindowsFakeGo 编译 Windows fixture，确保测试使用真实 PE Go 命令而非 PATH 假 binary。
func buildWindowsFakeGo(t *testing.T, path, source string) {
	t.Helper()
	sourcePath := filepath.Join(filepath.Dir(path), "main.go")
	writeFile(t, sourcePath, source)
	goPath, err := exec.LookPath("go.exe")
	if err != nil {
		t.Fatalf("locate Go executable for Windows fixture: %v", err)
	}
	cmd := exec.Command(goPath, "build", "-o", path, sourcePath)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Windows fake go: %v\n%s", err, output)
	}
}
