**对发现至关重要：** 未来的 Claude 需要找到你的技能。

### 1. 丰富的 Description 字段

**目的：** Claude 会读取 description 来决定是否为给定任务加载某个技能。它应该回答：“我现在应该读这个技能吗？”

**格式：** 以 “Use when...” 开头，聚焦触发条件。

**关键：Description = 何时使用，而不是技能做什么**

description 只应描述触发条件。不要在 description 中概括技能流程或工作流。

**为什么重要：** 测试显示，当 description 概括技能工作流时，Claude 可能会遵循 description，而不是读取完整技能内容。一个写着 “code review between tasks” 的 description 让 Claude 只做了一次审查，尽管技能流程图清楚写了两次审查（先规格符合性，再代码质量）。

当 description 改为 “Use when executing implementation plans with independent tasks”（没有工作流总结）时，Claude 正确读取流程图并遵循两阶段审查流程。

**陷阱：** 概括工作流的 description 会创建 Claude 会采用的捷径。技能正文会变成 Claude 跳过的文档。

```yaml
# ❌ BAD: Summarizes workflow - Claude may follow this instead of reading skill
description: Use when executing plans - dispatches subagent per task with code review between tasks

# ❌ BAD: Too much process detail
description: Use for TDD - write test first, watch it fail, write minimal code, refactor

# ✅ GOOD: Just triggering conditions, no workflow summary
description: Use when executing implementation plans with independent tasks in the current session

# ✅ GOOD: Triggering conditions only
description: Use when implementing any feature or bugfix, before writing implementation code
```

**内容：**
- 使用能表明技能适用的具体触发词、症状和场景
- 描述*问题*（竞态条件、不一致行为），而不是*语言特定症状*（setTimeout、sleep）
- 除非技能本身是技术特定的，否则保持技术无关
- 如果技能是技术特定的，在触发条件中明确说明
- 使用第三人称（会注入系统提示）
- **绝不要概括技能流程或工作流**

```yaml
# ❌ BAD: Too abstract, vague, doesn't include when to use
description: For async testing

# ❌ BAD: First person
description: I can help you with async tests when they're flaky

# ❌ BAD: Mentions technology but skill isn't specific to it
description: Use when tests use setTimeout/sleep and are flaky

# ✅ GOOD: Starts with "Use when", describes problem, no workflow
description: Use when tests have race conditions, timing dependencies, or pass/fail inconsistently

# ✅ GOOD: Technology-specific skill with explicit trigger
description: Use when using React Router and handling authentication redirects
```

### 2. 关键词覆盖

使用 Claude 可能搜索的词：
- 错误消息：“Hook timed out”、“ENOTEMPTY”、“race condition”
- 症状：“flaky”、“hanging”、“zombie”、“pollution”
- 同义词：“timeout/hang/freeze”、“cleanup/teardown/afterEach”
- 工具：实际命令、库名、文件类型

### 3. 描述性命名

**使用主动语态，动词优先：**
- ✅ `creating-skills` 而不是 `skill-creation`
- ✅ `condition-based-waiting` 而不是 `async-test-helpers`

### 4. Token 效率（关键）

**问题：** getting-started 和频繁引用的技能会加载进每个对话。每个 token 都重要。

**目标词数：**
- getting-started 工作流：每个 <150 词
- 频繁加载技能：总计 <200 词
- 其他技能：<500 词（仍需简洁）

**技术：**

**把细节移到工具帮助里：**
```bash
# ❌ BAD: Document all flags in SKILL.md
search-conversations supports --text, --both, --after DATE, --before DATE, --limit N

# ✅ GOOD: Reference --help
search-conversations supports multiple modes and filters. Run --help for details.
```

**使用交叉引用：**
```markdown
# ❌ BAD: Repeat workflow details
When searching, dispatch subagent with template...
[20 lines of repeated instructions]

# ✅ GOOD: Reference other skill
始终使用子代理 (50-100x context savings). 强制要求: Use [other-skill-name] for workflow.
```

**压缩示例：**
```markdown
# ❌ BAD: Verbose example (42 words)
your human partner: "How did we handle authentication errors in React Router before?"
You: I'll search past conversations for React Router authentication patterns.
[Dispatch subagent with search query: "React Router authentication error handling 401"]

# ✅ GOOD: Minimal example (20 words)
Partner: "How did we handle auth errors in React Router?"
You: Searching...
[Dispatch subagent → synthesis]
```

**消除冗余：**
- 不要重复交叉引用技能中的内容
- 不要解释命令本身显而易见的内容
- 不要为同一种模式包含多个示例

**验证：**
```bash
wc -w skills/path/SKILL.md
# getting-started workflows: aim for <150 each
# Other frequently-loaded: aim for <200 total
```

**按你要做什么或核心洞察命名：**
- ✅ `condition-based-waiting` > `async-test-helpers`
- ✅ `using-skills` 而不是 `skill-usage`
- ✅ `flatten-with-flags` > `data-structure-refactoring`
- ✅ `root-cause-tracing` > `debugging-techniques`

**动名词（-ing）适合流程：**
- `creating-skills`、`testing-skills`、`debugging-with-logs`
- 主动，描述你正在采取的动作

### 4. 交叉引用其他技能

**编写引用其他技能的文档时：**

只使用技能名称，并使用明确的要求标记：
- ✅ 好：`**强制要求子技能:** Use superpowers:测试驱动开发`
- ✅ 好：`**强制要求背景知识:** 你必须理解 superpowers:系统化调试`
- ❌ 坏：`See skills/testing/测试驱动开发`（不清楚是否必需）
- ❌ 坏：`@skills/testing/测试驱动开发/SKILL.md`（强制加载，消耗上下文）

**为什么不用 @ 链接：** `@` 语法会立即强制加载文件，在你需要前就消耗 200k+ 上下文。
