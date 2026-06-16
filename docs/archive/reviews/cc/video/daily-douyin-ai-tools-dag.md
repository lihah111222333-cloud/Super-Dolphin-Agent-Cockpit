# 每日 AI 工具抖音成片生成 DAG

## 目标

每天固定生成 3 条 AI 工具方向的抖音成片，用于后续人工检查、发布或二次剪辑。

> 说明：这里的“爆款”按高概率爆款结构设计，不能保证实际播放量必然爆。

## 基础配置

| 项目 | 配置 |
|---|---|
| 运行时间 | 每天 17:40 |
| 时区 | Asia/Shanghai |
| Cron | `40 17 * * *` |
| 内容方向 | AI 工具 |
| 每日产出 | 3 条成片 |
| 输出目录 | `docs/cc/video` |
| 默认发布方式 | 只生成成片，不自动发布 |

## 自动化任务文件

已拆出两份可注册用文件：

- DAG 自动化任务定义：`docs/cc/video/daily-douyin-ai-tools-task.yaml`
- 节点 Prompt 模板：`docs/cc/video/douyin-ai-tools-prompt-templates.md`

注册到真实运行时前，必须绑定：

- `agent_key`：实际执行 agent 任务的 agent 标识。
- `video_render_command_ref`：实际合成 `final.mp4` 的视频渲染命令引用。

## 关于 `command_ref`

`command_ref` 指的是实际执行视频渲染命令的引用名。

例如 DAG 前面的节点负责生成脚本、分镜、素材、配音，真正把这些内容合成为 `final.mp4` 时，需要调用一个视频渲染程序。这个程序可以是：

- 本地脚本，例如 `scripts/render_video.sh`
- Python 渲染程序，例如 MoviePy、FFmpeg wrapper
- 外部视频生成服务
- 内部 MCP 工具或 agent 命令

如果当前项目里还没有现成的视频渲染命令，就需要新增一个渲染节点实现；如果已经有，就把 `command_ref` 指向已有命令即可。

## 每日产出结构

```text
docs/cc/video/
  daily-douyin-ai-tools-dag.md
  YYYY-MM-DD/
    video_1/
      script.md
      storyboard.md
      title_options.md
      hashtags.md
      cover.png
      final.mp4
      qa_report.md
    video_2/
      script.md
      storyboard.md
      title_options.md
      hashtags.md
      cover.png
      final.mp4
      qa_report.md
    video_3/
      script.md
      storyboard.md
      title_options.md
      hashtags.md
      cover.png
      final.mp4
      qa_report.md
    daily_summary.md
```

## DAG 总览

```text
trend_collect
  ↓
topic_generate
  ↓
topic_rank
  ↓
并行生成 3 条视频
  ├─ video_1_pipeline
  ├─ video_2_pipeline
  └─ video_3_pipeline
  ↓
daily_summary
  ↓
feedback_update
```

## 节点设计

| 节点 | 作用 | 输入 | 输出 |
|---|---|---|---|
| `trend_collect` | 采集 AI 工具热点、热门用法、近期爆款结构 | AI 工具关键词、账号定位 | `trend_report.md` |
| `topic_generate` | 生成 20-30 个 AI 工具候选选题 | 趋势报告、历史表现 | `topic_pool.json` |
| `topic_rank` | 按爆款潜力排序并选择 3 个主题 | 选题池 | `selected_topics.json` |
| `video_1_pipeline` | 生成第 1 条成片 | 选题 1 | `video_1/final.mp4` |
| `video_2_pipeline` | 生成第 2 条成片 | 选题 2 | `video_2/final.mp4` |
| `video_3_pipeline` | 生成第 3 条成片 | 选题 3 | `video_3/final.mp4` |
| `daily_summary` | 汇总 3 条视频的标题、标签、质检结果 | 三条视频结果 | `daily_summary.md` |
| `feedback_update` | 写入内容记忆，用于后续优化选题和结构 | 每日汇总 | `content_memory.json` |

## 单条视频子 DAG

```text
script_generate
  ↓
storyboard
  ↓
asset_prepare
  ↓
voiceover
  ↓
edit_video
  ↓
cover_title
  ↓
qa
```

