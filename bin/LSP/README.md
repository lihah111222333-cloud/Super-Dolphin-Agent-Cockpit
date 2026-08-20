# mcp-lsp 跨平台包使用说明

本目录提供 macOS、Linux 的既有 x86-64/ARM64 二进制，以及文件名中直接标明 x86、x64、ARM64 的 Windows mcp-lsp 二进制、AGENTS.md 样板和指导 Agent 为 Codex、Claude Code、Google Antigravity 配置项目级 MCP 的技能。Windows 命名调整不改变 macOS、Linux 或 WSL 的既有交付名与选择逻辑。

技能不携带固定配置脚本。Agent 会根据当前系统、项目目录、已有配置和客户端版本自行选择二进制并精确编辑。

## 必需启动契约

`mcp-lsp` 是 fail-fast 的独立 sidecar。Codex、Claude Code 和 Antigravity 的每一个本地 stdio server 配置都必须在该 server 的 `env` 中显式提供以下三个字段：

| 环境变量 | 独立 dev 二进制的值 | 说明 |
|---|---|---|
| `SUPER_DOLPHIN_RUNTIME_MODE` | `dev` | 只允许 `dev` 或发行包使用的 `packaged` |
| `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR` | 本分发包的资源根绝对路径；源码构建则为 checkout 根 | dev 下是运行时资源根，同时用于建立 `PROJECT_ROOT`；不能从目标文件或当前目录猜测 |
| `SUPER_DOLPHIN_DEPENDENCY_PROFILE` | `production` | 独立 sidecar 使用生产依赖装配；`desktop_host` 只属于桌面 owner 开发启动器 |

缺少前两个字段会报 `sidecar requires SUPER_DOLPHIN_RUNTIME_MODE and SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR`；补齐后若缺第三个字段，会报 `SUPER_DOLPHIN_DEPENDENCY_PROFILE is required for production bootstrap`。这些错误不能靠代码默认值绕过。

原生 Windows 的 `production` profile 会按 Windows API 检出的版本/build、`NativeArch` 和 `ProcessArch` 装配本机自动安装器；即使 PATH 为空且没有额外 LSP bundle，`tools/list` 仍必须精确公开 `file`、`inspect`、`xref`、`grep`、`structure`、`patch_edit`、`completion` 七个工具族，并记录 `source=windows_production_auto_installer`。这只证明工具入口可按需安装，不证明某个语言已经安装或 36 个动作已通过；必须继续实际调用该语言的语义动作。非 Windows 与 Windows→WSL sidecar 保持原有 bundle/PATH 探测语义，不读取 Windows 安装器。

### 36 个小动作的精确验收合同

每种受支持语言都必须逐一执行同一组 36 个动作，工具分布固定为 `file/inspect/xref/grep/structure/patch_edit/completion = 8/5/7/6/5/4/1`：

- `file`：`open_file`、`read_file-single`、`read_file-full`、`read_file-batch`、`read_file-lines`、`read_file-function`、`diagnostics`、`diagnostics-batch`。
- `inspect`：`hover`、`definition`、`implementation`、`type_definition`、`signature_help`。
- `xref`：`references`、`references-no-declaration`、`call_hierarchy-incoming`、`call_hierarchy-outgoing`、`call_hierarchy-both`、`type_hierarchy-supertypes`、`type_hierarchy-subtypes`。
- `grep`：`text_search`、`text_search-regex`、`text_search-paths`、`text_search-file_paths`、`text_search-glob`、`ast_search`。
- `structure`：`document_symbol`、`workspace_symbol-file`、`workspace_symbol-language`、`folding_range`、`semantic_tokens`。
- `patch_edit`：`replace_range`、`rename`、`code_action`、`format`。
- `completion`：`completion`。

每个动作只能归档为 `success`、`legal_empty_success`、`capability_unsupported` 或 `runtime_failure/NON_PASS`。只有服务器初始化/动态注册的能力快照明确不支持该动作时，才允许 `capability_unsupported`；合法空结果必须由动作合同明确允许，运行失败绝不能算成功。语言级 PASS 还必须同时满足自动安装来源、36 键唯一闭包、至少 15 分钟生命周期以及 PID/启动身份对应的进程树零残留。

## 生命周期与 Windows 授权政策

### Windows 私有 Terraform/rustfmt companion 锁定边界

