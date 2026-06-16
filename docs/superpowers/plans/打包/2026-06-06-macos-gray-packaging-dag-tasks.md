# macOS 灰度打包 DAG 执行任务拆分

> Source plan: `docs/superpowers/plans/打包/2026-06-06-macos-gray-packaging-and-updater.md`

**Goal:** 把 macOS 灰度打包与一键更新实施计划拆成可并行、可审查、可验证的 DAG 执行任务。

**Execution rule:** 每个节点只改自己的 write scope，不回滚其他节点变更。每个 Go 文件修改后先跑单文件守卫；每个 DAG 的 gate 节点负责汇总验证。用户灰度交付物始终只有 `Super Dolphin.dmg`。

---

## 全局约束

- 不升级 Wails。
- 不做 Windows/Linux。
- 不引入 `.app.zip` 更新产物。
- 灰度用户不需要单独安装 Codex。
- `SUPER_DOLPHIN_CODEX_ARTIFACT` 只作为打包机可信输入，最终必须被复制进 `Super Dolphin.app/Contents/Resources/bin/codex`。
- 签名/公证 gate 只在 `SUPER_DOLPHIN_RELEASE_PROFILE=gray` 下强制。
- packaged runtime 缺失 Codex、MCP sidecar、LSP、Git、PostgreSQL 或 runtime manifest 时必须 fail-fast。

## DAG 总览

| DAG | 目的 | 可并行性 | 最终 gate |
| --- | --- | --- | --- |
| `macos_gray_foundation` | 打包完整性底座：peer fail-fast、manifest 验签、helper、DMG profile | 4 个根节点可并行 | `foundation_gate` |
| `macos_gray_update_backend` | 后端更新能力：下载 DMG、校验、RPC、manifest 生成命令 | 2 个节点可并行 | `backend_gate` |
| `macos_gray_frontend` | 设置页一键更新入口 | 串行，避免 API/UI 测试冲突 | `frontend_gate` |
| `macos_gray_release_smoke` | 打包脚本、smoke、真实灰度验收 | 串行为主 | `clean_vm_acceptance` |

跨 DAG 依赖：

```text
macos_gray_foundation.foundation_gate
  -> macos_gray_update_backend.appupdate_service_rpc
  -> macos_gray_update_backend.release_manifest_cmd

macos_gray_update_backend.backend_gate
  -> macos_gray_frontend.backend_api_facade
  -> macos_gray_release_smoke.smoke_scripts

macos_gray_foundation.foundation_gate
  -> macos_gray_release_smoke.bundle_helper_in_package
  -> macos_gray_release_smoke.build_gray_dmg
  -> macos_gray_release_smoke.gray_manifest_publish

macos_gray_frontend.frontend_gate
  -> macos_gray_release_smoke.build_gray_dmg
  -> macos_gray_release_smoke.package_smoke_gate
```

---

## DAG 1: `macos_gray_foundation`

**Purpose:** 先打牢底座，证明打包后项目工具不会静默缺失。

**Suggested `task_create_dag` sketch:**

```yaml
dag_key: macos_gray_foundation
title: macOS 灰度打包底座
schedule:
  trigger: manual
final_node_key: foundation_gate
nodes:
  - node_key: peer_failfast
    title: Packaged peer fail-fast
    node_type: agent
    depends_on: []
  - node_key: update_manifest_verifier
    title: Signed update manifest verifier
    node_type: agent
    depends_on: []
  - node_key: updater_helper
    title: DMG updater helper
    node_type: agent
    depends_on: []
  - node_key: release_profile_dmg
    title: Gray release profile and single DMG artifact
    node_type: agent
    depends_on: []
  - node_key: foundation_gate
    title: Foundation verification gate
    node_type: agent
    depends_on:
      - peer_failfast
      - update_manifest_verifier
      - updater_helper
      - release_profile_dmg
```

### Node `peer_failfast`

**Objective:** packaged runtime 下 `mcp-orch` / `mcp-lsp` 初始启动失败必须返回错误，dev 保持 best-effort。

**Write scope:**

- `internal/provider/codexapp/peer_supervisor.go`
- `internal/provider/codexapp/peer_supervisor_test.go`

**Steps:**

- [ ] 添加 `TestPeerSupervisorPackagedInitialLaunchFailureIsFatal`。
- [ ] 添加 `TestPeerSupervisorDevInitialLaunchFailureStaysBestEffort`。
- [ ] 实现 `packagedPeerLaunchRequired()`。
- [ ] 在 initial launch failure 分支中 packaged 返回 `packaged peer launch failed`。

