# macOS 灰度打包与一键更新 Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**DAG breakdown:** See `docs/superpowers/plans/打包/2026-06-06-macos-gray-packaging-dag-tasks.md` for executable DAG nodes, dependencies, write scopes, and verification gates.

**Goal:** 让 Super Dolphin 在 macOS 灰度阶段产出可签名、公证、可安装、可一键更新的包，并证明前端、底层 Codex CLI、MCP sidecar、LSP、embedded PostgreSQL、Git 等包内能力不会缺失。

**Architecture:** 不升级 Wails，也不接入 Wails 新版内置 updater；当前仓库仍使用 `github.com/wailsapp/wails/v3 v3.0.0-alpha.74`。灰度用户只接触一个 `Super Dolphin.dmg`：首次安装用它，后续 App 内更新也下载同一个 DMG 产物。主 App 下载并校验 signed manifest + DMG sha256，独立 helper 等待主进程退出后挂载 DMG、验证其中的 `.app`、替换 `/Applications/Super Dolphin.app` 并重启。

**Tech Stack:** Go 1.25.7, Wails v3 alpha.74 desktop host, React/Vite `frontend-app`, fx modules, jrpc2 handler map, shell packaging scripts, macOS `codesign`, `notarytool`, `stapler`, `spctl`, `ditto`, Ed25519 update manifest signatures.

---

## Scope

本计划只覆盖 macOS 灰度发布：

- 平台：macOS arm64 与 macOS amd64，先以当前打包机架构为准。
- 初始安装：灰度用户下载并打开 `Super Dolphin.dmg`。
- 更新安装：App 内 updater 下载新版 `Super Dolphin.dmg`，校验、挂载、复制其中的 `.app`，调用 helper 替换。
- 灰度分发：私有服务器或 GitHub Releases 都可以，只要能提供 HTTPS URL、manifest 和签名。
- 安装位置：灰度默认 `/Applications/Super Dolphin.app`。如果当前用户无权限替换该目录，helper 必须 fail-fast 返回明确错误，不弹隐藏提权脚本。

不包含：

- Windows 打包。
- Linux 打包修复。
- App Store 分发。
- Wails 框架升级。
- Sparkle 集成。
- 静默后台强制更新。
- 增量/差分更新。

## Codex Artifact Policy

你从官网下载安装的 Codex 只作为**打包机上的可信输入来源**，不是灰度用户的前置安装要求。

灰度发布时必须把 Codex CLI 复制进 `Super Dolphin.app/Contents/Resources/bin/codex`，并写入 `codex-manifest.json`。灰度用户只安装 `Super Dolphin.dmg`，不需要额外下载安装 Codex。更新时新版 DMG 也必须内置新版或同版 Codex CLI。

`SUPER_DOLPHIN_CODEX_ARTIFACT` 可以指向从官方 Codex 安装包中提取出的 `codex` 可执行文件；`SUPER_DOLPHIN_CODEX_SHA256` 必须来自可信校验来源，不能在打包时临时对同一个本地文件现算后当作信任依据。

## Current Repository Evidence

- `cmd/agent-terminal/main.go` 已在桌面入口调用 `runtimeenv.ConfigurePackagedApp()`。
- `cmd/agent-terminal/frontend.go` 通过 `//go:embed all:frontend/dist` 嵌入前端静态资源。
- `scripts/package_macos.sh` 已构建前端、`agent-terminal`、`mcp-orch`、`mcp-lsp`、`mcp-ida`，并打包 migrations、model registry、relay `.env`、Git、LSP bundle、Codex CLI、PostgreSQL runtime。
- `scripts/package_macos.sh` 已支持 `CODESIGN_IDENTITY` 和 `NOTARY_PROFILE`，但灰度 profile 尚未强制签名/公证。
- `scripts/verify_packaged_app_macos.sh` 已校验包结构、runtime manifest、Codex manifest、LSP manifest、PostgreSQL、Git、dylib 引用。
- `internal/provider/codexapp/peer_supervisor.go` 当前对 peer 初始启动失败采用 warn+skip。灰度包必须把 packaged runtime 下的 peer 初始启动失败升级为硬失败，否则 App 可打开但项目工具缺失。
- `frontend-app/src/pages/settings/SettingsPage.jsx` 已有 About/构建信息区域，适合作为灰度更新入口。

## File Structure

新增或修改的文件：

- `scripts/package_macos.sh`
  - 增加 `SUPER_DOLPHIN_RELEASE_PROFILE=dev-local|gray`。
  - gray profile 强制 `CODESIGN_IDENTITY` 为 Developer ID，强制 `NOTARY_PROFILE`，生成唯一对外分发的 DMG。
  - 构建并打包 `super-dolphin-updater` helper。

- `scripts/package_macos_guard_test.go`
  - 锁定 gray profile 的签名、公证、helper、DMG update manifest 输入约束。

- `scripts/verify_packaged_app_macos.sh`
  - 校验 `Contents/Resources/bin/super-dolphin-updater` 存在、可执行、签名有效。
  - 校验 DMG 可以挂载并包含合法 `.app`。

- `docs/scripts/macos_release_smoke.sh`
  - 增加 `packaged-tools` 和 `update-loop` 两个 smoke mode。
  - `packaged-tools` 证明 Codex CLI、sidecar、LSP、PostgreSQL、Git 在 clean env 下可用。
  - `update-loop` 证明从旧版本安装到新版本后仍能启动并保留 app data。

- `internal/provider/codexapp/peer_supervisor.go`
  - packaged runtime 下，`mcp-orch` 或 `mcp-lsp` 初始启动失败必须返回错误。

- `internal/provider/codexapp/peer_supervisor_test.go`
  - 锁定 dev best-effort 与 packaged fail-fast 的差异。

- `internal/module/appupdate/manifest.go`
  - 定义 signed update manifest 的 JSON schema、Ed25519 验签、HTTPS artifact URL 校验、sha256 校验、平台匹配和版本比较。

- `internal/module/appupdate/manifest_test.go`
  - 锁定 manifest 验签、篡改拒绝、平台不匹配拒绝、版本不升级拒绝。

- `internal/module/appupdate/service.go`
  - 实现检查更新、下载 DMG、sha256 校验、调用 helper，并在 helper 启动后请求 Wails 主程序退出。

- `internal/module/appupdate/service_test.go`
  - 用 `httptest.NewTLSServer` 和临时目录锁定 check/download/stage/install 调用。

- `internal/module/appupdate/rpc.go`
  - 注册 `app/update/check`、`app/update/download`、`app/update/install`、`app/update/installLatest`。

- `internal/module/appupdate/rpc_test.go`
  - 锁定 JSON-RPC 参数校验和错误传播。

- `internal/module/appupdate/module.go`
  - 通过 fx 提供 service 与 handler map。

- `internal/app/modules.go`
  - 挂载 `appupdate.Module`。

- `internal/ui/wails/lifecycle.go`
  - 暴露受控的 `RequestQuitForUpdate`，供 app updater 在 helper 启动后请求主程序退出。

- `internal/ui/wails/lifecycle_test.go`
  - 锁定 updater 请求退出会触发 Wails quit callback，且不走正常用户关闭拦截。

- `internal/ui/wails/module.go`
  - 在 desktop-only Wails fx module 中提供 `appupdate.RequestQuit`，避免非桌面 fx 图依赖 Wails lifecycle。

- `cmd/super-dolphin-updater/main.go`
  - updater helper 入口。

- `cmd/super-dolphin-updater/install.go`
  - 验证 staged app、等待主进程退出、替换 app、重启 app。

- `cmd/super-dolphin-updater/install_test.go`
  - 锁定路径校验、bundle id 校验、替换顺序、权限错误。

- `frontend-app/src/shared/api/backendApi.js`
  - 增加 update RPC method 常量和 facade。

- `frontend-app/src/shared/api/backendApi.test.js`
  - 锁定 facade 调用 method 名称与参数。

- `frontend-app/src/pages/settings/SettingsPage.jsx`
  - 在 About 区域增加“检查更新/安装更新”入口。

- `frontend-app/src/pages/settings/SettingsPage.test.jsx`
  - 锁定可用更新、无更新、下载失败、安装失败、安装中状态。

- `frontend-app/src/SettingsPage.test.jsx`
  - 同步更新根级 SettingsPage 测试的 backend API mock，避免全量 `npm test` 因新增 named export 缺失而失败。

- `docs/运维发布/打包发布/macos-gray-release.md`
  - 记录灰度打包、签名、公证、manifest 发布、clean VM 验收步骤。

---

## Task 1: Enforce macOS Gray Release Signing Gate

**Files:**
- Modify: `scripts/package_macos.sh`
- Modify: `scripts/package_macos_guard_test.go`
- Create: `docs/运维发布/打包发布/macos-gray-release.md`

- [ ] **Step 1: Write failing guard tests for gray profile**

Add these tests to `scripts/package_macos_guard_test.go`:

```go
func TestPackageMacOSGrayProfileRequiresDeveloperIDAndNotarization(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, `release_profile="${SUPER_DOLPHIN_RELEASE_PROFILE:-dev-local}"`)
	assertScriptContains(t, script, `case "$release_profile" in`)
	assertScriptContains(t, script, `dev-local|gray)`)
	assertScriptContains(t, script, `gray release requires CODESIGN_IDENTITY`)
	assertScriptContains(t, script, `gray release requires NOTARY_PROFILE`)
	assertScriptContains(t, script, `gray release requires SUPER_DOLPHIN_UPDATE_MANIFEST_URL`)
	assertScriptContains(t, script, `gray release requires SUPER_DOLPHIN_UPDATE_PUBLIC_KEY`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_UPDATE_MANIFEST_URL to be an HTTPS URL with a host`)
	assertScriptContains(t, script, `32-byte Ed25519 public key`)
	assertScriptContains(t, script, `Developer ID Application:`)
	assertScriptOrder(t, script, `enforce_release_profile`, `phase_start "frontend build"`)
	assertScriptOrder(t, script, `xcrun notarytool submit "$dmg_path"`, `xcrun stapler staple "$dmg_path"`)
	assertScriptContains(t, script, `spctl -a -vv -t open "$dmg_path"`)
}
```

- [ ] **Step 2: Run the guard test and confirm failure**

Run:

```bash
./scripts/test_with_guard.sh scripts/package_macos_guard_test.go -count=1
```

Expected: FAIL because `SUPER_DOLPHIN_RELEASE_PROFILE` and `enforce_release_profile` do not exist yet.

- [ ] **Step 3: Add release profile validation**

In `scripts/package_macos.sh`, after `platform="${goos}-${goarch}"`, add:

```bash
release_profile="${SUPER_DOLPHIN_RELEASE_PROFILE:-dev-local}"
case "$release_profile" in
  dev-local|gray)
    ;;
  *)
    echo "unsupported SUPER_DOLPHIN_RELEASE_PROFILE=$release_profile; expected dev-local or gray" >&2
    exit 1
    ;;
esac

