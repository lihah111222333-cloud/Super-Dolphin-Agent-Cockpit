package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallHooksMakeTargetUsesWorktreeSafeRelativePath(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	launcher := commitInstallHooksFixture(t, root)

	runInstallHooksGit(t, root, "config", "core.hooksPath", ".githooks")

	output, err := runInstallHooksMake(t, root)
	if err != nil {
		t.Fatalf("make install-hooks failed: %v\n%s", err, output)
	}

	if got := installHooksGitOutput(t, root, "config", "--get", "core.hooksPath"); got != ".githooks" {
		t.Fatalf("core.hooksPath = %q, want .githooks\nmake output:\n%s", got, output)
	}
	if _, err := os.Stat(launcher); err != nil {
		t.Fatalf("exact-tree launcher was not installed: %v", err)
	}
	assertInstallHooksLauncherConfigAbsent(t, root, output)

	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runInstallHooksGit(t, root, "worktree", "add", "-q", "--detach", linkedRoot, "HEAD")
	if got := installHooksGitOutput(t, linkedRoot, "config", "--get", "core.hooksPath"); got != ".githooks" {
		t.Fatalf("linked worktree core.hooksPath = %q, want .githooks", got)
	}
	if _, err := os.Stat(filepath.Join(linkedRoot, ".githooks", "pre-commit")); err != nil {
		t.Fatalf("linked worktree .githooks/pre-commit is missing: %v", err)
	}
}

func TestInstallHooksMakeTargetBuildsExactTreeLauncherByDefault(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	launcher := commitInstallHooksFixture(t, root)
	output, err := runInstallHooksMake(t, root)
	if err != nil {
		t.Fatalf("make install-hooks failed: %v\n%s", err, output)
	}

	if _, err := os.Stat(launcher); err != nil {
		t.Fatalf("exact-tree builder output is unavailable: %v", err)
	}
	assertInstallHooksLauncherConfigAbsent(t, root, output)
}

func TestTrustedLauncherBuilderReadsExactTreeBlobsWithExplicitType(t *testing.T) {
	guard := readRequiredFile(t, filepath.Join(scriptRepoRoot(t), "scripts", "build-trusted-gate-launcher.sh"))
	assertScriptContains(t, guard, `git -C "$repo_root" cat-file blob "${tree}:${file_path}"`)
	assertScriptContains(t, guard, `git -C "$repo_root" archive --format=tar -o "$archive_path" "$tree"`)
	assertScriptContains(t, guard, `tar -xf "$archive_path" -C "$temp_root"`)
	assertScriptDoesNotContain(t, guard, `git -C "$repo_root" cat-file "${tree}:${file_path}"`)
	assertScriptDoesNotContain(t, guard, `archive --format=tar "$tree" | tar`)
	assertScriptDoesNotContain(t, guard, "--install-root")
	assertScriptDoesNotContain(t, guard, "super-dolphin-gate-launchers")
}

func TestTrustedGateLauncherShellContract(t *testing.T) {
	installTrustedLauncherContractOSHome(t)
	command := exec.Command("bash", "tests/test_trusted_gate_launcher_contract.sh")
	command.Env = append(os.Environ(), "SUPER_DOLPHIN_TRUSTED_LAUNCHER_CONTRACT_SHELLS="+strings.Join(trustedLauncherContractShells(), " "))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("trusted launcher shell contract failed: %v\n%s", err, output)
	}
	helper := readRequiredFile(t, filepath.Join(scriptRepoRoot(t), ".githooks", "trusted-gate-launcher.sh"))
	assertScriptContains(t, helper, `git -C "$repo_root" cat-file blob "${tree}:scripts/build-trusted-gate-launcher.sh"`)
}

