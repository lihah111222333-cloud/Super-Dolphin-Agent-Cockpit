package kernel

import (
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
)

// ConfigString returns the first non-empty string value from cfg.
func ConfigString(cfg map[string]any, keys ...string) string {
	return configutil.ConfigString(cfg, keys...)
}

// StrictConfigString returns a required string value from cfg.
func StrictConfigString(cfg map[string]any, label string, keys ...string) (string, error) {
	return configutil.StrictString(cfg, label, keys...)
}

// SanitizeConfigString trims control characters from a config string.
func SanitizeConfigString(value string) string {
	return configutil.SanitizeConfigString(value)
}

// ConfigStringMap converts a loosely typed config value to string map.
func ConfigStringMap(raw any) map[string]string {
	return configutil.StringMap(raw)
}

// ConfigStringSlice returns a normalized string slice from cfg.
func ConfigStringSlice(cfg map[string]any, keys ...string) []string {
	return configutil.ConfigStringSlice(cfg, keys...)
}

// NormalizeConfigStringSlice normalizes a loosely typed list of strings.
func NormalizeConfigStringSlice(values any) []string {
	return configutil.NormalizeConfigStringSlice(values)
}

// TrimConfigStringValues trims a loosely typed string value slice.
func TrimConfigStringValues(values []any) []string {
	return configutil.TrimConfigStringValues(values)
}

// SplitConfigStringSlice splits a delimited config string.
func SplitConfigStringSlice(value string) []string {
	return configutil.SplitConfigStringSlice(value)
}

// TrimStrings trims and drops empty strings.
func TrimStrings(values []string) []string {
	return configutil.TrimStrings(values)
}
