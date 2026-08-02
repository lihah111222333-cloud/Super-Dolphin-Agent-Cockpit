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
| `pre-commit` | `git commit` | 捕获 authoritative staged tree，执行 closure 与 project-map check；首次漂移时由受信 CLI 自动刷新受管输出并复验一次，再按配置执行本地 coordinator 或同步远程 gate | closure/project-map check/按需刷新 + 完整容器 gate |
| `commit-msg` | `git commit` | 要求提交标题包含中文；提交正文如果存在也必须包含中文；提交主题属于 `fix` / `hotfix` / `bugfix` / `修复` 时，要求同一提交修改锁定 bug 的测试、fixture、golden 或 snapshot | <1 秒 |
| `pre-push` | `git push` | 将 Git 的每条精确 ref update 规范化后交给受信 `super-dolphin-gate hook pre-push`；通过后才消费绑定该 ref update 的一次性 push grant | coordinator queue + 真实容器 gate |

`pre-commit` 捕获 initial `git write-tree`，并保留 Git 传入的 `GIT_INDEX_FILE` 到 Git 完成最终 commit。closure 首次漂移时，受信 CLI 只按该 tree 刷新 `build/gate/Dockerfile` 与 `build/gate/inputs.json`，hook 只暂存这两个受管文件、重算 staged tree 并复验一次；刷新失败或第二次仍漂移才 fail closed。复验后的 authoritative tree OID 显式交给 submit/source 与 wait；无论 hook 同步 passed 还是 queued 后 wait 成功，都必须对同一 index 重读 `git write-tree` 并与该最终 OID 比对。任何一步不得清除 alternate index 或回退到默认 index 重算。verifier 只从 Git tree object 提取输入，不执行工作树源码。

通过 closure witness 后，thin hook 只调用受信 gate CLI。入口从本地 Git 配置读取由安装器 provision 的绝对 launcher，并在每次执行时重验当前用户 owner 和非 group/world 可写 mode；它绝不从调用方 PATH 解析 CLI。`super-dolphin-gate` 是唯一构建和部署的门禁二进制，本地 coordinator、远程 coordinator 与 OCI image 内 ECI worker 都由它的固定子命令承担；不存在第二个 executor、helper 或双执行路径。其仓库内传递依赖只允许进入 `cmd/super-dolphin-gate`、`internal/devtools/**` 与 `build/gate/closure/**`，依赖边界测试会拒绝生产后端的 `internal/module`、`internal/platform`、`internal/store`、`internal/util` 或 `pkg` 代码。

安装器生成的 launcher 和 `production.json` 不持久化安装机器的绝对目录：launcher 从自身目录定位配置和控制器，配置路径字段相对配置文件目录保存，加载后再恢复为规范绝对路径进行安全校验。远程配置用命令名 `aliyun` 从部署环境 `PATH` 查找阿里云 CLI。只有每个 clone 的本地 Git 配置保存安装时发现的受信 launcher 绝对路径；这是 hook 从任意 cwd 启动所需的可重建绑定，迁盘后重跑 `make install-hooks` 即可更新。

CLI 为本次 Git action 生成新的 delivery identity，规范化 staged tree 或 pre-push ref update，然后把 entrypoint、authority owner、attestation、plan 和 source 绑定为 durable coordinator job。pre-commit 收到 queued/running 状态后立即以严格解析出的 job id 调用 `wait --job`，直到终态才把控制权交还 Git；重复同一 delivery 复用该 invocation，新的 hook action 即使 tree/range 相同也会生成新的 invocation，并得到新的 fresh-container execution。

本地 Docker 模式下，协调器持久化 job 后由 owner scheduler 执行 canonical plan；每次 CI invocation 固定为恰好三个 canonical shard/container，并只在完整保留的 `max-3` gang 中启动。每个 shard 都是 fresh `PlanExecution` container，passed 状态必须带完整 shard set 的签名 receipt。receipt 重新绑定 entrypoint、authority owner、source、plan、image generation 和 container evidence；只有 release receipt 还会绑定 owner-signed attestation，普通 hook receipt 不携带该 release attestation。release profile 只接受 `release` authoritative entrypoint 的已验签 owner attestation；普通 `super-dolphin-gate submit --profile release` 会被拒绝，不能伪造 release authority。

