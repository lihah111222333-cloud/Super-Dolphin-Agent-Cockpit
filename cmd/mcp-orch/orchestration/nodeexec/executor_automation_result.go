package nodeexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// finalizeAutomationOutcome 将命令执行结果物化为配置声明的输出。
// sharedfile 写入失败必须让节点失败，避免 node.result 指向不存在或未落盘的产物。
func finalizeAutomationOutcome(
	ctx context.Context,
	cfg *AutomationNodeConfig,
	node Node,
	runCtx RunContext,
	result AutomationCommandResult,
) (NodeOutcome, error) {
	payload, err := automationCommandResultPayload(result)
	if err != nil {
		return failedAutomationOutcome(FailureClassValidation, "marshal automation result: "+err.Error()), nil
	}
	outcome := NodeOutcome{Status: NodeStatusDone}
	if shouldEmitFullNodeResult(cfg.Outputs) {
		if failure := enforceNodeResultSizeCap(payload); failure != nil {
			return *failure, nil
		}
		outcome.Result = payload
	} else if shouldEmitSharedfileEnvelope(cfg.Outputs) {
		envelope, failure := buildAutomationSharedfileEnvelope(cfg.Outputs, node, runCtx)
		if failure != nil {
			return *failure, nil
		}
		outcome.Result = envelope
	}
	if failure := writeAutomationSharedfile(ctx, cfg, runCtx, result); failure != nil {
		return *failure, nil
	}
	return outcome, nil
}

// automationCommandResultPayload 生成可持久化的 command 结果。
// Args 只用于 runner 入参，不能落入 node.result；其他字段再统一递归脱敏后才允许公开。
func automationCommandResultPayload(result AutomationCommandResult) (json.RawMessage, error) {
	payload, err := json.Marshal(struct {
		CardKey  string `json:"card_key"`
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout,omitempty"`
		Stderr   string `json:"stderr,omitempty"`
		Command  string `json:"command,omitempty"`
	}{
		CardKey:  result.CardKey,
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		Command:  result.Command,
	})
	if err != nil {
		return nil, err
	}
	return ScrubAutomationResultPayload(payload), nil
}

// ScrubAutomationResultPayload 清理对外或落库的 automation result JSON。
// 它删除 args/__inputs，并递归遮蔽 token、authorization、cookie、secret、password、api_key 等敏感键。
func ScrubAutomationResultPayload(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return json.RawMessage(`{"redacted":true,"reason":"invalid_result_json"}`)
	}
	cleaned, err := json.Marshal(scrubAutomationResultValue(value))
	if err != nil {
		return json.RawMessage(`{"redacted":true,"reason":"marshal_scrubbed_result"}`)
	}
	return cleaned
}

func scrubAutomationResultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return scrubAutomationResultObject(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, scrubAutomationResultValue(item))
		}
		return out
	case string:
		return redactSensitiveText(typed)
	default:
		return value
	}
}

func scrubAutomationResultObject(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		if shouldDropAutomationResultKey(key) {
			continue
		}
		if isSensitiveAutomationResultKey(key) {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = scrubAutomationResultValue(value)
	}
	return out
}

func shouldDropAutomationResultKey(key string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(key))
	return trimmed == "args" || trimmed == "__inputs"
}

func isSensitiveAutomationResultKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, marker := range []string{"token", "authorization", "cookie", "secret", "password", "apikey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// NodeResultSizeCapBytes 是 outputs.to_node_result 写入 task_dag_nodes.result 前的硬上限。
// 超过 4KB 的命令结果必须改走 outputs.to_sharedfile，避免持久化列和节点列表承载大块原始输出。
const NodeResultSizeCapBytes = 4096

// enforceNodeResultSizeCap 在 node.result 落库前执行大小检查。
// len(payload) <= 4096 放行；超过上限返回 validation outcome，并提示调用方改用 outputs.to_sharedfile。
func enforceNodeResultSizeCap(payload []byte) *NodeOutcome {
	if len(payload) <= NodeResultSizeCapBytes {
		return nil
	}
	outcome := failedAutomationOutcome(
		FailureClassValidation,
		fmt.Sprintf(
			"result exceeds 4KB size cap (%d > %d bytes), configure outputs.to_sharedfile (ADR-006)",
			len(payload), NodeResultSizeCapBytes,
		),
	)
	return &outcome
}

// shouldEmitFullNodeResult 判定是否把完整命令结果写入 NodeOutcome.Result。
// 未声明 sharedfile 时保留完整结果，避免旧配置丢失输出；声明 sharedfile 且未显式要求 node_result 时只写轻量 envelope。
func shouldEmitFullNodeResult(out OutputsConfig) bool {
	if out.ToNodeResult {
		return true
	}
	return automationSharedfilePath(out) == ""
}

func shouldEmitSharedfileEnvelope(out OutputsConfig) bool {
	if automationSharedfilePath(out) == "" {
		return false
	}
	return out.NodeResultEnvelope == nil || *out.NodeResultEnvelope
}

func automationSharedfilePath(out OutputsConfig) string {
	if out.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(out.ToSharedfile.Path)
}

// buildAutomationSharedfileEnvelope 构建写入 node.result 的轻量 sharedfile envelope。
// envelope 必须带 dag/run/node 三元组，缺任一上下文都会失败，避免 UI 指向不可审计的输出路径。
func buildAutomationSharedfileEnvelope(out OutputsConfig, node Node, runCtx RunContext) (json.RawMessage, *NodeOutcome) {
	path := automationSharedfilePath(out)
	if path == "" {
		return nil, nil
	}
	dagKey, nodeKey := resolveAutomationEnvelopeKeys(node, runCtx)
	if dagKey == "" || nodeKey == "" || runCtx.RunID <= 0 {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"outputs.to_sharedfile requires dag_key/run_id/node_key for node result envelope")
		return nil, &outcome
	}
	payload, err := json.Marshal(struct {
		Kind       string `json:"kind"`
		Path       string `json:"path"`
		Dag        string `json:"dag"`
		Run        int64  `json:"run"`
		Node       string `json:"node"`
		Sharedfile struct {
			Path string `json:"path"`
		} `json:"sharedfile"`
	}{
		Kind: "sharedfile",
		Path: path,
		Dag:  dagKey,
		Run:  runCtx.RunID,
		Node: nodeKey,
		Sharedfile: struct {
			Path string `json:"path"`
		}{Path: path},
	})
	if err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation, "marshal automation sharedfile envelope: "+err.Error())
		return nil, &outcome
	}
	return payload, nil
}

func resolveAutomationEnvelopeKeys(node Node, runCtx RunContext) (string, string) {
	dagKey := strings.TrimSpace(runCtx.DagKey)
	if dagKey == "" {
		dagKey = strings.TrimSpace(node.DagKey)
	}
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if nodeKey == "" {
		nodeKey = strings.TrimSpace(node.NodeKey)
	}
	return dagKey, nodeKey
}

// writeAutomationSharedfile 在配置了 outputs.to_sharedfile 时，把 result.Stdout 写入 sharedfile。
// 设计选型：仅写 stdout——这是 command_card 输出的「有意义载荷」；如需完整 result（包括 stderr / exit）
// 可后续在 SharedfileTarget 上加 mode 字段扩展。写入失败 → validation（任务约定）。
func writeAutomationSharedfile(
	ctx context.Context,
	cfg *AutomationNodeConfig,
	runCtx RunContext,
	result AutomationCommandResult,
) *NodeOutcome {
	target := cfg.Outputs.ToSharedfile
	if target == nil {
		return nil
	}
	path := strings.TrimSpace(target.Path)
	if path == "" {
		return nil
	}
	if runCtx.SharedFileWriter == nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			"outputs.to_sharedfile configured but SharedFileWriter not wired in RunContext")
		return &outcome
	}
	content := stripAutomationControlFieldsBeforePromptReuse(result.Stdout)
	if writer, ok := runCtx.SharedFileWriter.(SharedFileMetadataWriter); ok {
		err := writer.WriteSharedFileWithMetadata(ctx, SharedFileWriteRequest{
			Path:          path,
			Content:       content,
			ContentType:   "text/plain",
			OwnerNode:     automationSharedFileOwnerNode(runCtx),
			ProducerActor: automationSharedFileProducerActor(runCtx),
			RunID:         runCtx.RunID,
		})
		if err != nil {
			outcome := failedAutomationOutcome(FailureClassValidation,
				fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
			return &outcome
		}
		return nil
	}
	if err := runCtx.SharedFileWriter.WriteSharedFile(ctx, path, content); err != nil {
		outcome := failedAutomationOutcome(FailureClassValidation,
			fmt.Sprintf("outputs.to_sharedfile[%q]: %v", path, err))
		return &outcome
	}
	return nil
}

func automationSharedFileOwnerNode(runCtx RunContext) string {
	dagKey := strings.TrimSpace(runCtx.DagKey)
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if dagKey == "" || nodeKey == "" {
		return ""
	}
	return dagKey + "/" + nodeKey
}

func automationSharedFileProducerActor(runCtx RunContext) string {
	nodeKey := strings.TrimSpace(runCtx.NodeKey)
	if nodeKey == "" {
		return ""
	}
	return "automation:" + nodeKey
}
