package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"golang.org/x/sync/errgroup"
)

const (
	// 误判防护：dashboardPageDefaultLimit 是 dashboard DAG 列表的默认容量守卫。
	dashboardPageDefaultLimit          = 100
	dashboardMemoryLimit               = 500
	dashboardFinalOutputDAGLimit       = 20
	dashboardFinalOutputRunLimit int32 = 3
	// 误判防护：dashboardDAGLatestRunLookupLimit 配合 group.SetLimit 限制 latest run 并发。
	dashboardDAGLatestRunLookupLimit = 4
)

type DashboardPage struct {
	Agents              []AgentOverview                `json:"agents"`
	DAGs                []DashboardDAG                 `json:"dags"`
	Skills              []contract.SkillInfo           `json:"skills"`
	CommandCards        []commandcardstore.CommandCard `json:"commandCards"`
	Prompts             []promptstore.PromptTemplate   `json:"prompts"`
	Memory              []sharedfilestore.SharedFile   `json:"memory"`
	FinalOutputRefs     []FinalOutputRef               `json:"finalOutputRefs"`
	SharedFileRetention SharedFileRetention            `json:"sharedFileRetention"`
}

type dashboardPageLoader func(context.Context) error

type SharedFileRetention struct {
	Items                 []SharedFileRetentionItem `json:"items"`
	ProtectedCount        int                       `json:"protectedCount"`
	CleanupCandidateCount int                       `json:"cleanupCandidateCount"`
}

type SharedFileRetentionItem struct {
	Path             string          `json:"path"`
	Protected        bool            `json:"protected"`
	CleanupCandidate bool            `json:"cleanupCandidate"`
	Reason           string          `json:"reason,omitempty"`
	FinalOutput      *FinalOutputRef `json:"finalOutput,omitempty"`
}

// GetDashboardPage 读取dashboardpage。
func (s *service) GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error) {
	out := newDashboardPage()
	if err := s.populateDashboardPage(ctx, out, page); err != nil {
		return nil, err
	}
	return out, nil
}

