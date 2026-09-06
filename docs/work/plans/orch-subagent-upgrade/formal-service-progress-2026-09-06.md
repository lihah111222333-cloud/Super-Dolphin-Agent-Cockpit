# 正式服务运行进度 — 2026-09-06

Status: Active — Formal Runtime Progress, Full Upgrade Incomplete

## 原目标，不另定一个较小终点

遵循[独立单实例执行计划](../2026-08-31-orch-standalone-upgrade.md)的完整目标：
orch 脱离 V3，以同状态域唯一 owner 服务接入官方 Claude/Codex，完成真实任务、跨任务协作、
授权、等待、恢复和适用验收。`serve` 本机 operator profile 已存在，不代表 strict `unified_v2`
及整份升级完成；RecoveryCustody 的外部依赖未接通也不意味着已有 `mcp-orch serve` 无法启动。

工作树为 `/Volumes/hello/wj/super-agent-v3/.worktrees/20260831-orch-subagent-upgrade`，
HEAD `d95c6379812d9d9b95e8831c4713d219752e3eaa`。编码子 agent 使用 Astra/low，主 agent
亲自复核、运行验证并裁决；未提交、合并或推送。先前候选文件及其他 dirty 改动保留。

## 本轮真实结果

| 目标/入口 | 结果 | 准确边界 |
|---|---|---|
| `TestMCPOrchServeIndependentStateDomains` | PASS，约 0.96s | 两个真实 daemon/独立 state 同时存活；停止 A 后 B 原会话可继续。不调用模型。 |
| `TestStandaloneCodexClientLaunchesManagedCodexThroughMCP` | PASS，增强收敛与 Git 隔离后约 18.58s | 真实 binary、官方 Codex client、managed Codex 首轮/后续 turn，使用 `gpt-6-astra`；此前 16.67s 是前一源码快照。 |
| `TestClaudeFirstTurnBootstrapsInit` | RED→GREEN | 真实受控 child 在读首 user 前不发 init；原实现准确报 unavailable/busy；修改后首输入、错误 session init、active-turn 防重、真实 init/readiness/terminal 顺序正确。 |
| `TestClaudeFirstTurnProtocolContract` | PASS | 首消息、result-before-init、init-before-attachment、terminal receipt retry 四条窄回归。 |
| `TestStandaloneBinaryLaunchesManagedClaudeThroughMCP` | 未通过 | 原首输入代码错误已越过；当前等待模型报告超时。同 CLI 独立请求也产生 HTTP 503 api_retry。 |
| `TestClaudeProviderProbeControlContract` | PASS | typed request/response、反射字段检查、关联 ID、坏帧/重复字段/大小、取消与退出分类。 |
| Probe request-ID 临时破坏 | 有效 RED，恢复后 GREEN | 删除真实关联 ID 比较，RequestID one-hot 与 foreign-ID 测试均实际失败；源文件按原字节恢复。 |
| `TestStandaloneOfficialClaudeProbe` | PASS，约 1.27s | 官方 Claude 2.1.237，无模型 control initialize 与主动关停；不是模型执行或生产授权。 |
| `TestStandaloneManagedProcessReapingEvidence` | PASS | 旧 PPID 反例、reparent、同 birth token 换 exe、NotFound、PID reuse、读取错误和空快照。 |
| `TestCLIEnvironmentKeepsGitWorkspaceBoundary` | RED→GREEN，约 0.08s | 真实 Git 在两个临时仓库验证 cwd 不能被父 Git 环境改路由；两 provider、动态 local-env-vars、indexed config、认证/作者/home 保留均覆盖。 |

所有表中 host-test PASS 均由原 `scripts/test_with_guard.sh --host-test light|medium`
精确 selector 执行，包含原 code guard、选定依赖守卫、负载准入和后置检查。
本地运行结果不是 Windows、远程、全部 provider 能力或发布凭证。

## 修复因果链

### 1. 正式 Claude 首消息的循环前置

实际 MCP `launch_agent` 返回 `Claude session is unavailable or busy`。源码
`standalone/execution_claude.go:submitClaude` 在发送首条 user 前要求 `claudeInitialized`，
但 provider 的 `system/init` 在首条 user 后到达。修复只移除此错误投递前置：真实 process
非空、stopped/failed 和同锁内 active-turn 占位仍保留；没有提前设置 initialized/ready。

测试不再让 fixture 先发 init：child 先通过独立控制连接确认 awaiting-user，再等实际 stdin；
错误 session init、匹配 init 和 result 均由明确屏障放行。原错误已经由真实测试体复现。

### 2. Probe 是另外一个缺陷，不虚构生产调用关系

当前 `ProbeConfiguredProviders/ProbeProviders` 没有正式运行根 caller；它们的错误不能被
拿来解释所有 launch 失败。旧 probe 的 `--session-id orch-probe` 被真实 CLI 直接拒绝为非 UUID；
即使改成 UUID，空 user 也不是可靠的无模型握手。

