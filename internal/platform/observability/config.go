package observability

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvMap 是测试用环境变量来源，实现 LookupEnv 以复用真实解析逻辑。
type EnvMap map[string]string

// Config 汇总 observability 的内存索引、JSONL、stack 和慢 RPC 阈值配置。
type Config struct {
	SchemaVersion           int
	Enabled                 bool
	Debug                   bool
	DisabledReason          string
	TraceStacks             map[Status]bool
	IndexMaxEvents          int
	IndexMaxTraceEvents     int
	IndexMaxThreadEvents    int
	IndexMaxSlowEvents      int
	IndexMaxErrorEvents     int
	EventMaxBytes           int
	MetadataMaxBytes        int
	StackMaxFrames          int
	StackMaxBytes           int
	StringMaxBytes          int
	JSONLMaxFileMB          int
	JSONLQueryTailMB        int
	JSONLRetentionDays      int
	JSONLRetentionMaxMB     int
	QueryTailTimeoutMS      int
	QueryTailMaxConcurrency int
	SlowRPCDefaultMS        int
	SlowRPCUIStateMS        int
	SlowRPCTurnStartMS      int
}

// intBound 描述一个整数环境变量的取值范围和写入 Config 的方式。
type intBound struct {
	key          string
	min          int
	max          int
	defaultValue int
	set          func(*Config, int)
}

// ParseConfigFromEnv 从真实进程环境解析 observability 配置。
func ParseConfigFromEnv() (Config, error) {
	return ParseConfig(osEnv{})
}

// ParseConfig 解析 observability 配置；显式关闭 tracing 时返回 disabled 配置且不继续校验其余项。
func ParseConfig(env interface{ LookupEnv(string) (string, bool) }) (Config, error) {
	cfg := defaultConfig(false)
	enabled, present, err := parseOptionalBool(env, "OBS_TRACING_ENABLED")
	if err != nil {
		return Config{}, err
	}
	if present && !enabled {
		cfg.Enabled = false
		cfg.DisabledReason = "observability tracing disabled"
		return cfg, nil
	}
	debug, err := parseDebug(env)
	if err != nil {
		return Config{}, err
	}
	cfg = defaultConfig(debug)
	cfg.Enabled = true
	if err := applyConfigBounds(env, &cfg); err != nil {
		return Config{}, err
	}
	stacks, err := parseTraceStacks(env)
	if err != nil {
		return Config{}, err
	}
	cfg.TraceStacks = stacks
	cfg.StringMaxBytes = deriveStringMaxBytes(cfg.EventMaxBytes)
	return cfg, nil
}

// LookupEnv 从 EnvMap 读取键值，供测试注入确定性环境。
func (env EnvMap) LookupEnv(key string) (string, bool) {
	value, ok := env[key]
	return value, ok
}

// osEnv 是真实 os.LookupEnv 的适配器。
type osEnv struct{}

// LookupEnv 代理 os.LookupEnv。
func (osEnv) LookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

// CaptureStackFor 判断指定状态是否需要捕获 stack。
func (cfg Config) CaptureStackFor(status Status) bool {
	return cfg.TraceStacks[status]
}

// defaultConfig 根据 debug 模式生成默认配置，并派生字符串字段大小上限。
func defaultConfig(debug bool) Config {
	cfg := Config{SchemaVersion: SchemaVersion, Debug: debug}
	for _, bound := range bounds(debug) {
		bound.set(&cfg, bound.defaultValue)
	}
	cfg.TraceStacks = defaultTraceStacks()
	cfg.StringMaxBytes = deriveStringMaxBytes(cfg.EventMaxBytes)
	return cfg
}

// parseDebug 读取 OBS_TRACE_DEBUG，非法 bool 会直接返回错误。
func parseDebug(env interface{ LookupEnv(string) (string, bool) }) (bool, error) {
	debug, _, err := parseOptionalBool(env, "OBS_TRACE_DEBUG")
	if err != nil {
		return false, err
	}
	return debug, nil
}

// applyConfigBounds 逐项解析整数边界配置，任一越界或非法值都会 fail-fast。
func applyConfigBounds(env interface{ LookupEnv(string) (string, bool) }, cfg *Config) error {
	for _, bound := range bounds(cfg.Debug) {
		value, err := parseBoundedInt(env, bound)
		if err != nil {
			return err
		}
		bound.set(cfg, value)
	}
	return nil
}

// parseOptionalBool 解析可选 bool 环境变量，空值视为未设置。
func parseOptionalBool(env interface{ LookupEnv(string) (string, bool) }, key string) (bool, bool, error) {
	raw, ok := env.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return false, false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, true, fmt.Errorf("%s must be boolean: %w", key, err)
	}
	return value, true, nil
}

// parseBoundedInt 解析单个整数环境变量，并强制落在声明范围内。
func parseBoundedInt(env interface{ LookupEnv(string) (string, bool) }, bound intBound) (int, error) {
	raw, ok := env.LookupEnv(bound.key)
	if !ok || strings.TrimSpace(raw) == "" {
		return bound.defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer between %d and %d: %w", bound.key, bound.min, bound.max, err)
	}
	if value < bound.min || value > bound.max {
		return 0, fmt.Errorf("%s=%d outside supported range [%d,%d]", bound.key, value, bound.min, bound.max)
	}
	return value, nil
}

// parseTraceStacks 读取需要捕获 stack 的状态集合，未配置时使用默认 slow/error/panic。
func parseTraceStacks(env interface{ LookupEnv(string) (string, bool) }) (map[Status]bool, error) {
	raw, ok := env.LookupEnv("OBS_TRACE_STACKS")
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultTraceStacks(), nil
	}
	return parseStackStatuses(raw)
}

