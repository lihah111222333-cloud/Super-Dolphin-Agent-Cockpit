package dashboard

import (
	"context"
	"strings"

	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
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
	CommandCards []commandcardstore.CommandCard `json:"commandCards"`
	Prompts      []promptstore.PromptTemplate   `json:"prompts"`
	Memory       []sharedfilestore.SharedFile   `json:"memory"`
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
		CommandCards: []commandcardstore.CommandCard{},
		Prompts:      []promptstore.PromptTemplate{},
		Memory:       []sharedfilestore.SharedFile{},
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

func (s *service) listDashboardCommandCards(ctx context.Context) ([]commandcardstore.CommandCard, error) {
	if s.commandCards == nil {
		return []commandcardstore.CommandCard{}, nil
	}
	return s.commandCards.List(ctx, commandcardstore.ListFilter{Limit: dashboardPageDefaultLimit})
}

func (s *service) listDashboardPrompts(ctx context.Context) ([]promptstore.PromptTemplate, error) {
	if s.prompts == nil {
		return []promptstore.PromptTemplate{}, nil
	}
	return s.prompts.List(ctx, promptstore.ListFilter{Limit: dashboardPageDefaultLimit})
}

func (s *service) listDashboardMemory(ctx context.Context) ([]sharedfilestore.SharedFile, error) {
	if s.sharedFiles == nil {
		return []sharedfilestore.SharedFile{}, nil
	}
	return s.sharedFiles.List(ctx, sharedfilestore.ListFilter{Limit: dashboardMemoryLimit})
}
