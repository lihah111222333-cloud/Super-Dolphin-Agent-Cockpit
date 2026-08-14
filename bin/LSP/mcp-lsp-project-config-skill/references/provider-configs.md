# 三家项目级 MCP 配置

本参考适用于本地 stdio 形式的 mcp-lsp。配置格式核对日期：2026-08-14。

所有示例都必须保留三个 sidecar 启动字段：`SUPER_DOLPHIN_RUNTIME_MODE=dev`、`SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=<分发包资源根；源码构建则为 checkout 根，必须是绝对路径>`、`SUPER_DOLPHIN_DEPENDENCY_PROFILE=production`。它们必须位于当前 server 的 `env` 内，不能依赖另一个 shell 临时导出，也不能由二进制隐式补默认值。

## 先看懂四个路径

| 配置项 | 含义 | 本参考的取值规则 |
|---|---|---|
| `command` | MCP host 实际创建的进程 | 直连时是 mcp-lsp 二进制；Windows host 桥接 WSL sidecar 时必须是 Windows `wsl.exe`，Linux ELF 位于 args 的最后一项。 |
| `cwd` | mcp-lsp 进程的默认启动目录，也是未另传 `work_dir` 时相对工具路径的起点 | 项目级配置统一设为项目根。客户端 schema 没有 `cwd` 字段时，必须从项目根启动客户端，并验证子进程实际继承的 cwd 是项目根。 |
| `GO_AGENT_LSP_ROOT` / `GO_AGENT_LSP_ROOTS` | LSP 可以读取、导航和修改的**可信工作区根** | 单项目时两者都指向项目根。首个根必须是绝对路径；`cwd` 不会自动扩大可信范围，根外路径必须返回 `path_outside_workspace`。 |
| `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` | sidecar 查找运行时资源的根 | 源码构建时是当前 checkout 根；分发包则是该包的资源根。它不是 Agent 每次工具调用要传的源码路径。 |

配置文件负责一次性写清绝对二进制路径、资源根和可信根。配置成功后，Agent 调用 LSP 工具时使用相对项目根的路径即可，例如 `internal/foo.go`、`internal/foo.go:42:9`；默认 cwd 已经是项目根，不需要每次传 `/absolute/project/internal/foo.go`，也不需要重复传整个绝对 `work_dir`。只有确实要切换到另一个已配置的可信根时才显式传 `work_dir`。

> 示例中的 `/absolute/project` 代表目标项目根，必须在落地配置时替换为该项目的真实绝对路径。示例二进制使用 macOS Apple Silicon；其他平台只替换 `command` 为“平台二进制映射”中的对应文件，不能同时混用另一平台的路径语义。

原生 Windows 的 Go LSP 还要求同一 server 显式提供 `SUPER_DOLPHIN_LSP_BUNDLE_DIR=<项目根>/bin/LSP/lsp` 和 `SUPER_DOLPHIN_LSP_MANIFEST=<项目根>/bin/LSP/lsp/lsp-manifest.json` 的绝对路径。它们绑定包内 gopls 身份，不扩大 `GO_AGENT_LSP_ROOT(S)` 定义的 workspace。WSL 使用 Linux 二进制，不得混入 Windows bundle 路径。

## Codex

项目文件：.codex/config.toml。项目必须被信任，否则 Codex 忽略项目 .codex 配置层。

建议只配置目标项目根的 `<project-root>/.codex/config.toml`。不要把本项目的 lsp server 写入 `~/.codex/config.toml` 或其他用户级全局配置；二进制路径、默认 cwd 和可信根都应由各项目独立声明。

使用规范同步到项目根 AGENTS.md。

~~~toml
[mcp_servers.lsp]
enabled = true
required = true
# macOS Apple Silicon selects mcp-lsp-mac-arm; use the platform mapping below on other hosts.
# macOS Apple Silicon 选择 mcp-lsp-mac-arm；其他平台按下方映射替换文件名。
command = "/absolute/project/bin/LSP/mcp-lsp-mac-arm"
args = []
# Use the project root as the default startup cwd; agents may then pass relative source paths.
# mcp-lsp 的默认启动 cwd 固定为项目根；Agent 后续可直接传 internal/foo.go 等相对路径。
cwd = "/absolute/project"
startup_timeout_sec = 30

