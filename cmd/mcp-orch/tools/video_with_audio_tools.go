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

// videoWithAudioInput 是一站式视频加配音工具的入参。
type videoWithAudioInput struct {
	Prompt         string `json:"prompt"`
	VoiceText      string `json:"voice_text"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

// videoWithAudioToolDefinitions 注册视频、TTS、ffmpeg 合成工具定义。
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

// handleVideoWithAudio 串联 TTS、视频生成和 ffmpeg 合成。
// 任一步失败都直接返回错误，避免产出只有部分素材的成功响应。
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

		// 先生成 TTS，失败时直接停止，避免继续等待耗时的视频任务。
		audioPath, err := generateTTS(ctx, apiKey, in.VoiceText, "")
		if err != nil {
			return nil, fmt.Errorf("tts: %w", err)
		}

		// 再提交视频生成任务；轮询会响应 ctx 取消，避免工具关闭时长时间阻塞。
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

		// 最后合成音视频，任何 ffmpeg 错误都会带上两个输入路径便于排查。
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

// generateTTS 调用 SiliconFlow TTS 并把音频写入本地文件。
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

// newTTSRequest 构造带鉴权头的 TTS HTTP 请求。
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

// ttsStatusError 读取 TTS 错误响应体并保留状态码。
func ttsStatusError(resp *http.Response) error {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read tts error response: %w", err)
	}
	return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
}

// mediaOutputPath 生成 Movies 目录下的时间戳输出路径。
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

// writeResponseBody 将响应流写入目标文件。
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

// mergeAV 用 ffmpeg 将已下载的视频和 TTS 音频合成到新的 MP4。
// 使用 -shortest 防止任一轨道过长造成尾部黑屏或静音拖尾，ffmpeg 输出原样进入错误信息。
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
