---
name: "video-generate"
display_name: "video-generate"
description: "用户要求生成视频、制作短视频、生成抖音视频、生成视频内容时。"
---

# 视频生成技能（含自动配音）

## 触发时机
用户要求生成视频、制作短视频、生成抖音视频、生成视频内容时。

## 执行规则

**必须调用 `video_with_audio` MCP tool**，不得使用其他方式。

## 执行流程

1. 根据用户需求，用中文写：
   - `prompt`：详细的视频画面描述（场景、风格、色彩、人物动作、镜头语言，竖版构图，抖音爆款风格）
   - `voice_text`：配音文案（**不超过15个字**，对应视频3-4秒时长，简短有力；如果用户没有指定旁白，必须主动生成一句）

2. 调用 `video_with_audio` tool，传入 `prompt` 和 `voice_text`

3. 等待 tool 返回（约 10 分钟），将 `output_path` 返回给用户

## 示例

```json
{
  "prompt": "阳光明媚的公园里，一个AI美女穿着白色连衣裙，赛博朋克风格，霓虹灯背景，特写镜头，竖版构图，慢动作",
  "voice_text": "每天都是新的开始"
}
```

## 禁止事项
- 禁止传 `voice` 参数，必须省略让工具使用默认值
- 禁止调用 `video_generate`
- 禁止分开调用 `tts_generate`、`av_merge`
- 禁止访问任何外部网站