**Verification:**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
./scripts/test_with_guard.sh internal/provider/codexapp/peer_supervisor.go
```

**Handoff output:**

- changed files
- test output summary
- 是否仍存在 packaged peer warn+skip 路径

### Node `update_manifest_verifier`

**Objective:** 建立 signed manifest schema、Ed25519 验签、HTTPS artifact URL、平台匹配、版本比较、DMG sha256/size 校验。

**Write scope:**

- `internal/module/appupdate/manifest.go`
- `internal/module/appupdate/manifest_test.go`

**Steps:**

- [ ] 新建 `ManifestPayload`、`SignedManifest`、`UpdateArtifact`、`VerifyOptions`。
- [ ] 写入验签通过测试。
- [ ] 写入篡改拒绝、平台不匹配拒绝、非 HTTPS artifact URL 拒绝测试。
- [ ] 写入 malformed version 拒绝测试；版本解析不能把 `Atoi` 错误静默当 0。
- [ ] 写入非升级返回 `ErrNoUpdate` 的测试；`Check` 层映射为 `available:false`，下载/安装层仍 fail-fast。
- [ ] 实现 `VerifySignedManifest`。

**Verification:**

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate -count=1
./scripts/test_with_guard.sh internal/module/appupdate/manifest.go
```

**Handoff output:**

- manifest JSON schema
- version comparison rule
- public key 输入方式建议

### Node `updater_helper`

**Objective:** 新增包内 helper，接收 `-dmg`，挂载 DMG，验证里面的 `.app` 和签名，替换目标 app 并按需重启。

**Write scope:**

- `cmd/super-dolphin-updater/main.go`
- `cmd/super-dolphin-updater/install.go`
- `cmd/super-dolphin-updater/install_test.go`

**Steps:**

- [ ] 写 `validateInstallRequest` 缺 DMG/缺 target 的失败测试。
- [ ] 写 `validateMountedApp` 接受合法 `.app` 结构测试。
- [ ] 写目标父目录不可用/不可写的预检测试；helper 必须在主 App 退出前暴露这类错误。
- [ ] 实现 `mountDMG` / `detachDMG`。
- [ ] 实现 `verifyBundleID(com.superdolphin.app)`、`verifyCodeSignature`、Developer ID/Team ID 检查、`spctl -a -vv -t execute`。
- [ ] Team ID 必须和当前已安装 app 或 `SUPER_DOLPHIN_EXPECTED_TEAM_ID` 一致，不能只检查存在任意 TeamIdentifier。
- [ ] 实现备份、替换、失败回滚、重启。
- [ ] 复制后对目标 `.app` 再跑 codesign + spctl，通过后才清理 quarantine；只有全部 xattr 错误行都属于 `No such xattr` 时才视为无需清理，真实 post-copy codesign/spctl/xattr 错误必须恢复 backup，且 rollback 失败必须作为错误返回，不能吞掉。
- [ ] `detachDMG` 失败时重试 `hdiutil detach -force` 并清理 mount dir。

**Verification:**

```bash
./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -count=1
./scripts/test_with_guard.sh cmd/super-dolphin-updater/main.go
./scripts/test_with_guard.sh cmd/super-dolphin-updater/install.go
```

**Handoff output:**

- helper CLI 参数
- 替换流程
- 失败场景清单

### Node `release_profile_dmg`

**Objective:** `package_macos.sh` 支持 `dev-local|gray` profile，gray 强制 Developer ID + notarization，并把 DMG 作为唯一用户/更新产物。

**Write scope:**

- `scripts/package_macos.sh`
- `scripts/package_macos_guard_test.go`
- `scripts/verify_packaged_app_macos.sh`
- `docs/运维发布/打包发布/macos-gray-release.md`

**Steps:**