`pre-push` 不复用 pre-commit 的 delivery identity；它为本次 action 的每个 ref update 建立 exact range job，并在签名 receipt 匹配后才签发/消费单次 git.push grant。`commit-msg` 仍负责中文标题/正文与 fix 测试证据。

## Truth-image CI 契约

- `pre-commit` 以及手工 `make ci-l0`、`make ci-l1`、`make ci-l2-claude` 都以 `git write-tree` 取得的 staged tree 为唯一检查对象；只有首次 closure 漂移允许受信 CLI 刷新两个受管输出并形成新的 staged tree，后续 submit/source、wait 和最终 Git commit 必须贯穿该最终 OID。自动刷新前，`build/gate/Dockerfile` 或 `build/gate/inputs.json` 只要存在未暂存或未跟踪内容就立即拒绝，绝不覆盖或混入提交。release（`make ci-l3-release`）先拒绝 staged tree 与 `HEAD^{tree}` 不一致的状态，再检查这个 exact commit。GitHub Actions 将 event SHA 作为 exact commit 导入受信镜像后检查，候选 checkout 仅作只读数据，不能执行候选 workflow 或脚本。
- truth-image 输入变更会自动构建新的候选镜像；候选只带 provenance，在 trusted ref 提升前不能作为可运行镜像。非镜像输入的普通源码变更复用已接受的不可变镜像。
- 远程模式只为指纹 miss 创建 ECI：一个分片对应一个隔离 ContainerGroup，分片数由仓外 `maxShards` 和当前 miss 目录动态决定，多个 Agent 不共享固定本地队列。CPU/内存按 workload 资源画像选择并受仓外上限约束；抢占资源 30 秒内未取得时自动删除并返回失败，不存在替代执行环境。源码准备不占分片执行时钟；普通执行时限 10 分钟，release 执行时限 30 分钟，100 秒只是分片优化告警阈值，不会杀死容器。
- 不存在独立的 GitHub `commit-guard` 旁路。缺少受信 CLI、source/commit 不匹配、镜像或 receipt/provenance 校验失败、超时或任何 gate 失败都会 fail closed；本地提交信息与 fix 测试证据仍由 `commit-msg` 的两个直接 guards 检查。正文为空允许；正文一旦存在，纯英文正文会失败。
- 所有显式测试入口统一使用受信 CLI 的 `test` 子命令。每次测试请求都先查询 SQLite ledger 中与 ECI 相同语义、同一 OCI baseline image digest 的 PASS 指纹；全命中时不启动 ECI，未命中时只在 ECI 执行一次。不存在宿主机快速路径、第二次执行或替代缓存来源；本地结果不得写入通过缓存。旧 `test_with_guard`/`go_with_guard` 测试入口只接受远程 worker 注入的执行身份，宿主机直接调用会 fail-fast。
- canonical `backend:test_with_guard` 在 fresh container 内调用 `scripts/test_with_guard.sh --canonical-backend`；当解析后的包集合包含 `cmd/mcp-lsp` 时，脚本必须追加定向 `-tags=e2e` resource-cohort binary 测试。普通 `--quick-guard` 或不带 build tag 的包测试不能替代这条证据；`make test-e2e-mcp-lsp-resource-cohort` 是同一场景的显式复现入口，`TestAIMaintenanceGateSelectsMcpLSPResourceCohortE2E` 锁定选择映射。

## 远程 CI 模式契约

远程模式由 `SUPER_DOLPHIN_GATE_MODE=remote` 或本地 Git 配置 `super-dolphin.gate.mode=remote` 启用；环境变量优先。未设置时为 `local`，任何其他值都 fail closed。它仍先通过受信 launcher 和 closure check/单次自动刷新，不允许以远程模式绕过本地 tree 闭包或从 `PATH` 取得 CLI。

