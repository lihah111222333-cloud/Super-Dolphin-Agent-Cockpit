//go:build !windows

package multilsp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFakePnpmExecutablePlatform 写入 POSIX pnpm shell fixture。
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
