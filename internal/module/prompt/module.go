package prompt

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/builtinprompts"
)

// Module 把 prompt 的注册表、组装器和 RPC 接起来。
// prompt 只负责组 start/turn 内容；memory 写入、skill mirror 和 provider 启动在别处做。
var Module = fx.Module("prompt",
	fx.Provide(
		NewConfig,
		NewServiceFx,
		builtinprompts.NewDefaultRegistry,
		AsPromptRegistry,
		AsPromptAssemblyService,
		AsDynamicSectionRegistrar,
		AsSectionInvalidator,
		registerPromptHandlers,
		newMatchWhenEvaluator,
		newEnableWhenEvaluator,
	),
)

// newMatchWhenEvaluator 暴露模板 auto-route 条件评估器给跨模块 contract。
func newMatchWhenEvaluator() contract.MatchWhenEvaluator {
	return EvaluateMatchWhen
}

// newEnableWhenEvaluator 暴露 section enable_when 条件评估器给跨模块 contract。
func newEnableWhenEvaluator() contract.EnableWhenEvaluator {
	return EvaluateEnableWhen
}

// promptHandlersParams 汇总 prompt RPC handler 装配需要的 store 和可选协作者。
type promptHandlersParams struct {
	fx.In

	Store      Store
	Builtin    contract.BuiltinPromptRegistry `optional:"true"`
	Dream      contract.DreamExecutor         `optional:"true"`
	Sections   contract.SectionInvalidator    `optional:"true"`
	Dispatcher *event.Dispatcher              `optional:"true"`
}

// registerPromptHandlers 用 fx 参数创建 prompt RPC handler map。
func registerPromptHandlers(params promptHandlersParams) platformrpc.HandlerMapResult {
	return buildPromptHandlersWithService(
		newPromptServiceWithBuiltin(params.Store, params.Builtin, params.Sections),
		params.Store,
		params.Builtin,
		params.Sections,
		params.Dream,
		params.Dispatcher,
	)
}

// ServiceFxParams 收集 prompt Service 的 fx 依赖，包含可选偏好存储和共享文件读取器。
type ServiceFxParams struct {
	fx.In
	Cfg             *Config
	Logger          *slog.Logger           `optional:"true"`
	Prefs           PreferenceReader       `optional:"true"`
	SharedFiles     SharedFileReader       `optional:"true"`
	DisabledToolsFn DisabledBuiltinToolsFn `optional:"true"`
}

// NewServiceFx 是 fx 使用的 prompt Service 构造函数，负责注入可选配置来源。
func NewServiceFx(p ServiceFxParams) Service {
	opts := []ServiceOption{
		WithPromptHintSources(p.Prefs, p.SharedFiles),
	}
	if p.DisabledToolsFn != nil {
		opts = append(opts, WithDisabledBuiltinToolsFn(p.DisabledToolsFn))
	}
	return NewService(p.Cfg, p.Logger, opts...)
}

type promptHandlerDeps struct {
	store              Store
	intentStore        promptintent.Store
	sectionInvalidator contract.SectionInvalidator
	dream              contract.DreamExecutor
	builtin            contract.BuiltinPromptRegistry
	emitPromptsChanged func(uidto.UIPromptsChanged)
}

// collectPromptHandlerDeps 在 assembly 边界把 fx/test 依赖转换成本包 port。
func collectPromptHandlerDeps(deps []any) promptHandlerDeps {
	var out promptHandlerDeps
	for _, dep := range deps {
		out.applyPromptHandlerDep(dep)
	}
	if out.intentStore == nil && out.store != nil {
		out.intentStore = promptIntentStoreAdapter{store: out.store}
	}
	return out
}

// applyPromptHandlerDep 按依赖实际类型填充 handler 装配参数。
func (d *promptHandlerDeps) applyPromptHandlerDep(dep any) {
	switch value := dep.(type) {
	case nil:
	case Store:
		d.store = value
	case promptintent.Store:
		d.intentStore = value
	case contract.SectionInvalidator:
		d.sectionInvalidator = value
	case contract.DreamExecutor:
		d.dream = value
	case contract.BuiltinPromptRegistry:
		d.builtin = value
	case *event.Dispatcher:
		if value != nil {
			d.emitPromptsChanged = contract.NewEmitter[uidto.UIPromptsChanged](value)
		}
	default:
		// archguard:ignore panic_count -- unknown handler deps are assembly programming errors and must fail fast.
		panic(fmt.Sprintf("unsupported prompt handler dependency %T", dep))
	}
}

