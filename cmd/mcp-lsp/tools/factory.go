package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// ToolHandler 是 MCP 工具层统一的处理函数类型。
type ToolHandler = middleware.Handler

// Handler 保留旧调用点使用的工具处理器别名。
type Handler = ToolHandler

type explicitToolWorkDirContextKey struct{}

// decodeMode 控制工具参数按原始、宽松或严格模式解码。
type decodeMode int

// actionHandler 是按 action 分发表中单个动作的处理函数。
type actionHandler[T any] func(context.Context, T) (any, error)

type appManagedWriteCapabilityContextKey struct{}
type appManagedReadCapabilityContextKey struct{}

// 解码模式常量决定未知字段、空参数和原始 payload 的处理策略。
const (
	decodeRaw decodeMode = iota
	decodeLenient
	decodeStrict
)

// WithAppManagedWriteCapability 标记调用方已经通过应用侧授权，可写入 app-managed 数据根。
// 默认 direct edit 不带该能力，因此只能写 workspace roots 内文件。
func WithAppManagedWriteCapability(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, appManagedWriteCapabilityContextKey{}, true)
}

// WithAppManagedReadCapability 标记调用方已经通过应用侧授权，可读取 app-managed 数据根。
// 默认 direct file/diagnostics 不带该能力，因此只能读取 workspace roots 内文件。
func WithAppManagedReadCapability(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, appManagedReadCapabilityContextKey{}, true)
}

// hasAppManagedWriteCapability 读取应用侧授予的 app-managed 写能力标记。
func hasAppManagedWriteCapability(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(appManagedWriteCapabilityContextKey{}).(bool)
	return allowed
}

// hasAppManagedReadCapability 读取应用侧授予的 app-managed 读能力标记。
func hasAppManagedReadCapability(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(appManagedReadCapabilityContextKey{}).(bool)
	return allowed
}

func toolWorkspaceRoot(ctx context.Context) (string, error) {
	return common.WorkspaceRootFromContextStrict(ctx)
}

func scopedWorkspaceRoots(ctx context.Context) ([]string, error) {
	roots, err := common.WorkspaceRootsFromContextStrict(ctx)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, common.ErrMissingWorkspaceRoots
	}
	return append([]string(nil), roots...), nil
}

func appendAppManagedRoots(roots []string, capability string) ([]string, error) {
	appRoots, err := platformshared.AppManagedDataRoots()
	if err != nil {
		return nil, fmt.Errorf("resolve app-managed %s roots: %w", capability, err)
	}
	return append(append([]string(nil), roots...), appRoots...), nil
}

func toolWorkspaceRoots(ctx context.Context) (string, []string, error) {
	roots, err := scopedWorkspaceRoots(ctx)
	if err != nil {
		return "", nil, err
	}
	if hasAppManagedWriteCapability(ctx) {
		roots, err = appendAppManagedRoots(roots, "write")
		if err != nil {
			return "", nil, err
		}
	}
	return roots[0], append([]string(nil), roots[1:]...), nil
}

func toolReadableRoots(ctx context.Context) (string, []string, error) {
	roots, err := scopedWorkspaceRoots(ctx)
	if err != nil {
		return "", nil, err
	}
	if hasAppManagedReadCapability(ctx) {
		roots, err = appendAppManagedRoots(roots, "read")
		if err != nil {
			return "", nil, err
		}
	}
	return roots[0], append([]string(nil), roots[1:]...), nil
}

func toolResolvePath(ctx context.Context, target string) (search.PathInfo, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return search.PathInfo{}, err
	}
	return search.ResolvePathInRoots(root, roots, target)
}

