# Orch 独立单实例部署设计

Status: Proposed

负责人：当前 Codex 根任务；创建日期：2026-08-31。

用户已明确产品方向：脱离 V3，单实例接入 Claude Code 与 Codex CLI。本文承接该方向，精确接口和安全合同仍须评审；它不把旧总规格的 NO-GO 自动改成生产 GO。

## 唯一 owner 与接入

一个配置/私有数据目录对应一个长期存活的 `mcp-orch serve --config <绝对路径> --ready-file <绝对路径>` 进程。启动先校验配置并取得跨进程内核锁，再迁移、装配 domain owners、监听、发布 ready。第二次启动同目录在任何迁移/监听/agent 副作用前失败。锁持有到全部执行器、writer 和 listener 关闭；不以 PID 文件、超时或端口占用推断 owner 死亡，不删除持锁文件。

Claude Code、Codex CLI、V3 作为 MCP 客户端连接同一 authenticated Streamable HTTP endpoint。每个连接有独立 session/request/cancel/auth scope；业务任务、消息、事件和执行器全部在服务侧。HTTP session 断开不等于停止 agent。可选 stdio `connect` 只做协议连接，不包含数据库、迁移、调度器或 provider 执行；没有 daemon 时不能静默回退成另一套 standalone core。

### 单实例的准确边界

单实例限定的是一个私有状态域的编排核心，不是全机器只能运行一个 orch，也不是只允许一个客户端或一个子 agent。两个刻意隔离的状态目录可以分别拥有服务，但不共享数据库、进程句柄或隐式跨域权限。同一状态目录即使经不同配置文件或路径别名接入，也不能产生两个 writer；只检查配置文件名或监听端口不能满足此条件。

```text
Claude Code ─┐
Codex CLI ──┼─ MCP 客户端连接 ─ orch daemon（同一状态域唯一 owner）
V3（可选） ─┘                    ├─ 任务、消息、事件与权限
                                └─ 多个受管 Claude/Codex 执行进程
```

客户端退出只释放自己的连接与请求资源；停止受管任务必须提交显式业务命令并经过授权。服务崩溃后的重新连接、任务恢复和 owner 接管是三个不同步骤：取得进程锁不代表旧任务已经恢复，读取 ready 文件也不代表旧实例仍存活。

跨任务对话发生在服务内的逻辑任务之间，不绑定某个 MCP 连接。同一个 daemon 让任务寻址、消息入库与事件顺序有共同 owner，但不自动授予客户端读取/发送其他任务的权限。Claude 与 Codex 可以同时接入且保持各自会话；它们是否能控制既有原生子任务，仍取决于显式绑定、权限和已验证 provider 能力。

本方案不直接启用当前标为 legacy 的 HTTPServer。必须复用其可验证的协议构件并补齐正式 method/session/auth/bounded concurrency 合同，不新增绕过 schema/鉴权的 adhoc HTTP API；MCP 方法不借 ctl 回调执行。

## 独立启动输入

配置候选 schema 固定为显式 `schema_version`、`workspace_root`、`state_dir`、loopback `listen`、私有 `credential_file`、canonical `principal_ref`、canonical `scope_ref` 和非空 `providers`；provider 声明 name、绝对 executable、home 和允许模型集合。未知/重复字段、相对路径、缺失凭据或未声明 provider 拒绝。`principal_ref/scope_ref` 只生成 local operator transport identity，不是 child/task principal issuer，也不自动授予任意 agent 的读写权限。凭据值不写入 ready 文件、日志、测试报告或命令行。服务不扫描/修改用户现有 Claude/Codex 配置，不通过 V3 环境变量补缺。

ready 只在健康启动后以私有文件原子发布 endpoint 与 instance ID；它是发现信息，不是授权证据。真实运行状态以持锁进程与服务握手为准。第一阶段只开放 loopback；远程暴露必须另行批准 TLS、auth、egress 和 A2A 边界，不把 `0.0.0.0` 当默认。

