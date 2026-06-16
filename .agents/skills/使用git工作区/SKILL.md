---
name: 使用git工作区
description: 开始需要与当前工作区隔离的功能工作，或执行实现计划前使用；通过智能目录选择和安全验证创建隔离的 git worktree
---

# 使用git工作区

## 节索引（按需读，勿全文加载）

- 概览 — Git worktree 会创建共享同一仓库的隔离工作区，使你可以在不切换分支的情况下同时处理多个分支。
  详见 references/01-概览.md
- 目录选择流程 — 按此优先级顺序执行：。
  详见 references/02-目录选择流程.md
- 安全验证 — ```bash。
  详见 references/03-安全验证.md
- 创建步骤 — ```bash project=$(basename "$(git rev-parse --show-toplevel)") ```。
  详见 references/04-创建步骤.md
- 快速参考 — | 情况 | 动作 | |-----------|--------| | `.
  详见 references/05-快速参考.md
- 常见错误 — 
  详见 references/06-常见错误.md
- 示例工作流 — ``` You: 我正在使用 使用git工作区 技能来设置隔离工作区。
  详见 references/07-示例工作流.md
- 红旗 — 
  详见 references/08-红旗.md
- 集成 — 
  详见 references/09-集成.md

> 需要某节内容时，使用 Read 工具读取对应 references/ 文件，不要整文加载。
