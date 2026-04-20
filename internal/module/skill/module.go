package skill

import (
	"strings"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type serviceParams struct {
	fx.In

	Config     *platformconfig.Config      `optional:"true"`
	Dispatcher *event.Dispatcher           `optional:"true"`
	Sections   contract.SectionInvalidator `optional:"true"`
	Policy     SkillPolicy                 `optional:"true"`
	Metrics    SkillMetrics                `optional:"true"`
}

type skillCatalogPromptProviderParams struct {
	fx.In

	Registrar contract.DynamicSectionRegistrar `optional:"true"`
	Provider  *SkillCatalogProvider            `optional:"true"`
}

// TODO(P7): 接入事件驱动 auto-match。当前仅 skills/match/preview RPC 触发，
// 无运行时自动触发（如 thread 启动时自动匹配）。需要订阅 thread.Started 事件
// 并在回调中执行 auto-match + 绑定到 session。
var Module = fx.Module("skill",
	fx.Provide(newService),
	fx.Provide(NewSkillCatalogProvider),
	fx.Provide(NewSkillHandlers),
	fx.Invoke(registerSkillCatalogPromptProvider),
)

func newService(p serviceParams) Service {
	projectRoot := ""
	if p.Config != nil {
		projectRoot = strings.TrimSpace(p.Config.ProjectRoot)
	}
	policy := p.Policy
	if policy == nil {
		policy = NewDefaultSkillPolicy(p.Config)
	}
	metrics := p.Metrics
	if metrics == nil {
		metrics = NewNoopSkillMetrics()
	}
	svc := newServiceWithDeps(projectRoot, policy, metrics)
	if impl, ok := svc.(*service); ok {
		impl.sections = p.Sections
		impl.bindDispatcher(p.Dispatcher)
	}
	return svc
}

func registerSkillCatalogPromptProvider(p skillCatalogPromptProviderParams) error {
	if p.Registrar == nil || p.Provider == nil {
		return nil
	}
	return p.Registrar.RegisterDynamicProvider(p.Provider)
}
