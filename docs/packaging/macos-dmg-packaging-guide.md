# macOS DMG 打包指南

本文档描述当前仓库的 macOS DMG 打包流程，覆盖：首次打包、同一台机器重复打包、换打包机、复用依赖缓存、正式 release 打包，以及常见错误排查。

> 安全提醒：`SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN` 会被写入打包产物 `Contents/Resources/.env`，只能使用可公开分发的 bootstrap credential，不能使用 privileged API key。不要把真实 token 发到聊天、截图或提交到 git。若已泄露，请立即轮换。

## 1. 打包目标和产物结构

本仓库的 macOS 打包目标是把运行所需依赖复制为 `.app` 内的可重定位副本，再用 `hdiutil` 生成 `.dmg`。

标准版输出：

```text
dist/package/macos/Super Dolphin.app
dist/package/macos/Super Dolphin.dmg
```

Full LSP 版输出：

```text
dist/package/macos/Super Dolphin Full LSP.app
dist/package/macos/Super Dolphin Full LSP.dmg
```

`.app` 内关键资源：

```text
Contents/MacOS/agent-terminal
Contents/Resources/.env
Contents/Resources/bin/codex
Contents/Resources/bin/git
Contents/Resources/bin/mcp-orch
Contents/Resources/bin/mcp-lsp
Contents/Resources/bin/mcp-ida
Contents/Resources/postgres/<goos-goarch>/
Contents/Resources/lsp/
Contents/Resources/runtime-manifest.json
Contents/Resources/codex-manifest.json
Contents/Resources/lsp/lsp-manifest.json
```

当前打包按本机架构生成，不生成 universal DMG：

- Apple Silicon: `darwin-arm64`
- Intel Mac: `darwin-amd64`

## 2. 两个打包入口

### 2.1 本地测试打包入口

用于开发者本机 smoke 测试：

```bash
./scripts/package_macos_local.sh standard
./scripts/package_macos_local.sh full
./scripts/package_macos_local.sh all
```

特点：

- 会调用 `scripts/prepare_lsp_bundle_macos.sh` 自动准备 LSP bundle。
- 默认从 `command -v codex` 找本机 Codex artifact。
- 默认使用 `.build-cache/postgres/16.14/<goos-goarch>` 作为 PostgreSQL runtime。
- 会把当前提供的 relay bootstrap token 写入 `.app/Contents/Resources/.env`。
- 产物用于本地验证，不应直接对外分发。

### 2.2 正式 release 打包入口

用于发布：

```bash
./scripts/package_macos.sh
```

特点：

- 要求显式提供 PostgreSQL runtime、LSP bundle、Codex artifact、Codex sha256、Codex version、relay bootstrap 信息。
- 不允许 `SUPER_DOLPHIN_CODEX_RELAY_API_KEY`。
- 可接入 Developer ID codesign 和 notarization。

## 3. 四类内置依赖如何处理

### 3.1 Embedded PostgreSQL

输入：

```bash
SUPER_DOLPHIN_POSTGRES_DIST=/absolute/path/to/postgres-runtime
```

该目录必须包含：

```text
bin/postgres
bin/initdb
bin/pg_ctl
bin/pg_config
share/postgres.bki          # 或 share/postgresql*/postgres.bki
lib/                        # 如 runtime 依赖动态库
```

本地首次可用脚本构建：

```bash
./scripts/build_relocatable_postgres_macos.sh
```

默认生成：

```text
.build-cache/postgres/16.14/<goos-goarch>
```

打包时复制到：

```text
Contents/Resources/postgres/<goos-goarch>/
```

脚本会校验布局，并处理 Mach-O dylib / rpath，避免运行时依赖 Homebrew 原路径。

### 3.2 Git

Git 由 `scripts/package_macos.sh` 自动解析，优先级：

1. `SUPER_DOLPHIN_GIT_BIN`
2. `/Library/Developer/CommandLineTools/usr/bin/git`
3. `/Applications/Xcode.app/Contents/Developer/usr/bin/git`
4. `brew --prefix git` 下的 Git
5. `command -v git`，但不能是 `/usr/bin/git` shim

打包时复制：

```text
Contents/Resources/bin/git
Contents/Resources/libexec/git-core/
Contents/Resources/share/git-core/
```

并复制必要 helper、修 dylib / rpath。若机器没有可复制的 portable Git，脚本会 fail-fast。

### 3.3 Codex

本地脚本默认：

```bash
SUPER_DOLPHIN_CODEX_ARTIFACT="$(command -v codex)"
```