enforce_release_profile() {
  if [[ "$release_profile" != "gray" ]]; then
    return 0
  fi
  if [[ -z "${CODESIGN_IDENTITY:-}" || "${CODESIGN_IDENTITY:-}" == "-" ]]; then
    echo "gray release requires CODESIGN_IDENTITY to be a Developer ID Application identity" >&2
    exit 1
  fi
  if [[ "${CODESIGN_IDENTITY:-}" != Developer\ ID\ Application:* ]]; then
    echo "gray release requires CODESIGN_IDENTITY to start with 'Developer ID Application:'" >&2
    exit 1
  fi
  if [[ -z "${NOTARY_PROFILE:-}" ]]; then
    echo "gray release requires NOTARY_PROFILE for notarytool" >&2
    exit 1
  fi
  if [[ -z "${SUPER_DOLPHIN_UPDATE_MANIFEST_URL:-}" ]]; then
    echo "gray release requires SUPER_DOLPHIN_UPDATE_MANIFEST_URL" >&2
    exit 1
  fi
  if [[ ! "$SUPER_DOLPHIN_UPDATE_MANIFEST_URL" =~ ^https://[^/?#]+(/|$) ]]; then
    echo "gray release requires SUPER_DOLPHIN_UPDATE_MANIFEST_URL to be an HTTPS URL with a host" >&2
    exit 1
  fi
  if [[ -z "${SUPER_DOLPHIN_UPDATE_PUBLIC_KEY:-}" ]]; then
    echo "gray release requires SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" >&2
    exit 1
  fi
  local decoded_key
  decoded_key="$(mktemp)"
  if ! printf '%s' "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" | /usr/bin/base64 -D > "$decoded_key" 2>/dev/null; then
    rm -f "$decoded_key"
    echo "gray release requires SUPER_DOLPHIN_UPDATE_PUBLIC_KEY to be base64" >&2
    exit 1
  fi
  if [[ "$(wc -c < "$decoded_key" | tr -d ' ')" != "32" ]]; then
    rm -f "$decoded_key"
    echo "gray release requires SUPER_DOLPHIN_UPDATE_PUBLIC_KEY to decode to a 32-byte Ed25519 public key" >&2
    exit 1
  fi
  rm -f "$decoded_key"
}
```

Call it after dependency env resolution and before `phase_start "frontend build"`:

```bash
enforce_release_profile
```

When writing the packaged `.env`, include:

```bash
SUPER_DOLPHIN_UPDATE_ENABLED=1
SUPER_DOLPHIN_UPDATE_MANIFEST_URL=$SUPER_DOLPHIN_UPDATE_MANIFEST_URL
SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY
SUPER_DOLPHIN_UPDATE_CHANNEL=${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray-macos}
VERSION=$version
```

The values must be written from build-time env into the packaged runtime env file; gray users must not need to set update variables manually. Use the script's resolved lowercase `version="${VERSION:-0.1.0}"` value when writing `VERSION=$version`; do not read `$VERSION` directly under `set -u`. `VERSION` must be included because the updater compares the installed version against the signed manifest when the app is launched by Finder and no shell environment is inherited.

After stapling the DMG, add:

```bash
  spctl -a -vv -t open "$dmg_path"
```

- [ ] **Step 4: Run package script guard**

Run:

```bash
./scripts/test_with_guard.sh scripts/package_macos_guard_test.go -count=1
```

Expected: PASS.

- [ ] **Step 5: Document gray release variables**

Create `docs/运维发布/打包发布/macos-gray-release.md` with:

````markdown
# macOS Gray Release

## Required Environment

```bash
export SUPER_DOLPHIN_RELEASE_PROFILE=gray
export CODESIGN_IDENTITY="Developer ID Application: Your Team (TEAMID)"
export NOTARY_PROFILE="super-dolphin-notary"
export VERSION="0.1.1"
export SUPER_DOLPHIN_POSTGRES_DIST="/absolute/path/to/postgres-runtime"
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="/absolute/path/to/lsp-bundle"
export SUPER_DOLPHIN_CODEX_ARTIFACT="/absolute/path/to/codex"
export SUPER_DOLPHIN_CODEX_SHA256="<trusted-sha256>"
export SUPER_DOLPHIN_CODEX_VERSION="<codex-version>"
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://relay.example.com"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<bootstrap-token>"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF="<proof-or-config-id>"
export SUPER_DOLPHIN_UPDATE_MANIFEST_URL="https://updates.example.com/latest.json"
export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY="<base64-ed25519-public-key>"
export SUPER_DOLPHIN_UPDATE_SIGNING_KEY="/absolute/path/to/ed25519-private-key"
export SUPER_DOLPHIN_UPDATE_ENABLED=1
```

## Build

```bash
./scripts/package_macos.sh
docs/scripts/macos_release_smoke.sh local
docs/scripts/macos_release_smoke.sh notarized-dmg
docs/scripts/macos_release_smoke.sh packaged-tools
```

## Acceptance

- DMG passes `stapler validate`.
- DMG passes `spctl -a -vv -t open`.
- Packaged app starts from a clean macOS user with external Codex paths hidden.
- Packaged Codex CLI, `mcp-orch`, `mcp-lsp`, LSP bundle, Git, embedded PostgreSQL, and migrations are present and usable.
````

- [ ] **Step 6: Verify docs-only addition is tracked**

Run:

```bash
git status --short
```

Expected: shows `docs/运维发布/打包发布/macos-gray-release.md` and the script/test changes only.

---

## Task 2: Make Packaged Tool Loss Fail Fast in macOS Package Runtime

**Files:**
- Modify: `internal/provider/codexapp/peer_supervisor.go`
- Modify: `internal/provider/codexapp/peer_supervisor_test.go`

- [ ] **Step 1: Write failing peer supervisor test**

Add this test to `internal/provider/codexapp/peer_supervisor_test.go`:

```go
func TestPeerSupervisorPackagedInitialLaunchFailureIsFatal(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	s, launcher, _ := newTestSupervisor(t,
		WithPeerNames([]string{"mcp-orch", "mcp-lsp"}),
		WithPeerControlProbe("", time.Millisecond, 0),
	)
	launcher.launchErr["mcp-lsp"] = errors.New("missing packaged mcp-lsp")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.Run(ctx)
	if err == nil {
		t.Fatal("Run() error = nil, want packaged peer launch failure")
	}
	if !strings.Contains(err.Error(), "packaged peer launch failed") {
		t.Fatalf("Run() error = %v, want packaged peer launch failure", err)
	}
}
```

Add this cleanup test so packaged fail-fast cannot leave already-started peers running:

```go
func TestPeerSupervisorPackagedInitialLaunchFailureStopsStartedPeers(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	s, launcher, _ := newTestSupervisor(t,
		WithPeerNames([]string{"mcp-orch", "mcp-lsp"}),
		WithPeerControlProbe("", time.Millisecond, 0),
	)
	launcher.launchErr["mcp-lsp"] = errors.New("missing packaged mcp-lsp")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.Run(ctx)
	if err == nil {
		t.Fatal("Run() error = nil, want packaged peer launch failure")
	}
	if !launcher.stopped("mcp-orch") {
		t.Fatal("mcp-orch was not stopped after later packaged launch failure")
	}
}
```

Add this test to preserve dev behavior:

```go
func TestPeerSupervisorDevInitialLaunchFailureStaysBestEffort(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "dev")
	s, launcher, _ := newTestSupervisor(t,
		WithPeerNames([]string{"mcp-orch", "mcp-lsp"}),
		WithPeerControlProbe("", time.Millisecond, 0),
	)
	launcher.launchErr["mcp-lsp"] = errors.New("missing dev mcp-lsp")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	launcher.waitLaunch(t, "mcp-orch", time.Second)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dev supervisor shutdown")
	}
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
```

Expected: FAIL because packaged initial launch failure is still warn+skip.

- [ ] **Step 3: Implement packaged peer launch policy**

In `internal/provider/codexapp/peer_supervisor.go`, add:

```go
func packagedPeerLaunchRequired() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE")), "packaged")
}
```

Change the initial launch loop in `Run` so it tracks successfully launched peers and the failure branch becomes:

```go
		if err != nil {
			if packagedPeerLaunchRequired() {
				s.stopLaunchedPeers(startedPeers)
				return fmt.Errorf("packaged peer launch failed: %s: %w", name, err)
			}
			s.logger.Warn("peer_supervisor: initial launch failed, peer skipped",
				"peer", name, "error", err)
			continue
		}
```

`stopLaunchedPeers` must use the existing peer stop/kill path for all peers launched earlier in the same initial loop before returning the fatal error.

- [ ] **Step 4: Run codexapp tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
```

Expected: PASS.

- [ ] **Step 5: Run single-file guard**

Run:

```bash
./scripts/test_with_guard.sh internal/provider/codexapp/peer_supervisor.go
```

Expected: exit 0 with no output.

---

## Task 3: Treat the DMG as the Single Update Artifact

**Files:**
- Modify: `scripts/package_macos.sh`
- Modify: `scripts/package_macos_guard_test.go`
- Modify: `scripts/verify_packaged_app_macos.sh`

- [ ] **Step 1: Write failing package guard test**

Add this test to `scripts/package_macos_guard_test.go`:

```go
func TestPackageMacOSUsesDMGAsSingleUserAndUpdateArtifact(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, `dmg_path="$dist/$app_name.dmg"`)
	assertScriptContains(t, script, `write_dmg_checksum()`)
	assertScriptContains(t, script, `shasum -a 256 "$dmg_path" > "$dmg_path.sha256"`)
	assertScriptDoesNotContain(t, script, `.app.zip`)
	assertScriptOrder(t, script, `phase_start "create dmg"`, `xcrun notarytool submit "$dmg_path"`)
	assertScriptOrder(t, script, `xcrun stapler staple "$dmg_path"`, `write_dmg_checksum`)
	assertScriptOrder(t, script, `spctl -a -vv -t open "$dmg_path"`, `write_dmg_checksum`)
}
```

- [ ] **Step 2: Run guard and confirm failure**

Run:

```bash
./scripts/test_with_guard.sh scripts/package_macos_guard_test.go -count=1
```

Expected: FAIL because `dmg_path` and `write_dmg_checksum` do not exist yet, or `.app.zip` references still exist.

- [ ] **Step 3: Make DMG the named release artifact**

In `scripts/package_macos.sh`, after `app="$dist/$app_name.app"`, add:

```bash
dmg_path="$dist/$app_name.dmg"
```

In the cleanup line, change:

```bash
rm -rf "$app" "$dist/$app_name.dmg"
```

to:

```bash
rm -rf "$app" "$dmg_path" "$dmg_path.sha256"
```

Add this function before the main packaging flow:

```bash
write_dmg_checksum() {
  if [[ ! -f "$dmg_path" ]]; then
    echo "missing DMG artifact: $dmg_path" >&2
    exit 1
  fi
  shasum -a 256 "$dmg_path" > "$dmg_path.sha256"
}
```

