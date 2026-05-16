package skill

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"
	"go.uber.org/fx"

	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
)

// TODO(P7): event-driven auto-match. The current skills/match/preview RPC
// is the only trigger; we still need a thread.Started subscriber that
// performs auto-match + binds the result onto the session at runtime.
var Module = fx.Module("skill",
	fx.Provide(
		fx.Annotate(
			newService,
			fx.As(new(Service)),
			fx.As(new(contract.SkillMirrorReconciler)),
		),
		ProvideSkillLister,
		ProvideSkillCatalogSource,
		ProvideSkillHydrationSource,
	),
	fx.Provide(NewSkillHandlers),
)

type serviceDeps struct {
	fx.In

	Config          *contract.Config
	Dispatcher      *event.Dispatcher
	DisclosureTiers contract.SkillDisclosureTierSource `optional:"true"`
	CandidateStore  skillcandidate.Store               `optional:"true"`
	AuditStore      auditstore.Store
}

func newService(deps serviceDeps) *service {
	projectRoot := ""
	if deps.Config != nil {
		projectRoot = strings.TrimSpace(deps.Config.ProjectRoot)
	}
	svc := NewService(projectRoot).(*service)
	svc.bindDispatcher(deps.Dispatcher)
	svc.disclosureTiers = deps.DisclosureTiers
	if deps.CandidateStore != nil {
		svc.candidateStore = deps.CandidateStore
	}
	svc.auditStore = deps.AuditStore
	return svc
}

func ProvideSkillLister(svc Service) SkillLister { return svc }

func ProvideSkillCatalogSource(svc Service) SkillCatalogSource { return svc }

func ProvideSkillHydrationSource(svc Service) SkillHydrationSource { return svc }
