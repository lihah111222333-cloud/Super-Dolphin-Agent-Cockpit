package tools

import "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"

var applyOpsOpEnum = []string{
	string(nodeexec.OpKindUpdateDAG),
	string(nodeexec.OpKindAddNode),
	string(nodeexec.OpKindUpdateNode),
	string(nodeexec.OpKindRemoveNode),
}

func applyOpsOpSchema() Schema {
	return ObjectSchema(map[string]Schema{
		"op": EnumStringSchema("Operation discriminator.", applyOpsOpEnum...),
		"node": ObjectSchema(map[string]Schema{
			"node_key":   StringSchema("Node key for add_node."),
			"title":      StringSchema("Node title for add_node."),
			"node_type":  EnumStringSchema("Node type for add_node.", "agent", "automation", "hybrid"),
			"depends_on": ArraySchema(StringSchema("Dependency node key."), "Dependency node keys."),
			"config":     RawObjectSchema("Optional node config for add_node."),
		}, "node_key", "title", "node_type"),
		"node_key": StringSchema("Target node key for update_node/remove_node."),
		"patch":    RawObjectSchema("Patch object for update_dag or update_node."),
	}, "op")
}
