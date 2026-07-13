package archtest

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type wireDTOFieldRule struct {
	Name   string
	Type   reflect.Type
	Mapped []string
	Exempt map[string]string
}

type skillInfoConsumerField struct {
	Field     string `json:"field"`
	Validator string `json:"validator"`
	Consumer  string `json:"consumer"`
}

// TestSkillInfoConsumerRegistryMatchesProducerJSONFields binds the runtime frontend
// registry to contract.SkillInfo. Adding or removing a producer field must update the
// frontend validator registry in the same change.
func TestSkillInfoConsumerRegistryMatchesProducerJSONFields(t *testing.T) {
	t.Parallel()

	fields := collectWireDTOJSONFields(reflect.TypeFor[contract.SkillInfo]())
	registry := loadSkillInfoConsumerRegistry(t)
	registered := make(map[string]string, len(registry))
	for _, entry := range registry {
		if strings.TrimSpace(entry.Field) == "" || strings.TrimSpace(entry.Validator) == "" || strings.TrimSpace(entry.Consumer) == "" {
			t.Fatalf("SkillInfo consumer registry contains incomplete entry: %+v", entry)
		}
		if _, exists := registered[entry.Field]; exists {
			t.Fatalf("SkillInfo consumer registry contains duplicate field %q", entry.Field)
		}
		registered[entry.Field] = entry.Validator
	}

	assertWireDTOFieldsCovered(t, "internal/contract.SkillInfo -> skillSlashCommandAdapter", fields, registered)
	assertWireDTORegistryCurrent(t, "internal/contract.SkillInfo -> skillSlashCommandAdapter", fields, registered)
}

func loadSkillInfoConsumerRegistry(t *testing.T) []skillInfoConsumerField {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve wire DTO field registry test path")
	}
	path := filepath.Join(
		filepath.Dir(currentFile),
		"..", "..", "frontend-app", "src", "features", "slash-commands", "adapters", "skillInfoFieldRegistry.json",
	)
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read SkillInfo consumer registry: %v", err)
	}
	var registry []skillInfoConsumerField
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatalf("decode SkillInfo consumer registry: %v", err)
	}
	if len(registry) == 0 {
		t.Fatal("SkillInfo consumer registry must not be empty")
	}
	return registry
}

// TestWireDTORegistryCoversSelectedSurfaceFields 扩展 FileMetrics 之外的字段守卫。
// 这些 DTO 是 internal/dto、internal/contract 到 UI/provider mirror 的 wire 边界；
// 新增 JSON 字段时必须登记 mapper 消费面，或写明为什么暂不进入该 wire surface。
func TestWireDTORegistryCoversSelectedSurfaceFields(t *testing.T) {
	t.Parallel()

	for _, rule := range wireDTOFieldRules() {
		t.Run(rule.Name, func(t *testing.T) {
			assertWireDTOFieldRule(t, rule)
		})
	}
}

// TestWireDTOFieldRegistryRequiresToolCallEndPersistFailure 锁定工具结果持久化失败字段必须进入 wire registry。
func TestWireDTOFieldRegistryRequiresToolCallEndPersistFailure(t *testing.T) {
	t.Parallel()
	registered := registeredWireDTOFields(toolCallEndWireDTOFieldRule())
	for _, field := range []string{"persist_failed", "persist_error"} {
		if registered[field] != "mapped" {
			t.Fatalf("ToolCallEnd field %q = %q, want mapped", field, registered[field])
		}
	}
}

func wireDTOFieldRules() []wireDTOFieldRule {
	return []wireDTOFieldRule{
		threadStartedWireDTOFieldRule(),
		turnOutputDeltaWireDTOFieldRule(),
		toolCallEndWireDTOFieldRule(),
		agentRuntimeReportedWireDTOFieldRule(),
		skillMirrorReportItemWireDTOFieldRule(),
		skillProviderMirrorTargetWireDTOFieldRule(),
	}
}

func assertWireDTOFieldRule(t *testing.T, rule wireDTOFieldRule) {
	t.Helper()
	fields := collectWireDTOJSONFields(rule.Type)
	registered := registeredWireDTOFields(rule)
	assertWireDTOFieldsCovered(t, rule.Name, fields, registered)
	assertWireDTORegistryCurrent(t, rule.Name, fields, registered)
	assertWireDTOExemptionsHaveReasons(t, rule)
}

func threadStartedWireDTOFieldRule() wireDTOFieldRule {
	return wireDTOFieldRule{
		Name:   "internal/dto/thread.Started -> eventsurface.threadStartedPayload",
		Type:   reflect.TypeFor[threaddto.Started](),
		Mapped: []string{"thread_id", "agent_id", "provider", "provider_thread_id", "cwd", "model"},
		Exempt: map[string]string{
			"timestamp":      "eventsurface 当前通知不下发事件时间，UI 投影负责排序时间线",
			"name":           "thread 展示名由 UI projection/update 事件更新，threadStartedPayload 保持启动路由最小面",
			"pending_launch": "pending launch 是内部惰性启动状态，前端通过后续 launch/projection 事件感知",
		},
	}
}

