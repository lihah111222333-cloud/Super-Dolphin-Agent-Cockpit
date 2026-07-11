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
	"path"
	"path/filepath"
	"strings"
	"time"

	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	sharedfilefs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilefs"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilepath"
)

// videoWithAudioInput 是一站式视频加配音工具的入参。
type videoWithAudioInput struct {
	Prompt         string `json:"prompt"`
	VoiceText      string `json:"voice_text"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	OutputPath     string `json:"output_path,omitempty"`
}

// videoWithAudioToolDefinitions 注册视频、TTS、ffmpeg 合成工具定义。
func videoWithAudioToolDefinitions() []ToolDefinition {
	return buildToolDefinitions(
		defineGovernedTool(
			"video_with_audio",
			"Generate a short-form video with voiceover in one step. All media files are written through controlled sharedfile or workspace-relative paths.",
			ObjectSchema(map[string]Schema{
				"prompt":          StringSchema("Detailed video scene description (style, color, motion, camera angle, vertical format)."),
				"voice_text":      StringSchema("The narration text to convert to speech and overlay on the video."),
				"negative_prompt": StringSchema("What to avoid in the video (optional)."),
				"output_path":     StringSchema("Optional final output ref: shared:<path> or workspace-relative path. Defaults to shared:reports/media/merged-<timestamp>.mp4."),
			}, "prompt", "voice_text"),
			handleVideoWithAudio(),
			mediaToolMetadata("video_with_audio", nil, []string{"output_path"}),
		),
	)
}

// handleVideoWithAudio 串联 TTS、视频生成和 ffmpeg 合成。
// 任一步失败都直接返回错误，避免产出只有部分素材的成功响应。
func handleVideoWithAudio() ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		in, err := decodeVideoWithAudioInput(input)
		if err != nil {
			return nil, err
		}

		apiKey, err := siliconFlowAPIKey()
		if err != nil {
			return nil, err
		}

		// 先生成 TTS，失败时直接停止，避免继续等待耗时的视频任务。
		audioPath, err := generateTTS(ctx, apiKey, in.VoiceText, "", "")
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
		videoPath, err := downloadVideoToControlledPath(ctx, videoURL, requestID, "")
		if err != nil {
			return nil, fmt.Errorf("video download: %w", err)
		}

		// 最后合成音视频，任何 ffmpeg 错误都会带上两个输入路径便于排查。
		mergedPath, err := mergeAV(ctx, videoPath.AbsPath, audioPath.AbsPath, in.OutputPath)
		if err != nil {
			return nil, fmt.Errorf("merge audio and video (video_path=%q audio_path=%q): %w", videoPath.Ref, audioPath.Ref, err)
		}

		return map[string]any{
			"success":     true,
			"output_path": mergedPath.Ref,
			"local_path":  mergedPath.AbsPath,
			"video_path":  videoPath.Ref,
			"audio_path":  audioPath.Ref,
		}, nil
	}
}

// decodeVideoWithAudioInput 解析视频合成输入并先做 fail-fast 校验。
// 这里提前拦住空 prompt、空旁白和越权输出路径，避免后续远程生成或 ffmpeg 已经产生副作用。
func decodeVideoWithAudioInput(input json.RawMessage) (videoWithAudioInput, error) {
	var in videoWithAudioInput
	if err := json.Unmarshal(input, &in); err != nil {
		return videoWithAudioInput{}, fmt.Errorf("invalid input: %w", err)
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return videoWithAudioInput{}, fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(in.VoiceText) == "" {
		return videoWithAudioInput{}, fmt.Errorf("voice_text is required")
	}
	if err := rejectUncontrolledOutputPath(in.OutputPath, "output_path"); err != nil {
		return videoWithAudioInput{}, err
	}
	return in, nil
}

type controlledMediaPath struct {
	Ref     string
	AbsPath string
}

// mediaToolMetadata 给本地媒体写入工具声明统一 path policy。
func mediaToolMetadata(name string, readFields, writeFields []string) ToolMetadata {
	return ToolMetadata{
		Version:                "media.local.v1",
		OutputSchema:           RawObjectSchema("Media tool response object."),
		Capabilities:           []string{name, "media.local_file"},
		RiskClass:              ToolRiskHigh,
		Permission:             ToolPermissionSharedFileWrite,
		WorkspaceScope:         ToolWorkspaceScopeAllowedRoots,
		TimeoutSeconds:         900,
		IdempotencyRequirement: ToolIdempotencyRecommended,
		ApprovalRequired:       false,
		AuditEventType:         name,
		RedactionPolicy:        ToolRedactionMetadataOnly,
		PathPolicy: ToolPathPolicy{
			PathAuthority: ToolPathAuthoritySharedOrWorkspace,
			ReadFields:    append([]string(nil), readFields...),
			WriteFields:   append([]string(nil), writeFields...),
			Validator:     "sharedfilepath + mcp ToolScope workspace roots",
		},
	}
}

// generateTTS 调用 SiliconFlow TTS 并把音频写入受控路径。
func generateTTS(ctx context.Context, apiKey, text, voice, outputPath string) (controlledMediaPath, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return controlledMediaPath{}, fmt.Errorf("text is required")
	}
	if strings.TrimSpace(voice) == "" {
		voice = sfTTSVoice
	}
	req, err := newTTSRequest(ctx, apiKey, text, voice)
	if err != nil {
		return controlledMediaPath{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return controlledMediaPath{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return controlledMediaPath{}, ttsStatusError(resp)
	}
	dest, err := resolveControlledMediaOutput(ctx, outputPath, "tts", "mp3")
	if err != nil {
		return controlledMediaPath{}, err
	}
	if err := writeResponseBody(dest.AbsPath, resp.Body); err != nil {
		return controlledMediaPath{}, err
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

// mediaOutputPath 是旧 home/Movies 输出路径入口，保留为 fail-fast shim。
// 新代码必须调用 resolveControlledMediaOutput，带 trusted workspace scope 解析路径。
func mediaOutputPath(prefix, ext string) (string, error) {
	_ = prefix
	_ = ext
	return "", fmt.Errorf("controlled media output requires output_path resolved from trusted workspace scope")
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

// mergeAV 用 ffmpeg 将已下载的视频和 TTS 音频合成到受控 MP4 输出。
// 使用 -shortest 防止任一轨道过长造成尾部黑屏或静音拖尾，ffmpeg 输出原样进入错误信息。
func mergeAV(ctx context.Context, videoPath, audioPath, outputPath string) (controlledMediaPath, error) {
	out, err := resolveControlledMediaOutput(ctx, outputPath, "merged", "mp4")
	if err != nil {
		return controlledMediaPath{}, err
	}

	cmd := exec.CommandContext(ctx, ffmpegBin(),
		"-y", "-i", videoPath, "-i", audioPath,
		"-shortest", "-c:v", "copy", "-c:a", "aac", out.AbsPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return controlledMediaPath{}, fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(output)))
	}
	return out, nil
}

func rejectUncontrolledOutputPath(raw, field string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	_, _, err := parseControlledMediaRef(raw, field, true)
	return err
}

func resolveControlledMediaInput(ctx context.Context, raw, field string) (string, error) {
	authority, cleaned, err := parseControlledMediaRef(raw, field, false)
	if err != nil {
		return "", err
	}
	if authority == ToolPathAuthoritySharedFile {
		return resolveSharedMediaAbs(ctx, cleaned, false)
	}
	return resolveWorkspaceMediaAbs(ctx, cleaned)
}

// resolveControlledMediaOutput 把可选输出 ref 解析成可写路径。
// 空值只落到 sharedfile 默认目录，显式值必须是 shared: ref 或工作区相对路径。
func resolveControlledMediaOutput(ctx context.Context, raw, prefix, ext string) (controlledMediaPath, error) {
	if strings.TrimSpace(raw) == "" {
		raw = defaultSharedMediaOutput(prefix, ext)
	}
	authority, cleaned, err := parseControlledMediaRef(raw, "output_path", true)
	if err != nil {
		return controlledMediaPath{}, err
	}
	ref := cleaned
	var abs string
	if authority == ToolPathAuthoritySharedFile {
		ref = "shared:" + cleaned
		abs, err = resolveSharedMediaAbs(ctx, cleaned, true)
	} else {
		abs, err = resolveWorkspaceMediaAbs(ctx, cleaned)
	}
	if err != nil {
		return controlledMediaPath{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return controlledMediaPath{}, fmt.Errorf("create media output dir %q: %w", filepath.Dir(abs), err)
	}
	return controlledMediaPath{Ref: ref, AbsPath: abs}, nil
}

// parseControlledMediaRef 校验媒体工具传入的受控 ref 并返回权限类型。
// sharedfile 路径复用 sharedfilepath 规则，工作区路径只接受相对路径并拒绝 home/absolute/traversal。
func parseControlledMediaRef(raw, field string, write bool) (ToolPathAuthority, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ToolPathAuthorityNone, "", fmt.Errorf("%s is required", field)
	}
	if sharedPath, ok := strings.CutPrefix(trimmed, "shared:"); ok {
		var (
			cleaned string
			err     error
		)
		if write {
			cleaned, err = sharedfilepath.ValidateAgentWritePath(sharedPath)
		} else {
			cleaned, err = sharedfilepath.ValidateReadPath(sharedPath)
		}
		if err != nil {
			return ToolPathAuthorityNone, "", fmt.Errorf("%s sharedfile path invalid: %w", field, err)
		}
		return ToolPathAuthoritySharedFile, cleaned, nil
	}
	cleaned, err := cleanWorkspaceRelativePath(trimmed)
	if err != nil {
		return ToolPathAuthorityNone, "", fmt.Errorf("%s workspace-relative path invalid: %w", field, err)
	}
	return ToolPathAuthorityWorkspaceRelative, cleaned, nil
}

func resolveSharedMediaAbs(ctx context.Context, cleaned string, write bool) (string, error) {
	root, err := mcpcommon.WorkspaceRootFromContextStrict(ctx)
	if err != nil {
		return "", err
	}
	cfg := sharedfilefs.Config{CWD: root}
	if write {
		return cfg.ResolveWriteAbs(cleaned)
	}
	return cfg.ResolveReadAbs(cleaned)
}

func resolveWorkspaceMediaAbs(ctx context.Context, cleaned string) (string, error) {
	root, err := mcpcommon.WorkspaceRootForPathFromContextStrict(ctx, "")
	if err != nil {
		return "", err
	}
	abs := filepath.Join(root, filepath.FromSlash(cleaned))
	if !platformshared.ContainsPath(root, abs) {
		return "", fmt.Errorf("workspace path %q escapes trusted root %q", cleaned, root)
	}
	return abs, nil
}

func defaultSharedMediaOutput(prefix, ext string) string {
	name := strings.TrimSpace(prefix)
	if name == "" {
		name = "media"
	}
	suffix := strings.TrimPrefix(strings.TrimSpace(ext), ".")
	if suffix == "" {
		suffix = "bin"
	}
	fileName := name + "-" + time.Now().Format("20060102-150405") + "." + suffix
	return "shared:" + path.Join("reports", "media", fileName)
}
