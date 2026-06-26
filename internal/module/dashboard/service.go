package dashboard

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"golang.org/x/sync/errgroup"
)

const (
	// 日志读取默认值和可选来源，所有入口都经 resolveLogSource 规整。
	defaultLogLimit = 100
	maxLogLimit     = 500
	logSourceAll    = "all"
	logSourceAI     = "ai"
	logSourceSystem = "system"
)

// service 聚合 dashboard 需要的 orchestration、store 和只读模块依赖。
// 字段允许为 nil 的 reader 会在具体 list helper 中返回空切片；核心 orchestration 缺失时直接报错。
type service struct {
	orchestration  contract.OrchestrationService
	dagRuntime     contract.DAGRuntime
	agentStatuses  agentstatusstore.Store
	systemLogs     systemlogstore.Store
	auditLogs      auditlogstore.Store
	busLogs        buslogstore.Store
	aiLogs         ailogstore.Store
	dbQueries      dbquerystore.Store
	commandCards   commandcardstore.Reader
	prompts        promptstore.Reader
	sharedFiles    sharedfilestore.Reader
	skills         contract.SkillLister
	skillInventory contract.SkillInventoryLister
	startedAt      time.Time
}

// dashboardPromptScopeCWDKey 是 dashboard prompt 过滤使用的 context 私有 key。
type dashboardPromptScopeCWDKey struct{}

var _ Service = (*service)(nil)

// NewService 创建 dashboard 服务。
// 构造阶段只保存依赖，不访问 store；部分 reader 可为 nil，以支持精简运行模式。
func NewService(
	orchestrationSvc contract.OrchestrationService,
	agentStatuses agentstatusstore.Store,
	systemLogs systemlogstore.Store,
	auditLogs auditlogstore.Store,
	busLogs buslogstore.Store,
	aiLogs ailogstore.Store,
	dbQueries dbquerystore.Store,
	commandCards commandcardstore.Reader,
	prompts promptstore.Reader,
	sharedFiles sharedfilestore.Reader,
	skills contract.SkillLister,
) Service {
	return &service{
		orchestration:  orchestrationSvc,
		dagRuntime:     orchestrationSvc,
		agentStatuses:  agentStatuses,
		systemLogs:     systemLogs,
		auditLogs:      auditLogs,
		busLogs:        busLogs,
		aiLogs:         aiLogs,
		dbQueries:      dbQueries,
		commandCards:   commandCards,
		prompts:        prompts,
		sharedFiles:    sharedFiles,
		skills:         skills,
		skillInventory: skillInventoryFromLister(skills),
		startedAt:      time.Now(),
	}
}

// newServiceWithDAGRuntime 创建带独立 DAG runtime 的 dashboard 服务。
// 当 runtime 为 nil 时保留 orchestration 作为默认 DAGRuntime，便于旧 wiring 兼容。
func newServiceWithDAGRuntime(
	orchestrationSvc contract.OrchestrationService,
	dagRuntime contract.DAGRuntime,
	agentStatuses agentstatusstore.Store,
	systemLogs systemlogstore.Store,
	auditLogs auditlogstore.Store,
	busLogs buslogstore.Store,
	aiLogs ailogstore.Store,
	dbQueries dbquerystore.Store,
	commandCards commandcardstore.Reader,
	prompts promptstore.Reader,
	sharedFiles sharedfilestore.Reader,
	skills contract.SkillLister,
) Service {
	svc := NewService(
		orchestrationSvc,
		agentStatuses,
		systemLogs,
		auditLogs,
		busLogs,
		aiLogs,
		dbQueries,
		commandCards,
		prompts,
		sharedFiles,
		skills,
	)
	if impl, ok := svc.(*service); ok && dagRuntime != nil {
		impl.dagRuntime = dagRuntime
	}
	return svc
}

// skillInventoryFromLister 在 skill lister 同时支持 inventory 时提取增强接口。
// 不支持时返回 nil，调用方再回退到 ListSkills。
func skillInventoryFromLister(skills contract.SkillLister) contract.SkillInventoryLister {
	inventory, _ := skills.(contract.SkillInventoryLister)
	return inventory
}

// withDashboardPromptScopeCWD 将当前页面 cwd 放进 context，供 prompt/skill 列表过滤使用。
func withDashboardPromptScopeCWD(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, dashboardPromptScopeCWDKey{}, strings.TrimSpace(cwd))
}

// dashboardPromptScopeCWDFromContext 读取 dashboard 页面传递的 cwd scope，缺失时返回空字符串。
func dashboardPromptScopeCWDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(dashboardPromptScopeCWDKey{}).(string)
	return strings.TrimSpace(value)
}

