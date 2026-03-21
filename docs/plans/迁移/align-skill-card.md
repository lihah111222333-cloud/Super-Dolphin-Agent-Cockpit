# V2↔V3 1:1 对齐结论：`command/card/*` + `skills/config/*` + `skills/remote/*`

## 结论总览

| 项目 | 结论 | 结论摘要 |
| --- | --- | --- |
| `command/card/*` 7 个 handler | ⚠️ | 严格按对外方法面看，V2 对 `command/card/*` 是 `0/7`；若把 dashboard / resource store / executor 内部能力也算入，`list/get/create/update/delete` 只能算部分承接，`run/versions` 不成立。 |
| card 工厂模式 | ⚠️ | V3 是显式 card-RPC handler factory；V2 是泛化的 `ResourceProvider -> CardStore adapter -> saveStoreValue` 链，不是 1:1 的 card factory。 |
| `skills/config/read` | ✅ | 两边都是 agent 级绑定占位返回；V3 只是补了 `configured/binding_count/binding_source`。 |
| `skills/config/write` | ⚠️ | V3 保留了 V2 的 legacy RPC key，并显式落到 `WriteSkillContent`，但底层写入语义已偏离 V2。 |
| `skills/remote/read` / `skills/remote/list` | ✅ | 两边都把 `list` 当作 `read` 的 alias，都是 HTTP GET 后返回 `{skill:{url,content}}`。 |
| `skills/remote/write` / `skills/remote/export` | ⚠️ | 两边都把 `write/export` 视为“写入本地 skill”，不是回写远端；但 V3 的本地落盘语义已偏离 V2。 |

## 读取范围

### V3

- `internal/module/skill/contract.go`
- `internal/module/skill/rpc.go`
- `internal/module/skill/rpc_types.go`
- `internal/module/skill/rpc_skill_types.go`
- `internal/module/skill/cards.go`
- `internal/module/skill/skills_fs.go`
- `internal/module/skill/skills_meta.go`
- `internal/module/skill/service.go`
- `internal/module/skill/types.go`
- `internal/store/commandcard/contract.go`
- `internal/store/commandcard/store.go`

### V2

- `go-agent-v2/internal/apiserver/methods.go`
- `go-agent-v2/internal/apiserver/methods_command.go`
- `go-agent-v2/internal/skills/methods.go`
- `go-agent-v2/internal/service/skills_core.go`
- `go-agent-v2/internal/service/skills_import.go`
- `go-agent-v2/internal/executor/command_card.go`
- `go-agent-v2/internal/apiserver/tool_provider_adapters.go`
- `go-agent-v2/internal/mcp/resource_adapters.go`
- `go-agent-v2/pkg/toolsdk/tools/resource.go`
- `go-agent-v2/pkg/toolsdk/tools/resource_specs.go`
- `go-agent-v2/pkg/toolsdk/tools/providers.go`
- `go-agent-v2/internal/store/command_card.go`
- `go-agent-v2/internal/store/models.go`
- `go-agent-v2/internal/dashboard/handler.go`
- `go-agent-v2/internal/skillutil/skillutil.go`

## `command/card/*` 7 个 handler 覆盖度

先给硬结论：

- V3 在 `internal/module/skill/rpc.go:42-50` 明确注册了 7 个 `command/card/*` handler。
- V2 的 `go-agent-v2/internal/apiserver/methods.go:229-236` 只注册 `skills/*`，没有任何 `command/card/*`。
- V2 的 agent/tool 面只暴露了 `command_list` 和 `command_get`，定义在 `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:116-168`，实现见 `go-agent-v2/pkg/toolsdk/tools/resource.go:253-284`。

因此，严格按“同级 RPC method 1:1”衡量，V2 对 `command/card/*` 的精确覆盖是 `0/7`。

