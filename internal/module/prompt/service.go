package prompt

import (
	"context"
	"log/slog"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// PromptRegistry 管理内置和动态 prompt section 的注册与枚举。
type PromptRegistry interface {
	RegisterSection(section PromptSection) error
	RegisterDynamicProvider(provider DynamicSectionProvider) error
	UnregisterDynamicProvider(name string) bool
	Sections() []PromptSection
}

// PromptAssemblyService 暴露 prompt 组装能力的跨模块契约别名。
type PromptAssemblyService = contract.PromptAssemblyService

// Service 聚合 prompt section 注册、组装、失效和 Claude.md 来源注入能力。
type Service interface {
	PromptRegistry
	contract.PromptAssemblyService
	SectionInvalidator
	RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error
	Config() Config
}

// service 接口断言确保 prompt 运行时满足跨模块 section 失效契约。
// 该契约要求并发安全；实现侧通过 cache mutex 和 dynamicMu 保护共享状态。
var _ contract.SectionInvalidator = (*service)(nil)
var _ contract.PromptAssemblyService = (*service)(nil)

// DisabledBuiltinToolsFn 返回用户在 UI 偏好中手动禁用的 builtin tool IDs。
// 该函数由上层注入，避免 prompt 包直接依赖 uistate 实现。
type DisabledBuiltinToolsFn func(ctx context.Context, cwd, provider string) ([]string, error)

// service 是 prompt 模块的运行时实现，持有 section 注册表、动态 provider 和缓存。
type service struct {
	cfg              *Config
	logger           *slog.Logger
	registry         *SectionRegistry
	cache            *sectionCache
	userContextCache *userContextCache
	claudeMdProvider contract.ClaudeMdSourceProvider
	flight           singleflight.Group

	prefs           PreferenceReader
	sharedFiles     SharedFileReader
	disabledToolsFn DisabledBuiltinToolsFn

	dynamicMu sync.RWMutex
	dynamic   map[string]DynamicSectionProvider
}

// ServiceOption 注入 prompt Service 的可选依赖，避免构造函数参数继续膨胀。
type ServiceOption func(*service)

// WithPromptHintSources 注入用于解析 LSP prompt hint 的偏好存储和共享文件读取器。
func WithPromptHintSources(prefs PreferenceReader, sharedFiles SharedFileReader) ServiceOption {
	return func(s *service) {
		s.prefs = prefs
		s.sharedFiles = sharedFiles
	}
}

// WithDisabledBuiltinToolsFn 注入 builtin tools 软过滤查询函数，避免 prompt 包直接依赖 uistate。
func WithDisabledBuiltinToolsFn(fn DisabledBuiltinToolsFn) ServiceOption {
	return func(s *service) {
		s.disabledToolsFn = fn
	}
}

// Config 返回当前配置快照；nil 配置时返回零值，保持调用方读路径无 panic。
func (s *service) Config() Config {
	if s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

// RegisterSection 注册单个 prompt section，重复名称由 registry 拒绝。
func (s *service) RegisterSection(section PromptSection) error {
	return s.registry.Register(section)
}

// RegisterClaudeMdSourceProvider 设置 Claude.md 来源 provider，并清空依赖该来源的 user context 缓存。
func (s *service) RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error {
	s.claudeMdProvider = provider
	if s.userContextCache != nil {
		s.userContextCache.InvalidateAll()
	}
	return nil
}

// Sections 返回注册表中的 section 快照。
func (s *service) Sections() []PromptSection {
	return s.registry.Sections()
}

// registerBuiltInSections 注册内置静态 section 和动态 slot section，启动期失败由 must* helper 直接暴露。
func (s *service) registerBuiltInSections() {
	if !parseBoolEnv(envDisableBuiltinStaticSections, false) {
		for _, section := range StaticSections() {
			mustRegisterSection(s.registry, section)
		}
	}
	for _, section := range s.dynamicSlotSections() {
		mustRegisterSection(s.registry, section)
	}
}

// mustRegisterSection 注册内置 section；启动不变量失败时 panic，避免系统带缺失 section 继续运行。
func mustRegisterSection(registry *SectionRegistry, section PromptSection) {
	if err := registry.Register(section); err != nil {
		// archguard:ignore panic_count -- builtin prompt section registration is a startup invariant.
		panic(err)
	}
}

// mustRegisterDynamicProvider 注册内置动态 provider；失败说明启动配置冲突，必须立即暴露。
func mustRegisterDynamicProvider(svc *service, provider DynamicSectionProvider) {
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		// archguard:ignore panic_count -- builtin dynamic provider registration is a startup invariant.
		panic(err)
	}
}

// NewService 创建 prompt 组装服务并注册全部内置动态 provider。
func NewService(cfg *Config, logger *slog.Logger, opts ...ServiceOption) Service {
	if cfg == nil {
		cfg = &Config{}
	}
	svc := &service{
		cfg:              cfg,
		logger:           logger,
		registry:         NewSectionRegistry(),
		cache:            newSectionCache(),
		userContextCache: newUserContextCache(),
		dynamic:          map[string]DynamicSectionProvider{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	svc.registerBuiltInSections()
	mustRegisterDynamicProvider(svc, SessionGuidanceProvider{})
	mustRegisterDynamicProvider(svc, newEnvInfoProvider())
	mustRegisterDynamicProvider(svc, LanguageProvider{})
	mustRegisterDynamicProvider(svc, newMCPInstructionsProvider())
	mustRegisterDynamicProvider(svc, OutputStyleProvider{})
	mustRegisterDynamicProvider(svc, ScratchpadProvider{})
	mustRegisterDynamicProvider(svc, FRCProvider{})
	mustRegisterDynamicProvider(svc, SummarizeToolResultsProvider{})
	mustRegisterDynamicProvider(svc, NumericLengthAnchorsProvider{})
	mustRegisterDynamicProvider(svc, TokenBudgetProvider{})
	mustRegisterDynamicProvider(svc, BriefProvider{})
	return svc
}

// AsPromptRegistry 将聚合 Service 暴露为 PromptRegistry，供 fx 按窄接口注入。
func AsPromptRegistry(svc Service) PromptRegistry {
	return svc
}

// AsPromptAssemblyService 将聚合 Service 暴露为 prompt 组装契约。
func AsPromptAssemblyService(svc Service) contract.PromptAssemblyService {
	return svc
}

// AsDynamicSectionRegistrar 将聚合 Service 暴露为动态 section 注册契约。
func AsDynamicSectionRegistrar(svc Service) contract.DynamicSectionRegistrar {
	return svc
}
