package supportutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type codexConfigTOML struct {
	ModelProvider string `toml:"model_provider"`
}

// ResolveCodexModelProvider 按 Codex 启动优先级解析 provider。
// 显式 model_provider 优先；本地 openai 默认场景会读取 Codex CLI 的 config.toml，
// 缺少 config.toml 表示未配置，其他读取或解析错误会返回给启动链路。
func ResolveCodexModelProvider(config map[string]any, home, fallback, localProvider string, reserved ...string) (string, error) {
	if provider := FirstConfigString(config, contract.CodexModelProviderKey, "modelProvider", "model_provider"); provider != "" && !isReservedCodexProvider(provider, reserved) {
		return provider, nil
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == localProvider {
		provider, err := CodexConfigModelProvider(home)
		if err != nil {
			return "", err
		}
		if provider != "" {
			return provider, nil
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return localProvider, nil
}

// CodexConfigModelProvider 读取 Codex CLI config.toml 的顶层 model_provider。
func CodexConfigModelProvider(home string) (string, error) {
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(home), "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Codex config.toml: %w", err)
	}
	var cfg codexConfigTOML
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return "", fmt.Errorf("decode Codex config.toml model_provider: %w", err)
	}
	return strings.TrimSpace(cfg.ModelProvider), nil
}

func isReservedCodexProvider(provider string, reserved []string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, value := range reserved {
		if provider == strings.ToLower(strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}
