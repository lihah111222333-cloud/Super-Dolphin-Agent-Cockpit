package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/jackc/pgx/v5"
)

type stubRecallPromptStore struct {
	body     string
	err      error
	gotCWD   string
	gotTopic string
	calls    int
}

func (s *stubRecallPromptStore) GetSectionByRecallTopic(_ context.Context, cwd, topic string) (string, error) {
	s.calls++
	s.gotCWD = cwd
	s.gotTopic = topic
	if s.err != nil {
		return "", s.err
	}
	return s.body, nil
}

func promptRecallTestContext() context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{CWD: "/repo/a", ThreadID: "thread-1"})
}

func TestHandlePromptRecallHitReturnsTopicBodyAndLength(t *testing.T) {
	store := &stubRecallPromptStore{body: "Use the smallest useful context."}
	handler := HandlePromptRecall(store)

	result, err := handler(promptRecallTestContext(), mustRawInput(t, promptRecallInput{Topic: " context-budget "}))
	if err != nil {
		t.Fatalf("HandlePromptRecall() error = %v", err)
	}
	got, ok := result.(promptRecallResult)
	if !ok {
		t.Fatalf("result type = %T, want promptRecallResult", result)
	}
	if store.gotTopic != "context-budget" {
		t.Fatalf("topic passed to store = %q, want context-budget", store.gotTopic)
	}
	if store.gotCWD != "/repo/a" {
		t.Fatalf("cwd passed to store = %q, want /repo/a", store.gotCWD)
	}
	assertPromptRecallHit(t, got, "context-budget", store.body)
}

