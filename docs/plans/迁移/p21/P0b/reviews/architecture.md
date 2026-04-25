# P0b Architecture Review (2026-04-25)

## Correctness

- **major** — `internal/module/turn/skill_extractor.go:364`
- `flushOnce` 先 `collector.Drain()` 清空 completed 队列，再逐条 `Extract`；`ctx` 取消、`Extract` 返回错误、或 panic recover 后，该 trajectory 都不会 requeue / retry，实际是永久丢弃。
- 期望写法：collector 提供 ack/requeue 语义，或 runner 只在成功 / 明确不可重试 skip 后 ACK；panic / transient error / ctx canceled 的未处理项应保留或重新入队。

- **nit** — `internal/module/turn/module.go:60`
- `NewDefaultExtractor` 注释和构造函数支持 `logger == nil` fallback，但 fx tag 第 5 个参数不是 optional。app graph 当前有 `NewLogger` 所以不炸，但“partial wiring”语义不一致。
- 期望写法：若真要 partial graph 可用，`fx.ParamTags(..., optional logger)`；否则删除 fallback/注释里的 partial-wiring 承诺。

## Security

- **major** — `internal/module/turn/redaction.go:89` + `internal/module/skill/trust.go:66` + `internal/module/turn/skill_extractor.go:197`
- `RepoFingerprint` 在 `turn` 和 `skill` 各有一份实现，算法不一致：turn 是 abs + sha256 前 12 hex；skill 是 abs + clean + EvalSymlinks + 前 16 hex。P0b 的 approval / candidate isolation 依赖 `repo_fingerprint`，插入端和审批/lookup 端可能永远对不上；较短指纹也弱化 repo 隔离。
- 期望写法：把 fingerprint 生成抽到 `internal/contract` 或 `internal/platform` 的单一函数，turn extractor、skill approval、prompt catalog 全部复用同一实现。

## Readability / Architecture

- **major** — `internal/module/turn/service.go:16` + `internal/module/turn/observation_wiring.go:9`
- `turn` 上层直接 import `internal/module/turn/observation`，并调用 `SetSkillsSelected` / `MapTurn`。这直接违反 P0 文档的 `turn/tracker 不得 import observation` 和单向 push 边界。
- 期望写法：turn 只发 bus DTO / neutral contract event；observation subscriber 独立消费并写入 observation memory。若必须同步写，接口也应放在中立包，不能让 turn import observation 子包。

- **major** — `internal/module/turn/trajectory_collector.go:219`
- collector 不只是读取 observation；它调用 `AttributeCall` 和 `Dedupe` 写回 observation state（`trajectory_collector.go:228`, `trajectory_collector.go:291`）。这把方向变成 `collector → observation`，破坏 `observation → collector → consumer`。
- 期望写法：observation subscriber 独占写入 `AttributeCall/Dedupe`；collector 只依赖 read-only interface，例如 `LookupCall/Terminal/Tokens/SkillsSelected`。collector 自己需要的 begin/end dedupe 应放本地 map。

- **major** — `internal/module/turn/observation/contract.go:105`
- `Contract` 同时暴露读写方法，导致任何 consumer 都能写 observation，架构不变量无法由类型系统守住。
- 期望写法：拆成 `Reader` / `Writer`，`Memory` 可同时实现；fx 对 collector / insight 只提供 `Reader`，对 observation subscribers 才提供 `Writer`。

- **minor** — `internal/module/skill/module.go:54`
- `registerCandidateStores` 用类型断言 + setter late-bind candidate/audit store；未来替换 `Service` 实现时会静默跳过注入，编译期不报错。
- 期望写法：fx constructor 用 `fx.In` 接收 optional `skillcandidate.Store` / `auditlog.Store`，构造完成后再返回 `Service` interface；不要通过 post-constructor 类型断言补依赖。

- **minor** — `internal/module/skill/contract.go:94`
- `Service` 已经是大接口，又把 candidate review 的 4 个方法塞进去，导致无关 consumer/fake 也被迫满足 review gate contract。
- 期望写法：拆出 `CandidateReviewer` / `CandidateApprovalLookup` 子接口，RPC review handler 和 P0b lookup 只依赖子接口；主 `Service` 保持 skill filesystem / expand 能力。

## Performance

- **minor** — `internal/module/turn/trajectory_collector.go:82`
- `drained map[string]struct{}` 生命周期内无界增长。长时间运行 + 高频 turn 会持续占内存。
- 期望写法：用 TTL/LRU 清理，或在 terminal/drain 后记录有限窗口；至少把 bound 做成明确参数并加测试。

## fx / import graph 验证

已跑：

- `go list -deps` / `go list -json` 检查 turn / skill / store / observation 依赖
- `go test ./internal/app -run TestAppModuleGraphIsClosed -count=1`
- `go test ./internal/archtest -run TestFxValidateApp -count=1`

结果：fx app graph 当前闭合；未发现 `group:"runners"` 引入 fx cycle。`NewTrajectoryCollector` 和 `NewDefaultExtractor` 的 DreamExecutor / Store optional tag 顺序与签名匹配。

Import graph 示意：

```text
turn
-> module/skill
-> module/turn/observation        // 违反 P0: turn 上层反向依赖 observation
-> store/skillcandidate
-> store/turndedupe

skill
-> store/auditlog
-> store/skillcandidate
-> no module/turn import

store
-> store/* subpackages only        // root assembler 形态健康

turn/observation
-> no module/turn
-> no module/skill
-> no store
```

额外 grep：

```text
app -> turn/observation              // root module assembly，合理
insight -> turn/observation          // downstream consumer，合理
turn/service.go -> turn/observation  // 不合理
turn/trajectory_collector.go -> turn/observation with writes // 不合理
```

架构 verdict：`STRUCTURAL_BUG:3`
