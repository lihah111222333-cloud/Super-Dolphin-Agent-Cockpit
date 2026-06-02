package observability

import (
	"strings"
	"testing"
)

func TestConfigDefaultEnablesSafeTracing(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{})
	if err != nil {
		t.Fatalf("ParseConfig default: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("default Enabled = false, want true")
	}
	if cfg.DisabledReason != "" {
		t.Fatalf("DisabledReason = %q, want empty", cfg.DisabledReason)
	}
	if cfg.Debug {
		t.Fatalf("Debug = true, want default safe mode")
	}
	if cfg.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", cfg.SchemaVersion, SchemaVersion)
	}
	if cfg.IndexMaxEvents != 5000 || cfg.IndexMaxTraceEvents != 128 || cfg.IndexMaxThreadEvents != 256 {
		t.Fatalf("unexpected safe defaults: %+v", cfg)
	}
}

func TestConfigEnabledDefaultsAndTraceStacks(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1"})
	if err != nil {
		t.Fatalf("ParseConfig enabled defaults: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if cfg.IndexMaxEvents != 5000 || cfg.IndexMaxTraceEvents != 128 || cfg.IndexMaxThreadEvents != 256 {
		t.Fatalf("unexpected index defaults: %+v", cfg)
	}
	if cfg.EventMaxBytes != 8192 || cfg.MetadataMaxBytes != 4096 || cfg.StackMaxFrames != 12 || cfg.StackMaxBytes != 8192 {
		t.Fatalf("unexpected byte/stack defaults: %+v", cfg)
	}
	assertStackEnabled(t, cfg, StatusSlow)
	assertStackEnabled(t, cfg, StatusError)
	assertStackEnabled(t, cfg, StatusPanic)
}

func assertStackEnabled(t *testing.T, cfg Config, status Status) {
	t.Helper()
	if !cfg.CaptureStackFor(status) {
		t.Fatalf("CaptureStackFor(%q) = false", status)
	}
}

func TestConfigEnabledRejectsUnsafeValues(t *testing.T) {
	cases := []EnvMap{
		{"OBS_INDEX_MAX_EVENTS": "0"},
		{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "0"},
		{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_TRACE_EVENTS": "-1"},
		{"OBS_TRACING_ENABLED": "1", "OBS_EVENT_MAX_BYTES": "104857601"},
		{"OBS_TRACING_ENABLED": "1", "OBS_METADATA_MAX_BYTES": "abc"},
		{"OBS_TRACING_ENABLED": "1", "OBS_TRACE_STACKS": "slow,unknown"},
		{"OBS_TRACING_ENABLED": "1", "OBS_TRACE_DEBUG": "definitely"},
		{"OBS_TRACING_ENABLED": "1", "OBS_QUERY_TAIL_MAX_CONCURRENCY": "0"},
	}
	for _, env := range cases {
		_, err := ParseConfig(env)
		if err == nil {
			t.Fatalf("ParseConfig(%v) succeeded, want fail-fast error", env)
		}
	}
}

func TestConfigDisabledDoesNotSilentlyEnableForInvalidKnob(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "0", "OBS_INDEX_MAX_EVENTS": "0"})
	if err != nil {
		t.Fatalf("disabled config with invalid inactive knob should remain inspectable: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("Enabled = true, want false")
	}
	if !strings.Contains(cfg.DisabledReason, "disabled") {
		t.Fatalf("DisabledReason = %q", cfg.DisabledReason)
	}
}

func TestConfigDebugDefaultsRaiseIndexLimits(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_TRACE_DEBUG": "true"})
	if err != nil {
		t.Fatalf("ParseConfig debug: %v", err)
	}
	if cfg.IndexMaxEvents != 20000 || cfg.IndexMaxTraceEvents != 256 || cfg.IndexMaxThreadEvents != 512 {
		t.Fatalf("debug defaults not applied: %+v", cfg)
	}
}