func TestHandlePromptRecallHitSerializesEmptyBodyAndZeroLength(t *testing.T) {
	handler := HandlePromptRecall(&stubRecallPromptStore{body: ""})

	result, err := handler(promptRecallTestContext(), mustRawInput(t, promptRecallInput{Topic: "empty"}))
	if err != nil {
		t.Fatalf("HandlePromptRecall() error = %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	got := string(raw)
	for _, want := range []string{`"topic":"empty"`, `"body":""`, `"length":0`} {
		if !strings.Contains(got, want) {
			t.Fatalf("json = %s, want contain %s", got, want)
		}
	}
}

func TestHandlePromptRecallNotFoundReturnsSoftResult(t *testing.T) {
	store := &stubRecallPromptStore{
		err: platformdb.WrapStoreError(pgx.ErrNoRows, "get_section", "prompt_template_section"),
	}
	handler := HandlePromptRecall(store)

	result, err := handler(promptRecallTestContext(), mustRawInput(t, promptRecallInput{Topic: " missing "}))
	if err != nil {
		t.Fatalf("HandlePromptRecall() error = %v, want soft result", err)
	}
	got, ok := result.(promptRecallResult)
	if !ok {
		t.Fatalf("result type = %T, want promptRecallResult", result)
	}
	if got.Topic != "missing" {
		t.Fatalf("Topic = %q, want missing", got.Topic)
	}
	if got.Error != "unknown topic" {
		t.Fatalf("Error = %q, want unknown topic", got.Error)
	}
	if got.Hint != "see recall_catalog section for available topics" {
		t.Fatalf("Hint = %q, want recall_catalog hint", got.Hint)
	}
	if got.Body != nil || got.Length != nil {
		t.Fatalf("unexpected hit fields for miss: %+v", got)
	}
}

func TestHandlePromptRecallLogsHitAndMissShape(t *testing.T) {
	var logs bytes.Buffer
	previous := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { pkglogger.SetForTest(previous) })

	ctx := promptRecallTestContext()
	handler := HandlePromptRecall(&stubRecallPromptStore{body: "Use sqlc verify after generation."})
	if _, err := handler(ctx, mustRawInput(t, promptRecallInput{Topic: "sqlc-workflow"})); err != nil {
		t.Fatalf("HandlePromptRecall(hit) error = %v", err)
	}
	hitLogs := logs.String()
	for _, want := range []string{
		`"msg":"prompt_recall: call begin"`,
		`"msg":"prompt_recall: call done"`,
		`"tool_name":"prompt_recall"`,
		`"topic":"sqlc-workflow"`,
		`"thread_id":"thread-1"`,
		`"body_len":33`,
		`"hit":true`,
		`"latency_ms":`,
	} {
		if !strings.Contains(hitLogs, want) {
			t.Fatalf("hit logs missing %s in:\n%s", want, hitLogs)
		}
	}
	if strings.Contains(hitLogs, "Use sqlc verify") {
		t.Fatalf("hit logs leaked recall body:\n%s", hitLogs)
	}

	logs.Reset()
	missHandler := HandlePromptRecall(&stubRecallPromptStore{
		err: platformdb.WrapStoreError(pgx.ErrNoRows, "get_section", "prompt_template_section"),
	})
	if _, err := missHandler(ctx, mustRawInput(t, promptRecallInput{Topic: "missing-topic"})); err != nil {
		t.Fatalf("HandlePromptRecall(miss) error = %v", err)
	}
	missLogs := logs.String()
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"prompt_recall: call miss"`,
		`"topic":"missing-topic"`,
		`"thread_id":"thread-1"`,
		`"hit":false`,
		`"latency_ms":`,
	} {
		if !strings.Contains(missLogs, want) {
			t.Fatalf("miss logs missing %s in:\n%s", want, missLogs)
		}
	}
}

func TestHandlePromptRecallDuplicateTopicInSameThreadReturnsWarning(t *testing.T) {
	store := &stubRecallPromptStore{body: "Use sqlc verify after generation."}
	handler := HandlePromptRecall(store)
	ctx := promptRecallTestContext()

	firstResult, err := handler(ctx, mustRawInput(t, promptRecallInput{Topic: "sqlc-workflow"}))
	if err != nil {
		t.Fatalf("first HandlePromptRecall() error = %v", err)
	}
	first := firstResult.(promptRecallResult)
	if first.Warning != "" {
		t.Fatalf("first Warning = %q, want empty", first.Warning)
	}

	secondResult, err := handler(ctx, mustRawInput(t, promptRecallInput{Topic: "sqlc-workflow"}))
	if err != nil {
		t.Fatalf("second HandlePromptRecall() error = %v", err)
	}
	second := secondResult.(promptRecallResult)
	assertPromptRecallHit(t, second, "sqlc-workflow", store.body)
	if second.Warning != "already recalled in this thread" {
		t.Fatalf("second Warning = %q, want duplicate warning", second.Warning)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want 2", store.calls)
	}
}

func assertPromptRecallHit(t *testing.T, got promptRecallResult, wantTopic, wantBody string) {
	t.Helper()

	if got.Topic != wantTopic {
		t.Fatalf("Topic = %q, want %s", got.Topic, wantTopic)
	}
	if got.Body == nil || *got.Body != wantBody {
		t.Fatalf("Body = %v, want %q", got.Body, wantBody)
	}
	if got.Length == nil || *got.Length != len(wantBody) {
		t.Fatalf("Length = %v, want %d", got.Length, len(wantBody))
	}
	if got.Error != "" || got.Hint != "" {
		t.Fatalf("unexpected soft-error fields: %+v", got)
	}
}

func TestHandlePromptRecallEmptyTopicReturnsError(t *testing.T) {
	store := &stubRecallPromptStore{body: "unused"}
	handler := HandlePromptRecall(store)

	_, err := handler(context.Background(), mustRawInput(t, promptRecallInput{Topic: " \t\n "}))
	if err == nil {
		t.Fatal("HandlePromptRecall() error = nil, want required-field error")
	}
	if !strings.Contains(err.Error(), "topic is required") {
		t.Fatalf("error = %v, want topic is required", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestHandlePromptRecallInvalidTopicReturnsSoftResult(t *testing.T) {
	store := &stubRecallPromptStore{body: "unused"}
	handler := HandlePromptRecall(store)

	result, err := handler(context.Background(), mustRawInput(t, promptRecallInput{Topic: "LSP_Basics"}))
	if err != nil {
		t.Fatalf("HandlePromptRecall() error = %v, want soft result", err)
	}
	got := result.(promptRecallResult)
	if got.Topic != "LSP_Basics" || got.Error != "invalid topic" {
		t.Fatalf("result = %+v, want invalid topic soft result", got)
	}
	if !strings.Contains(got.Hint, "lowercase") {
		t.Fatalf("Hint = %q, want lowercase naming guidance", got.Hint)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestHandlePromptRecallStoreErrorFailsFast(t *testing.T) {
	storeErr := errors.New("database unavailable")
	handler := HandlePromptRecall(&stubRecallPromptStore{err: storeErr})

	result, err := handler(promptRecallTestContext(), mustRawInput(t, promptRecallInput{Topic: "ops"}))
	if err == nil {
		t.Fatalf("HandlePromptRecall() error = nil, want fail-fast store error")
	}
	if !errors.Is(err, storeErr) {
		t.Fatalf("error = %v, want wrapping store error", err)
	}
	got, ok := result.(promptRecallResult)
	if !ok {
		t.Fatalf("result type = %T, want promptRecallResult zero value", result)
	}
	if got.Error != "" || got.Hint != "" || got.Body != nil {
		t.Fatalf("result = %+v, want no soft result fields on store error", got)
	}
}

func TestHandlePromptRecallRequiresTrustedCWD(t *testing.T) {
	store := &stubRecallPromptStore{body: "unused"}
	handler := HandlePromptRecall(store)

	_, err := handler(context.Background(), mustRawInput(t, promptRecallInput{Topic: "ops"}))
	if err == nil {
		t.Fatal("HandlePromptRecall() error = nil, want trusted cwd error")
	}
	if !strings.Contains(err.Error(), "trusted cwd") {
		t.Fatalf("error = %v, want trusted cwd error", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestValidRecallTopicName(t *testing.T) {
	valid := []string{"lsp-basics", "frontend-react", "sqlc-workflow", strings.Repeat("a", 63)}
	for _, topic := range valid {
		if !validRecallTopicName(topic) {
			t.Fatalf("validRecallTopicName(%q) = false, want true", topic)
		}
	}
	invalid := []string{"", "LSP_Basics", "my topic", "a.b", "-leading", "trailing-", "two--dashes", strings.Repeat("a", 64)}
	for _, topic := range invalid {
		if validRecallTopicName(topic) {
			t.Fatalf("validRecallTopicName(%q) = true, want false", topic)
		}
	}
}

func TestNewRegistryIncludesPromptRecall(t *testing.T) {
	registry := NewRegistry(Dependencies{})

	def, ok := registry.Lookup("prompt_recall")
	if !ok {
		t.Fatal("prompt_recall not registered")
	}
	if def.Handler == nil {
		t.Fatal("prompt_recall handler is nil")
	}
	required, ok := def.InputSchema["required"].([]string)
	if !ok {
		t.Fatalf("required schema type = %T, want []string", def.InputSchema["required"])
	}
	if len(required) != 1 || required[0] != "topic" {
		t.Fatalf("required = %#v, want [topic]", required)
	}
	props, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties schema type = %T, want map[string]any", def.InputSchema["properties"])
	}
	if _, ok := props["cwd"]; ok {
		t.Fatalf("prompt_recall schema must not expose cwd argument: %#v", props)
	}
}
