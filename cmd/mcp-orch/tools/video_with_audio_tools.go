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
			// ffmpeg failure is non-fatal: return both files
			return map[string]any{
				"success":    false,
				"video_path": videoPath,
				"audio_path": audioPath,
				"error":      fmt.Sprintf("merge failed (ffmpeg): %v", err),
			}, nil
		}

		return map[string]any{
			"success":     true,
			"output_path": mergedPath,
			"video_path":  videoPath,
			"audio_path":  audioPath,
		}, nil
	}
}

func generateTTS(ctx context.Context, apiKey, text, voice string) (string, error) {
	if strings.TrimSpace(voice) == "" {
		voice = sfTTSVoice
	}
	body, _ := json.Marshal(map[string]any{
		"model": sfTTSModel,
		"input": strings.TrimSpace(text),
		"voice": voice,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sfTTSURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Movies")
	_ = os.MkdirAll(dir, 0o755)
	dest := filepath.Join(dir, "tts-"+time.Now().Format("20060102-150405")+".mp3")

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return dest, nil
}

func mergeAV(ctx context.Context, videoPath, audioPath string) (string, error) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Movies")
	out := filepath.Join(dir, "merged-"+time.Now().Format("20060102-150405")+".mp4")

	cmd := exec.CommandContext(ctx, ffmpegBin(),
		"-y", "-i", videoPath, "-i", audioPath,
		"-shortest", "-c:v", "copy", "-c:a", "aac", out,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return out, nil
}
