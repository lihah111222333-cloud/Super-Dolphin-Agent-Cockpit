# mcp-lsp 跨平台包使用说明

本目录提供 macOS、Linux、Windows 的 x86-64/ARM64 mcp-lsp 二进制、AGENTS.md 样板，以及指导 Agent 为 Codex、Claude Code、Google Antigravity配置项目级 MCP 的技能。

技能不携带固定配置脚本。Agent 会根据当前系统、项目目录、已有配置和客户端版本自行选择二进制并精确编辑。

## 必需启动契约

`mcp-lsp` 是 fail-fast 的独立 sidecar。Codex、Claude Code 和 Antigravity 的每一个本地 stdio server 配置都必须在该 server 的 `env` 中显式提供以下三个字段：

| 环境变量 | 独立 dev 二进制的值 | 说明 |
|---|---|---|
| `SUPER_DOLPHIN_RUNTIME_MODE` | `dev` | 只允许 `dev` 或发行包使用的 `packaged` |
| `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` | 本分发包的资源根绝对路径；源码构建则为 checkout 根 | dev 下是运行时资源根，同时用于建立 `PROJECT_ROOT`；不能从目标文件或当前目录猜测 |
| `SUPER_DOLPHIN_DEPENDENCY_PROFILE` | `production` | 独立 sidecar 使用生产依赖装配；`desktop_host` 只属于桌面 owner 开发启动器 |

缺少前两个字段会报 `sidecar requires SUPER_DOLPHIN_RUNTIME_MODE and SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR`；补齐后若缺第三个字段，会报 `SUPER_DOLPHIN_DEPENDENCY_PROFILE is required for production bootstrap`。这些错误不能靠代码默认值绕过。

Windows 原生版共享 gopls 时还必须显式提供受信 bundle：

| 环境变量 | `bin/LSP` 交付布局的值 |
|---|---|
| `SUPER_DOLPHIN_LSP_BUNDLE_DIR` | `<项目根>/bin/LSP/lsp` 的绝对路径 |
| `SUPER_DOLPHIN_LSP_MANIFEST` | `<项目根>/bin/LSP/lsp/lsp-manifest.json` 的绝对路径 |

这两个字段只绑定包内 gopls 身份，不定义可信 workspace；workspace 仍只由外部 `GO_AGENT_LSP_ROOT(S)` 配置决定。缺少 bundle、manifest、摘要或原生 `gopls.exe` 时 Windows Go LSP 必须 fail-fast，不能退回 PATH gopls。

项目作用域还应显式提供 `GO_AGENT_LSP_ROOT` 和合法 JSON 数组形式的 `GO_AGENT_LSP_ROOTS`。Windows 配置优先使用正斜杠路径，避免在 JSON 字符串中手工转义反斜杠。

## 目录内容

| 文件或目录 | 用途 |
|---|---|
| mcp-lsp-mac-x86 | macOS Intel |
| mcp-lsp-mac-arm | macOS Apple Silicon |
| mcp-lsp-linux-x86 | Linux x86-64 |
| mcp-lsp-linux-arm | Linux ARM64 |
| mcp-lsp-windows-x86.exe | Windows x86-64 |
| mcp-lsp-windows-arm.exe | Windows ARM64 |
| lsp/lsp-manifest.json、lsp/bin/gopls.exe | Windows 共享 gopls 的受信 bundle 与原生二进制 |
| AGENTS.md | LSP 导航、影响分析、诊断和验证规则样板 |
| codex-lsp-config.example.toml | 从本项目 `.codex/config.toml` 提取的 Codex LSP 配置示例，含平台二进制、默认 cwd、可信根和相对工具路径注释 |
| mcp-lsp-project-config-skill | 三家客户端的项目级配置判断方法和参考 |

## 第一步：保留目录结构

把整个 bin/LSP 放在目标项目根目录下。macOS/Linux 如果复制过程丢失执行权限：

~~~bash
chmod +x bin/LSP/mcp-lsp-mac-* bin/LSP/mcp-lsp-linux-*
~~~

## 第二步：准备客户端规范文件

