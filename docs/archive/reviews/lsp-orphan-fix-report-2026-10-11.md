# LSP 父进程死亡后的孤儿进程排查与修复

日期：2026-10-11（Asia/Seoul）

## 结论

父进程死亡后，操作系统会把存活的子进程交给 init/launchd；它不会自动替应用结束这些进程。本次确认 `mcp-lsp` 的退出路径存在三处缺陷：协议输出阻塞导致退出计时器根本不启动、监管进程把工作目录删除作为额外必要条件，以及同步退出日志阻塞清理。已经在源码中修复，并用真实二进制和管道复现验证。

根据后续“修复”和“去掉强制签名要求”的指令，已将签名回执改为可选，并原子替换本机 `bin/LSP/mcp-lsp-mac-arm`，安装后的真实 Codex 强杀及 stdout 阻塞复测通过。旧文件已备份；已启动的旧进程需要重新启动才使用新版。仓库统一冻结门禁过期，Go 单元测试没有进入执行阶段，本报告不代表完整门禁通过。

初始进程快照中，现有 LSP 进程均有存活的父进程；没有把这些进程直接判定为孤儿。后续通过独立测试父进程、真实 MCP 请求、SIGKILL 和保留管道端点复现了故障，没有终止用户原有进程。

## 根因与修改

### 1. stdio 阻塞可以绕过原来的退出时限

`common.Server.Run` 同步写协议响应；stdout 管道写满后，该调用可能无法响应上下文取消。原来的 30 秒强退计时器只在 `server.Run` 返回后启动，因此 watchdog 虽然发现孤儿并触发 RunGroup 取消，RunGroup 仍会等着阻塞的 stdio runner，强退计时器永远没有启动。

修改 `cmd/mcp-lsp/runtime_lifecycle.go`：在进入服务前通过 `context.AfterFunc` 监听取消；取消或服务返回都会启动一次退出时限，覆盖协议写入及 manager 清理。强退直接使用退出码 1，不先写可能阻塞的 stderr。正常 EOF 仍能以退出码 0 完成。

### 2. 监管进程错误依赖工作目录被删除

Unix 监管进程的定时判定原来要求 `PPID <= 1` 和工作目录不存在同时成立。如果父进程死了，但工作目录还在、控制管道写端仍由其他进程持有，监管进程既收不到 EOF，也不执行组回收。

修改 `cmd/mcp-lsp/internal/hiddenexec/process_supervisor_darwin_linux_platform.go`：父进程已归 init/launchd 时，即启动监管根自己的独立 session/PGID 回收，不再依赖目录删除。先发 TERM，2 秒后对本监管进程组发 KILL。移除已经无消费者的私有 `workingDir` 字段；构造函数仍用传入目录启动语言服务器，未改变外部接口。

### 3. 退出日志也能阻塞退出

原 watchdog 在返回错误、触发取消之前同步写错误日志。监管进程在设置强退计时器及执行 KILL 之前也同步写 stderr。当继承的 stderr 管道写满且无人读取时，清理流程会卡在日志写入。

修改 `cmd/mcp-lsp/orphan_watchdog.go` 和监管实现：孤儿判断直接返回错误；关键 TERM/KILL 和硬退出路径不再等待继承的 stderr。watchdog 默认检查间隔从 30 秒缩短到 1 秒，并记录原父 PID，检测父 PID 变化。

## 真实进程验证

测试工具：`cmd/mcp-lsp/testdata/orphan_shutdown_repro.py`。

每个父进程死亡用例均保留工作目录、stdin/stdout 端点和控制管道写端。stdout 用例通过真实 `initialize`、`tools/list` 后持续发送请求填满响应管道；stderr 用例填满实际继承管道。监管用例启动忽略 TERM 的 shell 与子进程，并检查整个被监管 PGID 已无存活成员。测试脚本只清理由自身创建且核验身份的进程。

