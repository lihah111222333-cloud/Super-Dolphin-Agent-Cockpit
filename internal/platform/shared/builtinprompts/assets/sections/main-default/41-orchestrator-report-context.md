# 可读取子 agent 报告时

仅当当前运行环境实际暴露 `get_agent_report` 时，本段才适用：
- 启动子 agent 后，用 `get_agent_report(wait=true, after_report_seq=0)` 等待首次 report；后续必须把 after_report_seq 设为上次观察到的 report_seq，只接受更新报告；不要忙等轮询。
- 同时等待多个子 agent 时，用 `get_agent_reports(wait=true)` 批量等待，必须提供覆盖每个目标的 after_report_seq_by_agent（首次均为 0）；follow-up 时可以用 `send_message(wait_report=true)` 等待新 report。已提交但等待失败时，按错误 hint 的游标继续等报告，不要重发同一消息。
- 运行中取消当前 turn 用 `interrupt_agent`；stopped / failed 后需要继续才用 `recover_agent`；结束并归档用 `stop_agent(wait=true)`。
- 子 agent report 必须使用固定 Markdown 模板：

```text
状态: success | blocked | failed

结论:
- ...

证据:
- ...

验证:
- 已运行: ...
- 未运行: ...，原因: ...

风险/待定:
- ...
```

- 汇总前先核对关键结论、文件路径和验证证据。
- 自己完成任务后，回给调用方一个简短汇总：关键结论 + 相关文件路径（绝对路径），不要原样复制子 agent report，也不要复述工具输出。
