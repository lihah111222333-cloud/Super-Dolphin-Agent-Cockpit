package skill

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/toolstore"
	"github.com/kelindar/event"
	"go.uber.org/fx"

	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
)

// Module 组装 skill 模块的 service、RPC handler 和启动期内置 skill 种子。
// skill 自动匹配当前只在 `skills/match/preview` 按需执行，运行期绑定等待 provider context 提供状态。
var Module = fx.Module("skill",
	fx.Provide(
		fx.Annotate(
			newService,
			fx.As(new(Service)),
			fx.As(new(contract.SkillMirrorReconciler)),
			fx.As(new(contract.SkillToolProvider)),
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
	AuditStore auditstore.Store
	DB         *sql.DB `optional:"true"`
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
	svc.skillTools = toolstore.New(deps.DB)
	return svc
}

// ProvideSkillLister 暴露只读 skill 列表接口给 prompt 和 provider 装配层。
func ProvideSkillLister(svc Service) SkillLister { return svc }

// ProvideSkillInventoryLister 暴露完整 inventory 读取接口给治理界面。
func ProvideSkillInventoryLister(svc Service) SkillInventoryLister { return svc }

// ProvideSkillCatalogSource 将 service 作为 catalog 来源注入跨模块 contract。
func ProvideSkillCatalogSource(svc Service) SkillCatalogSource { return svc }

// ProvideSkillHydrationSource 将 service 作为 skill 全文补水来源注入 provider。
func ProvideSkillHydrationSource(svc Service) SkillHydrationSource { return svc }

const builtInSkillRoot = "embedded_skills"

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

func listBuiltInSkillNames() ([]string, error) {
	entries, err := builtInSkillFS.ReadDir(builtInSkillRoot)
	if err != nil {
		return nil, fmt.Errorf("skill builtins: list embedded: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if name := entry.Name(); entry.IsDir() && builtInSkillExists(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func builtInSkillExists(name string) bool {
	_, err := builtInSkillFS.ReadFile(builtInSkillRoot + "/" + name + "/" + skillMainFile)
	return err == nil
}
