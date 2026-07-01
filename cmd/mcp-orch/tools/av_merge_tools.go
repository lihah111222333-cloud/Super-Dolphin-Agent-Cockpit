package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// avMergeInput 是 av_merge 工具的入参。
type avMergeInput struct {
	VideoRef   string `json:"video_ref"`
	AudioRef   string `json:"audio_ref"`
	OutputPath string `json:"output_path,omitempty"`
}

// avMergeToolDefinitions 注册 av_merge 工具；该工具直接调用本机 ffmpeg，失败时回传命令输出。
func avMergeToolDefinitions() []ToolDefinition {
	return buildToolDefinitions(
		defineGovernedTool(
			"av_merge",
			"Merge a controlled video_ref and audio_ref into a single MP4 using ffmpeg. Refs must be shared:<path> or workspace-relative paths; output_path is optional but must use the same controlled path policy.",
			ObjectSchema(map[string]Schema{
				"video_ref":   StringSchema("Input video ref: shared:<path> or workspace-relative path."),
				"audio_ref":   StringSchema("Input audio ref: shared:<path> or workspace-relative path."),
				"output_path": StringSchema("Optional output ref: shared:<path> or workspace-relative path. Defaults to shared:reports/media/merged-<timestamp>.mp4."),
			}, "video_ref", "audio_ref"),
			handleAVMerge(),
			mediaToolMetadata("av_merge", []string{"video_ref", "audio_ref"}, []string{"output_path"}),
		),
	)
}

// handleAVMerge 校验受控输入引用并生成合并后的 MPEG-4 文件。
// 所有本地路径都必须从 trusted workspace scope 或 sharedfile sandbox 解析，避免任意 absolute path 覆盖。
func handleAVMerge() ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in avMergeInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}
		if err := rejectUncontrolledOutputPath(in.OutputPath, "output_path"); err != nil {
			return nil, err
		}
		videoPath, err := resolveControlledMediaInput(ctx, in.VideoRef, "video_ref")
		if err != nil {
			return nil, err
		}
		audioPath, err := resolveControlledMediaInput(ctx, in.AudioRef, "audio_ref")
		if err != nil {
			return nil, err
		}
		output, err := resolveControlledMediaOutput(ctx, in.OutputPath, "merged", "mp4")
		if err != nil {
			return nil, err
		}

		// 输出文件允许覆盖，视频轨直接 copy，音频按最短流截断后转 AAC。
		cmd := exec.CommandContext(ctx, ffmpegBin(),
			"-y",
			"-i", videoPath,
			"-i", audioPath,
			"-shortest",
			"-c:v", "copy",
			"-c:a", "aac",
			output.AbsPath,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg failed: %w\n%s", err, strings.TrimSpace(string(output)))
		}

		return map[string]any{"success": true, "output_path": output.Ref, "local_path": output.AbsPath}, nil
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
