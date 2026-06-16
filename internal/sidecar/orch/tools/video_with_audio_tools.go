package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type videoWithAudioInput struct {
	Prompt         string `json:"prompt"`
	VoiceText      string `json:"voice_text"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

func videoWithAudioToolDefinitions() []ToolDefinition {
	return buildToolDefinitions(
		defineTool(
			"video_with_audio",
			"Generate a short-form video with voiceover in one step: generates TTS audio from voice_text, generates video from prompt, then merges them. Returns the merged MP4 path. Requires SILICONFLOW_API_KEY and ffmpeg.",
			ObjectSchema(map[string]Schema{
				"prompt":          StringSchema("Detailed video scene description (style, color, motion, camera angle, vertical format)."),
				"voice_text":      StringSchema("The narration text to convert to speech and overlay on the video."),
				"negative_prompt": StringSchema("What to avoid in the video (optional)."),
			}, "prompt", "voice_text"),
			handleVideoWithAudio(),
		),
	)
}

// handleVideoWithAudio 处理带audio的video。
func handleVideoWithAudio() ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in videoWithAudioInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
		if strings.TrimSpace(in.Prompt) == "" {
			return nil, fmt.Errorf("prompt is required")
		}
		if strings.TrimSpace(in.VoiceText) == "" {
			return nil, fmt.Errorf("voice_text is required")
		}

		apiKey, err := siliconFlowAPIKey()
		if err != nil {
			return nil, err
		}

		// Step 1: TTS (fast, do first)
		audioPath, err := generateTTS(ctx, apiKey, in.VoiceText, "")
		if err != nil {
			return nil, fmt.Errorf("tts: %w", err)
		}

		// Step 2: Video generation (slow, ~10 min)
		videoIn := videoGenerateInput{Prompt: in.Prompt, NegativePrompt: in.NegativePrompt}
		requestID, err := sfSubmit(ctx, apiKey, videoIn)
		if err != nil {
			return nil, fmt.Errorf("video submit: %w", err)
		}
		videoURL, err := sfPoll(ctx, apiKey, requestID)
		if err != nil {
			return nil, fmt.Errorf("video poll: %w", err)
		}
		videoPath, err := downloadVideoToDesktop(ctx, videoURL, requestID)
		if err != nil {
			return nil, fmt.Errorf("video download: %w", err)
		}

		// Step 3: Merge
		mergedPath, err := mergeAV(ctx, videoPath, audioPath)
		if err != nil {
			return nil, fmt.Errorf("merge audio and video (video_path=%q audio_path=%q): %w", videoPath, audioPath, err)
		}

		return map[string]any{
			"success":     true,
			"output_path": mergedPath,
			"video_path":  videoPath,
			"audio_path":  audioPath,
		}, nil
	}
}

// generateTTS 处理generatetts。
func generateTTS(ctx context.Context, apiKey, text, voice string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	if strings.TrimSpace(voice) == "" {
		voice = sfTTSVoice
	}
	req, err := newTTSRequest(ctx, apiKey, text, voice)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ttsStatusError(resp)
	}
	dest, err := mediaOutputPath("tts", "mp3")
	if err != nil {
		return "", err
	}
	if err := writeResponseBody(dest, resp.Body); err != nil {
		return "", err
	}
	return dest, nil
}

func newTTSRequest(ctx context.Context, apiKey, text, voice string) (*http.Request, error) {
	body, err := json.Marshal(map[string]any{
		"model": sfTTSModel,
		"input": text,
		"voice": voice,
	})
	if err != nil {
		return nil, fmt.Errorf("encode tts request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sfTTSURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func ttsStatusError(resp *http.Response) error {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read tts error response: %w", err)
	}
	return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
}

func mediaOutputPath(prefix, ext string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Movies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir %q: %w", dir, err)
	}
	return filepath.Join(dir, prefix+"-"+time.Now().Format("20060102-150405")+"."+ext), nil
}

func writeResponseBody(dest string, body io.Reader) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, body); err != nil {
		return err
	}
	return nil
}

func mergeAV(ctx context.Context, videoPath, audioPath string) (string, error) {
	out, err := mediaOutputPath("merged", "mp4")
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, ffmpegBin(),
		"-y", "-i", videoPath, "-i", audioPath,
		"-shortest", "-c:v", "copy", "-c:a", "aac", out,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return out, nil
}
