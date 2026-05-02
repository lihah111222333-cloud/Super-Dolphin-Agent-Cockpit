package skill

import (
	"strings"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/fbsd"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
)

// TODO(P7): event-driven auto-match. The current skills/match/preview RPC
// is the only trigger; we still need a thread.Started subscriber that
// performs auto-match + binds the result onto the session at runtime.
var Module = fx.Module("skill",
	fx.Provide(
		newService,
		ProvideSkillLister,
		ProvideSkillCatalogSource,
		ProvideSkillHydrationSource,
	),
	fx.Provide(NewSkillHandlers),
	// P0b Step 5: late-bind the candidate review gate dependencies.
	// Both stores are optional so this Module keeps building when the
	// review gate is not provisioned (tests, early bootstrap).
	fx.Invoke(registerCandidateStores),
)

type serviceDeps struct {
	fx.In

	Config     *platformconfig.Config
	Dispatcher *event.Dispatcher
	Tracker    *fbsd.Tracker `optional:"true"`
}

func newService(deps serviceDeps) Service {
	projectRoot := ""
	if deps.Config != nil {
		projectRoot = strings.TrimSpace(deps.Config.ProjectRoot)
	}
	svc := NewService(projectRoot)
	if impl, ok := svc.(*service); ok {
		impl.bindDispatcher(deps.Dispatcher)
		impl.tracker = deps.Tracker
	}
	return svc
}

func ProvideSkillLister(svc Service) SkillLister { return svc }

func ProvideSkillCatalogSource(svc Service) SkillCatalogSource { return svc }

func ProvideSkillHydrationSource(svc Service) SkillHydrationSource { return svc }

// candidateStoreParams collects the optional review-gate dependencies.
// Marked optional so this Invoke does not force every fx graph (CLI
// utilities, integration tests) to provide them - the service simply
// exposes errCandidateStoreUnavailable until they are wired.
type candidateStoreParams struct {
	fx.In

	Service        Service
	CandidateStore skillcandidate.Store `optional:"true"`
	AuditStore     auditstore.Store     `optional:"true"`
}

// registerCandidateStores wires the candidate / audit stores onto the
// concrete *service via package-private setters. The setter pattern
// keeps NewService backwards-compatible (no signature change) and
// avoids leaking the internal stores into the Service interface.
func registerCandidateStores(p candidateStoreParams) {
	if p.Service == nil {
		return
	}
	type csSetter interface {
		setCandidateStore(skillcandidate.Store)
	}
	type asSetter interface {
		setAuditStore(auditstore.Store)
	}
	if cs, ok := p.Service.(csSetter); ok && p.CandidateStore != nil {
		cs.setCandidateStore(p.CandidateStore)
	}
	if as, ok := p.Service.(asSetter); ok && p.AuditStore != nil {
		as.setAuditStore(p.AuditStore)
	}
}
