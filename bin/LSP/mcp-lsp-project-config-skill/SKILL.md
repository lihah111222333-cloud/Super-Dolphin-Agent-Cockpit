---
name: mcp-lsp-project-config
description: 配置、安装、检查、修复或迁移项目级 mcp-lsp stdio MCP，覆盖 Codex、Claude Code 和 Google Antigravity，以及 Windows 原生 PowerShell/WSL 自动选型。用户提到项目 MCP、LSP MCP、.codex/config.toml、.mcp.json、.agents/mcp_config.json、工具已显示但调用报 client is closing、EOF、Transport closed，或希望三个客户端共用仓库内 mcp-lsp 时使用。
---

# 项目级 mcp-lsp 配置

根据目标项目、当前主机和已安装客户端的真实状态，选择 bin/LSP 中正确的 mcp-lsp，并精确合并项目级配置。不要写用户级或全局配置。

本技能提供判断方法和配置契约，不提供固定生成脚本。Agent 必须自行检查和编辑；不得为了单次配置默认创建新的生成器、wrapper 或安装脚本，除非用户明确要求。

## 读取顺序

1. 完整读取本技能。
2. 读取 references/provider-configs.md，掌握三家项目级文件、字段差异和刷新方式。
3. 确认项目根、Git 状态、已有配置、将启动 MCP 的客户端运行环境、当前操作系统和 CPU；Windows 必须区分原生进程与 WSL 进程。
4. 解析项目根及其上一级目录，按“确认可信 cwd 与 workspace root”向用户提问并等待明确选择；未确认不得写配置。
5. 检查目标客户端当前版本、CLI help 或官方文档。已有参考与当前客户端冲突时，以当前官方格式和本机可验证行为为准，并同步修订参考。
6. 读取所有将被修改的配置，建立现有 server 和设置清单后再编辑。

## 同步客户端规范文件

配置 MCP 之前，必须让对应客户端加载 LSP 工作规范：

1. 完整读取 bin/LSP/AGENTS.md。
2. 配置 Codex 或 Antigravity 时，目标是项目根 AGENTS.md。
3. 配置 Claude Code 时，目标是项目根 CLAUDE.md。
4. 配置三家时，同时同步 AGENTS.md 和 CLAUDE.md；不要假设一个客户端会读取另一个客户端的规范文件。
5. 样板中的受管范围由 mcp-lsp-project-rules:start 和 mcp-lsp-project-rules:end 标记。目标已有同一标记时更新该范围，不得再次追加。
6. 目标没有标记但已经有等价规则时，做语义对照；只补缺失项，可为本次纳入的规则添加一组标记，禁止制造重复章节。
7. AGENTS.md 不存在时可以使用完整样板创建；CLAUDE.md 不存在时创建 CLAUDE.md，并只追加样板受管标记之间的规则，不要把 AGENTS.md 标题复制成 CLAUDE.md 标题。
8. 已有目标文件时保留原内容和更强规则，只追加缺失的 LSP 规范，不得整份覆盖。

同步后必须确认本次客户端对应的规范文件至少包含：

- file、inspect、xref、grep、structure、patch_edit、completion 七个短工具名。
- 定位、定义理解、引用或调用层级、精读、diagnostics 的证据链。
- file(action=diagnostics) 是诊断入口，patch_edit 是编辑入口。
- LSP 失败时先收窄重试，仍失败则记录 blocker，不得静默降级。
- diagnostics 的 Error、Warning、Information、Hint 都要处理。
- 修改后运行匹配的构建与测试，异常和缺配置 fail-fast。

对应规范文件同步未完成或无法确认作用域时，不得继续声称 MCP 已按规范配置。MCP 配置让客户端发现工具，AGENTS.md 或 CLAUDE.md 让对应 Agent 按项目规则使用工具，两者缺一不可。

## 选择并验证二进制

| 当前主机 | 使用文件 | 预期 Go 目标 |
|---|---|---|
| macOS Intel | bin/LSP/mcp-lsp-mac-x86 | darwin/amd64 |
| macOS Apple Silicon | bin/LSP/mcp-lsp-mac-arm | darwin/arm64 |
| Linux/WSL x86-64 | bin/LSP/mcp-lsp-linux-x86 | linux/amd64 |
| Linux/WSL ARM64 | bin/LSP/mcp-lsp-linux-arm | linux/arm64 |
| Windows x86-64 | bin/LSP/mcp-lsp-windows-x86.exe | windows/amd64 |
| Windows ARM64 | bin/LSP/mcp-lsp-windows-arm.exe | windows/arm64 |