Windows `NativeArch` 还必须选择产品私有的 Terraform CLI `1.15.6` 与 Rust
`1.96.0` `rustfmt-preview` companion；以下官方资产是唯一允许来源，SHA-256
为归档摘要，不能用 PATH、用户缓存或另一架构代替：

| NativeArch | Terraform CLI 1.15.6 | rustfmt Rust 1.96.0 (MSVC) |
|---|---|---|
| ARM64 | `https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_arm64.zip` / `02820bcae3725c9c4e91deb6656e9b96ca8af9f395fc5faccc0820dd3295d6e0` | `https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-aarch64-pc-windows-msvc.tar.xz` / `d9e403d778e0ad95d814275b1265057478d4cde463d8bf620846056a7f00a59d` |
| x64 | `https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_amd64.zip` / `56b4d3a157e346f8fc1e94254d0a944e6fec81f58ddd43eb274b8e0ebb56e334` | `https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-x86_64-pc-windows-msvc.tar.xz` / `7ae6d141dfb844355c4756a41f39ed45b74ff9295fff86bd0bf9b559a83c5d5d` |
| x86 | `https://releases.hashicorp.com/terraform/1.15.6/terraform_1.15.6_windows_386.zip` / `00d51ccf53664f68bd6fb7dfa7edbc7bbff4032ff048787c096d23ece2dcc092` | `https://static.rust-lang.org/dist/2026-05-28/rustfmt-1.96.0-i686-pc-windows-msvc.tar.xz` / `75a69f518db96b5c46fa4b98d169688e7670c8bff29b7f1831f6dcfdfc6311ab` |

安装器必须锁定 URL、归档摘要、解包后的 `terraform.exe`/`rustfmt.exe` SHA、PE
Machine、产品根 ACL 与 reparse 状态；不匹配即 fail-fast。`terraform-ls` 和
`rust-analyzer` 只向产品自有子进程注入同一架构 companion 目录的 PATH，外部
配置不改写。非 Windows 不读取这些资产，且禁止跨架构 fallback。下载/安装证明
必须使用空私有根并记录 HTTP、URL、SHA 和安装 receipt；正确性 E2E 则使用已锁定
cohort 的 `cache_only=true`、`HTTP=0`，验证 ready/hash/架构、36 动作和生命周期。
本地锁定包存在时不得重复下载；setup/import receipt 不能冒充自动下载证明。
公共说明：这些是 Windows 生产依赖的审计规则，macOS/Linux/WSL 的既有依赖选择
与行为保持不变。远程 `6b7ed46f3` 之后工具结果只看 content 纯文本行，
`StructuredContent` 必须为空且不可 fallback。

正式 mcp-lsp 的 LSP 空闲生命周期默认且最低为 15 分钟。`MCP_LSP_IDLE_TIMEOUT` 或旧别名 `MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT` 显式配置为小于 `15m` 时启动立即失败，不会静默改值。小于 15 分钟的回收只允许存在于带 `mcp_lsp_short_idle_precheck` 测试 build tag 的快速预检二进制中，不能作为交付、发布或生命周期通过证据。正式 Windows E2E 在 14 分钟验证进程仍存活，达到 15 分钟后才允许回收，并按 PID 与启动身份验证整棵进程树零残留。

Windows 文件或目录操作返回 Win32 `5`（access denied）或 `1314`（privilege not held）时，sidecar 不得改用宽松 ACL、备用目录或跨用户缓存。工具调用先返回结构化 `authorization_required`；桌面宿主必须通过 `ApprovalRequester` 发布真实授权提示，且只有用户明确批准后，才对同一个已固定 peer 精确重试一次。拒绝、无决定、无审批 UI 或单次重试仍失败时都保持阻断，禁止循环重试。独立 stdio sidecar 没有审批桥，不会伪造弹窗或自行提权。日志必须保留已脱敏路径、Windows 错误码、权限类型、审批结果和单次重试结果，供根因诊断。

Windows 原生版共享 gopls 时还必须显式提供受信 bundle：

| 环境变量 | `bin/LSP` 交付布局的值 |
|---|---|
| `SUPER_DOLPHIN_LSP_BUNDLE_DIR` | `<项目根>/bin/LSP/lsp` 的绝对路径 |
| `SUPER_DOLPHIN_LSP_MANIFEST` | `<项目根>/bin/LSP/lsp/lsp-manifest.json` 的绝对路径 |