| 场景 | 修复前证据 | 最终候选结果 |
| --- | --- | --- |
| 父进程存活、正常 EOF | 用于检查误杀及正常退出 | 持续响应 ping，EOF 退出码 0 |
| 父进程死亡、普通空闲服务 | 原源码约 30.027 秒后退出 | 1.038 秒退出 |
| 父进程死亡、stdout 写满 | 原源码及实际旧二进制在 35 秒后仍存活，PPID=1；watchdog 已记录检测结果 | 30.556 秒退出 |
| 父进程死亡、stderr 写满 | 移除日志阻塞前的中间候选在 35 秒后仍存活，PPID=1 | 31.005 秒退出 |
| 监管父进程死亡、目录存在、控制写端保留、子进程忽略 TERM | 原实现 6 秒后仍存活，PPID=1 | 2.324 秒清理整个监管组 |
| 同上且 stderr 写满 | 移除日志阻塞前的中间候选 6 秒后仍存活，PPID=1 | 2.328 秒清理整个监管组 |

六个最终用例均退出码 0。时间为本机本次实测值，并非跨平台时延承诺。测试结束后未发现残留测试进程。

## 静态验证与门禁边界

通过：

- macOS arm64 原生构建，以及 Linux amd64、Windows amd64 交叉编译，均退出码 0。
- `env GOMAXPROCS=2 go vet -p 1 ./cmd/mcp-lsp ./cmd/mcp-lsp/internal/hiddenexec`，退出码 0。
- 通过最终候选的真实 MCP `diagnostics` 检查五个变更 Go 文件：`OK total=0`，`isError=false`。
- `make project-map-refresh` 和 `make project-map-check`，退出码 0，生成物漂移检查通过。
- 保留已有 CRLF 文件的行尾；以 `cr-at-eol` 配置执行 scoped `git diff --check` 通过。

未通过或未执行：

- 正确的分类测试入口 `./scripts/test_with_guard.sh --host-test light ./cmd/mcp-lsp -run '^TestStdioRunnerCancellationStartsHardExitBeforeServerReturns$' -count=1 -timeout=120s` 返回 1，报错 `guard freeze <json> invalid acceptance: acceptance.review_by has expired`。此失败发生在测试执行前；新增取消阻塞回归测试及监管判定单测均未声称执行通过。没有绕过冻结检查。
- 既有 `scripts/lsp_diagnostics_gate` 采集器调用已经移除的 `file` 工具，报错 `unknown tool: file`。上述五文件诊断成功来自直接调用当前 `diagnostics` 工具，不能当作旧采集器通过。
- Linux/Windows 只完成编译，未执行这些平台的进程死亡用例；未执行全量 Go 测试或 race 门禁。
- 未提交、推送或重启用户会话。二进制已在最终阶段替换，见下面的安装记录；仓库原有其他源码变更保持原状。

## 候选与复现方式

源码基准 HEAD：`f874c0f64db83f62569ae7f8f24d7e9ca5a53aa6`（构建时工作区存在未提交修改）。下面的二进制哈希用于精确区分测试对象。

| 测试对象 | SHA-256 |
| --- | --- |
| 安装前旧工具，现已备份 | `2ab89f0fd4ecfe37f0d4bcd5bf138e3288574b2d0e6ef84996942076eb9124ca` |
| 签名改为可选前的生命周期候选 `/tmp/super-dolphin-lsp-orphan-candidate` | `e83ca85bd0d418a3becbbdd0a9de3adda4bdbca3a20a46e89a2df76e310fe65b` |
| 首次安装的签名可选版本，与 `/tmp/super-dolphin-lsp-orphan-unsigned-candidate` 完全相同 | `67f5965202dd9f237ecec137b5773cd1fbfb48b255b9f092d7f527eeea10a512` |
| 当前已安装版本，从隔离后的本次提交源码构建 | `43c7304e8f979fcfe4938a7fac6e69cdc4cc84e7c639b3804c6e733ea3c5e2ed` |

从仓库根目录构建并运行：

```bash
env GOMAXPROCS=2 go build -p 1 -o /tmp/super-dolphin-lsp-orphan-unsigned-candidate ./cmd/mcp-lsp
python3 cmd/mcp-lsp/testdata/orphan_shutdown_repro.py --binary /tmp/super-dolphin-lsp-orphan-unsigned-candidate --root "$PWD" --case healthy --timeout 40
python3 cmd/mcp-lsp/testdata/orphan_shutdown_repro.py --binary /tmp/super-dolphin-lsp-orphan-unsigned-candidate --root "$PWD" --case backpressure --timeout 40
```

