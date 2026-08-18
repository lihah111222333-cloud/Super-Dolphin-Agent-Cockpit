//go:build windows

package multilsp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func goAdapterArgsForPlatform() []string {
	return []string{"-remote.listen.timeout=2.5s"}
}

func expectedGoRSSLimit() uint64 {
	return defaultGoWindowsRSSLimitBytes
}

func installFakeTypeScriptNavigationNodePlatform(t *testing.T, dir, outputPath string) {
	t.Helper()
	nodePath := filepath.Join(dir, "node.cmd")
	script := "@echo off\r\ntype \"" + outputPath + "\"\r\n"
	if err := os.WriteFile(nodePath, []byte(script), 0o600); err != nil {
		t.Fatalf("write fake Windows node: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeFakeGoOutput(t *testing.T, root, name, output string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	path := filepath.Join(dir, "go.exe")
	buildWindowsFakeGo(t, path, fmt.Sprintf("package main\nimport \"fmt\"\nfunc main() { fmt.Print(%q) }\n", output))
	return dir
}

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

func buildWindowsFakeGo(t *testing.T, path, source string) {
	t.Helper()
	sourcePath := filepath.Join(filepath.Dir(path), "main.go")
	writeFile(t, sourcePath, source)
	goPath, err := exec.LookPath("go.exe")
	if err != nil {
		goPath, err = exec.LookPath("go")
	}
	if err != nil {
		t.Fatalf("locate Go executable for Windows fixture: %v", err)
	}
	cmd := exec.Command(goPath, "build", "-o", path, sourcePath)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Windows fake go: %v\n%s", err, output)
	}
}

func writeFakePnpmExecutablePlatform(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "pnpm.bat")
	body := "@echo off\r\n" +
		"echo %CD% %*>> \"%PNPM_LOG%\"\r\n" +
		"if not \"%1 %2 %3\"==\"install --frozen-lockfile --ignore-scripts\" exit /b 64\r\n" +
		"if not \"%PNPM_EXIT%\"==\"\" (\r\n" +
		"  echo pnpm failed 1>&2\r\n" +
		"  exit /b %PNPM_EXIT%\r\n" +
		")\r\n" +
		"mkdir node_modules >NUL 2>NUL\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake pnpm: %v", err)
	}
}