[mcp_servers.lsp.env]
SUPER_DOLPHIN_RUNTIME_MODE = "dev"
# For a source build, the runtime resources root is the checkout root and must be absolute.
# 源码构建时的运行时资源根是 checkout 根；该值必须是绝对路径。
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = "/absolute/project"
SUPER_DOLPHIN_DEPENDENCY_PROFILE = "production"
# A single-project trusted root is the project root; cwd does not implicitly define trust.
# 单项目的可信根就是项目根；它限制 LSP 可访问范围，不由 cwd 隐式推断。
GO_AGENT_LSP_ROOT = "/absolute/project"
# This TOML string contains a JSON array whose first trusted root must be absolute.
# JSON 数组字符串；首项是主可信根且必须为绝对路径。
GO_AGENT_LSP_ROOTS = '["/absolute/project"]'
~~~

Codex 的 stdio 配置由 command 判定，不需要额外 type = "stdio"。required = true 会在启用的 server 无法初始化时阻断启动或恢复，适合仓库强制 LSP。

上面 TOML 中的注释可以保留在真实配置里。Codex 从该项目根启动 lsp 后，Agent 的 `file`、`inspect`、`xref`、`grep`、`structure`、`patch_edit` 和 `completion` 调用默认都应传项目相对路径。

验证：

~~~text
/mcp
codex mcp list
~~~

配置修改后必须先完整退出并重启 Codex Desktop/IDE 宿主或 Codex CLI 进程，再执行上述验证；Reload Window 或新建聊天不等于宿主重启。

官方依据：

- https://developers.openai.com/codex/mcp/
- https://learn.chatgpt.com/docs/codex/config-advanced#project-config-files-codexconfigtoml
- https://learn.chatgpt.com/docs/codex/mcp

## Claude Code

项目文件：项目根 .mcp.json。这是 project scope，会被团队共享；不要混淆默认 local scope。

使用规范同步到项目根 CLAUDE.md。

字段注释：`command` 示例选择 macOS Apple Silicon 二进制；`SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` 是绝对资源根；两个 `GO_AGENT_LSP_ROOT(S)` 是绝对可信根。`.mcp.json` 是严格 JSON，不能在下面可复制配置块中加入 `//` 注释。Claude Code 的项目 MCP schema 没有通用 `cwd` 字段，因此应从项目根启动并批准该项目 server，再实测子进程继承的默认 cwd 是项目根；成功后 Agent 工具参数同样只传项目相对路径。

~~~json
{
  "mcpServers": {
    "lsp": {
      "command": "/absolute/project/bin/LSP/mcp-lsp-mac-arm",
      "args": [],
      "env": {
        "SUPER_DOLPHIN_RUNTIME_MODE": "dev",
        "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": "/absolute/project",
        "SUPER_DOLPHIN_DEPENDENCY_PROFILE": "production",
        "GO_AGENT_LSP_ROOT": "/absolute/project",
        "GO_AGENT_LSP_ROOTS": "[\"/absolute/project\"]"
      }
    }
  }
}
~~~

等价 CLI 添加方式：

~~~bash
claude mcp add --scope project \
  --env SUPER_DOLPHIN_RUNTIME_MODE=dev \
  --env SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/absolute/project \
  --env SUPER_DOLPHIN_DEPENDENCY_PROFILE=production \
  --env GO_AGENT_LSP_ROOT=/absolute/project \
  --env 'GO_AGENT_LSP_ROOTS=["/absolute/project"]' \
  --transport stdio lsp -- /absolute/project/bin/LSP/mcp-lsp-mac-arm
~~~

Claude Code 会要求批准项目 .mcp.json 中的 server。待批准时 claude mcp list 会显示 Pending approval；进入交互会话完成 workspace trust 和 MCP 批准。

验证：

~~~text
/mcp
claude mcp list
claude mcp get lsp
~~~

配置修改后必须先结束并重启 Claude Code/CLI 或承载其 MCP client 的 IDE host，再完成批准和上述验证；新建会话不能替代宿主重启。

官方依据：

- https://code.claude.com/docs/en/mcp
- https://code.claude.com/docs/en/claude-directory

## Google Antigravity

项目文件：.agents/mcp_config.json。IDE、CLI 和 SDK 都能发现 workspace-local 配置。

使用规范同步到项目根 AGENTS.md。

字段注释：`command` 示例选择 macOS Apple Silicon 二进制；`cwd` 是 mcp-lsp 的默认启动 cwd，固定为项目根；两个 `GO_AGENT_LSP_ROOT(S)` 是可信根，单项目时同样固定为项目根。`.agents/mcp_config.json` 是严格 JSON，下面保持为可解析配置，不写 `//` 注释。

