package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
	"golang.org/x/sync/errgroup"
)

const (
	dashboardPageDefaultLimit          = 100
	dashboardMemoryLimit               = 500
	dashboardFinalOutputDAGLimit       = 20
	dashboardFinalOutputRunLimit int32 = 3
)

type DashboardPage struct {
	Agents          []AgentOverview                `json:"agents"`
	DAGs            []contract.DAGSummary          `json:"dags"`
	TaskTraces      []tasktracestore.TaskTrace     `json:"taskTraces"`
	Skills          []contract.SkillInfo           `json:"skills"`
	CommandCards    []commandcardstore.CommandCard `json:"commandCards"`
	Prompts         []promptstore.PromptTemplate   `json:"prompts"`
	Memory          []sharedfilestore.SharedFile   `json:"memory"`
	FinalOutputRefs []FinalOutputRef               `json:"finalOutputRefs"`
}

type dashboardPageLoader func(context.Context) error

type finalOutputEnvelope struct {
	FinalOutput json.RawMessage `json:"final_output"`
}

type finalOutputPayload struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	SourceNodeKey string `json:"source_node_key"`
	SharedFile    *struct {
		Path string `json:"path"`
	} `json:"sharedfile"`
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
		Agents:          []AgentOverview{},
		DAGs:            []contract.DAGSummary{},
		TaskTraces:      []tasktracestore.TaskTrace{},
		Skills:          []contract.SkillInfo{},
		CommandCards:    []commandcardstore.CommandCard{},
		Prompts:         []promptstore.PromptTemplate{},
		Memory:          []sharedfilestore.SharedFile{},
		FinalOutputRefs: []FinalOutputRef{},
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
	case "dags":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardDAGs(ctx, out) },
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

func (s *service) populateDashboardDAGs(ctx context.Context, out *DashboardPage) error {
	if s.orchestration == nil {
		return nil
	}
	items, err := s.ListDAGs(ctx, contract.ListDAGsFilter{Limit: dashboardPageDefaultLimit})
	if err != nil {
		return err
	}
	if items == nil {
		items = []contract.DAGSummary{}
	}
	out.DAGs = items
	return nil
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
	if err != nil {
		return err
	}
	out.Memory = items
	refs, err := s.listDashboardFinalOutputRefs(ctx)
	out.FinalOutputRefs = refs
	return err
}

func (s *service) listDashboardTaskTraces(ctx context.Context) ([]tasktracestore.TaskTrace, error) {
	return safeList(s.taskTraces != nil, func() ([]tasktracestore.TaskTrace, error) {
		return s.taskTraces.List(ctx, tasktracestore.ListFilter{Limit: dashboardPageDefaultLimit})
	})
}

func (s *service) listDashboardSkills(ctx context.Context) ([]contract.SkillInfo, error) {
	cwd := dashboardPromptScopeCWDFromContext(ctx)
	return safeList(s.skills != nil, func() ([]contract.SkillInfo, error) {
		return s.skills.ListSkills(contract.WithSkillCWD(ctx, cwd))
	})
}

func (s *service) listDashboardCommandCards(ctx context.Context) ([]commandcardstore.CommandCard, error) {
	return safeList(s.commandCards != nil, func() ([]commandcardstore.CommandCard, error) {
		return s.commandCards.List(ctx, commandcardstore.ListFilter{Limit: dashboardPageDefaultLimit})
	})
}

func (s *service) listDashboardPrompts(ctx context.Context) ([]promptstore.PromptTemplate, error) {
	cwd := dashboardPromptScopeCWDFromContext(ctx)
	return safeList(s.prompts != nil, func() ([]promptstore.PromptTemplate, error) {
		items, err := s.prompts.List(ctx, promptstore.ListFilter{CWD: cwd, Limit: dashboardPageDefaultLimit})
		if err != nil {
			return nil, err
		}
		return filterDashboardPromptsByCWD(items, cwd), nil
	})
}