func turnOutputDeltaWireDTOFieldRule() wireDTOFieldRule {
	return wireDTOFieldRule{
		Name:   "internal/dto/turn.TurnOutputDelta -> eventsurface.turnOutputDeltaPayload",
		Type:   reflect.TypeFor[turndto.TurnOutputDelta](),
		Mapped: []string{"thread_id", "agent_id", "turn_id", "stream", "delta"},
		Exempt: map[string]string{
			"timestamp": "streaming delta 不带事件时间，客户端按接收顺序和 thread patch sequence 排序",
		},
	}
}

func toolCallEndWireDTOFieldRule() wireDTOFieldRule {
	return wireDTOFieldRule{
		Name: "internal/dto/tool.ToolCallEnd -> eventsurface.toolCallEndPayload",
		Type: reflect.TypeFor[tooldto.ToolCallEnd](),
		Mapped: []string{
			"thread_id", "agent_id", "turn_id", "call_id", "tool_name", "success",
			"error", "result", "persisted_path", "persist_failed", "persist_error", "truncated", "original_size", "elapsed_ms",
		},
		Exempt: map[string]string{
			"timestamp": "工具时间线使用 UI patch/event order，tool call end payload 不重复携带 EventHeader 时间",
		},
	}
}

func agentRuntimeReportedWireDTOFieldRule() wireDTOFieldRule {
	return wireDTOFieldRule{
		Name:   "internal/dto/agent.AgentRuntimeReported -> eventsurface.agentRuntimeReportedPayload",
		Type:   reflect.TypeFor[agentdto.AgentRuntimeReported](),
		Mapped: []string{"thread_id", "agent_id", "session_id", "port", "provider"},
		Exempt: map[string]string{
			"timestamp": "runtime report 只表示当前运行端口快照，UI 不按该事件时间排序",
		},
	}
}

func skillMirrorReportItemWireDTOFieldRule() wireDTOFieldRule {
	return wireDTOFieldRule{
		Name: "internal/contract.SkillMirrorReportItem -> provider mirror report JSON",
		Type: reflect.TypeFor[contract.SkillMirrorReportItem](),
		Mapped: []string{
			"target_id", "provider", "scope", "relative_mirror_path", "canonical_id",
			"old_hash", "new_hash", "conflict_kind", "error",
		},
		Exempt: map[string]string{},
	}
}

func skillProviderMirrorTargetWireDTOFieldRule() wireDTOFieldRule {
	return wireDTOFieldRule{
		Name:   "internal/contract.SkillProviderMirrorTarget -> provider mirror reconcile input",
		Type:   reflect.TypeFor[contract.SkillProviderMirrorTarget](),
		Mapped: []string{"provider", "home_root", "skills_root", "allow_explicit_home"},
		Exempt: map[string]string{},
	}
}

func registeredWireDTOFields(rule wireDTOFieldRule) map[string]string {
	registered := make(map[string]string, len(rule.Mapped)+len(rule.Exempt))
	for _, field := range rule.Mapped {
		registered[field] = "mapped"
	}
	maps.Copy(registered, rule.Exempt)
	return registered
}

func assertWireDTOFieldsCovered(t *testing.T, name string, fields map[string]bool, registered map[string]string) {
	t.Helper()
	var missing []string
	for field := range fields {
		if _, ok := registered[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%s has JSON fields missing from wire registry: %v", name, missing)
	}
}

func assertWireDTORegistryCurrent(t *testing.T, name string, fields map[string]bool, registered map[string]string) {
	t.Helper()
	var stale []string
	for field := range registered {
		if !fields[field] {
			stale = append(stale, field)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("%s wire registry references fields that no longer exist: %v", name, stale)
	}
}

func assertWireDTOExemptionsHaveReasons(t *testing.T, rule wireDTOFieldRule) {
	t.Helper()
	for field, reason := range rule.Exempt {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("%s field %q has empty exemption reason", rule.Name, field)
		}
	}
}

func collectWireDTOJSONFields(typ reflect.Type) map[string]bool {
	fields := make(map[string]bool)
	collectWireDTOJSONFieldsInto(typ, fields)
	return fields
}

func collectWireDTOJSONFieldsInto(typ reflect.Type, fields map[string]bool) {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if shouldExpandEmbeddedWireDTOField(field) {
			collectWireDTOJSONFieldsInto(field.Type, fields)
			continue
		}
		if tag, ok := wireDTOJSONFieldName(field); ok {
			fields[tag] = true
		}
	}
}

func shouldExpandEmbeddedWireDTOField(field reflect.StructField) bool {
	return field.Anonymous && field.Tag.Get("json") == ""
}

func wireDTOJSONFieldName(field reflect.StructField) (string, bool) {
	tag := strings.TrimSpace(field.Tag.Get("json"))
	if tag == "" || tag == "-" {
		return "", false
	}
	if comma := strings.IndexByte(tag, ','); comma >= 0 {
		tag = tag[:comma]
	}
	return tag, tag != "" && tag != "-"
}