type promptIntentStoreAdapter struct {
	store Store
}

// List 将 prompt 模块模板列表转换为 intent 子包 DTO。
func (a promptIntentStoreAdapter) List(ctx context.Context, filter promptintent.ListFilter) ([]promptintent.PromptTemplate, error) {
	items, err := a.store.List(ctx, ListFilter{
		AgentKey: filter.AgentKey,
		Keyword:  filter.Keyword,
		CWD:      filter.CWD,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return promptIntentTemplatesFromPrompt(items), nil
}

// WithTx 保持父 store 事务边界，并向 intent 子包提供事务内 port。
func (a promptIntentStoreAdapter) WithTx(ctx context.Context, fn func(txStore promptintent.Store) error) error {
	return a.store.WithTx(ctx, func(txStore Store) error {
		return fn(promptIntentStoreAdapter{store: txStore})
	})
}

// Get 读取单个模板并转换为 intent 子包 DTO。
func (a promptIntentStoreAdapter) Get(ctx context.Context, promptKey string) (*promptintent.PromptTemplate, error) {
	template, err := a.store.Get(ctx, promptKey)
	if err != nil {
		return nil, err
	}
	return promptIntentTemplatePtrFromPrompt(template), nil
}

// InsertVersion 将 intent 版本快照转换后写入父 store。
func (a promptIntentStoreAdapter) InsertVersion(ctx context.Context, version promptintent.PromptTemplateVersion) (int64, error) {
	return a.store.InsertVersion(ctx, promptTemplateVersionFromIntent(version))
}

// CreatePromptTemplate 将 intent 模板转换后写入父 store。
func (a promptIntentStoreAdapter) CreatePromptTemplate(
	ctx context.Context,
	template promptintent.PromptTemplate,
) (*promptintent.PromptTemplate, error) {
	saved, err := a.store.CreatePromptTemplate(ctx, promptTemplateFromIntent(template))
	if err != nil {
		return nil, err
	}
	return promptIntentTemplatePtrFromPrompt(saved), nil
}

// ListSectionsByTemplateID 读取单模板 sections 并转换为 intent 子包 DTO。
func (a promptIntentStoreAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]promptintent.PromptTemplateSection, error) {
	sections, err := a.store.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	return promptIntentTemplateSectionsFromPrompt(sections), nil
}

// ListSectionsByTemplateIDs 批量读取 sections 并转换为 intent 子包 DTO。
func (a promptIntentStoreAdapter) ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]promptintent.PromptTemplateSection, error) {
	sections, err := a.store.ListSectionsByTemplateIDs(ctx, templateIDs)
	if err != nil {
		return nil, err
	}
	return promptIntentTemplateSectionsFromPrompt(sections), nil
}

// ListDefaultRuleSections 读取当前 cwd 的默认规则 sections。
func (a promptIntentStoreAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]promptintent.PromptTemplateSection, error) {
	sections, err := a.store.ListDefaultRuleSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return promptIntentTemplateSectionsFromPrompt(sections), nil
}

// UpsertSection 将 intent section 转换后写入父 store。
func (a promptIntentStoreAdapter) UpsertSection(
	ctx context.Context,
	section promptintent.PromptTemplateSection,
) (*promptintent.PromptTemplateSection, error) {
	saved, err := a.store.UpsertSection(ctx, promptTemplateSectionFromIntent(section))
	if err != nil {
		return nil, err
	}
	return promptIntentTemplateSectionPtrFromPrompt(saved), nil
}

// UpsertRecallTopicTargetInCWD 维护 intent recall topic 的 cwd 内索引。
func (a promptIntentStoreAdapter) UpsertRecallTopicTargetInCWD(
	ctx context.Context,
	cwd, topic string,
	templateID int64,
	sectionKey string,
) error {
	return a.store.UpsertRecallTopicTargetInCWD(ctx, cwd, topic, templateID, sectionKey)
}

func promptIntentTemplatesFromPrompt(items []Template) []promptintent.PromptTemplate {
	return promptConvertSlice[promptintent.PromptTemplate](items)
}

func promptIntentTemplatePtrFromPrompt(template *Template) *promptintent.PromptTemplate {
	return promptConvertPtr[promptintent.PromptTemplate](template)
}

func promptTemplateFromIntent(template promptintent.PromptTemplate) Template {
	return promptConvertStruct[Template](template)
}

