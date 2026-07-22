# .githooks — 项目级 git client hook

仓库共享的 git 客户端钩子，在**本地** `git commit` / `git push` 时自动提交受信 gate coordinator；它们与 GitHub Actions 复用同一套 truth-image / receipt 契约，但仍只拦截本地 Git action。

## 一次性激活

clone 仓库后必须跑一次：

```bash
make install-hooks
```

底层先要求 provision 受信 launcher：`SUPER_DOLPHIN_GATE_LAUNCHER=/absolute/path make install-hooks`。安装器将通过本地 Git 配置保存绝对路径，并拒绝 owner 不匹配、group/world 可写、非普通文件或不可执行的 launcher；缺失/无效 launcher 时不安装 hooks。随后它在没有其他 `core.hooksPath` 时执行 `git config --local core.hooksPath .githooks`。安装器拒绝静默覆盖已有 hooks；先显式检查并迁移它们，再重新执行安装。

> 用**相对路径**是为了让 `git worktree add` 创建的 linked worktree 在各自工作区根目录解析 `.githooks`。
> 如果旧配置仍指向主仓绝对路径，进 linked worktree 后重跑 `make install-hooks`。

之后所有 `git commit` / `git push` 自动经过对应 hook。

## 钩子清单

| Hook | 触发 | 做什么 | 大约耗时 |
|---|---|---|---|
| `pre-commit` | `git commit` | 一次捕获 authoritative staged tree，针对该 OID 执行 `gate-image-closure:check`、受信 `super-dolphin-gate hook pre-commit` 和绑定 tree 的 wait | closure check + 完整容器 gate |
| `commit-msg` | `git commit` | 要求提交标题包含中文；提交正文如果存在也必须包含中文；提交主题属于 `fix` / `hotfix` / `bugfix` / `修复` 时，要求同一提交修改锁定 bug 的测试、fixture、golden 或 snapshot | <1 秒 |
| `pre-push` | `git push` | 将 Git 的每条精确 ref update 规范化后交给受信 `super-dolphin-gate hook pre-push`；通过后才消费绑定该 ref update 的一次性 push grant | coordinator queue + 真实容器 gate |

`pre-commit` 捕获 initial `git write-tree`，并保留 Git 传入的 `GIT_INDEX_FILE` 到 Git 完成最终 commit。这个 authoritative tree OID 显式交给 closure verifier、submit/source 与 wait；无论 hook 同步 passed 还是 queued 后 wait 成功，都必须对同一 index 重读 `git write-tree` 并与初始 OID 比对。任何一步不得清除 alternate index 或回退到默认 index 重算。verifier 只从 Git tree object 提取输入，不编译或执行工作树源码。CLI 缺失、tree 解析失败、tree 不匹配或检查失败都会在提交前 fail closed。

通过 closure witness 后，thin hook 只调用受信 gate CLI。入口从本地 Git 配置读取由安装器 provision 的绝对 launcher，并在每次执行时重验当前用户 owner 和非 group/world 可写 mode；它绝不从调用方 PATH 解析 CLI。CLI 为本次 Git action 生成新的 delivery identity，规范化 staged tree 或 pre-push ref update，然后把 entrypoint、authority owner、attestation、plan 和 source 绑定为 durable coordinator job。pre-commit 收到 queued/running 状态后立即以严格解析出的 job id 调用 `wait --job`，直到终态才把控制权交还 Git；重复同一 delivery 复用该 invocation，新的 hook action 即使 tree/range 相同也会生成新的 invocation，并得到新的 fresh-container execution。

协调器持久化 job 后由 owner scheduler 执行 canonical plan；每次 CI invocation 固定为恰好三个 canonical shard/container，并只在完整保留的 `max-3` gang 中启动。每个 shard 都是 fresh `PlanExecution` container，passed 状态必须带完整 shard set 的签名 receipt。receipt 重新绑定 entrypoint、authority owner、source、plan、image generation 和 container evidence；只有 release receipt 还会绑定 owner-signed attestation，普通 hook receipt 不携带该 release attestation。release profile 只接受 `release` authoritative entrypoint 的已验签 owner attestation；普通 `super-dolphin-gate submit --profile release` 会被拒绝，不能伪造 release authority。

`pre-push` 不复用 pre-commit 的 delivery identity；它为本次 action 的每个 ref update 建立 exact range job，并在签名 receipt 匹配后才签发/消费单次 git.push grant。`commit-msg` 仍负责中文标题/正文与 fix 测试证据。

## Truth-image CI 契约

