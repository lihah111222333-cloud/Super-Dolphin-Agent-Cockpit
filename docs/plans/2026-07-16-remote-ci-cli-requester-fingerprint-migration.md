# 阿里云 ECI 弹性远程 CI 迁移与验收计划

> 更新日期：2026-07-30
>
> 当前结论：`NOT_VERIFIED`。深圳 ECI 已接受 manifest v9 的第 47 代 Anchor DataCache；Seed 从上一代读取完整 Go build cache、Go module、前端、LSP 和工具依赖，只在云端用 3 秒编译新的统一 CLI。第 47 代 manifest 已绑定精确 CLI 编译闭包源码指纹、`linux/amd64`、工具链摘要和二进制摘要，CLI 还能从自身读取并报告同一组构建身份。单一稳定 Anchor DataCache + OSS 不可变压缩 delta、目标级 PASS 复用和失败续跑已经进入候选实现；仍须完成 CLI 不相关源码变化的真实复用、CLI 源码变化的真实重编译、相关定向测试、commit/push/full 和 Git hook 完整放行/阻断证据后，才能把总任务改为 `VERIFIED`。

## 1. 最终效果

统一 CLI `super-dolphin-gate` 同时承担本地协调器和 ECI worker：

1. Git pre-commit、pre-push、直接 CLI 和任意 Agent 客户端只提交 CI 意图，不直接执行各自的一套脚本。
2. 协调器解析精确 Git commit/tree，选择 commit、push、full 或指定测试场景。
3. 每日检查受信远程 `main`。基线输入不变时仅复验并续期稳定 Anchor DataCache；输入变化时才创建一次短生命周期 ECI seed。普通源码变化先读取同一 Anchor 与既有 delta，再只向 OSS 追加本代 source/build-cache 压缩 delta，不创建新的 DataCache。
4. 每次 CI 只上传相对于已接受基线的 binary-safe Git tree delta、严格 manifest 和分片请求到 OSS，不构建、不上传业务镜像。
5. 一个分片对应一个按需 ECI 容器。分片与 baseline seed 都先请求抢占实例；创建成功后只对尚未取得资源的 `Scheduling` 状态观察最多 30 秒，进入 `Pending` 即表示资源已分配并继续启动。30 秒仍未取得资源或进入 `ScheduleFailed` 时，必须先删除抢占容器并确认查询结果为空，再用完全相同的 CPU、内存、命令、卷和标签创建 `NoSpot` 按量容器，禁止两种计费实例重叠。每个容器只挂载同一份只读 Anchor DataCache，再按 shard request 从 OSS 读取摘要绑定的有序 delta 链；在私有 `EmptyDir` 中先验签 Anchor 和全部 delta，再展开 source/build-cache 层，复制源码到自己的可写工作目录，应用本次源码差分并复验目标 tree 后执行本分片。
6. 每次运行先生成 workload catalog，再按目标语义、生产输入闭包和执行环境计算通过缓存；只有 cache miss 才进入耗时规划和 LPT 分片。全命中时不上传源码、不创建分片计划、不启动 ECI。
7. 普通 commit、push 或 full 运行会先单飞检查校准元数据。当前 Worker 执行闭包已有完整同身份成功耗时样本时，CLI 直接重建并接受 commit/push/release 三套 catalog 校准元数据，不启动 ECI；只有新增、命令变化或确实缺少耗时证据的目标才进入 authoritative 校准。release 校准目录为 inventory 中每个 Go 包产生独立 race workload。每项新实测耗时进入仓外 SQLite 权威账本，规划器据此做确定性 LPT 分片，并以 100 秒完成整套 CI 为优化目标。
8. 每个 job 的最大分片数和资源档位均由仓外配置决定。一个分片一个 ECI ContainerGroup；guard、Node、Go 和 baseline seed 可以使用不同 CPU/内存档位，单片硬上限为 `8 vCPU / 32 GiB`。
9. 所有 cache miss 分片成功、缓存与新结果完整聚合且临时 ECI/OSS 对象清理成功后，才允许生成通过结果。缺项、重复、worker 安全上限超时、tree 不一致或清理失败均 fail-fast；单片超过 100 秒只产生优化告警，不改变其真实成功或失败终态。
10. CLI 可生成随机的 requester fingerprint。任意 Agent 在 Git hook、直接 `remote run`、`test` 或其他 CLI 客户端中持续携带同一指纹时，协调器把这些请求归到同一逻辑 Agent；该字段只用于审计、续跑和请求归并，不从 `cwd`、厂商 session 或进程号猜测，也不进入 workload PASS、runner identity、生产输入或授权判定。

`super-dolphin-gate` 是唯一构建、安装和放入 DataCache 的门禁可执行文件。本地协调器、远程协调器、ECI materializer、worker 和 runtime-seed 只是同一二进制的固定命令面；不再生成 `super-dolphin-gate-executor` 或独立 seed helper。源码与生产后端物理隔离在 `cmd/super-dolphin-gate`、`internal/devtools/**` 和 `build/gate/**`，并由传递依赖测试拒绝导入生产后端的 `internal/module`、`internal/platform`、`internal/store`、`internal/util` 或 `pkg`。pre-commit 首次发现 gate-image closure 生成物漂移时，由受信仓外 CLI 按精确 staged tree 自动刷新一次，只暂存 `build/gate/Dockerfile` 与 `build/gate/inputs.json`，重新计算 staged tree 并复验；刷新失败或第二次仍漂移才阻断，禁止依赖 Agent 人工执行生成器。

生产安装闭包不得把安装机器的绝对目录写入 launcher 或 `production.json`。launcher 必须先规范解析自身目录，再以相对路径定位配置和控制器；`production.json` 的所有 `*_root`、`*_file`、`*_profile`、`*_repository` 路径字段都相对配置文件目录持久化，加载后才解析为绝对路径并执行既有 owner、mode、无符号链接、仓库外和根隔离校验。仓外远程配置的 `aliyun_cli` 保存可执行名 `aliyun`，由部署环境的 `PATH` 提供，不得写入用户主目录。Git 每个 clone 的本地配置仍由安装器记录当前受信 launcher 的规范绝对路径，因为 hook 可从任意 worktree/cwd 启动；该值是可重建的安装状态，迁盘后必须重跑安装器，不得进入仓库或版本化部署产物。

CLI 本身采用独立的编译闭包和校验链。`build/gate/inputs.json` schema v2 的 `gate_compile_inputs` 必须是从 `cmd/super-dolphin-gate` 出发解析得到的精确、排序、无通配符的非测试 Go 传递闭包，并包含根模块与本地 replace 模块的锁文件；普通业务源码、前端锁文件和测试文件不进入该闭包。协调器从目标 Git tree 对这组输入计算内容摘要并写入 baseline manifest v9 的 `gate_source_sha256`。Seed 先校验上一代 CLI 的 manifest 摘要、字节数和二进制摘要，再调用 `worker cli-identity` 读取二进制通过链接参数固化的源码摘要、`GOOS/GOARCH` 和工具链摘要。三项全部匹配才允许直接复用；任一项缺失或变化都必须在 ECI 内离线重编译并再次自检，禁止由本机上传 CLI。日志固定输出 `gate CLI mode: reuse|compile`、源码摘要和编译耗时，使复用决策可以独立审计。

## 2. 明确不采用的方案

