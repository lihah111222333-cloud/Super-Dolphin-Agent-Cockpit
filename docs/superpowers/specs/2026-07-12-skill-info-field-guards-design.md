# SkillInfo 前端字段守卫设计

## 背景

斜杠命令面板通过 Dashboard `skills` 响应构建 Skill 命令。此前前端错误地要求响应包含 `skill_file`，而后端 `contract.SkillInfo` 并不暴露该字段，导致单个字段契约偏差使整个 Skill 分类加载失败。

当前适配器已区分部分必填字段和 `omitempty` 字段，但尚未对 `SkillInfo` 的全部已知字段建立统一边界守卫。因此，未被 UI 直接消费的字段即使类型错误，也可能静默进入前端边界。

## 目标

- 在 `skillSlashCommandAdapter` 入口校验当前 `contract.SkillInfo` 的全部已知字段。
- 必填语义字段缺失或类型错误时立即抛出带条目索引和字段名的 `TypeError`。
- 可选字段缺省时合法；字段存在但类型错误时立即失败。
- 未登记字段立即失败；后端新增字段必须同步补充消费 registry 和验证语义。
- 保持现有 Skill 命令映射、排序和选择行为不变。
- 在 canonical 生产边界校验 owner、trust 和 content hash，在 turn 边界禁止结构化 `SkillRef` 降级为 name-only。

## 字段规则

| 字段 | 规则 |
| --- | --- |
| `name`、`dir`、`scope` | 非空字符串；`scope` 仅允许 `project` 或 `personal` |
| `description`、`summary` | 必须是字符串，允许空字符串 |
| `display_name`、`personal_type`、`content_hash` | 可缺省；存在时必须是字符串 |
| `trust` | 可缺省；存在时仅允许 `user`、`project` 或 `signed` |
| `trigger_words`、`force_words`、`allowed_tools` | 可缺省；存在时必须是字符串数组 |
| `disable_model_invocation` | 可缺省；存在时必须是布尔值 |
| `replaces_native` | 可缺省；存在时必须是普通对象，键对应的值必须是字符串数组 |

关系约束：`project` scope 的 `personal_type` 必须为空；`personal` scope 的 `personal_type` 必须是 `user`、`agent` 或 `imported`。

`skill_file` 不属于 Dashboard `SkillInfo` 契约；出现时按未知字段立即失败，不允许静默扩大 wire surface。

## 组件边界

`skillInfoFieldRegistry.json` 是前端运行时字段 registry。适配器遍历 registry 执行每个字段的 validator，只有全部通过后才构造标准斜杠命令。

Go archtest 通过反射枚举 `contract.SkillInfo` 的 JSON tags，并与同一份前端运行时 registry 双向比较：生产字段缺少登记或 registry 出现过期字段都会失败。字段兼容必须通过显式协议版本演进，不能依赖未知字段静默通过。

canonical 扫描在生成 `SkillInfo` 后校验 `name`、`dir`、scope/owner 关系、trust 和 SHA-256 content hash；frontmatter 显式声明非法 trust 时直接报错，不再按“未设置”回落。turn hydration 仅对没有结构化身份的旧引用保留 name-only 兼容；包含 `key`、`scope`、`personalType` 或 `path` 的引用必须精确命中，否则返回 `ErrSkillRefIdentityMismatch`。

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
- 覆盖未知字段失败以及 scope/owner 关系约束。
- archtest 动态枚举 Go producer 字段，锁定 registry 的 missing/stale 双向守卫。
- 覆盖 canonical producer 非法 owner/trust/hash fail-fast。
- 覆盖结构化 `SkillRef` 精确命中、身份不匹配失败，以及旧 name-only 兼容。

## 非目标

- 不修改 Go `contract.SkillInfo`。
- 不增加或恢复 `skill_file` 字段。
- 不建立跨语言 schema 生成管线。