不能只按文件名判断。优先用 go version -m 读取 Go 构建元数据并确认 GOOS/GOARCH；macOS/Linux 可用 file 交叉验证 Mach-O/ELF，Windows 可检查 PE Machine。若无法验证实际目标，记录 blocker，不要写配置。

同时确认文件存在、非空；macOS/Linux 还要确认可执行权限。不得把 Intel、ARM 或其他操作系统的文件配置给当前主机。

### Windows 原生与 WSL 自动选型

不要求用户填写额外平台开关。Agent 必须根据“最终启动 MCP server 的客户端进程”自动选择：

1. 原生 Windows/Codex Desktop/PowerShell 客户端使用 `windows/*` 二进制；配置路径使用 Windows drive 或 UNC。JSON/TOML 中优先写 `G:/develop/project` 形式，避免手工反斜杠转义。
2. WSL 内运行的 Codex、Claude Code 或 Antigravity CLI 使用 `linux/*` 二进制；路径使用 `/mnt/g/develop/project` 等 Linux 绝对路径。
3. 当前编辑 shell 不一定等于目标客户端环境。若 Agent 在 WSL 中编辑 Windows Desktop 客户端的配置，必须按 Windows 客户端选 `.exe`；反之同理。
4. WSL 可以透传执行 Windows `.exe`，但本技能禁止把 Linux `/mnt/...` roots 配给 Windows 二进制。无法确认最终 owner 时先报告 blocker。
5. 用 `go version -m` 复核实际 GOOS/GOARCH；`mcp-lsp` 会按该构建目标自动采用对应路径和可执行文件语义，不做 Windows 与 WSL 路径猜测转换。

当前 shell 就是目标客户端环境时，先记录自动探测证据：

~~~powershell
$isNativeWindows = ($PSVersionTable.PSEdition -eq "Desktop") -or ($IsWindows -eq $true)
$isWSL = ($IsLinux -eq $true) -and (($null -ne $env:WSL_INTEROP) -or ($null -ne $env:WSL_DISTRO_NAME))
$env:PROCESSOR_ARCHITECTURE
~~~

~~~bash
is_wsl=0
if [ -n "${WSL_INTEROP:-}${WSL_DISTRO_NAME:-}" ] || grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null; then is_wsl=1; fi
printf 'is_wsl=%s arch=%s\n' "$is_wsl" "$(uname -m)"
~~~

`$isNativeWindows=true` 选择 Windows `.exe`；`$isWSL=true` 或 `is_wsl=1` 选择 Linux/WSL 文件。若当前 shell 只是在编辑另一个环境中运行的 Desktop 客户端配置，仍以目标客户端进程为准，不能用编辑 shell 的结果覆盖它。

## 确认可信 cwd 与 workspace root

写入任何客户端配置前，必须先解析并向用户展示两个真实绝对路径，然后询问本次 LSP 的可信 cwd 选择：

1. **本仓库**：`<repo-root>`。只允许 LSP 工具访问本仓库及其子目录，适合单仓库开发，也是最小权限选项。
2. **仓库上一级**：`<parent-of-repo-root>`。允许 LSP 工具访问该上级目录及其全部子目录，适合顶层目录下多个相关 Git 微服务需要互相导航的场景。

必须等待用户明确选择“本仓库”或“仓库上一级”；不得根据当前 shell cwd、配置文件位置、聊天截图或多仓库布局自行扩大范围。用户未确认、回答含糊、项目根无法唯一解析或项目根已经是文件系统根时，立即阻断并说明原因。除非用户另行明确指定并授权其他绝对路径，否则只提供上述两个候选。

将用户选中的绝对路径记为 `<trusted-cwd>`，并保持以下绑定：

- 支持 `cwd` 字段的客户端 server 配置必须写 `cwd = <trusted-cwd>`；不支持该字段的客户端不得发明字段。
- `GO_AGENT_LSP_ROOT=<trusted-cwd>`。
- `GO_AGENT_LSP_ROOTS` 必须由 JSON encoder 生成，当前选择只写一个元素：`[<trusted-cwd>]`。
- 项目级配置文件以及 AGENTS.md/CLAUDE.md 仍写在原项目根；选择仓库上一级不等于把配置迁移到上一级。
- `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` 仍指向已验证的分发包资源根或 source checkout 根，不跟随 `<trusted-cwd>` 扩大。

