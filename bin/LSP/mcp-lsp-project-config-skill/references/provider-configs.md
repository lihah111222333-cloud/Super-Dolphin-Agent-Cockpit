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

不要配置小于 `15m` 的 `MCP_LSP_IDLE_TIMEOUT` 或旧别名：正式构建把 15 分钟作为默认值和硬下限。更短值只允许专用 `mcp_lsp_short_idle_precheck` 测试 build tag 做快速预检，不能作为生命周期交付证据。Windows 权限错误 `5`/`1314` 会以 `authorization_required` 返回；桌面宿主通过 `ApprovalRequester` 显示真实授权提示，只有用户明确批准后才能对同一已固定 peer 重试一次。拒绝、无决定、无 UI 或再次失败均保持阻断；独立 sidecar 不得放宽 ACL、切换备用目录、自提权或循环重试。

每种语言的验收矩阵固定为七族 36 动作，分布 `file/inspect/xref/grep/structure/patch_edit/completion = 8/5/7/6/5/4/1`。精确 action key 清单见 `bin/LSP/README.md` 的“36 个小动作的精确验收合同”，可执行唯一闭包由 `realMCPExpectedActionKeys` 守卫。结果分别记为 `success`、`legal_empty_success`、能力快照明确允许的 `capability_unsupported` 或 `runtime_failure/NON_PASS`；最后一类永远不能算 PASS。

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
| Windows | x64 | bin/LSP/mcp-lsp-windows-x64.exe |
| Windows | ARM64 | bin/LSP/mcp-lsp-windows-arm64.exe |
| Windows | x86 | bin/LSP/mcp-lsp-windows-x86.exe |

表中的 `mcp-lsp-windows-{x64,arm64,x86}.exe` 是发布包的 canonical 文件名及 `windows/{amd64,arm64,386}` 架构映射，供打包、分发和配置示例选择；它们不是运行时 basename 白名单。运行时接受受信交付布局内的其他 `.exe` 名称（例如重命名或版本化名称），但仍强制校验受信 `bin`/`bin/LSP` 安装根、包内 `lsp` bundle、`lsp-manifest.json` 位置与摘要及可执行文件身份；改名不能绕过这些校验。Windows 资产和依赖仍只由 `NativeArch` 选择，`ProcessArch` 不得替代。

配置 Agent 只选择最终启动 MCP server 的客户端平台对应文件，不把其他平台二进制误配为当前命令：

- macOS Apple Silicon（常见 `uname -m` 为 `arm64`）选择 `mcp-lsp-mac-arm`。
- macOS Intel（`x86_64`）选择 `mcp-lsp-mac-x86`。
- Linux/WSL 的 `x86_64` 选择 `mcp-lsp-linux-x86`，`aarch64`/`arm64` 选择 `mcp-lsp-linux-arm`。
- Windows 原生进程的 AMD64、ARM64、386 分别选择 `mcp-lsp-windows-x64.exe`、`mcp-lsp-windows-arm64.exe`、`mcp-lsp-windows-x86.exe`。

这里选择的是**二进制文件**；`cwd` 和可信根仍是目标项目根。选择完成后应使用 `go version -m`、`file` 或 PE Machine 信息核对真实 GOOS/GOARCH，不能只相信文件名。

## Windows 原生、WSL 原生与 Windows→WSL 桥接

Windows 选型还必须把系统版本/build、操作系统原生架构和当前进程架构分开记录。原生 PowerShell 使用 `[Environment]::OSVersion.Version`、`[Runtime.InteropServices.RuntimeInformation]::OSArchitecture` 和 `ProcessArchitecture`；按 `OSArchitecture` 选择 ARM64/x64/x86 文件，`ProcessArchitecture` 仅用于识别仿真。mcp-lsp 安装器会用 Windows API 复核版本/build 与两种架构，目录没有该原生架构资产或系统版本不满足最低要求时立即失败，不得换用另一个架构。

