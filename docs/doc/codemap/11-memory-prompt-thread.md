# 11 Memory 兼容契约 / Prompt / Thread 拆卷索引

> 兼容旧链接。本页只保留导航；memory runtime 已从当前生产树移除，prompt/thread 仍由独立卷描述。

## 新入口

- Memory 兼容契约、retired section 与 fail-fast 事实：[11-memory.md](11-memory.md)
- Prompt / Thread / Provider 启停链路：[11-prompt-thread.md](11-prompt-thread.md)

## 拆卷映射表

| 卷 | 重点章节 | 讲什么 |
|---|---|---|
| [11-memory.md](11-memory.md) | §1–§6 | `internal/contract/memory.go` 兼容面、prompt guard、旧 wiring 的 typed unsupported，以及退役能力证据 |
| [11-prompt-thread.md](11-prompt-thread.md) | 当前入口 / 说明 | prompt assembly、thread 生命周期、provider `start/resume/fork` 稳定入口 |

## 阅读顺序

1. 先读 [11-memory.md](11-memory.md) §1–§2，确认当前没有 memory runtime 实现。
2. 需要 prompt assembly / snapshot 时，转到 [11-prompt-thread.md](11-prompt-thread.md)。
3. 需要 thread/start、resume、fork 执行链时，联读 [07-module-write.md](07-module-write.md) §2.4 / §3.4。

## 跨卷跳转锚点

- `PROMPT_START_CURRENT_DATE`：见 [11-prompt-thread.md](11-prompt-thread.md)。
- `internal/contract/memory.go`：见 [11-memory.md](11-memory.md) §2.1。
- `internal/contract/prompt.go`：见 [11-memory.md](11-memory.md) §2.2 及 [11-prompt-thread.md](11-prompt-thread.md)。
- `internal/module/prompt/assembler.go`：见 [11-memory.md](11-memory.md) §3 及 [11-prompt-thread.md](11-prompt-thread.md)。

## 常见误导

- 本页及 [11-memory.md](11-memory.md) 不应被解释为旧 memory 文件树仍存在；当前卷只记录契约和拒绝边界。
- `memory_read` / `memory_write` 是退役名称，不是 MCP 注册表或 host-tool 实现。
- provider-native skill metadata 与已删除的本地 Skills UI 是两件事；provider 侧 carrier 仍由 DTO 卷描述。
