package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// secureTrustedLauncherTestRoot 注入隔离的 OS home，并返回其中的 canonical launcher 根。
func secureTrustedLauncherTestRoot(t *testing.T) string {
	t.Helper()
	homeBase, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve secure parent for launcher fixture: %v", err)
	}
	homeBase, err = filepath.EvalSymlinks(homeBase)
	if err != nil {
		t.Fatalf("resolve secure parent for launcher fixture: %v", err)
	}
	home, err := os.MkdirTemp(homeBase, ".super-dolphin-launcher-home-")
	if err != nil {
		t.Fatalf("create isolated launcher fixture home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	installTrustedLauncherOSHomeResolver(t, home)
	root := filepath.Join(home, ".super-dolphin-gate-launchers")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create canonical launcher root: %v", err)
	}
	return root
}

func installTrustedLauncherOSHomeResolver(t *testing.T, home string) {
	t.Helper()
	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(fakeBin, 0o700); err != nil {
		t.Fatalf("create launcher fixture command directory: %v", err)
	}
	name := "getent"
	body := "#!/bin/sh\nprintf 'fixture:x:0:0::" + home + ":/bin/sh\\n'\n"
	if runtime.GOOS == "darwin" {
		name = "dscl"
		body = "#!/bin/sh\nprintf 'NFSHomeDirectory: " + home + "\\n'\n"
	}
	if err := os.WriteFile(filepath.Join(fakeBin, name), []byte(body), 0o700); err != nil {
		t.Fatalf("write launcher fixture %s: %v", name, err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