| 用途 | 环境变量（优先） | 本地 Git 配置 | 要求 |
|---|---|---|---|
| 受信 launcher 安装 | `SUPER_DOLPHIN_GATE_LAUNCHER` | `superdolphin.gateLauncher` | 安装器写入绝对路径；每次 hook 复验 owner、常规文件、可执行且非 group/world 可写。 |
| hook 路径 | — | `core.hooksPath` | 由安装器设为相对 `.githooks`，使 linked worktree 在自己的根目录解析 hook。 |
| 模式 | `SUPER_DOLPHIN_GATE_MODE` | `super-dolphin.gate.mode` | 必须为 `remote` 才走以下远程契约。 |
| 远程配置 | `SUPER_DOLPHIN_GATE_REMOTE_CONFIG` | `super-dolphin.remote.config` | 必填；指向 schema v4 的仓外配置。 |
| SQLite authority | `SUPER_DOLPHIN_GATE_LEDGER` | `super-dolphin.remote.ledger` | 必填；唯一 SQLite 真相源，保存已接受基线、校准、分桶耗时样本与本地可比较的 PASS 查询结果。 |
| 分片上限 | — | `super-dolphin.remote.maxShards` | 可选；传为 `--max-shards`，不得超过协议上限。 |
| 请求归属 | `SUPER_DOLPHIN_GATE_REQUESTER_FINGERPRINT` | — | 可选；由 `super-dolphin-gate requester create` 生成，只用于跨 Git hook、直接 CLI 和任意 Agent 客户端查询同一逻辑发起方的 job。 |

远程 `pre-commit` 先执行 `super-dolphin-gate project-map check --tree <tree>`，再将 initial `git write-tree` 和 `HEAD^{commit}` 传给 `remote hook pre-commit`，并在返回后重读 tree；远程结果必须是同一 tree 的 authoritative、`git-pre-commit`、`local-fast`、passed 且 `cleanup_complete=true`。远程 `pre-push` 保留 Git stdin 的每条 ref update，规范化为精确 range；每条结果必须是对应 source tree 的 authoritative、`git-pre-push`、`push`、passed 且完成清理。二者任一身份、状态或清理证据缺失均拒绝 Git 动作。

仓库不安装任何 Agent 厂商专用 lifecycle hook。需要把多个请求归到同一逻辑 Agent 时，客户端复用同一个 requester fingerprint；远程结果回显该字段，`super-dolphin-gate requester runs --ledger <path>` 可按 SQLite 索引查询最近 job。该字段不参与 PASS、owner、grant 或 receipt 判定，缺失时保持未声明而不是从工作树或进程猜测。

schema v4 的 SQLite ledger 是唯一真相源：只有命中同一输入闭包、执行语义、runner、平台和 OCI baseline image digest 的可比较 PASS 才可复用；未知、冲突或缺少顶层 PASS 一律不是通过证据，也不得转查第二个存储或缓存来源。远程 disposable worker 的 `project-map:check` 只运行严格检查，绝不先刷新再检查，因此提交中的地图漂移会保持失败而不会被 worker 写入后伪造 PASS。本地 `pre-commit` 在 closure refresh 后、提交远程 CI 前，通过仓外受信 launcher 调用已编译 CLI 的 `project-map check --tree <tree>`；候选 tree 只作为数据解包到隔离目录，禁止执行候选 tree 中的生成器、Makefile 或脚本。首次严格检查失败时，CLI 使用其编译闭包绑定的受信生成逻辑自动刷新一次，只允许写入并暂存 `docs/doc/codemap/project-map`，然后重算 staged tree 并再次严格检查；第二次仍漂移才拒绝。受管输出已有未暂存或未跟踪内容时同样进入这次受限刷新，不把“工作区有漂移”本身提前当作不可修复错误；刷新不得覆盖受管目录以外的用户改动。