- [ ] 增加 `SUPER_DOLPHIN_RELEASE_PROFILE`。
- [ ] gray profile 强制 `CODESIGN_IDENTITY` 和 `NOTARY_PROFILE`。
- [ ] gray profile 强制 `SUPER_DOLPHIN_UPDATE_MANIFEST_URL` 和 `SUPER_DOLPHIN_UPDATE_PUBLIC_KEY`，并把 update 配置和 `VERSION` 写入包内 `.env`。
- [ ] 包内 `.env` 写 `VERSION=$version`，使用脚本已解析的小写变量，不能在 `set -u` 下直接引用可能未导出的 `$VERSION`。
- [ ] 打包 gate 必须拒绝非 HTTPS 或无 host 的 manifest URL，并拒绝无法 base64 解码为 32 字节 Ed25519 public key 的公钥。
- [ ] DMG stapled 后跑 `spctl -a -vv -t open`。
- [ ] 引入 `dmg_path="$dist/$app_name.dmg"`。
- [ ] 在 notarization/stapling/spctl 之后生成 `Super Dolphin.dmg.sha256`，避免 checksum 对应未 stapled 的旧 DMG。
- [ ] `scripts/verify_packaged_app_macos.sh` 支持 `UPDATE_DMG`，挂载后校验完整 app 结构，并用 trap 确保 detach。
- [ ] 增加 guard，禁止 `.app.zip` 出现在打包脚本。
- [ ] 新增 macOS gray release 文档。

**Verification:**

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackageMacOS|TestVerifyPackagedAppMacOS' -count=1
bash -n scripts/package_macos.sh
```

**Handoff output:**

- gray profile 环境变量
- DMG 输出路径
- 签名/公证 gate 行为

### Node `foundation_gate`

**Objective:** 汇总 DAG 1 的验证，确认底座可进入后端集成。

**Read scope:**

- DAG 1 所有 changed files

**Verification:**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/module/appupdate ./cmd/super-dolphin-updater -count=1
./scripts/test_with_guard.sh ./scripts -run 'TestPackageMacOS|TestVerifyPackagedAppMacOS' -count=1
rg -n "[s]mall zip payload|[n]ewer app zip|[s]taged-app|[U]PDATE_ZIP|[a]pp\\.zip|\\.[a]pp\\.zip|[u]pdate zip" scripts docs/superpowers/plans/打包
```

**Expected:** 测试通过；`.app.zip` 只能出现在“禁止出现”的测试断言或历史文档中，不能出现在新实现路径。

---

## DAG 2: `macos_gray_update_backend`

**Purpose:** 建立后端更新服务、RPC 和 manifest 发布命令。

**Suggested `task_create_dag` sketch:**

```yaml
dag_key: macos_gray_update_backend
title: macOS 灰度更新后端
schedule:
  trigger: manual
final_node_key: backend_gate
nodes:
  - node_key: appupdate_service_rpc
    title: App update service and RPC
    node_type: agent
    depends_on:
      - macos_gray_foundation.foundation_gate
  - node_key: release_manifest_cmd
    title: Release manifest command
    node_type: agent
    depends_on:
      - macos_gray_foundation.foundation_gate
  - node_key: backend_gate
    title: Backend update gate
    node_type: agent
    depends_on:
      - appupdate_service_rpc
      - release_manifest_cmd
```

### Node `appupdate_service_rpc`

**Objective:** 实现检查更新、下载 DMG、sha256 校验、记录 staged manifest、启动 helper，以及 JSON-RPC。

**Write scope:**

- `internal/module/appupdate/service.go`
- `internal/module/appupdate/service_test.go`
- `internal/module/appupdate/rpc.go`
- `internal/module/appupdate/rpc_test.go`
- `internal/module/appupdate/module.go`
- `internal/app/modules.go`
- `internal/ui/wails/lifecycle.go`
- `internal/ui/wails/lifecycle_test.go`
- `internal/ui/wails/module.go`

**Steps:**

- [ ] 用 `httptest.NewTLSServer` 写 `Check` / `Download` 测试，并让 service 使用 `server.Client()`。
- [ ] 写 `app/update/check`、`app/update/download`、`app/update/install`、`app/update/installLatest` handler 注册测试。
- [ ] 实现 `Config`、`Service`、`CheckResult`、`DownloadResult`、`InstallResult`。
- [ ] `ProvideConfig` 从包内 `.env`/环境变量派生 manifest URL、公钥、channel、stage dir、helper path、target app path、platform、current version；manifest URL 必须是 HTTPS，当前版本来自包内 `VERSION` 或 Info.plist；启用更新时缺失任何关键项必须报错。
- [ ] `Download` 只接受 manifest 中的 DMG URL、sha256 和 size。
- [ ] `Install` 先校验 `RequestQuit` 非空，再用不绑定 RPC ctx 的 detached helper 调用：`-dmg <archivePath> -target-app <target> -main-pid <pid> -restart=true`。
- [ ] 测试 nil `RequestQuit` 不会启动 helper，必须用 marker 文件断言 helper 未运行；测试 canceled RPC ctx 不会杀掉 helper。
- [ ] helper 启动成功后调用注入的 `RequestQuit`；新增 `WailsLifecycle.RequestQuitForUpdate` 和测试，避免 helper 等待主进程退出时卡死。
- [ ] `internal/app/modules.go` 挂载 `appupdate.Module`；`appupdate.RequestQuit` provider 只能放在 desktop-only `internal/ui/wails/module.go`，不能放入核心 app module。