## 资源与执行器

产品数据库复用唯一规范 SQL migration 源，以 embed/`io/fs` 提供显式发行资源入口；不得复制 SQL 或要求安装 V3/保留项目 checkout。独立 profile 通过显式构造注入配置、模型策略和同一 registry/service，不加载 desktop app，不导出虚假的 `GO_AGENT_CTL_RPC_ADDR`。singleton、writer epoch 和 migration leader 各自职责明确，进程锁不能冒充恢复域外授权 receipt。

RecoveryCustody另行冻结为外部单实例服务独占私有SQLite DB：orch只能通过认证协议client访问，不能打开、迁移或共享authority文件，也不能复用产品主SQLite或heteroci PostgreSQL。当前已有三层digest、SQLite完整性join、revision-3 DB-resident external-service mode/writer_epoch/takeover、`PinnedDirectoryLock`+`KernelAuthorityLease`、typed observation/同key reconcile和非MCP loopback `rpcwire`/`authorityhttp`/`httpclient`候选；架构守卫禁止普通orch import/open authority DB。仍缺独立服务进程/CLI/supervisor/配置发布的生产接线、network FS证明、真实外部EvidenceFinalizer/anchor/签名trust root、durable coordinator job/journal/startup scan和production orch client/Fx/OUT接线。production composition继续unavailable/reject-all。

执行引擎仍是官方 CLI：Codex 使用它的 app-server 机器协议，Claude 使用它的官方 CLI 机器接口。CLI home/模型/能力由配置和真实 probe 确认；fork、注入、停止和恢复按已证明能力启用。MCP 客户端接入成功不等于获得接管客户端自身 native child 的权限；用户启动的 root task、orch 创建的 managed task、观察到的 native child 必须分别登记，不自动混为 fully controlled。

同域跨 provider 的最终目标是经统一工具/Room协作；当前已有task attribution、task-only HTTP session认证、内存credential owner、current task principal/canonical task event source候选，但没有第二task listener、managed CLI credential注入、identity/Fx激活链、真实event producer或`unified_v2` public list/wait/send。legacy send/directpeer不能因此升级为canonical cross-task能力。跨owner/host/security domain才走A2A；V3只消费/展示结果，不继续拥有standalone的binding、policy seed或业务数据库writer。

## 取舍与接受条件

选择单实例服务，是为了唯一进程/事件/outbox owner。拒绝每 CLI 各起完整 core 后共享 SQLite；共享文件不能安全路由运行中的 session/process handle。也不把 V3 app/runtime 捆绑进一个壳进程来声称独立。

规格接受条件：明确上述 startup/transport/identity/grant/custody 子合同及总规格 ancestor 的机器依赖，通过针对本修订的评审；用户已给出的单实例、第三方 CLI、脱离 V3及外部单实例SQLite custody方向不反复征求确认。实现验收/发布条件另为 [执行计划](../plans/2026-08-31-orch-standalone-upgrade.md) 的 ST-01～ST-18 及适用总规格测试通过，不以“测试全部完成”作为开始编写测试的循环前置。任何缺失项保持未完成；若用户改变部署方向则记录替代方案并迁出本提案，不留下隐式规范。

## 协议参考（2026-08-31 核对）

- [Codex MCP 接入](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)：CLI 可配置 STDIO 或 Streamable HTTP endpoint。
- [Codex App Server](https://learn.chatgpt.com/docs/app-server)：CLI 自身提供双向机器协议；生成 schema 与安装版本绑定。
- [Claude Code MCP](https://code.claude.com/docs/en/mcp)：支持外部 MCP 服务；接入配置必须与真实客户端测试一致。

本机只读版本探测：Codex CLI 0.150.1、Claude Code 2.1.237。版本号/文档不能替代实际 E2E，也不证明所有 native-child control capability。