In the `create dmg` phase, change the `hdiutil` target from `"$dist/$app_name.dmg"` to `"$dmg_path"`:

```bash
hdiutil create -volname "$app_name" -srcfolder "$staging" -ov -format UDZO "$dmg_path"
rm -rf "$staging"
phase_end
```

After the optional notarization/stapling block, write the checksum from the final DMG bytes:

```bash
write_dmg_checksum
```

This must run after `xcrun stapler staple "$dmg_path"` and `spctl -a -vv -t open "$dmg_path"` in gray profile, because stapling mutates the DMG.

- [ ] **Step 4: Extend verifier to inspect the DMG update artifact**

In `scripts/verify_packaged_app_macos.sh`, add helper logic that accepts optional `UPDATE_DMG`:

```bash
verify_update_dmg() {
  local dmg_path="${UPDATE_DMG:-}"
  [[ -n "$dmg_path" ]] || return 0
  require_file "$dmg_path"
  local mount_dir
  mount_dir="$(mktemp -d)"
  trap 'hdiutil detach "$mount_dir" >/dev/null 2>&1 || hdiutil detach -force "$mount_dir" >/dev/null 2>&1 || true; rm -rf "$mount_dir"' RETURN
  hdiutil attach "$dmg_path" -nobrowse -readonly -mountpoint "$mount_dir" >/dev/null
  require_dir "$mount_dir/Super Dolphin.app"
  require_exec "$mount_dir/Super Dolphin.app/Contents/MacOS/agent-terminal"
  require_file "$mount_dir/Super Dolphin.app/Contents/Resources/runtime-manifest.json"
}
```

Call it near the end of the verifier:

```bash
phase_start "update dmg"
verify_update_dmg
phase_end
```

- [ ] **Step 5: Run script tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackageMacOS|TestVerifyPackagedAppMacOS' -count=1
```

Expected: PASS.

---

## Task 4: Define and Verify Signed Update Manifest

**Files:**
- Create: `internal/module/appupdate/manifest.go`
- Create: `internal/module/appupdate/manifest_test.go`

- [ ] **Step 1: Write failing manifest tests**

Create `internal/module/appupdate/manifest_test.go`:

```go
package appupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func signedManifestFixture(t *testing.T, mutate func(*ManifestPayload)) ([]byte, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	payload := ManifestPayload{
		SchemaVersion: 1,
		AppID:         "com.superdolphin.app",
		Channel:       "gray-macos",
		Version:       "0.1.1",
		Platform:      "darwin-arm64",
		MinimumVersion:"0.1.0",
		PublishedAt:   "2026-06-06T00:00:00Z",
		Artifact: UpdateArtifact{
			URL:    "https://updates.example.com/Super%20Dolphin-0.1.1-darwin-arm64.dmg",
			SHA256: hex.EncodeToString(sha256.New().Sum(nil)),
			Size:   123,
		},
		ReleaseNotes: "灰度更新",
	}
	if mutate != nil {
		mutate(&payload)
	}
	payloadBytes, err := canonicalPayloadBytes(payload)
	if err != nil {
		t.Fatalf("canonicalPayloadBytes: %v", err)
	}
	doc := SignedManifest{
		Payload:   payload,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payloadBytes)),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return raw, pub
}

func TestVerifySignedManifestAcceptsValidManifest(t *testing.T) {
	raw, pub := signedManifestFixture(t, nil)
	got, err := VerifySignedManifest(raw, pub, VerifyOptions{
		AppID:           "com.superdolphin.app",
		Channel:         "gray-macos",
		Platform:        "darwin-arm64",
		CurrentVersion:  "0.1.0",
	})
	if err != nil {
		t.Fatalf("VerifySignedManifest() error = %v", err)
	}
	if got.Version != "0.1.1" {
		t.Fatalf("Version = %q, want 0.1.1", got.Version)
	}
}