func promptIntentTemplateSectionsFromPrompt(items []TemplateSection) []promptintent.PromptTemplateSection {
	return promptConvertSlice[promptintent.PromptTemplateSection](items)
}

func promptIntentTemplateSectionPtrFromPrompt(section *TemplateSection) *promptintent.PromptTemplateSection {
	return promptConvertPtr[promptintent.PromptTemplateSection](section)
}

func promptTemplateSectionFromIntent(section promptintent.PromptTemplateSection) TemplateSection {
	return promptConvertStruct[TemplateSection](section)
}

func promptTemplateVersionFromIntent(version promptintent.PromptTemplateVersion) TemplateVersion {
	return promptConvertStruct[TemplateVersion](version)
}

func promptIntentDraftsFromPrompt(items []IntentDraft) []promptintent.PromptIntentDraft {
	return promptConvertSlice[promptintent.PromptIntentDraft](items)
}

func promptIntentDraftPtrFromPrompt(draft *IntentDraft) *promptintent.PromptIntentDraft {
	return promptConvertPtr[promptintent.PromptIntentDraft](draft)
}

func promptIntentDraftFromIntent(draft promptintent.PromptIntentDraft) IntentDraft {
	return promptConvertStruct[IntentDraft](draft)
}

func promptConvertSlice[Out any, In any](items []In) []Out {
	out := make([]Out, 0, len(items))
	for _, item := range items {
		out = append(out, promptConvertStruct[Out](item))
	}
	return out
}

func promptConvertPtr[Out any, In any](in *In) *Out {
	if in == nil {
		return nil
	}
	out := promptConvertStruct[Out](*in)
	return &out
}

func promptConvertStruct[Out any, In any](in In) Out {
	var out Out
	promptCopyStructFields(reflect.ValueOf(in), reflect.ValueOf(&out).Elem())
	return out
}

// promptCopyStructFields 按同名字段做 adapter DTO 转换，字段缺失或类型不匹配会立即暴露。
func promptCopyStructFields(src, dst reflect.Value) {
	src, dst = promptDerefStruct(src), promptDerefStruct(dst)
	promptRequireSameFieldSet(src, dst)
	for i := 0; i < dst.NumField(); i++ {
		promptSetConvertedField(src.FieldByName(dst.Type().Field(i).Name), dst.Field(i))
	}
}

func promptDerefStruct(value reflect.Value) reflect.Value {
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		// archguard:ignore panic_count -- adapter DTO conversion must only receive structs.
		panic(fmt.Sprintf("prompt adapter expected struct, got %s", value.Kind()))
	}
	return value
}

func promptRequireSameFieldSet(src, dst reflect.Value) {
	for i := 0; i < src.NumField(); i++ {
		if !dst.FieldByName(src.Type().Field(i).Name).IsValid() {
			// archguard:ignore panic_count -- DTO drift must fail fast at the adapter boundary.
			panic(fmt.Sprintf("prompt adapter destination missing field %s", src.Type().Field(i).Name))
		}
	}
	for i := 0; i < dst.NumField(); i++ {
		if !src.FieldByName(dst.Type().Field(i).Name).IsValid() {
			// archguard:ignore panic_count -- DTO drift must fail fast at the adapter boundary.
			panic(fmt.Sprintf("prompt adapter source missing field %s", dst.Type().Field(i).Name))
		}
	}
}

func promptSetConvertedField(src, dst reflect.Value) {
	if src.Type().AssignableTo(dst.Type()) {
		dst.Set(promptCloneBytesIfNeeded(src, dst.Type()))
		return
	}
	if src.Type().ConvertibleTo(dst.Type()) {
		dst.Set(promptCloneBytesIfNeeded(src.Convert(dst.Type()), dst.Type()))
		return
	}
	// archguard:ignore panic_count -- DTO field type drift must fail fast at the adapter boundary.
	panic(fmt.Sprintf("prompt adapter cannot assign %s to %s", src.Type(), dst.Type()))
}

func promptCloneBytesIfNeeded(value reflect.Value, target reflect.Type) reflect.Value {
	if value.Kind() != reflect.Slice || value.Type().Elem().Kind() != reflect.Uint8 {
		return value
	}
	if value.IsNil() {
		return reflect.Zero(target)
	}
	copyValue := reflect.MakeSlice(target, value.Len(), value.Len())
	reflect.Copy(copyValue, value)
	return copyValue
}
