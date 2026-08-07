package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type strictToolUnknownFieldError struct {
	Field string
}

// Error 按 encoding/json 的未知字段格式返回错误信息。
func (e *strictToolUnknownFieldError) Error() string {
	return fmt.Sprintf("json: unknown field %q", e.Field)
}

func newManagerTool[T any](
	name string,
	tier time.Duration,
	registry lspmanager.Registry,
	mode decodeMode,
	dispatch func(context.Context, lspmanager.Registry, T) (any, error),
) ToolHandler {
	return newManagerToolWithTimeoutResolver(name, tier, nil, registry, mode, dispatch)
}

// newManagerToolWithoutOuterTimeout 创建不共享工具级总 deadline 的 manager 工具。
// 适用于由底层 LSP 单步 deadline 独立约束的多步骤导航请求。
func newManagerToolWithoutOuterTimeout[T any](
	name string,
	tier time.Duration,
	registry lspmanager.Registry,
	mode decodeMode,
	dispatch func(context.Context, lspmanager.Registry, T) (any, error),
) ToolHandler {
	return newManagerToolWithTimeoutResolver(name, tier, func(json.RawMessage) time.Duration {
		return toolTimeoutDisabled
	}, registry, mode, dispatch)
}

// newManagerToolWithTimeoutResolver 统一组装 manager 工具，并允许动作级选择工具层 deadline。
func newManagerToolWithTimeoutResolver[T any](
	name string,
	tier time.Duration,
	timeoutTier func(json.RawMessage) time.Duration,
	registry lspmanager.Registry,
	mode decodeMode,
	dispatch func(context.Context, lspmanager.Registry, T) (any, error),
) ToolHandler {
	if registry == nil {
		return missingManagerHandler()
	}
	return wrapToolHandlerWithTimeoutResolver(name, tier, timeoutTier, func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeToolParams[T](params, mode)
		if err != nil {
			return nil, err
		}
		return dispatch(ctx, registry, req)
	})
}

func decodeToolParams[T any](raw json.RawMessage, mode decodeMode) (T, error) {
	var value T
	var err error
	switch mode {
	case decodeLenient:
		err = decodeLenientToolParams(raw, &value)
	case decodeStrict:
		err = decodeStrictToolParams(raw, &value)
	default:
		err = decodeRawToolParams(raw, &value)
	}
	if err != nil {
		return value, err
	}
	return value, nil
}

func decodeRawToolParams[T any](raw json.RawMessage, value *T) error {
	return unmarshalToolParams(raw, value)
}

func decodeLenientToolParams[T any](raw json.RawMessage, value *T) error {
	return decodeStrictToolParams(raw, value)
}

func decodeStrictToolParams[T any](raw json.RawMessage, value *T) error {
	normalized := normalizeOptionalToolParams(raw)
	stripped, err := stripToolWrapperFields(normalized)
	if err != nil {
		return err
	}
	if err := validateStrictToolFields(stripped, value); err != nil {
		return formatDecodeParamsError(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(stripped))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return formatDecodeParamsError(err)
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("decode params: unexpected trailing JSON payload")
	}
	return nil
}

// validateStrictToolFields 在自定义 UnmarshalJSON 前检查顶层字段，避免自定义解码吞掉未知字段。
func validateStrictToolFields[T any](raw []byte, value *T) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return err
	}
	allowed := strictToolFieldSet(value)
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			return &strictToolUnknownFieldError{Field: field}
		}
	}
	return nil
}

// strictToolFieldSet 收集工具入参允许的 JSON 字段，并保留明确支持的兼容别名。
func strictToolFieldSet[T any](value *T) map[string]struct{} {
	allowed := make(map[string]struct{})
	addStrictJSONFields(reflect.TypeFor[T](), allowed)
	switch any(value).(type) {
	case *grepToolInput:
		allowed["paths"] = struct{}{}
		allowed["file_paths"] = struct{}{}
	}
	return allowed
}

// addStrictJSONFields 按 encoding/json 的顶层字段规则收集结构体字段。
func addStrictJSONFields(t reflect.Type, allowed map[string]struct{}) {
	t = strictJSONStructType(t)
	if t.Kind() != reflect.Struct {
		return
	}
	for field := range t.Fields() {
		addStrictJSONField(field, allowed)
	}
}

// strictJSONStructType 解开指针层，nil 时返回空结构体类型方便调用方统一处理。
func strictJSONStructType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return reflect.TypeOf(struct{}{})
	}
	return t
}

// addStrictJSONField 处理单个结构体字段，匿名嵌入字段按 JSON 展开规则递归收集。
func addStrictJSONField(field reflect.StructField, allowed map[string]struct{}) {
	if field.PkgPath != "" && !field.Anonymous {
		return
	}
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "-" {
		return
	}
	if field.Anonymous && name == "" {
		addStrictJSONFields(field.Type, allowed)
		return
	}
	if name == "" {
		name = field.Name
	}
	allowed[name] = struct{}{}
}

