package observability

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type EnvMap map[string]string

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

type intBound struct {
	key          string
	min          int
	max          int
	defaultValue int
	set          func(*Config, int)
}

// ParseConfigFromEnv 从env解析配置。
func ParseConfigFromEnv() (Config, error) {
	return ParseConfig(osEnv{})
}

// ParseConfig 解析配置。
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

// LookupEnv 处理lookupenv。
func (env EnvMap) LookupEnv(key string) (string, bool) {
	value, ok := env[key]
	return value, ok
}

type osEnv struct{}

// LookupEnv 处理lookupenv。
func (osEnv) LookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

// CaptureStackFor 为平台observability处理capturestack。
func (cfg Config) CaptureStackFor(status Status) bool {
	return cfg.TraceStacks[status]
}

func defaultConfig(debug bool) Config {
	cfg := Config{SchemaVersion: SchemaVersion, Debug: debug}
	for _, bound := range bounds(debug) {
		bound.set(&cfg, bound.defaultValue)
	}
	cfg.TraceStacks = defaultTraceStacks()
	cfg.StringMaxBytes = deriveStringMaxBytes(cfg.EventMaxBytes)
	return cfg
}

func parseDebug(env interface{ LookupEnv(string) (string, bool) }) (bool, error) {
	debug, _, err := parseOptionalBool(env, "OBS_TRACE_DEBUG")
	if err != nil {
		return false, err
	}
	return debug, nil
}

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

// parseBoundedInt 解析boundedint。
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

func parseTraceStacks(env interface{ LookupEnv(string) (string, bool) }) (map[Status]bool, error) {
	raw, ok := env.LookupEnv("OBS_TRACE_STACKS")
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultTraceStacks(), nil
	}
	return parseStackStatuses(raw)
}

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

func validStackStatus(status Status) bool {
	switch status {
	case StatusSlow, StatusError, StatusPanic:
		return true
	default:
		return false
	}
}

func defaultTraceStacks() map[Status]bool {
	return map[Status]bool{StatusSlow: true, StatusError: true, StatusPanic: true}
}

func deriveStringMaxBytes(eventMaxBytes int) int {
	limit := eventMaxBytes / 2
	if limit > 512 {
		return 512
	}
	return limit
}

// bounds 处理边界。
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
