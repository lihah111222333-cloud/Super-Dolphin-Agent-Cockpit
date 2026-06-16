# p8-handler-review-2

## 审查范围
- V3
  - `internal/sidecar/orch/tools/workspace_tools.go`
  - `internal/sidecar/orch/tools/prompt_tools.go`
  - `internal/sidecar/orch/tools/command_tools.go`
  - `internal/sidecar/orch/tools/shared_file_tools.go`
- V2 参考
  - `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/tools/resource.go`
  - `/Users/mima0000/Desktop/wj/go-agent-v2/pkg/toolsdk/tools/resource_specs.go`

## V2 Parity
| 工具 | V2 行为 | V3 实现 | 状态 |
|---|---|---|---|
| `workspace_create_run` | schema 要求 `source_root` 必填；resource handler 只解码后下发，实际必填校验落在下游 `WorkspaceManager.CreateRun` | schema 同样要求 `source_root`；handler 额外先做 `requireTrimmed(source_root)`，并会 trim 其他字符串字段、过滤空白 `files` | 部分一致 |
| `workspace_get_run` | `run_key` 必填；调用 `ops.GetRun` 后若返回 `nil`，显式报 `workspace run <key> not found` | `run_key` 必填；handler 直接返回 `store.GetRun` 结果，没有 V2 的 `nil -> not found` 兜底 | 不一致 |
| `workspace_list_runs` | 支持 `status`/`dag_key`/`limit`；`limit <= 0` 或 `limit > 5000` 时回退到 `200` | 同样支持 `status`/`dag_key`/`limit`；`normalizeWorkspaceListLimit` 也会把 `<=0` 和 `>5000` 收敛到 `200` | 一致 |
| `workspace_merge_run` | 返回 merge result，不是 run；支持 `updated_by`/`dry_run`/`delete_removed` | 也返回 merge result，不是 run；支持同名参数，但结果结构与 V2 不完全同形 | 部分一致 |
| `workspace_abort_run` | `run_key` 必填；返回更新后的 run | `run_key` 必填；返回 `store.AbortRun(...)` 的 run | 一致 |
| `prompt_list` | 支持 `keyword` 过滤；固定 `limit=50` | 支持 `keyword` 过滤；固定 `resourceListLimit=50` | 一致 |
| `prompt_get` | `prompt_key` 必填；`nil` 结果显式报 `prompt <key> not found` | `prompt_key` 必填；代码里有 `nil` 检查，但当前 sqlc store miss 时先返回 wrapped not-found error，通常到不了 V2 的精确文案 | 不一致 |
| `command_list` | 支持 `keyword` 过滤；固定 `limit=50` | 支持 `keyword` 过滤；固定 `resourceListLimit=50` | 一致 |
| `command_get` | `card_key` 必填；`nil` 结果显式报 `command <key> not found` | `card_key` 必填；同 `prompt_get`，当前 miss 会先走 wrapped store error，通常到不了 V2 精确文案 | 不一致 |
| `shared_file_read` | `path` 必填；`nil` 结果显式报 `file <path> not found`；返回整个文件对象，不只是 `content` | `path` 必填；返回整个 `sharedFileDTO`；但 miss 处理依赖 store error，且未做 V2 的路径规范化 | 不一致 |
| `shared_file_write` | `path` 必填；handler 先拒绝空白 path；调用 `Write(..., "agent")`，actor 固定为 `agent`；底层会规范化 path | `path` 必填；调用 `Upsert(... UpdatedBy:"agent")`，actor 也固定为 `agent`；但只 trim，不做 V2 的 path 规范化 | 部分一致 |

### 逐项结论
- 明确一致：`workspace_list_runs`、`workspace_abort_run`、`prompt_list`、`command_list`
- 部分一致：`workspace_create_run`、`workspace_merge_run`、`shared_file_write`
- 不一致：`workspace_get_run`、`prompt_get`、`command_get`、`shared_file_read`