如果 `codex` 是 npm launcher，脚本会解析到 native binary，例如：

```text
node_modules/@openai/codex-darwin-arm64/vendor/.../codex
```

正式 release 必须显式设置：

```bash
SUPER_DOLPHIN_CODEX_ARTIFACT=/absolute/path/to/codex
SUPER_DOLPHIN_CODEX_SHA256=<trusted-sha256>
SUPER_DOLPHIN_CODEX_VERSION=<version>
```

打包时复制到：

```text
Contents/Resources/bin/codex
Contents/Resources/codex-manifest.json
```

并验证：

```bash
Contents/Resources/bin/codex app-server --help
```

### 3.4 LSP bundle

LSP 是两阶段：先准备 bundle，再复制进 app。

准备脚本：

```bash
./scripts/prepare_lsp_bundle_macos.sh
```

默认输出：

```text
.build-cache/lsp/standard/<goos-goarch>
.build-cache/lsp/full/<goos-goarch>
```

标准版包含：

```text
node/bin/node
node_modules/typescript-language-server
node_modules/vscode-langservers-extracted
node_modules/pyright
node_modules/@ast-grep/cli
bin/gopls
bin/typescript-language-server
bin/vscode-css-language-server
bin/pyright-langserver
bin/rust-analyzer
bin/sg
bin/go
```

Full 版额外包含：

```text
jdk/
jdtls/
bin/java
bin/jdtls
```

打包时复制到：

```text
Contents/Resources/lsp/
Contents/Resources/bin/gopls -> ../lsp/bin/gopls
Contents/Resources/bin/typescript-language-server -> ../lsp/bin/typescript-language-server
...
```

并写入：

```text
Contents/Resources/lsp/lsp-manifest.json
Contents/Resources/runtime-manifest.json
```

## 4. 首次本机打包：标准版 DMG

### 4.1 进入仓库

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg
```

确认平台：

```bash
go env GOOS GOARCH
```

应为：

```text
darwin
arm64   # 或 amd64
```

### 4.2 安装基础工具

```bash
xcode-select --install
```

确认可用：

```bash
git --version
go version
node --version
npm --version
command -v codex
codex --version
```

如缺少 LSP 相关工具：

```bash
go install golang.org/x/tools/gopls@latest
brew install rust-analyzer
```

### 4.3 构建 PostgreSQL runtime

```bash
./scripts/build_relocatable_postgres_macos.sh
```

设置真实路径，不能使用 `/absolute/path/to/postgres-runtime` 这种占位符：

```bash
export SUPER_DOLPHIN_POSTGRES_DIST="$PWD/.build-cache/postgres/16.14/$(go env GOOS)-$(go env GOARCH)"
```

检查：

```bash
test -x "$SUPER_DOLPHIN_POSTGRES_DIST/bin/postgres" && echo "postgres ok"
test -x "$SUPER_DOLPHIN_POSTGRES_DIST/bin/initdb" && echo "initdb ok"
```

### 4.4 设置本机依赖路径

不同机器默认路径可能不同，建议首次打包都显式设置：

```bash
export SUPER_DOLPHIN_NODE_DIST="$(node -p 'require("path").dirname(require("path").dirname(process.execPath))')"
export SUPER_DOLPHIN_NPM_BIN="$(command -v npm)"
export SUPER_DOLPHIN_GO_TOOLCHAIN_DIR="$(go env GOROOT)"
export SUPER_DOLPHIN_GOPLS_BIN="$(command -v gopls)"
export SUPER_DOLPHIN_RUST_ANALYZER_BIN="$(command -v rust-analyzer)"
export SUPER_DOLPHIN_CODEX_ARTIFACT="$(command -v codex)"
```

### 4.5 设置 relay bootstrap

```bash
unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://your-relay.example"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<bootstrap-token>"
```

不要把真实 token 写进文档、提交或截图。

### 4.6 打包

```bash
./scripts/package_macos_local.sh standard
```

输出：

```text
dist/package/macos/Super Dolphin.app
dist/package/macos/Super Dolphin.dmg
```

### 4.7 验证

```bash
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

可选挂载 DMG 检查：

```bash
hdiutil attach "dist/package/macos/Super Dolphin.dmg"
```

## 5. 首次本机打包：Full LSP 版

Full LSP 版需要 Java runtime 和 jdtls：

```bash
brew install jdtls openjdk
export SUPER_DOLPHIN_JDTLS_HOME="$(brew --prefix jdtls)/libexec"
export SUPER_DOLPHIN_JDK_HOME="$(brew --prefix openjdk)/libexec/openjdk.jdk/Contents/Home"
```