Windows 宿主的主线交付是对应原生架构的 `mcp-lsp-windows-*.exe`。`wsl.exe` 只在 Windows Desktop/IDE 明确要求 WSL sidecar 时作为 Windows EXE 的启动/桥接边界；它不是与 Windows 主线无关的 Linux-only 旁路。WSL host 原生直连仍使用 Linux ELF。

不配置人工平台开关。先确认 MCP host，再确认用户要求 sidecar 在 Windows 还是 WSL 运行：

| 配置项 | Windows 原生直连 | WSL host 原生直连 | Windows host → WSL sidecar |
|---|---|---|---|
| `command` | `G:/project/bin/LSP/mcp-lsp-windows-x64.exe`（按 `OSArchitecture` 替换为 `mcp-lsp-windows-arm64.exe` 或 `mcp-lsp-windows-x86.exe`） | `/mnt/g/project/bin/LSP/mcp-lsp-linux-x86`（ARM64 使用 `mcp-lsp-linux-arm`） | `C:/Windows/System32/wsl.exe` |
| host `cwd` | `G:/project` | `/mnt/g/project` | `G:/project` |
| sidecar 启动 | command 本身 | command 本身 | args: `--cd /mnt/g/project env KEY=VALUE... PATH=<实机探测的 Linux PATH> ./bin/LSP/mcp-lsp-linux-x86` |
| `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` | `G:/project` | `/mnt/g/project` | args 的 env 段使用 `/mnt/g/project` |
| `GO_AGENT_LSP_ROOT` | `G:/project` | `/mnt/g/project` | args 的 env 段使用 `/mnt/g/project` |
| `GO_AGENT_LSP_ROOTS` | `["G:/project"]` | `["/mnt/g/project"]` | args 的 env 段使用 `["/mnt/g/project"]` |
| Windows gopls bundle | `G:/project/bin/LSP/lsp` 与 `G:/project/bin/LSP/lsp/lsp-manifest.json` 必需 | 不使用 Windows bundle | 不使用；由 WSL 的 Linux LSP 依赖负责 |

Windows ARM64 或 x86 主机分别把文件名替换为 `mcp-lsp-windows-arm64.exe` 或 `mcp-lsp-windows-x86.exe`；WSL ARM64 使用 Linux `*-arm`，其他字段不变。原生 Windows 配置使用正斜杠可以避免 TOML/JSON 反斜杠转义；URI 中的中文、空格和字面 `%` 由新二进制在 URI 到本机路径边界解码一次。不要把 `%E4...` 形式直接写进 cwd 或 roots。

WSL 可以调用 Windows `.exe`，但那仍是 Windows 进程语义。反向桥接也不会自动发生：Windows Desktop 不能直接执行 `/mnt/...` Linux ELF。用户要求 Windows Codex Desktop 使用 WSL sidecar 时，必须显式选择第三列，由 `wsl.exe` 建立边界。完整模板见 `bin/LSP/codex-windows-wsl-lsp-config.example.toml`。

第三列中 Windows `cwd` 和 WSL roots 字符串必然不同，但必须映射到同一目录。用 `wslpath`/`readlink -f` 验证映射，并用配置中的完整 command+args 完成 MCP initialize 和真实 tools/call。只在 WSL shell 中裸跑 ELF 不能证明 Windows host 配置正确。

Windows host 通过 `wsl.exe env` 启动 sidecar 时，不保证加载 WSL 用户的登录 shell PATH。配置前必须在目标发行版中运行 `command -v go gopls node typescript-language-server`，收集实际目录并在 args 的 `env` 段显式写入绝对 Linux PATH。常见的用户级目录是 `/home/<wsl-user>/.local/bin`，但用户名和安装位置必须实测，不能把该示例当默认值，也不能在 TOML args 中写不会展开的字面 `$HOME`。

在 WSL 直连或 Windows→WSL 桥接中，PATH 缺失可能不会让 sidecar 启动失败：能力探测会找不到 Linux 语义语言服务器，`tools/list` 可能只返回 `file`、`grep`。因此桥接验收必须精确核对七个工具，并实际调用至少一个 `structure`、`inspect` 或 `xref` 语义工具；仅 initialize 成功、只读文件成功或看到两个基础工具均不算完整可用。原生 Windows production 不使用这条两工具降级解释：其注册的自动安装器应直接公开七族并记录 `windows_production_auto_installer` 来源。