- 不购买 ACR 企业版。
- 不依赖 ACR 个人版远程构建。
- 不在本机执行 `docker build`、`docker push` 或上传 OCI 镜像。
- 不为每个 commit、Agent、clone 或 worktree 创建一份镜像或缓存。
- 不为每个 source/build-cache delta 创建 DataCache 或给单个分片挂载多份 DataCache。Anchor 容量由已验签 manifest 中的 gate、CA 和全部压缩层实际字节数计算：向上取整后预留 5 GiB，最低 20 GiB；仓外 `data_cache.max_size_gib` 只声明上限，当前为 100 GiB，并不预分配 100 GiB。产物增长时下一次 Anchor 自动扩容，计算值超过上限则 fail-fast。每个挂载会创建同容量按量云盘，因此普通变化必须保留为 OSS 内容寻址压缩层，不能把本机全部缓存无筛选搬入 Anchor。
- 不把正在运行的容器当“母容器”克隆。
- 不上传完整 Git 仓库或可写 `.git` 目录；每日基线只上传摘要绑定的浅层/增量 source bundle，单次 CI 只上传 binary-safe tree delta。
- 不接受只按 commit、batch、分片或对象名命中的缓存。只有内容寻址的 workload 身份与不可变通过标记同时命中，才可作为该目标的既有通过证据。
- 不因 100 秒优化目标暂时不可达而漏跑测试、伪造失败或伪造通过；慢分片必须跑到真实终态、记录实耗时并返回拆分或优化告警。

旧 Docker daemon bootstrap smoke 属于已放弃的本地镜像方案，测试文件已删除。Git tree、闭包、receipt 和提交身份校验仍属于 ECI 信任链，不得作为“与 ECI 无关”删除。

## 3. 三层真相

### 3.1 固定运行时

ECI 使用不可变 digest 的基础运行时：

```text
ac2-registry.cn-hangzhou.cr.aliyuncs.com/ac2/base@sha256:bcee972fb909c52a8ec33ceba23ffac90e0187ee93f922d9ecc1214de65bf1ff
```

该 digest 对应 `linux/amd64` Ubuntu 22.04。它只提供最小根文件系统，不承载不断变化的项目源码。

ECI ImageCache 只加速该固定镜像拉取。跨地域公共镜像首次创建 ImageCache 时必须具备公网路径：为缓存构建临时绑定 EIP，或为 VSwitch 配置 NAT。自动镜像缓存不能绕过这个网络前提。

### 3.2 每日基线

`remote baseline-refresh` 从受信远程 `main` 计算：

```text
BaselineIdentity =
  main_commit
  + main_tree
  + platform
  + toolchain_digest
  + gate_policy_digest(
      registry_digest
      + seed_script_digest
      + runtime_dependency_lock_digest
    )
  + gate_source_sha256(exact gate_compile_inputs)
  + immutable_runtime_image_digest
```

输入未变化：

1. 查询已接受 DataCache。
2. 确认状态、bucket、path、size 和身份完整。
3. 续期当前 generation。
4. 原子更新仓外 accepted state。
5. 不创建 seed ECI，不重复下载依赖。

输入变化：

1. 解析受信远程 `main` 的精确 commit/tree。
2. 计算下一 canonical generation，以 DataCache bucket/path 和 `owner`、`generation` 标签精确查询同代未接受候选。唯一结果先删除 DataCache 并等待消失，再幂等删除整代 OSS 前缀；零结果直接删前缀；多结果、分页或身份漂移 fail-fast。该动作只能命中下一代，禁止清理 current、previous 或 retired。
3. 协调器仅对首次运行或工具链变化在本机下载少量不可变工具归档；云端可能失败的下载必须流式限长并校验锁文件中的 SHA-256，再上传到本次 generation 的 OSS 输入。完整 Seed 脚本也以普通文本对象写入该输入目录；ECI 配置只携带小于 32 KiB 的固定 bootstrap，先校验脚本字节数与 SHA-256 再执行，禁止 base64 包装或未验签执行。它不是镜像上传，seed ECI 不直接访问 GitHub release asset。
4. 创建一个 seed ECI；只有首次构建或运行时依赖闭包、工具链、策略、平台、基础镜像变化时才绑定自动释放的临时 EIP，普通源码变化和仅为层数压实不申请 EIP。Create 使用幂等 `ClientToken`；瞬态错误重试耗尽或成功响应损坏时，必须按固定容器组名称和标签回查真实 ID，再纳入同一清理闭环。
5. Seed 只挂载稳定 Anchor DataCache，并以只读 OSS volume 读取已经接受的 delta 链。在 ECI 私有临时目录内先展开 Anchor，再按 generation 顺序应用 source bundle 和 Go build-cache delta；兼容入口只允许一次性读取 v6 `baseline.tar.gz` 或 v7 三层归档并压实为首个 v8/v9 Anchor。
6. 仅在依赖 identity 变化时安装或更新锁定 Go/Node/Python 与系统依赖。即使 runtime recipe 变化而必须压实新 Anchor，也要先复用上一代已验签的 Go/Node/Python、完整 `GOMODCACHE`、前端与 LSP `node_modules` 及固定 Go 工具，只补齐真正缺失的包或对象；recipe 变化不等于冷构建。主模块和有源码包的嵌套模块使用 `GOFLAGS=-mod=readonly go list -deps -test ./...` 只物化实际测试闭包；无源码包的专用 `runtime-proxy` 仅执行一次无 `all` 的 `go mod download`，补齐其显式锁定的 file-proxy 对象。禁止 `go mod download all` 下载无关模块图或改写 `go.sum`。源码变化时先将 Anchor 和全部旧 build-cache delta 合成为只读种子，再以实际普通与 race 参数只把 GOCACHE miss 写入本代私有层；源码不变时不执行该刷新。分片不得为已存在模块重复下载、冷解压或冷编译。
7. 普通变化生成 `source.delta.bundle` 与 `go-build-cache.delta.tar.gz`，只包含 accepted tree 之后的 Git 对象和本代私有 GOCACHE miss；旧压缩层保持原对象和摘要，不复制、不重打。manifest v9 记录 storage mode、generation、base/target commit/tree、每层 SHA-256 和字节数，并把 CLI 编译闭包源码摘要与本代 gate 二进制作为 OSS 内容寻址制品。
8. 只有没有可兼容 Anchor、运行时/工具链/依赖 identity 变化、source 不是 accepted tree 的后继，或 delta 层达到上限时，才生成完整 `runtime-deps.tar.gz`、`source.tar.gz` 和 `go-build-cache.tar.gz` 并创建新的 Anchor DataCache。完整压实同样必须先恢复旧 Anchor 和全部 delta，在其上只补新依赖、源码与缓存 miss 后重打完整层；“生成完整层”不等于冷构建。压实不能阻塞普通 CI，旧 accepted Anchor 和 delta 链在新 Anchor `Available` 前持续服务。
9. 普通 delta 不创建 DataCache；完整压实才等待新 DataCache `Available` 并复验 manifest。
10. 再次解析远程 `main`；构建期间发生漂移则拒绝晋升。
11. 原子晋升新 logical generation。delta 晋升复用同一 Anchor 并追加一层；压实晋升替换 Anchor、清空 active delta，并保留上一套 Anchor + delta 链供已启动分片完成。
12. 删除 seed ECI；仅当 accepted 与 previous 链均不再引用时，才回收 retired Anchor DataCache 和 OSS delta。失败时保留 retired 记录，下一次 refresh 先重试，不能伪称已回收。manifest、bundle 或工具归档发生部分上传失败时，也必须立即删除整个未接受 generation。