func TestVerifySignedManifestRejectsTampering(t *testing.T) {
	raw, pub := signedManifestFixture(t, nil)
	raw = []byte(strings.Replace(string(raw), "0.1.1", "0.1.2", 1))
	_, err := VerifySignedManifest(raw, pub, VerifyOptions{
		AppID: "com.superdolphin.app", Channel: "gray-macos", Platform: "darwin-arm64", CurrentVersion: "0.1.0",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want signature failure")
	}
}

func TestVerifySignedManifestRejectsWrongPlatform(t *testing.T) {
	raw, pub := signedManifestFixture(t, func(p *ManifestPayload) { p.Platform = "darwin-amd64" })
	_, err := VerifySignedManifest(raw, pub, VerifyOptions{
		AppID: "com.superdolphin.app", Channel: "gray-macos", Platform: "darwin-arm64", CurrentVersion: "0.1.0",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want platform mismatch")
	}
}

func TestVerifySignedManifestReturnsNoUpdateForNonUpgrade(t *testing.T) {
	raw, pub := signedManifestFixture(t, func(p *ManifestPayload) { p.Version = "0.1.0" })
	_, err := VerifySignedManifest(raw, pub, VerifyOptions{
		AppID: "com.superdolphin.app", Channel: "gray-macos", Platform: "darwin-arm64", CurrentVersion: "0.1.0",
	})
	if !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("VerifySignedManifest() error = %v, want ErrNoUpdate", err)
	}
}

func TestVerifySignedManifestRejectsNonHTTPSArtifactURL(t *testing.T) {
	raw, pub := signedManifestFixture(t, func(p *ManifestPayload) { p.Artifact.URL = "http://updates.example.com/Super%20Dolphin.dmg" })
	_, err := VerifySignedManifest(raw, pub, VerifyOptions{
		AppID: "com.superdolphin.app", Channel: "gray-macos", Platform: "darwin-arm64", CurrentVersion: "0.1.0",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want non-HTTPS artifact URL rejection")
	}
}

func TestVerifySignedManifestRejectsMalformedVersion(t *testing.T) {
	raw, pub := signedManifestFixture(t, func(p *ManifestPayload) { p.Version = "0.1.beta" })
	_, err := VerifySignedManifest(raw, pub, VerifyOptions{
		AppID: "com.superdolphin.app", Channel: "gray-macos", Platform: "darwin-arm64", CurrentVersion: "0.1.0",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want malformed version rejection")
	}
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate -count=1
```

Expected: FAIL because `ManifestPayload`, `SignedManifest`, `VerifyOptions`, and `VerifySignedManifest` do not exist.

- [ ] **Step 3: Implement manifest verification**

Create `internal/module/appupdate/manifest.go`:

```go
package appupdate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type UpdateArtifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ManifestPayload struct {
	SchemaVersion int            `json:"schema_version"`
	AppID         string         `json:"app_id"`
	Channel       string         `json:"channel"`
	Version       string         `json:"version"`
	Platform      string         `json:"platform"`
	MinimumVersion string        `json:"minimum_version"`
	PublishedAt   string        `json:"published_at"`
	Artifact      UpdateArtifact `json:"artifact"`
	ReleaseNotes  string         `json:"release_notes"`
}

type SignedManifest struct {
	Payload   ManifestPayload `json:"payload"`
	Signature string          `json:"signature"`
}

type VerifyOptions struct {
	AppID          string
	Channel        string
	Platform       string
	CurrentVersion string
}

var ErrNoUpdate = errors.New("no newer app update is available")

func VerifySignedManifest(raw []byte, publicKey ed25519.PublicKey, opts VerifyOptions) (ManifestPayload, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return ManifestPayload{}, errors.New("app update manifest public key is invalid")
	}
	var doc SignedManifest
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ManifestPayload{}, fmt.Errorf("parse app update manifest: %w", err)
	}
	payloadBytes, err := canonicalPayloadBytes(doc.Payload)
	if err != nil {
		return ManifestPayload{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(doc.Signature))
	if err != nil {
		return ManifestPayload{}, fmt.Errorf("decode app update manifest signature: %w", err)
	}
	if !ed25519.Verify(publicKey, payloadBytes, sig) {
		return ManifestPayload{}, errors.New("app update manifest signature verification failed")
	}
	if err := validatePayload(doc.Payload, opts); err != nil {
		return ManifestPayload{}, err
	}
	return doc.Payload, nil
}

func canonicalPayloadBytes(payload ManifestPayload) ([]byte, error) {
	return json.Marshal(payload)
}

func validatePayload(payload ManifestPayload, opts VerifyOptions) error {
	if payload.SchemaVersion != 1 {
		return fmt.Errorf("unsupported app update manifest schema_version %d", payload.SchemaVersion)
	}
	if strings.TrimSpace(payload.AppID) != strings.TrimSpace(opts.AppID) {
		return fmt.Errorf("app update manifest app_id mismatch: %s", payload.AppID)
	}
	if strings.TrimSpace(payload.Channel) != strings.TrimSpace(opts.Channel) {
		return fmt.Errorf("app update manifest channel mismatch: %s", payload.Channel)
	}
	if strings.TrimSpace(payload.Platform) != strings.TrimSpace(opts.Platform) {
		return fmt.Errorf("app update manifest platform mismatch: %s", payload.Platform)
	}
	cmp, err := compareVersion(payload.Version, opts.CurrentVersion)
	if err != nil {
		return err
	}
	if cmp <= 0 {
		return fmt.Errorf("%w: app update version %s is not newer than current version %s", ErrNoUpdate, payload.Version, opts.CurrentVersion)
	}
	if payload.MinimumVersion != "" {
		minCmp, err := compareVersion(opts.CurrentVersion, payload.MinimumVersion)
		if err != nil {
			return err
		}
		if minCmp < 0 {
			return fmt.Errorf("current version %s is below update minimum_version %s", opts.CurrentVersion, payload.MinimumVersion)
		}
	}
	artifactURL, err := url.Parse(strings.TrimSpace(payload.Artifact.URL))
	if err != nil {
		return fmt.Errorf("app update artifact url is invalid: %w", err)
	}
	if artifactURL.Scheme != "https" || artifactURL.Host == "" {
		return errors.New("app update artifact url must be an HTTPS URL")
	}
	if _, err := hex.DecodeString(payload.Artifact.SHA256); err != nil || len(strings.TrimSpace(payload.Artifact.SHA256)) != sha256.Size*2 {
		return fmt.Errorf("app update artifact sha256 is invalid")
	}
	if payload.Artifact.Size <= 0 {
		return fmt.Errorf("app update artifact size must be positive")
	}
	return nil
}

func compareVersion(a, b string) (int, error) {
	ap, err := versionParts(a)
	if err != nil {
		return 0, err
	}
	bp, err := versionParts(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if ap[i] > bp[i] {
			return 1, nil
		}
		if ap[i] < bp[i] {
			return -1, nil
		}
	}
	return 0, nil
}

func versionParts(value string) ([3]int, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if value == "" {
		return [3]int{}, errors.New("version is required")
	}
	items := strings.Split(value, ".")
	if len(items) > 3 {
		return [3]int{}, fmt.Errorf("version %q has too many segments", value)
	}
	var out [3]int
	for i := 0; i < len(items) && i < 3; i++ {
		n, err := strconv.Atoi(items[i])
		if err != nil || n < 0 {
			return [3]int{}, fmt.Errorf("version %q has invalid segment %q", value, items[i])
		}
		out[i] = n
	}
	return out, nil
}
```

- [ ] **Step 4: Run manifest tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate -count=1
```

Expected: PASS for manifest tests.

---

## Task 5: Implement Update Service and RPC

**Files:**
- Create: `internal/module/appupdate/service.go`
- Create: `internal/module/appupdate/service_test.go`
- Create: `internal/module/appupdate/rpc.go`
- Create: `internal/module/appupdate/rpc_test.go`
- Create: `internal/module/appupdate/module.go`
- Modify: `internal/app/modules.go`
- Modify: `internal/ui/wails/lifecycle.go`
- Modify: `internal/ui/wails/lifecycle_test.go`
- Modify: `internal/ui/wails/module.go`

- [ ] **Step 1: Write failing service test**

Create `internal/module/appupdate/service_test.go` with an `httptest.NewTLSServer` that serves a signed manifest and a small DMG payload:

```go
package appupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceCheckDownloadAndStage(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	dmgBytes := []byte("fake dmg bytes")
	sum := sha256.Sum256(dmgBytes)
	var artifactURL string
	payload := ManifestPayload{
		SchemaVersion: 1,
		AppID: "com.superdolphin.app",
		Channel: "gray-macos",
		Version: "0.1.1",
		Platform: "darwin-arm64",
		MinimumVersion: "0.1.0",
		PublishedAt: "2026-06-06T00:00:00Z",
		Artifact: UpdateArtifact{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(dmgBytes))},
		ReleaseNotes: "灰度更新",
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			payload.Artifact.URL = artifactURL
			rawPayload, marshalErr := canonicalPayloadBytes(payload)
			if marshalErr != nil {
				t.Fatalf("canonicalPayloadBytes: %v", marshalErr)
			}
			doc := SignedManifest{
				Payload: payload,
				Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, rawPayload)),
			}
			if err := json.NewEncoder(w).Encode(doc); err != nil {
				t.Fatalf("Encode: %v", err)
			}
		case "/SuperDolphin.dmg":
			_, _ = w.Write(dmgBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	artifactURL = server.URL + "/SuperDolphin.dmg"

	stageDir := t.TempDir()
	svc := NewService(Config{
		Enabled: true,
		ManifestURL: server.URL + "/latest.json",
		PublicKey: pub,
		AppID: "com.superdolphin.app",
		Channel: "gray-macos",
		Platform: "darwin-arm64",
		CurrentVersion: "0.1.0",
		StageDir: stageDir,
		HelperPath: filepath.Join(stageDir, "super-dolphin-updater"),
		TargetAppPath: filepath.Join(stageDir, "Super Dolphin.app"),
	})
	svc.client = server.Client()

	check, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !check.Available || check.Version != "0.1.1" {
		t.Fatalf("Check() = %#v, want available 0.1.1", check)
	}
	download, err := svc.Download(context.Background())
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	data, err := os.ReadFile(download.ArchivePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != string(dmgBytes) {
		t.Fatalf("archive content mismatch")
	}
}

func TestServiceCheckMapsNoUpdateToUnavailable(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	payload := ManifestPayload{
		SchemaVersion: 1,
		AppID: "com.superdolphin.app",
		Channel: "gray-macos",
		Version: "0.1.0",
		Platform: "darwin-arm64",
		MinimumVersion: "0.1.0",
		PublishedAt: "2026-06-06T00:00:00Z",
		Artifact: UpdateArtifact{URL: "https://updates.example.com/SuperDolphin.dmg", SHA256: strings.Repeat("0", 64), Size: 1},
	}
	rawPayload, err := canonicalPayloadBytes(payload)
	if err != nil {
		t.Fatalf("canonicalPayloadBytes: %v", err)
	}
	doc := SignedManifest{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, rawPayload))}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}))
	defer server.Close()

	svc := NewService(Config{
		Enabled: true,
		ManifestURL: server.URL,
		PublicKey: pub,
		AppID: "com.superdolphin.app",
		Channel: "gray-macos",
		Platform: "darwin-arm64",
		CurrentVersion: "0.1.0",
	})
	svc.client = server.Client()

	check, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if check.Available {
		t.Fatalf("Check() = %#v, want unavailable", check)
	}
}

func TestServiceInstallStartsHelperThenRequestsQuit(t *testing.T) {
	stageDir := t.TempDir()
	helper := filepath.Join(stageDir, "helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$1.args\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	payload := ManifestPayload{
		Version: "0.1.1",
		Platform: "darwin-arm64",
		Artifact: UpdateArtifact{SHA256: strings.Repeat("0", 64)},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "selected-update.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile staged manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "Super Dolphin-0.1.1-darwin-arm64.dmg"), []byte("dmg"), 0o600); err != nil {
		t.Fatalf("WriteFile dmg: %v", err)
	}
	var quitCalled bool
	svc := NewService(Config{
		Enabled: true,
		StageDir: stageDir,
		HelperPath: helper,
		TargetAppPath: filepath.Join(stageDir, "Super Dolphin.app"),
		RequestQuit: func() error {
			quitCalled = true
			return nil
		},
	})

	if _, err := svc.Install(context.Background()); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if !quitCalled {
		t.Fatal("Install() did not request main app quit after helper start")
	}
}

func TestServiceInstallRejectsMissingQuitBeforeStartingHelper(t *testing.T) {
	stageDir := t.TempDir()
	started := filepath.Join(stageDir, "started")
	helper := filepath.Join(stageDir, "helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch \"$SUPER_DOLPHIN_HELPER_STARTED\"\nexit 99\n"), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HELPER_STARTED", started)
	writeStagedUpdateFixture(t, stageDir)
	svc := NewService(Config{
		Enabled: true,
		StageDir: stageDir,
		HelperPath: helper,
		TargetAppPath: filepath.Join(stageDir, "Super Dolphin.app"),
	})

	if _, err := svc.Install(context.Background()); err == nil {
		t.Fatal("Install() error = nil, want missing quit callback")
	}
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper was started before missing quit rejection")
	}
}

func TestServiceInstallStartsHelperEvenWhenRPCCtxIsCanceled(t *testing.T) {
	stageDir := t.TempDir()
	started := filepath.Join(stageDir, "started")
	helper := filepath.Join(stageDir, "helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch \"$SUPER_DOLPHIN_HELPER_STARTED\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	writeStagedUpdateFixture(t, stageDir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := NewService(Config{
		Enabled: true,
		StageDir: stageDir,
		HelperPath: helper,
		TargetAppPath: filepath.Join(stageDir, "Super Dolphin.app"),
		RequestQuit: func() error { return nil },
	})
	t.Setenv("SUPER_DOLPHIN_HELPER_STARTED", started)

	if _, err := svc.Install(ctx); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not run after canceled RPC ctx")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeStagedUpdateFixture(t *testing.T, stageDir string) {
	t.Helper()
	payload := ManifestPayload{
		Version: "0.1.1",
		Platform: "darwin-arm64",
		Artifact: UpdateArtifact{SHA256: strings.Repeat("0", 64)},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "selected-update.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile staged manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, "Super Dolphin-0.1.1-darwin-arm64.dmg"), []byte("dmg"), 0o600); err != nil {
		t.Fatalf("WriteFile dmg: %v", err)
	}
}
```

- [ ] **Step 2: Run service test and confirm failure**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate -count=1
```

Expected: FAIL because service types do not exist.

- [ ] **Step 3: Implement service skeleton**

Create `internal/module/appupdate/service.go` with these public shapes. `RequestQuit` is required for `Install`; starting the helper without asking the main app to quit is a hard bug because the helper waits on `-main-pid`.

```go
package appupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.uber.org/fx"
)

type Config struct {
	Enabled        bool
	ManifestURL    string
	PublicKey      ed25519.PublicKey
	AppID          string
	Channel        string
	Platform       string
	CurrentVersion string
	StageDir       string
	HelperPath     string
	TargetAppPath  string
	RequestQuit    RequestQuit
}

type RequestQuit func() error

type Service struct {
	cfg    Config
	client *http.Client
}

type CheckResult struct {
	Available    bool   `json:"available"`
	Version      string `json:"version,omitempty"`
	CurrentVersion string `json:"currentVersion"`
	ReleaseNotes string `json:"releaseNotes,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type DownloadResult struct {
	Version     string `json:"version"`
	ArchivePath string `json:"archivePath"`
	SHA256      string `json:"sha256"`
}

type InstallResult struct {
	Restarting bool   `json:"restarting"`
	Version    string `json:"version"`
}

func NewService(cfg Config) *Service {
	return &Service{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second}}
}

func (s *Service) Check(ctx context.Context) (CheckResult, error) {
	if s == nil || !s.cfg.Enabled {
		return CheckResult{Available: false, CurrentVersion: currentVersionOrUnknown(s)}, nil
	}
	payload, err := s.fetchManifest(ctx)
	if err != nil {
		if errors.Is(err, ErrNoUpdate) {
			return CheckResult{Available: false, CurrentVersion: currentVersionOrUnknown(s)}, nil
		}
		return CheckResult{}, err
	}
	return CheckResult{
		Available: true,
		Version: payload.Version,
		CurrentVersion: s.cfg.CurrentVersion,
		ReleaseNotes: payload.ReleaseNotes,
		Size: payload.Artifact.Size,
	}, nil
}

func (s *Service) Download(ctx context.Context) (DownloadResult, error) {
	payload, err := s.fetchManifest(ctx)
	if err != nil {
		return DownloadResult{}, err
	}
	if strings.TrimSpace(s.cfg.StageDir) == "" {
		return DownloadResult{}, errors.New("app update stage dir is required")
	}
	if err := os.MkdirAll(s.cfg.StageDir, 0o700); err != nil {
		return DownloadResult{}, fmt.Errorf("create app update stage dir: %w", err)
	}
	archivePath := s.archivePath(payload)
	if err := s.downloadArtifact(ctx, payload, archivePath); err != nil {
		return DownloadResult{}, err
	}
	if err := s.writeStagedManifest(payload); err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{Version: payload.Version, ArchivePath: archivePath, SHA256: payload.Artifact.SHA256}, nil
}

func (s *Service) Install(ctx context.Context) (InstallResult, error) {
	_ = ctx
	payload, err := s.readStagedManifest()
	if err != nil {
		return InstallResult{}, err
	}
	archivePath := s.archivePath(payload)
	helper := strings.TrimSpace(s.cfg.HelperPath)
	if helper == "" {
		return InstallResult{}, errors.New("app update helper path is required")
	}
	target := strings.TrimSpace(s.cfg.TargetAppPath)
	if target == "" {
		return InstallResult{}, errors.New("app update target app path is required")
	}
	if s.cfg.RequestQuit == nil {
		return InstallResult{}, errors.New("app update quit callback is required")
	}
	installCmd := exec.Command(helper,
		"-dmg", archivePath,
		"-target-app", target,
		"-main-pid", strconv.Itoa(os.Getpid()),
		"-restart=true",
	)
	if err := installCmd.Start(); err != nil {
		return InstallResult{}, fmt.Errorf("start app update helper: %w", err)
	}
	if err := installCmd.Process.Release(); err != nil {
		return InstallResult{}, fmt.Errorf("release app update helper process: %w", err)
	}
	if err := s.cfg.RequestQuit(); err != nil {
		return InstallResult{}, fmt.Errorf("request app quit for update: %w", err)
	}
	return InstallResult{Restarting: true, Version: payload.Version}, nil
}

func (s *Service) fetchManifest(ctx context.Context) (ManifestPayload, error) {
	if strings.TrimSpace(s.cfg.ManifestURL) == "" {
		return ManifestPayload{}, errors.New("app update manifest url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.ManifestURL, nil)
	if err != nil {
		return ManifestPayload{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return ManifestPayload{}, fmt.Errorf("fetch app update manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ManifestPayload{}, fmt.Errorf("fetch app update manifest: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return ManifestPayload{}, fmt.Errorf("read app update manifest: %w", err)
	}
	return VerifySignedManifest(raw, s.cfg.PublicKey, VerifyOptions{
		AppID: s.cfg.AppID, Channel: s.cfg.Channel, Platform: s.cfg.Platform, CurrentVersion: s.cfg.CurrentVersion,
	})
}

func (s *Service) downloadArtifact(ctx context.Context, payload ManifestPayload, archivePath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, payload.Artifact.URL, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("download app update artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download app update artifact: status %d", resp.StatusCode)
	}
	tmpPath := archivePath + ".download"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create app update artifact: %w", err)
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hash), resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("write app update artifact: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close app update artifact: %w", closeErr)
	}
	if n != payload.Artifact.Size {
		return fmt.Errorf("app update artifact size mismatch: got %d want %d", n, payload.Artifact.Size)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, payload.Artifact.SHA256) {
		return fmt.Errorf("app update artifact sha256 mismatch: got %s want %s", got, payload.Artifact.SHA256)
	}
	return os.Rename(tmpPath, archivePath)
}

func (s *Service) archivePath(payload ManifestPayload) string {
	return filepath.Join(s.cfg.StageDir, "Super Dolphin-"+payload.Version+"-"+payload.Platform+".dmg")
}

func (s *Service) stagedManifestPath() string {
	return filepath.Join(s.cfg.StageDir, "selected-update.json")
}

func (s *Service) writeStagedManifest(payload ManifestPayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(s.stagedManifestPath(), raw, 0o600)
}

func (s *Service) readStagedManifest() (ManifestPayload, error) {
	raw, err := os.ReadFile(s.stagedManifestPath())
	if err != nil {
		return ManifestPayload{}, fmt.Errorf("read staged app update manifest: %w", err)
	}
	var payload ManifestPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ManifestPayload{}, fmt.Errorf("parse staged app update manifest: %w", err)
	}
	return payload, nil
}

func currentVersionOrUnknown(s *Service) string {
	if s == nil || strings.TrimSpace(s.cfg.CurrentVersion) == "" {
		return "unknown"
	}
	return s.cfg.CurrentVersion
}

type ConfigParams struct {
	fx.In

	RequestQuit RequestQuit `optional:"true"`
}

func ProvideConfig(p ConfigParams) (Config, error) {
	channel := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_UPDATE_CHANNEL"))
	if channel == "" {
		channel = "gray-macos"
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	cfg := Config{
		Enabled: false,
		AppID: "com.superdolphin.app",
		Channel: channel,
		Platform: platform,
		CurrentVersion: currentVersionFromRuntime(),
		RequestQuit: p.RequestQuit,
	}
	manifestURL := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_UPDATE_MANIFEST_URL"))
	publicKeyValue := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"))
	explicitEnabled := strings.EqualFold(strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_UPDATE_ENABLED")), "1")
	if !explicitEnabled && manifestURL == "" && publicKeyValue == "" {
		return cfg, nil
	}
	if manifestURL == "" {
		return Config{}, errors.New("SUPER_DOLPHIN_UPDATE_MANIFEST_URL is required when app update is enabled")
	}
	parsedManifestURL, err := url.Parse(manifestURL)
	if err != nil || parsedManifestURL.Scheme != "https" || parsedManifestURL.Host == "" {
		return Config{}, errors.New("SUPER_DOLPHIN_UPDATE_MANIFEST_URL must be an HTTPS URL")
	}
	publicKey, err := base64.StdEncoding.DecodeString(publicKeyValue)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return Config{}, errors.New("SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be a base64 Ed25519 public key")
	}
	resourcesDir := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"))
	if resourcesDir == "" {
		return Config{}, errors.New("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR is required when app update is enabled")
	}
	if cfg.CurrentVersion == "" {
		return Config{}, errors.New("VERSION is required when app update is enabled")
	}
	stageRoot, err := macOSApplicationSupportDir()
	if err != nil {
		return Config{}, err
	}
	targetApp, err := currentAppBundlePath()
	if err != nil {
		return Config{}, err
	}
	if p.RequestQuit == nil {
		return Config{}, errors.New("app update quit callback is required when app update is enabled")
	}
	cfg.Enabled = true
	cfg.ManifestURL = manifestURL
	cfg.PublicKey = ed25519.PublicKey(publicKey)
	cfg.StageDir = filepath.Join(stageRoot, "updates")
	cfg.HelperPath = filepath.Join(resourcesDir, "bin", "super-dolphin-updater")
	cfg.TargetAppPath = targetApp
	return cfg, nil
}

func macOSApplicationSupportDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve app update home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "Super Dolphin"), nil
}

func currentAppBundlePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_UPDATE_TARGET_APP")); override != "" {
		if filepath.Ext(override) != ".app" {
			return "", errors.New("SUPER_DOLPHIN_UPDATE_TARGET_APP must point to a .app bundle")
		}
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	for dir := filepath.Dir(exe); dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if filepath.Ext(dir) == ".app" {
			return dir, nil
		}
	}
	return "", errors.New("could not resolve current .app bundle path")
}

func currentVersionFromRuntime() string {
	if value := strings.TrimSpace(os.Getenv("VERSION")); value != "" {
		return value
	}
	appPath, err := currentAppBundlePath()
	if err != nil {
		return ""
	}
	cmd := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleShortVersionString", filepath.Join(appPath, "Contents", "Info.plist"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

Add tests for `ProvideConfig` covering disabled-by-default, missing manifest URL, non-HTTPS manifest URL, malformed public key, missing resources dir, missing current version, current version read from Info.plist, missing quit callback, and valid gray config derivation.

- [ ] **Step 4: Add RPC tests**

Create `internal/module/appupdate/rpc_test.go`:

```go
package appupdate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestNewHandlersRegistersUpdateRoutes(t *testing.T) {
	handlers := NewHandlers(NewService(Config{})).Handlers
	for _, method := range []string{
		"app/update/check",
		"app/update/download",
		"app/update/install",
		"app/update/installLatest",
	} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("missing handler %s", method)
		}
	}
}

func TestCheckRouteReturnsDisabledWhenServiceDisabled(t *testing.T) {
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService(Config{})).Handlers)
	raw, err := server.Dispatch(context.Background(), "app/update/check", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	var result CheckResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if result.Available {
		t.Fatalf("available = true, want false")
	}
}
```

- [ ] **Step 5: Implement RPC and module wiring**

Create `internal/module/appupdate/rpc.go`:

```go
package appupdate