当前 shell 与目标客户端相同时，PowerShell 用 `$IsWindows`/`$IsLinux` 和 `WSL_INTEROP`、`WSL_DISTRO_NAME` 判断原生 Windows 或 WSL，再读取 `[Runtime.InteropServices.RuntimeInformation]::OSArchitecture` 与 `ProcessArchitecture`；POSIX shell 用 WSL 环境变量或 `/proc/sys/kernel/osrelease` 中的 `microsoft` 判断，并用 `uname -m` 选择 x86-64/ARM64。当前 shell 不是最终 owner 时，以客户端进程平台为准。

### Windows 运行时锁定清单与安全边界

公共配置字段把 `OSArchitecture` 记录为 `NativeArch`，把 `ProcessArchitecture` 记录为 `ProcessArch`。只有 `NativeArch` 选择 Windows EXE 和依赖，`ProcessArch` 仅用于仿真诊断；安装器必须对版本、来源、SHA-256、PE/Appx 身份和原生架构做完整复验，缺失或不匹配立即 fail-fast。

下表是当前生产配方的官方来源和摘要。Node/VCLibs 的三种架构是三个独立锁定资产，不能把 x64、x86 或 ARM64 当作可替代 fallback：

| 组件/架构 | 官方固定版本与来源 | SHA-256/身份要点 |
|---|---|---|
| EmmyLua ARM64 | `0.25.1`；[emmylua_ls-win32-arm64.zip](https://github.com/EmmyLuaLs/emmylua-analyzer-rust/releases/download/0.25.1/emmylua_ls-win32-arm64.zip) | ZIP `f6f335f01fccca6f000a6240fb78c6fbab069230b1bb4347361ef3f64550390a`；内含 `emmylua_ls.exe` `c05a85e354de013e0300c42197592355d425a8ef7fae7ef1eb3febd68c1791ac`；PE Machine 必须为 ARM64；只支持 `NativeArch=ARM64` |
| Node ARM64 | Node `22.22.0`；[node-v22.22.0-win-arm64.zip](https://nodejs.org/dist/v22.22.0/node-v22.22.0-win-arm64.zip) | `5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a` |
| Node x64 | Node `22.22.0`；[node-v22.22.0-win-x64.zip](https://nodejs.org/dist/v22.22.0/node-v22.22.0-win-x64.zip) | `c97fa376d2becdc8863fcd3ca2dd9a83a9f3468ee7ccf7a6d076ec66a645c77a` |
| Node x86 | Node `22.22.0`；[node-v22.22.0-win-x86.zip](https://nodejs.org/dist/v22.22.0/node-v22.22.0-win-x86.zip) | `5d7f6cfc50474cf784027ce9ddabf47a0198ea4b588301ab8675de8c56217247` |
| VCLibs ARM64 | 微软 `Microsoft.VCLibs.140.00.UWPDesktop` `14.0.33321.0`；[Microsoft.VCLibs.arm64.14.00.Desktop.appx](https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.arm64.14.00.Desktop.appx) | `9a7f6d69ea6cf042ea8680b7cd0bfaa9c04f0f6cc89055d43f7f6cd0250508d3`；Appx identity/publisher/架构必须精确匹配 |
| VCLibs x64 | 微软 `Microsoft.VCLibs.140.00.UWPDesktop` `14.0.33321.0`；[Microsoft.VCLibs.x64.14.00.Desktop.appx](https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.x64.14.00.Desktop.appx) | `b56a9101f706f9d95f815f5b7fa6efbac972e86573d378b96a07cff5540c5961`；Appx identity/publisher/架构必须精确匹配 |
| VCLibs x86 | 微软 `Microsoft.VCLibs.140.00.UWPDesktop` `14.0.33321.0`；[Microsoft.VCLibs.x86.14.00.Desktop.appx](https://download.microsoft.com/download/4/7/c/47c6134b-d61f-4024-83bd-b9c9ea951c25/Microsoft.VCLibs.x86.14.00.Desktop.appx) | `a7fb9d76e07b36d868179eb53ffd13740c25242176fa363f154798cf34edd4a9`；Appx identity/publisher/架构必须精确匹配 |
| Terraform CLI ARM64 | `1.15.6`；[terraform_1.15.6_windows_arm64.zip](https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_arm64.zip) | `02820bcae3725c9c4e91deb6656e9b96ca8af9f395fc5faccc0820dd3295d6e0`；解包 `terraform.exe` 的 PE Machine 必须为 ARM64 |
| Terraform CLI x64 | `1.15.6`；[terraform_1.15.6_windows_amd64.zip](https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_amd64.zip) | `56b4d3a157e346f8fc1e94254d0a944e6fec81f58ddd43eb274b8e0ebb56e334`；解包 `terraform.exe` 的 PE Machine 必须为 AMD64 |
| Terraform CLI x86 | `1.15.6`；[terraform_1.15.6_windows_386.zip](https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_386.zip) | `00d51ccf53664f68bd6fb7dfa7edbc7bbff4032ff048787c096d23ece2dcc092`；解包 `terraform.exe` 的 PE Machine 必须为 I386 |
| rustfmt ARM64 | Rust `1.96.0`；[rustfmt-1.96.0-aarch64-pc-windows-msvc.tar.xz](https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-aarch64-pc-windows-msvc.tar.xz) | `d9e403d778e0ad95d814275b1265057478d4cde463d8bf620846056a7f00a59d`；`rustfmt.exe` PE Machine 必须为 ARM64 |
| rustfmt x64 | Rust `1.96.0`；[rustfmt-1.96.0-x86_64-pc-windows-msvc.tar.xz](https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-x86_64-pc-windows-msvc.tar.xz) | `7ae6d141dfb844355c4756a41f39ed45b74ff9295fff86bd0bf9b559a83c5d5d`；`rustfmt.exe` PE Machine 必须为 AMD64 |
| rustfmt x86 | Rust `1.96.0`；[rustfmt-1.96.0-i686-pc-windows-msvc.tar.xz](https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-i686-pc-windows-msvc.tar.xz) | `75a69f518db96b5c46fa4b98d169688e7670c8bff29b7f1831f6dcfdfc6311ab`；`rustfmt.exe` PE Machine 必须为 I386 |

Swift/sourcekit-lsp 的 ARM64 recipe 固定为 Swift `6.3.3` 官方 installer [`swift-6.3.3-RELEASE-windows10-arm64.exe`](https://download.swift.org/swift-6.3.3-release/windows10-arm64/swift-6.3.3-RELEASE/swift-6.3.3-RELEASE-windows10-arm64.exe)，installer SHA-256 为 `09e39c60f0b05d00fbe5f55b2d344752ccbc86e64802a2d896c0d55bc51e243d`，内嵌 CAB stream SHA-512 为 `bcbe4157eaf2c5dc39497d6996bb6e4d181119f719832153a4edf4b4183b1277367e7c372546b4cec9f140edd77ad4a2357459408a44df8b6283c620d70fa3b7`（大小 `1362397722`）。必须在同一 payload 目录保留下列官方文件和 SHA-256；`windows.msi` 会按相邻路径解析它们，不能只抽取 ARM64 CAB，也不能跨版本拼接：

| source | 文件 | SHA-256 |
|---|---|---|
| a0 | `bld.asserts.msi` | `df5221e5236416c439281ab155a92d25d5805cafb7cfc2321d8a154698279762` |
| a2 | `cli.asserts.msi` | `53d08077af6d2725402aa6ff5a6eb747ea059622dfbb563ada35a887259eab47` |
| a9 | `rtl.msi` | `43c6621147bd75f2d582af9e638d03b4e0d1015fb6dc080a8d4cb2765d2ee23d` |
| a10 | `windows.msi` | `e1f2862a901c8f63694170385a5c5274f9e229bad1564d8bef8bbf3684f0ece0` |
| a11 | `bld.asserts.cab` | `64dedf53dbe51ae5cb988714e50a18a2255abf65145c2327fce7afb87389babf` |
| a13 | `cli.asserts.cab` | `5cf49010b810a192bc1ebf8b82ce7148b9327d52c09a92c256821064b7a9a1bf` |
| a20 | `rtl.cab` | `4a712368e4d05fe5fb0047533f5c5c3c4b677f346a4a3cc89ce2c52ad269159b` |
| a21 | `windows.cab` | `e4a296cefe800d7b14eac5dc02bb8d7091c35870dfaa721681109938001d6f9c` |
| a22 | `sdk.windows.arm64.cab` | `126401101c14628b06cc69bd6702083b23316b093ad4fb59becd9679491a4345` |
| a23 | `sdk.windows.x64.cab` | `a62e8ef7fe8cc9bd27052c0baf2ece2b6e18f47e7f1c3281900a912db32b8159` |
| a24 | `sdk.windows.x86.cab` | `962ce69347b83d3b5c18ff4ccd5efd57cab95cb2b5b3da4de1ac6b45c04dd396` |
| a25 | `windows.experimental.cab` | `39bf60e5f445f8d4214f8e5a8c910956107b645ed51fb692db498d66fff46464` |
| a26 | `sdk.windows.experimental.arm64.cab` | `4bb1a56b3d597c7ada56b241ec8b45869833d5f74a81677561158b234d766f5e` |
| a27 | `sdk.windows.experimental.x64.cab` | `26d023290f3323b091cc82170560a28c9d5bb3ea35ef2c9174fd23a304161237` |
| a28 | `sdk.windows.experimental.x86.cab` | `399a6b61e6b81fb745fe1633561601b6a3441a088523c3381b152b3e82ecb9bb` |

Swift 的最终 `swiftc.exe`/`sourcekit-lsp.exe` 和 EmmyLua 的 `emmylua_ls.exe` 都必须在交付前做 PE ARM64 复核。为规避 `MAX_PATH`，8.3 short path 只准进入 installer/Node 子进程命令行及其临时环境；cache、manifest、日志、receipt、roots 和授权身份一律使用 canonical long path，短名不得持久化或参与身份比较。配置示例中的 TOML/PowerShell 注释不授予 ACL、不改变架构、不代表已经获得用户授权；Win32 `5`/`1314` 必须保持 `authorization_required`，由宿主审批 UI 处理，sidecar 不自提权、不放宽 ACL、不换备用目录。

Kotlin/IntelliJ Windows ARM64 bundle 还必须使用产品私有的物理扁平 short root，供 rocksdb/JNI 和 server 加载；禁止以 `subst`、8.3、junction 或系统公共目录替代。该 root 必须通过 ACL、reparse、架构和摘要校验，canonical root 继续用于 cache、manifest、receipt 和授权身份。容量预检不足或受控 tar 解包返回空间耗尽时，收据必须是 `disk_space_exhausted`/`runtime_failure`，不能记为 `legal_empty`、`capability_unsupported` 或成功。

远程 6b7 的结果验收只使用 content 纯文本行（`OK`/`ERROR`/`ATTR`/`ROW`/`HINT`/`WARNING`）；`StructuredContent` 必须为空且不得 fallback。文本协议错误、空响应或子进程失败必须保持 `runtime_failure/NON_PASS`，只有动作合同明确允许的文本空结果才能单列为 `legal_empty`。

以上清单仅约束 Windows 原生 EXE 生产路径。非 Windows 行为保持不变：macOS、Linux 和 WSL 原生仍使用既有 Mach-O/ELF 二进制和依赖选择，不读取 Windows 资产；Windows 正式生命周期仍为至少 `15m`，14 分钟存活、15 分钟回收及 PID+start identity 零残留才是长测证据，`mcp_lsp_short_idle_precheck` 只能算快速预检。

## Go 自动工具链

保留用户已有的 `GOTOOLCHAIN=auto` 或 `<name>+auto`。mcp-lsp 在已解析的 module/go.work 目录中运行 PATH 候选 `go version`，让 Go 根据当前 `go.mod`/`go.work` 选择 PATH 中或可下载的工具链。不要因为本机 PATH 默认 Go 版本较旧，就把某个 `GOTOOLCHAIN=go1.x.y` 补丁版本硬编码进可共享示例。显式 `GOTOOLCHAIN=local` 会禁用自动切换，版本不足时应继续 fail-fast。

## 启动错误对照

| 错误 | 必须检查 |
|---|---|
| `sidecar requires SUPER_DOLPHIN_RUNTIME_MODE and SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` | 当前 server 的 env 是否显式包含前两个字段，资源根是否为绝对路径 |
| `SUPER_DOLPHIN_DEPENDENCY_PROFILE is required for production bootstrap` | 当前 server 的 env 是否包含 `SUPER_DOLPHIN_DEPENDENCY_PROFILE=production` |
| `path_outside_workspace` 且路径含 `%E4...`/`%20` | 是否运行新二进制；command、cwd、roots 是否属于同一 Windows 或 WSL 路径体系 |
| Windows Codex 启动 `/mnt/.../mcp-lsp-linux-*` 失败 | Windows `CreateProcess` 不会自动进入 WSL；改用 `command=wsl.exe`，并在 args 中传 `--cd`、`env`、Linux roots 和 ELF |
| WSL/Windows→WSL sidecar 能启动但 `tools/list` 只有 `file`、`grep` | 用配置的完整桥接命令检查 `command -v gopls`、`command -v typescript-language-server`；把实机探测的 Linux PATH 显式注入 args 的 `env` 段后重启宿主 |
| 原生 Windows production 的 `tools/list` 只有 `file`、`grep` | 检查当前二进制是否为匹配 NativeArch 的 Windows EXE、三个 `SUPER_DOLPHIN_*` 字段是否完整，以及日志中为何没有 `source=windows_production_auto_installer`；这是门控/安装器注册错误，不能用 PATH fallback 掩盖 |
| `Go toolchain selection failed` | PATH 是否有可运行的 Go；`GOTOOLCHAIN` 是否允许自动切换；module/go.work 声明是否可满足 |
| `MCP_LSP_IDLE_TIMEOUT must be at least 15m` | 删除短周期覆盖或改为至少 `15m`；带测试 build tag 的短周期只能记为 precheck，不能记为交付 PASS |
| `authorization_required` 且 Windows error 为 `5`/`1314` | 桌面宿主通过 `ApprovalRequester` 发布真实授权提示；仅在用户明确批准后对同一已固定 peer 重试一次。拒绝、无决定、无 UI 或再次失败都记录 blocker；禁止循环重试、改 ACL、换备用目录或声称独立 sidecar 已提权 |
| Antigravity 已显示工具，但调用时报 `client is closing: EOF` 或 `Transport closed` | 对比 `.agents/mcp_config.json`、`.agents/plugins/*/mcp_config.json`、workspace `.gemini/config/mcp_config.json` 和只读全局配置；从 sidecar 日志确认实际 command、root、请求路径和第一个错误 |
| `resolve parent path: lstat /parent/cmd: no such file or directory` | 若 trusted root 是仓库上一级，请求必须包含仓库目录前缀，例如 `repo/cmd/...`；不要把按项目根书写的 `cmd/...` 直接交给父级 root |
| sidecar 日志先有 `tools/call done`，随后 `mcp stdio: read failed EOF` | 调用曾经成功，EOF 是 stdin 关闭结果；检查上游取消、重复 server 和旧 client，不得判为二进制崩溃 |
| 修改配置后仍持续 `client is closing` | 完整退出并重启实际拥有 stdio 管道的 Codex、Claude Code、Antigravity 或其他宿主进程，再 Refresh/检查；旧 stdio client 无法原地恢复 |