新 probe 使用两个独立 UUID 和官方 control initialize/关联 response；协议依据
[官方 SDK initialize 实现](https://github.com/anthropics/claude-agent-sdk-python/blob/main/src/claude_agent_sdk/_internal/query.py)。
本项目仍以 Go 直接管理 CLI，没有引入 Python SDK runtime。response 动态能力对象允许扩展，
稳定 envelope/关联字段严格检查，不输出 account 或 response 正文。

真实 CLI 将本探针主动 TERM 转为 143，旧 cleanup 会错误报告失败。现在仅在明确主动停止条件下
接受对应 TERM/KILL 信号及观测到的 TERM=143 映射；非主动 143、普通 1/7 和 SIGSEGV 不豁免，
管道错误继续保留。5 秒探针与 3 秒停止升级期限未放宽。

### 3. 进程回收验收不能只看旧 PPID

daemon 退出后 child 可被 reparent 到 PID 1。旧检查此时可能看到零后代，却漏掉仍存活的进程。
增强后保存关停前已观测 PID/start token/executable identity，并逐 PID 重新读取。
只有明确 NotFound 或新的非空 start token 才证明原进程退出；同 token 换 exe 仍算活着。
观察器不发送信号，不把权限/读取错误当作退出，必须有非空 provider 身份证据。

它仍只证明已观测集合，不能覆盖所有未来后代或未经本轮执行的平台。这是对验收范围的明确说明，
不是删除 ST-16 的剩余要求。

### 4. 指定 cwd 不能替代 Git 环境隔离

`cliEnvironment` 原来仍继承 `GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE` 等父进程上下文。
真实 Git RED 显示，两个 provider 分支即使 `cmd.Dir=repoA`，仍解析到另一个临时 repoB。
现在只剥离 Git 自描述的仓库局部变量及 indexed config key/value；保留认证、作者等非定位环境，
原有 V3/DB 剥离和显式 provider home 不变。生产过滤不运行 Git，测试动态枚举 Git 的变量集合
以发现未来漂移。新回归 GREEN 后，又重跑了真实 Codex 双 turn 与 stable process 收敛。

原主包 E2E 的 `serveE2EEnv` 使用环境白名单，未继承这些 Git 变量，因此不能宣称此前临时
review index 已污染那些 E2E；该问题针对实际服务从一般父进程环境启动的生产路径。

## CLI 来源与外部状态

- 本轮实际 binary：Codex 0.153.3、Claude Code 2.1.237，均为当前安装的 Mach-O arm64。
- Homebrew 入口是 symlink。配置校验明确要求实体普通文件；采用显式解析后的真实 executable，
  没有关闭校验。初次 Codex E2E 配置了 symlink 而在 ready 前退出，修正输入后正式链路 PASS。
- Claude 使用操作者既有 home 和明确模型 `claude-opus-5`。独立 CLI 请求已观察到
  `system/init` 及 `api_retry.error_status=503`；这与首消息代码错误是不同问题。
- 未改账户、凭据、模型或原测试超时来制造通过。官方 CLI 自己可能写正常运行状态，
  本轮没有手动迁移或覆盖个人配置。

操作说明见[运行手册](../../../reference/orch-standalone-runbook.md)。

## 下一执行依赖与尚未完成项

1. 当前 Claude 模型响应仍需真实成功重验；无模型 probe PASS 不能代替它。
2. 正式 identity 激活 → canonical runtime EventStore 的生产调用链仍须接通；构造图中的
   owner 存在，不等于 runtime 已写入权威事件。
3. CONTENT/RESOURCE/OUT 的真实 owner/adapter、外部 custody/finalizer/verifier/trust root、
   accepted/attempt/permit/nonce/unknown reconcile 必须落到正式执行链；不能用测试替身作生产证明。
4. `unified_v2`、durable wait、task credential 与跨任务 grant、Room、跨进程 crash/recovery、
   V3 可选客户端兼容及发行验证仍按原计划推进。
5. 将文档/门禁当可验证实现，不把静态 Draft 或局部 PASS 当作终点；若规则与真实边界冲突，
   先证明冲突并修正规则，不通过隐式 fallback、假回执或修改基线遮盖问题。

原 ST-01～ST-18 及适用总规格继续是完整验收集合；本报告不提供全量完成百分比或发布 GO。

## 本机证据位置

`/Volumes/hello/wj/.codex-cache/orch-takeover-20260905/formal-service/`：

- `logs/domains-first.log`、`logs/codex-formal-final.log`；
- `logs/claude-bootstrap-red.log`、`logs/claude-bootstrap-green.log`；
- `logs/claude-formal-first.log`、`logs/claude-formal-fixed.log`；
- `logs/claude-probe-contract-final.log`、`logs/claude-live-probe-final.log`；
- `logs/reaping-contract.log`、`claude-api-retry-diagnostic.json`；
- `logs/git-isolation-red.log`、`logs/git-isolation-green.log`；
- `probe-field-mutation-sr88_k44/host-test.log` 与 `result.json`；
- `provider-provenance.json` 保存实体路径与哈希，不含账户凭据。

这些是本机可追溯日志，后续新源码/新 binary 必须重新绑定验证，不能永久继承本次 PASS。