Anchor DataCache 和 OSS delta 链跨 Agent、clone 和 worktree 共享，不是每个工作树一份缓存。`baseline-refresh` 对 accepted state 文件旁的 `*.refresh.lock` 取得本机排他锁，避免 linked worktree 并发创建 generation；刷新间隔强制为 `1440` 分钟，续期 `ClientToken` 使用 UTC 日桶。identity 不变时只验证、续期并更新 `AcceptedAt`，不会创建 seed ECI、OSS generation 或 DataCache。候选恢复必须发生在新工件上传前，因此即使前一次上传、Anchor 创建或失败清理同时遭遇 STS 超时，下一次刷新也不会被既有 OSS 目录标记卡死或误复用半成品。源码变化、依赖变化和压实都必须读取旧 Anchor 与既有 delta：Seed 将旧层合成为只读缓存种子并把 miss 写入私有层，worker 按“私有写层 -> 最新 delta -> 较旧 delta -> Anchor”查询；禁止复制完整旧 cache 后伪称增量，也禁止忽略旧层重新冷构建。SQLite 只保存 DataCache/manifest 身份和验证结果，不能替代当前容器对实际挂载字节的校验：ECI 按 bucket/path 挂载可更新的 DataCache，而不是把不可变 `DataCacheId` 直接暴露给 worker。每个分片仍须先核对小 manifest，并对每个压缩层执行一次常规文件类型、SHA-256 与字节数校验；随后在隔离目录中把条目安全校验和解压合并为一次流式读取，完整成功后才发布到执行根，禁止再次完整解压只做预检。任何测试写入都不得回写 Anchor 或 OSS。

Go 依赖缓存必须以完整 `GOMODCACHE` 为一个不可分割的只读种子，既包含已解压模块目录，也包含 `cache/download` 下的 `.mod`、`.info`、`.zip`、`.ziphash` 和版本列表。worker 为每个分片创建物理私有 `GOMODCACHE` 根：已解压模块目录和既有下载元数据文件链接到同一个不可变 seed，`cache/download` 的目录拓扑则在分片内以 `0700` 重建，使 Go 可以写入新的本地查询状态而不会修改共享 seed；不得退化为每棵工作树复制依赖。运行期固定 `GOPROXY=off`，缓存缺失立即失败，禁止从 runtime-proxy 或公网重新物化。每日刷新复用校验和最终 gate 二进制构建也必须对 accepted runtime 的 `go-mod-cache` 执行离线 `-mod=readonly` 构建，不能误读 seed 临时空目录并触发无变化重建。每个分片启动时只遍历并校验一次所需的不可变 Go/前端依赖树；同一分片内各 lane 只复验当前 Git tree 的锁文件并绑定已验证目录，不得为每个 gate 重复哈希整棵缓存。独立单 gate 执行没有分片级验证证明时仍须完整校验。

所有 worktree 只以精确 Git tree 参与 workload 指纹，不得把本机路径写入依赖缓存或通过缓存键；同一输入闭包、执行语义和 runner 环境必须命中同一 OSS 通过标记。前端 `node_modules` 使用物理私有根并把顶层依赖逐项链接到同一个不可变 seed，只为 Vite 的 `.vite` 与 `.vite-temp` 建立分片内可写目录；完整性守卫必须验证链接目标和可写覆盖层拓扑，不能按 gate 复制依赖。`npm ci` 只在 seed 构建阶段使用临时下载缓存；运行期没有安装步骤，baseline 不保留 npm 下载缓存，各分片只创建空的私有 npm 日志目录。可写 `GOCACHE` 不能跨 ECI 容器并发写入；需要新 generation 且源码变化时，Seed 把精确 tree 物化到 worker 固定路径 `/workspace/work/lanes/lane-0/run/source`，分别以 `GOFLAGS="-p=2"` 和 `GOFLAGS="-p=1"` compile-only 增量补齐普通与 race 测试包。实际 Go workload 使用同一路径和参数，因此无需 `-trimpath` 也能跨工作树命中编译缓存，并保留 `runtime.Caller` 的正常绝对路径语义。每个包含 Go workload 的分片只从该 seed 复制一次可写缓存，并由该分片全部 lane 复用；纯前端或文本门禁分片不得准备它。本地合同测试必须用两棵绝对路径不同的宿主工作树依次物化到同一 worker 路径，并证明第二次不发生 compile。测试输出和源码副本继续按 lane/gate 隔离，避免并发写污染共享真值。

刷新锁只属于刷新命令，普通 `remote run`、Git hook 和指定测试不得读取或等待该锁。新 seed、delta 生成或 Anchor 压实期间，仓外 accepted state 保持上一代不变，所有门禁继续从上一代 Anchor + delta 链派生分片；普通 delta 在 manifest、远程 `main` 和全部对象摘要复验通过后即可原子晋升，完整压实还必须等待新 Anchor DataCache `Available`。候选失败或刷新进程中断时仍由上一代继续服务，这一顺序由 CLI 状态机和回归测试强制，不依赖 Agent 选择执行时机。

### 3.3 单次任务源码

每次 CI 绑定：

```text
JobSourceTruth =
  base_commit
  + base_tree
  + target_commit(optional)
  + target_tree
  + patch_format
  + patch_sha256
  + manifest_sha256
```

协调器通过以下固定 Git 命令生成差分：

```bash
git diff --binary --full-index --no-renames \
  <base-tree> <target-tree> --
```

worker init 的固定步骤：

1. 下载 request，验证 SHA-256、大小、对象键和 schema。
2. 将 request 绑定的 baseline manifest digest 与只读 DataCache 中的 manifest 精确比较。
3. 验证 bootstrap 二进制及 manifest 固定三层归档的 SHA-256 和字节数；任一层失败时不得开始解包。
4. 将 archive 解包到私有 expanded `EmptyDir`，并拒绝缺失或多余的顶层目录。
5. 从 expanded source 复制基线仓库到分片私有源码目录。
6. 下载 source manifest 和 binary patch，逐一验证 SHA-256、大小、对象键和 schema。
7. 验证基线仓库 clean 且 `HEAD^{tree}` 等于 `base_tree`。
8. 使用 `git apply --binary --index` 应用差分。
9. 使用 `git write-tree` 复验结果恰好等于 `target_tree`。
10. 创建本地 synthetic commit 并 detached checkout。
11. 再次确认 worktree clean，才交给只读挂载 expanded runtime 的非 root executor。

`runner identity digest` 与 `baseline manifest digest` 是两个独立合同字段。前者只用于校准兼容性、耗时分桶、workload 通过缓存和分片身份；后者只绑定 accepted state、shard request、init 容器环境变量与 DataCache 中 `baseline-manifest.json` 的精确字节。即使纯源码刷新应保持前者稳定，也绝不能用前者代替后者完成 bootstrap 验签。

binary-safe tree delta 支持已提交代码、暂存 tree 和活动 worktree 对应的精确 tree；worker 不依赖协调机的路径，也不读取协调机 `.git`。request、manifest 与 patch 必须位于同一 `job_id` 对象目录，目录根由仓外 `source_prefix` 配置，协议和实现不得写死某个 bucket、设备或工作树路径。