| V3 handler | V3 落点 | V2 对应能力 | 结论 | 说明 |
| --- | --- | --- | --- | --- |
| `command/card/list` | `NewSkillHandlers` -> `svc.ListCards` | `command_list` tool + dashboard `listCommandCards` + `CommandCardStore.List` | ⚠️ | 能列卡，但 V2 没有 `command/card/list` 这个 RPC key。 |
| `command/card/get` | `NewSkillHandlers` -> `svc.GetCard` | `command_get` tool + `CommandCardStore.Get` | ⚠️ | 有读取能力，但表面接口是 `command_get`，不是 `command/card/get`。 |
| `command/card/create` | `cardCreateHandler` -> `svc.CreateCard` | dashboard `saveCommandCard` -> `CommandCardStore.Save` | ⚠️ | V2 只有单一 `Save` upsert，没有独立 create 语义。 |
| `command/card/update` | `cardUpdateHandler` -> `svc.UpdateCard` | dashboard `saveCommandCard` -> `CommandCardStore.Save` | ⚠️ | V2 仍是 upsert，不要求“先存在再更新”。 |
| `command/card/delete` | `cardByKeyHandler` -> `svc.DeleteCard` | dashboard `deleteCommandCard` + `CardStore.Delete` | ⚠️ | V2 有删除能力，但没有同级 RPC；且删除不做版本归档。 |
| `command/card/run` | `cardRunHandler` -> `svc.RunCard` | 只有内部 `CommandCardExecutor.RunOne/Prepare/Review/Execute` | ❌ | V2 没有对外 handler；语义也不是直接执行。 |
| `command/card/versions` | `cardByKeyHandler` -> `svc.ListCardVersions` | V2 只有保存时写 version 行，没有对外 list handler | ❌ | 无 public list 能力；delete 也不归档版本。 |

### 为什么 `create/update/delete` 只能算 ⚠️

- V3 `CreateCard` 和 `UpdateCard` 已拆成两个语义明确的入口：
  - `CreateCard` 会先查重，已存在则报错，见 `internal/module/skill/cards.go:34-49`。
  - `UpdateCard` 会先取当前版本、写版本归档，再 upsert，见 `internal/module/skill/cards.go:51-68`。
- V2 `CommandCardStore.Save` 是单一 upsert：
  - 先尝试 `Get`，若已有则写一条 `command_card_versions`；
  - 然后统一 `INSERT ... ON CONFLICT DO UPDATE`，见 `go-agent-v2/internal/store/command_card.go:15-40`。
- 这意味着 V2 没有“create 必须不存在 / update 必须已存在”的 handler 级语义边界。

### create/update 还有两个关键语义漂移

1. 默认 `enabled` 行为漂移

- V3 `buildCard` 把缺省 `enabled` 视为 `true`，见 `internal/module/skill/rpc.go:90-101`。
- V2 dashboard `saveCommandCard` 直接把 JSON 绑定到 `store.CommandCard.Enabled bool`，见 `go-agent-v2/internal/dashboard/handler.go:168-178` 与 `go-agent-v2/internal/store/models.go:58-72`。
- V2 若调用方省略 `enabled`，Go 的布尔零值会落成 `false`；V3 则会落成 `true`。

2. delete 的版本语义漂移

- V3 `DeleteCard` 会先 `archiveCard` 再删，见 `internal/module/skill/cards.go:70-83`。
- V2 `CommandCardStore.Delete` 直接 `DeleteByKey`，见 `go-agent-v2/internal/store/command_card.go:73-75`。
- 所以即使“能删”，delete 后版本可追溯性也不是 1:1。

### 为什么 `run` 是 ❌

- V3 `RunCard` 只是：
  - `GetCard`
  - 模板替换 `renderCardCommand`
  - `execShell("sh", "-lc", rendered)` 直接执行
  - 返回 `{card_key, rendered_command, exec}`
  - 见 `internal/module/skill/cards.go:93-110` 与 `internal/module/skill/exec.go:42-105`
- V2 的 run 能力在内部 executor：
  - `Prepare` 会根据 risk/dangerous pattern 产生 run 记录，见 `go-agent-v2/internal/executor/command_card.go:59-93`
  - `Review` 处理审批流，见 `go-agent-v2/internal/executor/command_card.go:177-229`
  - `Execute` 执行已准备 run，见 `go-agent-v2/internal/executor/command_card.go:239-314`
  - `RunOne` 是 prepare + optional review + execute 组合，见 `go-agent-v2/internal/executor/command_card.go:352-388`
- 但这套 executor 没有被 V2 的 RPC/tool surface 注册出来。
- 即便只比内部能力，V2 也是“prepare/review/audit/persisted run”，而 V3 是“立即 shell 执行”，不属 1:1。

