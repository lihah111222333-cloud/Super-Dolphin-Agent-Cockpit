# With-LSP 语义工作流（仅三个工具同时可用时注入）

**Mandatory With-LSP gate / With-LSP 强制门禁:** 只有 `structure`、`xref`、`diagnostics` 全部暴露时注入本段；No-LSP 或工具不完整时零注入 LSP 名称、schema、示例和 workflow。

工具契约：

- `structure` 定位 AST/document symbol：`{"action":"workspace_symbol","query":"Name","workspace_language":"python"}` 或 `{"action":"document_symbol","file_path":"path.py"}`。
- `xref` 查询真实引用/调用/类型关系：`{"action":"references","pos":"path:line:column"}`；`xref.pos` 必须逐字来自当前 `structure` 返回，不得猜测。
- `diagnostics` 检查变更生产文件；`OK` 只表示请求成功。逐条审阅 `ROW` 的 `Error`、`Warning`、`Information`、`Hint`。

LSP 是可选语义能力，不是调用配额。工具可用但任务无需调用时允许零调用；不得为了生成 receipt 而调用。`diagnostics` 用于提高修改质量，但不能替代目标测试或官方 grader。

## 任务边界、语义定位与触发矩阵

| 信号 | 动作 | 边界 |
| --- | --- | --- |
| 单生产文件、局部符号、无共享/导出契约且无跨文件失败 | 原生读取、修改和定向测试；可跳过 `structure`/`xref` | 不为凑调用次数使用语义工具 |
| 多生产文件，但定义位置和完整消费者集合均已明确 | 直接修改，随后一次逐文件差量 `diagnostics` | 文件数量本身不触发语义探索 |
| 定义位置未知或有歧义 | 只调用一次窄范围 `structure`，定位后用原生工具精读 | 不因多文件自动调用 `xref` |
| imported symbol、共享基类、注册器、dispatcher、parser、transform graph 或公共 helper 的消费者/影响面未知 | `structure` → 当前真实位置 → 一次 `xref(references)` | 只追踪未知的消费边界 |
| 跨文件失败、重命名、重名符号或无法解释的调用方失败 | 重新 `structure` 和 `xref` | 失败后改变假设并停止扩散 |

## 差量验证

从 `git diff -U0` 的新文件侧 hunk 取得 1-based 行号；每个文件独立映射：

```json
{
  "file_paths": ["internal/pkg/collector.go", "internal/pkg/admin.go"],
  "diff_filter": {
    "mode": "changed_lines",
    "changed_lines": {
      "internal/pkg/collector.go": [42, 43, 44],
      "internal/pkg/admin.go": [18, 19]
    }
  }
}
```

禁止多个文件共享行号、使用旧文件行号、漏映射或退回未过滤全文件请求；每个变更生产文件必须覆盖，generated 文件需覆盖或明确 `diagnostics_unsupported` 豁免。changed-line 问题、存量问题和环境/工具链问题必须分别说明，未覆盖不能结束。

## 失败分类与 compact round-state

`MODEL_TEST_FAILURE` 才进入下一质量轮；`PATCH_APPLY_FAILURE`、`TIMEOUT`、`INFRASTRUCTURE_ERROR` 留在当前质量轮做有界重试；`FEEDBACK_UNAVAILABLE` 或 `UNRESOLVED` 终止为 unverified。`PASSED` 由 runner 权威结果确认。多轮语义失败必须走失败恢复；相同 patch/hash、无 `file_change` 或 contract failure 属于 no-progress：不重复 patch、已通过测试或 grader，先改变假设再定位。

多轮只传以下字段，禁止粘贴 transcript/full log：

```json
{"round":2,"patch_hash":"sha256:...","latest_failure":"...","unresolved_evidence":["..."],"cache_key":"...","previous_hypothesis":"..."}
```

退出前输出 self-check：

```json
{"lsp_self_check":{"changed_production_files":["..."],"structure_evidence":[{"tool":"structure","pos":"path:line:column"}],"xref_evidence":[{"tool":"xref","action":"references","pos":"path:line:column"}],"diagnostics_coverage":["path.py"],"exemptions":[]}}
```

`runner` 是最终硬守卫；prompt 或 diagnostics 不能替代权威验收。
