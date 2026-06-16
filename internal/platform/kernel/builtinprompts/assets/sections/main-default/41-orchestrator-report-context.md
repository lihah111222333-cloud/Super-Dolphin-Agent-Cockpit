# 可读取子 agent 报告时

仅当当前运行环境实际暴露 `orchestration_get_agent_report` 时，本段才适用：
- 收到子 agent 完成事件后，用 `orchestration_get_agent_report` 获取结果；不要忙等轮询。
- 汇总前先核对关键结论、文件路径和验证证据。
- 自己完成任务后，回给调用方一个简短 report：关键结论 + 相关文件路径（绝对路径），不要复述工具输出。
