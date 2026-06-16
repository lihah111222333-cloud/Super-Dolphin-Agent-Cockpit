# Round 001 - prompt catalog/manifest 与 skill metadata 泄露审查

## 时间

- 开始：2026-06-01 KST
- 结束：2026-06-01 KST
- 审查批次：6.1 第 1 轮（接 5.17/round-065 的下一轮建议）

## 审查口径

依据 `docs/li/p1`（兜底/静默/弱契约/Fail-Fast 零容忍）：
- 任何 `nil` / 类型断言失败 / marshal 失败被静默转为「空值」「false」「零」都视为违反 Fail-Fast。
- 接口入参/返回值的语义必须在契约层强制；不允许"配置缺失 ≡ 业务空值"的语义二义。
- 静默吞错（不记录、不返回）一律记 [major] 或以上。

## 本轮范围

承接 Round 065 第 1、2 条的延伸——确认 prompt catalog / manifest builder / dashboard 在装配 skill metadata 时是否引入兜底导致未审批 project skill 元数据泄露 / 静默降级。

- `internal/contract/skill.go`
- `internal/contract/manifest.go`
- `internal/contract/approval.go`
- `internal/module/skill/service.go`
- `internal/module/dashboard/service.go`
- `internal/module/dashboard/ui_page.go`
- `internal/module/dashboard/factory.go`
- `internal/module/uistate/builtin_tools.go`（采样）
- `internal/module/prompt/`（grep 验证未消费 ApprovalSource / SkillLister）

## Findings

1. **[blocker] `service.LookupArtifactApproval` 静默吞配置缺失，把"系统未配置审批源"伪装成"未批准"**
   - 证据：`internal/module/skill/service.go:97-109`
     ```go
     func (s *service) LookupArtifactApproval(_ context.Context, req contract.ArtifactApprovalRequest) (bool, error) {
         if s == nil || s.approval == nil {
             return false, nil
         }
         _, ok := s.approval.LookupArtifact(...)
         return ok, nil
     }
     ```
   - 风险：契约 `contract.ApprovalSource.LookupArtifactApproval` 的语义是"返回 artifact 是否已被批准"。当 service 或 approval cache 任一为 nil 时，返回 `(false, nil)` 让调用方误判为"未批准"，调用方可能因此走"自动重新征求审批"路径，或者更糟——如果调用方实现的是"未批准则跳过"，那么 fail-open 静默放行。两种行为都不可接受。
   - Fail-Fast 修法：`s == nil || s.approval == nil` 应当 `return false, fmt.Errorf("skill: approval cache not configured")`，让 fx 装配阶段就崩，不要让 nil service 进入运行期。同理 `ApprovalRevision()` 返回 `0` 让 prompt cache 误判"从未变化过"，应改为 panic 或在构造时强制非空。

2. **[blocker] `hashResolutionEnvelope` 丢弃 `json.Marshal` 错误，多个不同 envelope 可碰撞为同一 hash**
   - 证据：`internal/module/skill/service.go:91-95`
     ```go
     func hashResolutionEnvelope(v any) string {
         data, _ := json.Marshal(v)
         sum := sha256.Sum256(data)
         return hex.EncodeToString(sum[:])
     }
     ```
   - 风险：`resolutionPreviewHash` 用此 hash 做冲突预览的去重 / 一致性比对（`service.go:54-89`）。如果输入包含不可序列化字段（chan / func / cyclic struct），`Marshal` 返回 `nil, err`，则 `data == nil`，sha256 算出的是固定空摘要 `e3b0c44...`，**任何两个 marshal 失败的 envelope 都会被判同一个**。下游可能据此跳过 confirm 检查、或把不同冲突预览串号。
   - Fail-Fast 修法：函数签名改为 `(string, error)`，调用方上抛；或断言 `if err != nil { panic(...) }`。绝不允许 `_ =`。

3. **[major] `safeList[T]` 把"依赖未注入 (`s.xxx == nil`)"静默兜底成空数组，违反 Fail-Fast**
   - 证据：`internal/module/dashboard/factory.go:59-68`
     ```go
     func safeList[T any](enabled bool, query func() ([]T, error)) ([]T, error) {
         if !enabled {
             return []T{}, nil
         }
         ...
     }
     ```
     调用点：`ui_page.go:241`（taskTraces）、`ui_page.go:251`（skills 兜底支路）、`ui_page.go:257`（commandCards）、`ui_page.go:264`（prompts）、`ui_page.go:343`（sharedFiles）。
   - 风险：构造函数 `NewService` 接受多个 store 参数但完全不在装配期校验非空。生产环境如果某个 store 因 fx 装配失败被注入 nil，dashboard 会直接给前端返回 `[]`——UI 显示"没有任何 skill / prompt / 命令卡 / shared file"，运维以为"业务为空"，实际是依赖断链。这种静默是 P1 类违规第 1 条直接命中。
   - Fail-Fast 修法：`NewService` 在构造期 `if skills == nil { return nil, errors.New("dashboard: skills lister required") }`。要么允许 nil 但在调用期返回 `error`，绝不能用空数组兜底。