## 4. 分片与 100 秒优化目标

### 4.1 工作量目录

CLI 枚举并给每项工作生成稳定 ID：

- 七个 Go guard：`source`、`copylocks-provider`、`copylocks-platform`、`copylocks-thread`、`nested-runtime-proxy`、`nested-runtime-tools`、`nested-kelindar-event`；旧 canonical ID 只保留账本兼容映射。
- Go package test。
- Go race guard/package。
- 前端 Vitest 文件。
- 其他已登记 gate。

耗时样本按可复现环境分桶，自动分片只允许使用同桶成功样本：

```text
workload_id
command_digest
platform
runner_manifest_digest
toolchain_digest
success
duration_ms
```

新工作量没有样本时必须使用登记的保守初值；不得使用 0，也不得跳过。

### 4.2 首代全量校准

CLI 保留显式校准命令用于运维和复验：

```bash
super-dolphin-gate remote calibrate \
  --config "$SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG" \
  --ledger "$SUPER_DOLPHIN_GATE_LEDGER" \
  --repository <repo> \
  --commit <main-commit> \
  --max-shards <1..64>
```

普通 commit、push 或 full 运行不要求操作者先执行该命令。CLI 在解析运行场景后自动检查账本；缺少校准或稳定 runner 身份真正变化时，它在账本旁取得跨进程 `*.calibration.lock`，锁内复查并只执行一次校准。其他 Agent 等待同一把锁，取得锁后若校准已经完成就直接复用，不重复启动 ECI。锁内首先迁移同一 accepted baseline 下旧 runner identity 的耗时桶；若 commit/push/release 三套当前 catalog 已被迁移后的成功样本完整覆盖，则直接以这些既有云端样本重建校准元数据，零 ECI 返回。只有仍缺证据时才运行 cache miss 目标。校准把 commit、push、full 三个场景的真实进度原子写入 SQLite 权威文件旁的 `*.calibration.checkpoint`；schema v3 断点只保存恢复目录和接受校准所必需的 source、runner、catalog、状态、清理及 job 身份，禁止嵌入完整 RunInput、分片日志或 RunResult，文件硬上限为 4 MiB。旧 schema v1/v2 采用流式迁移并把历史 completed 状态降为可重放 partial，下一次通过 PASS 复用恢复，不能信任旧快照直接完成。断点身份绑定 commit/tree/base、平台、runner 和工具链，身份漂移时立即替换，损坏状态立即失败。某场景已有成功 workload 后，失败续跑先复用 PASS 和时长样本；场景权威完成后还必须匹配 SQLite 中同一 job 的 passed、authoritative、cleanup-complete 运行事实，才能跨协调器重启恢复；校准被接受后删除断点。历史 workload 样本在校准元数据修复和身份迁移期间必须完整保留；只有显式 `--force-rerun` 才覆盖断点复用并要求重新执行。

耗时账本同样必须有界：按稳定 workload、platform 和 toolchain 分桶，每桶最多保留最近 3 个精确执行身份；每个身份最多保留 8 个成功样本和 2 个失败样本。裁剪不改变 PASS 标记，也不允许用旧执行身份的样本冒充当前 runner；它只阻止多工作树和长期迭代无限追加无查询价值的历史行。

校准不是静态枚举，必须实际完成：

1. authoritative `git-pre-commit` tree 的全部 gate 子程序。
2. 同一 commit 相对第一父提交、`refs/heads/main -> refs/heads/main` 的 synthetic fast-forward authoritative `git-pre-push` range 的全部 gate 子程序。
3. 同一 commit 的 authoritative `release` 全量 gate 子程序；这是 release-only race gate 的权威入口。
4. 三套 authoritative catalog 中 inventory 的全部普通 Go package workload。
5. release catalog 中 inventory 每个 Go package 的独立 `-race -short -count=1` workload，以及 race guard。
6. commit、push 与 release 目录中的前端 Vitest 文件和其他 canonical gate。

校准完成标记记录 commit、tree、platform、稳定 runner identity、toolchain、commit/push/release entrypoint 和三套 catalog digest。稳定 runner identity 只由不可变 runtime image、policy digest、toolchain digest 和 accepted baseline Git tree 中的 Worker 入口及生产 Go 传递依赖闭包摘要构成。它不绑定统一 CLI 的整体二进制、runtime seed、协调器、hook、文档、`main_commit`、DataCache generation 或 baseline manifest digest；因此协调器迭代、生成物刷新和缓存层更新不会使 Worker 耗时样本失效，Worker 执行引擎或其生产依赖变化仍会准确失效。只有当前三套 catalog 的每个目录项都存在同环境成功耗时样本，且 race 包数等于 inventory Go package 数时，才允许原子写入标记；这些样本可以来自本次 authoritative job，也可以是同一 Worker 身份下已经接受的历史云端样本。失败或中断只保留样本，不得伪造完成。

校准执行时，目标正确性复用使用内容寻址 PASS 标记，耗时覆盖判断使用同 workload command、platform、runner、toolchain 的成功样本；二者不能互相冒充。校准元数据缺失但成功耗时样本完整时直接修复元数据，不要求再次读取 PASS 或启动 ECI。完成 checkpoint 在账本缺少任一目标的可比较成功样本时自动重开所属场景；协调器只把缺证据目标判为 miss，其他已确认目标继续复用。缺样本的 Go 父包必须以整包 workload 重新执行，即使历史失败超过 100 秒或已有部分顶层测试 PASS，也不能通过测试级投影绕过父包耗时采样。

普通 commit、push 和 full CI 在首代校准未完成，或 runner/platform/toolchain 身份变化时自动进入上述单飞校准；校准失败才阻断本次 CI。指定测试允许在校准前用于基础设施探测，但不能产生校准标记。后续新增或命令摘要变化的 workload 先使用显式保守初值，成功后再进入账本。

#### 4.2.1 SQLite 查询权威

`SUPER_DOLPHIN_GATE_LEDGER` 必须指向所有本机工作树共同可达的仓外文件，不能落在仓库、工作树或 Agent 私有目录。新配置应使用 `.sqlite` 后缀；为兼容已有部署，传入历史 `.json` 路径时 CLI 将同目录同名 `.sqlite` 解析为实际权威文件。SQLite 一旦存在，旧 JSON 即使变化也不能覆盖它；首次迁移采用单遍流式解析、严格字段校验和跨进程迁移锁，成功提交后旧 JSON 只作为已导入来源保留，不再参与热路径查询。

SQLite 保存需要查询、关联和统计的协调器事实：

- `duration_ledger_meta`、`duration_calibrations` 和 `duration_samples` 保存 generation、规范化校准身份及包级/测试级耗时。
- `ci_workload_catalogs`、`ci_catalog_observations` 和 `ci_catalog_workloads` 保存内容寻址目录、tree/entrypoint 观测及稳定顺序。
- `ci_workload_fingerprints` 和 `ci_workload_pass_proofs` 保存生产输入、执行环境与已验证 PASS 投影。
- `ci_runs`、`ci_run_workloads`、`ci_shards`、`ci_shard_workloads`、`ci_gate_executions` 和 `ci_run_warnings` 保存运行、分片、复用、miss、执行终态与慢项告警。

