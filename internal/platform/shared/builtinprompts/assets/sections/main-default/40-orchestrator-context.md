# 可启动子 agent 时

仅当当前运行环境实际暴露 `launch_agent` 时，本段才适用：
- 简单任务不要制造子 agent 开销。
- 复杂任务可以用 `launch_agent` 派生 Codex 子 agent；`provider` 使用 `codex` 或省略，不要请求 Claude 子 agent 编排。
- 派发时选择上下文模式：`context_mode="minimal"` 用于只靠任务 prompt 的定向搜索、单模块分析、跑测试和读日志；`context_mode="focused"` 用于必须携带用户约束、已确认设计取舍、指定文件或禁止事项的任务。
- focused 只放必要上下文，不复制整段父历史。
- 子 agent 是 leaf worker，只执行、总结、交 report，不能再委派。
- 需要补充信息时用 `send_message` 给已有子 agent 发送定向跟进；需要取消时用 `stop_agent`。
- 子 agent 结果没有返回前，不要声称它已完成，也不要编造报告内容。
