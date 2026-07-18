package shared

import (
	"fmt"
	"strings"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// RawTerminalOutcome 是 provider raw adapter 共用的 success/status 严格配对结果。
type RawTerminalOutcome = providerdto.TerminalOutcome

// RawTermination 是 provider raw cancel/interruption 的严格 cause/request 配对结果。
type RawTermination struct {
	Cause         string
	RequestID     string
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
	outcome := resolveRawTerminalPair(success, strings.ToLower(strings.TrimSpace(status)))
	if outcome.ContractError != "" {
		return outcome
	}
	return resolveRawTerminalDependencies(payload, outcome)
}

// resolveRawTerminalDependencies 把 cause/request 依赖规则附加到已验证的 success/status 结果。
func resolveRawTerminalDependencies(payload map[string]any, outcome RawTerminalOutcome) RawTerminalOutcome {
	fallbackCause := ""
	if outcome.Status == "interrupted" || outcome.Status == "cancelled" {
		fallbackCause = "provider"
	}
	termination := ResolveRawTermination(payload, fallbackCause)
	if termination.ContractError != "" {
		return rawTerminalContractError(termination.ContractError)
	}
	if fallbackCause == "" && (termination.Cause != "" || termination.RequestID != "") {
		return rawTerminalContractError("non-cancel terminal contains termination fields")
	}
	outcome.Cause = termination.Cause
	outcome.RequestID = termination.RequestID
	return outcome
}

// ResolveRawTermination 校验 terminationCause 与 terminationRequestId 的依赖和互斥规则。
func ResolveRawTermination(data any, fallbackCause string) RawTermination {
	payload, ok := data.(map[string]any)
	if !ok {
		return rawTerminationContractError("terminal payload is not an object")
	}
	cause, err := terminalStringAlias(payload, "termination_cause", "terminationCause")
	if err != nil {
		return rawTerminationContractError(err.Error())
	}
	requestID, err := terminalStringAlias(payload, "termination_request_id", "terminationRequestId")
	if err != nil {
		return rawTerminationContractError(err.Error())
	}
	if cause == "" {
		cause = strings.TrimSpace(fallbackCause)
	}
	return validateRawTermination(cause, requestID)
}

// validateRawTermination 固定 user_request 必须带 requestId，provider/system 必须不带。
func validateRawTermination(cause, requestID string) RawTermination {
	switch cause {
	case "":
		if requestID == "" {
			return RawTermination{}
		}
	case "user_request":
		if requestID != "" {
			return RawTermination{Cause: cause, RequestID: requestID}
		}
	case "provider", "system":
		if requestID == "" {
			return RawTermination{Cause: cause}
		}
	default:
		return rawTerminationContractError("unknown termination cause " + cause)
	}
	return rawTerminationContractError("termination cause and request id conflict")
}

// terminalStringAlias 严格读取 snake/camel alias，并拒绝错类型、空值和冲突值。
func terminalStringAlias(payload map[string]any, keys ...string) (string, error) {
	var resolved string
	for _, key := range keys {
		value, exists := payload[key]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("%s is missing or non-string", key)
		}
		text = strings.TrimSpace(text)
		if resolved != "" && resolved != text {
			return "", fmt.Errorf("conflicting %s aliases", key)
		}
		resolved = text
	}
	return resolved, nil
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
			return RawTerminalOutcome{Status: status}
		}
	default:
		return rawTerminalContractError("unknown status " + status)
	}
	return rawTerminalContractError("conflicting success and status")
}

func rawTerminalContractError(message string) RawTerminalOutcome {
	return RawTerminalOutcome{Status: "failed", ContractError: message}
}

func rawTerminationContractError(message string) RawTermination {
	return RawTermination{ContractError: message}
}