func filterDashboardPromptsByCWD(items []promptstore.PromptTemplate, cwd string) []promptstore.PromptTemplate {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return items
	}
	filtered := make([]promptstore.PromptTemplate, 0, len(items))
	for _, item := range items {
		if dashboardPromptVisibleForCWD(item, requestScope) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func dashboardPromptVisibleForCWD(template promptstore.PromptTemplate, cwd string) bool {
	storedScope := dashboardPromptScopeFromTags(template.Tags)
	return storedScope == "" || storedScope == strings.TrimSpace(cwd)
}

func dashboardPromptScopeFromTags(raw json.RawMessage) string {
	for _, tag := range dashboardPromptTags(raw) {
		if value, ok := strings.CutPrefix(tag, "scope.cwd:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func dashboardPromptTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return []string{}
	}
	return tags
}

func (s *service) listDashboardMemory(ctx context.Context) ([]sharedfilestore.SharedFile, error) {
	return safeList(s.sharedFiles != nil, func() ([]sharedfilestore.SharedFile, error) {
		return s.sharedFiles.List(ctx, sharedfilestore.ListFilter{Limit: dashboardMemoryLimit})
	})
}

func (s *service) listDashboardFinalOutputRefs(ctx context.Context) ([]FinalOutputRef, error) {
	if s.orchestration == nil {
		return []FinalOutputRef{}, nil
	}
	dags, err := s.ListDAGs(ctx, contract.ListDAGsFilter{Limit: dashboardFinalOutputDAGLimit})
	if err != nil {
		return nil, err
	}
	refs := make([]FinalOutputRef, 0)
	seen := make(map[string]struct{})
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, dag := range dags {
		dag := dag
		group.Go(func() error {
			runs, runErr := s.ListDAGRuns(groupCtx, dag.DagKey, dashboardFinalOutputRunLimit)
			if runErr != nil {
				return runErr
			}
			for _, run := range runs {
				ref, ok := finalOutputRefFromRun(run)
				if !ok {
					continue
				}
				mu.Lock()
				if _, exists := seen[ref.Path]; !exists {
					seen[ref.Path] = struct{}{}
					refs = append(refs, ref)
				}
				mu.Unlock()
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return refs, nil
}

func finalOutputRefFromRun(run contract.Run) (FinalOutputRef, bool) {
	finalOutput, ok := finalOutputFromMetadata(run.Metadata)
	if !ok {
		return FinalOutputRef{}, false
	}
	output, path, ok := finalOutputFilePayload(finalOutput)
	if !ok {
		return FinalOutputRef{}, false
	}
	return FinalOutputRef{
		Path:          path,
		RunKey:        strings.TrimSpace(run.RunKey),
		DagKey:        strings.TrimSpace(run.DagKey),
		SourceNodeKey: strings.TrimSpace(output.SourceNodeKey),
		Kind:          "file",
	}, true
}

func finalOutputFromMetadata(metadataJSON json.RawMessage) (json.RawMessage, bool) {
	if isEmptyJSON(metadataJSON) {
		return nil, false
	}
	var metadata finalOutputEnvelope
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil || isEmptyJSON(metadata.FinalOutput) {
		return nil, false
	}
	return metadata.FinalOutput, true
}

func finalOutputFilePayload(finalOutput json.RawMessage) (finalOutputPayload, string, bool) {
	var output finalOutputPayload
	if err := json.Unmarshal(finalOutput, &output); err != nil {
		return finalOutputPayload{}, "", false
	}
	if kind := strings.TrimSpace(output.Kind); kind != "" && kind != "file" {
		return finalOutputPayload{}, "", false
	}
	path := finalOutputSharedFilePath(output)
	if path == "" {
		return finalOutputPayload{}, "", false
	}
	return output, path, true
}

func finalOutputSharedFilePath(output finalOutputPayload) string {
	path := strings.TrimSpace(output.Path)
	if path == "" && output.SharedFile != nil {
		path = strings.TrimSpace(output.SharedFile.Path)
	}
	return path
}

func isEmptyJSON(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null"
}