import (
	"context"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func NewHandlers(svc *Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"app/update/check": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.Check(ctx)
		}),
		"app/update/download": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.Download(ctx)
		}),
		"app/update/install": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.Install(ctx)
		}),
		"app/update/installLatest": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			if _, err := svc.Download(ctx); err != nil {
				return nil, err
			}
			return svc.Install(ctx)
		}),
	}}
}
```

Create `internal/module/appupdate/module.go`:

```go
package appupdate

import "go.uber.org/fx"

var Module = fx.Module("appupdate",
	fx.Provide(ProvideConfig),
	fx.Provide(NewService),
	fx.Provide(NewHandlers),
)
```

Add `RequestQuitForUpdate` to `internal/ui/wails/lifecycle.go`:

```go
func (l *WailsLifecycle) RequestQuitForUpdate() error {
	if l == nil {
		return errors.New("wails lifecycle is not configured")
	}
	l.NotifyBackendFailed()
	return nil
}
```

Add a lifecycle test that sets `SetQuitFunc`, calls `MarkFrontendReady`, then calls `RequestQuitForUpdate` and observes the quit callback.

In the desktop-only Wails module, provide the updater quit callback:

```go
func provideAppUpdateQuit(lifecycle *WailsLifecycle) appupdate.RequestQuit {
	return lifecycle.RequestQuitForUpdate
}
```

In `internal/app/modules.go`, import `internal/module/appupdate` and add `appupdate.Module` next to the other module entries. Do not add the `RequestQuit` provider to the core app module; wire it from `internal/ui/wails/module.go` or another desktop-only fx option so non-Wails app graphs can still construct when app update env is absent.

- [ ] **Step 6: Run module tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate ./internal/app ./internal/ui/wails -count=1
```

Expected: PASS.

- [ ] **Step 7: Run single-file guards**

Run:

```bash
./scripts/test_with_guard.sh internal/module/appupdate/manifest.go
./scripts/test_with_guard.sh internal/module/appupdate/service.go
./scripts/test_with_guard.sh internal/module/appupdate/rpc.go
./scripts/test_with_guard.sh internal/app/modules.go
./scripts/test_with_guard.sh internal/ui/wails/lifecycle.go
./scripts/test_with_guard.sh internal/ui/wails/module.go
```

Expected: each command exits 0.

---

## Task 6: Add macOS Updater Helper Binary

**Files:**
- Create: `cmd/super-dolphin-updater/main.go`
- Create: `cmd/super-dolphin-updater/install.go`
- Create: `cmd/super-dolphin-updater/install_test.go`
- Modify: `scripts/package_macos.sh`
- Modify: `scripts/package_macos_guard_test.go`

- [ ] **Step 1: Write helper install tests**

Create `cmd/super-dolphin-updater/install_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateInstallRequestRejectsMissingDMG(t *testing.T) {
	err := validateInstallRequest(installRequest{
		DMGPath: filepath.Join(t.TempDir(), "Missing.dmg"),
		TargetApp: filepath.Join(t.TempDir(), "Super Dolphin.app"),
	})
	if err == nil {
		t.Fatal("validateInstallRequest() error = nil, want missing dmg")
	}
}

func TestValidateMountedAppAcceptsMountedAppShape(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "Super Dolphin.app")
	macos := filepath.Join(staged, "Contents", "MacOS")
	resources := filepath.Join(staged, "Contents", "Resources")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(resources, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(macos, "agent-terminal"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile agent-terminal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resources, "runtime-manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile runtime-manifest: %v", err)
	}
	infoPlist := `<plist><dict><key>CFBundleIdentifier</key><string>com.superdolphin.app</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(staged, "Contents", "Info.plist"), []byte(infoPlist), 0o644); err != nil {
		t.Fatalf("WriteFile Info.plist: %v", err)
	}
	if err := validateMountedApp(staged); err != nil {
		t.Fatalf("validateMountedApp() error = %v", err)
	}
}