// stripToolWrapperFields 去掉 handler 外层已经消费的参数，剩余字段继续走严格 schema。
// work_dir 只允许由 wrapper 处理；agent_id/cwd 旧字段必须显式迁移。
func stripToolWrapperFields(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, fmt.Errorf("parse wrapper fields: %w", err)
	}
	changed, err := validateReservedToolWrapperFields(fields)
	if err != nil {
		return nil, err
	}
	if !changed {
		return raw, nil
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode params without wrapper fields: %w", err)
	}
	return encoded, nil
}

func normalizeOptionalToolParams(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return trimmed
}

func unmarshalToolParams[T any](raw []byte, value *T) error {
	if err := json.Unmarshal(raw, value); err != nil {
		return formatDecodeParamsError(err)
	}
	return nil
}

func formatDecodeParamsError(err error) error {
	hint := "next: pass numeric fields as JSON numbers, string fields as JSON strings, and remove unknown fields"
	if migration := legacyPositionMigrationHint(err); migration != "" {
		hint = hint + "; " + migration
	}
	return fmt.Errorf("decode params: %w; %s", err, hint)
}

func dispatchToolAction[T any](
	ctx context.Context,
	label string,
	action string,
	req T,
	handlers map[string]actionHandler[T],
) (any, error) {
	normalized := normalizeAction(action)
	handler, ok := handlers[normalized]
	if !ok {
		return nil, unsupportedActionError(label, action, handlers)
	}
	return handler(ctx, req)
}

func unsupportedActionError[T any](label string, action string, handlers map[string]actionHandler[T]) error {
	valid := validActionNames(handlers)
	message := fmt.Sprintf("unsupported %s action %q (valid actions: %s)", label, action, strings.Join(valid, ", "))
	if closest := closestAction(normalizeAction(action), valid); closest != "" {
		message += fmt.Sprintf("; did you mean %q?", closest)
	}
	if hint := legacyActionHint(label, normalizeAction(action)); hint != "" {
		message += "; " + hint
	}
	return errors.New(message)
}

func validActionNames[T any](handlers map[string]actionHandler[T]) []string {
	names := make([]string, 0, len(handlers))
	for name := range handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// legacyActionHint 为已移除的历史 action 名称返回迁移提示。
func legacyActionHint(label string, action string) string {
	switch label {
	case "file":
		switch action {
		case "read":
			return `legacy action "read" is no longer accepted; use "read_file"`
		case "open":
			return `legacy action "open" is no longer accepted; use "open_file"`
		}
	case "xref":
		if action == "references" {
			return `use tool "xref" with action "references"`
		}
	}
	return ""
}

func closestAction(action string, valid []string) string {
	if action == "" {
		return ""
	}
	best := ""
	bestDistance := 3
	for _, candidate := range valid {
		distance := editDistance(action, candidate)
		if distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	return best
}

// editDistance 计算短 action 名称的编辑距离，用于 unsupported action 的最近候选提示。
func editDistance(a string, b string) int {
	ar := []rune(a)
	br := []rune(b)
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}

func missingDependencyHandler(message string) ToolHandler {
	return func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New(message)
	}
}

func missingManagerHandler() ToolHandler {
	return missingDependencyHandler("lsp manager is not available; use text_search or read_file as alternatives")
}

func managerForFile(ctx context.Context, registry lspmanager.Registry, filePath string, languageID string) (lspmanager.Manager, error) {
	if registry == nil {
		return nil, errManagerUnavailable
	}
	resolvedLanguageID, err := resolveLanguageIDForFile(ctx, filePath, languageID)
	if err != nil {
		return nil, err
	}
	manager, err := registry.GetManagerForFileWithLanguage(ctx, filePath, resolvedLanguageID)
	if err != nil && errors.Is(err, lspmanager.ErrUnsupportedLanguage) {
		return nil, languageUnsupportedError(
			err,
			filePath,
			normalizeLanguageIDOverride(languageID),
			lspmanager.DetectLanguageID(filePath),
			resolvedLanguageID,
		)
	}
	return manager, err
}

func languageUnsupportedError(err error, filePath, requested, detected, resolved string) error {
	meta := map[string]any{
		"requested_language": requested,
		"detected_language":  detected,
		"resolved_language":  resolved,
		"file_extension":     strings.ToLower(filepath.Ext(filePath)),
		"adapter_status":     "registry_lookup_miss",
	}
	return &common.CodedToolError{
		Err:       err,
		Code:      "language_unsupported",
		Retryable: false,
		Meta:      meta,
	}
}

func normalizeLanguageIDOverride(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}