**Verification:**

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate ./internal/app ./internal/ui/wails -count=1
./scripts/test_with_guard.sh internal/module/appupdate/service.go
./scripts/test_with_guard.sh internal/module/appupdate/rpc.go
./scripts/test_with_guard.sh internal/app/modules.go
./scripts/test_with_guard.sh internal/ui/wails/lifecycle.go
./scripts/test_with_guard.sh internal/ui/wails/module.go
```

**Handoff output:**

- RPC method names
- update service config env 需求
- 当前版本来源：包内 `.env` 的 `VERSION` 或 `.app/Contents/Info.plist`

### Node `release_manifest_cmd`

**Objective:** 新增命令生成 `latest.json`，对 DMG sha256/size 和 payload 做 Ed25519 签名。

**Write scope:**

- `cmd/super-dolphin-release-manifest/main.go`
- `cmd/super-dolphin-release-manifest/main_test.go`

**Steps:**

- [ ] 测试 `-artifact Super Dolphin.dmg` 写出 signed manifest。
- [ ] 实现 `run(args []string)`。
- [ ] 校验 Ed25519 private key 长度。
- [ ] 输出 `latest.json`。

**Verification:**

```bash
./scripts/test_with_guard.sh ./cmd/super-dolphin-release-manifest ./internal/module/appupdate -count=1
./scripts/test_with_guard.sh cmd/super-dolphin-release-manifest/main.go
```

**Handoff output:**

- 命令参数示例
- latest.json 示例字段

### Node `backend_gate`

**Objective:** 汇总后端更新能力。

**Verification:**

```bash
./scripts/test_with_guard.sh ./internal/module/appupdate ./cmd/super-dolphin-release-manifest ./internal/app ./internal/ui/wails -count=1
make guard
```

**Expected:** 后端更新模块与 app graph 都通过。

---

## DAG 3: `macos_gray_frontend`

**Purpose:** 在设置页提供灰度用户可见的一键更新入口。

**Suggested `task_create_dag` sketch:**

```yaml
dag_key: macos_gray_frontend
title: macOS 灰度更新前端入口
schedule:
  trigger: manual
final_node_key: frontend_gate
nodes:
  - node_key: backend_api_facade
    title: Frontend backendApi update facade
    node_type: agent
    depends_on:
      - macos_gray_update_backend.backend_gate
  - node_key: settings_update_ui
    title: Settings About update controls
    node_type: agent
    depends_on:
      - backend_api_facade
  - node_key: frontend_gate
    title: Frontend verification gate
    node_type: agent
    depends_on:
      - settings_update_ui
```

### Node `backend_api_facade`

**Objective:** React API facade 暴露 update RPC。

**Write scope:**

- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/backendApi.test.js`

**Steps:**

- [ ] 增加 `APP_UPDATE_CHECK`、`APP_UPDATE_DOWNLOAD`、`APP_UPDATE_INSTALL`、`APP_UPDATE_INSTALL_LATEST`。
- [ ] 增加 `checkAppUpdate`、`downloadAppUpdate`、`installAppUpdate`、`installLatestAppUpdate` facade。
- [ ] 测试 facade 调用 method 名称和 `{}` 参数。

**Verification:**

```bash
cd frontend-app
npm test -- backendApi.test.js
```

### Node `settings_update_ui`

**Objective:** 设置页 About 面板增加“检查更新/安装更新”按钮和状态提示。

**Write scope:**

- `frontend-app/src/pages/settings/SettingsPage.jsx`
- `frontend-app/src/pages/settings/SettingsPage.test.jsx`
- `frontend-app/src/SettingsPage.test.jsx`

**Steps:**

