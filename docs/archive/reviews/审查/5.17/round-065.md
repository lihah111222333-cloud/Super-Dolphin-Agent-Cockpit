# Round 065 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 09:02:49 KST
- 结束：2026-05-17 09:05:27 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 turn/prompt 技能注入链路、selected skill hydration、expanded artifact state、Codex host-direct `skill_read_section` 与 provider 输入映射。重点看审批状态、artifact fresh 状态和实际模型输入之间是否闭环。

- `internal/module/turn/skills.go`
- `internal/module/turn/expanded_state.go`
- `internal/module/turn/service.go`
- `internal/module/turn/contract.go`
- `internal/module/turn/service_skill_hydrate_test.go`
- `internal/module/turn/expanded_state_test.go`
- `internal/dto/provider/turn.go`
- `internal/provider/codexapp/session_turn.go`
- `internal/provider/codexapp/input_map_test.go`
- `internal/provider/claudecli/session_turn.go`
- `internal/platform/toolbridge/skill_read_section.go`
- `internal/platform/toolbridge/host_tools.go`
- `internal/module/skilllibrary/section.go`
- `internal/app/toolbridge_adapters.go`
- `internal/contract/approval.go`
- `internal/contract/toolbridge.go`

## Findings

1. **[major] `skill_read_section` host tool 不检查 ApprovalSource，模型可直接读取缓存 section**
   - 证据：`SkillReadSectionRegistry.CallHostTool()` 只 decode args 后调用 `r.tool.readSection(args)`（`internal/platform/toolbridge/host_tools.go:120-146`）。`SkillReadSectionTool.readSection()` 只用 `reader(cacheDir, name, anchor)` 读文件、截断和 FBSD 打点（`internal/platform/toolbridge/skill_read_section.go:64-80`）。虽然 skill service 实现了 `contract.ApprovalSource`（`internal/module/skill/contract.go:100-104`，`internal/contract/approval.go:50-55`），但 toolbridge wiring 只注入 cacheDir/reader/recorder，没有注入审批源（`internal/app/toolbridge_adapters.go:40-59`）。
   - 风险：上一轮发现 `skill/expand` 的审批路径存在断层；本轮进一步确认 Codex 生产可见的 `skill_read_section` 完全绕过审批源，只要 section 已在 skilllibrary cache 中，模型就能读取。量化反馈生成的技能如果进入 cache，section 读取不再受 project trust 或 artifact approval 约束。
   - 建议：`SkillReadSectionTool` 接入 `ApprovalSource` 和 repo/cwd，读前用 `(repo_fp,name,resource/body locator,hash)` 校验；未批准时返回 `SkillApprovalRequiredError`，由 host tool 已有 approval envelope 承接。

2. **[major] `ExpandedArtifactState` 只有测试和 helper，没有生产写入/查询点**
   - 证据：生产代码中 `MarkArtifact`/`IsArtifactFresh` 只在 `expanded_state.go` 定义，`rg` 未发现除测试外的调用；结构注释说供 `skill_read_section` host tool 使用（`internal/module/turn/expanded_state.go:91-94`，`internal/module/turn/expanded_state.go:133-148`），但 toolbridge 的 `skill_read_section` 未接收或更新该状态（`internal/platform/toolbridge/host_tools.go:120-146`）。
   - 风险：系统看似具备“已注入 artifact 在 TTL 内不重复注入”的状态机，但实际 turn 和 host tool 不会记录成功读取，也不会基于 fresh 状态抑制重复读取或提示。长会话下量化技能 section 可能反复进入模型上下文，频次统计被放大，审计也缺少“何时注入过哪个 artifact”的生产证据。
   - 建议：把 expanded state 挂到 thread/session 级服务，`skill_read_section` 成功后 MarkArtifact，PrepareTurn/manifest 构建时查询 IsArtifactFresh，并在 compact/resume 时 Reset。

