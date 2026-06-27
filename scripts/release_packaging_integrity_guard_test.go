package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestGitHubReleaseWrappersRejectMissingAndPlaceholderRepos(t *testing.T) {
	for _, script := range []string{
		readScript(t, "package_macos_github_release.sh"),
		readScript(t, "publish_github_release.sh"),
	} {
		assertScriptContains(t, script, "SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required")
		assertScriptContains(t, script, "known placeholder update repo is not allowed")
		assertScriptContains(t, script, "xiaoxiaotest9527-bit/-")
	}
	windows := readScript(t, "package_windows_github_release.ps1")
	assertScriptContains(t, windows, "SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required")
	assertScriptContains(t, windows, "known placeholder update repo is not allowed")
	assertScriptContains(t, windows, "xiaoxiaotest9527-bit/-")
}

func TestWindowsGitHubReleaseWrapperRejectsPublicKeyMismatch(t *testing.T) {
	script := readScript(t, "package_windows_github_release.ps1")
	body := powerShellFunctionBody(t, script, "Assert-UpdatePublicKeyContinuity")

	assertScriptContains(t, body, "$PreviousPublicKey.Trim() -ne $PublicKey.Trim()")
	assertScriptContains(t, body, "throw 'previous package update public key does not match SUPER_DOLPHIN_UPDATE_PUBLIC_KEY'")
	assertScriptDoesNotContain(t, body, "+ $PreviousPublicKey")
	assertScriptDoesNotContain(t, body, "+ $PublicKey")
}

func TestGitHubReleasePublisherRequiresExplicitRepoBeforeGitHubAccess(t *testing.T) {
	binDir := t.TempDir()
	writeFailingGitHubReleaseFakeGH(t, binDir)

	cmd := exec.Command("bash", "publish_github_release.sh", "--print-context")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected print-context without update repo to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required") {
		t.Fatalf("expected explicit repo error, got:\n%s", output)
	}
	if strings.Contains(string(output), "gh should not be called") {
		t.Fatalf("script called gh before requiring explicit repo:\n%s", output)
	}
}

func TestGitHubReleasePublisherRejectsPlaceholderRepoBeforeGitHubAccess(t *testing.T) {
	binDir := t.TempDir()
	writeFailingGitHubReleaseFakeGH(t, binDir)

	cmd := exec.Command("bash", "publish_github_release.sh", "--print-context")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=xiaoxiaotest9527-bit/-",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected print-context with placeholder repo to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "known placeholder update repo is not allowed") {
		t.Fatalf("expected placeholder repo error, got:\n%s", output)
	}
	if strings.Contains(string(output), "gh should not be called") {
		t.Fatalf("script called gh before rejecting placeholder repo:\n%s", output)
	}
}

func TestPackageShellFrontendBuildCacheKeyIncludesAllViteInputs(t *testing.T) {
	for _, scriptPath := range []string{"package_macos.sh", "package_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "SUPER_DOLPHIN_RELEASE_BUILD")
			assertScriptContains(t, script, "frontend_node_version_input")
			assertScriptContains(t, script, "frontend_npm_version_input")
			assertScriptContains(t, script, "\"input:NODE_VERSION=$(frontend_node_version_input)\"")
			assertScriptContains(t, script, "\"input:NPM_VERSION=$(frontend_npm_version_input)\"")
			assertScriptContains(t, script, "\"$root/frontend-app/package.json\"")
			assertScriptContains(t, script, "\"$root/frontend-app/package-lock.json\"")
			assertScriptContains(t, script, "\"$root/frontend-app/vite.config.js\"")
			assertScriptContains(t, script, "\"$root/frontend-app/index.html\"")
			assertScriptContains(t, script, "\"$root/frontend-app/public\"")
			assertScriptContains(t, script, "\"$root/frontend-app/src\"")
			assertScriptOrder(t, script, "\"$root/frontend-app/package.json\"", "npm ci")
			assertScriptOrder(t, script, "\"$root/frontend-app/public\"", "npm run build")
		})
	}
}

func powerShellFunctionBody(t *testing.T, script, name string) string {
	t.Helper()

	script = strings.ReplaceAll(script, "\r\n", "\n")
	startMarker := "function " + name + "() {"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("script missing function %s", name)
	}
	rest := script[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("script function %s has no closing brace", name)
	}
	return rest[:end]
}