其他 case：`idle`、`supervisor`、`stderr-backpressure`、`supervisor-stderr`。脚本现在默认不配置签名，也不生成密钥；显式传入 `--signed-receipts` 才在私有临时目录创建测试密钥和日志。上面签名改为可选前的历史候选要求签名，使用现在的脚本复现它时需加此选项，不使用生产密钥。

完整 Go 门禁仍被冻结验收记录过期阻断。安装已完成，当前会话中已运行的旧进程不会因磁盘文件替换而自动升级。

## 补测：实际 Codex 和真实语言服务器的父进程死亡

收到“只要 Codex 或其他父进程死掉，就不会自动回收”的现场补充后，先执行父进程 SIGKILL 对照。以下历史对照使用旧工具和签名改为可选前的生命周期候选，当时生产工具尚未替换。

### 实际 Codex app-server

使用当前桌面应用内的实际 Codex CLI，启动独立的 `app-server --listen stdio://`，通过真实 `initialize`、临时 `thread/start`（`ephemeral=true`）和 `mcpServerStatus/list` 完成 LSP 连接。核验 LSP 是该测试 Codex 的直接子进程后，仅向测试 Codex 发 SIGKILL；工作目录继续存在，不持有 Codex 与 LSP 之间的管道端点。不执行模型 turn，不终止当前用户会话。

| 对象 | 实际父子 PID | Codex 退出码 | LSP 退出耗时 | 结果 |
| --- | --- | --- | --- | --- |
| 当前旧工具 | Codex 61628 → LSP 61705 | -9（SIGKILL） | 0.018 秒 | 退出，无 LSP 残留 |
| 修复候选 | Codex 61629 → LSP 61701 | -9（SIGKILL） | 0.017 秒 | 退出，无 LSP 残留 |

这组实测表明：管道正常关闭时，当前旧工具也可以退出。发现的缺陷需要管道端点仍被持有或写入阻塞等条件才能触发，不能把所有父进程死亡都描述为永久泄漏。该组验证针对 LSP 的直接 Codex app-server 父进程，没有杀死当前桌面 UI。

可复用脚本：`cmd/mcp-lsp/testdata/codex_parent_death_repro.py`。它按测试父子关系记录 PID、启动身份和组身份，仅清理由自身创建且身份仍匹配的进程组。

```bash
python3 cmd/mcp-lsp/testdata/codex_parent_death_repro.py --codex '/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex' --binary /tmp/super-dolphin-lsp-orphan-unsigned-candidate --root "$PWD" --timeout 40
```

### 管道保留与阻塞对照复测

| 场景 | 当前旧工具 | 修复候选 |
| --- | --- | --- |
| 保留管道端点，空闲 LSP，父进程 SIGKILL | 30.059 秒退出 | 1.044 秒退出 |
| 同上，stdout 填满 | 35 秒后仍存活，PPID=1；测试返回 1 | 30.526 秒退出 |
| 同上，stderr 填满 | 本轮没有重测旧工具；前述中间候选 RED 保留 | 31.030 秒退出 |
| 监管父进程 SIGKILL、目录存在、控制写端保留、子进程忽略 TERM | 8 秒后仍存活，PPID=1；测试返回 1 | 2.061 秒清理整个监管组 |
| 同上，stderr 填满 | 本轮没有重测旧工具；前述中间候选 RED 保留 | 2.287 秒清理整个监管组 |
| 父进程存活、正常 EOF | 前述对照与真实 Codex 用例保留 | 正常响应，EOF 退出码 0 |

候选六组复测均返回 0。旧工具的两组返回 1 是预期的故障复现，随后测试脚本回收了自己创建的遗留进程，不将 RED 当作验证通过。

### 真实 pyright 进程树

额外通过真实 MCP `structure/document_symbol` 请求打开临时 Python 文件，实际启动本机 `/opt/homebrew/bin/pyright-langserver --stdio`。在父进程被 SIGKILL 前核验 `mcp-lsp → supervisor → pyright` 的真实父子关系，再保留 stdio 管道端点并杀死测试父进程。