这里控制的是 mcp-lsp 的 workspace containment。可信目录之外的路径会返回 `path_outside_workspace`；扩大 LSP workspace 不等于自动扩大客户端自身的文件系统沙箱或写权限。

## 必需 sidecar 启动契约

每一个本地 stdio server 的 `env` 必须显式包含：

~~~text
SUPER_DOLPHIN_RUNTIME_MODE=dev
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=<分发包资源根；源码构建则为 checkout 根，必须是绝对路径>
SUPER_DOLPHIN_DEPENDENCY_PROFILE=production
GO_AGENT_LSP_ROOT=<用户确认的 trusted-cwd 绝对路径>
GO_AGENT_LSP_ROOTS=<由 JSON encoder 生成、仅含 trusted-cwd 的绝对路径数组>
~~~

前三项是 sidecar 启动所需契约，后两项绑定可信 LSP workspace。独立 `mcp-lsp` 使用 `production`；`desktop_host` 只用于桌面 owner 的开发启动器，不能复制到三家独立 MCP 配置。不得依赖 shell 中偶然继承的值，不得在二进制中添加隐式默认值。

原生 Windows 使用共享 gopls 时，当前 server 的 `env` 还必须显式包含：

~~~text
SUPER_DOLPHIN_LSP_BUNDLE_DIR=<bin/LSP/lsp 的绝对路径>
SUPER_DOLPHIN_LSP_MANIFEST=<bin/LSP/lsp/lsp-manifest.json 的绝对路径>
~~~

这两个字段只证明包内 gopls 的路径与摘要，不改变可信 workspace；不得把它们与 `GO_AGENT_LSP_ROOT(S)` 混用。bundle、manifest 或原生 `gopls.exe` 缺失时 fail-fast，不得退回 PATH gopls。

`SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` 在本 dev 分发布局中是随 `bin/LSP` 交付的资源根；从源码构建时是 source checkout 根。它不是目标源码文件目录，也不是凭二进制位置临时猜出的值。若用户采用其他资源布局，必须先核对真实资源根，再显式配置。

Windows 的 `GO_AGENT_LSP_ROOTS` 必须由 JSON encoder 生成；不要手工拼接 `C:\...`。原生 Windows 和 WSL 的 command、cwd、runtime resources 与两个 root 字段必须全部属于同一种路径体系。

## 根据客户端配置

只处理用户要求的客户端；用户要求 all 或三家共用时才处理全部。

### Codex

- 项目文件：.codex/config.toml。
- 只在项目受信任时生效。
- stdio server 使用 mcp_servers.<name> 表、command、args、cwd 和 env。
- env 必须包含三个 sidecar 启动字段与两个 `GO_AGENT_LSP_ROOT(S)` workspace 字段；原生 Windows 还必须包含两个受信 LSP bundle 字段。
- 需要强制 LSP 时可设置 enabled = true 和 required = true。
- 保留其他 MCP server、features、hooks、model 和逐工具审批设置。

### Claude Code

- 项目文件：项目根 .mcp.json。
- 使用 mcpServers 对象中的项目 server；这是 project scope。
- 本地 stdio server 使用 command、args 和 env。
- env 必须包含三个 sidecar 启动字段与两个 `GO_AGENT_LSP_ROOT(S)` workspace 字段；原生 Windows 还必须包含两个受信 LSP bundle 字段。
- 首次加载项目 server 时需要 workspace trust 和用户批准。
- 不得误写默认 local scope 或用户级 ~/.claude.json。

### Google Antigravity

- 项目文件：.agents/mcp_config.json。
- 使用 mcpServers 对象中的 workspace-local server。
- 本地 stdio server 使用 command、args、cwd 和 env。
- env 必须包含三个 sidecar 启动字段与两个 `GO_AGENT_LSP_ROOT(S)` workspace 字段；原生 Windows 还必须包含两个受信 LSP bundle 字段。
- 不得写全局 ~/.gemini/config/mcp_config.json。
- 远程 MCP 才使用 serverUrl；本地 mcp-lsp 不使用 url、httpUrl 或 serverUrl。

