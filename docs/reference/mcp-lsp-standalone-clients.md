# mcp-lsp 独立客户端配置

本文是仓库内可跟踪的当前参考，适用于把 `cmd/mcp-lsp` 构建产物作为本地 stdio MCP server 接入 Codex、Claude Code 和 Google Antigravity。跨平台发行包中的 `bin/LSP/README.md`、配置 skill 与本文保持同一契约。

## 必需启动契约

独立 `mcp-lsp` 会 fail-fast。每一家客户端的 `lsp` server 都必须在自己的 `env` 中显式提供：

| 环境变量 | 独立 dev 二进制的值 | 作用 |
| --- | --- | --- |
| `SUPER_DOLPHIN_RUNTIME_MODE` | `dev` | 选择源码/dev 运行时；发行包 owner 使用 `packaged` |
| `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` | source/dev 使用源码 checkout 根；分发包使用其资源根，均为绝对路径 | dev 下建立 `PROJECT_ROOT`，不能从目标文件或进程当前目录猜测 |
| `SUPER_DOLPHIN_DEPENDENCY_PROFILE` | `production` | 独立 sidecar 的生产依赖装配；`desktop_host` 只属于桌面 owner 开发启动器 |

缺少前两个字段时会报：

```text
sidecar requires SUPER_DOLPHIN_RUNTIME_MODE and SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR
```

补齐后若缺少第三个字段会报：

```text
SUPER_DOLPHIN_DEPENDENCY_PROFILE is required for production bootstrap
```

本文示例的 `bin/LSP` 是跨平台分发包布局，不是 runtime parser 强制检查的目录名。从源码自行构建时，resources 指向源码 checkout 根；使用分发包时，指向随二进制一起交付的资源根。不要给独立二进制增加默认值来掩盖配置错误。项目级 LSP 还应显式提供：

```text
GO_AGENT_LSP_ROOT=<目标项目根绝对路径>
GO_AGENT_LSP_ROOTS=<由 JSON encoder 生成的绝对路径数组>
```

## Windows 自动选择：PowerShell 或 WSL

不增加人工平台变量。配置工具或 Agent 必须按最终启动 MCP server 的客户端进程自动选择同一列中的全部值：

| 配置项 | Windows 原生 PowerShell/Desktop | WSL 内客户端 |
| --- | --- | --- |
| x64 `command` | `G:/develop/中转/new-api-main/bin/LSP/mcp-lsp-windows-x64.exe` | `/mnt/g/develop/中转/new-api-main/bin/LSP/mcp-lsp-linux-x86` |
| `cwd` | `G:/develop/中转/new-api-main` | `/mnt/g/develop/中转/new-api-main` |
| runtime resources | `G:/develop/中转/new-api-main` | `/mnt/g/develop/中转/new-api-main` |
| LSP root | `G:/develop/中转/new-api-main` | `/mnt/g/develop/中转/new-api-main` |

Windows ARM64 和 x86 主机分别选择 `mcp-lsp-windows-arm64.exe` 和 `mcp-lsp-windows-x86.exe`；WSL ARM64 选择 Linux `*-arm`。原生 Windows 配置优先使用正斜杠，避免手工转义 JSON/TOML 反斜杠。WSL 即使能启动 Windows `.exe`，该进程仍使用 Windows 路径语义；本参考要求 WSL 客户端使用 Linux 二进制和 `/mnt/...` 路径。

`mcp-lsp` 按已构建二进制的 GOOS 自动采用 Windows 或 Linux 的路径、可执行文件与 URI 规则；它不会把 `G:/...` 猜成 `/mnt/g/...`。command、cwd、runtime resources 和两个 LSP root 字段必须属于同一种路径体系。

当前 shell 就是最终客户端环境时，PowerShell 以 `$PSVersionTable.PSEdition -eq "Desktop"` 或 `$IsWindows` 识别原生 Windows，以 `$IsLinux` 加 `WSL_INTEROP`/`WSL_DISTRO_NAME` 识别 WSL；POSIX shell可检查这些 WSL 变量，或检查 `/proc/sys/kernel/osrelease` 是否包含 `microsoft`。架构分别读取 `$env:PROCESSOR_ARCHITECTURE` 或 `uname -m`。当前 shell 只负责编辑另一环境里的 Desktop 配置时，仍以最终客户端进程为准。

## Codex：`.codex/config.toml`

```toml
[mcp_servers.lsp]
enabled = true
required = true
command = "G:/develop/中转/new-api-main/bin/LSP/mcp-lsp-windows-x64.exe"
args = []
cwd = "G:/develop/中转/new-api-main"
startup_timeout_sec = 30

[mcp_servers.lsp.env]
SUPER_DOLPHIN_RUNTIME_MODE = "dev"
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = "G:/develop/中转/new-api-main"
SUPER_DOLPHIN_DEPENDENCY_PROFILE = "production"
GO_AGENT_LSP_ROOT = "G:/develop/中转/new-api-main"
GO_AGENT_LSP_ROOTS = '["G:/develop/中转/new-api-main"]'
```

