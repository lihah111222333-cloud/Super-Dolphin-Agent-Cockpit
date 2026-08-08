# 项目级 Git hooks

执行 `make install-hooks` 安装版本化 hooks。安装器默认从当前精确 staged tree 物化、编译并原子安装内容寻址的 `super-dolphin-gate` launcher；receipt 绑定 tree、实际 Go 导入闭包、工具链、编译器、构建参数、闭包生成器和二进制摘要。每次 hook 都重新计算 tree 身份并复验 receipt、owner、类型、可执行权限及非 group/world 可写 mode。

builder 也必须从 staged tree 通过 Git plumbing 读取后执行；安装器不接受环境变量或任意外部路径覆盖 launcher，不从调用方 `PATH` 解析候选 Gate。

如果 pre-commit 自动刷新受管生成物导致 staged tree 改变，hook 会从刷新后的 exact staged tree 提取受信 builder，重建并复验一次内容寻址 launcher，然后继续同一次提交；不会执行候选工作树中的 Makefile 或安装脚本。受管 codemap、capability-contract 清单和 project-map 的自动刷新固定按 `codemap → capcontract → project-map` 顺序执行，各自仍由自己的 owner 生成器负责；`scripts/refresh_generated_artifacts.sh all` 是工作树维护时的统一入口，hook 则使用同一顺序的受信 exact-tree 入口，绝不执行候选工作树 generator。不同 linked worktree 的 tree launcher 可在受限安装根中并存，执行阶段按当前 tree 从安装根选择并完整复验，不依赖最近一次安装路径。

所有提交和推送只使用 remote ECI 门禁；不存在本地 scheduler、Docker 容器执行或本地回退分支。两个 hook 都要求本地 Git 配置或环境提供 `SUPER_DOLPHIN_GATE_REMOTE_CONFIG` 和 `SUPER_DOLPHIN_GATE_LEDGER`，缺失即 fail closed。

远端 hook 不读取 `super-dolphin.remote.maxShards`，也不传递 `--max-shards`；分片调度不设本地上限，由远端协调器按当前工作负载决定。

在受信 launcher 可用后，两个 hook 都先检查调用方继承的 `SUPER_DOLPHIN_CI_AGENT_TOKEN`：缺失时立即调用对应的无参数 `remote hook`，只输出阶段一申请 guidance 并 fail closed；值为 `issue` 时同样立即调用，由 gate 明确拒绝。hook 不签发、缓存、改写或通过 argv 传递 token；实际 token 仅由调用 Git 的 agent 任务上下文继承到最终 remote hook。

持有实际 token 时，`pre-commit` 才固定 staged tree，验证并在允许时单次刷新受管 closure 输出，然后以该精确 tree 和 parent commit 调用 `remote hook pre-commit`。`pre-push` 才将 Git stdin 的每条精确 ref update 交给 `remote hook pre-push`。远端结果必须绑定同一 source tree、入口和清理证据；任何身份、权威性或状态缺失均拒绝 Git 动作。

`commit-msg` 仍要求中文提交信息和 fix-test evidence。hooks 不从调用方 PATH 解析 gate CLI，也不执行候选工作树中的脚本。

mcp-lsp 本地门禁只消费版本化 workload ID：`mcp-lsp-idle-quick`、`mcp-lsp-native-process-tree`、`mcp-lsp-default-15m`。本地 runner 生成的 `local-runner` receipt 只证明本地执行；catalog 的 `producer_implementation_status=missing` 表示尚无 CI/release artifact producer，不能把本地 receipt 冒充发布权威，且必须保持 `release_blocking=true`，直到真实 producer 落地并通过 workflow/artifact 校验。其中 default-15m source-E2E 当前缺实现，必须保持 N/V；100-workspace soak 与 release artifact 同样保持 N/V，并阻断 T6/release。

真实 gopls root-cohort source E2E 的权威入口是 `make test-e2e-gopls-daemon-lifecycle`；其版本化脚本固定 20 分钟测试总超时，覆盖 15 分钟 daemon 自退出窗口。该入口只提供源码级生命周期证据，不会提升 catalog 中仍为 N/V 的 default-15m workload 或 release 状态。
