# 技能编写最佳实践

> 学习如何编写 Claude 能发现并成功使用的有效技能。

好的技能应该简洁、结构清晰，并经过真实使用测试。本指南提供实用的编写决策，帮助你编写 Claude 能有效发现和使用的技能。

关于技能如何工作的概念背景，请参阅 [Skills overview](/en/docs/agents-and-tools/agent-skills/overview)。

## 核心原则

### 简洁是关键

[上下文窗口](https://platform.claude.com/docs/en/build-with-claude/context-windows) 是公共资源。你的技能会与 Claude 需要知道的所有其他内容共享上下文窗口，包括：

* 系统提示
* 对话历史
* 其他技能的元数据
* 你的实际请求

技能中的每个 token 并不都会立刻产生成本。启动时，只会预加载所有技能的元数据（name 和 description）。Claude 只有在技能相关时才读取 SKILL.md，并且只在需要时读取额外文件。但 SKILL.md 仍然应该简洁：一旦 Claude 加载它，每个 token 都会与对话历史和其他上下文竞争。

**默认假设**：Claude 已经很聪明

只添加 Claude 还不知道的上下文。质疑每一条信息：

* “Claude 真的需要这个解释吗？”
* “我能假设 Claude 已经知道这个吗？”
* “这段文字值得它消耗的 token 吗？”

**好示例：简洁**（约 50 token）：

````markdown  theme={null}
## Extract PDF text

Use pdfplumber for text extraction:

```python
import pdfplumber

with pdfplumber.open("file.pdf") as pdf:
    text = pdf.pages[0].extract_text()
```
````

**坏示例：太啰嗦**（约 150 token）：

```markdown  theme={null}
## Extract PDF text

PDF (Portable Document Format) files are a common file format that contains
text, images, and other content. To extract text from a PDF, you'll need to
use a library. There are many libraries available for PDF processing, but we
recommend pdfplumber because it's easy to use and handles most cases well.
First, you'll need to install it using pip. Then you can use the code below...
```

简洁版本假设 Claude 已经知道 PDF 是什么，以及库如何工作。

### 设置合适的自由度

让具体程度匹配任务的脆弱性和可变性。

**高自由度**（基于文本的说明）：

适用场景：

* 多种方案都有效
* 决策依赖上下文
* 需要启发式方法指导

示例：

```markdown  theme={null}
## Code review process

1. Analyze the code structure and organization
2. Check for potential bugs or edge cases
3. Suggest improvements for readability and maintainability
4. Verify adherence to project conventions
```

**中等自由度**（伪代码或带参数脚本）：

适用场景：

* 存在偏好的模式
* 允许一些变化
* 配置会影响行为

示例：

````markdown  theme={null}
## Generate report

Use this template and customize as needed:

```python
def generate_report(data, format="markdown", include_charts=True):
    # Process data
    # Generate output in specified format
    # Optionally include visualizations
```
````

**低自由度**（具体脚本，参数很少或没有参数）：

适用场景：

* 操作脆弱且容易出错
* 一致性至关重要
* 必须遵循特定顺序

示例：

````markdown  theme={null}
## Database migration

Run exactly this script:

```bash
python scripts/migrate.py --verify --backup
```

Do not modify the command or add additional flags.
````

**类比**：把 Claude 想象成在探索路径的机器人：

* **两侧是悬崖的窄桥**：只有一种安全前进方式。提供明确护栏和精确指令（低自由度）。例如必须按精确顺序运行的数据库迁移。
* **没有危险的开阔地**：很多路径都能成功。给出大方向，相信 Claude 找到最佳路线（高自由度）。例如代码审查，最佳方式取决于上下文。

### 用你计划使用的所有模型测试

技能是对模型的补充，因此效果取决于底层模型。用你计划搭配使用的所有模型测试你的技能。

**按模型考虑测试：**

* **Claude Haiku**（快速、经济）：技能是否提供了足够指导？
* **Claude Sonnet**（平衡）：技能是否清晰且高效？
* **Claude Opus**（强推理）：技能是否避免过度解释？

对 Opus 完美有效的内容，可能需要为 Haiku 添加更多细节。如果你计划让技能跨多个模型使用，就让说明对所有模型都表现良好。

## 技能结构

<Note>
  **YAML Frontmatter**：SKILL.md frontmatter 需要两个字段：

  * `name`：技能的人类可读名称（最多 64 个字符）
  * `description`：一行描述技能做什么以及何时使用（最多 1024 个字符）

  完整技能结构详情见 [Skills overview](/en/docs/agents-and-tools/agent-skills/overview#skill-structure)。
</Note>

### 命名约定

使用一致命名模式，让技能更容易引用和讨论。我们建议技能名称使用**动名词形式**（动词 + -ing），因为它能清楚描述技能提供的活动或能力。

**好命名示例（动名词形式）：**

* "Processing PDFs"
* "Analyzing spreadsheets"
* "Managing databases"
* "Testing code"
* "Writing documentation"

**可接受替代：**

* 名词短语："PDF Processing"、"Spreadsheet Analysis"
* 面向动作："Process PDFs"、"Analyze Spreadsheets"

**避免：**

* 含糊名称："Helper"、"Utils"、"Tools"
* 过于泛化："Documents"、"Data"、"Files"
* 技能集合内部命名模式不一致

一致命名有助于：

* 在文档和对话中引用技能
* 一眼理解技能用途
* 组织和搜索多个技能
* 维护专业、统一的技能库

### 编写有效 description

`description` 字段支持技能发现，应同时包含技能做什么以及何时使用。

<Warning>
  **始终使用第三人称**。description 会注入系统提示，不一致的视角可能导致发现问题。

  * **好：** "Processes Excel files and generates reports"
  * **避免：** "I can help you process Excel files"
  * **避免：** "You can use this to process Excel files"
</Warning>

**要具体并包含关键词。** 同时包含技能做什么，以及何时使用的具体触发/上下文。

每个技能只有一个 description 字段。description 对技能选择至关重要：Claude 用它从可能 100+ 个可用技能中选择正确技能。description 必须提供足够细节，让 Claude 知道何时选择此技能；SKILL.md 的其他部分则提供实现细节。

有效示例：

**PDF Processing 技能：**

```yaml  theme={null}
description: Extract text and tables from PDF files, fill forms, merge documents. Use when working with PDF files or when the user mentions PDFs, forms, or document extraction.
```

**Excel Analysis 技能：**

```yaml  theme={null}
description: Analyze Excel spreadsheets, create pivot tables, generate charts. Use when analyzing Excel files, spreadsheets, tabular data, or .xlsx files.
```

**Git Commit Helper 技能：**

```yaml  theme={null}
description: Generate descriptive commit messages by analyzing git diffs. Use when the user asks for help writing commit messages or reviewing staged changes.
```

避免这些含糊描述：

```yaml  theme={null}
description: Helps with documents
```

```yaml  theme={null}
description: Processes data
```

```yaml  theme={null}
description: Does stuff with files
```

### 渐进式披露模式

SKILL.md 是概览，按需把 Claude 指向详细材料，就像入门指南中的目录。关于渐进式披露如何工作的解释，见概览中的 [How Skills work](/en/docs/agents-and-tools/agent-skills/overview#how-skills-work)。

**实践指南：**

* 为获得最佳性能，保持 SKILL.md 正文低于 500 行
* 接近此限制时，将内容拆分到单独文件
* 使用下面的模式有效组织说明、代码和资源

#### 视觉概览：从简单到复杂

基础技能只需要一个包含元数据和说明的 SKILL.md 文件：

<img src="https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-simple-file.png?fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=87782ff239b297d9a9e8e1b72ed72db9" alt="Simple SKILL.md file showing YAML frontmatter and markdown body" data-og-width="2048" width="2048" data-og-height="1153" height="1153" data-path="images/agent-skills-simple-file.png" data-optimize="true" data-opv="3" srcset="https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-simple-file.png?w=280&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=c61cc33b6f5855809907f7fda94cd80e 280w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-simple-file.png?w=560&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=90d2c0c1c76b36e8d485f49e0810dbfd 560w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-simple-file.png?w=840&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=ad17d231ac7b0bea7e5b4d58fb4aeabb 840w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-simple-file.png?w=1100&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=f5d0a7a3c668435bb0aee9a3a8f8c329 1100w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-simple-file.png?w=1650&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=0e927c1af9de5799cfe557d12249f6e6 1650w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-simple-file.png?w=2500&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=46bbb1a51dd4c8202a470ac8c80a893d 2500w" />

随着技能增长，你可以打包 Claude 只在需要时加载的额外内容：

<img src="https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-bundling-content.png?fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=a5e0aa41e3d53985a7e3e43668a33ea3" alt="Bundling additional reference files like reference.md and forms.md." data-og-width="2048" width="2048" data-og-height="1327" height="1327" data-path="images/agent-skills-bundling-content.png" data-optimize="true" data-opv="3" srcset="https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-bundling-content.png?w=280&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=f8a0e73783e99b4a643d79eac86b70a2 280w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-bundling-content.png?w=560&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=dc510a2a9d3f14359416b706f067904a 560w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-bundling-content.png?w=840&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=82cd6286c966303f7dd914c28170e385 840w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-bundling-content.png?w=1100&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=56f3be36c77e4fe4b523df209a6824c6 1100w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-bundling-content.png?w=1650&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=d22b5161b2075656417d56f41a74f3dd 1650w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-bundling-content.png?w=2500&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=3dd4bdd6850ffcc96c6c45fcb0acd6eb 2500w" />

完整技能目录结构可能如下：

```
pdf/
├── SKILL.md              # Main instructions (loaded when triggered)
├── FORMS.md              # Form-filling guide (loaded as needed)
├── reference.md          # API reference (loaded as needed)
├── examples.md           # Usage examples (loaded as needed)
└── scripts/
    ├── analyze_form.py   # Utility script (executed, not loaded)
    ├── fill_form.py      # Form filling script
    └── validate.py       # Validation script
```

#### 模式 1：带参考文件的高层指南

````markdown  theme={null}
---
name: PDF Processing
description: Extracts text and tables from PDF files, fills forms, and merges documents. Use when working with PDF files or when the user mentions PDFs, forms, or document extraction.
---

# PDF Processing

## Quick start

Extract text with pdfplumber:
```python
import pdfplumber
with pdfplumber.open("file.pdf") as pdf:
    text = pdf.pages[0].extract_text()
```

## Advanced features

**Form filling**: See [FORMS.md](FORMS.md) for complete guide
**API reference**: See [REFERENCE.md](REFERENCE.md) for all methods
**Examples**: See [EXAMPLES.md](EXAMPLES.md) for common patterns
````

Claude 只在需要时加载 FORMS.md、REFERENCE.md 或 EXAMPLES.md。

#### 模式 2：按领域组织

对包含多个领域的技能，按领域组织内容，避免加载无关上下文。当用户询问销售指标时，Claude 只需要读取销售相关 schema，而不需要财务或营销数据。这能保持 token 使用低、上下文聚焦。

```
bigquery-skill/
├── SKILL.md (overview and navigation)
└── reference/
    ├── finance.md (revenue, billing metrics)
    ├── sales.md (opportunities, pipeline)
    ├── product.md (API usage, features)
    └── marketing.md (campaigns, attribution)
```

````markdown SKILL.md theme={null}
# BigQuery Data Analysis

## Available datasets

**Finance**: Revenue, ARR, billing → See [reference/finance.md](reference/finance.md)
**Sales**: Opportunities, pipeline, accounts → See [reference/sales.md](reference/sales.md)
**Product**: API usage, features, adoption → See [reference/product.md](reference/product.md)
**Marketing**: Campaigns, attribution, email → See [reference/marketing.md](reference/marketing.md)

## Quick search

Find specific metrics using grep:

```bash
grep -i "revenue" reference/finance.md
grep -i "pipeline" reference/sales.md
grep -i "api usage" reference/product.md
```
````

#### 模式 3：条件式细节

展示基础内容，并链接到高级内容：

```markdown  theme={null}
# DOCX Processing

## Creating documents

Use docx-js for new documents. See [DOCX-JS.md](DOCX-JS.md).

## Editing documents

For simple edits, modify the XML directly.

**For tracked changes**: See [REDLINING.md](REDLINING.md)
**For OOXML details**: See [OOXML.md](OOXML.md)
```

Claude 只在用户需要这些功能时读取 REDLINING.md 或 OOXML.md。

### 避免深层嵌套引用

当引用文件再引用其他文件时，Claude 可能只部分读取文件。遇到嵌套引用时，Claude 可能使用 `head -100` 之类命令预览内容，而不是读取完整文件，导致信息不完整。

**引用文件应与 SKILL.md 保持一层深度。** 所有参考文件都应直接从 SKILL.md 链接，确保 Claude 在需要时读取完整文件。

**坏示例：太深：**

```markdown  theme={null}
# SKILL.md
See [advanced.md](advanced.md)...

# advanced.md
See [details.md](details.md)...

# details.md
Here's the actual information...
```

**好示例：一层深：**

```markdown  theme={null}
# SKILL.md

**Basic usage**: [instructions in SKILL.md]
**Advanced features**: See [advanced.md](advanced.md)
**API reference**: See [reference.md](reference.md)
**Examples**: See [examples.md](examples.md)
```

### 给较长参考文件添加目录

对超过 100 行的参考文件，在顶部加入目录。这样即使用部分读取预览，Claude 也能看到可用信息的完整范围。

**示例：**

```markdown  theme={null}
# API Reference

## Contents
- Authentication and setup
- Core methods (create, read, update, delete)
- Advanced features (batch operations, webhooks)
- Error handling patterns
- Code examples

## Authentication and setup
...

## Core methods
...
```

Claude 随后可以读取完整文件，或按需跳转到具体章节。

关于基于文件系统的架构如何支持渐进式披露，见下方高级章节中的 [Runtime environment](#runtime-environment)。

## 工作流和反馈循环

### 对复杂任务使用工作流

将复杂操作拆成清晰、顺序化的步骤。对特别复杂的工作流，提供 Claude 可以复制到回复中并逐步勾选的检查清单。

**示例 1：研究综合工作流**（适用于没有代码的技能）：

````markdown  theme={null}
## Research synthesis workflow

Copy this checklist and track your progress:

```
Research Progress:
- [ ] Step 1: Read all source documents
- [ ] Step 2: Identify key themes
- [ ] Step 3: Cross-reference claims
- [ ] Step 4: Create structured summary
- [ ] Step 5: Verify citations
```

**Step 1: Read all source documents**

Review each document in the `sources/` directory. Note the main arguments and supporting evidence.

**Step 2: Identify key themes**

Look for patterns across sources. What themes appear repeatedly? Where do sources agree or disagree?

**Step 3: Cross-reference claims**

For each major claim, verify it appears in the source material. Note which source supports each point.

**Step 4: Create structured summary**

Organize findings by theme. Include:
- Main claim
- Supporting evidence from sources
- Conflicting viewpoints (if any)

**Step 5: Verify citations**

Check that every claim references the correct source document. If citations are incomplete, return to Step 3.
````

此示例展示了工作流如何应用于不需要代码的分析任务。检查清单模式适用于任何复杂的多步骤流程。

**示例 2：PDF 表单填写工作流**（适用于带代码的技能）：

````markdown  theme={null}
## PDF form filling workflow

Copy this checklist and check off items as you complete them:

```
Task Progress:
- [ ] Step 1: Analyze the form (run analyze_form.py)
- [ ] Step 2: Create field mapping (edit fields.json)
- [ ] Step 3: Validate mapping (run validate_fields.py)
- [ ] Step 4: Fill the form (run fill_form.py)
- [ ] Step 5: Verify output (run verify_output.py)
```

**Step 1: Analyze the form**

Run: `python scripts/analyze_form.py input.pdf`

This extracts form fields and their locations, saving to `fields.json`.

**Step 2: Create field mapping**

Edit `fields.json` to add values for each field.

**Step 3: Validate mapping**

Run: `python scripts/validate_fields.py fields.json`

Fix any validation errors before continuing.

**Step 4: Fill the form**

Run: `python scripts/fill_form.py input.pdf fields.json output.pdf`

**Step 5: Verify output**

Run: `python scripts/verify_output.py output.pdf`

If verification fails, return to Step 2.
````

清晰步骤可以防止 Claude 跳过关键验证。检查清单也帮助 Claude 和你跟踪多步骤工作流进度。

### 实现反馈循环

**常见模式**：运行验证器 → 修复错误 → 重复

这个模式能显著提高输出质量。

**示例 1：风格指南符合性**（适用于没有代码的技能）：

```markdown  theme={null}
## Content review process

1. Draft your content following the guidelines in STYLE_GUIDE.md
2. Review against the checklist:
   - Check terminology consistency
   - Verify examples follow the standard format
   - Confirm all required sections are present
3. If issues found:
   - Note each issue with specific section reference
   - Revise the content
   - Review the checklist again
4. Only proceed when all requirements are met
5. Finalize and save the document
```

这里用参考文档而不是脚本展示验证循环模式。“验证器”是 STYLE_GUIDE.md，Claude 通过读取和比较来执行检查。

**示例 2：文档编辑流程**（适用于带代码的技能）：

```markdown  theme={null}
## Document editing process

1. Make your edits to `word/document.xml`
2. **Validate immediately**: `python ooxml/scripts/validate.py unpacked_dir/`
3. If validation fails:
   - Review the error message carefully
   - Fix the issues in the XML
   - Run validation again
4. **Only proceed when validation passes**
5. Rebuild: `python ooxml/scripts/pack.py unpacked_dir/ output.docx`
6. Test the output document
```

验证循环可以及早捕获错误。

## 内容指南

### 避免时间敏感信息

不要包含会过期的信息：

**坏示例：时间敏感**（会变错）：

```markdown  theme={null}
If you're doing this before August 2025, use the old API.
After August 2025, use the new API.
```

**好示例**（使用 “old patterns” 章节）：

```markdown  theme={null}
## Current method

Use the v2 API endpoint: `api.example.com/v2/messages`

## Old patterns

<details>
<summary>Legacy v1 API (deprecated 2025-08)</summary>

The v1 API used: `api.example.com/v1/messages`

This endpoint is no longer supported.
</details>
```

旧模式章节提供历史上下文，同时不干扰主体内容。

### 使用一致术语

选择一个术语并在整个技能中保持一致：

**好：一致**

* 始终使用 “API endpoint”
* 始终使用 “field”
* 始终使用 “extract”

**坏：不一致**

* 混用 “API endpoint”、“URL”、“API route”、“path”
* 混用 “field”、“box”、“element”、“control”
* 混用 “extract”、“pull”、“get”、“retrieve”

一致性帮助 Claude 理解并遵循指令。

## 常见模式

### 模板模式

为输出格式提供模板。严格程度应与你的需求匹配。

**对严格要求**（如 API 响应或数据格式）：

````markdown  theme={null}
## Report structure

始终 use this exact template structure:

```markdown
# [Analysis Title]

## Executive summary
[One-paragraph overview of key findings]

## Key findings
- Finding 1 with supporting data
- Finding 2 with supporting data
- Finding 3 with supporting data

## Recommendations
1. Specific actionable recommendation
2. Specific actionable recommendation
```
````

**对弹性指南**（适合根据情况调整时）：

````markdown  theme={null}
## Report structure

Here is a sensible default format, but use your best judgment based on the analysis:

```markdown
# [Analysis Title]

## Executive summary
[Overview]

## Key findings
[Adapt sections based on what you discover]

## Recommendations
[Tailor to the specific context]
```

Adjust sections as needed for the specific analysis type.
````

### 示例模式

对输出质量依赖示例的技能，像常规提示一样提供输入/输出对：

````markdown  theme={null}
## Commit message format

Generate commit messages following these examples:

**Example 1:**
Input: Added user authentication with JWT tokens
Output:
```
feat(auth): implement JWT-based authentication

Add login endpoint and token validation middleware
```

**Example 2:**
Input: Fixed bug where dates displayed incorrectly in reports
Output:
```
fix(reports): correct date formatting in timezone conversion

Use UTC timestamps consistently across report generation
```

**Example 3:**
Input: Updated dependencies and refactored error handling
Output:
```
chore: update dependencies and refactor error handling

- Upgrade lodash to 4.17.21
- Standardize error response format across endpoints
```

Follow this style: type(scope): brief description, then detailed explanation.
````

示例比单纯描述更能帮助 Claude 理解期望风格和详细程度。

### 条件式工作流模式

引导 Claude 经过决策点：

```markdown  theme={null}
## Document modification workflow

1. Determine the modification type:

   **Creating new content?** → Follow "Creation workflow" below
   **Editing existing content?** → Follow "Editing workflow" below

2. Creation workflow:
   - Use docx-js library
   - Build document from scratch
   - Export to .docx format

3. Editing workflow:
   - Unpack existing document
   - Modify XML directly
   - Validate after each change
   - Repack when complete
```

<Tip>
  如果工作流变得庞大或复杂且有很多步骤，考虑把它们放入单独文件，并告诉 Claude 根据当前任务读取合适文件。
</Tip>

## 评估和迭代

### 先构建评估

**在编写大量文档之前创建评估。** 这能确保技能解决真实问题，而不是记录想象出来的问题。

**评估驱动开发：**

1. **识别缺口**：在没有技能的情况下，让 Claude 执行代表性任务。记录具体失败或缺失上下文
2. **创建评估**：构建三个测试这些缺口的场景
3. **建立基线**：衡量没有技能时 Claude 的表现
4. **编写最小说明**：只创建足够处理缺口并通过评估的内容
5. **迭代**：执行评估，与基线比较，并细化

这个方法确保你解决实际问题，而不是预判可能永远不会出现的需求。

**评估结构：**

```json  theme={null}
{
  "skills": ["pdf-processing"],
  "query": "Extract all text from this PDF file and save it to output.txt",
  "files": ["test-files/document.pdf"],
  "expected_behavior": [
    "Successfully reads the PDF file using an appropriate PDF processing library or command-line tool",
    "Extracts text content from all pages in the document without missing any pages",
    "Saves the extracted text to a file named output.txt in a clear, readable format"
  ]
}
```

<Note>
  此示例展示了带简单测试评分规则的数据驱动评估。目前我们不提供内置评估运行方式。用户可以创建自己的评估系统。评估是衡量技能效果的事实来源。
</Note>

### 与 Claude 迭代开发技能

最有效的技能开发流程会让 Claude 自身参与。你可以与一个 Claude 实例（“Claude A”）协作，创建供其他实例（“Claude B”）使用的技能。Claude A 帮你设计和细化说明，Claude B 在真实任务中测试它们。这之所以有效，是因为 Claude 模型既理解如何编写有效代理指令，也理解代理需要哪些信息。

**创建新技能：**

1. **不使用技能完成一次任务**：用普通提示和 Claude A 解决一个问题。工作过程中，你会自然提供上下文、解释偏好、共享流程知识。注意哪些信息你反复提供。

2. **识别可复用模式**：任务完成后，识别你提供的哪些上下文对未来类似任务有用。

   **示例**：如果你完成了一次 BigQuery 分析，你可能提供了表名、字段定义、过滤规则（例如“始终排除测试账户”）和常见查询模式。

3. **请 Claude A 创建技能**：“创建一个技能，记录我们刚才使用的 BigQuery 分析模式。包含表 schema、命名约定，以及过滤测试账户的规则。”

   <Tip>
     Claude 模型原生理解技能格式和结构。你不需要特殊系统提示或“writing skills”技能来让 Claude 帮你创建技能。只需请 Claude 创建技能，它会生成结构正确的 SKILL.md 内容，并带合适的 frontmatter 和正文。
   </Tip>

4. **审查简洁性**：检查 Claude A 是否添加了不必要解释。可以问：“删除关于 win rate 含义的解释，Claude 已经知道这个。”

5. **改进信息架构**：请 Claude A 更有效地组织内容。例如：“把表 schema 放到单独的参考文件里组织。之后我们可能会添加更多表。”

6. **在相似任务上测试**：用 Claude B（加载了该技能的新实例）处理相关用例。观察 Claude B 是否找到正确资料、正确应用规则并成功完成任务。

7. **基于观察迭代**：如果 Claude B 卡住或漏掉内容，带着具体情况回到 Claude A：“Claude 使用这个技能时，忘了按 Q4 日期过滤。我们是否应该添加一个关于日期过滤模式的章节？”

**迭代现有技能：**

改进技能时也使用同样的层级模式。你在以下两者之间交替：

* **与 Claude A 合作**（帮助细化技能的专家）
* **用 Claude B 测试**（使用技能执行真实工作的代理）
* **观察 Claude B 的行为**，并把洞察带回 Claude A

1. **在真实工作流中使用技能**：给 Claude B（已加载技能）真实任务，而不是测试场景

2. **观察 Claude B 的行为**：记录它在哪里卡住、成功或做出意外选择

   **示例观察**：“当我让 Claude B 做区域销售报告时，它写了查询，但忘记过滤测试账户，尽管技能提到了这个规则。”

3. **回到 Claude A 改进**：分享当前 SKILL.md 并描述你的观察。询问：“我发现当我要求 Claude B 生成区域销售报告时，它忘了过滤测试账户。技能提到了过滤，但也许不够醒目？”

4. **审查 Claude A 的建议**：Claude A 可能建议重新组织内容以突出规则，使用更强语言如 “MUST filter” 而不是 “always filter”，或重构工作流章节。

5. **应用并测试变更**：用 Claude A 的细化建议更新技能，然后再次用 Claude B 处理相似请求

6. **基于使用持续重复**：遇到新场景时继续观察-细化-测试循环。每次迭代都基于真实代理行为而不是假设改进技能。

**收集团队反馈：**

1. 与队友分享技能并观察他们的使用
2. 询问：技能是否在预期时激活？说明是否清楚？缺少什么？
3. 纳入反馈，处理你自己使用模式中的盲点

**为什么这种方法有效**：Claude A 理解代理需求，你提供领域专业知识，Claude B 通过真实使用暴露缺口，迭代细化基于观察到的行为而不是假设改进技能。

### 观察 Claude 如何浏览技能

迭代技能时，注意 Claude 在实践中实际如何使用它们。留意：

* **意外探索路径**：Claude 是否以你没预料的顺序读取文件？这可能说明你的结构不如你以为的直观
* **遗漏连接**：Claude 是否没有跟随到重要文件的引用？你的链接可能需要更明确或更突出
* **过度依赖某些章节**：如果 Claude 反复读取同一个文件，考虑这些内容是否应该放进主 SKILL.md
* **被忽略内容**：如果 Claude 从不访问某个打包文件，它可能不必要，或在主说明中提示不够清楚

基于这些观察迭代，而不是基于假设。技能元数据中的 `name` 和 `description` 尤其关键。Claude 会用它们决定是否针对当前任务触发技能。确保它们清楚描述技能做什么以及何时使用。

## 要避免的反模式

### 避免 Windows 风格路径

即使在 Windows 上，也始终在文件路径中使用正斜杠：

* ✓ **好**：`scripts/helper.py`、`reference/guide.md`
* ✗ **避免**：`scripts\helper.py`、`reference\guide.md`

Unix 风格路径可跨平台工作，而 Windows 风格路径会在 Unix 系统上导致错误。

### 避免提供过多选项

除非必要，不要呈现多种方法：

````markdown  theme={null}
**Bad example: Too many choices** (confusing):
"You can use pypdf, or pdfplumber, or PyMuPDF, or pdf2image, or..."

**Good example: Provide a default** (with escape hatch):
"Use pdfplumber for text extraction:
```python
import pdfplumber
```

For scanned PDFs requiring OCR, use pdf2image with pytesseract instead."
````

## 高级：带可执行代码的技能

以下章节聚焦于包含可执行脚本的技能。如果你的技能只使用 Markdown 说明，跳到 [有效技能检查清单](#有效技能检查清单)。

### 解决问题，不要推给 Claude

为技能编写脚本时，处理错误条件，而不是把问题推给 Claude。

**好示例：明确处理错误：**

```python  theme={null}
def process_file(path):
    """Process a file, creating it if it doesn't exist."""
    try:
        with open(path) as f:
            return f.read()
    except FileNotFoundError:
        # Create file with default content instead of failing
        print(f"File {path} not found, creating default")
        with open(path, 'w') as f:
            f.write('')
        return ''
    except PermissionError:
        # Provide alternative instead of failing
        print(f"Cannot access {path}, using default")
        return ''
```

**坏示例：推给 Claude：**

```python  theme={null}
def process_file(path):
    # Just fail and let Claude figure it out
    return open(path).read()
```

配置参数也应有理由和文档，避免 “voodoo constants”（Ousterhout 定律）。如果你不知道正确值，Claude 又如何确定？

**好示例：自解释：**

```python  theme={null}
# HTTP requests typically complete within 30 seconds
# Longer timeout accounts for slow connections
REQUEST_TIMEOUT = 30

# Three retries balances reliability vs speed
# Most intermittent failures resolve by the second retry
MAX_RETRIES = 3
```

**坏示例：魔法数字：**

```python  theme={null}
TIMEOUT = 47  # Why 47?
RETRIES = 5   # Why 5?
```

### 提供实用脚本

即使 Claude 可以写脚本，预制脚本仍有优势：

**实用脚本的收益：**

* 比生成代码更可靠
* 节省 token（无需把代码放进上下文）
* 节省时间（无需生成代码）
* 确保每次使用一致

<img src="https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-executable-scripts.png?fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=4bbc45f2c2e0bee9f2f0d5da669bad00" alt="Bundling executable scripts alongside instruction files" data-og-width="2048" width="2048" data-og-height="1154" height="1154" data-path="images/agent-skills-executable-scripts.png" data-optimize="true" data-opv="3" srcset="https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-executable-scripts.png?w=280&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=9a04e6535a8467bfeea492e517de389f 280w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-executable-scripts.png?w=560&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=e49333ad90141af17c0d7651cca7216b 560w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-executable-scripts.png?w=840&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=954265a5df52223d6572b6214168c428 840w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-executable-scripts.png?w=1100&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=2ff7a2d8f2a83ee8af132b29f10150fd 1100w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-executable-scripts.png?w=1650&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=48ab96245e04077f4d15e9170e081cfb 1650w, https://mintcdn.com/anthropic-claude-docs/4Bny2bjzuGBK7o00/images/agent-skills-executable-scripts.png?w=2500&fit=max&auto=format&n=4Bny2bjzuGBK7o00&q=85&s=0301a6c8b3ee879497cc5b5483177c90 2500w" />

上图展示了可执行脚本如何与说明文件配合工作。说明文件（forms.md）引用脚本，Claude 可以执行它，而不必把脚本内容加载进上下文。

**重要区别**：在说明中明确 Claude 应该：

* **执行脚本**（最常见）：“运行 `analyze_form.py` 提取字段”
* **把脚本作为参考阅读**（用于复杂逻辑）：“查看 `analyze_form.py`，了解字段提取算法”

对大多数实用脚本，优先执行，因为它更可靠、更高效。脚本执行如何工作，见下方 [Runtime environment](#runtime-environment)。

**示例：**

````markdown  theme={null}
## Utility scripts

**analyze_form.py**: Extract all form fields from PDF

```bash
python scripts/analyze_form.py input.pdf > fields.json
```

Output format:
```json
{
  "field_name": {"type": "text", "x": 100, "y": 200},
  "signature": {"type": "sig", "x": 150, "y": 500}
}
```

**validate_boxes.py**: Check for overlapping bounding boxes

```bash
python scripts/validate_boxes.py fields.json
# Returns: "OK" or lists conflicts
```

**fill_form.py**: Apply field values to PDF

```bash
python scripts/fill_form.py input.pdf fields.json output.pdf
```
````

### 使用视觉分析

当输入可以渲染成图像时，让 Claude 分析它们：

````markdown  theme={null}
## Form layout analysis

1. Convert PDF to images:
   ```bash
   python scripts/pdf_to_images.py form.pdf
   ```

2. Analyze each page image to identify form fields
3. Claude can see field locations and types visually
````

<Note>
  在这个示例中，你需要编写 `pdf_to_images.py` 脚本。
</Note>

Claude 的视觉能力有助于理解布局和结构。

### 创建可验证的中间输出

当 Claude 执行复杂、开放式任务时，可能会出错。“计划-验证-执行”模式通过让 Claude 先以结构化格式创建计划，再用脚本验证该计划，最后执行，来及早捕获错误。

**示例**：想象让 Claude 根据电子表格更新 PDF 中的 50 个表单字段。没有验证时，Claude 可能引用不存在的字段、创建冲突值、漏掉必填字段，或错误应用更新。

**解决方案**：使用上面展示的工作流模式（PDF 表单填写），但添加一个中间 `changes.json` 文件，并在应用变更前验证它。工作流变成：分析 → **创建计划文件** → **验证计划** → 执行 → 验证。

**为什么这个模式有效：**

* **及早捕获错误**：验证会在应用变更前发现问题
* **机器可验证**：脚本提供客观验证
* **可逆计划**：Claude 可以在不触碰原始文件的情况下迭代计划
* **调试清晰**：错误消息指向具体问题

**何时使用**：批量操作、破坏性变更、复杂验证规则、高风险操作。

**实现提示**：验证脚本应输出详细且具体的错误消息，例如 “Field 'signature_date' not found. Available fields: customer_name, order_total, signature_date_signed”，以帮助 Claude 修复问题。

### 打包依赖

技能运行在带平台特定限制的代码执行环境中：

* **claude.ai**：可以从 npm 和 PyPI 安装包，并从 GitHub 仓库拉取
* **Anthropic API**：没有网络访问，也不能在运行时安装包

在 SKILL.md 中列出必需包，并根据 [code execution tool documentation](/en/docs/agents-and-tools/tool-use/code-execution-tool) 验证它们可用。

### 运行时环境

技能运行在带文件系统访问、bash 命令和代码执行能力的代码执行环境中。关于此架构的概念解释，见概览中的 [The Skills architecture](/en/docs/agents-and-tools/agent-skills/overview#the-skills-architecture)。

**这如何影响你的编写：**

**Claude 如何访问技能：**

1. **预加载元数据**：启动时，所有技能 YAML frontmatter 中的 name 和 description 会加载到系统提示
2. **按需读取文件**：需要时，Claude 使用 bash Read 工具从文件系统访问 SKILL.md 和其他文件
3. **高效执行脚本**：实用脚本可以通过 bash 执行，而不把完整内容加载进上下文。只有脚本输出消耗 token
4. **大文件没有上下文惩罚**：参考文件、数据或文档只有在实际读取时才消耗上下文 token

* **文件路径很重要**：Claude 像浏览文件系统一样浏览技能目录。使用正斜杠（`reference/guide.md`），不要使用反斜杠
* **文件名要描述性**：使用能说明内容的名称：`form_validation_rules.md`，不要用 `doc2.md`
* **为发现而组织**：按领域或功能组织目录
  * 好：`reference/finance.md`、`reference/sales.md`
  * 坏：`docs/file1.md`、`docs/file2.md`
* **打包完整资源**：包含完整 API 文档、大量示例、大数据集；访问前没有上下文惩罚
* **确定性操作优先用脚本**：编写 `validate_form.py`，而不是让 Claude 生成验证代码
* **明确执行意图**：
  * “Run `analyze_form.py` to extract fields”（执行）
  * “See `analyze_form.py` for the extraction algorithm”（作为参考读取）
* **测试文件访问模式**：用真实请求验证 Claude 能浏览你的目录结构

**示例：**

```
bigquery-skill/
├── SKILL.md (overview, points to reference files)
└── reference/
    ├── finance.md (revenue metrics)
    ├── sales.md (pipeline data)
    └── product.md (usage analytics)
```

当用户询问收入时，Claude 读取 SKILL.md，看到对 `reference/finance.md` 的引用，并调用 bash 只读取该文件。sales.md 和 product.md 仍留在文件系统中，在需要前消耗零上下文 token。这个基于文件系统的模型使渐进式披露成为可能。Claude 可以导航并选择性加载每个任务需要的精确内容。

技术架构完整详情见 Skills 概览中的 [How Skills work](/en/docs/agents-and-tools/agent-skills/overview#how-skills-work)。

### MCP 工具引用

如果你的技能使用 MCP（Model Context Protocol）工具，始终使用完全限定的工具名称，避免 “tool not found” 错误。

**格式**：`ServerName:tool_name`

**示例：**

```markdown  theme={null}
Use the BigQuery:bigquery_schema tool to retrieve table schemas.
Use the GitHub:create_issue tool to create issues.
```

其中：

* `BigQuery` 和 `GitHub` 是 MCP 服务器名称
* `bigquery_schema` 和 `create_issue` 是这些服务器内的工具名称

没有服务器前缀时，Claude 可能无法定位工具，尤其在有多个 MCP 服务器可用时。

### 避免假设工具已安装

不要假设包可用：

````markdown  theme={null}
**Bad example: Assumes installation**:
"Use the pdf library to process the file."

**Good example: Explicit about dependencies**:
"Install required package: `pip install pypdf`

Then use it:
```python
from pypdf import PdfReader
reader = PdfReader("file.pdf")
```"
````

## 技术说明

### YAML frontmatter 要求

SKILL.md frontmatter 需要 `name`（最多 64 个字符）和 `description`（最多 1024 个字符）字段。完整结构详情见 [Skills overview](/en/docs/agents-and-tools/agent-skills/overview#skill-structure)。

### Token 预算

为获得最佳性能，保持 SKILL.md 正文低于 500 行。如果内容超过此限制，使用前文描述的渐进式披露模式拆成单独文件。架构详情见 [Skills overview](/en/docs/agents-and-tools/agent-skills/overview#how-skills-work)。

## 有效技能检查清单

分享技能前，验证：

### 核心质量

* [ ] Description 具体且包含关键词
* [ ] Description 同时包含技能做什么和何时使用
* [ ] SKILL.md 正文低于 500 行
* [ ] 额外细节放在单独文件中（如果需要）
* [ ] 没有时间敏感信息（或放在 “old patterns” 章节）
* [ ] 全文术语一致
* [ ] 示例具体而非抽象
* [ ] 文件引用只有一层深
* [ ] 合理使用渐进式披露
* [ ] 工作流步骤清晰

### 代码和脚本

* [ ] 脚本解决问题，而不是推给 Claude
* [ ] 错误处理明确且有帮助
* [ ] 没有 “voodoo constants”（所有值都有理由）
* [ ] 指令中列出必需包，并验证可用
* [ ] 脚本文档清晰
* [ ] 没有 Windows 风格路径（全部使用正斜杠）
* [ ] 关键操作有验证/核验步骤
* [ ] 质量关键任务包含反馈循环

### 测试

* [ ] 至少创建三个评估
* [ ] 用 Haiku、Sonnet 和 Opus 测试
* [ ] 用真实使用场景测试
* [ ] 纳入团队反馈（如适用）

## 下一步

<CardGroup cols={2}>
  <Card title="Get started with Agent Skills" icon="rocket" href="/en/docs/agents-and-tools/agent-skills/quickstart">
    Create your first Skill
  </Card>

  <Card title="Use Skills in Claude Code" icon="terminal" href="/en/docs/claude-code/skills">
    Create and manage Skills in Claude Code
  </Card>

  <Card title="Use Skills with the API" icon="code" href="/en/api/skills-guide">
    Upload and use Skills programmatically
  </Card>
</CardGroup>