~~~json
{
  "mcpServers": {
    "lsp": {
      "command": "/absolute/project/bin/LSP/mcp-lsp-mac-arm",
      "args": [],
      "cwd": "/absolute/project",
      "env": {
        "SUPER_DOLPHIN_RUNTIME_MODE": "dev",
        "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": "/absolute/project",
        "SUPER_DOLPHIN_DEPENDENCY_PROFILE": "production",
        "GO_AGENT_LSP_ROOT": "/absolute/project",
        "GO_AGENT_LSP_ROOTS": "[\"/absolute/project\"]"
      }
    }
  }
}
~~~

配置修改后必须先完整退出并重启 Antigravity IDE/Desktop 或 CLI 宿主。重启后，IDE 在 Agent 侧栏 ... → MCP Servers → Manage MCP Servers → Refresh；CLI 输入 /mcp 查看状态和日志。仅 Refresh 或新建 Agent 会话不能替代宿主重启。

远程 MCP 使用 serverUrl；本技能配置的是本地 stdio，所以只使用 command。不要使用旧的远程字段 url 或 httpUrl。

官方依据：

- https://antigravity.google/docs/mcp
- https://antigravity.google/docs/gcli-migration

## 平台二进制映射

| 系统 | 架构 | 文件 |
|---|---|---|
| macOS | Intel | bin/LSP/mcp-lsp-mac-x86 |
| macOS | Apple Silicon | bin/LSP/mcp-lsp-mac-arm |
| Linux | x86-64 | bin/LSP/mcp-lsp-linux-x86 |
| Linux | ARM64 | bin/LSP/mcp-lsp-linux-arm |
| Windows | x86-64 | bin/LSP/mcp-lsp-windows-x86.exe |
| Windows | ARM64 | bin/LSP/mcp-lsp-windows-arm.exe |

配置 Agent 只选择最终启动 MCP server 的客户端平台对应文件，不把其他平台二进制误配为当前命令：

- macOS Apple Silicon（常见 `uname -m` 为 `arm64`）选择 `mcp-lsp-mac-arm`。
- macOS Intel（`x86_64`）选择 `mcp-lsp-mac-x86`。
- Linux/WSL 的 `x86_64` 选择 `mcp-lsp-linux-x86`，`aarch64`/`arm64` 选择 `mcp-lsp-linux-arm`。
- Windows 原生进程的 AMD64 选择 `mcp-lsp-windows-x86.exe`，ARM64 选择 `mcp-lsp-windows-arm.exe`。

这里选择的是**二进制文件**；`cwd` 和可信根仍是目标项目根。选择完成后应使用 `go version -m`、`file` 或 PE Machine 信息核对真实 GOOS/GOARCH，不能只相信文件名。

## Windows 原生、WSL 原生与 Windows→WSL 桥接

不配置人工平台开关。先确认 MCP host，再确认用户要求 sidecar 在 Windows 还是 WSL 运行：

| 配置项 | Windows 原生直连 | WSL host 原生直连 | Windows host → WSL sidecar |
|---|---|---|---|
| `command`（x86-64） | `G:/project/bin/LSP/mcp-lsp-windows-x86.exe` | `/mnt/g/project/bin/LSP/mcp-lsp-linux-x86` | `C:/Windows/System32/wsl.exe` |
| host `cwd` | `G:/project` | `/mnt/g/project` | `G:/project` |
| sidecar 启动 | command 本身 | command 本身 | args: `--cd /mnt/g/project env KEY=VALUE... PATH=<实机探测的Linux PATH> ./bin/LSP/mcp-lsp-linux-x86` |
| resources / roots | `G:/project` | `/mnt/g/project` | args 的 env 段使用 `/mnt/g/project` |
| Windows gopls bundle | 必需 | 不使用 | 不使用；由 WSL 的 Linux LSP 依赖负责 |

ARM64 主机把文件名替换为同平台 `*-arm`，其他字段不变。原生 Windows 配置使用正斜杠可以避免 TOML/JSON 反斜杠转义；URI 中的中文、空格和字面 `%` 由新二进制在 URI 到本机路径边界解码一次。不要把 `%E4...` 形式直接写进 cwd 或 roots。

WSL 可以调用 Windows `.exe`，但那仍是 Windows 进程语义。反向桥接也不会自动发生：Windows Desktop 不能直接执行 `/mnt/...` Linux ELF。用户要求 Windows Codex Desktop 使用 WSL sidecar 时，必须显式选择第三列，由 `wsl.exe` 建立边界。完整模板见 `bin/LSP/codex-windows-wsl-lsp-config.example.toml`。

