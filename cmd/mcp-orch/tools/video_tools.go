package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
)

const (
	sfSubmitURL = "https://api.siliconflow.cn/v1/video/submit"
	sfStatusURL = "https://api.siliconflow.cn/v1/video/status"
	sfModel     = "Wan-AI/Wan2.2-T2V-A14B"
	sfImageSize = "720x1280"
)

type videoGenerateInput struct {
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
}

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

// sfPoll polls until Succeed/Failed (max 15 minutes).
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

func downloadVideoToDesktop(ctx context.Context, videoURL, requestID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	filename := "video-" + requestID + "-" + time.Now().Format("20060102-150405") + ".mp4"
	moviesDir := filepath.Join(home, "Movies")
	if err := os.MkdirAll(moviesDir, 0o755); err != nil {
		moviesDir = home
	}
	dest := filepath.Join(moviesDir, filename)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download status %d", resp.StatusCode)
	}

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
