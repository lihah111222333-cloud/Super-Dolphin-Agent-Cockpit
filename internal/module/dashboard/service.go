package dashboard

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
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
	orchestration contract.OrchestrationService
	agentStatuses agentstatusstore.Store
	systemLogs    systemlogstore.Store
	auditLogs     auditlogstore.Store
	busLogs       buslogstore.Store
	aiLogs        ailogstore.Store
	dbQueries     dbquerystore.Store
	taskTraces    tasktracestore.Store
	commandCards  commandcardstore.Reader
	prompts       promptstore.Reader
	sharedFiles   sharedfilestore.Reader
	skills        skillmodule.Service
	startedAt     time.Time
}

var _ Service = (*service)(nil)

func NewService(
	orchestrationSvc contract.OrchestrationService,
	agentStatuses agentstatusstore.Store,
	systemLogs systemlogstore.Store,
	auditLogs auditlogstore.Store,
	busLogs buslogstore.Store,
	aiLogs ailogstore.Store,
	dbQueries dbquerystore.Store,
	taskTraces tasktracestore.Store,
	commandCards commandcardstore.Reader,
	prompts promptstore.Reader,
	sharedFiles sharedfilestore.Reader,
	skills skillmodule.Service,
) Service {
	return &service{
		orchestration: orchestrationSvc,
		agentStatuses: agentStatuses,
		systemLogs:    systemLogs,
		auditLogs:     auditLogs,
		busLogs:       busLogs,
		aiLogs:        aiLogs,
		dbQueries:     dbQueries,
		taskTraces:    taskTraces,
		commandCards:  commandCards,
		prompts:       prompts,
		sharedFiles:   sharedFiles,
		skills:        skills,
		startedAt:     time.Now(),
	}
}

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
			return nil
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return nil, err
	}
	snapshot := AgentSnapshot(rawSnapshot)
	report := strings.TrimSpace(snapshot.LastReport)
	if reportErr == nil {
		report = firstNonEmpty(strings.TrimSpace(reportResp.Report), report)
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

func (s *service) GetLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error) {
	mode, err := resolveLogSource(filter.Source)
	if err != nil {
		return nil, err
	}
	limit := clampLogLimit(filter.Limit)
	filter.Limit = limit
	entries := make([]LogEntry, 0, limit)
	if mode.includeSystem {
		entries, err = s.appendSystemLogs(ctx, entries, filter)
		if err != nil {
			return nil, err
		}
	}
	if mode.includeAI {
		entries, err = s.appendAILogs(ctx, entries, filter)
		if err != nil {
			return nil, err
		}
	}
	sortLogEntries(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

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

func clampLogLimit(limit int) int {
	if limit <= 0 {
		return defaultLogLimit
	}
	if limit > maxLogLimit {
		return maxLogLimit
	}
	return limit
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
	rows, err := s.systemLogs.List(ctx, systemlogstore.ListFilter{
		Level:     strings.TrimSpace(filter.Level),
		Logger:    strings.TrimSpace(filter.Logger),
		Component: strings.TrimSpace(filter.Component),
		AgentID:   strings.TrimSpace(filter.AgentID),
		ThreadID:  strings.TrimSpace(filter.ThreadID),
		EventType: strings.TrimSpace(filter.EventType),
		ToolName:  strings.TrimSpace(filter.ToolName),
		Keyword:   strings.TrimSpace(filter.Keyword),
		Limit:     int32(filter.Limit),
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		entry := mapSystemLogEntry(row)
		if matchesLogFilter(entry, filter) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
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
	for _, row := range rows {
		entry := mapAILogEntry(row)
		if matchesLogFilter(entry, filter) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func mapSystemLogEntry(row systemlogstore.SystemLog) LogEntry {
	return LogEntry{
		Source:     logSourceSystem,
		ID:         row.ID,
		Timestamp:  row.Ts,
		Level:      row.Level,
		Logger:     row.Logger,
		Message:    row.Message,
		Raw:        row.Raw,
		Component:  row.Component,
		AgentID:    row.AgentID,
		ThreadID:   row.ThreadID,
		TraceID:    row.TraceID,
		EventType:  row.EventType,
		ToolName:   row.ToolName,
		DurationMs: row.DurationMs,
		Extra:      row.Extra,
	}
}

func mapAILogEntry(row ailogstore.AILog) LogEntry {
	return LogEntry{
		Source:     logSourceAI,
		ID:         row.ID,
		Timestamp:  row.Ts,
		Level:      row.Level,
		Logger:     row.Logger,
		Message:    row.Message,
		Raw:        row.Raw,
		Component:  row.Component,
		AgentID:    row.AgentID,
		ThreadID:   row.ThreadID,
		TraceID:    row.TraceID,
		EventType:  row.EventType,
		ToolName:   row.ToolName,
		DurationMs: row.DurationMs,
		Extra:      row.Extra,
	}
}
