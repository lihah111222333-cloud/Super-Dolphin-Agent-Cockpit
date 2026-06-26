package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"golang.org/x/sync/errgroup"
)

const (
	// dashboard 页面聚合的容量守卫，避免单次 RPC 拉取无界列表。
	dashboardPageDefaultLimit          = 100
	dashboardMemoryLimit               = 500
	dashboardFinalOutputDAGLimit       = 20
	dashboardFinalOutputRunLimit int32 = 3
	// dashboardDAGLatestRunLookupLimit 配合 group.SetLimit 限制 latest run 并发。
	dashboardDAGLatestRunLookupLimit = 4
)

// DashboardPage 是前端 dashboard 分页接口的聚合 wire 结构。
// 所有切片初始化为空切片，避免 JSON 输出 null 破坏前端兼容。
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

// dashboardPageLoader 是 dashboard 页面区块加载函数。
// loader 之间可并发执行，但必须只写入自己负责的 DashboardPage 字段。
type dashboardPageLoader func(context.Context) error

// SharedFileRetention 描述共享文件相对 final output 引用的保留判断。
// 它只做 dashboard 展示分析，不直接删除文件。
type SharedFileRetention struct {
	Items                 []SharedFileRetentionItem `json:"items"`
	ProtectedCount        int                       `json:"protectedCount"`
	CleanupCandidateCount int                       `json:"cleanupCandidateCount"`
}

// SharedFileRetentionItem 描述单个共享文件的保留或清理候选状态。
// Protected=true 表示该文件仍被 final output 引用，不能被清理建议选中。
type SharedFileRetentionItem struct {
	Path             string          `json:"path"`
	Protected        bool            `json:"protected"`
	CleanupCandidate bool            `json:"cleanupCandidate"`
	Reason           string          `json:"reason,omitempty"`
	FinalOutput      *FinalOutputRef `json:"finalOutput,omitempty"`
}

// GetDashboardPage 加载指定 dashboard 页面区块。
// 未知 page 返回空聚合结构，不触发无关 store 查询。
func (s *service) GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error) {
	out := newDashboardPage()
	if err := s.populateDashboardPage(ctx, out, page); err != nil {
		return nil, err
	}
	return out, nil
}

// newDashboardPage 初始化带空切片的 DashboardPage，避免 JSON 序列化时输出 null。
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

// populateDashboardPage 并发执行当前页面对应的所有 loader，任一失败即返回错误。
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

// dashboardPageLoaders 根据 page 名称选择需要并发执行的 loader。
// 每个 case 只填充对应区块，避免页面局部刷新时读取全量 dashboard 数据。
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

// populateDashboardAgents 读取 agent 概览并写入 DashboardPage.Agents。
// 读取失败仍保留默认空切片，由返回 error 告知调用方。
func (s *service) populateDashboardAgents(ctx context.Context, out *DashboardPage) error {
	items, err := s.listAgents(ctx)
	out.Agents = items
	return err
}

