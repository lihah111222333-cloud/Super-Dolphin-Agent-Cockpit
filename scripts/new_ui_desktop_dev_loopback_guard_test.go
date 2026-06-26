package main

import "testing"

func TestNewUIDesktopDevRejectsNonLoopbackViteURL(t *testing.T) {
	shell := readScript(t, "../run-new-ui-desktop.sh")
	powerShell := readScript(t, "../run-new-ui-desktop.ps1")

	for _, want := range []string{
		"validate_vite_dev_url()",
		"VITE_DEV_URL must use loopback http/https with host and port",
		`case "$VITE_DEV_HOST" in`,
		"localhost|127.0.0.1|::1)",
		`validate_vite_dev_url "$VITE_DEV_URL"`,
	} {
		assertScriptContains(t, shell, want)
	}
	assertScriptOrder(t, shell, `VITE_DEV_URL="${VITE_DEV_URL:-http://127.0.0.1:5175}"`, `validate_vite_dev_url "$VITE_DEV_URL"`)
	assertScriptOrder(t, shell, `validate_vite_dev_url "$VITE_DEV_URL"`, `stop_stale_vite_for_port "$VITE_DEV_PORT"`)

	for _, want := range []string{
		"function Assert-ViteDevLoopbackUrl",
		"VITE_DEV_URL must use loopback http/https with host and port",
		"'localhost', '127.0.0.1', '::1'",
		"$viteUri = Assert-ViteDevLoopbackUrl -Url $env:VITE_DEV_URL",
	} {
		assertScriptContains(t, powerShell, want)
	}
	assertScriptOrder(t, powerShell, "Set-DefaultEnv -Name 'VITE_DEV_URL' -Value 'http://127.0.0.1:5175'", "$viteUri = Assert-ViteDevLoopbackUrl -Url $env:VITE_DEV_URL")
	assertScriptOrder(t, powerShell, "$viteUri = Assert-ViteDevLoopbackUrl -Url $env:VITE_DEV_URL", "Stop-StaleViteForPort -Port $ViteDevPort")
}
