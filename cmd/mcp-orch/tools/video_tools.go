package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
)

// SiliconFlow 视频生成默认配置。
const (
	sfSubmitURL = "https://api.siliconflow.cn/v1/video/submit"
	sfStatusURL = "https://api.siliconflow.cn/v1/video/status"
	sfModel     = "Wan-AI/Wan2.2-T2V-A14B"
	sfImageSize = "720x1280"
)

// videoGenerateInput 是视频生成工具的入参。
type videoGenerateInput struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

// siliconFlowAPIKey 从环境或视频设置文件读取 API key。
// 缺 key 时直接报错，避免外部 API 调用用空凭据重试。
func siliconFlowAPIKey() (string, error) {
	apiKey := strings.TrimSpace(os.Getenv("SILICONFLOW_API_KEY"))
	if apiKey != "" {
		return apiKey, nil
	}
	if err := runtimeenv.LoadVideoEnv(); err != nil {
		return "", fmt.Errorf("load video env: %w", err)
	}
	apiKey = strings.TrimSpace(os.Getenv("SILICONFLOW_API_KEY"))
	if apiKey == "" {
		return "", errors.New("SILICONFLOW_API_KEY is required; set it in Settings -> Video")
	}
	return apiKey, nil
}

// sfPost 向 SiliconFlow 发送 JSON POST 请求并返回响应体。
// 非 2xx 响应会带上服务端文本，便于用户判断额度、鉴权或参数问题。
func sfPost(ctx context.Context, apiKey, url string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("siliconflow status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// sfSubmit 提交视频生成任务并返回 requestID。
func sfSubmit(ctx context.Context, apiKey string, in videoGenerateInput) (string, error) {
	payload := map[string]any{"model": sfModel, "prompt": strings.TrimSpace(in.Prompt), "image_size": sfImageSize}
	if strings.TrimSpace(in.NegativePrompt) != "" {
		payload["negative_prompt"] = strings.TrimSpace(in.NegativePrompt)
	}
	data, err := sfPost(ctx, apiKey, sfSubmitURL, payload)
	if err != nil {
		return "", fmt.Errorf("siliconflow submit: %w", err)
	}
	var result struct {
		RequestID string `json:"requestId"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("siliconflow submit parse: %w", err)
	}
	if result.RequestID == "" {
		return "", fmt.Errorf("siliconflow submit failed: %s", result.Message)
	}
	return result.RequestID, nil
}

// sfPoll 轮询视频生成结果，最多等待 15 分钟。
// 上下文取消会立即返回，避免长轮询阻塞工具关闭。
func sfPoll(ctx context.Context, apiKey, requestID string) (string, error) {
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(15 * time.Second):
		}
		data, err := sfPost(ctx, apiKey, sfStatusURL, map[string]string{"requestId": requestID})
		if err != nil {
			return "", fmt.Errorf("siliconflow status: %w", err)
		}
		var result struct {
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Results struct {
				Videos []struct {
					URL string `json:"url"`
				} `json:"videos"`
			} `json:"results"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return "", fmt.Errorf("siliconflow status parse: %w", err)
		}
		switch result.Status {
		case "Succeed":
			if len(result.Results.Videos) == 0 {
				return "", errors.New("siliconflow: no video URL in result")
			}
			return result.Results.Videos[0].URL, nil
		case "Failed":
			return "", fmt.Errorf("siliconflow: video generation failed: %s", result.Reason)
		}
	}
	return "", fmt.Errorf("siliconflow: request %s timed out", requestID)
}

// downloadVideoToDesktop 是旧 home/Movies 下载入口，保留为 fail-fast shim。
// 新代码必须调用 downloadVideoToControlledPath，避免 Movies 失败时退回 home。
func downloadVideoToDesktop(ctx context.Context, videoURL, requestID string) (string, error) {
	_ = ctx
	_ = videoURL
	_ = requestID
	return "", fmt.Errorf("controlled video output_path is required; home/Movies fallback is disabled")
}

// downloadVideoToControlledPath 将生成的视频下载到 sharedfile 或 workspace-relative 受控路径。
func downloadVideoToControlledPath(ctx context.Context, videoURL, requestID, outputPath string) (controlledMediaPath, error) {
	dest, err := resolveControlledMediaOutput(ctx, outputPath, "video-"+strings.TrimSpace(requestID), "mp4")
	if err != nil {
		return controlledMediaPath{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return controlledMediaPath{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return controlledMediaPath{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return controlledMediaPath{}, fmt.Errorf("download status %d", resp.StatusCode)
	}
	if err := writeResponseBody(dest.AbsPath, resp.Body); err != nil {
		return controlledMediaPath{}, err
	}
	return dest, nil
}