Codex 和 Antigravity 使用项目根 AGENTS.md。项目根没有该文件时：

~~~bash
cp bin/LSP/AGENTS.md ./AGENTS.md
~~~

Claude Code 使用项目根 CLAUDE.md。不要简单把文件名改成 CLAUDE.md 后整份复制，因为样板标题是 AGENTS.md；应让 Agent 提取 mcp-lsp-project-rules:start 和 mcp-lsp-project-rules:end 之间的内容，追加或合并到 CLAUDE.md。

已有 AGENTS.md 或 CLAUDE.md 时不要覆盖。人工或让 Agent 合并受管范围中的 LSP 工具链、强制证据链、不可用处理和验证规则，并保留项目自己的构建、测试、目录与提交要求。

重复执行时先检查目标文件是否已有同一标记：已有则更新该范围，没有标记但已有等价规则则只补缺失项，禁止重复追加。

## 第三步：安装项目级技能

Codex 和 Antigravity：

~~~bash
mkdir -p .agents/skills
cp -R bin/LSP/mcp-lsp-project-config-skill \
  .agents/skills/mcp-lsp-project-config
~~~

Claude Code：

~~~bash
mkdir -p .claude/skills
cp -R bin/LSP/mcp-lsp-project-config-skill \
  .claude/skills/mcp-lsp-project-config
~~~

如果目标目录已存在，先比较内容，不要直接覆盖。新建此前不存在的顶层技能目录后，客户端可能需要重启。

## 第四步：让 Agent 自行配置

在项目中向 Agent 提出：

~~~text
使用 mcp-lsp-project-config 技能。
配置 Codex 或 Antigravity 时，把 bin/LSP/AGENTS.md 的缺失规范合并到项目根 AGENTS.md。
配置 Claude Code 时，把同一受管规则合并到项目根 CLAUDE.md。
三家一起配置时同步两份文件，保留已有更强规则并避免重复。
再检查当前系统和 CPU、bin/LSP 二进制的真实 GOOS/GOARCH、现有项目级 MCP 配置以及客户端版本。
在 Windows 上自动判断当前是原生 PowerShell 还是 WSL：PowerShell 选择 Windows .exe 和 Windows 路径，WSL 选择 Linux 二进制和 /mnt 路径，禁止混用。
根据实际情况为 Codex、Claude Code 和 Antigravity 配置项目级 mcp-lsp。
在每家 server 的 env 中显式写入 SUPER_DOLPHIN_RUNTIME_MODE、SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR、SUPER_DOLPHIN_DEPENDENCY_PROFILE、GO_AGENT_LSP_ROOT 和 GO_AGENT_LSP_ROOTS；Windows 原生版还写入 SUPER_DOLPHIN_LSP_BUNDLE_DIR 和 SUPER_DOLPHIN_LSP_MANIFEST。
逐字段合并，不覆盖其他 server，不写用户级配置，不写凭据。
完成后重新解析配置、刷新客户端，并实际调用一个 LSP 工具验证。
~~~

只配置一个客户端时明确说：

~~~text
使用 mcp-lsp-project-config，只为 Claude Code 配置项目级 MCP。
保留现有 .mcp.json 内容；如果同名 server 冲突，先报告差异再修改。
~~~

Agent 应自行完成：

1. 根据客户端合并并复核项目根 AGENTS.md、CLAUDE.md 或两者的 LSP 规则。
2. 识别 macOS/Linux/Windows 和 x86-64/ARM64；Windows 还要区分原生 PowerShell 与 WSL。
3. 用 go version -m、file 或 PE 信息验证二进制真实目标。
4. 读取全部现有配置。
5. 根据客户端当前 schema 和项目布局拟定最小补丁，并补齐三个 sidecar 必需环境变量与两个 LSP root 字段；Windows 原生版同时补齐受信 bundle 和 manifest 路径。
6. 逐字段合并并保留其他设置。
7. 重新解析 JSON/TOML。
8. 刷新客户端并调用真实 LSP 工具。

## 三家项目级配置位置