| 节点 | 作用 | 输出 |
|---|---|---|
| `script_generate` | 生成 AI 工具短视频脚本 | `script.md` |
| `storyboard` | 拆分镜头、字幕、节奏点 | `storyboard.md` |
| `asset_prepare` | 准备工具截图、演示素材、B-roll、封面素材 | `assets_manifest.json` |
| `voiceover` | 生成配音 | `voice.mp3` |
| `edit_video` | 合成画面、配音、字幕、转场、音效 | `final.mp4` |
| `cover_title` | 生成封面、标题和标签 | `cover.png`、`title_options.md`、`hashtags.md` |
| `qa` | 检查时长、字幕、违禁词、版权风险、节奏 | `qa_report.md` |

## 选题策略

每天生成 3 种不同类型，避免同质化：

1. **效率提升型**：一个 AI 工具如何节省时间、降低成本。
2. **避坑测评型**：某个 AI 工具真实体验、限制、坑点。
3. **场景教程型**：用 AI 工具完成一个具体任务，例如写方案、做 PPT、剪视频、整理资料。

## 选题评分

候选选题按 100 分评分：

```text
爆款潜力 = 热度 30%
        + 实用价值 25%
        + 情绪/反差 20%
        + 可视化程度 15%
        + 账号匹配度 10%
```

低于 75 分的选题不进入成片生成。

## 视频结构

建议每条视频控制在 20-45 秒：

```text
0-3 秒：强钩子，例如“这个 AI 工具让我 10 分钟做完 2 小时的活”
3-8 秒：展示问题或痛点
8-25 秒：演示工具核心操作
25-35 秒：展示结果或对比
最后 3 秒：引导评论、收藏或关注
```

## DAG 配置草案

```yaml
name: daily_douyin_ai_tools_video_dag
timezone: Asia/Shanghai
schedule: "40 17 * * *"

inputs:
  niche: "AI 工具"
  video_count: 3
  output_root: "docs/cc/video"
  publish_mode: "generate_only"
  duration_seconds:
    min: 20
    max: 45

nodes:
  - id: trend_collect
    type: agent_task
    prompt_template: douyin_ai_tools_trend_collect
    output: "${output_root}/${date}/trend_report.md"

  - id: topic_generate
    type: agent_task
    depends_on:
      - trend_collect
    prompt_template: douyin_ai_tools_topic_generate
    output: "${output_root}/${date}/topic_pool.json"

  - id: topic_rank
    type: agent_task
    depends_on:
      - topic_generate
    prompt_template: douyin_ai_tools_topic_rank
    output: "${output_root}/${date}/selected_topics.json"

  - id: video_1_pipeline
    type: subdag
    depends_on:
      - topic_rank
    input_selector: "selected_topics[0]"
    output_root: "${output_root}/${date}/video_1"

  - id: video_2_pipeline
    type: subdag
    depends_on:
      - topic_rank
    input_selector: "selected_topics[1]"
    output_root: "${output_root}/${date}/video_2"

  - id: video_3_pipeline
    type: subdag
    depends_on:
      - topic_rank
    input_selector: "selected_topics[2]"
    output_root: "${output_root}/${date}/video_3"

  - id: daily_summary
    type: agent_task
    depends_on:
      - video_1_pipeline
      - video_2_pipeline
      - video_3_pipeline
    prompt_template: douyin_ai_tools_daily_summary
    output: "${output_root}/${date}/daily_summary.md"

  - id: feedback_update
    type: agent_task
    depends_on:
      - daily_summary
    prompt_template: douyin_ai_tools_feedback_update
    output: "${output_root}/content_memory.json"
```

## Fail-Fast 规则

以下情况必须直接失败，不允许静默兜底：

- 趋势采集结果为空。
- 候选选题少于 3 个。
- 3 个入选选题中任一低于 75 分。
- 脚本缺少 3 秒强钩子。
- 视频时长低于 20 秒或超过 45 秒。
- `final.mp4` 未生成。
- 字幕缺失或明显不同步。
- 检测到侵权素材、敏感词或违规表达。

## 人工确认点

当前 DAG 默认只生成成片，不自动发布。发布前建议人工确认：

- 标题是否夸大。
- AI 工具演示是否真实可复现。
- 画面是否包含隐私信息、账号信息或未授权素材。
- 封面是否清晰表达卖点。
- 标签是否与内容一致。
