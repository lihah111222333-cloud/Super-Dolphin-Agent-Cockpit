# 项目级 Git hooks

执行 `make install-hooks` 安装版本化 hooks。`pre-commit` 先用受信 Gate 对 exact staged tree 校验项目地图；发现 drift 时自动刷新并暂存受管地图，再物化最终 staged tree 运行轻量代码守卫。兼容 launcher 缺失时，hook 从 exact staged tree 读取受信 builder 并按内容寻址安装，不要求人工先刷新地图或重跑安装器。receipt 绑定构建来源 tree、实际 Go 导入闭包、工具链、编译器、构建参数、闭包生成器和二进制摘要。`pre-push` 复验 receipt、owner、类型、可执行权限及非 group/world 可写 mode，并从 pushed tree 重算 Gate 编译闭包；版本闭包一致即可复用 launcher，不要求 pushed tree 等于 launcher 的构建来源 tree。

pre-push launcher 的 builder 必须从 staged tree 通过 Git plumbing 读取后执行；安装器不接受环境变量或任意外部路径覆盖 launcher，不从调用方 `PATH` 解析候选 Gate。

`pre-commit` 只允许受信 Gate 的 exact-tree `project-map check/refresh` 自动维护项目地图，不执行候选树的生成脚本、Makefile、closure、codemap、capability-contract 或其他完整门禁。项目地图目录存在未暂存或未跟踪内容时立即阻断，禁止自动刷新覆盖用户工作。不同 linked worktree 的 launcher 可在受限安装根中并存，hook 按当前 exact tree 从安装根选择并完整复验，不依赖最近一次安装路径。

`pre-commit` 不启动 remote ECI 门禁，也不要求 remote config、duration ledger 或 agent token。它先把受信项目地图收敛到 exact staged tree fixed point，再在仓库根目录 `.worktrees/` 下物化最终 staged tree，只运行 `scripts/test_with_guard.sh --light-guard-only` 轻量代码守卫，并在结束后验证临时 worktree 已清理且 code guard 期间 index tree 未变化。`pre-push` 是 Git 交付路径唯一的完整 remote ECI 门禁；不存在本地 scheduler、Docker 容器执行或本地回退分支，并继续要求 `SUPER_DOLPHIN_GATE_REMOTE_CONFIG`、`SUPER_DOLPHIN_GATE_LEDGER` 与实际 agent token，缺失即 fail closed。

远端 hook 不读取 `super-dolphin.remote.maxShards`，也不传递 `--max-shards`；分片调度不设本地上限，由远端协调器按当前工作负载决定。

在受信 launcher 可用后，`pre-push` 检查调用方继承的 `SUPER_DOLPHIN_CI_AGENT_TOKEN`：缺失时立即调用无参数 `remote hook pre-push`，只输出阶段一申请 guidance 并 fail closed；值为 `issue` 时同样立即调用，由 gate 明确拒绝。hook 不签发、缓存、改写或通过 argv 传递 token；实际 token 仅由调用 Git 的 agent 任务上下文继承到最终 remote hook。`pre-commit` 不读取该 token。

`pre-commit` 只调用受信 Gate CLI 的 project-map exact-tree 子命令；自动刷新后重新写入 tree、复验 launcher 与项目地图，再在隔离 worktree 中执行轻量代码守卫。它不会调用候选生成器或 `remote hook pre-commit`。持有实际 token 时，`pre-push` 将 Git stdin 的每条精确 ref update 交给 `remote hook pre-push`，统一执行完整门禁。远端结果必须绑定同一 pushed source tree、入口和清理证据；任何身份、权威性或状态缺失均拒绝 push。

`make remote-ci-init` 会安装仓库本地 `git remote-ci` 别名。该别名调用版本化 `scripts/git_with_remote_ci_credentials.sh`，从 GitHub CLI 的系统凭据存储读取 GHCR 凭据，并在 hook 外通过受信 Gate 签发当前进程链的 agent token；随后只通过环境 `exec git`。原始凭据不进入 Git 配置、文件、SQLite、日志或 argv。启动器接受 `--repository`，因此同一版本化实现可服务其他已经安装 exact-tree Gate 的受信仓库；目标仓库不受信或任一凭据不完整时立即拒绝。

`commit-msg` 仍要求中文提交信息和 fix-test evidence。hooks 不从调用方 PATH 解析 gate CLI；`pre-commit` 只执行 exact staged tree 中版本化的代码守卫入口，`pre-push` 不执行候选工作树脚本。

mcp-lsp 本地门禁只消费版本化 workload ID：`mcp-lsp-idle-quick`、`mcp-lsp-native-process-tree`、`mcp-lsp-default-15m`。本地 runner 生成的 `local-runner` receipt 只证明本地执行；catalog 的 `producer_implementation_status=missing` 表示尚无 CI/release artifact producer，不能把本地 receipt 冒充发布权威，且必须保持 `release_blocking=true`，直到真实 producer 落地并通过 workflow/artifact 校验。其中 default-15m source-E2E 当前缺实现，必须保持 N/V；100-workspace soak 与 release artifact 同样保持 N/V，并阻断 T6/release。

真实 gopls root-cohort source E2E 的权威入口是 `make test-e2e-gopls-daemon-lifecycle`；其版本化脚本固定 20 分钟测试总超时，覆盖 15 分钟 daemon 自退出窗口。该入口只提供源码级生命周期证据，不会提升 catalog 中仍为 N/V 的 default-15m workload 或 release 状态。
