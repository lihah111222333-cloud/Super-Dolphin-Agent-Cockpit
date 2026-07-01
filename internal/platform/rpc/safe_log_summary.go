package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

var rpcSensitiveParamFields = map[string]struct{}{
	"prompt":                {},
	"baseinstructions":      {},
	"developerinstructions": {},
	"input":                 {},
	"content":               {},
	"attachments":           {},
	"config":                {},
	"headers":               {},
	"token":                 {},
	"cookie":                {},
	"authorization":         {},
	"apikey":                {},
}

// SafeRPCLogSummary 生成可写入日志的 RPC 参数摘要。
// 摘要只保留 ID 类字段、字段名、字节数和稳定 hash，避免 prompt/header/token 等内容进入日志。
func SafeRPCLogSummary(method string, params string) string {
	params = strings.TrimSpace(params)
	if params == "" {
		return ""
	}

	summary := map[string]any{
		"method":     strings.TrimSpace(method),
		"raw_bytes":  len(params),
		"raw_sha256": stableRPCLogHash([]byte(params)),
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &raw); err != nil {
		summary["invalid_json"] = true
		return marshalRPCLogSummary(summary)
	}

	fields := make([]string, 0, len(raw))
	ids := map[string]string{}
	for key, value := range raw {
		fields = append(fields, key)
		if isSensitiveRPCParamField(key) {
			addRedactedRPCParamSummary(summary, key, value)
			continue
		}
		if isRPCIDField(key) {
			var id string
			if err := json.Unmarshal(value, &id); err == nil && strings.TrimSpace(id) != "" {
				ids[key] = strings.TrimSpace(id)
			}
		}
	}
	sort.Strings(fields)
	summary["fields"] = fields
	if len(ids) > 0 {
		summary["ids"] = ids
	}
	return marshalRPCLogSummary(summary)
}

func addRedactedRPCParamSummary(summary map[string]any, key string, value json.RawMessage) {
	summary[key+"_bytes"] = len(value)
	summary[key+"_sha256"] = stableRPCLogHash(value)
}

func isSensitiveRPCParamField(key string) bool {
	_, ok := rpcSensitiveParamFields[normalizeRPCParamField(key)]
	return ok
}

// isRPCIDField 只识别约定俗成的 ID 字段，避免把普通字符串参数当诊断 ID 写入日志。
func isRPCIDField(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || isSensitiveRPCParamField(key) {
		return false
	}
	lower := strings.ToLower(key)
	return lower == "id" ||
		strings.HasSuffix(key, "ID") ||
		strings.HasSuffix(key, "Id") ||
		strings.HasSuffix(lower, "_id") ||
		strings.HasSuffix(lower, "-id")
}

func normalizeRPCParamField(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")
	return key
}

func stableRPCLogHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func marshalRPCLogSummary(summary map[string]any) string {
	encoded, err := json.Marshal(summary)
	if err != nil {
		return ""
	}
	return string(encoded)
}