首代 `remote calibrate` 不是简化 smoke：它在同一 commit/tree 上依次完整执行 authoritative `git-pre-commit`（tree）、相对第一父提交且 `refs/heads/main -> refs/heads/main` 的 synthetic fast-forward authoritative `git-pre-push`（range），以及 authoritative `release`（commit）。第三段 release catalog 是首代全包 race 基线的事实来源，因为 commit/push profile 本身不承诺包含 release-only race gate。三个 job 都成功且身份一致、每个 catalog workload 都有同环境成功样本、inventory 的每个 Go package 都有独立 race workload 样本后，才可接受校准账本。普通 commit、push 和 full 在账本缺失或稳定 runner 身份变化时会自动取得 `*.calibration.lock` 并单飞完成该流程；并发 Agent 锁内复查后复用结果。失败重试默认复用生产指纹未变化的已通过 workload 与既有成功样本，只有显式 `--force-rerun` 才绕过缓存。稳定 runner 身份只绑定 OCI baseline image、policy、toolchain 与 worker 二进制，不因纯源码 generation 更新而失效。账本以 `workload_id`、`command_digest`、`platform`、`runner`、`toolchain` 分桶，自动 LPT 分片只能使用同桶成功样本或显式保守初值，不能混用不可比耗时。每个 Go package workload 固定使用 `go test -json`；worker 将事件还原为普通文本日志，并为每个终态固定输出 `SUPER_DOLPHIN_CI_TEST_TIMING name=<test/subtest> status=<pass|fail|skip> duration_ms=<整数>`，同时把相同的名称、状态与毫秒耗时写入报告。每个 gate 只保留最多 32 KiB 的 UTF-8 日志尾部，计划报告与聚合 JSON 都编码为普通可读字符串，不使用 `[]byte` 的 base64 JSON；AI 和 CLI 读取方默认只取尾部。Coordinator 将明细按父 workload 和测试名绑定后，与包级样本一起追加到同一 SQLite 事务，账本 generation 随事务原子递增，禁止只保留包级总时间。即使同批其他分片失败、缺报告或超时，已经取得的终态分片、成功 PASS 标记和可比较耗时样本仍必须写入结果与账本，不得因为整批聚合失败而丢失。失败包中明确 `pass` 的顶层测试会发布独立内容寻址标记；后续任意 Agent 或工作树只执行失败或未完成测试。`skip`、缺少目标终态和子测试局部通过不能发布顶层 PASS；输入闭包变化或 `--force-rerun` 会恢复整包执行。

稳定 runner identity 与精确 OCI baseline image digest 必须分字段传递：前者服务于校准、耗时、缓存和分片身份，后者绑定唯一已接受的不可变镜像。源码-only 刷新可以保持前者不变，但每个 shard request 与执行环境必须携带当前 SQLite ledger 记录的 image digest。

远程模式没有跨 job 的全局 FIFO。每个 job 先按 SQLite ledger 做 workload 级通过缓存判定，只把 cache miss 交给 LPT；每个非空分片创建一个独立 ECI ContainerGroup，多 Agent 的实际计算并发量自然等于各 job 的 miss 分片总数。ECI 创建、轮询、日志与删除不设置固定并发上限；STS `Throttling.User`、TLS 握手超时、网络 timeout 和瞬态 EOF 由 ECI adapter 最多执行 12 次可取消退避，间隔从 500 ms 指数增长并封顶 8 秒。ECI CLI 的每次尝试另有 15 秒硬上限，分片状态探测和终态日志汇总的 observation watchdog 由“12 次尝试 + 全部退避 + 1 分钟余量”自动计算，不能使用短于完整重试预算的拍脑袋阈值。持续取得 `Pending`、`Scheduling` 或 `Running` 状态属于云端尚未终态；单次状态探测超过完整预算属于协调器 observation stalled；已经终态但日志汇总超过预算属于 terminal evidence unavailable，三者必须返回不同诊断并自动进入清理。重试必须复用完全相同的 CLI 参数和 `ClientToken`；权限、参数等永久错误立即失败，不得进入退避，也不会把本地协调器变成固定宽度队列。`max_shards_per_job` 可由配置或 `--max-shards` 调整，资源策略从登记档位按 workload 和历史观测选择，允许从 `2 vCPU / 4 GiB` 扩展到 `8 vCPU / 32 GiB`。创建时优先抢占实例；`Scheduling` 最多等待 30 秒，进入 `Pending` 即视为已取得资源，超时或 `ScheduleFailed` 时删除并确认容器组消失，再返回失败。不存在另一个执行环境、按量回退或本地双执行路径。

远程基线是唯一的不可变 OCI baseline image；镜像包含受信的 `super-dolphin-gate`、完整 Go module/build cache 与运行时依赖。worker 在拉取和执行前必须校验 image digest、provenance 和镜像内受信二进制；不使用归档分层、兼容旧格式或第二个基线来源。普通分片只使用该镜像中的依赖，执行环境固定 `GOPROXY=off`、`-mod=readonly`；缓存或依赖缺失立即阻断，禁止联网下载、运行时物化或替代来源。ECI 内外均由同一二进制执行，禁止第二个 executor 或并行执行路径。

worker 将 OCI image 内完整 `go-mod-cache` 根目录只读绑定为分片 `GOMODCACHE`；`cache/download` 元数据与已解压模块目录缺一不可。最终 gate 二进制构建与刷新校验同样只使用已接受 image 的离线缓存。每个分片只完整校验一次所需的不可变 Go/前端依赖树；各 lane 只复验当前 Git tree 的锁文件并绑定已验证目录，禁止每个 gate 重复遍历整棵缓存。独立单 gate 执行仍完整校验。

