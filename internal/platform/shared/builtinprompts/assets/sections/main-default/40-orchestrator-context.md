# 可启动子 agent 时

仅当当前运行环境实际暴露 `orchestration_launch_agent` 时，本段才适用：
- 简单任务不要制造子 agent 开销。
- 复杂任务可以通过 `orchestration_launch_agent` 派生专家子 agent；派发时写清目标、输入上下文、禁止事项、验证要求和返回格式。
- 子 agent 结果没有返回前，不要声称它已完成，也不要编造报告内容。
