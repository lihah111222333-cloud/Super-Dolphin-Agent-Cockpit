# SkillInfo 前端字段守卫设计

## 背景

斜杠命令面板通过 Dashboard `skills` 响应构建 Skill 命令。此前前端错误地要求响应包含 `skill_file`，而后端 `contract.SkillInfo` 并不暴露该字段，导致单个字段契约偏差使整个 Skill 分类加载失败。

当前适配器已区分部分必填字段和 `omitempty` 字段，但尚未对 `SkillInfo` 的全部已知字段建立统一边界守卫。因此，未被 UI 直接消费的字段即使类型错误，也可能静默进入前端边界。

## 目标

- 在 `skillSlashCommandAdapter` 入口校验当前 `contract.SkillInfo` 的全部已知字段。
- 必填语义字段缺失或类型错误时立即抛出带条目索引和字段名的 `TypeError`。
- 可选字段缺省时合法；字段存在但类型错误时立即失败。
- 允许未知字段通过，避免后端新增字段导致旧前端失效。
- 保持现有 Skill 命令映射、排序和选择行为不变。

## 字段规则

| 字段 | 规则 |
| --- | --- |
| `name`、`dir`、`scope` | 非空字符串；`scope` 仅允许 `project` 或 `personal` |
| `description`、`summary` | 必须是字符串，允许空字符串 |
| `display_name`、`personal_type`、`trust`、`content_hash` | 可缺省；存在时必须是字符串 |
| `trigger_words`、`force_words`、`allowed_tools` | 可缺省；存在时必须是字符串数组 |
| `disable_model_invocation` | 可缺省；存在时必须是布尔值 |
| `replaces_native` | 可缺省；存在时必须是普通对象，键对应的值必须是字符串数组 |

`skill_file` 不属于 Dashboard `SkillInfo` 契约，不读取、不要求，也不因其作为未知字段出现而失败。

## 组件边界

新增内部守卫函数负责验证单个 Skill 原始对象；映射函数只在守卫通过后读取并构造标准斜杠命令。守卫保持在适配器文件内，不引入跨功能公共抽象。

未知字段不参与校验。这样可以同时满足已知契约严格校验和协议向前兼容。

## 错误处理

沿用现有 fail-fast 行为：任何已知字段不合法时，整个响应适配失败。错误消息包含 `slash command skill item <index>`、字段路径及预期类型，例如：

```text
slash command skill item 0 replaces_native.codex must be an array
```

不吞错、不自动纠正错误类型，也不为错误值提供默认值。

## 测试

- 保留真实 Dashboard wire shape 在 `omitempty` 字段缺省时可成功适配的回归测试。
- 增加完整合法字段样例，证明所有已知字段均可通过守卫。
- 使用表格化用例覆盖每类可选字段的错误类型。
- 覆盖 `replaces_native` 非对象及其成员非字符串数组两层错误。
- 覆盖未知字段可通过，锁定向前兼容行为。

## 非目标

- 不修改 Go `contract.SkillInfo`。
- 不增加或恢复 `skill_file` 字段。
- 不修改 Skill 扫描、审批、调用或斜杠命令选择逻辑。
- 不建立跨语言 schema 生成管线。