- `pre-commit` 以及手工 `make ci-l0`、`make ci-l1`、`make ci-l2-claude` 都以 `git write-tree` 取得的 staged tree 为唯一检查对象；`pre-commit` 只捕获一次，并将这个 OID 贯穿 closure、submit/source、wait 和最终 Git commit。release（`make ci-l3-release`）先拒绝 staged tree 与 `HEAD^{tree}` 不一致的状态，再检查这个 exact commit。GitHub Actions 将 event SHA 作为 exact commit 导入受信镜像后检查，候选 checkout 仅作只读数据，不能执行候选 workflow 或脚本。
- truth-image 输入变更会自动构建新的候选镜像；候选只带 provenance，在 trusted ref 提升前不能作为可运行镜像。非镜像输入的普通源码变更复用已接受的不可变镜像。
- 每个 CI invocation 都从已接受的 truth image fork 恰好三个 fresh、隔离的 Docker `PlanExecution` shard/container；owner 只为完整 `max-3` gang 预留容量，超出的 invocation 按 FIFO 排队。每个 shard 固定 4 CPU、8 GiB，Docker 总预算约 25 GiB。排队和镜像准备不消耗 shard 执行时钟；源码快照准备单独限制为 15 分钟，fresh-container 普通执行时限 10 分钟，release 执行时限 30 分钟。truth-image 输入变化时，候选构建与晋升另有独立 30 分钟预算。
- 不存在独立的 GitHub `commit-guard` 旁路。缺少受信 CLI、source/commit 不匹配、镜像或 receipt/provenance 校验失败、超时或任何 gate 失败都会 fail closed；本地提交信息与 fix 测试证据仍由 `commit-msg` 的两个直接 guards 检查。正文为空允许；正文一旦存在，纯英文正文会失败。

## 入口与失败语义

- `pre-commit` 的入口是先捕获 staged tree OID，再执行 `super-dolphin-gate closure check --tree <tree>` 和 `super-dolphin-gate hook pre-commit --tree <tree>`；CLI 返回异步状态时，hook 必须继续执行 `super-dolphin-gate wait --job <job-id> --tree <tree>`。closure、提交或等待任一入口失败都会拒绝提交。
- `pre-push` 的入口是 `super-dolphin-gate hook pre-push <remote-name> <remote-url>`。它将 Git 传入的远端名称、URL 与 ref updates 交给 coordinator；缺少受信 CLI、参数不完整、job 未取得匹配 receipt 或 push grant 都会拒绝推送。
- `commit-msg` 仍直接执行 `scripts/guard_commit_titles.sh --message <message-file>` 与 `scripts/guard_fix_commits_have_tests.sh --cached <message-file>`。它拒绝不符合中文标题/正文规则或缺少 fix 测试证据的提交。

thin hook 不直接运行 gofmt、go vet、包测试、前端检查、codemap/project-map 刷新或 AI-maintenance plan。它们只由 canonical gate plan 在对应 coordinator job 的 fresh container 中执行；不要把本地 hook 输出当成这些旧入口的结果。

这些门禁不支持绕过。受信 CLI、closure、coordinator job、receipt 或 push grant 失败时，必须修复失败原因后重新执行对应 Git action。

## 诊断

| 现象 | 检查命令 |
|---|---|
| commit 被 closure 或 coordinator 拒绝 | `bash .githooks/pre-commit` 查看受信 CLI、closure 或 job 输出 |
| push 被拒绝 | `bash .githooks/pre-push <remote-name> <remote-url>`，并保留 Git 提供的 ref-update 输入以复核 coordinator receipt/grant |
| 提交信息检查失败 | `bash .githooks/commit-msg .git/COMMIT_EDITMSG` |
| 想确认 hook 装没装 | `git config --get core.hooksPath`（应输出 `.githooks`） |

## FAQ

### IDE / GUI 提交会不会跑？

会。Git hook 是 Git 客户端行为，只要 IDE / GUI 最终调用的是这个仓库的 `git commit` / `git push`，就会走 `core.hooksPath` 指向的脚本。若 IDE 内置 Git 工作目录不同，请先在 IDE 终端里确认：

```bash
git config --get core.hooksPath
```

### rebase / cherry-pick / merge / revert 中间提交会怎样？

`pre-commit` / `commit-msg` 不覆盖所有 sequencer 自动产生的中间提交；这是 Git 客户端 hook 的结构性限制。最终 push 时，`pre-push` 会为每个 ref update 建立新的 coordinator delivery，并只在匹配的签名 receipt 与一次性 push grant 存在时放行。

## 卸载

```bash
git config --unset core.hooksPath
```

重新激活就 `make install-hooks`。

## 修改钩子内容

直接编辑 `.githooks/pre-commit`、`.githooks/commit-msg` 或 `.githooks/pre-push`，git 追踪它们，提交后所有装了 hook 的人下次 pull 自动生效。
