package tools

// coreAgentOutputSchema 声明核心 agent 工具的封闭输出契约；未知工具立即拒绝。
func coreAgentOutputSchema(name string) Schema {
	s := StringSchema("")
	b := BooleanSchema("")
	i := IntegerSchema("")
	truth := Schema{"type": "boolean", "const": true}
	switch name {
	case "launch_agent":
		return ObjectSchema(map[string]Schema{"success": truth, "agent_id": s, "status": EnumStringSchema("", "launching", "existing"), "launch_id": s, "thread_id": s}, "success", "agent_id", "status")
	case "send_message":
		plain := ObjectSchema(map[string]Schema{"success": truth, "agent_id": s}, "success", "agent_id")
		wait := ObjectSchema(map[string]Schema{"success": truth, "agent_id": s, "submitted": truth, "previous_report_seq": i, "report": agentReportOutputSchema()}, "success", "agent_id", "submitted", "previous_report_seq", "report")
		return Schema{"type": "object", "oneOf": []Schema{plain, wait}}
	case "stop_agent":
		plain := ObjectSchema(map[string]Schema{"success": truth, "agent_id": s, "archived": b}, "success", "agent_id", "archived")
		wait := ObjectSchema(map[string]Schema{"success": truth, "agent_id": s, "archived": b, "stopped": truth, "state": s}, "success", "agent_id", "archived", "stopped", "state")
		return Schema{"type": "object", "oneOf": []Schema{plain, wait}}
	case "recover_agent":
		return agentSnapshotOutputSchema()
	case "interrupt_agent":
		return ObjectSchema(map[string]Schema{"success": truth, "agent_id": s, "interrupted": truth, "state": s, "active_turn_id": Schema{"type": "string", "const": ""}}, "success", "agent_id", "interrupted", "state", "active_turn_id")
	case "list_agents":
		return ObjectSchema(map[string]Schema{"agents": ArraySchema(agentSnapshotOutputSchema(), ""), "data": ArraySchema(agentSnapshotOutputSchema(), ""), "total": i, "showing": i, "truncated": b, "hint": s}, "agents", "data", "total", "showing", "truncated")
	case "get_agent_report":
		return agentReportOutputSchema()
	case "get_agent_reports":
		item := ObjectSchema(map[string]Schema{"agent_id": s, "state": s, "report": s, "report_seq": i, "updated_at": s, "ok": b, "error": s}, "agent_id", "state", "report", "report_seq", "ok", "error")
		return ObjectSchema(map[string]Schema{"status": EnumStringSchema("", "completed", "partial", "timed_out"), "results": ArraySchema(item, ""), "completed": i, "pending": i, "timed_out": b}, "status", "results", "completed", "pending", "timed_out")
	default:
		// 非法 type 使注册期 schema 编译明确失败，禁止未登记工具取得空契约。
		return Schema{"type": "undeclared_agent_tool_" + name}
	}
}

// agentReportOutputSchema 与持久化报告 DTO 的 JSON 字段保持一致。
func agentReportOutputSchema() Schema {
	s := StringSchema("")
	metadata := ObjectSchema(map[string]Schema{"requester_ids": ArraySchema(s, "")})
	return ObjectSchema(map[string]Schema{"agent_id": s, "report": s, "report_seq": IntegerSchema(""), "updated_at": s, "state": s, "metadata": metadata}, "agent_id", "report", "report_seq", "state")
}

// nullableAgentOutputSchema 为真实可空 DTO 字段声明 null 分支。
func nullableAgentOutputSchema(schema Schema) Schema {
	return Schema{"anyOf": []Schema{schema, {"type": "null"}}}
}

// agentSnapshotOutputSchema 覆盖快照及嵌套看板 DTO，拒绝未登记字段。
func agentSnapshotOutputSchema() Schema {
	s := StringSchema("")
	i := IntegerSchema("")
	assignment := ObjectSchema(map[string]Schema{"title": s, "description": s, "assignedAt": s}, "title", "description", "assignedAt")
	progress := ObjectSchema(map[string]Schema{"status": s, "currentStep": nullableAgentOutputSchema(s), "completedSteps": nullableAgentOutputSchema(i), "totalSteps": nullableAgentOutputSchema(i), "updatedAt": s}, "status", "currentStep", "completedSteps", "totalSteps", "updatedAt")
	outcome := ObjectSchema(map[string]Schema{"kind": s, "summary": s, "reason": s, "code": s, "recoverable": nullableAgentOutputSchema(BooleanSchema("")), "completedAt": s}, "kind", "recoverable", "completedAt")
	return ObjectSchema(map[string]Schema{
		"id": s, "agent_id": s, "launch_id": s, "name": s, "parent_id": s, "port": i, "port_source": s, "pid": i, "thread_id": s, "active_turn_id": s, "cwd": s, "state": s, "provider": s, "provider_source": s, "last_report": s, "created_at": s, "updated_at": s,
		"assignment": nullableAgentOutputSchema(assignment), "progress": progress, "outcome": nullableAgentOutputSchema(outcome),
	}, "id", "agent_id", "name", "port", "thread_id", "cwd", "state", "updated_at", "assignment", "progress", "outcome")
}