然后执行：

```bash
./scripts/package_macos_local.sh full
```

输出：

```text
dist/package/macos/Super Dolphin Full LSP.app
dist/package/macos/Super Dolphin Full LSP.dmg
```

## 6. 同一台机器重复打包

如果 `.build-cache/postgres/16.14/<goos-goarch>` 已存在，可省略 PostgreSQL 构建：

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg

unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://your-relay.example"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<bootstrap-token>"
export SUPER_DOLPHIN_CODEX_ARTIFACT="$(command -v codex)"

./scripts/package_macos_local.sh standard
```

如需要重新构建 PostgreSQL：

```bash
./scripts/build_relocatable_postgres_macos.sh
```

如需要重新生成 LSP bundle：

```bash
rm -rf .build-cache/lsp/standard/$(go env GOOS)-$(go env GOARCH)
./scripts/package_macos_local.sh standard
```

## 7. 换一台 macOS 打包机：从零开始

### 7.1 准备代码

```bash
git clone <repo-url> Super-Dolphin
cd Super-Dolphin
git checkout codex/package-embedded-pg
```

或切到对应 worktree。

### 7.2 安装工具

```bash
xcode-select --install
brew install go node rust-analyzer
go install golang.org/x/tools/gopls@latest
```

安装并确认 Codex：

```bash
command -v codex
codex --version
```

Full LSP 需要：

```bash
brew install jdtls openjdk
```

### 7.3 显式设置机器相关路径

```bash
export SUPER_DOLPHIN_NODE_DIST="$(node -p 'require("path").dirname(require("path").dirname(process.execPath))')"
export SUPER_DOLPHIN_NPM_BIN="$(command -v npm)"
export SUPER_DOLPHIN_GO_TOOLCHAIN_DIR="$(go env GOROOT)"
export SUPER_DOLPHIN_GOPLS_BIN="$(command -v gopls)"
export SUPER_DOLPHIN_RUST_ANALYZER_BIN="$(command -v rust-analyzer)"
export SUPER_DOLPHIN_CODEX_ARTIFACT="$(command -v codex)"
```

Full LSP：

```bash
export SUPER_DOLPHIN_JDTLS_HOME="$(brew --prefix jdtls)/libexec"
export SUPER_DOLPHIN_JDK_HOME="$(brew --prefix openjdk)/libexec/openjdk.jdk/Contents/Home"
```

### 7.4 构建 PostgreSQL runtime

```bash
./scripts/build_relocatable_postgres_macos.sh
export SUPER_DOLPHIN_POSTGRES_DIST="$PWD/.build-cache/postgres/16.14/$(go env GOOS)-$(go env GOARCH)"
```

### 7.5 设置 relay 并打包

```bash
unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://your-relay.example"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<bootstrap-token>"

./scripts/package_macos_local.sh standard
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

## 8. 换机器但复用已准备好的依赖缓存

可以把以下目录从同架构打包机复制到新机器：

```text
.build-cache/postgres/16.14/darwin-arm64
.build-cache/lsp/standard/darwin-arm64
.build-cache/lsp/full/darwin-arm64
```

然后设置：

```bash
export SUPER_DOLPHIN_POSTGRES_DIST="/path/to/postgres/16.14/darwin-arm64"
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="/path/to/lsp/standard/darwin-arm64"
export SUPER_DOLPHIN_CODEX_ARTIFACT="/path/to/codex"
```

注意：缓存必须同 OS/架构。`darwin-arm64` 不能用于 `darwin-amd64`。

## 9. 正式 release 打包

正式 release 使用 `scripts/package_macos.sh`，不要使用 local helper。

### 9.1 准备 LSP bundle

标准版：

```bash
export SUPER_DOLPHIN_LSP_PROFILE=standard
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$PWD/.build-cache/lsp/standard/$(go env GOOS)-$(go env GOARCH)"
./scripts/prepare_lsp_bundle_macos.sh
```

Full 版：

```bash
export SUPER_DOLPHIN_LSP_PROFILE=full
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="$PWD/.build-cache/lsp/full/$(go env GOOS)-$(go env GOARCH)"
./scripts/prepare_lsp_bundle_macos.sh
```

### 9.2 计算 Codex checksum

```bash
export SUPER_DOLPHIN_CODEX_ARTIFACT="/absolute/path/to/codex"
export SUPER_DOLPHIN_CODEX_SHA256="$(shasum -a 256 "$SUPER_DOLPHIN_CODEX_ARTIFACT" | awk '{print $1}')"
export SUPER_DOLPHIN_CODEX_VERSION="$($SUPER_DOLPHIN_CODEX_ARTIFACT --version | head -n1)"
```