func newDashboardPage() *DashboardPage {
	return &DashboardPage{
		Agents:              []AgentOverview{},
		DAGs:                []DashboardDAG{},
		Skills:              []contract.SkillInfo{},
		CommandCards:        []commandcardstore.CommandCard{},
		Prompts:             []promptstore.PromptTemplate{},
		Memory:              []sharedfilestore.SharedFile{},
		FinalOutputRefs:     []FinalOutputRef{},
		SharedFileRetention: SharedFileRetention{Items: []SharedFileRetentionItem{}},
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

// dashboardPageLoaders 处理dashboardpageloaders。
func (s *service) dashboardPageLoaders(out *DashboardPage, page string) []dashboardPageLoader {
	switch page {
	case "agents":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardAgents(ctx, out) },
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
	case "commandcards":
		return []dashboardPageLoader{
			func(ctx context.Context) error { return s.populateDashboardCommandCards(ctx, out) },
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

// populateDashboardDAGs 处理populatedashboarddags。
func (s *service) populateDashboardDAGs(ctx context.Context, out *DashboardPage) error {
	if !s.hasDAGSnapshotQueries() && s.effectiveDAGRuntime() == nil {
		return nil
	}
	// 误判防护：ListDAGs 使用 dashboardPageDefaultLimit，不做无界 dashboard 查询。
	items, err := s.ListDAGs(ctx, contract.ListDAGsFilter{Limit: dashboardPageDefaultLimit})
	if err != nil {
		return err
	}
	if items == nil {
		items = []contract.DAGSummary{}
	}
	var dags []DashboardDAG
	if s.hasDAGSnapshotQueries() {
		dags, err = s.buildDashboardDAGsFromSnapshot(ctx, items)
	} else {
		dags, err = s.buildDashboardDAGs(ctx, items)
	}
	if err != nil {
		return err
	}
	out.DAGs = dags
	return nil
}

func (s *service) buildDashboardDAGsFromSnapshot(ctx context.Context, items []contract.DAGSummary) ([]DashboardDAG, error) {
	out := make([]DashboardDAG, len(items))
	dagKeys := make([]string, 0, len(items))
	for index, item := range items {
		out[index] = DashboardDAG{DAGSummary: item}
		dagKeys = append(dagKeys, item.DagKey)
	}
	latestRuns, err := s.listLatestDAGRunsByDAGFromSnapshot(ctx, dagKeys)
	if err != nil {
		return nil, err
	}
	for index, item := range items {
		run, ok := latestRuns[item.DagKey]
		if !ok {
			continue
		}
		out[index].LatestRun = &run
		out[index].HasFinalOutput = runMetadataHasFinalOutput(run.Metadata)
	}
	return out, nil
}

func (s *service) buildDashboardDAGs(ctx context.Context, items []contract.DAGSummary) ([]DashboardDAG, error) {
	out := make([]DashboardDAG, len(items))
	group, groupCtx := errgroup.WithContext(ctx)
	// 误判防护：group.SetLimit 使用 dashboardDAGLatestRunLookupLimit 限制并发 run lookup。
	group.SetLimit(dashboardDAGLatestRunLookupLimit)
	for index, item := range items {
		index, item := index, item
		out[index] = DashboardDAG{DAGSummary: item}
		group.Go(func() error {
			runs, err := s.ListDAGRuns(groupCtx, item.DagKey, "", 1)
			if err != nil {
				return err
			}
			if len(runs) > 0 {
				run := runs[0]
				out[index].LatestRun = &run
				out[index].HasFinalOutput = runMetadataHasFinalOutput(run.Metadata)
			}
			return nil
		})
	}
	return out, group.Wait()
}

// runMetadataHasFinalOutput 运行元数据hasfinaloutput。
func runMetadataHasFinalOutput(raw json.RawMessage) bool {
	var envelope struct {
		FinalOutput json.RawMessage `json:"final_output"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	trimmed := strings.TrimSpace(string(envelope.FinalOutput))
	return trimmed != "" && trimmed != "null" && trimmed != `""` && trimmed != "{}" && trimmed != "[]"
}

func (s *service) populateDashboardSkills(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardSkills(ctx)
	out.Skills = items
	if errors.Is(err, contract.ErrSkillSameNameConflict) {
		return nil
	}
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
	if err != nil {
		return err
	}
	if refs == nil {
		refs = []FinalOutputRef{}
	}
	out.FinalOutputRefs = refs
	out.SharedFileRetention = buildSharedFileRetention(items, refs)
	return nil
}

func (s *service) listDashboardSkills(ctx context.Context) ([]contract.SkillInfo, error) {
	cwd := dashboardPromptScopeCWDFromContext(ctx)
	if s.skillInventory != nil {
		return s.skillInventory.ListSkillInventory(contract.WithSkillCWD(ctx, cwd))
	}
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

// filterDashboardPromptsByCWD 按工作目录处理过滤条件dashboardprompts。
func filterDashboardPromptsByCWD(items []promptstore.PromptTemplate, cwd string) []promptstore.PromptTemplate {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return []promptstore.PromptTemplate{}
	}
	filtered := make([]promptstore.PromptTemplate, 0, len(items))
	for _, item := range items {
		if dashboardPromptIsSystemManaged(item) {
			continue
		}
		if requestScope == "" || dashboardPromptVisibleForCWD(item, requestScope) {
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

// dashboardPromptIsSystemManaged 处理dashboardpromptissystemmanaged。
func dashboardPromptIsSystemManaged(template promptstore.PromptTemplate) bool {
	for _, tag := range dashboardPromptTags(template.Tags) {
		if strings.TrimSpace(tag) == "builtin:system" {
			return true
		}
	}
	if template.ManuallyEdited {
		return false
	}
	if dashboardPromptAuthorIsRPC(template.CreatedBy) || dashboardPromptAuthorIsRPC(template.UpdatedBy) {
		return false
	}
	return dashboardPromptAuthorLooksSystem(template.CreatedBy) || dashboardPromptAuthorLooksSystem(template.UpdatedBy)
}

func dashboardPromptAuthorIsRPC(author string) bool {
	return strings.TrimSpace(author) == "rpc.prompts"
}

func dashboardPromptAuthorLooksSystem(author string) bool {
	normalized := strings.ToLower(strings.TrimSpace(author))
	return strings.HasPrefix(normalized, "system") ||
		strings.Contains(normalized, "seed") ||
		strings.Contains(normalized, "migration") ||
		strings.Contains(normalized, "builtin.registry")
}

func (s *service) listDashboardMemory(ctx context.Context) ([]sharedfilestore.SharedFile, error) {
	return safeList(s.sharedFiles != nil, func() ([]sharedfilestore.SharedFile, error) {
		return s.sharedFiles.List(ctx, sharedfilestore.ListFilter{Limit: dashboardMemoryLimit})
	})
}

// ListSharedFiles 列出shared文件。
func (s *service) ListSharedFiles(ctx context.Context) ([]sharedfilestore.SharedFile, error) {
	items, err := s.listDashboardMemory(ctx)
	if items == nil {
		items = []sharedfilestore.SharedFile{}
	}
	return items, err
}

// listDashboardFinalOutputRefs 列出dashboardfinaloutputrefs。
func (s *service) listDashboardFinalOutputRefs(ctx context.Context) ([]FinalOutputRef, error) {
	if s.effectiveDAGRuntime() == nil {
		return s.listDashboardFinalOutputRefsFromSnapshot(ctx)
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
			runs, runErr := s.ListDAGRuns(groupCtx, dag.DagKey, "", dashboardFinalOutputRunLimit)
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

// listDashboardFinalOutputRefsFromSnapshot 从快照列出dashboardfinaloutputrefs。
func (s *service) listDashboardFinalOutputRefsFromSnapshot(ctx context.Context) ([]FinalOutputRef, error) {
	if !s.hasDAGSnapshotQueries() {
		return []FinalOutputRef{}, nil
	}
	dags, err := s.listDAGsFromSnapshot(ctx, contract.ListDAGsFilter{Limit: dashboardFinalOutputDAGLimit})
	if err != nil {
		return nil, err
	}
	refs := make([]FinalOutputRef, 0)
	seen := make(map[string]struct{})
	for _, dag := range dags {
		runs, runErr := s.listDAGRunsFromSnapshot(ctx, dag.DagKey, "", dashboardFinalOutputRunLimit)
		if runErr != nil {
			return nil, runErr
		}
		for _, run := range runs {
			ref, ok := finalOutputRefFromRun(run)
			if !ok {
				continue
			}
			if _, exists := seen[ref.Path]; exists {
				continue
			}
			seen[ref.Path] = struct{}{}
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func finalOutputRefFromRun(run contract.Run) (FinalOutputRef, bool) {
	output, ok := contract.FinalOutputFileFromRunMetadata(run.Metadata)
	if !ok {
		return FinalOutputRef{}, false
	}
	return FinalOutputRef{
		Path:          output.Path,
		RunKey:        strings.TrimSpace(run.RunKey),
		DagKey:        strings.TrimSpace(run.DagKey),
		SourceNodeKey: strings.TrimSpace(output.SourceNodeKey),
		Kind:          "file",
	}, true
}

// buildSharedFileRetention 构建shared文件retention。
func buildSharedFileRetention(files []sharedfilestore.SharedFile, refs []FinalOutputRef) SharedFileRetention {
	refByPath := make(map[string]FinalOutputRef, len(refs))
	for _, ref := range refs {
		path := strings.TrimSpace(ref.Path)
		if path != "" {
			ref.Path = path
			refByPath[path] = ref
		}
	}
	out := SharedFileRetention{Items: make([]SharedFileRetentionItem, 0, len(files))}
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		item := SharedFileRetentionItem{
			Path:             path,
			CleanupCandidate: true,
			Reason:           "unreferenced",
		}
		if ref, ok := refByPath[path]; ok {
			refCopy := ref
			item.Protected = true
			item.CleanupCandidate = false
			item.Reason = "final_output"
			item.FinalOutput = &refCopy
			out.ProtectedCount++
		} else {
			out.CleanupCandidateCount++
		}
		out.Items = append(out.Items, item)
	}
	return out
}