| 对象 | LSP / supervisor / pyright PID | LSP 退出耗时 | 最终存活的测试后代 |
| --- | --- | --- | --- |
| 当前旧工具 | 73698 / 73708 / 73710 | 30.151 秒 | 0 |
| 修复候选 | 73697 / 73707 / 73709 | 1.116 秒 | 0 |

这组确认了实际 manager 启动与清理链，前面的 TERM 忽略测试则锁定需要强制清空后代的情况。共享 gopls auto daemon 的 15 分钟协议 idle timeout 继续遵守原契约，未按进程名或持久 PID 批量终止共享服务。

`docs/契约/mcp-lsp-gopls-root-cohort.md` 已同步为父进程归 init/launchd 后回收自身监管组，不再要求 CWD 删除，并禁止关键清理路径等待继承的输出管道写入。完整 Go 门禁的过期 `acceptance.review_by` 阻断仍未解除。

## 最终变更：取消强制签名与本机安装

`cmd/mcp-lsp/tool_receipt.go` 在三个签名环境变量均为空时返回明确的未启用状态；`fx.go` 去掉 stdio 装配时必须有签名 sink 的限制。stdio、HTTP 与 bootstrap 的 `registryToolProvider.CallTool` 共享该可选 sink，只有显式启用后才追加签名回执。生产代码不生成临时密钥，不设置默认日志目录；半配置、错误的 canonical 开关、无效密钥或日志仍 fail-fast。没有改变工具参数、输出 schema 或签名回执格式。已同步 `docs/契约/mcp-service-convention.md`。

新增无配置启动、半配置失败和无签名 stdio 装配单测。分类入口 `./scripts/test_with_guard.sh --host-test light ./cmd/mcp-lsp -run '^TestNewStdioServerStartsWithoutSignedReceipt$' -count=1 -timeout=120s` 返回 1，仍在执行测试前被 `acceptance.review_by` 过期阻断，未修改冻结基线或绕过入口。此前含多个名字的 selector 被入口拒绝，随后已按单个精确 selector 重试；不将任何未执行单测计为通过。

### 无签名版本的真实运行结果

| 场景 | 结果 |
| --- | --- |
| 无签名，父进程存活、ping、正常 EOF | 通过，EOF 退出码 0 |
| 无签名，父进程强杀、保留管道端点 | 1.053 秒退出 |
| 无签名，父进程强杀、stdout 填满 | 30.531 秒退出 |
| 无签名，父进程强杀、stderr 填满 | 31.035 秒退出 |
| 无签名，监管组忽略 TERM，目录和控制管道保留 | 2.327 秒清空监管组 |
| 无签名，以上监管场景且 stderr 填满 | 2.326 秒清空监管组 |
| 无签名，真实 Codex SIGKILL | Codex 30567 → LSP 30612；Codex 退出码 -9，LSP 0.015 秒退出 |
| 显式有效签名配置 | 正常启动、ping、EOF 退出码 0 |
| 日志单配、密钥单配、canonical 单配、无效 canonical、密钥不存在 | 五个用例均启动失败，退出码 1 |
| 真实 MCP diagnostics，无签名检查全部 10 个修改 Go 文件 | `OK total=0`、`isError=false`，正常 EOF 退出码 0 |

以上程序入口均返回预期的明确退出码。macOS arm64 原生构建、Linux amd64 / Windows amd64 交叉编译、`go vet -p 1` 均返回 0。Linux 与 Windows 没有执行运行时测试。项目地图刷新与漂移检查、两个 Python 测试脚本 AST 检查、保留 CRLF 的定向 diff 检查均通过。

直接读取并使用现有三个客户端的 LSP env 配置完成 `initialize`、`tools/list`、`ping` 和 EOF 验证，均没有配置签名：`/Volumes/hello/wj/wjboot-v2/.codex/config.toml`、`/Volumes/hello/wj/wjboot-v2/.mcp.json`、`/Users/mima0000/.gemini/antigravity/mcp_config.json`。三者 command 均已核验指向本次替换的同一文件，客户端配置无需修改。

### 安装与回滚证据