func newManagerTool[T any](
	name string,
	tier time.Duration,
	registry lspmanager.Registry,
	mode decodeMode,
	dispatch func(context.Context, lspmanager.Registry, T) (any, error),
) ToolHandler {
	if registry == nil {
		return missingManagerHandler()
	}
	return wrapToolHandler(name, tier, func(ctx context.Context, params json.RawMessage) (any, error) {
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
			return fmt.Errorf("json: unknown field %q", field)
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
	for i := range t.NumField() {
		addStrictJSONField(t.Field(i), allowed)
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

// legacyPositionMigrationHint 从严格解码错误中识别旧版 file_path/line/column 参数。
// 只在命中旧字段时提示改用统一 pos 参数，其他错误保持通用修复建议。
func legacyPositionMigrationHint(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, `"file_path"`),
		strings.Contains(msg, `"line"`),
		strings.Contains(msg, `"column"`),
		strings.Contains(msg, "field file_path"),
		strings.Contains(msg, "field line"),
		strings.Contains(msg, "field column"):
		return `the inspect/xref/completion tools merged file_path/line/column into a single pos parameter formatted as "file_path:line:column" (example internal/foo.go:42:9)`
	default:
		return ""
	}
}

func dispatchToolAction[T any](
	ctx context.Context,
	label string,
	action string,
	req T,
	handlers map[string]actionHandler[T],
) (any, error) {
	normalized := normalizeAction(action)
	if alias := legacyActionAlias(label, normalized); alias != "" {
		normalized = alias
	}
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

func legacyActionAlias(label string, action string) string {
	switch label {
	case "file":
		switch action {
		case "read":
			return "read_file"
		case "open":
			return "open_file"
		}
	}
	return ""
}

// legacyActionHint 为历史 action 名称返回兼容提示。
// 只提示仍被接受或有明确替代的旧名称，避免把任意拼写错误误导成兼容行为。
func legacyActionHint(label string, action string) string {
	switch label {
	case "file":
		switch action {
		case "read":
			return `legacy action "read" is accepted as "read_file"`
		case "open":
			return `legacy action "open" is accepted as "open_file"`
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
	return registry.GetManagerForFileWithLanguage(ctx, filePath, normalizeLanguageIDOverride(languageID))
}

func normalizeLanguageIDOverride(languageID string) string {
	return strings.ToLower(strings.TrimSpace(languageID))
}

// requireFilePath 验证路径非空，返回 trim 后的路径。
func requireFilePath(raw string) (string, error) {
	filePath := strings.TrimSpace(raw)
	if filePath == "" {
		return "", errors.New("file_path is required; pass an absolute or workspace-relative path")
	}
	return filePath, nil
}

// normalizeAction 把 action 字符串规范化为小写无空白。
func normalizeAction(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// renderListResult 截取列表至 limit，空时返回标准空列表信封。
func renderListResult[T any](items []T, limit int, emptyMessage string, render func([]T, int) any) (any, error) {
	total := len(items)
	items = limitSlice(items, limit)
	if len(items) == 0 {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: emptyMessage},
		}, nil
	}
	return render(items, total), nil
}

// wrapToolHandler 用 Recovery/Logging/Timeout/Budget 中间件链包装工具处理函数。
func wrapToolHandler(toolName string, tier time.Duration, handler middleware.Handler) middleware.Handler {
	log := pkglogger.Get()
	scopedHandler := func(ctx context.Context, params json.RawMessage) (any, error) {
		var err error
		ctx, err = contextWithExplicitToolWorkDir(ctx, params)
		if err != nil {
			return nil, err
		}
		return handler(ctx, params)
	}
	chained := middleware.Chain(
		scopedHandler,
		middleware.Recovery(log, toolName),
		middleware.Logging(log, toolName),
		middleware.Timeout(tier),
	)
	return middleware.WithOutputBudget(toolName, chained, middleware.Budget{})
}

// contextWithExplicitToolWorkDir 从工具请求参数中提取 work_dir 并写入 tool scope。
// 空参数保持原 context；非法 JSON 或越界路径会直接返回错误。
func contextWithExplicitToolWorkDir(ctx context.Context, params json.RawMessage) (context.Context, error) {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ctx, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return ctx, fmt.Errorf("parse explicit tool work_dir params: %w", err)
	}
	rawWorkDir, ok := fields["work_dir"]
	if !ok {
		return ctx, nil
	}
	var workDir string
	if err := json.Unmarshal(rawWorkDir, &workDir); err != nil {
		return ctx, fmt.Errorf("parse explicit tool work_dir: %w", err)
	}
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ctx, errors.New("work_dir is required")
	}
	scopedCtx, _, err := contextWithExplicitWorkDir(ctx, workDir)
	if err != nil {
		return ctx, err
	}
	return scopedCtx, nil
}

// contextWithExplicitWorkDir 把显式传入的 work_dir 写入 ctx 的 tool scope，并验证路径在工作区根内。
func contextWithExplicitWorkDir(ctx context.Context, workDir string) (context.Context, string, error) {
	normalized, err := normalizeExplicitWorkDir(ctx, workDir)
	if err != nil {
		return ctx, "", err
	}
	if err := ensureExplicitWorkDirWithinWorkspaceRoots(ctx, normalized); err != nil {
		return ctx, "", err
	}
	scope, _ := common.ToolScopeFromContext(ctx)
	scope.CWD = normalized
	scope.WorkspaceRoots = append(scope.WorkspaceRoots, normalized)
	if strings.TrimSpace(scope.Family) == "" {
		scope.Family = "lsp"
	}
	return context.WithValue(common.WithToolScope(ctx, scope), explicitToolWorkDirContextKey{}, true), normalized, nil
}

// ensureExplicitWorkDirWithinWorkspaceRoots 确保 work_dir 在工作区根目录范围内。
func ensureExplicitWorkDirWithinWorkspaceRoots(ctx context.Context, workDir string) error {
	roots, err := common.WorkspaceRootsFromContextStrict(ctx)
	if err != nil {
		return fmt.Errorf("explicit work_dir requires trusted workspace roots: %w", err)
	}
	for _, root := range roots {
		if platformshared.ContainsPath(root, workDir) {
			return nil
		}
	}
	return fmt.Errorf("work_dir %s is outside workspace roots [%s]", workDir, strings.Join(roots, ", "))
}

// normalizeExplicitWorkDir 规范化工具请求中的显式 work_dir。
// 相对路径必须基于可信 workspace root 展开，并且最终必须指向已存在目录。
func normalizeExplicitWorkDir(ctx context.Context, workDir string) (string, error) {
	trimmed := strings.TrimSpace(workDir)
	if trimmed == "" {
		return "", errors.New("work_dir is required")
	}
	if !filepath.IsAbs(trimmed) {
		root, err := common.WorkspaceRootFromContextStrict(ctx)
		if err != nil {
			return "", fmt.Errorf("relative work_dir requires trusted workspace root: %w", err)
		}
		trimmed = filepath.Join(root, trimmed)
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat work_dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("work_dir is not a directory: %s", resolved)
	}
	return filepath.Clean(resolved), nil
}

// explicitToolWorkDirFromContext 标记本次调用已经用参数中的 work_dir 重建可信作用域。
// grep 的 stale fallback 只适用于缺少显式工作目录的旧运行时根，不应拦截这种调用。
func explicitToolWorkDirFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	ok, _ := ctx.Value(explicitToolWorkDirContextKey{}).(bool)
	return ok
}

// funcRangeEnricher 按需读取并缓存 DocumentSymbols。
// inspect/xref 等位置型工具用它给 LocationResult 附加 FuncStart/FuncEnd，同一请求内同文件只查一次。
type funcRangeEnricher struct {
	ctx      context.Context
	registry lspmanager.Registry
	cache    map[string][]protocol.DocumentSymbol
}

// newFuncRangeEnricher 创建 funcRangeEnricher，registry 为 nil 时返回 nil。
func newFuncRangeEnricher(ctx context.Context, registry lspmanager.Registry) *funcRangeEnricher {
	if registry == nil {
		return nil
	}
	return &funcRangeEnricher{
		ctx:      ctx,
		registry: registry,
		cache:    make(map[string][]protocol.DocumentSymbol),
	}
}

// Symbols 返回文件的 DocumentSymbols，并在请求级缓存中复用结果。
// registry 或语言服务器错误会直接返回给调用方，避免生成没有函数范围的伪成功结果。
func (p *funcRangeEnricher) Symbols(absPath string) ([]protocol.DocumentSymbol, error) {
	if p == nil {
		return nil, errors.New("funcRangeEnricher is nil")
	}
	if cached, ok := p.cache[absPath]; ok {
		return cached, nil
	}
	mgr, err := p.registry.GetManagerForFile(p.ctx, absPath)
	if err != nil {
		return nil, err
	}
	symbols, err := mgr.DocumentSymbol(p.ctx, fileURI(absPath))
	if err != nil {
		return nil, err
	}
	p.cache[absPath] = symbols
	return symbols, nil
}

// ToPlainText 渲染为纯文本。
func (e emptyListEnvelope) ToPlainText() string {
	if e.Meta.Message != "" {
		return e.Meta.Message
	}
	return "No items found."
}