// populateDashboardDAGs 加载 DAG 列表并补充最新 run/final output 标记。
// 没有快照查询和 DAG runtime 时返回空切片，支持无编排能力的 dashboard。
func (s *service) populateDashboardDAGs(ctx context.Context, out *DashboardPage) error {
	if !s.hasDAGSnapshotQueries() && s.effectiveDAGRuntime() == nil {
		return nil
	}
	// 只取 dashboardPageDefaultLimit 条，避免 dashboard 页面触发无界 DAG 查询。
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

// buildDashboardDAGsFromSnapshot 直接查数据库快照，批量获取每个 DAG 最新 run，避免依赖编排服务。
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

// buildDashboardDAGs 通过编排服务并发获取每个 DAG 最新 run，受 dashboardDAGLatestRunLookupLimit 并发限制。
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

// runMetadataHasFinalOutput 判断 run metadata 是否包含非空 final_output。
// 解析失败按 false 处理，因为该字段只影响 dashboard 展示标记，不影响 run 状态。
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

// populateDashboardSkills 读取技能列表并填充，同名冲突错误视为软错误不阻断。
func (s *service) populateDashboardSkills(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardSkills(ctx)
	out.Skills = items
	if errors.Is(err, contract.ErrSkillSameNameConflict) {
		return nil
	}
	return err
}

// populateDashboardCommandCards 读取命令卡片并填充到 DashboardPage。
// store 缺失时 helper 返回空切片，页面仍可加载其他区块。
func (s *service) populateDashboardCommandCards(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardCommandCards(ctx)
	out.CommandCards = items
	return err
}

// populateDashboardPrompts 读取当前 cwd 可见的用户提示模板。
// 系统管理模板在 listDashboardPrompts 中过滤，避免前端误当作用户资产。
func (s *service) populateDashboardPrompts(ctx context.Context, out *DashboardPage) error {
	items, err := s.listDashboardPrompts(ctx)
	out.Prompts = items
	return err
}

// populateDashboardMemory 读取共享文件和 final output refs，组装 retention 分析结果。
// retention 只给 UI 做保护/清理提示，不在这里执行删除。
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

// listDashboardSkills 优先使用 skillInventory，回退到 skills.ListSkills。
func (s *service) listDashboardSkills(ctx context.Context) ([]contract.SkillInfo, error) {
	cwd := dashboardPromptScopeCWDFromContext(ctx)
	if s.skillInventory != nil {
		return s.skillInventory.ListSkillInventory(contract.WithSkillCWD(ctx, cwd))
	}
	return safeList(s.skills != nil, func() ([]contract.SkillInfo, error) {
		return s.skills.ListSkills(contract.WithSkillCWD(ctx, cwd))
	})
}

// listDashboardCommandCards 读取命令卡片列表。
// store 为 nil 时返回空切片，保持 dashboard commands 页可降级展示。
func (s *service) listDashboardCommandCards(ctx context.Context) ([]commandcardstore.CommandCard, error) {
	return safeList(s.commandCards != nil, func() ([]commandcardstore.CommandCard, error) {
		return s.commandCards.List(ctx, commandcardstore.ListFilter{Limit: dashboardPageDefaultLimit})
	})
}

// listDashboardPrompts 读取提示模板，按 cwd 过滤并排除系统管理的模板。
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

// filterDashboardPromptsByCWD 按请求 cwd 过滤 prompt 模板。
// 空 cwd 不展示任何模板，避免把全局模板泄露到未绑定工作区的页面。
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

// dashboardPromptVisibleForCWD 判断模板是否对指定 cwd 可见；无 scope 标签的模板对所有 cwd 可见。
func dashboardPromptVisibleForCWD(template promptstore.PromptTemplate, cwd string) bool {
	storedScope := dashboardPromptScopeFromTags(template.Tags)
	return storedScope == "" || storedScope == strings.TrimSpace(cwd)
}

// dashboardPromptScopeFromTags 从标签列表中提取 scope.cwd: 前缀的 cwd 值。
func dashboardPromptScopeFromTags(raw json.RawMessage) string {
	for _, tag := range dashboardPromptTags(raw) {
		if value, ok := strings.CutPrefix(tag, "scope.cwd:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// dashboardPromptTags 反序列化 tags JSON 为字符串切片，解析失败时返回空切片。
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

// dashboardPromptIsSystemManaged 判断模板是否由系统创建而非用户手工维护。
// 优先检查 builtin:system 标签，其次检查作者名称是否符合系统命名模式。
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

// dashboardPromptAuthorIsRPC 检查模板作者是否为 RPC 接口写入。
func dashboardPromptAuthorIsRPC(author string) bool {
	return strings.TrimSpace(author) == "rpc.prompts"
}

// dashboardPromptAuthorLooksSystem 通过命名模式判断作者是否为系统自动写入。
func dashboardPromptAuthorLooksSystem(author string) bool {
	normalized := strings.ToLower(strings.TrimSpace(author))
	return strings.HasPrefix(normalized, "system") ||
		strings.Contains(normalized, "seed") ||
		strings.Contains(normalized, "migration") ||
		strings.Contains(normalized, "builtin.registry")
}

// listDashboardMemory 读取共享文件列表。
// store 为 nil 时返回空切片，避免 memory 页因可选能力缺失而失败。
func (s *service) listDashboardMemory(ctx context.Context) ([]sharedfilestore.SharedFile, error) {
	return safeList(s.sharedFiles != nil, func() ([]sharedfilestore.SharedFile, error) {
		return s.sharedFiles.List(ctx, sharedfilestore.ListFilter{Limit: dashboardMemoryLimit})
	})
}

// ListSharedFiles 暴露 dashboard 共享文件只读列表。
// 返回值保证非 nil 切片，保持 JSON wire 兼容。
func (s *service) ListSharedFiles(ctx context.Context) ([]sharedfilestore.SharedFile, error) {
	items, err := s.listDashboardMemory(ctx)
	if items == nil {
		items = []sharedfilestore.SharedFile{}
	}
	return items, err
}

// listDashboardFinalOutputRefs 收集最近 DAG run 中引用的 final output 文件。
// 有 DAG runtime 时并发查询运行记录；无 runtime 时回退到数据库快照查询。
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
				ref, ok, parseErr := finalOutputRefFromRun(run)
				if parseErr != nil {
					return parseErr
				}
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

// listDashboardFinalOutputRefsFromSnapshot 从数据库快照收集 final output 引用。
// 没有快照查询能力时返回空切片，避免把缺能力误报为查询失败。
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
			ref, ok, parseErr := finalOutputRefFromRun(run)
			if parseErr != nil {
				return nil, parseErr
			}
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

// finalOutputRefFromRun 从 run 元数据中提取 final output 文件引用，metadata 不含 final_output 时返回 false。
// 损坏的 metadata 必须返回错误，避免 dashboard 把坏产物记录当作“无产物”。
func finalOutputRefFromRun(run contract.Run) (FinalOutputRef, bool, error) {
	output, ok, err := contract.FinalOutputFileFromRunMetadataStrict(run.Metadata)
	if err != nil {
		return FinalOutputRef{}, false, fmt.Errorf("dashboard final_output metadata for run %q: %w", strings.TrimSpace(run.RunKey), err)
	}
	if !ok {
		return FinalOutputRef{}, false, nil
	}
	return FinalOutputRef{
		Path:          output.Path,
		RunKey:        strings.TrimSpace(run.RunKey),
		DagKey:        strings.TrimSpace(run.DagKey),
		SourceNodeKey: strings.TrimSpace(output.SourceNodeKey),
		Kind:          "file",
	}, true, nil
}

// buildSharedFileRetention 根据 final output 引用标记共享文件保留状态。
// 仅按路径匹配，路径为空的文件会被跳过，避免生成不可操作的清理候选。
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