这两个字段只绑定包内 gopls 身份，不定义可信 workspace；workspace 仍只由外部 `GO_AGENT_LSP_ROOT(S)` 配置决定。缺少 bundle、manifest、摘要或原生 `gopls.exe` 时 Windows Go LSP 必须 fail-fast，不能退回 PATH gopls。bundle 只为命中的 adapter 提供受信首选二进制，绝不能作为通用语言白名单；未写入 manifest 的其他已支持语言仍必须注册，并在首次语义调用时自动发现或安装自己的语言服务器。

项目作用域还应显式提供 `GO_AGENT_LSP_ROOT` 和合法 JSON 数组形式的 `GO_AGENT_LSP_ROOTS`。Windows 配置优先使用正斜杠路径，避免在 JSON 字符串中手工转义反斜杠。

## 目录内容

| 文件或目录 | 用途 |
|---|---|
| mcp-lsp-mac-x86 | macOS Intel |
| mcp-lsp-mac-arm | macOS Apple Silicon |
| mcp-lsp-linux-x86 | Linux x86-64 |
| mcp-lsp-linux-arm | Linux ARM64 |
| mcp-lsp-windows-x64.exe | Windows x64 |
| mcp-lsp-windows-arm64.exe | Windows ARM64 |
| mcp-lsp-windows-x86.exe | Windows x86 |
| lsp/lsp-manifest.json、lsp/bin/gopls.exe | Windows 共享 gopls 的受信 bundle 与原生二进制 |
| AGENTS.md | LSP 导航、影响分析、诊断和验证规则样板 |
| codex-lsp-config.example.toml | 从本项目 `.codex/config.toml` 提取的 Codex LSP 配置示例，含平台二进制、默认 cwd、可信根和相对工具路径注释 |
| codex-windows-wsl-lsp-config.example.toml | Windows Codex Desktop/IDE 通过 `wsl.exe` 启动 WSL2 Linux sidecar 的项目级配置模板 |
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
在 Windows 上分别判断 MCP host 与 sidecar 目标：Windows 原生直连选择 Windows `.exe`；WSL host 直连选择 Linux 二进制；Windows Desktop 明确要求 WSL sidecar 时用 `wsl.exe` 桥接，Windows `cwd` 与 args 内的 `/mnt/...` roots 分层配置。
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
2. 识别 macOS/Linux/Windows 和 ARM64/x64/x86；Windows 还要区分原生 PowerShell 与 WSL。
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
| Windows x64 | bin/LSP/mcp-lsp-windows-x64.exe | windows/amd64 |
| Windows ARM64 | bin/LSP/mcp-lsp-windows-arm64.exe | windows/arm64 |
| Windows x86 | bin/LSP/mcp-lsp-windows-x86.exe | windows/386 |

上表中的 `mcp-lsp-windows-{x64,arm64,x86}.exe` 是发布包的 canonical 文件名及 `windows/{amd64,arm64,386}` 架构映射，供打包、分发和配置示例选择；它们不是运行时 basename 白名单。运行时接受受信交付布局内的其他 `.exe` 名称（例如重命名或版本化名称），但仍强制校验受信 `bin`/`bin/LSP` 安装根、包内 `lsp` bundle、`lsp-manifest.json` 位置与摘要及可执行文件身份；改名不能绕过这些校验。Windows 资产和依赖仍只由 `NativeArch` 选择，`ProcessArch` 不得替代。

不要只看文件名。优先运行：

~~~bash
go version -m bin/LSP/mcp-lsp-mac-arm
~~~

确认输出中的 build GOOS 和 GOARCH。macOS/Linux 还可用 file 交叉检查；Windows 检查 PE Machine。

Windows 还必须同时记录真实系统版本/build、操作系统原生架构和当前进程架构；不能只看 `PROCESSOR_ARCHITECTURE`，因为仿真进程的架构可能不同于系统原生架构。原生 PowerShell 可直接读取：

