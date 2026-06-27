# 背景与约束

用户要求“后端全域”使用 30 个 agent 依据 `.agents/skills/注释规范/SKILL.md` 添加注释。

## 注释原则

- 中文 doc comment 优先，技术术语保留原文。
- 注释必须解释职责、约束、失败边界、状态变化或资源生命周期，不逐行复述代码。
- 导出符号、跨模块入口、provider/store/scheduler/thread/prompt/memory/skill/DAG 关键路径函数优先。
- 私有函数如有效代码长、分支复杂、嵌套深，必须说明负责什么和不能误改什么。
- 不给简单 getter/setter、小型纯映射、直观 JSX/测试片段写机械注释。

## 非目标

- 不修改业务逻辑。
- 不重命名符号。
- 不更新生成代码。
- 不通过降低 guard、baseline 或阈值来通过验证。
- 不做全仓 gofmt；只格式化自己改过的 Go 文件。

## MCP 编排说明

AGENTS.md 不再要求子代理强制绑定 `mcp-orch` 生命周期。worker 可直接使用当前平台原生子代理能力；只有任务确实需要持久 DAG、重试、租约或结构化交接记录时，才可选使用 `mcp-go-agent-orchestration`。如果未使用 mcp-orch，在最终报告中说明缺少持久 DAG 观测即可，不要把原生子代理视为绕过策略。
