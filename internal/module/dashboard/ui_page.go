package dashboard

import (
	"context"
	"strings"

	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
	"golang.org/x/sync/errgroup"
)

const (
	dashboardPageDefaultLimit = 100
	dashboardMemoryLimit      = 500
)

type DashboardPage struct {
	Agents       []AgentOverview                `json:"agents"`
	TaskTraces   []tasktracestore.TaskTrace     `json:"taskTraces"`
	Skills       []skillmodule.SkillInfo        `json:"skills"`
	CommandCards []sqlc.ListCommandCardsRow     `json:"commandCards"`
	Prompts      []sqlc.ListPromptTemplatesRow  `json:"prompts"`
	Memory       []sqlc.SharedFile              `json:"memory"`
}

type dashboardPageLoader func(context.Context) error

func (s *service) GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error) {
	out := newDashboardPage()
	if err := s.populateDashboardPage(ctx, out, page); err != nil {
		return nil, err
	}
	return out, nil
}

func newDashboardPage() *DashboardPage {
	return &DashboardPage{
		Agents:       []AgentOverview{},
		TaskTraces:   []tasktracestore.TaskTrace{},
		Skills:       []skillmodule.SkillInfo{},
		CommandCards: []sqlc.ListCommandCardsRow{},
		Prompts:      []sqlc.ListPromptTemplatesRow{},
		Memory:       []sqlc.SharedFile{},
	}
}

func (s *service) populateDashboardPage(ctx context.Context, out *DashboardPage, page string) error {
	loaders := s.dashboardPageLoaders(out, strings.ToLower(strings.TrimSpace(page)))
	if len(loaders) == 0 {
		return nil
	}
	group, groupCtx := errgroup.WithContext(ctx)
	for _, load := range loaders {
		load := load
		group.Go(func() error {
			return load(groupCtx)
		})
	}
	return group.Wait()
}

func (s *service) dashboardPageLoaders(out *DashboardPage, page string) []dashboardPageLoader {
	switch page {
	case "agents":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardAgents(ctx, out) },
		}
	case "tasks":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardTaskTraces(ctx, out) },
		}
	case "skills":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardSkills(ctx, out) },
		}
	case "commands":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardCommandCards(ctx, out) },
			func(ctx context.Context) error { return s.populateDashboardPrompts(ctx, out) },
		}
	case "memory":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardMemory(ctx, out) },
		}
	default:
		return nil
	}
}

func (s *service) populateDashboardAgents(ctx context.Context, out *DashboardPage) error {
	items, err := s.listAgents(ctx)
	out.Agents = items
	return err
}

func (s *service) populateDashboardTaskTraces(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardTaskTraces(ctx)
	out.TaskTraces = items
	return err
}

func (s *service) populateDashboardSkills(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardSkills(ctx)
	out.Skills = items
	return err
}

func (s *service) populateDashboardCommandCards(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardCommandCards(ctx)
	out.CommandCards = items
	return err
}

func (s *service) populateDashboardPrompts(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardPrompts(ctx)
	out.Prompts = items
	return err
}

func (s *service) populateDashboardMemory(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardMemory(ctx)
	out.Memory = items
	return err
}

func (s *service) listDashboardTaskTraces(ctx context.Context) ([]tasktracestore.TaskTrace, error) {
	if s.taskTraces == nil {
		return []tasktracestore.TaskTrace{}, nil
	}
	return s.taskTraces.List(ctx, tasktracestore.ListFilter{Limit: dashboardPageDefaultLimit})
}

func (s *service) listDashboardSkills(ctx context.Context) ([]skillmodule.SkillInfo, error) {
	if s.skills == nil {
		return []skillmodule.SkillInfo{}, nil
	}
	return s.skills.ListSkills(ctx)
}

func (s *service) listDashboardCommandCards(ctx context.Context) ([]sqlc.ListCommandCardsRow, error) {
	if s.queries == nil {
		return []sqlc.ListCommandCardsRow{}, nil
	}
	return s.queries.ListCommandCards(ctx, sqlc.ListCommandCardsParams{Limit: dashboardPageDefaultLimit})
}

func (s *service) listDashboardPrompts(ctx context.Context) ([]sqlc.ListPromptTemplatesRow, error) {
	if s.queries == nil {
		return []sqlc.ListPromptTemplatesRow{}, nil
	}
	return s.queries.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{Limit: dashboardPageDefaultLimit})
}

func (s *service) listDashboardMemory(ctx context.Context) ([]sqlc.SharedFile, error) {
	if s.queries == nil {
		return []sqlc.SharedFile{}, nil
	}
	return s.queries.ListSharedFiles(ctx, sqlc.ListSharedFilesParams{Limit: dashboardMemoryLimit})
}