| 客户端 | 项目级文件 | 重新加载 |
|---|---|---|
| Codex | .codex/config.toml | 信任项目并重启；用 /mcp 或 codex mcp list |
| Claude Code | .mcp.json | 完成 workspace trust 和项目 MCP 批准；用 /mcp |
| Antigravity | .agents/mcp_config.json | MCP Servers → Manage MCP Servers → Refresh，或 CLI /mcp |

这些配置都建议放在目标项目根目录。Codex 使用 `<project-root>/.codex/config.toml`，不要把本示例写入 `~/.codex/config.toml` 等用户级全局配置；LSP 的二进制、默认 cwd 和可信根应由每个项目独立声明。

具体字段示例见：

~~~text
bin/LSP/codex-lsp-config.example.toml
bin/LSP/mcp-lsp-project-config-skill/references/provider-configs.md
~~~

前者是可直接逐段合并的 Codex TOML 注释示例；后者覆盖 Codex、Claude Code 和 Antigravity。示例仅用于判断，Agent 必须先检查当前客户端版本和现有配置，不得盲目覆盖。

## 二进制映射

| 主机 | 文件 | Go 目标 |
|---|---|---|
| macOS Intel | bin/LSP/mcp-lsp-mac-x86 | darwin/amd64 |
| macOS Apple Silicon | bin/LSP/mcp-lsp-mac-arm | darwin/arm64 |
| Linux x86-64 | bin/LSP/mcp-lsp-linux-x86 | linux/amd64 |
| Linux ARM64 | bin/LSP/mcp-lsp-linux-arm | linux/arm64 |
| Windows x86-64 | bin/LSP/mcp-lsp-windows-x86.exe | windows/amd64 |
| Windows ARM64 | bin/LSP/mcp-lsp-windows-arm.exe | windows/arm64 |

不要只看文件名。优先运行：

~~~bash
go version -m bin/LSP/mcp-lsp-mac-arm
~~~

确认输出中的 build GOOS 和 GOARCH。macOS/Linux 还可用 file 交叉检查；Windows 检查 PE Machine。

### Windows：自动选择 PowerShell 或 WSL

不增加 `POWER_SHELL_OR_WSL` 一类人工开关。选择依据是启动 MCP server 的实际执行环境和二进制构建目标：

| 启动环境 | 二进制 | 项目路径示例 | URI/路径语义 |
|---|---|---|---|
| Windows 原生 PowerShell | `mcp-lsp-windows-x86.exe` 或 `mcp-lsp-windows-arm.exe` | `G:/develop/中转/new-api-main` | Windows drive/UNC |
| WSL shell / WSL 客户端进程 | `mcp-lsp-linux-x86` 或 `mcp-lsp-linux-arm` | `/mnt/g/develop/中转/new-api-main` | Linux 绝对路径 |

WSL 能调用 Windows `.exe` 不代表两套路径可以混用；本教程要求 WSL 使用 Linux 二进制。`mcp-lsp` 会按实际二进制的 GOOS 使用对应的路径和可执行文件规则，不会把 `G:/...` 猜成 `/mnt/g/...`，也不会反向转换。

当前 shell 就是最终客户端环境时，可自动探测而无需额外开关：原生 PowerShell 以 `$PSVersionTable.PSEdition -eq "Desktop"` 或 `$IsWindows` 为真作为 Windows 证据；PowerShell 运行在 WSL 时 `$IsLinux` 为真且通常存在 `WSL_INTEROP`/`WSL_DISTRO_NAME`。在 POSIX shell 中检查上述 WSL 变量，或检查 `/proc/sys/kernel/osrelease` 是否包含 `microsoft`；架构分别读取 `$env:PROCESSOR_ARCHITECTURE` 或 `uname -m`。若当前 shell 只负责编辑 Windows Desktop 的配置，仍按 Desktop 的 Windows 进程选择 `.exe`。

## 验证不能停在 enabled

客户端列表显示 lsp enabled 只证明配置被发现。还要实际调用：

1. file 读取源码。
2. structure 查看符号。
3. inspect 跳转定义。
4. xref 查询引用或调用层级。
5. file diagnostics 获取诊断。