- 已原子替换 `/Volumes/hello/wj/super-agent-v3/bin/LSP/mcp-lsp-mac-arm`，mode 保持 `0755`，owner/group 保持 `502:20`；安装前后核验 SHA-256，原生可执行文件的 Apple code signature 校验通过。Apple 可执行文件签名与这里取消强制要求的工具调用签名回执是不同层面的机制。
- 旧工具备份：`/Volumes/hello/wj/super-agent-v3/.codex/audit-lsp-orphan-20261011/mcp-lsp-mac-arm.before-orphan-fix`，SHA-256 与旧工具一致。没有覆盖其他 LSP 二进制。
- 安装后直接使用上述生产路径复测：Codex 41332 → LSP 41598，SIGKILL 后 LSP 0.019 秒退出；stdout 阻塞且保留端点时 30.544 秒退出，两项测试入口返回 0，全程无签名配置。
- `.codex/audit-lsp-orphan-20261011/installation.json` 保存安装哈希和回滚路径，`runtime-validation.json` 保存本轮进程测试输出。已运行的旧进程保留原映像；重新启动对应客户端 LSP 后才使用新版。未终止当前用户会话，未提交或推送。

## “编译安装，替换现在使用的 LSP”后的复核

重新执行 `env GOMAXPROCS=2 go build -p 1 -o /tmp/super-dolphin-lsp-install-20261011 ./cmd/mcp-lsp`，退出码 0。重新构建产物与已原子安装的文件均为 `67f5965202dd9f237ecec137b5773cd1fbfb48b255b9f092d7f527eeea10a512`，确认安装路径包含当前源码修改；没有重复替换相同内容。再次使用安装路径执行无签名 healthy 用例，ping 正常，EOF 退出码 0，测试入口返回 0。

通过 `ps` 的父子关系和 `lsof -d txt` 的 executable mapping 核验，当前实际 Codex PID 66886 的 LSP 子进程 67742、73593、74127、74812、75341 正在映射新版安装文件的 inode `311850340`，不只是配置指向新版。该快照保存于 `.codex/audit-lsp-orphan-20261011/live-process-mappings-before-cleanup.json`。Antigravity 中安装前启动的旧实例仍需客户端重新启动 LSP 后加载新版；磁盘替换不改变已有进程的映像。

另外发现旧版孤儿 LSP PID 28877，PPID=1，启动身份为 `Sun Oct 11 02:02:18 2026`。每次发送信号前核验 UID、PPID、完整可执行路径和启动时间：TERM 后 5 秒仍未退出，再仅对这个已确认的旧孤儿发送 KILL，5.087 秒完成，确认不再存活。证据在 `.codex/audit-lsp-orphan-20261011/old-orphan-cleanup.json`。此次没有退出 Codex 父进程或重启用户会话；完整 Go 单测的冻结过期阻断仍如前述。

## 提交前的源码隔离与二进制复核

用户授权“提交、推送”后，发现主工作区另有约 500 个文件已暂存，且七个 LSP 导入依赖的 Go 源文件包含其他任务的未提交修改。为了让本次提交只包含 LSP 修复，建立 `.worktrees/lsp-orphan-publish-20261011`，以 `f874c0f64db83f62569ae7f8f24d7e9ca5a53aa6` 为基准，仅复制本次 16 个变更文件，保留主工作区原暂存内容。

隔离树重新执行原生构建和 `go vet -p 1`，均返回 0。新二进制 SHA-256 为 `43c7304e8f979fcfe4938a7fac6e69cdc4cc84e7c639b3804c6e733ea3c5e2ed`。无签名实测：healthy 的 ping/EOF 正常，真实 Codex 82737 被 SIGKILL 后 LSP 82770 在 0.025 秒退出；stdout 阻塞时 30.420 秒退出；监管组忽略 TERM 且 stderr 填满时 2.052 秒清空监管组，各测试入口返回 0。中文提交标题与 fix-test 关联守卫通过。

将隔离源码构建的二进制原子安装到原路径，首次安装版本保存在 `.codex/audit-lsp-orphan-20261011/mcp-lsp-mac-arm.before-source-isolation`；原始故障版本备份继续保留。安装哈希与提交树中的二进制相同，mode/owner/group 与 Apple 可执行文件签名校验保持有效。记录在 `source-isolated-installation.json`。Git hooks 将继续对 exact staged tree 执行正常门禁，冻结验收过期未被修改或绕过。