### 为什么 `versions` 是 ❌

- V3 有完整 public 能力：
  - `ListCardVersions` 暴露在 service contract 中，见 `internal/module/skill/contract.go:12`
  - RPC key `command/card/versions` 已注册，见 `internal/module/skill/rpc.go:50`
  - store 有 `ListVersions/InsertVersion`，见 `internal/store/commandcard/contract.go:12-15`
- V2 只有“保存时顺手插 version 行”的内部实现，见 `go-agent-v2/internal/store/command_card.go:15-25`。
- V2 没有对外 `versions` handler，也没有 delete 时归档版本。

## card 工厂模式

结论：⚠️

### V3

- V3 是显式 card-RPC factory：
  - `cardByKeyHandler`
  - `cardCreateHandler`
  - `cardUpdateHandler`
  - `cardRunHandler`
  - `buildCard`
- 它们统一把 RPC params 转成 typed `Card` 或 `(key,args)`，见 `internal/module/skill/rpc.go:12-33` 与 `internal/module/skill/rpc.go:90-101`。

### V2

- V2 没有对应的 `command/card/*` handler factory。
- V2 的 card 抽象边界在 generic resource/provider 层：
  - `ResourceProvider.CommandCardStore()`，见 `go-agent-v2/pkg/toolsdk/tools/providers.go:51-58`
  - `adaptCardStore(...)`，见 `go-agent-v2/internal/apiserver/tool_provider_adapters.go:263-285` 与 `go-agent-v2/internal/mcp/resource_adapters.go:121-143`
  - `saveStoreValue[T]` 泛型适配，见 `go-agent-v2/internal/apiserver/tool_provider_adapters.go:413-420`
  - `resourceCommandList/resourceCommandGet` 通过 `provider.CommandCardStore()` 调用，见 `go-agent-v2/pkg/toolsdk/tools/resource.go:253-284`

### 结论理由

- 两边都用了“间接层”，但层次不同：
  - V3 是 card-first 的 RPC handler factory。
  - V2 是 generic store adapter / resource tool factory。
- 这意味着迁移时不能把 V2 的 `CardStore` 适配链直接视作 V3 `command/card/*` handler 的前身。

## `skills/config/*` 语义

### `skills/config/read`

结论：✅

- V2 `SkillsConfigRead` 返回：
  - `agent_id`
  - `skills`
  - `session_bound`
  - 见 `go-agent-v2/internal/skills/methods.go:388-394`
- V3 `ReadConfig` 返回相同核心字段，并额外补了：
  - `configured`
  - `binding_count`
  - `binding_source`
  - 见 `internal/module/skill/skills_fs.go:143-157`
- 两边都明确还是 stub：
  - V3 代码里直接写了 TODO placeholder，见 `internal/module/skill/skills_fs.go:144`

### `skills/config/write`

结论：⚠️

先说“同名接口”层面：

- V2：
  - `skills/config/write` -> `skillsConfigWriteTyped`，见 `go-agent-v2/internal/apiserver/methods.go:233-235`
  - `skillsConfigWriteTyped` -> `Manager.SkillsConfigWrite`，见 `go-agent-v2/internal/apiserver/methods_command.go:278-280`
  - `SkillsConfigWrite` -> `skillSvc.WriteSkillContent`，见 `go-agent-v2/internal/skills/methods.go:396-413`
- V3：
  - `skills/config/write` 明确标注为 legacy RPC key，见 `internal/module/skill/rpc.go:77-80`
  - 最终仍落到 `svc.WriteSkillContent(ctx, name, content)`

所以“RPC key -> 写主 skill 内容”这层映射是保住了。

但底层语义已经不是 1:1：

1. 存储布局不同

- V2 `WriteSkillContent`：
  - 落在 `by-id/<storage-id>/SKILL.md`
  - 额外写 `skill.json`
  - 见 `go-agent-v2/internal/service/skills_core.go:407-438`
- V3 `WriteSkillContent`：
  - 直接写 `<skillsRoot>/<skillSlug(name)>/SKILL.md`
  - 不写 `skill.json`
  - 见 `internal/module/skill/skills_fs.go:159-168` 与 `internal/module/skill/skills_meta.go:259-270`

2. 冲突/碰撞处理不同

