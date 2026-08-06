package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const promptRecallUnknownTopicHint = "see recall_catalog section for available topics"
const promptRecallInvalidTopicHint = "use lowercase dash-separated recall topics shorter than 64 characters"
const promptRecallAlreadyRecalledWarning = "already recalled in this thread"

type promptRecallInput struct {
	Topic string `json:"topic"`
}

type promptRecallStore interface {
	GetSectionByRecallTopic(ctx context.Context, cwd, topic string) (string, error)
}

type promptRecallResult struct {
	Topic   string  `json:"topic,omitempty"`
	Body    *string `json:"body,omitempty"`
	Length  *int    `json:"length,omitempty"`
	Error   string  `json:"error,omitempty"`
	Hint    string  `json:"hint,omitempty"`
	Warning string  `json:"warning,omitempty"`
}

type promptRecallTracker struct {
	mu   sync.Mutex
	seen map[string]map[string]struct{}
}

func newPromptRecallTracker() *promptRecallTracker {
	return &promptRecallTracker{seen: map[string]map[string]struct{}{}}
}

// mark 记录当前 thread 已 recall 的 topic，并返回本次是否重复 recall。
func (t *promptRecallTracker) mark(threadID, topic string) bool {
	if t == nil || threadID == "" || topic == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	topics := t.seen[threadID]
	if topics == nil {
		topics = map[string]struct{}{}
		t.seen[threadID] = topics
	}
	if _, ok := topics[topic]; ok {
		return true
	}
	topics[topic] = struct{}{}
	return false
}

// HandlePromptRecall 注册 prompt_recall 工具，并为进程内重复 recall 提示维护记录器。
func HandlePromptRecall(store promptRecallStore) ToolHandler {
	return handlePromptRecallWithRuntimeState(store, newPromptRecallTracker(), newToolRuntimeState())
}

func handlePromptRecallWithRuntimeState(store promptRecallStore, tracker *promptRecallTracker, state *toolRuntimeState) ToolHandler {
	return makeHandler(store, "prompt store", func(ctx context.Context, in promptRecallInput) (promptRecallResult, error) {
		return recallPromptSectionWithRuntimeState(ctx, store, in, tracker, state)
	})
}

func recallToolDefinitionsWithRuntimeState(store promptRecallStore, state *toolRuntimeState) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("prompt_recall", "Recall a prompt knowledge package by topic. Use recall_catalog to discover available topics.", ObjectSchema(map[string]Schema{
			"topic": StringSchema("知识包 topic"),
		}, "topic"), handlePromptRecallWithRuntimeState(store, newPromptRecallTracker(), state)),
	)
}

// recallPromptSection 读取可信 cwd 下的 recall topic。
// 未知 topic 返回工具级软错误，存储不可用或 scope 缺失则 fail-fast。
func recallPromptSectionWithRuntimeState(ctx context.Context, store promptRecallStore, input promptRecallInput, tracker *promptRecallTracker, state *toolRuntimeState) (promptRecallResult, error) {
	if err := requireDependency(store, "prompt store"); err != nil {
		return promptRecallResult{}, err
	}
	topic, err := requireTrimmed(input.Topic, "topic")
	if err != nil {
		return promptRecallResult{}, err
	}
	if !validRecallTopicNameWithRuntimeState(state, topic) {
		return promptRecallSoftError(topic, "invalid topic", promptRecallInvalidTopicHint), nil
	}
	cwd, err := promptRecallTrustedCWD(ctx)
	if err != nil {
		return promptRecallResult{}, err
	}
	threadID := promptRecallThreadID(ctx)
	start := time.Now()
	pkglogger.Info("prompt_recall: call begin",
		pkglogger.FieldToolName, "prompt_recall",
		pkglogger.FieldTopic, topic,
		pkglogger.FieldThreadID, threadID,
		"cwd", cwd)
	body, err := store.GetSectionByRecallTopic(ctx, cwd, topic)
	if err != nil {
		if platformdb.IsNotFound(err) {
			// topic 未命中是可修正输入问题，返回软错误能让调用方看到 hint。
			pkglogger.Warn("prompt_recall: call miss",
				pkglogger.FieldToolName, "prompt_recall",
				pkglogger.FieldTopic, topic,
				pkglogger.FieldThreadID, threadID,
				"hit", false,
				pkglogger.FieldLatencyMS, time.Since(start).Milliseconds())
			return promptRecallResult{
				Topic: topic,
				Error: "unknown topic",
				Hint:  promptRecallUnknownTopicHint,
			}, nil
		}
		pkglogger.Warn("prompt_recall: call unavailable",
			pkglogger.FieldToolName, "prompt_recall",
			pkglogger.FieldTopic, topic,
			pkglogger.FieldThreadID, threadID,
			"hit", false,
			pkglogger.FieldLatencyMS, time.Since(start).Milliseconds(),
			pkglogger.FieldError, err)
		return promptRecallResult{}, fmt.Errorf("prompt_recall unavailable for topic %q: %w", topic, err)
	}
	length := len(body)
	pkglogger.Info("prompt_recall: call done",
		pkglogger.FieldToolName, "prompt_recall",
		pkglogger.FieldTopic, topic,
		pkglogger.FieldThreadID, threadID,
		"body_len", length,
		"hit", true,
		pkglogger.FieldLatencyMS, time.Since(start).Milliseconds())
	result := promptRecallResult{Topic: topic, Body: &body, Length: &length}
	if promptRecallAlreadySeen(ctx, tracker, topic) {
		result.Warning = promptRecallAlreadyRecalledWarning
	}
	return result, nil
}

func promptRecallTrustedCWD(ctx context.Context) (string, error) {
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok || strings.TrimSpace(scope.CWD) == "" {
		return "", fmt.Errorf("prompt_recall requires trusted cwd")
	}
	return scope.CWD, nil
}

func promptRecallSoftError(topic, errorText, hint string) promptRecallResult {
	return promptRecallResult{Topic: topic, Error: errorText, Hint: hint}
}

func promptRecallAlreadySeen(ctx context.Context, tracker *promptRecallTracker, topic string) bool {
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok {
		return false
	}
	return tracker.mark(scope.ThreadID, topic)
}

func promptRecallThreadID(ctx context.Context) string {
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok {
		return ""
	}
	return scope.ThreadID
}

func validRecallTopicName(topic string) bool {
	return validRecallTopicNameWithRuntimeState(newToolRuntimeState(), topic)
}

func validRecallTopicNameWithRuntimeState(state *toolRuntimeState, topic string) bool {
	return len(topic) < 64 && state.recallTopicNamePattern.MatchString(topic)
}