#### Antigravity 同名配置冲突排查

工具已经显示但首次或后续 `tools/call` 报 `client is closing: EOF`、`Transport closed` 或 sidecar 很快退出时，不要只检查 `.agents/mcp_config.json`，也不要先归因于 Antigravity。先在当前 workspace 内完整盘点：

1. 规范入口 `<workspace>/.agents/mcp_config.json`。
2. 自动加载的 `<workspace>/.agents/plugins/*/mcp_config.json`。
3. 旧安装或误写的 `<workspace>/.gemini/config/mcp_config.json`。
4. 全局 `~/.gemini/config/mcp_config.json` 中是否还有同名 server；只读检查，未经用户明确授权不得修改全局配置。

同名 `lsp` 出现在规范入口和插件或旧副本中不是冗余备份：不同 `cwd`、roots、command 或 env 可能让客户端发现工具后启动另一份 server，造成调用时路径解析、进程 owner 和 stdio 生命周期不一致。以用户确认的正确配置为基准逐字段比较；确认插件只用于重复注册同一 server、没有其他能力后，获得删除授权再删除整个插件目录。不要保留空 `plugin.json`、wrapper 或 stale 配置。

必须从 `~/.multi-agent/log/mcp-lsp/mcp-lsp-*.log` 核对实际生效值和时序：

- `tools/call begin/done` 之后才出现 `mcp stdio: read failed EOF`，说明二进制完成过调用，EOF 是 stdin 被关闭后的结果，不是二进制崩溃证据。
- `resolve parent path: lstat <trusted-cwd>/...` 说明请求相对路径按实际 trusted root 解析；用日志中的 root 和请求 path 重建最终路径。
- `sidecar requires SUPER_DOLPHIN_RUNTIME_MODE...` 只证明该次进程缺少启动 env。若它来自 Agent 用 shell 裸跑二进制的探测，不得据此断言正式 MCP server 配置漏传 env。
- `context canceled` 可能是上游 cascade 取消后的收敛结果；必须向前找到第一个工具错误或 stdin EOF，不能把最后一行当首因。

选择“仓库上一级”为 `<trusted-cwd>` 时，相对工具路径也以该上一级为基准。读取 `<trusted-cwd>/repo-a/cmd/main.go` 必须传 `repo-a/cmd/main.go`，不能传 `cmd/main.go`；否则会错误解析为 `<trusted-cwd>/cmd/main.go`。验证时必须覆盖这种带仓库目录前缀的真实调用。

删除重复配置或修改 server 后，必须完整退出并重启 Antigravity 宿主，再在 MCP Servers 页面 Refresh。仅 Refresh、新建 Agent 会话、重新加载窗口、覆盖配置或替换二进制都不能保证旧 stdio client 被销毁；已经 closing 的 client 不能原地复活。

## 修改配置后必须重启宿主

修改任何本地 stdio MCP 的 command、args、cwd、env、server 名、启用状态或配置来源后，必须完整停止并重新启动实际拥有 stdio 管道的宿主进程，然后再验证。该规则适用于 Codex、Claude Code、Antigravity 以及其他 MCP host：

- Codex Desktop/IDE：完整退出并重新打开宿主应用；Codex CLI：结束当前 CLI 进程并重新启动。
- Claude Code：结束当前 Claude Code/CLI 或承载其 MCP client 的 IDE host，再重新启动。
- Antigravity IDE/Desktop：完整退出并重新打开 Antigravity；Antigravity CLI：结束当前 CLI 进程并重新启动。

不得把 Refresh、Reload Window、重新打开项目、新建聊天或手工杀掉/重启 `mcp-lsp` 当作宿主重启。stdio 管道和 MCP client 由宿主拥有；宿主未重启时，旧进程、旧 env、旧配置快照或 closing client 都可能继续被复用。无法重启宿主时，把在线验证记为 `BLOCKED_HOST_RESTART_REQUIRED`，不得声称配置已经生效。

## Agent 自适配

Agent 可以根据事实调整 server 名、路径、环境变量、timeout、审批设置及客户端专属字段，也可以修改本技能和 references/provider-configs.md，使其匹配已验证的新版本行为。

自适配必须保留这些不变量：

