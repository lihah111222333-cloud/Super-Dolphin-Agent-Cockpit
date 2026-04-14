package prompt

import (
	"log/slog"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"golang.org/x/sync/singleflight"
)

type PromptRegistry interface {
	RegisterSection(section PromptSection) error
	RegisterDynamicProvider(provider DynamicSectionProvider) error
	UnregisterDynamicProvider(name string) bool
	Sections() []PromptSection
}

type Registry = PromptRegistry

type PromptAssemblyService = contract.PromptAssemblyService

type AssemblyService = PromptAssemblyService

type Service interface {
	PromptRegistry
	contract.PromptAssemblyService
	SectionInvalidator
	RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error
	Config() Config
}

type service struct {
	cfg              *Config
	logger           *slog.Logger
	registry         *SectionRegistry
	cache            *sectionCache
	userContextCache *userContextCache
	flight           singleflight.Group

	dynamicMu sync.RWMutex
	dynamic   map[string]DynamicSectionProvider

	claudeMdProvider contract.ClaudeMdSourceProvider
}

var _ contract.PromptAssemblyService = (*service)(nil)

func NewService(cfg *Config, logger *slog.Logger) Service {
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
	svc.registerBuiltInSections()
	mustRegisterDynamicProvider(svc, SessionGuidanceProvider{})
	mustRegisterDynamicProvider(svc, EnvInfoProvider{})
	mustRegisterDynamicProvider(svc, LanguageProvider{})
	mustRegisterDynamicProvider(svc, MCPInstructionsProvider{})
	mustRegisterDynamicProvider(svc, OutputStyleProvider{})
	mustRegisterDynamicProvider(svc, ScratchpadProvider{})
	mustRegisterDynamicProvider(svc, FRCStubProvider{})
	mustRegisterDynamicProvider(svc, SummarizeToolResultsProvider{})
	mustRegisterDynamicProvider(svc, NumericLengthAnchorsStubProvider{})
	mustRegisterDynamicProvider(svc, TokenBudgetStubProvider{})
	mustRegisterDynamicProvider(svc, BriefStubProvider{})
	mustRegisterDynamicProvider(svc, AntModelOverrideStubProvider{})
	return svc
}

func AsPromptRegistry(svc Service) PromptRegistry {
	return svc
}

func AsPromptAssemblyService(svc Service) contract.PromptAssemblyService {
	return svc
}

func (s *service) Config() Config {
	if s == nil || s.cfg == nil {
		return Config{}
	}
	return *s.cfg
}

func (s *service) RegisterSection(section PromptSection) error {
	return s.registry.Register(section)
}

func (s *service) RegisterClaudeMdSourceProvider(provider contract.ClaudeMdSourceProvider) error {
	s.claudeMdProvider = provider
	if s.userContextCache != nil {
		s.userContextCache.InvalidateAll()
	}
	return nil
}

func (s *service) Sections() []PromptSection {
	return s.registry.Sections()
}

func (s *service) registerBuiltInSections() {
	for _, section := range StaticSections() {
		mustRegisterSection(s.registry, section)
	}
	for _, section := range s.dynamicSlotSections() {
		mustRegisterSection(s.registry, section)
	}
}

func mustRegisterSection(registry *SectionRegistry, section PromptSection) {
	if err := registry.Register(section); err != nil {
		panic(err)
	}
}

func mustRegisterDynamicProvider(svc *service, provider DynamicSectionProvider) {
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		panic(err)
	}
}