第三列中 Windows `cwd` 和 WSL roots 字符串必然不同，但必须映射到同一目录。用 `wslpath`/`readlink -f` 验证映射，并用配置中的完整 command+args 完成 MCP initialize 和真实 tools/call。只在 WSL shell 中裸跑 ELF 不能证明 Windows host 配置正确。

Windows host 通过 `wsl.exe env` 启动 sidecar 时，不保证加载 WSL 用户的登录 shell PATH。配置前必须在目标发行版中运行 `command -v go gopls node typescript-language-server`，收集实际目录并在 args 的 `env` 段显式写入绝对 Linux PATH。常见的用户级目录是 `/home/<wsl-user>/.local/bin`，但用户名和安装位置必须实测，不能把该示例当默认值，也不能在 TOML args 中写不会展开的字面 `$HOME`。

PATH 缺失可能不会让 sidecar 启动失败：能力探测会找不到语义语言服务器，`tools/list` 可能只返回 `file`、`grep`。因此桥接验收必须精确核对七个工具，并实际调用至少一个 `structure`、`inspect` 或 `xref` 语义工具；仅 initialize 成功、只读文件成功或看到两个基础工具均不算完整可用。

当前 shell 与目标客户端相同时，PowerShell 用 `$IsWindows`/`$IsLinux` 和 `WSL_INTEROP`、`WSL_DISTRO_NAME` 判断原生 Windows 或 WSL；POSIX shell 用 WSL 环境变量或 `/proc/sys/kernel/osrelease` 中的 `microsoft` 判断，并用 `uname -m` 选择 x86-64/ARM64。当前 shell 不是最终 owner 时，以客户端进程平台为准。

## Go 自动工具链

保留用户已有的 `GOTOOLCHAIN=auto` 或 `<name>+auto`。mcp-lsp 在已解析的 module/go.work 目录中运行 PATH 候选 `go version`，让 Go 根据当前 `go.mod`/`go.work` 选择 PATH 中或可下载的工具链。不要因为本机 PATH 默认 Go 版本较旧，就把某个 `GOTOOLCHAIN=go1.x.y` 补丁版本硬编码进可共享示例。显式 `GOTOOLCHAIN=local` 会禁用自动切换，版本不足时应继续 fail-fast。

## 启动错误对照

| 错误 | 必须检查 |
|---|---|
| `sidecar requires SUPER_DOLPHIN_RUNTIME_MODE and SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` | 当前 server 的 env 是否显式包含前两个字段，资源根是否为绝对路径 |
| `SUPER_DOLPHIN_DEPENDENCY_PROFILE is required for production bootstrap` | 当前 server 的 env 是否包含 `SUPER_DOLPHIN_DEPENDENCY_PROFILE=production` |
| `path_outside_workspace` 且路径含 `%E4...`/`%20` | 是否运行新二进制；command、cwd、roots 是否属于同一 Windows 或 WSL 路径体系 |
| Windows Codex 启动 `/mnt/.../mcp-lsp-linux-*` 失败 | Windows `CreateProcess` 不会自动进入 WSL；改用 `command=wsl.exe`，并在 args 中传 `--cd`、`env`、Linux roots 和 ELF |
| sidecar 能启动但 `tools/list` 只有 `file`、`grep` | 用配置的完整桥接命令检查 `command -v gopls`、`command -v typescript-language-server`；把实机探测的 Linux PATH 显式注入 args 的 `env` 段后重启宿主 |
| `Go toolchain selection failed` | PATH 是否有可运行的 Go；`GOTOOLCHAIN` 是否允许自动切换；module/go.work 声明是否可满足 |
| Antigravity 已显示工具，但调用时报 `client is closing: EOF` 或 `Transport closed` | 对比 `.agents/mcp_config.json`、`.agents/plugins/*/mcp_config.json`、workspace `.gemini/config/mcp_config.json` 和只读全局配置；从 sidecar 日志确认实际 command、root、请求路径和第一个错误 |
| `resolve parent path: lstat /parent/cmd: no such file or directory` | 若 trusted root 是仓库上一级，请求必须包含仓库目录前缀，例如 `repo/cmd/...`；不要把按项目根书写的 `cmd/...` 直接交给父级 root |
| sidecar 日志先有 `tools/call done`，随后 `mcp stdio: read failed EOF` | 调用曾经成功，EOF 是 stdin 关闭结果；检查上游取消、重复 server 和旧 client，不得判为二进制崩溃 |
| 修改配置后仍持续 `client is closing` | 完整退出并重启实际拥有 stdio 管道的 Codex、Claude Code、Antigravity 或其他宿主进程，再 Refresh/检查；旧 stdio client 无法原地恢复 |