1. 只写项目级配置。
2. 先按客户端同步项目根 AGENTS.md、CLAUDE.md 或两者，并保证重复执行不会重复追加。
3. 写入前严格读取并解析现有 JSON/TOML；未知结构立即阻断。
4. 逐字段合并，保留其他 server 和非 MCP 设置，不得整份覆盖。
5. 同名 server 内容不同时先展示差异并判断 owner；不得静默替换。
6. command 使用已验证的当前平台二进制绝对路径；cwd 和 LSP roots 指向用户明确选择的同一个 `<trusted-cwd>`，Windows 原生与 WSL 路径不得混用。
7. 不写密钥、令牌、账号、OAuth secret 或其他凭据。
8. 缺文件、错误架构、解析失败、schema 不明或客户端不受支持时 fail-fast。
9. 三个 sidecar 必需字段必须显式写在 server env 中；原生 Windows 的两个受信 LSP bundle 字段同样必须显式提供。不为一次性差异增加默认值、兼容分支或隐式 fallback。
10. 保留用户已有 `GOTOOLCHAIN` 策略。`auto` 或 `<name>+auto` 由 Go 在模块/go.work 目录中执行真实选择；不得为某个项目硬编码通用 Go 补丁版本。

如果目标项目的布局与 bin/LSP 不同，Agent 应直接按实际路径配置并更新说明，而不是强行搬运目录。移动项目或换机器后，要重新确认绝对路径和二进制架构。

## 编辑方法

1. 先输出拟修改文件、已有 server、目标 server 和字段差异。
2. 使用精确补丁或结构化编辑完成最小变更。
3. JSON 必须重新解析；TOML 必须通过当前客户端或可靠 TOML parser 重新解析。
4. 检查 diff，确认未删除或重排无关设置。
5. 配置含本机绝对路径时，明确说明是否适合提交；不得把本机配置冒充跨机器通用配置。

## 验证

1. 重新验证 command 指向存在、非空、架构正确的二进制，并确认其 GOOS 与 Windows 原生/WSL 路径体系一致。
2. 重新解析项目根及其上一级，确认配置中的 cwd、`GO_AGENT_LSP_ROOT` 和解码后的 `GO_AGENT_LSP_ROOTS` 与用户选择的 `<trusted-cwd>` 完全一致；不得只凭字符串看起来相近就通过。
3. 重新读取本次客户端对应的 AGENTS.md、CLAUDE.md 或两者，确认受管规则存在一次且必需语义完整。
4. 重新解析所有修改后的 JSON/TOML。
5. Codex：完整退出并重启 Codex Desktop/IDE 宿主或 CLI 进程；信任项目后用 /mcp 或 codex mcp list 检查。
6. Claude Code：完整结束并重启 Claude Code/CLI 或承载 MCP client 的 IDE host；完成 workspace trust 和项目 MCP 批准后，用 /mcp、claude mcp list 或 claude mcp get 检查。
7. Antigravity：完整退出并重启 Antigravity IDE/Desktop 或 CLI 进程；宿主重启后再在 MCP Servers 页面 Refresh，或在 CLI 用 /mcp 检查。
8. 至少实际调用 file、structure、inspect、xref、diagnostics 中的一项；列表显示 enabled 不是运行证据。
9. 对含中文、空格或字面 `%` 的项目路径，至少用 `file(action=diagnostics)` 验证一次 file URI 能回到 workspace 内本机路径；`path_outside_workspace` 不能记为 PASS。
10. 对选择“仓库上一级”的配置，必须实际读取或导航一个位于上一级内、但不在本仓库内的已知文件；若没有可安全验证的相邻文件，明确记为未验证，不得扩大到其他目录取证。
11. 对 Go 项目保留并验证用户的 `GOTOOLCHAIN=auto`/`<name>+auto` 策略；版本探测应在已解析的 module 或 go.work 目录执行。
12. 若配置修改需要第二次复核，重复读取配置，确认没有重复规则、重复 server、stale 路径或旧字段。
13. Antigravity 必须再次扫描 `.agents/plugins/*/mcp_config.json` 和 workspace 内 `.gemini/config/mcp_config.json`，确认没有同名 `lsp` 副本；`initialize`、`tools/list` 和 UI enabled 不能替代至少一次真实 `tools/call`。

报告二进制目标、修改的项目级文件、保留的既有设置、解析结果、客户端发现状态和实际工具调用证据。未启动对应客户端时，把在线连接明确标为未验证。
