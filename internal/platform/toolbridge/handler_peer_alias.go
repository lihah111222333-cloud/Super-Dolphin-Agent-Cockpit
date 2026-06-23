package toolbridge

import "strings"

func legacyLSPName(canonical string) string {
	for legacy, short := range legacyLSPToolAliases {
		if short == canonical {
			return legacy
		}
	}
	return ""
}

// legacyOrchName 处理legacyorch名称。
func legacyOrchName(canonical string) string {
	switch strings.TrimSpace(canonical) {
	case "launch_agent":
		return "orchestration_launch_agent"
	case "send_message":
		return "orchestration_send_message"
	case "stop_agent":
		return "orchestration_stop_agent"
	case "recover_agent":
		return "orchestration_recover_agent"
	case "interrupt_agent":
		return "orchestration_interrupt_agent"
	case "list_agents":
		return "orchestration_list_agents"
	case "get_agent_report":
		return "orchestration_get_agent_report"
	case "get_agent_reports":
		return "orchestration_get_agent_reports"
	default:
		return ""
	}
}