- [ ] 扩展 mocked backend：`checkAppUpdate`、`installLatestAppUpdate`。
- [ ] 同步扩展根级 `frontend-app/src/SettingsPage.test.jsx` 的 mocked backend，避免全量 `npm test` 因 missing named export 失败。
- [ ] 测试发现新版本后显示 `发现新版本 0.1.1`。
- [ ] 测试点击安装后调用 `installLatestAppUpdate`。
- [ ] 测试无更新、检查失败路径、stale update 清理、安装 deferred promise pending 时隐藏/禁用安装按钮防止重复启动 helper。
- [ ] 测试安装失败时显示错误、恢复可重试状态，不能永久 `updateInstalling=true`。
- [ ] 实现 `useSettingsRuntime` 的 update state 和 callbacks。

**Verification:**

```bash
cd frontend-app
npm test -- SettingsPage.test.jsx
```

### Node `frontend_gate`

**Objective:** 前端完整验证。

**Verification:**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

**Expected:** 全部通过，`frontend-app/dist/index.html` 存在。

---

## DAG 4: `macos_gray_release_smoke`

**Purpose:** 把 helper、DMG、manifest、smoke 和 clean VM 验收串起来。

**Suggested `task_create_dag` sketch:**

```yaml
dag_key: macos_gray_release_smoke
title: macOS 灰度打包发布验收
schedule:
  trigger: manual
final_node_key: clean_vm_acceptance
nodes:
  - node_key: bundle_helper_in_package
    title: Bundle updater helper into app package
    node_type: agent
    depends_on:
      - macos_gray_foundation.foundation_gate
  - node_key: smoke_scripts
    title: Packaged tools and update-loop smoke
    node_type: agent
    depends_on:
      - bundle_helper_in_package
      - macos_gray_update_backend.backend_gate
  - node_key: build_gray_dmg
    title: Build signed gray DMG
    node_type: agent
    depends_on:
      - bundle_helper_in_package
      - macos_gray_update_backend.backend_gate
      - macos_gray_frontend.frontend_gate
  - node_key: gray_manifest_publish
    title: Generate signed gray latest.json
    node_type: agent
    depends_on:
      - build_gray_dmg
      - macos_gray_update_backend.backend_gate
  - node_key: package_smoke_gate
    title: Local package smoke gate
    node_type: agent
    depends_on:
      - smoke_scripts
      - gray_manifest_publish
      - build_gray_dmg
      - macos_gray_frontend.frontend_gate
  - node_key: clean_vm_acceptance
    title: Clean VM gray acceptance
    node_type: agent
    depends_on:
      - package_smoke_gate
```

### Node `bundle_helper_in_package`

**Objective:** 打包脚本构建并复制 `super-dolphin-updater`，签名 Mach-O tree 时包含 helper。

**Write scope:**

- `scripts/package_macos.sh`
- `scripts/package_macos_guard_test.go`
- `scripts/verify_packaged_app_macos.sh`

**Steps:**

- [ ] `go build -o bin/super-dolphin-updater ./cmd/super-dolphin-updater`。
- [ ] 复制 helper 到 `Contents/Resources/bin/super-dolphin-updater`。
- [ ] verifier 检查 helper 可执行。
- [ ] guard 锁定构建、复制、签名顺序。

**Verification:**

```bash
./scripts/test_with_guard.sh ./scripts -run 'TestPackageMacOS|TestVerifyPackagedAppMacOS' -count=1
bash -n scripts/package_macos.sh
bash -n scripts/verify_packaged_app_macos.sh
```

### Node `smoke_scripts`

**Objective:** 增加 `packaged-tools` 和 `update-loop` smoke mode。

**Write scope:**

- `docs/scripts/macos_release_smoke.sh`
- `docs/运维发布/打包发布/macos-gray-release.md`

**Steps:**

- [ ] usage 加入 `packaged-tools` / `update-loop`。
- [ ] `packaged-tools` 检查 Codex、sidecar、LSP、Git、PostgreSQL、startup 子进程。
- [ ] `update-loop` 使用 `UPDATE_DMG` 调 helper 安装到临时 `.app`，然后对更新后的 app 跑完整 `scripts/verify_packaged_app_macos.sh` 和 `packaged-tools` 检查。
- [ ] 文档加入 smoke 命令。

**Verification:**

```bash
bash -n docs/scripts/macos_release_smoke.sh
make guard
```

### Node `build_gray_dmg`

**Objective:** 在所有代码、前端、helper、后端 gate 通过后构建实际灰度 DMG，供 manifest 和 smoke 消费。

**Write scope:**

- no source changes by default
- writes generated artifacts under `dist/package/macos/`

**Command:**