// parseStackStatuses 解析逗号分隔状态列表，空项或未知状态都会拒绝。
func parseStackStatuses(raw string) (map[Status]bool, error) {
	result := make(map[Status]bool)
	for part := range strings.SplitSeq(raw, ",") {
		status := Status(strings.TrimSpace(part))
		if status == "" {
			return nil, fmt.Errorf("OBS_TRACE_STACKS contains an empty status")
		}
		if !validStackStatus(status) {
			return nil, fmt.Errorf("OBS_TRACE_STACKS contains unsupported status %q", status)
		}
		result[status] = true
	}
	return result, nil
}

// validStackStatus 判断状态是否允许开启 stack 捕获。
func validStackStatus(status Status) bool {
	switch status {
	case StatusSlow, StatusError, StatusPanic:
		return true
	default:
		return false
	}
}

// defaultTraceStacks 返回默认捕获 stack 的状态集合。
func defaultTraceStacks() map[Status]bool {
	return map[Status]bool{StatusSlow: true, StatusError: true, StatusPanic: true}
}

// deriveStringMaxBytes 从事件最大体积派生单字符串上限，避免单字段挤占整条事件预算。
func deriveStringMaxBytes(eventMaxBytes int) int {
	limit := eventMaxBytes / 2
	if limit > 512 {
		return 512
	}
	return limit
}

// bounds 返回所有整数配置边界；debug 模式放宽内存索引容量但仍保留硬上限。
func bounds(debug bool) []intBound {
	indexMaxEvents := 5000
	indexMaxTraceEvents := 128
	indexMaxThreadEvents := 256
	if debug {
		indexMaxEvents = 20000
		indexMaxTraceEvents = 256
		indexMaxThreadEvents = 512
	}
	return []intBound{
		{key: "OBS_INDEX_MAX_EVENTS", min: 1, max: 100000, defaultValue: indexMaxEvents, set: func(cfg *Config, v int) { cfg.IndexMaxEvents = v }},
		{key: "OBS_INDEX_MAX_TRACE_EVENTS", min: 1, max: 10000, defaultValue: indexMaxTraceEvents, set: func(cfg *Config, v int) { cfg.IndexMaxTraceEvents = v }},
		{key: "OBS_INDEX_MAX_THREAD_EVENTS", min: 1, max: 10000, defaultValue: indexMaxThreadEvents, set: func(cfg *Config, v int) { cfg.IndexMaxThreadEvents = v }},
		{key: "OBS_INDEX_MAX_SLOW_EVENTS", min: 1, max: 10000, defaultValue: 500, set: func(cfg *Config, v int) { cfg.IndexMaxSlowEvents = v }},
		{key: "OBS_INDEX_MAX_ERROR_EVENTS", min: 1, max: 10000, defaultValue: 500, set: func(cfg *Config, v int) { cfg.IndexMaxErrorEvents = v }},
		{key: "OBS_EVENT_MAX_BYTES", min: 1024, max: 1048576, defaultValue: 8192, set: func(cfg *Config, v int) { cfg.EventMaxBytes = v }},
		{key: "OBS_METADATA_MAX_BYTES", min: 256, max: 262144, defaultValue: 4096, set: func(cfg *Config, v int) { cfg.MetadataMaxBytes = v }},
		{key: "OBS_STACK_MAX_FRAMES", min: 1, max: 64, defaultValue: 12, set: func(cfg *Config, v int) { cfg.StackMaxFrames = v }},
		{key: "OBS_STACK_MAX_BYTES", min: 256, max: 262144, defaultValue: 8192, set: func(cfg *Config, v int) { cfg.StackMaxBytes = v }},
		{key: "OBS_JSONL_MAX_FILE_MB", min: 1, max: 1024, defaultValue: 64, set: func(cfg *Config, v int) { cfg.JSONLMaxFileMB = v }},
		{key: "OBS_JSONL_QUERY_TAIL_MB", min: 1, max: 1024, defaultValue: 20, set: func(cfg *Config, v int) { cfg.JSONLQueryTailMB = v }},
		{key: "OBS_JSONL_RETENTION_DAYS", min: 1, max: 365, defaultValue: 14, set: func(cfg *Config, v int) { cfg.JSONLRetentionDays = v }},
		{key: "OBS_JSONL_RETENTION_MAX_MB", min: 1, max: 102400, defaultValue: 512, set: func(cfg *Config, v int) { cfg.JSONLRetentionMaxMB = v }},
		{key: "OBS_QUERY_TAIL_TIMEOUT_MS", min: 1, max: 60000, defaultValue: 750, set: func(cfg *Config, v int) { cfg.QueryTailTimeoutMS = v }},
		{key: "OBS_QUERY_TAIL_MAX_CONCURRENCY", min: 1, max: 32, defaultValue: 1, set: func(cfg *Config, v int) { cfg.QueryTailMaxConcurrency = v }},
		{key: "OBS_SLOW_RPC_DEFAULT_MS", min: 1, max: 600000, defaultValue: 500, set: func(cfg *Config, v int) { cfg.SlowRPCDefaultMS = v }},
		{key: "OBS_SLOW_RPC_UI_STATE_MS", min: 1, max: 600000, defaultValue: 300, set: func(cfg *Config, v int) { cfg.SlowRPCUIStateMS = v }},
		{key: "OBS_SLOW_RPC_TURN_START_MS", min: 1, max: 600000, defaultValue: 1000, set: func(cfg *Config, v int) { cfg.SlowRPCTurnStartMS = v }},
	}
}