所有规划、身份、目录、运行恢复和历史统计查询必须命中显式主键或复合索引，禁止重新扫描或反序列化完整账本。写入使用 WAL、`FULL` 同步、立即写事务、有界 busy timeout 和冲突重试；generation 更新必须校验影响行数。不同工作树和 Agent 通过各自打开同一个仓外路径共享权威，不共享进程内对象。跨机器不直接共享 SQLite 文件：OSS 继续保存不可变 PASS 证明、请求、结果与日志对象，每台协调器把严格验签后的 OSS 证明投影进自己的 SQLite，后续查询走本机索引。

SQLite 是查询权威，OSS 是跨机器不可变证据，两者不能互相冒充。删除或重建本机 SQLite 只允许从受信旧账本和经严格校验的 OSS 事实恢复，不能因数据库为空把未知目标判为通过。

### 4.3 目标指纹与通过复用

每个 shardable workload 独立计算：

```text
WorkloadPassIdentity =
  execution_digest
  + production_input_digest
  + runner_environment_digest
```

`execution_digest` 绑定该 workload 的实际命令程序；`production_input_digest` 绑定精确 Git tree 中该目标可观察的生产代码、目标测试代码和必要构建输入；`runner_environment_digest` 绑定完整 `OS/arch` platform、不可变 runtime image、稳定 Worker 执行闭包 identity 和 toolchain。Worker identity 内含 policy 与 Worker 生产执行语义，但排除协调器、hook、统一 CLI 其他命令、整体二进制字节和 runtime seed。`linux/amd64`、`linux/arm64` 以及任何其他平台不得共享环境指纹。commit、完整 tree、CI 场景、batch、profile、job ID、分片号、LPT 账本和预计耗时不得进入缓存键。

输入闭包采用保守 fail-fast 规则：

- Go package 包含目标包、仓库内递归依赖包、目标测试文件、非 Go 资源、`go.mod`、`go.sum`、runtime-proxy 锁和执行脚本。
- Vitest 保守绑定整个 `frontend-app` tree，包括目标、其他测试文件、共享测试辅助代码、生产源码和构建配置；在依赖图能够证明测试间隔离之前，不得排除其他测试文件制造假命中。
- `source` guard 使用完整受版本控制 tree；三个 copylocks guard 使用对应包树及仓内递归依赖；三个 nested-module guard 只绑定目标模块与执行脚本。旧 canonical ID 只用于读取历史账本，不参与新目录分片。
- 无法解析本地依赖、路径逃逸、Git blob 不一致或出现未支持输入时直接失败，不能当作 cache miss 后静默继续。

执行顺序固定为：

1. 构造完整 workload catalog。
2. 计算每个 shardable workload 的输入和环境指纹。
3. 先以预期内容寻址身份批量查询 SQLite PASS 主键。精确命中直接复用，不访问 OSS；本机索引未知的身份才按环境前缀发起一次有界 OSS 列表请求，并在内存中与未知集合求交集。只并发下载精确命中的不可变 `.pass` 标记，严格校验 schema、环境、执行和输入身份，验签通过后立即写入 SQLite 投影；对象名存在但内容缺失、损坏或不匹配时 fail-fast，不能判为命中。列表响应超过上限、越出环境前缀或解析异常同样 fail-fast。任何缺少 `RunnerIdentityDigest` 的旧环境身份一律视为 miss；兼容旧键也必须完整绑定当前 worker 执行语义，通过内容校验后才可发布当前键。每次逻辑 `oss cp` 使用独立的系统临时 checkpoint 目录，同一次 TLS/STS 重试序列复用该目录，完成后清理；禁止并发调用共享当前工作目录下的 `.ossutil_checkpoint`。
4. 全命中时直接聚合，不生成 source delta、LPT 计划或 ECI。
5. 部分命中时只把 miss 交给 LPT，重新紧凑分片；hit 不占分片槽位。
6. 每个新通过 workload 立即写入内容寻址标记；其他 workload 或分片失败不撤销已确认的通过项。
7. 重试只执行仍未通过的目标。该规则跨 commit、push、full、指定测试及后续所有批次生效。

Go package 采用两级复用。先查询整包 PASS；整包未通过但报告中存在明确失败的顶层测试时，只为同次运行中明确 `pass` 的顶层测试发布独立标记。后续运行从精确 Git tree、runner 平台和 race 语义枚举该包实际可执行的顶层 `Test`、`Fuzz` 与有输出 `Example`，将已有标记的测试投影为复用结果，只把失败或从未完成的测试转为精确 `-run` workload。`skip`、缺少目标终态、子测试局部通过或仅有进程退出码 0 都不能发布顶层测试 PASS。测试标记仍使用该包的生产与测试输入闭包摘要，并额外绑定测试名对应的执行命令；工作树路径、Agent ID、job ID 和分片身份均不参与键计算。只要精确输入不变，任意工作树和 Agent 都可复用；生产依赖或测试代码变化、runner/toolchain 变化以及 `--force-rerun` 都回到整包执行。

显式传入 `--force-rerun` 时忽略读取缓存并把本次选中的所有 shardable workload 重新交给 LPT；成功结果仍可更新相同内容寻址标记。首代校准也遵守相同规则：默认复用身份兼容的历史通过标记和成功耗时样本，只有操作者显式要求时才强制重跑。校准 checkpoint 只保存场景进度，不得生成、推断或覆盖 `ForceRerun` 策略。

所有显式测试请求都必须先走同一套“SQLite 精确查询，未知项再验签 OSS”的指纹过滤，查询发生在“本地轻量执行或远程 ECI”后端选择之前。统一 CLI 的 `test` 子命令接受包、Vitest 文件、`<go-package>#<TestName>` 和 `<go-package>#<BenchmarkName>` 精确选择器。命中已验证 PASS 时直接返回 `backend=remote-cache`，不启动本地进程、Docker 或 ECI；不同 clone、Agent 和工作树通过同一仓外 SQLite 路径共享本机结果，跨机器通过 OSS 证明惰性建立本地投影。旧 `test_with_guard`、`go_with_guard` 和 PowerShell 测试包装器只接受远程 worker 注入的执行身份，宿主机调用在启动 Go、下载依赖或编译前 fail-fast。

本地执行是只读加速层，不是新的权威 runner。只有同时满足下列条件的 cache miss 才可使用 `backend=local-light`：

- 精确到一个顶层普通 Go `Test`，不得是包级目标、race、fuzz、example、benchmark、Vitest 文件或 canonical guard。
- 相同 runner、toolchain 和 platform 的云端账本同时存在该测试的 PASS 耗时与对应原子/包 workload 总耗时，两者最大值不超过 `1000 ms`；未知耗时直接远程执行。
- 一次请求在 OSS 指纹过滤后最多只剩一个 cache miss；多个各自轻量的 miss 也必须整组进入 ECI，避免累积占用本机。
- 当前工作区受版本控制内容与请求 tree 完全一致，且没有未跟踪文件。
- 所有工作树共享账本旁的单个本地轻量锁；锁已占用时不在物理机排队，直接切换到 ECI。
- 本地进程固定 `GOFLAGS="-p=1 -mod=readonly"`、`GOMAXPROCS=1`、`GOMEMLIMIT=768MiB`、`GOPROXY=off`、`GOSUMDB=off`、`GOTOOLCHAIN=local` 和 3 秒硬时限，顺序执行精确 `-run`。依赖/工具链缺失、未观察到目标测试终态、跳过或超时都会释放本地锁并切换到 ECI，不得在宿主机补齐前置准备。

