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
		group.Go(func() error {
			return load(groupCtx)
		})
	}
	return group.Wait()
}

func (s *service) dashboardPageLoaders(out *DashboardPage, page string) []dashboardPageLoader {
	descriptor, ok := s.pageRegistry(out)[page]
	if !ok {
		return nil
	}
	return descriptor.loaders
}

func (s *service) listDashboardTaskTraces(ctx context.Context) ([]tasktracestore.TaskTrace, error) {
	return safeList(s.taskTraces != nil, func() ([]tasktracestore.TaskTrace, error) {
		return s.taskTraces.List(ctx, tasktracestore.ListFilter{Limit: dashboardPageDefaultLimit})
	})
}

func (s *service) listDashboardSkills(ctx context.Context) ([]skillmodule.SkillInfo, error) {
	return safeList(s.skills != nil, func() ([]skillmodule.SkillInfo, error) {
		return s.skills.ListSkills(ctx)
	})
}

func (s *service) listDashboardCommandCards(ctx context.Context) ([]commandcardstore.CommandCard, error) {
	return safeList(s.commandCards != nil, func() ([]commandcardstore.CommandCard, error) {
		return s.commandCards.List(ctx, commandcardstore.ListFilter{Limit: dashboardPageDefaultLimit})
	})
}

func (s *service) listDashboardPrompts(ctx context.Context) ([]promptstore.PromptTemplate, error) {
	return safeList(s.prompts != nil, func() ([]promptstore.PromptTemplate, error) {
		return s.prompts.List(ctx, promptstore.ListFilter{Limit: dashboardPageDefaultLimit})
	})
}

func (s *service) listDashboardMemory(ctx context.Context) ([]sharedfilestore.SharedFile, error) {
	return safeList(s.sharedFiles != nil, func() ([]sharedfilestore.SharedFile, error) {
		return s.sharedFiles.List(ctx, sharedfilestore.ListFilter{Limit: dashboardMemoryLimit})
	})
}
