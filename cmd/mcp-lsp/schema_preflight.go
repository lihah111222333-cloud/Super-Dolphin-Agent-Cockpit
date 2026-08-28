package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// stableToolSchemaPreflightError 将调用前 schema 拒绝收敛为固定 code、字段集合和不可重试元数据。
func stableToolSchemaPreflightError(def toolDefinition, value any, cause error) error {
	unknown := unknownManifestFields(def.Manifest.Schema, value)
	allowed := manifestAllowedFields(def.Manifest.Schema)
	meta := map[string]any{
		"unknown_fields": unknown,
		"allowed_fields": allowed,
		"retryable":      false,
	}
	hint := "next: use the documented schema; remove unknown fields and fix validation errors; retryable=false"
	if len(unknown) > 0 {
		hint = fmt.Sprintf("next: remove unknown fields and retry with the documented schema; unknown_fields=%s; allowed_fields=%s; retryable=false", strings.Join(unknown, ","), strings.Join(allowed, ","))
	}
	return &common.CodedToolError{Err: cause, Code: "invalid_params", Retryable: false, Hint: hint, Meta: meta}
}

func unknownManifestFields(raw schema, value any) []string {
	arguments, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	properties, ok := raw["properties"].(map[string]any)
	if !ok {
		return nil
	}
	unknown := make([]string, 0)
	for field := range arguments {
		if _, declared := properties[field]; !declared {
			unknown = append(unknown, field)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func manifestAllowedFields(raw schema) []string {
	properties, ok := raw["properties"].(map[string]any)
	if !ok {
		return []string{}
	}
	allowed := make([]string, 0, len(properties))
	for field := range properties {
		allowed = append(allowed, field)
	}
	sort.Strings(allowed)
	return allowed
}
