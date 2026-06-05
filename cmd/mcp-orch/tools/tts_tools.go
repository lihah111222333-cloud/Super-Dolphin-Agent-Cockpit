package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		var in ttsInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
		if strings.TrimSpace(in.Text) == "" {
			return nil, fmt.Errorf("text is required")
		}
		voice := sfTTSVoice
		if strings.TrimSpace(in.Voice) != "" {
			voice = strings.TrimSpace(in.Voice)
		}

		body, _ := json.Marshal(map[string]any{
			"model": sfTTSModel,
			"input": strings.TrimSpace(in.Text),
			"voice": voice,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, sfTTSURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("siliconflow tts status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(home, "Movies")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			dir = home
		}
		filename := "tts-" + time.Now().Format("20060102-150405") + ".mp3"
		dest := filepath.Join(dir, filename)

		f, err := os.Create(dest)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err := io.Copy(f, resp.Body); err != nil {
			return nil, err
		}

		return map[string]any{"success": true, "local_path": dest}, nil
	}
}