```bash
SUPER_DOLPHIN_RELEASE_PROFILE=gray ./scripts/package_macos.sh
```

**Verification:**

```bash
test -s "dist/package/macos/Super Dolphin.dmg"
test -s "dist/package/macos/Super Dolphin.dmg.sha256"
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

**Handoff output:**

- DMG path
- sha256 file path
- signing/notarization/stapler/spctl status

### Node `gray_manifest_publish`

**Objective:** 用实际 DMG 生成 signed `latest.json`。

**Write scope:**

- no source changes by default
- writes generated local artifact under `dist/package/macos/latest.json`

**Command:**

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

**Verification:**

```bash
test -s "dist/package/macos/latest.json"
test "$(/usr/bin/plutil -extract payload.version raw -o - 'dist/package/macos/latest.json')" = "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' 'dist/package/macos/Super Dolphin.app/Contents/Info.plist')"
```

### Node `package_smoke_gate`

**Objective:** 本机灰度包验证。

**Verification:**

```bash
docs/scripts/macos_release_smoke.sh local
docs/scripts/macos_release_smoke.sh notarized-dmg
docs/scripts/macos_release_smoke.sh startup
docs/scripts/macos_release_smoke.sh packaged-tools
test -s "dist/package/macos/latest.json"
test "$(/usr/bin/plutil -extract payload.version raw -o - 'dist/package/macos/latest.json')" = "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' 'dist/package/macos/Super Dolphin.app/Contents/Info.plist')"
UPDATE_DMG="dist/package/macos/Super Dolphin.dmg" docs/scripts/macos_release_smoke.sh update-loop
```

**Expected artifacts:**

- `dist/package/macos/Super Dolphin.app`
- `dist/package/macos/Super Dolphin.dmg`
- `dist/package/macos/Super Dolphin.dmg.sha256`
- `dist/package/macos/latest.json`

### Node `clean_vm_acceptance`

**Objective:** 灰度前最后验收，证明用户只用 DMG 即可安装和更新。

**Manual verification checklist:**

- [ ] 干净 macOS VM 无 `~/.codex`、无外部 `codex`、无外部 PostgreSQL。
- [ ] 打开 `Super Dolphin.dmg` 并安装到 `/Applications`。
- [ ] 启动 `Super Dolphin.app`。
- [ ] 创建一个 Codex conversation。
- [ ] 发送一个 prompt 并收到回复。
- [ ] 进程列表包含 `agent-terminal`、`mcp-orch`、`mcp-lsp`、`codex`、embedded `postgres`。
- [ ] 发布新版 `latest.json` 和新版 `Super Dolphin.dmg` 到灰度 endpoint。
- [ ] 设置页点击“检查更新”看到新版本。
- [ ] 点击“安装更新”后 App 重启到新版本。
- [ ] 原本本地线程和数据仍存在。

**Final output:** 一份验收记录，包含 DMG 路径、latest.json URL、测试机器 macOS 版本、版本号、通过/失败项。

---

## 推荐并行调度

第一波可并行：

- `macos_gray_foundation.peer_failfast`
- `macos_gray_foundation.update_manifest_verifier`
- `macos_gray_foundation.updater_helper`
- `macos_gray_foundation.release_profile_dmg`

第二波：

- `macos_gray_update_backend.appupdate_service_rpc`
- `macos_gray_update_backend.release_manifest_cmd`

第三波：

- `macos_gray_frontend.backend_api_facade`
- `macos_gray_release_smoke.bundle_helper_in_package`

第四波：

- `macos_gray_frontend.settings_update_ui`
- `macos_gray_release_smoke.smoke_scripts`

最后串行：

- `macos_gray_frontend.frontend_gate`
- `macos_gray_release_smoke.build_gray_dmg`
- `macos_gray_release_smoke.gray_manifest_publish`
- `macos_gray_release_smoke.package_smoke_gate`
- `macos_gray_release_smoke.clean_vm_acceptance`

## 集成规则

- 每个节点完成后必须返回：changed files、验证命令、验证结果、剩余风险。
- gate 节点必须重新检查所有上游 diff，不能只信上游报告。
- 如果两个节点都要改 `scripts/package_macos.sh` 或 `scripts/package_macos_guard_test.go`，后执行节点必须先读当前文件并基于已有改动继续，不得回滚。
- 任何 guard 失败都阻断下游 DAG。
- 不允许用 `--no-verify`、不允许 weaken guard、不允许把 `.app.zip` 重新引入实现路径。