## InputSchema 一致性检查
| 工具 | V2 必填字段 | V3 必填字段 | 备注 | 状态 |
|---|---|---|---|---|
| `command_list` | 无 | 无 | `keyword` 都是可选 `string` | 一致 |
| `command_get` | `card_key` | `card_key` | 字段名和必填约束一致 | 一致 |
| `prompt_list` | 无 | 无 | `keyword` 都是可选 `string` | 一致 |
| `prompt_get` | `prompt_key` | `prompt_key` | 字段名和必填约束一致 | 一致 |
| `shared_file_read` | `path` | `path` | 字段名和必填约束一致 | 一致 |
| `shared_file_write` | `path`, `content` | `path`, `content` | 字段名和必填约束一致 | 一致 |
| `workspace_create_run` | `source_root` | `source_root` | 可选字段 `run_key`/`dag_key`/`created_by`/`files`/`metadata` 均对齐 | 一致 |
| `workspace_get_run` | `run_key` | `run_key` | 字段名和必填约束一致 | 一致 |
| `workspace_list_runs` | 无 | 无 | `status`/`dag_key` 都是可选；但 `limit` 类型 V2 是 `number`，V3 是 `integer` | 部分一致 |
| `workspace_merge_run` | `run_key` | `run_key` | 可选字段 `updated_by`/`dry_run`/`delete_removed` 对齐 | 一致 |
| `workspace_abort_run` | `run_key` | `run_key` | 可选字段 `updated_by`/`reason` 对齐 | 一致 |

### Schema 级别的全局差异
1. V2 的 `resourceObjectSchema(...)` 只生成 `type=object`、`properties`、`required`，没有显式 `additionalProperties` 限制。
2. V3 的 `ObjectSchema(...)` 统一附带 `additionalProperties: false`。
3. 因此从严格 schema parity 看，11 个工具的顶层 schema 都比 V2 更严格。
4. `workspace_create_run.metadata` 在两边都允许对象；这一项本身没有额外漂移。

## 安全性检查
| 维度 | V2 | V3 | 结论 |
|---|---|---|---|
| `shared_file_*` 路径规范化 | `normalizePath()` 会 trim、转 `/`、去首尾 `/` | 仅 `requireTrimmed(path)`，不做斜杠与首尾 `/` 规范化 | V3 退化 |
| `workspace_create_run.source_root` | handler 不拦，依赖下游 workspace manager 做绝对化、目录校验和 root 约束 | handler 只做必填和 trim，是否做沙箱完全依赖注入的 `WorkspaceStore` 实现 | V3 handler 侧无硬保证 |
| `workspace_create_run.files` | V2 handler 直接透传，实际校验靠下游 manager | V3 handler 只做 trim/filter empty，是否禁止 `..` 或绝对路径也依赖下游实现 | handler 侧都偏薄 |
| 列表结果大小 | `command_list`/`prompt_list` 固定 50；`workspace_list_runs` 默认 200，超大 limit 钳回 200 | 同上 | 一致 |
| `shared_file_write` 内容大小 | 未见 handler 级限制 | 未见 handler 级限制 | 均无上限 |
| 参数注入 | 强类型 JSON 解码，store 查询走参数化 SQL | 强类型 JSON 解码，store 查询走参数化 SQL | 未见明显 SQL 注入点 |

### 安全性结论
- SQL 注入面基本安全。
- 主要问题不在注入，而在路径规范化和 handler 层缺少独立的安全边界。
- `shared_file_*` 因为失去 V2 的 path canonicalization，逻辑同一路径可能被写成多个不同 key。
- `workspace_*` 的路径沙箱、文件数量、字节上限如果要成立，必须依赖被注入的具体实现；仅从 `internal/sidecar/orch/tools/*.go` 这层看，不是自证安全的。