首次调用具体语言能力时，mcp-lsp 可能按需启动对应语言服务器。依赖缺失或初始化失败必须报告，不能当成 PASS。

## 移动项目、换机器或改目录

三家本地 stdio 配置通常包含本机绝对二进制路径。移动项目、换 CPU 或换机器后，让 Agent 重新检查：

- command 是否仍存在。
- 二进制 GOOS/GOARCH 是否匹配。
- cwd、GO_AGENT_LSP_ROOT 和 GO_AGENT_LSP_ROOTS 是否指向当前项目。
- 三个 `SUPER_DOLPHIN_*` sidecar 字段是否仍存在且资源根是当前绝对路径。
- Windows 原生路径与 WSL `/mnt/...` 路径是否和所选二进制一致。
- 是否存在同名 server、旧路径或旧 schema 字段。

包含某台机器绝对路径的配置不应直接冒充跨机器通用配置。提交前由 Agent 判断哪些文件应共享，哪些应保持本地。

## Agent 可以调整什么

Agent 可以基于证据修改 server 名、路径、env、cwd、timeout、审批设置、客户端专属字段、本技能和配置参考，但必须保持：

- 项目级作用域。
- 对应客户端的 AGENTS.md、CLAUDE.md 或两者已合并 LSP 规范且不重复。
- 精确合并，不整份覆盖。
- 同名冲突先报告。
- 错误架构和未知 schema fail-fast。
- 不写凭据。
- 修改后重新解析并真实调用工具。

默认不要创建新的配置脚本或 wrapper。只有用户明确要求自动化时，才设计脚本并为冲突、幂等、原子写入和错误平台补测试。

## 常见问题

### Codex 忽略 .codex/config.toml

确认项目已被信任，修改后重启 Codex。

### Claude Code 显示 Pending approval

在项目根启动交互会话，完成 workspace trust，并批准 .mcp.json 中的项目 server。

### Antigravity 看不到 server

确认文件位于项目根 .agents/mcp_config.json，然后在 MCP Servers 页面 Refresh。

### 同名 lsp 已存在

不要覆盖。让 Agent列出现有值和目标值，确认 owner 后逐字段更新。

### 配置存在但工具不可用

检查二进制平台、执行权限、cwd、LSP roots 和外部语言服务器初始化错误，然后实际调用 file 或 structure 复测。

### 启动时报缺少 SUPER_DOLPHIN 环境变量

三个字段必须写在当前客户端 `lsp` server 自己的 `env` 内。独立配置使用 `dev`、实际运行时资源根的绝对路径和 `production`：源码构建时资源根是 checkout 根，使用本分发包时则是随 `bin/LSP` 交付的制品资源根。不要依赖另一个终端会话的临时环境，也不要给 sidecar 增加默认值。

### Windows 中文路径返回 path_outside_workspace

先用 `go version -m` 确认正在运行的是本目录最新构建的二进制，再确认 command、cwd、`GO_AGENT_LSP_ROOT(S)` 全部使用同一种 Windows 或 WSL 路径。新二进制会在 file URI 转本机路径的边界进行一次百分号解码；不要把 `%E4...` 形式的 URI path 当作裸 Windows 路径写进配置。

### Go 自动工具链被误报为 PATH 版本不匹配

保持 Go 官方的 `GOTOOLCHAIN=auto`（或 `<name>+auto`）策略即可，不要硬编码某个补丁版本作为通用配置。新二进制会在已解析的模块或 go.work 目录中运行候选 `go version`，让 Go 自己按 `go.mod`/`go.work` 选择 PATH 中或可下载的工具链。显式 `GOTOOLCHAIN=local` 禁止自动切换，版本不足时继续 fail-fast 是预期行为。

## 深入参考

- 技能：bin/LSP/mcp-lsp-project-config-skill/SKILL.md
- 三家配置示例：bin/LSP/mcp-lsp-project-config-skill/references/provider-configs.md
- Codex 官方 MCP 文档：https://developers.openai.com/codex/mcp/
- Claude Code 官方 MCP 文档：https://code.claude.com/docs/en/mcp
- Antigravity 官方 MCP 文档：https://antigravity.google/docs/mcp