// installTrustedLauncherContractOSHome 为无登录账户的 Linux worker 提供独立的系统 home 解析夹具。
func installTrustedLauncherContractOSHome(t *testing.T) {
	t.Helper()
	cacheRoot := strings.TrimSpace(os.Getenv("GOCACHE"))
	homeParent, homeErr := os.UserHomeDir()
	if homeErr != nil {
		homeParent = ""
	}
	if info, err := os.Stat(homeParent); err != nil || !info.IsDir() {
		if cacheRoot == "" {
			t.Fatalf("trusted launcher test requires an existing OS home or explicit GOCACHE")
		}
		homeParent = filepath.Dir(cacheRoot)
	}
	home, err := os.MkdirTemp(homeParent, ".trusted-launcher-home-")
	if err != nil {
		t.Fatalf("create trusted launcher test home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	fakeBin := t.TempDir()
	resolver, body := "getent", "#!/bin/sh\nprintf 'fixture:x:0:0::"+home+":/bin/sh\\n'\n"
	if runtime.GOOS == "darwin" {
		resolver = "dscl"
		body = "#!/bin/sh\nprintf 'NFSHomeDirectory: " + home + "\\n'\n"
	}
	if err := os.WriteFile(filepath.Join(fakeBin, resolver), []byte(body), 0o700); err != nil {
		t.Fatalf("write trusted launcher home resolver: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestInstallHooksMakeTargetRefusesConflictingHooksPath(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	_ = commitInstallHooksFixture(t, root)
	conflictingHooksPath := filepath.Join(t.TempDir(), ".githooks")
	runInstallHooksGit(t, root, "config", "core.hooksPath", conflictingHooksPath)

	output, err := runInstallHooksMake(t, root)
	if err == nil {
		t.Fatalf("make install-hooks unexpectedly succeeded with conflicting core.hooksPath\n%s", output)
	}
	if !strings.Contains(string(output), "existing core.hooksPath = "+conflictingHooksPath+"; refusing to replace it automatically") {
		t.Fatalf("make install-hooks did not report the conflicting core.hooksPath\n%s", output)
	}
	if got := installHooksGitOutput(t, root, "config", "--get", "core.hooksPath"); got != conflictingHooksPath {
		t.Fatalf("core.hooksPath = %q, want conflicting path %q", got, conflictingHooksPath)
	}
}

func TestInstallHooksMakeTargetRejectsMissingExactTreeBuilder(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	writeInstallHooksPreCommit(t, root)

	output, err := runInstallHooksMake(t, root)
	if err == nil || !strings.Contains(string(output), "builder is missing from the exact staged tree") {
		t.Fatalf("missing builder result error=%v output=%s", err, output)
	}
	check := exec.Command("git", "config", "--local", "--get", "core.hooksPath")
	check.Dir = root
	if output, err := check.Output(); err == nil || len(output) != 0 {
		t.Fatalf("hooks were installed without a provisioned launcher: %q", output)
	}
}

func writeInstallHooksBuilder(t *testing.T, root string, launcherMode os.FileMode) string {
	t.Helper()
	installRoot := secureTrustedLauncherTestRoot(t)
	path := filepath.Join(root, "scripts", "build-trusted-gate-launcher.sh")
	body := `#!/usr/bin/env bash
set -euo pipefail
repo_root=$(git rev-parse --show-toplevel)
repo_root=$(cd "$repo_root" && pwd -P)
tree=${1:?tree is required}
install_root=` + fmt.Sprintf("%q", installRoot) + `
# Keep each fixture tree distinct from any user-installed launcher.
# fixture-root: ` + root + `
launcher="$install_root/v2/$tree/` + installHooksStubDigest() + `/super-dolphin-gate"
if [[ -e "$launcher" ]]; then
  printf 'fixture refuses to overwrite existing launcher: %s\n' "$launcher" >&2
  exit 1
fi
mkdir -p "$(dirname "$launcher")"
printf '#!/usr/bin/env bash\nexit 0\n' >"$launcher"
chmod ` + fmt.Sprintf("%#o", launcherMode.Perm()) + ` "$launcher"
printf '%s\n' "$launcher"
`
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write exact-tree launcher builder: %v", err)
	}
	return installRoot
}

func TestInstallHooksMakeTargetRejectsGroupWritableLauncher(t *testing.T) {
	root := prepareInstallHooksRepo(t)
	writeInstallHooksBuilder(t, root, 0o770)
	runInstallHooksGit(t, root, "add", "scripts/build-trusted-gate-launcher.sh")

	output, err := runInstallHooksMake(t, root)
	if err == nil || !strings.Contains(string(output), "permit group or world writes") {
		t.Fatalf("group-writable launcher result error=%v output=%s", err, output)
	}
}

func prepareInstallHooksRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is required for install-hooks regression")
	}

	root := t.TempDir()
	copyFixTestGuardRepoFile(t, root, "scripts/ai_maintenance/deferred_e2e_packages.txt", 0o644)
	copyFixTestGuardRepoFile(t, root, "scripts/install-hooks.sh", 0o755)
	copyFixTestGuardRepoFile(t, root, ".githooks/trusted-gate-launcher.sh", 0o755)
	runInstallHooksGit(t, root, "init", "-q")
	return root
}

func commitInstallHooksFixture(t *testing.T, root string) string {
	t.Helper()
	runInstallHooksGit(t, root, "config", "user.email", "hooks@example.test")
	runInstallHooksGit(t, root, "config", "user.name", "Hooks Test")
	writeInstallHooksPreCommit(t, root)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hooks fixture\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	installRoot := writeInstallHooksBuilder(t, root, 0o700)
	runInstallHooksGit(t, root, "add", ".githooks/pre-commit", "README.md", "scripts/build-trusted-gate-launcher.sh")
	runInstallHooksGit(t, root, "commit", "-m", "chore: 初始化 hooks fixture")
	tree := installHooksGitOutput(t, root, "rev-parse", "HEAD^{tree}")
	return installHooksLauncherPath(installRoot, tree)
}

func writeInstallHooksPreCommit(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".githooks"), 0o755); err != nil {
		t.Fatalf("create .githooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".githooks", "pre-commit"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write pre-commit: %v", err)
	}
}

func installHooksLauncherPath(installRoot, tree string) string {
	return filepath.Join(installRoot, "v2", tree, installHooksStubDigest(), "super-dolphin-gate")
}

func installHooksStubDigest() string {
	digest := sha256.Sum256([]byte("#!/usr/bin/env bash\nexit 0\n"))
	return fmt.Sprintf("%x", digest[:])
}

func runInstallHooksMake(t *testing.T, root string, env ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("make", "-f", filepath.Join(scriptRepoRoot(t), "Makefile"), "install-hooks")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}

func runInstallHooksGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = installHooksGitOutput(t, dir, args...)
}

func installHooksGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

func assertInstallHooksLauncherConfigAbsent(t *testing.T, root string, installOutput []byte) {
	t.Helper()
	for _, key := range []string{"superdolphin.gateLauncher", "superdolphin.gateLauncherRoot"} {
		cmd := exec.Command("git", "config", "--local", "--get", key)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err == nil || strings.TrimSpace(string(output)) != "" {
			t.Fatalf("install-hooks retained repo-local %s=%q", key, output)
		}
		if strings.Contains(string(installOutput), key) {
			t.Fatalf("install-hooks output exposed retired config key %s: %s", key, installOutput)
		}
	}
}