func TestValidateInstallRequestRejectsMissingTargetParent(t *testing.T) {
	root := t.TempDir()
	dmg := filepath.Join(root, "Super Dolphin.dmg")
	if err := os.WriteFile(dmg, []byte("dmg"), 0o600); err != nil {
		t.Fatalf("WriteFile dmg: %v", err)
	}
	target := filepath.Join(root, "missing-parent", "Super Dolphin.app")
	if err := validateInstallRequest(installRequest{DMGPath: dmg, TargetApp: target}); err == nil {
		t.Fatal("validateInstallRequest() error = nil, want missing target parent rejection")
	}
}
```

- [ ] **Step 2: Run helper tests and confirm failure**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -count=1
```

Expected: FAIL because helper package does not exist.

- [ ] **Step 3: Implement helper request validation**

Create `cmd/super-dolphin-updater/install.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type installRequest struct {
	DMGPath   string
	TargetApp string
	MainPID   int
	Restart   bool
}

func validateInstallRequest(req installRequest) error {
	dmg := strings.TrimSpace(req.DMGPath)
	target := strings.TrimSpace(req.TargetApp)
	if dmg == "" {
		return errors.New("dmg path is required")
	}
	if target == "" {
		return errors.New("target app path is required")
	}
	if filepath.Ext(dmg) != ".dmg" {
		return errors.New("update artifact must be a .dmg")
	}
	if filepath.Ext(target) != ".app" {
		return errors.New("target path must be a .app bundle")
	}
	if err := requireFile(dmg); err != nil {
		return err
	}
	if err := preflightTarget(req.TargetApp); err != nil {
		return err
	}
	return nil
}

func preflightTarget(target string) error {
	parent := filepath.Dir(target)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("target parent directory is not available: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("target parent is not a directory: %s", parent)
	}
	probe, err := os.CreateTemp(parent, ".super-dolphin-update-write-test-")
	if err != nil {
		return fmt.Errorf("target parent directory is not writable: %w", err)
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return fmt.Errorf("close write probe: %w", err)
	}
	if err := os.Remove(probePath); err != nil {
		return fmt.Errorf("remove write probe: %w", err)
	}
	if _, err := os.Stat(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat target app: %w", err)
	}
	return nil
}

func validateMountedApp(staged string) error {
	if filepath.Ext(staged) != ".app" {
		return errors.New("mounted update payload must be a .app bundle")
	}
	if err := requireExecutable(filepath.Join(staged, "Contents", "MacOS", "agent-terminal")); err != nil {
		return err
	}
	if err := requireFile(filepath.Join(staged, "Contents", "Resources", "runtime-manifest.json")); err != nil {
		return err
	}
	if err := verifyBundleID(staged, "com.superdolphin.app"); err != nil {
		return err
	}
	return nil
}

func installUpdate(req installRequest) error {
	if err := validateInstallRequest(req); err != nil {
		return err
	}
	expectedTeamID, err := expectedTeamIDForUpdate(req.TargetApp)
	if err != nil {
		return err
	}
	if req.MainPID > 0 {
		if err := waitForPIDExit(req.MainPID, 30*time.Second); err != nil {
			return err
		}
	}
	mountDir, err := mountDMG(req.DMGPath)
	if err != nil {
		return err
	}
	defer detachDMG(mountDir)
	stagedApp := filepath.Join(mountDir, "Super Dolphin.app")
	if err := validateMountedApp(stagedApp); err != nil {
		return err
	}
	if err := verifyCodeSignature(stagedApp, expectedTeamID); err != nil {
		return err
	}
	if err := verifyGatekeeperAssessment(stagedApp); err != nil {
		return err
	}
	backup := req.TargetApp + ".previous-" + strconv.FormatInt(time.Now().Unix(), 10)
	if _, err := os.Stat(req.TargetApp); err == nil {
		if err := os.Rename(req.TargetApp, backup); err != nil {
			return fmt.Errorf("move current app to backup: %w", err)
		}
	}
	if err := dittoCopy(stagedApp, req.TargetApp); err != nil {
		return errors.Join(err, rollbackReplacement(req.TargetApp, backup))
	}
	if err := verifyCodeSignature(req.TargetApp, expectedTeamID); err != nil {
		return errors.Join(err, rollbackReplacement(req.TargetApp, backup))
	}
	if err := verifyGatekeeperAssessment(req.TargetApp); err != nil {
		return errors.Join(err, rollbackReplacement(req.TargetApp, backup))
	}
	if err := clearQuarantineAfterVerification(req.TargetApp); err != nil {
		return errors.Join(err, rollbackReplacement(req.TargetApp, backup))
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	if req.Restart {
		return exec.Command("open", req.TargetApp).Start()
	}
	return nil
}

func rollbackReplacement(target, backup string) error {
	if backup == "" {
		return nil
	}
	var errs []error
	if err := os.RemoveAll(target); err != nil {
		errs = append(errs, fmt.Errorf("remove failed replacement: %w", err))
	}
	if err := os.Rename(backup, target); err != nil {
		errs = append(errs, fmt.Errorf("restore backup app: %w", err))
	}
	return errors.Join(errs...)
}

func mountDMG(path string) (string, error) {
	mountDir, err := os.MkdirTemp("", "super-dolphin-update-")
	if err != nil {
		return "", err
	}
	cmd := exec.Command("hdiutil", "attach", path, "-nobrowse", "-readonly", "-mountpoint", mountDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(mountDir)
		return "", fmt.Errorf("mount update dmg: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return mountDir, nil
}

func detachDMG(mountDir string) {
	if err := exec.Command("hdiutil", "detach", mountDir).Run(); err != nil {
		_ = exec.Command("hdiutil", "detach", "-force", mountDir).Run()
	}
	_ = os.RemoveAll(mountDir)
}

func requireExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("missing executable %s: %w", path, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("path is not executable: %s", path)
	}
	return nil
}

func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("missing file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is not a file: %s", path)
	}
	return nil
}

func waitForPIDExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := exec.Command("kill", "-0", strconv.Itoa(pid)).Run(); err != nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("process %d did not exit before update timeout", pid)
}

func verifyCodeSignature(appPath, expectedTeamID string) error {
	cmd := exec.Command("codesign", "--verify", "--deep", "--strict", "--verbose=2", appPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("verify staged app code signature: %w: %s", err, strings.TrimSpace(string(out)))
	}
	teamID, err := teamIDFromSignature(appPath)
	if err != nil {
		return err
	}
	if expectedTeamID != "" && teamID != expectedTeamID {
		return fmt.Errorf("team id = %q, want %q", teamID, expectedTeamID)
	}
	return nil
}

func expectedTeamIDForUpdate(targetApp string) (string, error) {
	if value := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_EXPECTED_TEAM_ID")); value != "" {
		return value, nil
	}
	if _, err := os.Stat(targetApp); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("SUPER_DOLPHIN_EXPECTED_TEAM_ID is required when target app does not exist")
		}
		return "", fmt.Errorf("stat target app for team id: %w", err)
	}
	return teamIDFromSignature(targetApp)
}

func teamIDFromSignature(appPath string) (string, error) {
	info := exec.Command("codesign", "-dv", "--verbose=4", appPath)
	out, err := info.CombinedOutput()
	text := string(out)
	if err != nil {
		return fmt.Errorf("inspect app code signature: %w: %s", err, strings.TrimSpace(text))
	}
	if !strings.Contains(text, "Authority=Developer ID Application:") {
		return errors.New("app is not signed with a Developer ID Application identity")
	}
	for _, line := range strings.Split(text, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	return "", errors.New("app signature does not include TeamIdentifier")
}

func verifyGatekeeperAssessment(appPath string) error {
	cmd := exec.Command("spctl", "-a", "-vv", "-t", "execute", appPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("assess app with Gatekeeper: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func verifyBundleID(appPath, want string) error {
	cmd := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", filepath.Join(appPath, "Contents", "Info.plist"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("read bundle id: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) != want {
		return fmt.Errorf("bundle id = %q, want %q", strings.TrimSpace(string(out)), want)
	}
	return nil
}

func clearQuarantineAfterVerification(appPath string) error {
	cmd := exec.Command("xattr", "-dr", "com.apple.quarantine", appPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		text := strings.TrimSpace(string(out))
		if onlyNoSuchXattr(text) {
			return nil
		}
		return fmt.Errorf("clear quarantine after signature and Gatekeeper verification: %w: %s", err, text)
	}
	return nil
}

func onlyNoSuchXattr(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "No such xattr") {
			return false
		}
	}
	return true
}

func dittoCopy(src, dst string) error {
	cmd := exec.Command("ditto", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copy staged app into place: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
```

Create `cmd/super-dolphin-updater/main.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var req installRequest
	flag.StringVar(&req.DMGPath, "dmg", "", "path to downloaded Super Dolphin.dmg")
	flag.StringVar(&req.TargetApp, "target-app", "/Applications/Super Dolphin.app", "path to installed Super Dolphin.app")
	flag.IntVar(&req.MainPID, "main-pid", 0, "main app pid to wait for")
	flag.BoolVar(&req.Restart, "restart", true, "restart app after install")
	flag.Parse()

	if err := installUpdate(req); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Add helper to macOS package**

In `scripts/package_macos.sh`, in `phase_start "go binaries"` block, add:

```bash
  go build -o bin/super-dolphin-updater ./cmd/super-dolphin-updater