本地通过结果必须标记 `authoritative=false`、`cloud_verified=false`，不得上传 PASS 标记或耗时样本。只有云端 worker 结果可以发布跨设备通过证明。`Benchmark` 选择器映射为独立远程 workload，使用精确 `-bench` 执行；任何包含 benchmark 或其他非轻量 miss 的请求整组进入 ECI，避免同一次请求同时争用本机与云端资源。`--force-rerun` 始终刷新远程证明，不能落到本地。

### 4.4 自动分片

规划器按预计耗时从长到短执行确定性 LPT：

1. 从 1 片开始。
2. 逐步增加到可配置的 `max_shards_per_job`；当前协议安全上限为 128，且只按实际 cache miss 目标创建分片。
3. 优先使用可用分片数降低预计关键路径；预计或实测超过 `100000 ms` 不阻断执行。
4. 每片创建一个独立 ECI ContainerGroup。
5. 预计单片可以完成时只创建一片，不为凑并发创建空容器。

若不可再分的单项工作量本身超过 100 秒，容器仍必须运行到成功或报错，并记录实际耗时。结果返回具体 workload、实耗时及“优化或拆分”告警，但不能仅因慢而把分片或整批判失败。慢目标最终通过时照常发布 PASS 指纹；下次输入和执行身份不变时直接复用，整批真实失败后也只重跑失败或未完成项。

远程 `--ci-package*` 包装器固定使用 `go test -timeout=0` 关闭 Go 的第二套硬超时；唯一终止权归 worker 已验证的 workload context，普通与 push 为 10 分钟，release 为 30 分钟。包装器不得另设 100 秒或 90 秒等更短硬超时。

Go package workload 固定以 `go test -json` 执行。worker 必须把 JSON 事件转换为普通文本日志，并为每个测试终态固定输出 `SUPER_DOLPHIN_CI_TEST_TIMING name=<test/subtest> status=<pass|fail|skip> duration_ms=<整数>`；这是一条不可由 AI 或调用者省略的 CLI 产品合同。每个 gate 最多保留 32 KiB UTF-8 日志尾部；plan report 文本传输与聚合 JSON 都保存普通可读字符串，不使用 base64，AI/CLI 默认只请求尾部。plan report v2 同时逐项携带每个顶层测试和子测试的 `name/status/duration_ms`；Coordinator 将这些测试级观测与包级观测放在 SQLite 事务中追加到 `duration_samples`，并原子递增账本 generation。测试级样本绑定父 workload ID、父命令摘要、精确测试名和执行环境；失败续跑已经使用这些事实生成精确 `-run` workload，规划器按测试历史耗时或保守初值将未通过测试重新分片，不重复消费已通过测试。

顶层测试首次从包级耗时拆分时，单次 Go 测试进程采用 `10000 ms` 最小调用成本，并在已有测试体耗时上增加 `3000 ms` 进程开销；未知目标也不得低于该下限。目标取得同环境精确实测后由样本覆盖保守值。这样 LPT 不会把大量“测试体仅 1 ms、实际容器内启动约 10 秒”的目标错误塞进同一分片。

`max_shards_per_job` 不是写死为 3。资源策略登记合法 ECI 档位、guard/Node/Go 的首次档位、seed 档位、观测余量和降档所需样本数；当前建议档位为 `2C/4GiB`、`4C/8GiB`、`4C/16GiB` 和 `8C/32GiB`。多 Agent 调用相互独立，因此并发量自然为各 job cache-miss 分片数之和；单个抢占请求只允许占用 30 秒调度窗口，超时后由 ECI adapter 自动完成“删除并确认旧组消失 -> 同规格按量创建”，不占用协调器队列。阿里云配额不足时返回基础设施失败，不得误报为门禁失败。

计算执行与控制面都没有固定宽度队列：所有 cache miss 分片可并发上传请求、创建 ECI、轮询、取日志和清理。`Throttling.User` 与 `user flow control` 按凭证瞬态抖动使用已有六次有界指数退避，权限、参数和业务错误仍立即失败；降低本机开销应通过批量查询或复用云 API 客户端实现，不得重新引入固定并发上限。

### 4.5 协调器性能事件

协调器的每个可等待阶段必须输出普通文本事件，固定前缀为 `SUPER_DOLPHIN_CI_PHASE`：

1. 进入阶段时立即输出 `event=start`、作业 ID、阶段名、观测时间和当前 workload/shard/cache 计数。
2. 阶段持续期间每 10 秒输出 `event=heartbeat` 与累计 `elapsed_ms`。`eci.wait` 心跳表示协调器仍存活、云端分片尚未汇总，不得误判为协调器卡住。
3. 阶段退出前必须先停止并回收该阶段的 heartbeat，再输出唯一 `event=finish`、`duration_ms` 和 `succeeded|failed`；finish 后禁止出现同一阶段的迟到 heartbeat。
4. heartbeat 只用于实时可观测性，不写 SQLite。只有 finish timing 进入 `ci_run_phase_timings`，用于按阶段、结果和时间窗口查询 P50/P95 及慢点。
5. 外层 `run.total` 与当前子阶段同时存活，因此正常运行的协调器不会出现超过一个 heartbeat 周期的无解释静默。观察器写入失败必须并入本次 CI 错误，禁止吞错后继续报告成功。

### 4.6 本机 CLI 自动自更新

仓外 launcher 不得继续把 `gate-client-vNNN` 写死在脚本中。它固定执行同目录、owner-only、非符号链接的 `.super-dolphin-gate-current`，并按两个进程阶段工作：

1. 先调用 current 的隐藏 `_production-update` 命令，再重新打开同一路径执行 `_production-launcher`。更新进程原子替换 current 后，本次请求立即进入新版，不需要 Agent 或人工修改 launcher。
2. 更新源只允许是配置的受信 `main` Git ref 和精确 Git object tree。活动 worktree、暂存区、未跟踪文件、候选 Makefile 或候选脚本都不能成为 updater 的源码或构建入口。
3. updater 从该 tree 的 `build/gate/inputs.json` schema v2 动态读取并严格校验 `gate_compile_inputs`，只批量读取这些 Git blob，复用 gate CLI canonical context 算法计算 `gate_source_sha256`。该摘要和当前二进制身份相同就直接执行，普通业务源码、文档、测试或项目地图变化不得触发重编译。
4. 摘要不同时只允许一个 updater 取得仓外跨进程锁。它把精确闭包写入私有临时目录，使用安装状态或显式环境解析出的 Go 工具链和共享模块/构建缓存离线编译；路径不得写死在仓库或 launcher。其他并发 Agent 不排队等待更新，可继续使用尚未被替换的上一版 current。
5. 候选二进制必须通过 `worker cli-identity` 复核源码摘要、工具链摘要和本机平台，再计算二进制摘要。全部匹配后才以同目录临时文件加 `fsync` 原子替换 current，并只保留一个 `.super-dolphin-gate-previous`；失败时不得修改 current，也不得把失败候选登记为可用版本。
6. updater 必须输出普通文本 `check|cache-hit|build|verify|switch` 阶段和耗时。成功切换后更新严格 JSON 状态；未知字段、仓库 remote/ref 漂移、工具链不可用、缓存缺失或身份不一致均 fail-fast。迁盘只需重跑安装器重建仓外路径状态，不修改仓库代码。

