package skill

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// Module wires skill catalog, mirror, inventory, and RPC handlers.
// Skill matching currently runs on demand through skills/match/preview; runtime
// session binding waits for the provider context to expose the required state.
var Module = fx.Module("skill",
	fx.Provide(
		fx.Annotate(
			newService,
			fx.As(new(Service)),
			fx.As(new(contract.SkillMirrorReconciler)),
		),
		ProvideSkillLister,
		ProvideSkillInventoryLister,
		ProvideSkillCatalogSource,
		ProvideSkillHydrationSource,
	),
	fx.Provide(NewSkillHandlers),
	fx.Invoke(runBuiltinSkillSeed),
)

type serviceDeps struct {
	fx.In

	Config     *contract.Config
	Dispatcher *event.Dispatcher
	AuditStore contract.AuditLogStore
}

type skillHandlerDeps struct {
	fx.In

	Service       Service
	DreamExecutor contract.DreamExecutor `optional:"true"`
}

func newService(deps serviceDeps) *service {
	projectRoot := ""
	if deps.Config != nil {
		projectRoot = strings.TrimSpace(deps.Config.ProjectRoot)
	}
	svc := NewService(projectRoot).(*service)
	svc.bindDispatcher(deps.Dispatcher)
	svc.auditStore = deps.AuditStore
	return svc
}

// ProvideSkillLister 提供技能lister。
func ProvideSkillLister(svc Service) SkillLister { return svc }

// ProvideSkillInventoryLister 提供技能inventorylister。
func ProvideSkillInventoryLister(svc Service) SkillInventoryLister { return svc }

// ProvideSkillCatalogSource 提供技能catalogsource。
func ProvideSkillCatalogSource(svc Service) SkillCatalogSource { return svc }

// ProvideSkillHydrationSource 提供技能hydrationsource。
func ProvideSkillHydrationSource(svc Service) SkillHydrationSource { return svc }

func runBuiltinSkillSeed(lc fx.Lifecycle, svc Service) {
	if seeder, ok := svc.(*service); ok && seeder != nil {
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				_, err := seedBuiltInSkills(seeder.resolvedSuperDolphinHome())
				return err
			},
		})
	}
}

func seedBuiltInSkills(superDolphinHome string) (int, error) {
	if strings.TrimSpace(superDolphinHome) == "" {
		return 0, fmt.Errorf("super dolphin home is required")
	}
	return 0, nil
}

// seedOneBuiltInSkill 在技能处理seedonebuilt。
// writeBuiltInSkillFileIfMissing 在技能文件ifmissing写入built。