~~~powershell
[PSCustomObject]@{
  OSVersion = [Environment]::OSVersion.Version.ToString()
  OSArchitecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
  ProcessArchitecture = [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()
}
~~~

用 `OSArchitecture` 选择 `windows-arm64`、`windows-x64` 或 `windows-x86`，再用 `ProcessArchitecture` 记录是否存在仿真；不得因当前进程是 x64 就给 ARM64 Windows 选择 x64 交付物。mcp-lsp 的 Windows 安装器会用 Windows API 再次检测版本/build、原生架构和进程架构，低于目录要求或没有原生资产时 fail-fast，不做跨架构回退。

### Windows 原生架构与锁定运行时组件

公共配置和日志统一把 `OSArchitecture` 归一为 `NativeArch`，把 `ProcessArchitecture` 归一为 `ProcessArch`：只有 `NativeArch`（Windows 的真实系统架构）可以选择交付物和依赖，`ProcessArch` 只用于记录仿真/诊断，绝不能触发 x64/x86/ARM64 回退。以下 Windows 依赖是已核对的官方锁定事实，版本、来源、摘要和 PE/Appx 身份必须由安装器复验：

| 组件 | Windows 锁定事实 |
|---|---|
| EmmyLua | 官方 `EmmyLuaLs/emmylua-analyzer-rust` `0.25.1`，`emmylua_ls-win32-arm64.zip`；归档 SHA-256 `f6f335f01fccca6f000a6240fb78c6fbab069230b1bb4347361ef3f64550390a`，内含 `emmylua_ls.exe` SHA-256 `c05a85e354de013e0300c42197592355d425a8ef7fae7ef1eb3febd68c1791ac`，PE Machine 必须为 ARM64。该官方包只支持 Windows `NativeArch=ARM64`。 |
| Swift/sourcekit-lsp | Swift `6.3.3` 官方 Windows ARM64 installer；安装器 SHA-256 `09e39c60f0b05d00fbe5f55b2d344752ccbc86e64802a2d896c0d55bc51e243d`。`windows.msi`、`windows.cab` 和 `a22`–`a28`（包括 `sdk.windows.arm64/x64/x86.cab` 及 experimental sibling CAB）必须从同一官方 installer 提取到同一目录，不能只保留 ARM64 CAB 或跨版本拼接；最终 `sourcekit-lsp.exe` 必须是 ARM64。 |
| Node | 官方 Node `22.22.0` 的 `win-arm64`、`win-x64`、`win-x86` ZIP 各自独立锁定；按 `NativeArch` 选唯一归档、SHA-256 和 `node.exe`，不读 PATH、不跨架构回退。 |
| VCLibs Desktop | 微软官方 `Microsoft.VCLibs.140.00.UWPDesktop` `14.0.33321.0` Appx 的 `arm64`、`x64`、`x86` 包各自独立锁定；按 `NativeArch` 复验 Appx identity、版本、publisher、架构和 SHA-256，不把另一架构 DLL 当兼容运行时。 |

为规避 Windows `MAX_PATH`，8.3 short path 只允许作为 installer/Node 子进程的临时命令行或子进程环境边界；完整版本/架构/SHA 路径、manifest、缓存身份、日志、receipt、workspace roots 和安全审计必须保留 canonical long path，不能把短名持久化或用于身份比较。上述资产规则只适用于 Windows 原生 EXE；非 Windows 行为保持不变，macOS、Linux 和 WSL 原生仍按既有 Mach-O/ELF 选择，不能读取或安装 Windows 依赖。

Kotlin/IntelliJ 运行时是例外的物理进程边界：Windows ARM64 的产品私有 Kotlin bundle 必须发布到经过架构、摘要、ACL 和 reparse 校验的扁平短 root，供 rocksdb/JNI 与 server 进程加载；不能用 `subst`、8.3 名称、junction 或系统公共目录伪造短路径。缓存、manifest、receipt 和授权身份仍使用 canonical root。下载/解包前若容量预检不足，或受控解包明确返回磁盘耗尽，必须归档为 `disk_space_exhausted`/`runtime_failure` 并 fail-fast，不能改记为 `legal_empty`、`capability_unsupported` 或成功。

远程 6b7 的工具结果以 content 纯文本行协议为唯一事实源（`OK`/`ERROR`/`ATTR`/`ROW`/`HINT`/`WARNING`）；`StructuredContent` 必须为空，测试和验收不得读取它或在 content 缺失时 fallback。只有明确动作合同允许的文本才可记为合法空结果，协议错误、空响应和 server 崩溃仍是 `runtime_failure/NON_PASS`。

注释和公共说明只是配置语义与安全边界的解释，不是权限授予：TOML/脚本注释不能改变 `NativeArch`、授权 ACL、缓存根或执行身份。Win32 `5`/`1314` 必须返回结构化 `authorization_required`，交给有审批 UI 的宿主；sidecar 不得自提权、放宽 ACL、换备用目录或把阻断写成成功。

### Windows EXE 主线：原生 PowerShell 与 Windows→WSL 桥接

Windows 宿主的主线交付是相应原生架构的 `mcp-lsp-windows-*.exe`。`wsl.exe` 只在 Windows Desktop/IDE 明确要求 sidecar 在 WSL 运行时作为 Windows EXE 的启动/桥接边界；它不是绕开 Windows EXE 的 Linux-only 旁路。WSL host 原生直连仍选择 Linux ELF。不要增加 `POWER_SHELL_OR_WSL` 一类人工开关，选择依据是启动 MCP server 的实际执行环境和二进制构建目标：

| 启动环境 | 二进制 | 项目路径示例 | URI/路径语义 |
|---|---|---|---|
| Windows 原生 PowerShell/Desktop 直连 | `mcp-lsp-windows-x64.exe`、`mcp-lsp-windows-arm64.exe` 或 `mcp-lsp-windows-x86.exe` | `G:/develop/中转/new-api-main` | Windows drive/UNC |
| WSL shell / WSL 客户端进程直连 | `mcp-lsp-linux-x86` 或 `mcp-lsp-linux-arm` | `/mnt/g/develop/中转/new-api-main` | Linux 绝对路径 |
| Windows Desktop → WSL2 sidecar | `wsl.exe` 桥接 `mcp-lsp-linux-x86` 或 `mcp-lsp-linux-arm` | host cwd 为 Windows 路径；sidecar roots 为对应 `/mnt/...` 路径 | 以 `wsl.exe --cd ... env ...` 建立显式边界，并注入实机探测的 Linux PATH |

WSL 能调用 Windows `.exe` 不代表两套路径可以混用；Windows 也不会自动执行 `/mnt/...` Linux ELF。桥接模式必须显式使用 `wsl.exe`，其完整模板见 `codex-windows-wsl-lsp-config.example.toml`。`mcp-lsp` 会按实际二进制的 GOOS 使用对应语义，不自行猜测路径转换。

桥接前还必须在目标 WSL 发行版内用 `command -v` 定位 `go`、`gopls`、`node`、`typescript-language-server`，并把这些目录组成绝对 Linux PATH 写入模板 args。Windows Codex 启动的非登录 WSL 进程可能看不到 `/home/<user>/.local/bin`；此时 sidecar 仍可能启动，但只暴露 `file`、`grep`，不能判为完整可用。

当前 shell 就是 MCP host 时，可自动探测而无需额外开关：原生 PowerShell 以 `$PSVersionTable.PSEdition -eq "Desktop"` 或 `$IsWindows` 为真作为 Windows host 证据；PowerShell 运行在 WSL 时 `$IsLinux` 为真且通常存在 `WSL_INTEROP`/`WSL_DISTRO_NAME`。在 POSIX shell 中检查上述 WSL 变量，或检查 `/proc/sys/kernel/osrelease` 是否包含 `microsoft`；架构分别读取 `$env:PROCESSOR_ARCHITECTURE` 或 `uname -m`。Windows host 检出后仍要确认用户要求的是 Windows sidecar 还是 WSL sidecar，再选择直连或桥接。

## 验证不能停在 enabled

客户端列表显示 lsp enabled 只证明配置被发现。还要实际调用：

1. file 读取源码。
2. structure 查看符号。
3. inspect 跳转定义。
4. xref 查询引用或调用层级。
5. file diagnostics 获取诊断。

首次调用具体语言能力时，mcp-lsp 可能按需自动安装并启动对应语言服务器。原生 Windows production 验证必须确认 `tools/list` 精确为七族；专门清空 PATH 与 bundle 的自动安装门控测试还必须命中 `source=windows_production_auto_installer`。只看到 `file`、`grep` 是门控或 profile 异常，不是正常降级。依赖缺失、原生架构无资产或初始化失败必须返回明确错误，不能当成 PASS。

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
