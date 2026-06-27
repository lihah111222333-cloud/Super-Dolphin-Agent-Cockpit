# Recall Topic Naming Contract

`recall_topic` 是 `prompt_recall` 工具的全局查找键。它必须稳定、可读、可由模型从
`recall_catalog` 中原样复制调用。

## 规则

- 使用 ASCII lowercase 字母、数字和 dash。
- 长度必须小于 64 个字符。
- 使用 `<domain>-<subtopic>` 形状，例如 `sqlc-workflow`。
- 不允许空格、下划线、点号、斜杠、冒号或大小写混写。
- 不允许以 dash 开头或结尾。
- 不允许连续 dash；每个 dash 分隔段都必须非空。
- `trigger_type='recall'` 且 `recall_topic<>''` 的行在全表唯一；迁移或 UI 写入必须避免复用已有 topic。

## 示例

推荐：

- `lsp-basics`
- `lsp-advanced`
- `sqlc-workflow`
- `prompt-template-editing`
- `frontend-react`
- `migration-rules`
- `guard-rules`

反例：

- `LSP_Basics`
- `my topic`
- `prompt.template`
- `frontend/react`
- `-migration-rules`
- `guard-rules-`
- `guard--rules`

## 使用约定

- 新增大段、按需拉取的技术知识优先建成 `trigger_type='recall'` section。
- `prompt_recall(topic)` 只接受 topic 名，不接受 template id 或 section key。
- topic 不存在或命名不合法时，工具返回 soft result 和 hint，让调用方自行纠正。
- 同一 thread 重复拉同一 topic 不阻断，但工具结果会带 warning。