3. **[major] trusted skill hydration 会把完整 SKILL.md body 放入 `SkillRef.Prompt`，但 Codex/Claude provider 都不消费该字段**
   - 证据：`applyHydration()` 对 trusted skill 且 `Prompt` 为空时通过 `ReadLocal(<dir>/SKILL.md)` 读取全文并写入 `ref.Prompt`（`internal/module/turn/skills.go:227-257`）。`SkillRef.Prompt` 注释称 provider 都不再消费该字段拼 turn input（`internal/dto/provider/turn.go:40-46`）。Codex `turnInputsFromRequest()` 明确不插入 skill body，测试也锁定“per-turn skill body inlining is removed”（`internal/provider/codexapp/session_turn.go:91-104`，`internal/provider/codexapp/input_map_test.go:17-35`）。Claude `composeTurnContent()` 最终只拼 attachments、inputs、output_schema，没有读取 `req.Skills`（`internal/provider/claudecli/session_turn.go:347-385`）。
   - 风险：PrepareTurn 花费 I/O 读出的 trusted skill body 不进入模型输入，也不进入审批/fresh 状态。开发者容易误以为手选 trusted skill 会注入全文，但实际只有 selected skill name 进入 Codex 参数，Claude 则依赖 workspace native skills。量化审查时会出现“已 hydrate”但模型无正文的假阳性。
   - 建议：删除无效 body hydration，或明确只为特定 provider/adapter 使用；若保留，应在 TurnAssembly 中显式记录是否进入最终输入。

4. **[moderate] hydration 的 `Version` 只保留 12 位 hash，后续去重和 expanded state 以短 hash 为身份**
   - 证据：`applyHydration()` 把 `info.ContentHash` 经 `shortSkillHash()` 写入 `SkillRef.Version`（`internal/module/turn/skills.go:247-249`，`internal/module/turn/skills.go:295-304`）。`skillDedupKey()` 用 `name@version` 去重（`internal/module/turn/skills.go:79-90`）。`artifactFromRef()` 把 `SkillRef.Version` 当 artifact hash（`internal/module/turn/expanded_state.go:265-280`），`ArtifactKey()` 也只取 hash 前 12 位（`internal/module/turn/expanded_state.go:234-249`）。
   - 风险：两版同名技能 hash 前 12 位碰撞时，turn 解析和 expanded state 会把不同正文当同一版本处理。测试已承认短 hash 碰撞会覆盖旧 entry（`internal/module/turn/expanded_state_test.go:241-264`）。
   - 建议：`SkillRef.Version` 保留完整 hash 或至少在内部状态保留 full hash；对 UI/manifest 可显示短 hash，但决策键不要截断。

5. **[moderate] `skill_read_section` 的 `max_bytes` 只在大于 0 时生效，服务端没有硬上限**
   - 证据：schema 只声明 `minimum: 1`，但没有 maximum（`internal/platform/toolbridge/host_tools.go:60-81`）。`readSection()` 在 `a.MaxBytes > 0` 时按请求截断，否则返回完整 body（`internal/platform/toolbridge/skill_read_section.go:69-74`）。底层 `skilllibrary.ReadSection()` 直接 `os.ReadFile` 返回 section 文件，没有大小检查（`internal/module/skilllibrary/section.go:50-78`）。
   - 风险：过大的 cached section 会完整进入 host tool result，造成内存和上下文放大；模型也可以传很大的 `max_bytes` 请求绕过合理预算。
   - 建议：在 tool 层设置硬上限，缺省也强制截断；reader 层先 stat 大小再读。

## 误报与已覆盖项

- `skill_read_section` 成功后会记录 FBSD 调用频次，失败不会打点，避免未知 anchor 污染频次统计（`internal/platform/toolbridge/skill_read_section.go:75-80`）。
- Codex provider 已明确移除 per-turn skill body inlining，减少了普通 turn 输入被自动塞入大段技能正文的风险（`internal/provider/codexapp/input_map_test.go:17-35`）。
- `ExpandedArtifactState` 的 helper 本身覆盖了 body/resource 隔离、TTL、reset 和短 hash 严格比较等单元行为，问题在生产未接入（`internal/module/turn/expanded_state_test.go`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/turn ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -count=1
```

结果：通过。

## 下一轮建议

- Round 066 审查 prompt catalog/manifest 与 skill metadata disclosure：`internal/module/prompt`、manifest builder、approval source/revision cache，确认可见技能列表是否会泄露未批准 project skill 元数据。