4. **[major] `skillInventoryFromLister` 类型断言失败静默降级，调用方无法察觉**
   - 证据：`internal/module/dashboard/service.go:124-127` + `ui_page.go:248-253`
     ```go
     func skillInventoryFromLister(skills contract.SkillLister) contract.SkillInventoryLister {
         inventory, _ := skills.(contract.SkillInventoryLister)
         return inventory
     }
     // 调用：
     if s.skillInventory != nil {
         return s.skillInventory.ListSkillInventory(...)
     }
     return safeList(s.skills != nil, ... s.skills.ListSkills(...))
     ```
   - 风险：`ListSkillInventory`（管理面）和 `ListSkills`（运行面）语义不同——前者保留"重名 / 政策隐藏"的源记录用于冲突处理（见 `contract/skill.go:88-93` 的注释），后者只返回过滤后的 metadata。当注入的 `SkillLister` 实现碰巧没实现 `SkillInventoryLister` 时，dashboard 静默退回到 `ListSkills`，**冲突处理面会丢失隐藏记录**，但前端不知道自己看到的是降级后的列表。同时 `_, _` 的双下划线丢弃既丢了 ok，也丢了潜在的诊断。
   - Fail-Fast 修法：`SkillInventoryLister` 应当作为独立的 fx 依赖注入，而不是从 `SkillLister` 强转。如果一定要复用 service 实现，构造期断言 `inventory, ok := skills.(contract.SkillInventoryLister); if !ok { return nil, error }`。

5. **[moderate] `dashboardPromptTags` 把 `json.Unmarshal` 失败兜底为空切片，污染下游"系统管理"判定**
   - 证据：`internal/module/dashboard/ui_page.go:304-313`
     ```go
     func dashboardPromptTags(raw json.RawMessage) []string {
         if len(raw) == 0 {
             return []string{}
         }
         var tags []string
         if err := json.Unmarshal(raw, &tags); err != nil {
             return []string{}
         }
         return tags
     }
     ```
   - 风险：`dashboardPromptIsSystemManaged` 用 tags 判断是否为 builtin:system，从而决定是否在 dashboard 列表中隐藏。如果 tags JSON 损坏（迁移脏数据 / 写入端 bug），unmarshal 失败被吞 → 视为无标签 → 不被识别为 system → **本应隐藏的系统模板被泄露给 UI**。同时损坏的数据也无人告警，永久积压在库里。
   - Fail-Fast 修法：unmarshal 失败应在 RPC 层返回 5xx 并写一行带 prompt id 的 error 日志。

6. **[moderate] `populateDashboardSkills` 吞 `ErrSkillSameNameConflict`，前端无法感知冲突**
   - 证据：`internal/module/dashboard/ui_page.go:201-208`
     ```go
     func (s *service) populateDashboardSkills(ctx context.Context, out *DashboardPage) error {
         items, err := s.listDashboardSkills(ctx)
         out.Skills = items
         if errors.Is(err, contract.ErrSkillSameNameConflict) {
             return nil
         }
         return err
     }
     ```
   - 风险：把 sentinel error 转成 nil 但不在 `DashboardPage` 上挂"冲突标记"，UI 看到的 `out.Skills` 可能是部分列表，且 page 状态显示一切正常。冲突需要用户决议，吞掉就是"业务静默降级"。
   - Fail-Fast 修法：`DashboardPage` 加 `SkillConflicts []string`，把 conflict 信息显式回传，error 仍向上抛或转成 page-level warning。

## 误报与已覆盖项

- `internal/module/prompt/` 全目录（80+ 文件）grep `SkillLister|ApprovalSource|approval` 均无匹配，**确认 prompt catalog 不直接消费 skill 审批源**——本轮关心的"prompt 模块泄露未审批 metadata"路径不存在直接漏洞，问题集中在 dashboard / skill service 这两个出口。
- `internal/contract/manifest.go` 的 `BuildManifest` 不接触 skill metadata，本轮范围内未发现兜底问题；唯一可疑点 `panic(fmt.Sprintf(...))`（line 130）属于"manifest workspace roots must encode as JSON"——这是不可达分支（roots 为 string 切片，必然可序列化），保留 panic 作为契约断言合理。
- `LookupArtifactApproval` 的 receiver 写为指针并允许 `s == nil`，但 fx 装配里 `ProvideSkillLister` 返回的是 `Service`，当前没有"返回 nil service"的入口；`s == nil` 分支理论上不可达。但这不是不修的理由——契约不应该靠"上游不会触发"来保证。

## 验证

本轮为静态审查（基于 grep / read），未运行测试。验证策略待 round-002 同步：

```bash
# 用于验证 finding 1 的修法是否引发回归
./scripts/test_with_guard.sh ./internal/module/skill ./internal/module/dashboard -count=1
```

## 下一轮建议

- Round 002 审查 `internal/module/turn/` 和 `internal/dto/provider/` 在 selected skill / artifact hydration 链路上的兜底情况：重点查 `applyHydration` 失败是否静默返回空 ref、`SkillRef.Version` 短 hash 决策（Round 065 finding 4 的延伸）、provider session_turn 在 `req.Skills` 缺失字段时的兜底。
