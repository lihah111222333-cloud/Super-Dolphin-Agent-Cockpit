package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	sfTTSURL   = "https://api.siliconflow.cn/v1/audio/speech"
	sfTTSModel = "FunAudioLLM/CosyVoice2-0.5B"
	sfTTSVoice = "FunAudioLLM/CosyVoice2-0.5B:anna"
)

type ttsInput struct {
	Text  string `json:"text"`
	Voice string `json:"voice,omitempty"`
}

func ttsToolDefinitions() []ToolDefinition {
	return buildToolDefinitions(
		defineTool(
			"tts_generate",
			"Convert text to speech using SiliconFlow CosyVoice2. Returns the local path of the generated MP3 file. Requires SILICONFLOW_API_KEY environment variable.",
			ObjectSchema(map[string]Schema{
				"text":  StringSchema("The text to convert to speech."),
				"voice": StringSchema("Voice ID to use (optional). Defaults to anna."),
			}, "text"),
			handleTTSGenerate(),
		),
	)
}

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
		audioPath, err := generateTTS(ctx, apiKey, in.Text, in.Voice)
		if err != nil {
			return nil, fmt.Errorf("generate tts: %w", err)
		}
		return map[string]any{"success": true, "local_path": audioPath}, nil
	}
}

func decodeTTSInput(input json.RawMessage) (ttsInput, error) {
	var in ttsInput
	if err := json.Unmarshal(input, &in); err != nil {
		return ttsInput{}, fmt.Errorf("invalid input: %w", err)
	}
	in.Text = strings.TrimSpace(in.Text)
	in.Voice = strings.TrimSpace(in.Voice)
	if in.Text == "" {
		return ttsInput{}, fmt.Errorf("text is required")
	}
	return in, nil
}