## 问题清单
| # | 问题 | 严重度 | 建议 |
|---|---|---|---|
| 1 | `workspace_get_run` 缺少 V2 的 `nil -> not found` 兜底，handler 直接透传 `store.GetRun` 返回值。 | 中 | 在 handler 层补 `run == nil` 判断，统一报 `workspace run <key> not found`。 |
| 2 | `prompt_get` 的 not-found 处理与 V2 不等价。虽然代码里有 `template == nil` 判断，但当前 sqlc store miss 时先返回 wrapped not-found error，通常不会进入该分支。 | 中 | 对 `platformdb.IsNotFound(err)` 做显式翻译，输出与 V2 一致的 `prompt <key> not found`。 |
| 3 | `command_get` 的 not-found 处理与 V2 不等价，问题与 `prompt_get` 相同。 | 中 | 对 `platformdb.IsNotFound(err)` 做显式翻译，输出与 V2 一致的 `command <key> not found`。 |
| 4 | `shared_file_read` 的 not-found 处理与 V2 不等价。当前 miss 更可能返回 wrapped store error，而不是 V2 的 `file <path> not found`。 | 中 | 对 `platformdb.IsNotFound(err)` 做显式翻译，并统一错误文案。 |
| 5 | `shared_file_read` / `shared_file_write` 丢失了 V2 的 path canonicalization。`/a/b/` 和 `a/b` 在 V3 可能变成不同 key。 | 中 | 恢复 V2 的 `normalizePath` 语义，至少做 `filepath.ToSlash` 和首尾 `/` 规整。 |
| 6 | `workspace_merge_run` 虽然同样返回 merge result，但返回 shape 已偏离 V2：V3 多了 `removed`，少了 V2 结果里的 `finishedAt`。 | 中 | 如果目标是严格 parity，统一 merge result 字段集；否则在迁移文档里明确“行为兼容但结果不等形”。 |
| 7 | 11 个工具的顶层 InputSchema 都比 V2 更严格：V3 统一设置了 `additionalProperties:false`，V2 没有限制。 | 低 | 如果要做到 schema parity，移除顶层 `additionalProperties:false`；如果保留，需在文档中声明这是有意收紧。 |
| 8 | `workspace_list_runs.limit` 的 schema 类型从 V2 的 `number` 变成了 V3 的 `integer`。 | 低 | 改回 `number`，或者在兼容层明确接受两种 JSON 数值。 |
| 9 | `workspace_create_run` 在 handler 层比 V2 更早做了 `source_root` 必填和输入 trim/filter，属于“更严格但不完全同形”的行为收紧。 | 低 | 如果追求严格 parity，收敛为只做解码，把校验下放到统一 service；如果保留，文档注明为主动收紧。 |
| 10 | `workspace_*` handler 自身没有独立的路径沙箱、文件数、字节上限保证，安全性依赖外部注入实现。 | 中 | 在 handler 层或统一 adapter 层显式绑定到受控 workspace service，不要只暴露一个宽接口。 |
| 11 | `shared_file_write` 没有内容大小限制；大 payload 会直接写库。 | 低 | 增加 handler 级内容长度上限，或在 store/DB 层做限制并返回明确错误。 |

## 结论
1. 这 11 个 resource handler 还没有达到严格的 V2 parity。
2. Required/optional 字段基本对齐；schema 主要差异是 V3 统一加了 `additionalProperties:false`，以及 `workspace_list_runs.limit` 从 `number` 变成了 `integer`。
3. 行为差异主要集中在 not-found 语义、`shared_file_*` 的路径规范化，以及 `workspace_merge_run` 的返回 shape。
4. 安全性方面，SQL 注入风险不突出；真正的问题是路径 canonicalization 退化，以及 `workspace_*` handler 自身不提供独立的沙箱/限额保证。
5. 如果目标是 V2 parity，优先修复项应是：补齐 `workspace_get_run` / `prompt_get` / `command_get` / `shared_file_read` 的精确 not-found 语义，恢复 `shared_file_*` 的路径规范化，并明确 `workspace_merge_run` 的结果 shape 是否必须与 V2 完全一致。
