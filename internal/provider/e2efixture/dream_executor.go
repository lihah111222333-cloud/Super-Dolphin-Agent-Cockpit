package e2efixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	ProviderName     = "e2e-fixture"
	FixturePathEnv   = "PROMPT_INTENT_E2E_DREAM_FIXTURE"
	pathHashMaxChars = 16
	runIDPlaceholder = "{{RUN_ID}}"
)

var requiredFixtureKeys = []string{"health", "expert", "recall", "default_rule", "review", "block"}

type dreamExecutor struct {
	pathHash string
	cards    map[string]json.RawMessage
}

// newDreamExecutor 读取并校验 prompt intent e2e fixture。
// 所有必需卡片都必须存在且为 JSON object，避免测试运行到中途才暴露缺 fixture。
func newDreamExecutor(path string) (*dreamExecutor, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("prompt intent e2e dream fixture path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prompt intent e2e dream fixture %q: %w", path, err)
	}
	var cards map[string]json.RawMessage
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("parse prompt intent e2e dream fixture %q: %w", path, err)
	}
	for _, key := range requiredFixtureKeys {
		raw := cards[key]
		if len(raw) == 0 {
			return nil, fmt.Errorf("prompt intent e2e dream fixture %q missing key %q", path, key)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, fmt.Errorf("prompt intent e2e dream fixture key %q must be a JSON object: %w", key, err)
		}
	}
	sum := sha256.Sum256(data)
	return &dreamExecutor{
		pathHash: hex.EncodeToString(sum[:])[:pathHashMaxChars],
		cards:    cards,
	}, nil
}

func provideDreamExecutorProvider() (contract.DreamExecutorProvider, error) {
	executor, err := newDreamExecutor(os.Getenv(FixturePathEnv))
	if err != nil {
		return contract.DreamExecutorProvider{}, err
	}
	return contract.DreamExecutorProvider{Name: ProviderName, Executor: executor}, nil
}

// ExecuteDream 根据 prompt 选择 fixture 卡片并返回渲染后的 JSON。
// prompt 为空或无法匹配卡片时直接报错，保证 e2e 用例不会误用默认响应。
func (e *dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("prompt intent e2e dream prompt is empty")
	}
	key, err := e.fixtureKey(prompt)
	if err != nil {
		return "", err
	}
	if key == "health" {
		return e.health()
	}
	raw := e.cards[key]
	if len(raw) == 0 {
		return "", fmt.Errorf("prompt intent e2e dream fixture missing selected key %q", key)
	}
	rendered, err := renderFixtureCard(raw, prompt)
	if err != nil {
		return "", err
	}
	return rendered, nil
}

// fixtureKey 根据 prompt 中的标记选择 fixture 卡片。
// 该匹配规则是测试 wire 协议的一部分，新增测试变体时必须显式扩展。
func (e *dreamExecutor) fixtureKey(prompt string) (string, error) {
	normalized := strings.ToLower(prompt)
	switch {
	case strings.Contains(normalized, "e2e health"):
		return "health", nil
	case strings.Contains(normalized, "fixture:block") || strings.Contains(normalized, "block variant"):
		return "block", nil
	case strings.Contains(normalized, "fixture:review") || strings.Contains(normalized, "review variant"):
		return "review", nil
	case strings.Contains(normalized, "requested_kind: expert"):
		return "expert", nil
	case strings.Contains(normalized, "requested_kind: recall"):
		return "recall", nil
	case strings.Contains(normalized, "requested_kind: default_rule"):
		return "default_rule", nil
	default:
		return "", fmt.Errorf("prompt intent e2e dream fixture has no match for prompt")
	}
}

func renderFixtureCard(raw json.RawMessage, prompt string) (string, error) {
	rendered := string(raw)
	if !strings.Contains(rendered, runIDPlaceholder) {
		return rendered, nil
	}
	runID, ok := promptRunID(prompt)
	if !ok {
		return "", fmt.Errorf("prompt intent e2e dream fixture requires run_id when card uses %s", runIDPlaceholder)
	}
	return strings.ReplaceAll(rendered, runIDPlaceholder, runID), nil
}

func promptRunID(prompt string) (string, bool) {
	for _, line := range strings.Split(prompt, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(key) != "run_id" {
			continue
		}
		runID := strings.TrimSpace(value)
		return runID, runID != ""
	}
	return "", false
}

func (e *dreamExecutor) health() (string, error) {
	var health map[string]any
	if err := json.Unmarshal(e.cards["health"], &health); err != nil {
		return "", err
	}
	health["provider"] = ProviderName
	health["fixture_path_hash"] = e.pathHash
	raw, err := json.Marshal(health)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