首次迁移允许由当前受信安装器一次性把现有已验证控制器复制为 `.super-dolphin-gate-current` 并改写稳定 launcher；从该次起，版本推进必须走上述自动闭环，不再生成持续增长的 `gate-client-vNNN` 文件。

## 5. 支持的远程场景

### 5.1 提交 CI

```bash
super-dolphin-gate remote run \
  --config "$SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG" \
  --ledger "$SUPER_DOLPHIN_GATE_LEDGER" \
  --repository <repo> \
  --scenario commit \
  --commit <commit-or-tree>
```

目标是 Git hook 捕获的精确 staged tree。未推送 commit 可以由本地对象库生成 binary-safe tree delta，不要求 GitHub 已存在该对象。

### 5.2 推送 CI

```bash
super-dolphin-gate remote run \
  --config "$SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG" \
  --ledger "$SUPER_DOLPHIN_GATE_LEDGER" \
  --repository <repo> \
  --scenario push \
  --commit <local-head> \
  --base <observed-remote-head> \
  --local-ref <local-ref> \
  --remote-ref <remote-ref> \
  --observed-remote <observed-remote-head> \
  --update-kind fast-forward
```

create、fast-forward、force 和 delete 必须显式区分。pre-push 输入中的 ref/OID 与执行结果、最终远端 identity 必须绑定。

### 5.3 全量 CI

```bash
super-dolphin-gate remote run \
  --config "$SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG" \
  --ledger "$SUPER_DOLPHIN_GATE_LEDGER" \
  --repository <repo> \
  --scenario full \
  --commit <commit>
```

必须覆盖完整 authoritative workload catalog；100 秒目标通过增加分片和定点优化实现，不通过删减必需测试或把慢任务伪造成失败实现。

### 5.4 指定测试

```bash
super-dolphin-gate test \
  --config "$SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG" \
  --ledger "$SUPER_DOLPHIN_GATE_LEDGER" \
  --repository <repo> \
  --commit <commit> \
  --test './internal/module/skill#TestLoad' \
  --test './internal/module/turn#BenchmarkRedact_NoMatch'
```

也可传入 Go package 或 Vitest 文件；它们属于非轻量目标并直接进入 ECI。精确测试或 benchmark 名称必须由目标 Git tree 的 runner 平台 inventory 验证存在，不能以“无测试可运行”的退出码 0 伪造通过。任意 shell 文本不能作为本地或远程命令下发。需要强制全云执行时仍可使用 `remote run --scenario test` 的相同选择器协议。

上述四种场景都支持 `--force-rerun`。默认不重跑生产输入和执行环境均未变化的目标。

## 6. Git Hook 与请求指纹接入

薄 hook 只负责：

1. 读取 Git 提供的精确 tree/ref/OID。
2. 调用统一 CLI。
3. 等待相同 invocation 的结构化结果。
4. 校验 result/receipt 与请求 tree、profile、plan 和远端动作身份一致。
5. 失败、超时或结果不完整时阻断 Git 动作。

Git hook remote 模式由 `SUPER_DOLPHIN_GATE_MODE=remote` 或 `super-dolphin.gate.mode=remote` 启用，环境变量优先；缺省为 `local`，未知模式 fail-fast。完整键集合如下：`SUPER_DOLPHIN_GATE_LAUNCHER` 仅供安装时写入 `superdolphin.gateLauncher`；`core.hooksPath=.githooks`；`SUPER_DOLPHIN_GATE_REMOTE_CONFIG`/`super-dolphin.remote.config`、`SUPER_DOLPHIN_GATE_LEDGER`/`super-dolphin.remote.ledger` 为远程 hook 必填；`SUPER_DOLPHIN_GATE_REMOTE_STATE`/`super-dolphin.remote.state` 可选；`super-dolphin.remote.maxShards` 可选并传为 `--max-shards`。远程 pre-commit 仍先 closure check，传 initial tree 与 parent，返回后重读 tree；远程 pre-push 保留 Git stdin 的每条 ref update 并逐条执行 canonical range。两者均要求 authoritative、精确 entrypoint/profile/source tree、passed 与 cleanup complete，否则拒绝 Git 动作。

仓库不安装 `.codex/hooks.json`，不解析任何厂商的 lifecycle payload，也不把 `agent_id`、session、turn、`cwd` 或进程号固化为门禁协议。入口无关的逻辑 Agent 身份只由 CLI 生成的 requester fingerprint 表达：调用者先执行 `super-dolphin-gate requester create`，再通过统一参数 `--requester-fingerprint` 或环境变量 `SUPER_DOLPHIN_GATE_REQUESTER_FINGERPRINT` 携带一个 `sha256:<64-hex>` 指纹。Git hook、直接 CLI 和任意 Agent 客户端都透传同一字段，远程结果回显该字段，SQLite 使用独立索引支持 `requester runs` 查询最近 job。显式参数与环境变量冲突时 fail-fast；缺失时保留“未声明 requester”，不得自动把机器、工作树或 session 当作同一 Agent。

requester fingerprint 是可声明的关联键，不是 bearer authorization。它不得进入 worker request、workload PASS、生产输入闭包、runner identity、owner、grant 或 receipt 授权判定；更换、伪造或缺失该字段都不能取得额外权限，也不能使生产输入未变化的 PASS 失效。违规通知使用 CLI 退出码和结构化结果返回当前调用方；不同 Agent 产品可以在仓库外把该通用结果投影到自己的 UI 或生命周期，不得反向要求门禁二进制依赖某个 Agent 厂商。

## 7. 仓外配置

Coordinator 主机必须安装 Alibaba Cloud CLI。DataCache 创建使用已实网验证的 OpenAPI `CreateDataCache` flat 参数，并显式传入 `--method POST --force`；不得把 `DataSource` 作为单个 JSON 字符串发送，也不得使用当前会在 STS + map 键场景产生签名错误的插件调用。资源地域与 CLI profile 的 STS endpoint 独立配置；ECI、DataCache、OSS adapter 对 `Throttling.User`、TLS 握手超时、网络 timeout 和瞬态 EOF 最多重试 12 次，500 ms 指数退避并封顶 8 秒，等待必须响应 context 取消。每次重试复用稳定参数和幂等 token，永久错误不重试。所有 CLI 错误必须先移除 AccessKey、SecurityToken 和 Signature 值，再进入日志或上层结果。

生产配置保持在仓库外，schema 为 4，包含：

- Aliyun CLI 路径或命令名。
- credential profile 名称，不包含 AccessKey。
- `cn-shenzhen`、VSwitch、SecurityGroup、worker role。
- OSS 公网/内网 endpoint、bucket 和对象前缀。
- 不可变 Ubuntu 22.04 runtime digest。
- DataCache bucket、path prefix、Anchor 容量上限、retention 和 `1440` 分钟刷新间隔。CLI 必须先下载并验签 accepted Anchor manifest，再按“实际压缩产物向上取整 + 5 GiB、最低 20 GiB、最高为 `max_size_gib`”计算目标容量；目标容量变化必须强制生成新 Anchor，禁止继续复用旧容量或追加 delta。刷新期间普通 CI 仍可读取上一代 accepted Anchor，新代可用并原子接受后才切换。
- `max_shards_per_job`、`seed_class`、资源档位列表、guard/Node/Go 首次档位、headroom 和降档最小样本数。