- V2 `resolveStorageID` 会在 slug 冲突时生成稳定后缀，并用 `skill.json` 保留原始显示名，见 `go-agent-v2/internal/service/skills_core.go:116-141`
- V3 只用 `skillSlug(name)` 直接落目录，见 `internal/module/skill/skills_meta.go:314-337`
- 结果是：不同原始名称只要 slug 一样，V3 就可能覆盖同一路径；V2 则会分配不同 storage id。

3. 落盘原子性不同

- V2 先写 staging dir，再 `activateStagedSkillDir` 原子切换，见 `go-agent-v2/internal/service/skills_core.go:419-438` 与 `go-agent-v2/internal/service/skills_core.go:470-492`
- V3 直接 `os.WriteFile` 覆盖目标文件，见 `internal/module/skill/skills_meta.go:264-270`

4. 名称规范化边界不同

- V2 `SkillsConfigWrite` 先 `skillutil.NormalizeName`，见 `go-agent-v2/internal/skills/methods.go:401-409`
- V3 `WriteSkillContent` 只检查非空，真正目录名由 `skillSlug` 在落盘时生成，见 `internal/module/skill/skills_fs.go:159-168`

## `skills/remote/*` 语义

### `skills/remote/read` 与 `skills/remote/list`

结论：✅

- V2：
  - `skills/remote/list` 与 `skills/remote/read` 都注册到 `skillsRemoteReadTyped`，见 `go-agent-v2/internal/apiserver/methods.go:233-234`
  - 最终 `SkillsRemoteRead` 做 HTTP GET，返回 `{skill:{url,content}}`，见 `go-agent-v2/internal/skills/methods.go:437-459`
- V3：
  - `skills/remote/list` 与 `skills/remote/read` 都注册到 `svc.ReadRemote`，见 `internal/module/skill/rpc.go:68-72`
  - `ReadRemote` 也是 HTTP GET，返回 `{skill:{url,content}}`，见 `internal/module/skill/skills_fs.go:111-130`

这里连“`list` 实际是 `read` alias”这个怪异约定都保留了，可以算对齐。

### `skills/remote/write` 与 `skills/remote/export`

结论：⚠️

接口映射层面是对齐的：

- V2：
  - `skills/remote/export` 与 `skills/remote/write` 都注册到 `skillsRemoteWriteTyped`，见 `go-agent-v2/internal/apiserver/methods.go:233-234`
  - 最终都走 `SkillsRemoteWrite` -> `WriteSkillContent`，见 `go-agent-v2/internal/skills/methods.go:461-475`
- V3：
  - `skills/remote/export` 与 `skills/remote/write` 都注册到 `svc.WriteRemote`，见 `internal/module/skill/rpc.go:69-75`
  - `WriteRemote` 最终调用 `writeSkill`，见 `internal/module/skill/skills_fs.go:132-140`

因此：

- 两边都不是“回写远端”。
- 两边都把 remote `write/export` 解释成“把远端内容导入本地 skills 目录”。

但写入语义仍然沿用了上面 `skills/config/write` 的全部漂移：

- V2：`by-id + skill.json + staging/activate + collision resolution`
- V3：`skillSlug 目录 + 直接写 SKILL.md`

所以 alias 对齐是保住了，但存储语义不是 1:1。

## 最终判断

### 可以判定为 `✅` 的部分

- `skills/config/read`
- `skills/remote/read`
- `skills/remote/list`

### 只能判定为 `⚠️` 的部分

- `command/card/list`
- `command/card/get`
- `command/card/create`
- `command/card/update`
- `command/card/delete`
- card 工厂模式
- `skills/config/write`
- `skills/remote/write`
- `skills/remote/export`

### 必须判定为 `❌` 的部分

- `command/card/run`
- `command/card/versions`

## 一句话结论

当前 V3 不是对 V2 `command/card/* + skills/config/* + skills/remote/*` 的严格 1:1 平移。

最准确的表述是：

- `skills/config/read` 与 `skills/remote/read/list` 基本兼容；
- `skills/config/write` 与 `skills/remote/write/export` 只保留了 RPC key 和返回形状，底层写入语义已漂移；
- `command/card/*` 则是 V3 新建的显式 RPC 面，严格对照 V2 只能得到 `0/7` exact parity。
