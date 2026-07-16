package shared

import "strings"

// RawTerminalOutcome 是 provider raw adapter 共用的 success/status 严格配对结果。
type RawTerminalOutcome struct {
	Success       bool
	Status        string
	Cause         string
	ContractError string
}

// ResolveRawTerminalOutcome 校验 provider raw terminal，不接受缺失、错类型、未知或互斥组合。
func ResolveRawTerminalOutcome(data any) RawTerminalOutcome {
	payload, ok := data.(map[string]any)
	if !ok {
		return rawTerminalContractError("terminal payload is not an object")
	}
	success, ok := payload["success"].(bool)
	if !ok {
		return rawTerminalContractError("missing or non-boolean success")
	}
	status, ok := payload["status"].(string)
	if !ok || strings.TrimSpace(status) == "" {
		return rawTerminalContractError("missing or non-string status")
	}
	return resolveRawTerminalPair(success, strings.ToLower(strings.TrimSpace(status)))
}

// resolveRawTerminalPair 把 provider 的 success/status 对折叠成唯一终态，冲突组合必须返回契约错误。
func resolveRawTerminalPair(success bool, status string) RawTerminalOutcome {
	switch status {
	case "completed":
		if success {
			return RawTerminalOutcome{Success: true, Status: status}
		}
	case "failed":
		if !success {
			return RawTerminalOutcome{Status: status}
		}
	case "interrupted", "cancelled":
		if !success {
			return RawTerminalOutcome{Status: status, Cause: "provider"}
		}
	default:
		return rawTerminalContractError("unknown status " + status)
	}
	return rawTerminalContractError("conflicting success and status")
}

func rawTerminalContractError(message string) RawTerminalOutcome {
	return RawTerminalOutcome{Status: "failed", ContractError: message}
}