// GetDashboard 构建 dashboard 首页概览。
// agent 列表来自 orchestration，系统信息在本进程内采样；任一核心依赖失败直接返回错误。
func (s *service) GetDashboard(ctx context.Context) (*Dashboard, error) {
	agents, err := s.listAgents(ctx)
	if err != nil {
		return nil, err
	}
	return &Dashboard{
		Agents:     agents,
		System:     s.buildSystemInfo(len(agents)),
		TokenUsage: TokenUsage{},
		Uptime:     time.Since(s.startedAt),
	}, nil
}

// GetAgentDetail 并发读取 agent 快照和报告。
// orchestration 未配置或 agentID 为空时 fail-fast，避免详情页展示错 agent。
func (s *service) GetAgentDetail(ctx context.Context, agentID string) (*AgentDetail, error) {
	if s.orchestration == nil {
		return nil, errors.New("dashboard: orchestration service is not configured")
	}
	id := strings.TrimSpace(agentID)
	if id == "" {
		return nil, errors.New("dashboard: agent id is required")
	}
	var (
		rawSnapshot contract.AgentSnapshot
		reportResp  contract.AgentReportResult
		reportErr   error
	)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		var err error
		rawSnapshot, err = s.orchestration.Snapshot(groupCtx, id)
		return err
	})
	group.Go(func() error {
		reportResp, reportErr = s.orchestration.GetReport(groupCtx, id)
		if reportErr != nil {
			return reportErr
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return nil, err
	}
	snapshot := AgentSnapshot(rawSnapshot)
	report := strings.TrimSpace(snapshot.LastReport)
	if reportErr == nil {
		report = util.FirstNonEmpty(strings.TrimSpace(reportResp.Report), report)
	}
	snapshot.LastReport = report
	return &AgentDetail{
		AgentID:     snapshot.ID,
		Name:        snapshot.Name,
		Snapshot:    snapshot,
		ThreadID:    snapshot.ThreadID,
		Status:      snapshot.State,
		TurnHistory: turnHistoryFromSnapshot(snapshot),
		LastReport:  report,
	}, nil
}

// GetSystemInfo 采样当前进程和 agent 数量。
// orchestration 可缺省；存在时 ListAgents 失败会阻断，避免 agentCount 静默失真。
func (s *service) GetSystemInfo(ctx context.Context) (*SystemInfo, error) {
	agentCount := 0
	if s.orchestration != nil {
		agents, err := s.orchestration.ListAgents(ctx)
		if err != nil {
			return nil, err
		}
		agentCount = len(agents)
	}
	info := s.buildSystemInfo(agentCount)
	return &info, nil
}

// GetLogs 按 source 合并 system/AI 日志。
// source 不合法直接报错；两个来源都启用时按时间倒序合并并受 limit 约束。
func (s *service) GetLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error) {
	mode, err := resolveLogSource(filter.Source)
	if err != nil {
		return nil, err
	}
	limit := util.ClampLimit(filter.Limit, 1, maxLogLimit, defaultLogLimit)
	filter.Limit = limit

	var systemEntries, aiEntries []LogEntry
	if mode.includeSystem {
		systemEntries, err = s.appendSystemLogs(ctx, nil, filter)
		if err != nil {
			return nil, err
		}
	}
	if mode.includeAI {
		aiEntries, err = s.appendAILogs(ctx, nil, filter)
		if err != nil {
			return nil, err
		}
	}
	if !mode.includeSystem {
		return aiEntries, nil
	}
	if !mode.includeAI {
		return systemEntries, nil
	}
	return mergeLogEntries(systemEntries, aiEntries, limit), nil
}

// mergeLogEntries 合并两个已按时间倒序排列的日志切片。
// 它只取 limit 条，避免 dashboard 聚合时扩大前端传入的读取窗口。
func mergeLogEntries(a, b []LogEntry, limit int) []LogEntry {
	out := make([]LogEntry, 0, limit)
	i, j := 0, 0
	for len(out) < limit && (i < len(a) || j < len(b)) {
		if i < len(a) && (j >= len(b) || !a[i].Timestamp.Before(b[j].Timestamp)) {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	return out
}

// Query 透传 dashboard 只读 SQL 查询到 dbquery store。
// store 未配置时直接报错，避免调用方误以为空结果就是无数据。
func (s *service) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if s.dbQueries == nil {
		return nil, errors.New("dashboard: dbquery store is not configured")
	}
	return s.dbQueries.Query(ctx, query, args...)
}

// listAgents 从 orchestration 读取 agent 快照并转换为 dashboard 概览。
// orchestration 是该路径的硬依赖，缺失时返回显式配置错误。
func (s *service) listAgents(ctx context.Context) ([]AgentOverview, error) {
	if s.orchestration == nil {
		return nil, errors.New("dashboard: orchestration service is not configured")
	}
	agents, err := s.orchestration.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]AgentOverview, 0, len(agents))
	for _, agent := range agents {
		items = append(items, AgentOverview(agent))
	}
	return items, nil
}