项目必须受信任。修改后重启客户端，并用 `/mcp` 或 `codex mcp list` 检查。Codex 的 stdio 配置以 `command` 启动，不需要额外的 `type = "stdio"`。参见 [Codex MCP 官方文档](https://developers.openai.com/codex/mcp/)。

## Claude Code：`.mcp.json`

```json
{
  "mcpServers": {
    "lsp": {
      "command": "G:/develop/中转/new-api-main/bin/LSP/mcp-lsp-windows-x64.exe",
      "args": [],
      "env": {
        "SUPER_DOLPHIN_RUNTIME_MODE": "dev",
        "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": "G:/develop/中转/new-api-main",
        "SUPER_DOLPHIN_DEPENDENCY_PROFILE": "production",
        "GO_AGENT_LSP_ROOT": "G:/develop/中转/new-api-main",
        "GO_AGENT_LSP_ROOTS": "[\"G:/develop/中转/new-api-main\"]"
      }
    }
  }
}
```

这是 project scope。首次加载时完成 workspace trust 和项目 MCP 批准，再用 `/mcp`、`claude mcp list` 或 `claude mcp get lsp` 检查。不要误写默认 local scope 或用户级 `~/.claude.json`。参见 [Claude Code MCP 官方文档](https://code.claude.com/docs/en/mcp)。

## Google Antigravity：`.agents/mcp_config.json`

```json
{
  "mcpServers": {
    "lsp": {
      "command": "G:/develop/中转/new-api-main/bin/LSP/mcp-lsp-windows-x64.exe",
      "args": [],
      "cwd": "G:/develop/中转/new-api-main",
      "env": {
        "SUPER_DOLPHIN_RUNTIME_MODE": "dev",
        "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": "G:/develop/中转/new-api-main",
        "SUPER_DOLPHIN_DEPENDENCY_PROFILE": "production",
        "GO_AGENT_LSP_ROOT": "G:/develop/中转/new-api-main",
        "GO_AGENT_LSP_ROOTS": "[\"G:/develop/中转/new-api-main\"]"
      }
    }
  }
}
```

在 MCP Servers 页面 Refresh，或在 CLI 使用 `/mcp`。本地 stdio server 使用 `command`；`serverUrl` 只用于远程 server。参见 [Antigravity MCP 官方文档](https://antigravity.google/docs/mcp)。

## 中文路径与 file URI

客户端可以把 `G:/develop/中转/new-api-main/main.go` 编码成包含 `%E4...` 的标准 `file:` URI。当前实现只在 URI 到本机绝对路径的边界进行一次百分号解码，然后再做 workspace containment；裸路径中的字面 `%` 不会被当成 URI 转义重复解码。

如果 diagnostics 返回 `path_outside_workspace`：

1. 用 `go version -m <command>` 确认正在运行本次构建的正确 GOOS/GOARCH 二进制。
2. 确认 command、cwd、runtime resources、`GO_AGENT_LSP_ROOT` 和 `GO_AGENT_LSP_ROOTS` 没有混用 Windows 与 WSL 路径。
3. 不要把 `%E4...` 形式的 URI path 手工写入 cwd 或 root；这些字段应使用真实 Unicode 本机路径。
4. 实际调用 `file(action=diagnostics)` 验证，不要把客户端仅显示 enabled 当成 PASS。

## Go 自动工具链

保留用户配置的 `GOTOOLCHAIN=auto` 或 `<name>+auto`。mcp-lsp 会在解析出的 module 或 go.work 目录中运行 PATH 候选 `go version`，让 Go 根据当前 `go.mod`/`go.work` 选择 PATH 中或可下载的工具链；该探测不会再只比较进程启动目录下 PATH `go` 的固定版本。

不要把某个 `GOTOOLCHAIN=go1.x.y` 补丁版本写成跨项目通用配置。显式 `GOTOOLCHAIN=local` 会关闭自动切换，版本不足时继续 fail-fast 是预期行为。工具链选择规则以 [Go 官方说明](https://go.dev/doc/toolchain) 为准。

## 验证清单

1. `go version -m` 显示的 GOOS/GOARCH 与目标客户端环境一致。
2. 三个 `SUPER_DOLPHIN_*` 字段和两个 root 字段都位于当前 `lsp` server 的 `env` 内。
3. TOML/JSON 能重新解析，且未覆盖其他 MCP server。
4. 客户端完成信任、批准和刷新。
5. 依次调用 `file`、`structure`、`inspect`、`xref` 和 `file(action=diagnostics)`。
6. 在包含中文、空格或字面 `%` 的路径中重复 diagnostics。
7. Go 项目用 `GOTOOLCHAIN=auto` 验证模块声明高于 PATH 默认版本时仍能启动。
