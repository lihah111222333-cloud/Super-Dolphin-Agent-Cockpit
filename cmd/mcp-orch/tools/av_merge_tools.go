package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// avMergeInput 是 av_merge 工具的入参。
type avMergeInput struct {
	VideoPath  string `json:"video_path"`
	AudioPath  string `json:"audio_path"`
	OutputPath string `json:"output_path,omitempty"`
}

// avMergeToolDefinitions 注册 av_merge 工具；该工具直接调用本机 ffmpeg，失败时回传命令输出。
func avMergeToolDefinitions() []ToolDefinition {
	return buildToolDefinitions(
		defineTool(
			"av_merge",
			"Merge a video file and an audio file into a single MP4 using ffmpeg. The audio replaces or overlays the video's original audio. Returns the output file path. Requires ffmpeg installed or FFMPEG_PATH env var pointing to the binary.",
			ObjectSchema(map[string]Schema{
				"video_path":  StringSchema("Absolute path to the input video file."),
				"audio_path":  StringSchema("Absolute path to the input audio file."),
				"output_path": StringSchema("Absolute path for the output file (optional). Defaults to ~/Movies/merged-<timestamp>.mp4."),
			}, "video_path", "audio_path"),
			handleAVMerge(),
		),
	)
}

// handleAVMerge 校验输入路径并生成合并后的 MPEG-4 文件；未显式传 output_path 时写入用户 Movies 目录。
func handleAVMerge() ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in avMergeInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
		if strings.TrimSpace(in.VideoPath) == "" {
			return nil, fmt.Errorf("video_path is required")
		}
		if strings.TrimSpace(in.AudioPath) == "" {
			return nil, fmt.Errorf("audio_path is required")
		}

		ffmpeg := ffmpegBin()

		out := strings.TrimSpace(in.OutputPath)
		if out == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			dir := filepath.Join(home, "Movies")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create output dir %q: %w", dir, err)
			}
			out = filepath.Join(dir, "merged-"+time.Now().Format("20060102-150405")+".mp4")
		}

		// 输出文件允许覆盖，视频轨直接 copy，音频按最短流截断后转 AAC。
		cmd := exec.CommandContext(ctx, ffmpeg,
			"-y",
			"-i", in.VideoPath,
			"-i", in.AudioPath,
			"-shortest",
			"-c:v", "copy",
			"-c:a", "aac",
			out,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg failed: %w\n%s", err, strings.TrimSpace(string(output)))
		}

		return map[string]any{"success": true, "output_path": out}, nil
	}
}

// ffmpegBin 返回 ffmpeg 可执行文件路径，优先读取 FFMPEG_PATH 环境变量。
func ffmpegBin() string {
	if p := strings.TrimSpace(os.Getenv("FFMPEG_PATH")); p != "" {
		return p
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	return "ffmpeg"
}