```

In `phase_start "copy app resources"` block, add:

```bash
cp "$root/bin/super-dolphin-updater" "$resources/bin/super-dolphin-updater"
```

- [ ] **Step 5: Add package guard test for helper**

Add to `scripts/package_macos_guard_test.go`:

```go
func TestPackageMacOSBundlesUpdaterHelper(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "go build -o bin/super-dolphin-updater ./cmd/super-dolphin-updater")
	assertScriptContains(t, script, `cp "$root/bin/super-dolphin-updater" "$resources/bin/super-dolphin-updater"`)
	assertScriptOrder(t, script, "go build -o bin/super-dolphin-updater ./cmd/super-dolphin-updater", `sign_macho_tree "$codesign_identity"`)
}
```

- [ ] **Step 6: Run helper and script tests**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -count=1
./scripts/test_with_guard.sh ./scripts -run 'TestPackageMacOSBundlesUpdaterHelper' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run single-file guards**

Run:

```bash
./scripts/test_with_guard.sh cmd/super-dolphin-updater/main.go
./scripts/test_with_guard.sh cmd/super-dolphin-updater/install.go
./scripts/test_with_guard.sh scripts/package_macos_guard_test.go
```

Expected: each command exits 0.

---

## Task 7: Wire Update UI into Settings About Panel

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `frontend-app/src/pages/settings/SettingsPage.jsx`
- Modify: `frontend-app/src/pages/settings/SettingsPage.test.jsx`
- Modify: `frontend-app/src/SettingsPage.test.jsx`

- [ ] **Step 1: Write failing frontend API tests**

Add to `frontend-app/src/shared/api/backendApi.test.js`:

```js
it('wraps app update RPC methods', async () => {
  const callAPI = vi.fn();
  const api = createBackendApi({ callAPI, getBuildInfo: vi.fn() });
  callAPI.mockResolvedValueOnce({ available: false });
  await api.checkAppUpdate();
  expect(callAPI).toHaveBeenCalledWith('app/update/check', {});

  callAPI.mockResolvedValueOnce({ version: '0.1.1' });
  await api.downloadAppUpdate();
  expect(callAPI).toHaveBeenCalledWith('app/update/download', {});

  callAPI.mockResolvedValueOnce({ restarting: true });
  await api.installAppUpdate();
  expect(callAPI).toHaveBeenCalledWith('app/update/install', {});

  callAPI.mockResolvedValueOnce({ restarting: true });
  await api.installLatestAppUpdate();
  expect(callAPI).toHaveBeenCalledWith('app/update/installLatest', {});
});
```

- [ ] **Step 2: Add backend API facade**

In `frontend-app/src/shared/api/backendApi.js`, add constants:

```js
APP_UPDATE_CHECK: 'app/update/check',
APP_UPDATE_DOWNLOAD: 'app/update/download',
APP_UPDATE_INSTALL: 'app/update/install',
APP_UPDATE_INSTALL_LATEST: 'app/update/installLatest',
```

In the created backend API object, add:

```js
checkAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_CHECK, {}),
downloadAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_DOWNLOAD, {}),
installAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_INSTALL, {}),
installLatestAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_INSTALL_LATEST, {}),
```

Export:

```js
export const checkAppUpdate = backendApi.checkAppUpdate;
export const downloadAppUpdate = backendApi.downloadAppUpdate;
export const installAppUpdate = backendApi.installAppUpdate;
export const installLatestAppUpdate = backendApi.installLatestAppUpdate;
```

- [ ] **Step 3: Write failing Settings UI test**

Add to `frontend-app/src/pages/settings/SettingsPage.test.jsx`:

```jsx
it('checks and installs an available app update from the settings about panel', async () => {
  let resolveInstall;
  const installPromise = new Promise((resolve) => {
    resolveInstall = resolve;
  });
  backend.checkAppUpdate.mockResolvedValueOnce({
    available: true,
    version: '0.1.1',
    currentVersion: '0.1.0',
    releaseNotes: '灰度更新',
    size: 123,
  });
  backend.installLatestAppUpdate.mockReturnValueOnce(installPromise);

  await renderSettingsPage();
  fireEvent.click(screen.getByTestId('settings-update-check-button'));

  expect(await screen.findByTestId('settings-update-notice')).toHaveTextContent('发现新版本 0.1.1');
  fireEvent.click(screen.getByTestId('settings-update-install-button'));

  await waitFor(() => {
    expect(backend.installLatestAppUpdate).toHaveBeenCalled();
  });
  expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();

  resolveInstall({ restarting: true });
  await screen.findByText(/更新安装已启动/);
  expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('更新安装已启动');
  expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
});

it('clears stale update info when a later check fails or reports no update', async () => {
  backend.checkAppUpdate
    .mockResolvedValueOnce({ available: true, version: '0.1.1', currentVersion: '0.1.0' })
    .mockResolvedValueOnce({ available: false, currentVersion: '0.1.1' });

  await renderSettingsPage();
  fireEvent.click(screen.getByTestId('settings-update-check-button'));
  expect(await screen.findByTestId('settings-update-install-button')).toBeInTheDocument();

  fireEvent.click(screen.getByTestId('settings-update-check-button'));
  await waitFor(() => {
    expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
  });
  expect(screen.getByTestId('settings-update-notice')).toHaveTextContent('当前已是最新版本');

  backend.checkAppUpdate.mockResolvedValueOnce({ available: true, version: '0.1.2', currentVersion: '0.1.1' });
  fireEvent.click(screen.getByTestId('settings-update-check-button'));
  expect(await screen.findByTestId('settings-update-install-button')).toBeInTheDocument();

  backend.checkAppUpdate.mockRejectedValueOnce(new Error('network down'));
  fireEvent.click(screen.getByTestId('settings-update-check-button'));
  await waitFor(() => {
    expect(screen.queryByTestId('settings-update-install-button')).not.toBeInTheDocument();
  });
  expect(await screen.findByText(/检查更新失败/)).toBeInTheDocument();
});

it('restores install controls when app update installation fails', async () => {
  backend.checkAppUpdate.mockResolvedValueOnce({
    available: true,
    version: '0.1.1',
    currentVersion: '0.1.0',
  });
  backend.installLatestAppUpdate.mockRejectedValueOnce(new Error('helper failed'));

  await renderSettingsPage();
  fireEvent.click(screen.getByTestId('settings-update-check-button'));
  expect(await screen.findByTestId('settings-update-install-button')).toBeInTheDocument();

  fireEvent.click(screen.getByTestId('settings-update-install-button'));
  expect(await screen.findByText(/安装更新失败/)).toBeInTheDocument();
  expect(screen.getByTestId('settings-update-install-button')).toBeEnabled();
});
```

Update the mocked backend object in this test file:

```js
checkAppUpdate: vi.fn(),
downloadAppUpdate: vi.fn(),
installAppUpdate: vi.fn(),
installLatestAppUpdate: vi.fn(),
```

Apply the same mocked backend additions in `frontend-app/src/SettingsPage.test.jsx`; this root-level test imports the same backend facade and must not fail during full `npm test`.

- [ ] **Step 4: Implement Settings update panel**

In `frontend-app/src/pages/settings/SettingsPage.jsx`, extend the import from backend API:

```js
import { callBackend, checkAppUpdate, copyTextToClipboard, getBuildInfo, getPreference, installLatestAppUpdate, listDashboardLogs, readBuiltinTools, readConfig, readLspPromptHint, setPreference, writeBuiltinTool, writeLspPromptHint as writeLspPromptHintBackend } from '../../shared/api/backendApi.js';
```

Inside `useSettingsRuntime`, add state:

```js
const [updateInfo, setUpdateInfo] = useState(null);
const [updateBusy, setUpdateBusy] = useState(false);
const [updateInstalling, setUpdateInstalling] = useState(false);
const [updateNotice, setUpdateNotice] = useState('');
```

Add callbacks:

```js
const checkForUpdate = useCallback(async () => {
  setUpdateBusy(true);
  setUpdateInfo(null);
  setUpdateNotice('');
  setError('');
  try {
    const result = await checkAppUpdate();
    if (result?.available) {
      setUpdateInfo(result);
      setUpdateNotice('发现新版本 ' + result.version);
    } else {
      setUpdateInfo(null);
      setUpdateNotice('当前已是最新版本');
    }
  } catch (err) {
    setUpdateInfo(null);
    setError('检查更新失败：' + (err.message || String(err)));
  } finally {
    setUpdateBusy(false);
  }
}, []);

const installUpdate = useCallback(async () => {
  if (!updateInfo?.available || updateInstalling) {
    return;
  }
  setUpdateBusy(true);
  setUpdateInstalling(true);
  setError('');
  try {
    await installLatestAppUpdate();
    setUpdateNotice('更新安装已启动，应用即将重启');
  } catch (err) {
    setUpdateInstalling(false);
    setError('安装更新失败：' + (err.message || String(err)));
  } finally {
    setUpdateBusy(false);
  }
}, [updateInfo, updateInstalling]);
```

Return these fields from `useSettingsRuntime`:

```js
return { buildInfo, changeActiveProvider, checkForUpdate, error, form, installUpdate, refreshBuildInfo, saveProviderSettings, saveRuntimeSettings, status, updateBusy, updateForm, updateInfo, updateInstalling, updateNotice };
```

Change `AboutPanel` signature:

```jsx
function AboutPanel({ buildInfo, cwd, runtime }) {
```

Render update controls below the `<dl>`:

```jsx
<div className="settings-action-row settings-action-inline">
  <button className="btn btn-secondary btn-toolbar-sm" type="button" data-testid="settings-update-check-button" onClick={() => void runtime.checkForUpdate()} disabled={runtime.updateBusy || runtime.updateInstalling}>检查更新</button>
  {runtime.updateInfo?.available && !runtime.updateInstalling ? <button className="btn btn-primary btn-toolbar-sm" type="button" data-testid="settings-update-install-button" onClick={() => void runtime.installUpdate()} disabled={runtime.updateBusy || runtime.updateInstalling}>安装更新</button> : null}
  {runtime.updateNotice ? <span className="settings-page-notice settings-status" data-testid="settings-update-notice" role="status">{runtime.updateNotice}</span> : null}
</div>
```

Update caller:

```jsx
<AboutPanel buildInfo={runtime.buildInfo} cwd={cwd} runtime={runtime} />
```

- [ ] **Step 5: Run frontend tests**

Run:

```bash
cd frontend-app
npm test -- backendApi.test.js SettingsPage.test.jsx
```

Expected: PASS.

- [ ] **Step 6: Run frontend lint/build**

Run:

```bash
cd frontend-app
npm run lint
npm run build
```

Expected: PASS and `frontend-app/dist/index.html` exists.

---

## Task 8: Add Packaged Functionality and Update Smoke

**Files:**
- Modify: `docs/scripts/macos_release_smoke.sh`
- Modify: `docs/运维发布/打包发布/macos-gray-release.md`

- [ ] **Step 1: Add smoke usage text**

In `docs/scripts/macos_release_smoke.sh`, change usage text to include:

```text
  packaged-tools Validate packaged Codex, sidecars, LSP, Git, PostgreSQL, and runtime manifest under a clean environment.
  update-loop     Validate replacing an installed old app with a downloaded newer DMG.
```

- [ ] **Step 2: Add packaged tools smoke**

Add this function:

```bash
packaged_tools_smoke() {
  require_darwin
  local resources binary tmp_home tmp_dir pid stat status
  resources="$(resource_dir)"
  binary="$app_path/Contents/MacOS/agent-terminal"
  require_exec "$binary"
  require_exec "$resources/bin/codex"
  require_exec "$resources/bin/mcp-orch"
  require_exec "$resources/bin/mcp-lsp"
  require_exec "$resources/bin/super-dolphin-updater"
  require_exec "$resources/bin/git"
  require_exec "$resources/lsp/bin/gopls"
  require_exec "$resources/postgres/$(go env GOOS)-$(go env GOARCH)/bin/postgres"
  "$resources/bin/codex" app-server --help >/dev/null
  "$resources/lsp/bin/gopls" version >/dev/null
  "$resources/bin/git" --version >/dev/null
  tmp_home="$(mktemp -d)"
  tmp_dir="$(mktemp -d)"
  env -i \
    HOME="$tmp_home" \
    PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
    TMPDIR="$tmp_dir" \
    LOG_LEVEL="debug" \
    "$binary" &
  pid=$!
  sleep "${STARTUP_WINDOW_SECONDS:-20}"
  stat="$(ps -p "$pid" -o stat= 2>/dev/null || true)"
  if [[ -z "$stat" || "$stat" == *Z* ]]; then
    set +e
    wait "$pid"
    status=$?
    set -e
    rm -rf "$tmp_home" "$tmp_dir"
    fail "packaged app exited during packaged-tools smoke; exit_code=$status"
  fi
  pgrep -P "$pid" -f "mcp-orch" >/dev/null || fail "mcp-orch was not launched by packaged app"
  pgrep -P "$pid" -f "mcp-lsp" >/dev/null || fail "mcp-lsp was not launched by packaged app"
  kill -TERM "$pid" 2>/dev/null || true
  sleep 5
  kill -KILL "$pid" 2>/dev/null || true
  set +e
  wait "$pid"
  set -e
  rm -rf "$tmp_home" "$tmp_dir"
}
```

Add this case branch:

```bash
  packaged-tools)
    log_run "$log_dir/macos-packaged-tools-smoke.log" packaged_tools_smoke
    ;;