路径不得写死在源码或镜像内。跨机器部署时只替换仓外配置；代码、协议和 DataCache 布局保持一致。

### 7.1 当前深圳网络基线（2026-07-31）

VSwitch 的 CIDR 创建后不可修改。容量不足时必须在同一 VPC、同一可用区创建不重叠的大网段 VSwitch，再原子切换仓外配置；不得把新资源 ID 或机器绝对路径写死在源码、镜像或默认配置中。

- VPC：`vpc-wz9d1dq1jk8j7f9dkj3dt`，CIDR `172.16.0.0/16`。
- 可用区：`cn-shenzhen-e`。
- 当前 VSwitch：`vsw-wz94cm8m9vvop78so2icf`，CIDR `172.16.16.0/20`，创建完成时可用地址数 `4092`。
- SecurityGroup：`sg-wz954uxx0f4p0nr3qb79`。
- 旧 VSwitch：`vsw-wz9h5xuapuy5c03d435c8`，CIDR `172.16.1.0/24`。迁移验证期间保留，只在确认没有运行中或可恢复任务引用后另行回收。
- Coordinator 网络迁移权限显式包含 `vpc:DescribeVpcAttribute` 和 `vpc:CreateVSwitch`；日常 ECI 调度仍只读取仓外配置选中的 VSwitch。

迁移后的真实 ECI 已从新 VSwitch 创建并完成两次增量验证：

- `eci-wz9en0qj2twhxle4z70m` / `job-0cb9d3b7bb57fbd32149c4c7` 绑定候选 tree `64364492ef988f78e7a90f9ced6761f479c86032`，5 个此前失败或取消的缓存与分片精确测试全部通过。
- `eci-wz94t30a8698ruhvwh0z` / `job-bb2d8b3a641e0455ab96c5ef` 绑定候选 tree `2c70cc65734d0abdc0350e646f32713d57e2de1d`，只重跑生产输入发生变化的 2 个 CLI 编译闭包精确测试并全部通过。

两次运行均为 `cleanup_complete=true`。该证据只证明网络迁移和这 7 个聚焦目标，不把非权威候选运行提升为整项 `VERIFIED`。

## 8. 测试取舍

保留：

- ECI、OSS、DataCache adapter 参数与严格响应测试。
- baseline refresh/reuse/renew/promotion/cleanup 测试。
- binary tree delta 和 exact target tree 复验测试。
- workload inventory、耗时账本、LPT 和 100 秒合同测试。
- shard request、ECI 生命周期、结果聚合和清理测试。
- Git hook tree/ref/receipt 身份测试。
- closure 和受信 Git tree 完整性测试。

删除或不再进入本任务验收：

- 需要本地 Docker daemon 的旧 bootstrap 镜像 smoke。
- ACR repository/build rule/tag promotion 测试。
- 每个 worktree 独立镜像/cache 测试。
- 与已删除本地真相镜像方案一一绑定、且没有 ECI 信任合同价值的 fixture。

不得仅因测试慢就删除仍覆盖 ECI 必需合同的测试。慢测试应进入 workload 账本并拆成可并行 workload。

## 9. 当前实现边界

- 唯一独立二进制 `super-dolphin-gate` 承载 coordinator、materializer、worker 和 runtime-seed，不与生产后端共用目录或进程。
- 本地与云端使用同一套 CLI 协议；本地只负责 Git/tree 绑定、缓存判定和调度，重型 workload 在 ECI 执行。
- 每个分片对应一个 ECI 容器；单作业分片数、CPU 和内存档位由仓外配置决定，不设跨 Agent 的本地固定并发队列。
- 缓存判定先于分片规划。只有未命中的 workload 进入 LPT；全命中直接返回通过，不上传源码、不创建 ECI。
- PASS 以目标输入、执行契约和 `linux/amd64` 环境身份绑定，跨工作树共享；`--force-rerun` 是唯一主动绕过方式。
- 失败批次保留已经通过的目标和已完成分片事实，重试只调度未通过或输入已变化的目标。
- 100 秒是优化和自动拆分目标，不是杀容器的超时；超过目标必须记录具体 workload 耗时并返回非阻断告警。
- accepted baseline 使用 current + previous 链。候选刷新期间继续服务旧链；候选验收并原子持久化后，才允许按引用差集回收旧资源。
- Git hook 和任意 Agent 客户端只能调用仓外受信 launcher/已编译 CLI；候选 tree 的 Makefile、生成器或脚本不得成为门禁执行入口。
- requester fingerprint 跨 Git hook、直接 CLI 和其他 Agent 客户端保持同一逻辑 Agent，只用于审计、续跑和请求归并；它不得改变 PASS 命中，也不得替代 owner/receipt 授权。

## 10. 当前场景验收

本任务只验证当前实现和当前精确 Git tree 是否满足场景，不把历史 generation、旧 JSON/SQLite 行、旧 receipt 或过去的屏幕进度当作验收对象。无需重建完整历史、比较历史账本的每一行，也不因当前 SQLite 文件大小单独做优化。

在以下场景全部闭环前，状态保持 `NOT_VERIFIED`：

1. `test`：指定一个或多个精确测试。首次未命中时只创建对应 ECI；相同目标立即复跑必须由共享 PASS 返回且 ECI 为 0；`--force-rerun` 必须重新执行。
2. `commit`：pre-commit 绑定精确 staged tree；closure/project-map 只通过受信 CLI 自动刷新一次，刷新后仍漂移才阻断；远程结果必须绑定同一 tree。
3. `push`：pre-push 保留 Git stdin 的每条 ref update、远端名称和 URL，按 canonical range 执行；任一 range 失败即阻断。
4. `full`：对当前 catalog 先做目标级缓存过滤，再只对 miss 做分片；超过 100 秒只告警并为下一次规划提供拆分耗时。
5. 失败续跑：制造一个目标失败后，账本必须保留同批其他 PASS；修复后只执行失败或指纹变化的目标。
6. 跨工作树：不同绝对路径下相同目标输入和环境身份必须命中同一 PASS；工作树路径不得进入缓存身份。
7. 基线刷新：候选构建、验收或状态写入失败时 current/previous 旧链仍可用；delta 刷新不得删除仍被新链引用的 Anchor 或旧 delta。
8. 云端鲁棒性：STS/TLS/瞬时网络错误有界重试且响应取消；大型对象下载不受短握手预算误杀；日志按普通文本尾部读取。
9. 清理：所有已创建 ECI、临时 EIP 和临时 OSS 对象都进入可恢复清理；未创建的分片占位不得伪造成云端事实。
10. requester fingerprint：CLI 生成后，同一指纹经 Git hook、直接 CLI 和任意 Agent 客户端提交的运行可按逻辑 Agent 查询和续跑；仓库不存在 Codex 专用 lifecycle 入口；更换或缺失指纹不影响相同生产输入的 PASS 复用，伪造指纹也不能取得 owner、grant 或 receipt 权限。

`VERIFIED` 只由当前 tree 的 LSP 零诊断、当前聚焦合同测试、真实 ECI 场景结果和紧随其后的同 tree 缓存复跑共同证明。历史成功记录可以辅助定位，但不能替代本轮证据。
