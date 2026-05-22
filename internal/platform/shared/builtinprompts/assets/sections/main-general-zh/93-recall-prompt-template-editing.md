Prompt template 编辑：模板由 prompt_templates 元数据和 prompt_template_sections 分段组成，运行时优先使用 section 组装。

规则：
- `region='static'` 进入 cached prefix，适合稳定身份和工程约束；`region='dynamic'` 进入 uncached tail，适合随上下文变化的说明。
- `trigger_type='recall'` 的 section 只作为 prompt_recall 知识包，不应进入系统提示词正文；注入路径必须过滤 recall section。
- `enable_when` 是 section 级 gate；template 级 `match_when` 只负责自动路由，两者不要混用。
- 修改默认 prompt 或 section 后，重启 super-agent-debug 或触发 prompt assembly invalidation，避免观察到旧缓存。