// buildSystemInfo 汇总构建信息、runtime 信息和内存快照。
// agentCount 由调用方提供，避免这里再次触发 orchestration 查询。
func (s *service) buildSystemInfo(agentCount int) SystemInfo {
	stats := runtimeMemoryStats()
	build := loadBuildMetadata()
	return SystemInfo{
		StartedAt:        s.startedAt,
		BuildVersion:     build.version,
		BuildCommit:      build.commit,
		BuildTime:        build.buildTime,
		Dirty:            build.dirty,
		GoVersion:        build.goVersion,
		Runtime:          build.runtime,
		NumCPU:           runtime.NumCPU(),
		NumGoroutine:     runtime.NumGoroutine(),
		MemoryAllocBytes: stats.Alloc,
		MemorySysBytes:   stats.Sys,
		AgentCount:       agentCount,
	}
}

// loadBuildMetadata 从 debug.BuildInfo 提取构建元数据。
// 读不到构建信息时返回 dev/unknown 默认值，保证系统信息页面仍可渲染。
func loadBuildMetadata() buildMetadata {
	meta := buildMetadata{
		version:   "dev",
		commit:    "unknown",
		goVersion: runtime.Version(),
		runtime:   runtime.GOOS + "/" + runtime.GOARCH,
	}
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return meta
	}
	if version := strings.TrimSpace(buildInfo.Main.Version); version != "" && version != "(devel)" {
		meta.version = version
	}
	if goVersion := strings.TrimSpace(buildInfo.GoVersion); goVersion != "" {
		meta.goVersion = goVersion
	}
	for _, setting := range buildInfo.Settings {
		applyBuildSetting(&meta, setting.Key, setting.Value)
	}
	return meta
}

// applyBuildSetting 将 Go build setting 合并进 buildMetadata。
// 只消费 vcs 相关 key，未知 key 保持忽略以兼容不同构建器。
func applyBuildSetting(meta *buildMetadata, key, value string) {
	if meta == nil {
		return
	}
	switch key {
	case "vcs.revision":
		if commit := shortCommit(value); commit != "" {
			meta.commit = commit
		}
	case "vcs.time":
		meta.buildTime = strings.TrimSpace(value)
	case "vcs.modified":
		meta.dirty = strings.EqualFold(strings.TrimSpace(value), "true")
	}
}

// shortCommit 将完整 git revision 缩短为 UI 展示用的 12 位字符串。
func shortCommit(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}

// runtimeMemoryStats 读取当前进程内存统计。
// runtime.ReadMemStats 会短暂停顿调用线程，因此只在 dashboard 快照路径调用。
func runtimeMemoryStats() runtime.MemStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

// logSourceMode 表示一次日志查询需要读取哪些后端日志源。
type logSourceMode struct {
	includeSystem bool
	includeAI     bool
}

// resolveLogSource 将前端 source 字符串规整为日志读取模式。
// 未知来源返回错误，防止拼写错误悄悄退回 all。
func resolveLogSource(source string) (logSourceMode, error) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", logSourceAll:
		return logSourceMode{includeSystem: true, includeAI: true}, nil
	case logSourceSystem, "systemlog":
		return logSourceMode{includeSystem: true}, nil
	case logSourceAI, "ailog":
		return logSourceMode{includeAI: true}, nil
	default:
		return logSourceMode{}, errors.New("dashboard: unsupported log source")
	}
}

// appendSystemLogs 从 system log store 追加满足过滤条件的日志。
// store 为 nil 时返回已有 entries，支持无 system log 的轻量运行模式。
func (s *service) appendSystemLogs(ctx context.Context, entries []LogEntry, filter LogFilter) ([]LogEntry, error) {
	if s.systemLogs == nil {
		return entries, nil
	}
	rows, err := s.systemLogs.List(ctx, newSystemLogListFilter(filter))
	if err != nil {
		return nil, err
	}
	return appendMappedLogs(entries, rows, filter, func(row systemlogstore.SystemLog) LogEntry {
		return mapLogEntry(row, logSourceSystem)
	}), nil
}

// appendAILogs 从 AI log store 追加满足过滤条件的日志。
// store 为 nil 时返回已有 entries，错误不吞掉，交由 GetLogs 返回。
func (s *service) appendAILogs(ctx context.Context, entries []LogEntry, filter LogFilter) ([]LogEntry, error) {
	if s.aiLogs == nil {
		return entries, nil
	}
	rows, err := s.aiLogs.List(ctx, ailogstore.ListFilter{
		Keyword: strings.TrimSpace(filter.Keyword),
		Limit:   int32(filter.Limit),
	})
	if err != nil {
		return nil, err
	}
	return appendMappedLogs(entries, rows, filter, func(row ailogstore.AILog) LogEntry {
		return mapLogEntry(row, logSourceAI)
	}), nil
}