正式发布时，`SUPER_DOLPHIN_CODEX_SHA256` 应来自可信 release manifest 或签名验证流程，而不是未验证下载源。

### 9.3 设置 release 环境

```bash
export SUPER_DOLPHIN_POSTGRES_DIST="/absolute/path/to/postgres-runtime"
export SUPER_DOLPHIN_LSP_BUNDLE_DIR="/absolute/path/to/lsp-bundle"

export SUPER_DOLPHIN_CODEX_ARTIFACT="/absolute/path/to/codex"
export SUPER_DOLPHIN_CODEX_SHA256="<trusted-sha256>"
export SUPER_DOLPHIN_CODEX_VERSION="<version>"

unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://your-production-relay.example"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<public-bootstrap-token>"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF="<proof-or-config-id>"
```

可选签名：

```bash
export CODESIGN_IDENTITY="Developer ID Application: Your Team"
```

可选 notarization：

```bash
export NOTARY_PROFILE="notarytool-profile-name"
```

### 9.4 执行 release 打包

```bash
./scripts/package_macos.sh
```

验证：

```bash
scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

## 10. 一键本地首次标准版示例

复制前先替换 relay URL 和 token：

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg

unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY

./scripts/build_relocatable_postgres_macos.sh

export SUPER_DOLPHIN_POSTGRES_DIST="$PWD/.build-cache/postgres/16.14/$(go env GOOS)-$(go env GOARCH)"
export SUPER_DOLPHIN_NODE_DIST="$(node -p 'require("path").dirname(require("path").dirname(process.execPath))')"
export SUPER_DOLPHIN_NPM_BIN="$(command -v npm)"
export SUPER_DOLPHIN_GO_TOOLCHAIN_DIR="$(go env GOROOT)"
export SUPER_DOLPHIN_GOPLS_BIN="$(command -v gopls)"
export SUPER_DOLPHIN_RUST_ANALYZER_BIN="$(command -v rust-analyzer)"
export SUPER_DOLPHIN_CODEX_ARTIFACT="$(command -v codex)"

export SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="https://your-relay.example"
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<bootstrap-token>"

./scripts/package_macos_local.sh standard

scripts/verify_packaged_app_macos.sh "dist/package/macos/Super Dolphin.app"
```

## 11. 常见错误

### 11.1 `missing PostgreSQL dist`

原因：`SUPER_DOLPHIN_POSTGRES_DIST` 指向不存在目录，常见于把示例占位符原样复制：

```bash
export SUPER_DOLPHIN_POSTGRES_DIST="/absolute/path/to/postgres-runtime"
```

修复：

```bash
./scripts/build_relocatable_postgres_macos.sh
export SUPER_DOLPHIN_POSTGRES_DIST="$PWD/.build-cache/postgres/16.14/$(go env GOOS)-$(go env GOARCH)"
```

### 11.2 `missing gopls`

```bash
go install golang.org/x/tools/gopls@latest
export SUPER_DOLPHIN_GOPLS_BIN="$(command -v gopls)"
```

### 11.3 `missing rust-analyzer`

```bash
brew install rust-analyzer
export SUPER_DOLPHIN_RUST_ANALYZER_BIN="$(command -v rust-analyzer)"
```

### 11.4 `missing node`

```bash
brew install node
export SUPER_DOLPHIN_NODE_DIST="$(node -p 'require("path").dirname(require("path").dirname(process.execPath))')"
```

### 11.5 `SUPER_DOLPHIN_CODEX_RELAY_API_KEY must not be set`

打包禁止使用 privileged API key：

```bash
unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
```

改用：

```bash
export SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="<bootstrap-token>"
```

### 11.6 Codex artifact 不可执行或不是 native binary

检查：

```bash
command -v codex
codex --version
file "$(command -v codex)"
```

本地 helper 可处理 npm launcher；release 脚本要求 artifact、sha256 和 version 明确可信。

## 12. Clean VM 验收

发布前应参考：

```text
docs/packaging/macos-clean-vm-checklist.md
```

核心验收：

1. 干净 macOS VM，无 `DATABASE_URL`、无本机 PostgreSQL、无本机 Codex、无旧 Super Dolphin 数据。
2. 安装 DMG 里的 `.app`。
3. 首次启动能初始化 embedded PostgreSQL。
4. 能创建 Codex 对话并收到回复。
5. 退出重开后历史或新会话仍可用。
