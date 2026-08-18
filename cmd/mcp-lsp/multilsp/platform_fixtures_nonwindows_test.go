//go:build !windows

package multilsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
)

func goAdapterArgsForPlatform() []string {
	return []string{lspplatform.GoplsRemoteAutoCohortArg, "-remote.listen.timeout=2.5s"}
}

func expectedGoRSSLimit() uint64 {
	return defaultGoRSSLimitBytes
}

func installFakeTypeScriptNavigationNodePlatform(t *testing.T, dir, outputPath string) {
	t.Helper()
	nodePath := filepath.Join(dir, "node")
	script := strings.Join([]string{"#!/bin/sh", "cat >/dev/null", "cat " + shellQuote(outputPath), ""}, "\n")
	if err := os.WriteFile(nodePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeFakeGoOutput(t *testing.T, root, name, output string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	body := "#!/bin/sh\nprintf '%s' '" + strings.ReplaceAll(output, "'", "'\\''") + "'\n"
	path := filepath.Join(dir, "go")
	writeFile(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake go: %v", err)
	}
	return dir
}

func writeCWDDependentFakeGoVersion(t *testing.T, root, name, requiredDir, matchingOutput, fallbackOutput string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	body := "#!/bin/sh\n" +
		"if [ \"$PWD\" = '" + requiredDir + "' ] && [ \"$GOTOOLCHAIN\" = 'auto' ]; then\n" +
		"  /bin/echo '" + matchingOutput + "'\n" +
		"else\n" +
		"  /bin/echo '" + fallbackOutput + "'\n" +
		"fi\n"
	path := filepath.Join(dir, "go")
	writeFile(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod cwd-sensitive fake go: %v", err)
	}
	return dir
}

func writeFakePnpmExecutablePlatform(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "pnpm")
	body := "#!/bin/sh\n" +
		"printf '%s %s\\n' \"$PWD\" \"$*\" >> \"$PNPM_LOG\"\n" +
		"if [ \"$1 $2 $3\" != \"install --frozen-lockfile --ignore-scripts\" ]; then exit 64; fi\n" +
		"if [ -n \"$PNPM_EXIT\" ]; then echo pnpm failed >&2; exit \"$PNPM_EXIT\"; fi\n" +
		"mkdir -p node_modules\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake pnpm: %v", err)
	}
}
