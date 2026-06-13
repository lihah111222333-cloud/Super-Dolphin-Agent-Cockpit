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
	defaultLogLimit = 100
	maxLogLimit     = 500
	logSourceAll    = "all"
	logSourceAI     = "ai"
	logSourceSystem = "system"
)

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

type dashboardPromptScopeCWDKey struct{}

var _ Service = (*service)(nil)

// NewService 创建服务。
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

// newServiceWithDAGRuntime 创建带DAG运行时的服务。
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

func skillInventoryFromLister(skills contract.SkillLister) contract.SkillInventoryLister {
	inventory, _ := skills.(contract.SkillInventoryLister)
	return inventory
}

func withDashboardPromptScopeCWD(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, dashboardPromptScopeCWDKey{}, strings.TrimSpace(cwd))
}

func dashboardPromptScopeCWDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(dashboardPromptScopeCWDKey{}).(string)
	return strings.TrimSpace(value)
}

// GetDashboard 读取dashboard。
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

// GetAgentDetail 读取代理detail。
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

// GetSystemInfo 读取systeminfo。
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

// GetLogs 读取logs。
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

// mergeLogEntries 合并日志条目。
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

// Query 处理查询。
func (s *service) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if s.dbQueries == nil {
		return nil, errors.New("dashboard: dbquery store is not configured")
	}
	return s.dbQueries.Query(ctx, query, args...)
}

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

// loadBuildMetadata 加载build元数据。
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

// applyBuildSetting 应用buildsetting。
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

func shortCommit(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}

func runtimeMemoryStats() runtime.MemStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

type logSourceMode struct {
	includeSystem bool
	includeAI     bool
}

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
