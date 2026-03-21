package dashboard

import (
	"context"
	"strings"

	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	taskackstore "github.com/anthropic-ai/super-agent-v3/internal/store/taskack"
	taskdagstore "github.com/anthropic-ai/super-agent-v3/internal/store/taskdag"
	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
)

const (
	dashboardPageDefaultLimit = 100
	dashboardMemoryLimit      = 500
)

type DashboardPage struct {
	Agents       []AgentOverview              `json:"agents"`
	Dags         []taskdagstore.DAG           `json:"dags"`
	TaskAcks     []taskackstore.TaskAck       `json:"taskAcks"`
	TaskTraces   []tasktracestore.TaskTrace   `json:"taskTraces"`
	Skills       []skillmodule.SkillInfo      `json:"skills"`
	CommandCards []commandcardstore.CommandCard `json:"commandCards"`
	Prompts      []promptstore.PromptTemplate `json:"prompts"`
	Memory       []sharedfilestore.SharedFile `json:"memory"`
}

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
		Dags:         []taskdagstore.DAG{},
		TaskAcks:     []taskackstore.TaskAck{},
		TaskTraces:   []tasktracestore.TaskTrace{},
		Skills:       []skillmodule.SkillInfo{},
		CommandCards: []commandcardstore.CommandCard{},
		Prompts:      []promptstore.PromptTemplate{},
		Memory:       []sharedfilestore.SharedFile{},
	}
}

func (s *service) populateDashboardPage(ctx context.Context, out *DashboardPage, page string) error {
	switch strings.ToLower(strings.TrimSpace(page)) {
	case "agents":
		items, err := s.listAgents(ctx)
		out.Agents = items
		return err
	case "dags":
		items, err := s.listDashboardDAGs(ctx)
		out.Dags = items
		return err
	case "tasks":
		acks, err := s.listDashboardTaskAcks(ctx)
		if err != nil {
			return err
		}
		traces, err := s.listDashboardTaskTraces(ctx)
		out.TaskAcks, out.TaskTraces = acks, traces
		return err
	case "skills":
		items, err := s.listDashboardSkills(ctx)
		out.Skills = items
		return err
	case "commands":
		cards, err := s.listDashboardCommandCards(ctx)
		if err != nil {
			return err
		}
		prompts, err := s.listDashboardPrompts(ctx)
		out.CommandCards, out.Prompts = cards, prompts
		return err
	case "memory":
		items, err := s.listDashboardMemory(ctx)
		out.Memory = items
		return err
	default:
		return nil
	}
}

func (s *service) listDashboardDAGs(ctx context.Context) ([]taskdagstore.DAG, error) {
	if s.taskDAGs == nil {
		return []taskdagstore.DAG{}, nil
	}
	return s.taskDAGs.ListDAGs(ctx, taskdagstore.ListDAGsFilter{Limit: dashboardPageDefaultLimit})
}

func (s *service) listDashboardTaskAcks(ctx context.Context) ([]taskackstore.TaskAck, error) {
	if s.taskAcks == nil {
		return []taskackstore.TaskAck{}, nil
	}
	return s.taskAcks.List(ctx, taskackstore.ListFilter{Limit: dashboardPageDefaultLimit})
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
