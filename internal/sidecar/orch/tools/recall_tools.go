package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const promptRecallUnknownTopicHint = "see recall_catalog section for available topics"
const promptRecallInvalidTopicHint = "use lowercase dash-separated recall topics shorter than 64 characters"
const promptRecallAlreadyRecalledWarning = "already recalled in this thread"

var recallTopicNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

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

// mark 标记编排。
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

// HandlePromptRecall 处理promptrecall。
func HandlePromptRecall(store promptRecallStore) ToolHandler {
	return handlePromptRecall(store, newPromptRecallTracker())
}

func handlePromptRecall(store promptRecallStore, tracker *promptRecallTracker) ToolHandler {
	return makeHandler(store, "prompt store", func(ctx context.Context, in promptRecallInput) (promptRecallResult, error) {
		return recallPromptSection(ctx, store, in, tracker)
	})
}

func recallToolDefinitions(store promptRecallStore) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("prompt_recall", "Recall a prompt knowledge package by topic. Use recall_catalog to discover available topics.", ObjectSchema(map[string]Schema{
			"topic": StringSchema("知识包 topic"),
		}, "topic"), HandlePromptRecall(store)),
	)
}

// recallPromptSection 处理recallpromptsection。
func recallPromptSection(ctx context.Context, store promptRecallStore, input promptRecallInput, tracker *promptRecallTracker) (promptRecallResult, error) {
	if err := requireDependency(store, "prompt store"); err != nil {
		return promptRecallResult{}, err
	}
	topic, err := requireTrimmed(input.Topic, "topic")
	if err != nil {
		return promptRecallResult{}, err
	}
	if !validRecallTopicName(topic) {
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
			// Recall misses are tool-level soft results so the caller can read
			// the hint and correct the topic instead of receiving an MCP call
			// failure with the useful payload hidden by the framework.
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
	return len(topic) < 64 && recallTopicNamePattern.MatchString(topic)
}