```

- [ ] **Step 3: Add update-loop smoke placeholder-free contract**

Add this function:

```bash
update_loop_smoke() {
  require_darwin
  local update_dmg="${UPDATE_DMG:-}"
  local installed_root installed_app helper old_app_path
  require_file "$update_dmg"
  helper="$(resource_dir)/bin/super-dolphin-updater"
  require_exec "$helper"
  installed_root="$(mktemp -d)"
  installed_app="$installed_root/Super Dolphin.app"
  old_app_path="$app_path"
  ditto "$app_path" "$installed_app"
  "$helper" -dmg "$update_dmg" -target-app "$installed_app" -main-pid 0 -restart=false
  "$root/scripts/verify_packaged_app_macos.sh" "$installed_app"
  app_path="$installed_app"
  packaged_tools_smoke
  app_path="$old_app_path"
  rm -rf "$installed_root"
}
```

Add this case branch:

```bash
  update-loop)
    log_run "$log_dir/macos-update-loop-smoke.log" update_loop_smoke
    ;;
```

- [ ] **Step 4: Run shell syntax check**

Run:

```bash
bash -n docs/scripts/macos_release_smoke.sh
```

Expected: exit 0.

- [ ] **Step 5: Run guard**

Run:

```bash
make guard
```

Expected: PASS.

---

## Task 9: Release Manifest Publishing Command

**Files:**
- Create: `cmd/super-dolphin-release-manifest/main.go`
- Create: `cmd/super-dolphin-release-manifest/main_test.go`

- [ ] **Step 1: Write failing command test**

Create `cmd/super-dolphin-release-manifest/main_test.go`:

```go
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildManifestCommandWritesSignedManifest(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	root := t.TempDir()
	keyPath := filepath.Join(root, "key")
	if err := os.WriteFile(keyPath, priv, 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	artifact := filepath.Join(root, "Super Dolphin-0.1.1-darwin-arm64.dmg")
	if err := os.WriteFile(artifact, []byte("dmg"), 0o600); err != nil {
		t.Fatalf("WriteFile artifact: %v", err)
	}
	out := filepath.Join(root, "latest.json")
	err = run([]string{
		"-artifact", artifact,
		"-artifact-url", "https://updates.example.com/Super%20Dolphin-0.1.1-darwin-arm64.dmg",
		"-app-id", "com.superdolphin.app",
		"-channel", "gray-macos",
		"-version", "0.1.1",
		"-minimum-version", "0.1.0",
		"-platform", "darwin-arm64",
		"-signing-key", keyPath,
		"-out", out,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("manifest is empty")
	}
}
```

- [ ] **Step 2: Run command test and confirm failure**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/super-dolphin-release-manifest -count=1
```

Expected: FAIL because the command does not exist.

- [ ] **Step 3: Implement command using appupdate manifest types**

Create `cmd/super-dolphin-release-manifest/main.go`:

```go
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/appupdate"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	var artifactPath, artifactURL, appID, channel, version, minimumVersion, platform, signingKeyPath, outPath, notes string
	fs := flag.NewFlagSet("super-dolphin-release-manifest", flag.ContinueOnError)
	fs.StringVar(&artifactPath, "artifact", "", "path to notarized Super Dolphin.dmg")
	fs.StringVar(&artifactURL, "artifact-url", "", "download URL")
	fs.StringVar(&appID, "app-id", "", "bundle id")
	fs.StringVar(&channel, "channel", "", "release channel")
	fs.StringVar(&version, "version", "", "release version")
	fs.StringVar(&minimumVersion, "minimum-version", "", "minimum current version")
	fs.StringVar(&platform, "platform", "", "platform such as darwin-arm64")
	fs.StringVar(&signingKeyPath, "signing-key", "", "Ed25519 private key bytes")
	fs.StringVar(&outPath, "out", "", "output latest.json")
	fs.StringVar(&notes, "notes", "", "release notes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}
	key, err := os.ReadFile(signingKeyPath)
	if err != nil {
		return fmt.Errorf("read signing key: %w", err)
	}
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("signing key must be %d bytes", ed25519.PrivateKeySize)
	}
	sum := sha256.Sum256(data)
	payload := appupdate.ManifestPayload{
		SchemaVersion: 1,
		AppID: appID,
		Channel: channel,
		Version: version,
		Platform: platform,
		MinimumVersion: minimumVersion,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
		Artifact: appupdate.UpdateArtifact{URL: artifactURL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))},
		ReleaseNotes: notes,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	doc := appupdate.SignedManifest{
		Payload: payload,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(key), payloadBytes)),
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, append(raw, '\n'), 0o644)
}
```

- [ ] **Step 4: Run command tests**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/super-dolphin-release-manifest ./internal/module/appupdate -count=1
```

Expected: PASS.

---

## Task 10: End-to-End Verification

**Files:**
- No new implementation files.
- Uses changed package, frontend, helper, and smoke scripts.

- [ ] **Step 1: Run Go focused tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate ./internal/provider/codexapp ./cmd/super-dolphin-updater ./cmd/super-dolphin-release-manifest ./internal/app ./internal/ui/wails -count=1
```

Expected: PASS.

- [ ] **Step 2: Run script guard tests**

Run:

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackageMacOS|TestVerifyPackagedAppMacOS' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend checks**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all PASS.

- [ ] **Step 4: Build signed gray package on macOS**

Run from repository root with release env set:

```bash
./scripts/package_macos.sh
```

Expected:

- `dist/package/macos/Super Dolphin.app` exists.
- `dist/package/macos/Super Dolphin.dmg` exists.
- `dist/package/macos/Super Dolphin.dmg.sha256` exists.
- DMG is notarized and stapled when `SUPER_DOLPHIN_RELEASE_PROFILE=gray`.

- [ ] **Step 5: Run local macOS package smokes**

Run:

```bash
docs/scripts/macos_release_smoke.sh local
docs/scripts/macos_release_smoke.sh notarized-dmg
docs/scripts/macos_release_smoke.sh startup
docs/scripts/macos_release_smoke.sh packaged-tools
```

Expected: all PASS.

- [ ] **Step 6: Generate update manifest**

Run:

```bash
packaged_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' 'dist/package/macos/Super Dolphin.app/Contents/Info.plist')"
go run ./cmd/super-dolphin-release-manifest \
  -artifact "dist/package/macos/Super Dolphin.dmg" \
  -artifact-url "https://updates.example.com/Super%20Dolphin.dmg" \
  -app-id "com.superdolphin.app" \
  -channel "gray-macos" \
  -version "$packaged_version" \
  -minimum-version "0.1.0" \
  -platform "$(go env GOOS)-$(go env GOARCH)" \
  -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
  -out "dist/package/macos/latest.json"
```

Expected: `dist/package/macos/latest.json` exists and verifies with `internal/module/appupdate` tests when served by a test server.

- [ ] **Step 7: Run update-loop smoke**

Run:

```bash
UPDATE_DMG="dist/package/macos/Super Dolphin.dmg" \
docs/scripts/macos_release_smoke.sh update-loop
```

Expected: PASS.

- [ ] **Step 8: Clean VM acceptance**

On a clean macOS VM:

```bash
docs/scripts/macos_release_smoke.sh notarized-dmg
```

Then manually verify:

- Install the DMG into `/Applications`.
- Launch `Super Dolphin.app`.
- Create one Codex conversation.
- Send one prompt.
- Confirm the response returns.
- Confirm process list includes `agent-terminal`, `mcp-orch`, `mcp-lsp`, `codex`, and embedded `postgres`.
- Publish a newer `latest.json` and `Super Dolphin.dmg` to the gray update endpoint.
- Click Settings → About → Check Update.
- Click Install Update.
- Confirm app restarts into the newer version.
- Confirm existing local threads/data remain visible.

---

## Acceptance Criteria

- A gray macOS build cannot be produced without Developer ID signing and notarization credentials.
- The package contains `agent-terminal`, `mcp-orch`, `mcp-lsp`, `mcp-ida`, `super-dolphin-updater`, Codex CLI, LSP bundle, Git, embedded PostgreSQL, migrations, model registry, relay `.env`, runtime manifest, Codex manifest, and LSP manifest.
- Packaged runtime fails fast if required managed peers cannot launch.
- The single user-facing and update artifact is `Super Dolphin.dmg`, notarized/stapled, and accompanied by sha256 and signed manifest.
- The App can check updates and start installation from Settings.
- The helper replaces the installed app only after validating staged app structure and code signature.
- Clean VM smoke proves app startup and one Codex turn before release is declared usable.
- No dev/run-debug behavior regresses.

## Execution Order

1. Task 1: signing/profile gate.
2. Task 2: packaged peer fail-fast.
3. Task 3: DMG update artifact.
4. Task 4: signed manifest verification.
5. Task 5: update service/RPC.
6. Task 6: updater helper.
7. Task 7: Settings UI.
8. Task 8: smoke scripts.
9. Task 9: release manifest command.
10. Task 10: full verification.

## Risk Notes

- `/Applications` replacement can fail for non-admin users. In gray scope this must fail with a clear error; do not add hidden privilege escalation.
- `codesign --verify` is local and deterministic; notarization/stapler checks depend on Apple tooling and network during packaging.
- App update must not delete app data under `~/Library/Application Support/Super Dolphin`.
- Current Wails version does not provide the documented latest `app.Updater`; using a project-owned helper avoids Wails alpha upgrade risk.
- If gray testers need Claude provider out of the box, that is a separate dependency decision because current Claude provider expects an external `claude` CLI.
