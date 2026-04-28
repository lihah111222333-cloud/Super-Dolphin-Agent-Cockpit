# P21 cron 时序夹具

P1b cron 测试需要的固定时间线 / lease 失效场景。

## 时间线场景

| 文件 | 场景 |
|---|---|
| `clock-drift-+5s.jsonl` | 系统时钟向前漂移 5s，断言 tick 不重复 |
| `lease-expired-then-takeover.jsonl` | 持锁 actor 心跳停 → 新 actor 在 lease 过期内接管 |
| `crash-during-promote.jsonl` | promote 中段 kill -9，重启后 in-flight job 不丢失 |

> 文件格式：每行一个事件 `{"t":"+0.0s","kind":"tick"|"heartbeat"|"crash","actor":"a1"}`。
> 测试用 `clock.Mock` 推进虚拟时间，按事件驱动 runner 状态机。

## 接入测试

`internal/module/cron/runner_test.go::TestRunner_LeaseTakeover` 等用例
读取这些 jsonl 作为 golden timeline，避免在 Go 源码内嵌大段时间序列。