worktree 路径不进入 SQLite ledger 的 workload 或顶层测试缓存键；相同输入闭包、执行语义、runner 与 OCI image digest 跨 Agent、clone 与 worktree 复用同一结果。镜像内前端 `node_modules` 整体只读绑定；运行期只创建空的私有 npm 目录。每个含 Go workload 的分片只复制一次可写 `GOCACHE` 并供分片内全部 lane 复用，非 Go 分片不复制；不同 ECI 分片、源码副本和测试输出仍保持隔离。基线 image 只在 identity 变化时构建新 immutable tag，并在 worker 固定路径 `/workspace/work/lanes/lane-0/run/source` 执行；新镜像以已接受的上一代 immutable image 为唯一增量 build cache，绝不复制或解压另一套基线。实际分片在同一路径执行，因此不依赖 `-trimpath` 也能命中缓存，并保留路径敏感测试的正常语义。本地合同测试会把两棵绝对路径不同的宿主工作树依次物化到该固定路径，并要求第二次 `go test -x -count=1` 不出现 compile 调用。

`remote baseline-refresh` 以 SQLite ledger 旁的 `*.refresh.lock` 进行跨 linked worktree 的本机互斥；配置强制 `1440` 分钟刷新，续期 `ClientToken` 使用 UTC 日桶。identity 不变时只校验镜像可用性、续期并原子更新 SQLite 账本中的 `AcceptedAt`，不构建新镜像。identity 变化时，coordinator 只以 SQLite ledger 所记录的上一代已接受 immutable OCI image 作为增量 build cache 构建一个候选 image；候选完成 digest/provenance 验证并且远程 `main` 复验未漂移后，才在同一 SQLite 事务中原子提升为 current、保留 previous 并记录更早镜像 retired。普通远程运行不读取或等待刷新锁，且在候选构建期间继续使用 current image；不从 previous 或任何外部介质回退。镜像构建、验证、提升或 retired 清理任一步失败都 fail-fast；账本不得把未清理或未验证的候选称为 accepted。

当前候选实现仍为 `NOT_VERIFIED`。验收只绑定当前精确 tree：需要取得聚焦合同测试、真实 ECI 的 `test`/`commit`/`push`/`full` 场景、失败续跑、跨工作树共享 PASS、单一 OCI baseline image 的构建、原子提升与清理证据，并紧随同 tree 复跑证明全命中为零 ECI。历史 generation、旧 receipt、旧账本行或过去的屏幕进度只能辅助定位，不能替代当前实现证据；100 秒仅是优化与自动拆分目标，超时告警不把已经通过的运行改判失败。

## 入口与失败语义

- `pre-commit` 的入口是先捕获 staged tree OID，再执行 `super-dolphin-gate closure check --tree <tree>`；首次漂移自动调用 `closure refresh` 并复验新的 staged tree，随后执行 `super-dolphin-gate hook pre-commit --tree <final-tree>`。CLI 返回异步状态时，hook 必须继续执行 `super-dolphin-gate wait --job <job-id> --tree <final-tree>`。刷新后仍漂移、提交或等待任一入口失败都会拒绝提交。
- `pre-push` 的入口是 `super-dolphin-gate hook pre-push <remote-name> <remote-url>`。它将 Git 传入的远端名称、URL 与 ref updates 交给 coordinator；缺少受信 CLI、参数不完整、job 未取得匹配 receipt 或 push grant 都会拒绝推送。
- `commit-msg` 仍直接执行 `scripts/guard_commit_titles.sh --message <message-file>` 与 `scripts/guard_fix_commits_have_tests.sh --cached <message-file>`。它拒绝不符合中文标题/正文规则或缺少 fix 测试证据的提交。

thin hook 不直接运行 gofmt、go vet、包测试、前端检查、候选 tree 生成器或 AI-maintenance plan。唯一写操作是首次漂移时调用受信仓外 CLI：closure 只刷新并暂存其受管输出，project-map 只刷新并暂存受管地图目录；两者都必须重算 tree 并严格复验，第二次仍漂移才阻断。其余检查只由 canonical gate plan 在对应 coordinator job 的 fresh container 中执行。

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
