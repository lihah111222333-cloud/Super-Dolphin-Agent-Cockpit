package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SiliconFlow TTS 默认配置。
const (
	sfTTSURL   = "https://api.siliconflow.cn/v1/audio/speech"
	sfTTSModel = "FunAudioLLM/CosyVoice2-0.5B"
	sfTTSVoice = "FunAudioLLM/CosyVoice2-0.5B:anna"
)

// ttsInput 是 tts_generate 的入参。
type ttsInput struct {
	Text       string `json:"text"`
	Voice      string `json:"voice,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
}

// ttsToolDefinitions 注册文本转语音工具定义。
// 工具 schema 要求 text，实际鉴权和本地文件写入失败会在 handler 中 fail-fast。
func ttsToolDefinitions() []ToolDefinition {
	return buildToolDefinitions(
		defineGovernedTool(
			"tts_generate",
			"Convert text to speech using SiliconFlow CosyVoice2. Returns a controlled MP3 output path. output_path must be shared:<path> or workspace-relative when provided.",
			ObjectSchema(map[string]Schema{
				"text":        StringSchema("The text to convert to speech."),
				"voice":       StringSchema("Voice ID to use (optional). Defaults to anna."),
				"output_path": StringSchema("Optional output ref: shared:<path> or workspace-relative path. Defaults to shared:reports/media/tts-<timestamp>.mp3."),
			}, "text"),
			handleTTSGenerate(),
			mediaToolMetadata("tts_generate", nil, []string{"output_path"}),
		),
	)
}

// handleTTSGenerate 调用 SiliconFlow TTS 并返回本地 MP3 路径。
// 缺少 API key、空文本或远端非 2xx 都直接返回错误，避免报告不存在的音频文件。
func handleTTSGenerate() ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		apiKey, err := siliconFlowAPIKey()
		if err != nil {
			return nil, err
		}
		in, err := decodeTTSInput(input)
		if err != nil {
			return nil, err
		}
		audioPath, err := generateTTS(ctx, apiKey, in.Text, in.Voice, in.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("generate tts: %w", err)
		}
		return map[string]any{"success": true, "output_path": audioPath.Ref, "local_path": audioPath.AbsPath}, nil
	}
}

// decodeTTSInput 解码并裁剪 TTS 入参。
// text 是唯一必填字段，voice 为空时交给底层生成函数使用默认音色。
func decodeTTSInput(input json.RawMessage) (ttsInput, error) {
	var in ttsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return ttsInput{}, fmt.Errorf("invalid input: %w", err)
	}
	in.Text = strings.TrimSpace(in.Text)
	in.Voice = strings.TrimSpace(in.Voice)
	in.OutputPath = strings.TrimSpace(in.OutputPath)
	if in.Text == "" {
		return ttsInput{}, fmt.Errorf("text is required")
	}
	if err := rejectUncontrolledOutputPath(in.OutputPath, "output_path"); err != nil {
		return ttsInput{}, err
	}
	return in, nil
}
