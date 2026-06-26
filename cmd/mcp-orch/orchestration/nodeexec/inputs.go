package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 本文件负责把节点 inputs 配置装载成 executor 可消费的运行时上下文。
// agent 路径会把上游结果和 sharedfile 内容拼到 first_turn 前，automation 路径复用同一读取语义注入 args.__inputs。

// ErrInputsValidation 是 inputs 装载路径被判为「节点配置错误」的 sentinel。
// 例：from_nodes 引用不存在的 node_key；from_sharedfiles 引用不存在的 path；
// 未接通 reader 但节点 inputs 又引用了。assembleInputs 返回的 *InputsError 通过
// Unwrap 把它暴露出来，调用方可以用 errors.Is(err, ErrInputsValidation) 断言。
var ErrInputsValidation = errors.New("nodeexec: inputs validation")

// InputsError 是 assembleInputs 的结构化错误返回。
// Class 决定 NodeOutcome.FailureClass，Err 保留底层原因；Unwrap 让 errors.Is/As 仍能穿透到 sentinel。
type InputsError struct {
	Class FailureClass
	Err   error
}

// Error 返回底层错误文本；nil receiver 返回空串，避免日志路径 panic。
func (e *InputsError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap 返回底层错误，供调用方用 errors.Is/As 识别 validation sentinel 或具体错误类型。
func (e *InputsError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// newInputsValidationError 是构造 validation 类 InputsError 的小工厂；
// 帮 loadFromNodes / loadFromSharedfiles 等保持简洁。
func newInputsValidationError(format string, args ...any) *InputsError {
	return &InputsError{
		Class: FailureClassValidation,
		Err:   fmt.Errorf("%w: "+format, append([]any{ErrInputsValidation}, args...)...),
	}
}

// newInputsInfraError 是构造基础设施类 InputsError 的小工厂；
// class 由 classifyAgentLaunchError 决定（默认 transient）。
func newInputsInfraError(format string, args ...any) *InputsError {
	// 取 args 中最后一个 error 作 classify 来源；按当前调用只在 reader / store
	// 报错路径用，约定调用方把底层 err 放最后即可。
	var underlying error
	if len(args) > 0 {
		if e, ok := args[len(args)-1].(error); ok {
			underlying = e
		}
	}
	return &InputsError{
		Class: classifyAgentLaunchError(underlying),
		Err:   fmt.Errorf(format, args...),
	}
}

// assembleInputs 按 cfg.Inputs 拉取上游节点结果和 sharedfile 内容，拼成 LaunchRequest.Prompt 前缀。
// 空 prefix 表示无需注入；失败时返回带 FailureClass 的 InputsError，让 agent executor 按节点失败处理。
func (e *AgentExecutor) assembleInputs(
	ctx context.Context,
	cfg *AgentNodeConfig,
	runCtx RunContext,
	node Node,
) (string, *InputsError) {
	if cfg == nil || !inputsConfigured(&cfg.Inputs) {
		return "", nil
	}
	dagKey := resolveDagKey(runCtx, node)

	sections, ierr := collectInputSections(ctx, &cfg.Inputs, dagKey, runCtx)
	if ierr != nil {
		return "", ierr
	}
	if len(sections) == 0 {
		return "", nil
	}
	return strings.Join(sections, "\n\n"), nil
}

// inputsConfigured 报告 cfg.Inputs 是否声明了需要读取的来源。
func inputsConfigured(in *InputsConfig) bool {
	if in == nil {
		return false
	}
	return len(in.FromNodes) > 0 || len(in.FromSharedfiles) > 0
}

// resolveDagKey 优先使用 dispatcher 传入的 dag_key；缺失时才回退到节点快照字段。
func resolveDagKey(runCtx RunContext, node Node) string {
	if k := strings.TrimSpace(runCtx.DagKey); k != "" {
		return k
	}
	return strings.TrimSpace(node.DagKey)
}

// collectInputSections 依次拉取 from_nodes / from_sharedfiles 两节，返回
// 非空节的拼接列表。拆出的意义：避免 assembleInputs CC 超阈。
//
// inputs 数据源均走 RunContext（PrevResults / SharedFileReader），agent 与 automation 共享同一失败语义。
func collectInputSections(
	ctx context.Context,
	in *InputsConfig,
	dagKey string,
	runCtx RunContext,
) ([]string, *InputsError) {
	var sections []string
	if len(in.FromNodes) > 0 {
		section, ierr := loadFromNodes(dagKey, in.FromNodes, runCtx.PrevResults)
		if ierr != nil {
			return nil, ierr
		}
		if section != "" {
			sections = append(sections, section)
		}
	}
	if len(in.FromSharedfiles) > 0 {
		section, ierr := loadFromSharedfiles(ctx, in.FromSharedfiles, runCtx.SharedFileReader)
		if ierr != nil {
			return nil, ierr
		}
		if section != "" {
			sections = append(sections, section)
		}
	}
	return sections, nil
}

// loadFromNodes 拉取 cfg.Inputs.FromNodes 列出的 prev node result。
//
// prev == nil 但配置非空 → validation（避免遗漏注入需求）。
// node_key 不存在 → validation。
// result 为空 → 注入「(empty)」占位（上游可能未配 outputs.to_node_result）。
func loadFromNodes(
	dagKey string,
	nodeKeys []string,
	prev map[string]json.RawMessage,
) (string, *InputsError) {
	if prev == nil {
		return "", newInputsValidationError("inputs.from_nodes set but RunContext.PrevResults not wired")
	}
	if dagKey == "" {
		return "", newInputsValidationError("cannot resolve from_nodes without dag_key (runCtx + node both empty)")
	}

	var b strings.Builder
	b.WriteString("[inputs.from_nodes]\n")
	for _, raw := range nodeKeys {
		key := strings.TrimSpace(raw)
		if key == "" {
			// 空字符串来自用户列表里的无效项，忽略它可避免生成无法引用的空 node_key。
			continue
		}
		result, ok := prev[key]
		if !ok {
			return "", newInputsValidationError("from_nodes references unknown node_key %q", key)
		}
		fmt.Fprintf(&b, "## node:%s\n", key)
		if len(result) == 0 {
			b.WriteString("(empty)\n")
			continue
		}
		b.Write(result)
		if !strings.HasSuffix(string(result), "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

// loadFromSharedfiles 拉取 cfg.Inputs.FromSharedfiles 列出的文件内容。
//
// reader == nil 但配置非空 → validation。文件不存在 → validation。读失败 →
// classifyAgentLaunchError（一般 transient）。
func loadFromSharedfiles(
	ctx context.Context,
	paths []string,
	reader SharedFileReader,
) (string, *InputsError) {
	if reader == nil {
		return "", newInputsValidationError("inputs.from_sharedfiles set but RunContext.SharedFileReader not wired")
	}

	var b strings.Builder
	b.WriteString("[inputs.from_sharedfiles]\n")
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		content, exists, err := reader.ReadSharedFile(ctx, path)
		if err != nil {
			return "", newInputsInfraError("read sharedfile %q: %w", path, err)
		}
		if !exists {
			return "", newInputsValidationError("from_sharedfiles references unknown path %q", path)
		}
		fmt.Fprintf(&b, "## sharedfile:%s\n", path)
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

func artifactOutputContract(target *ArtifactTarget) string {
	if target == nil {
		return ""
	}
	sourceTool := strings.TrimSpace(target.SourceTool)
	pathField := strings.TrimSpace(target.SourcePathField)
	if sourceTool == "" || pathField == "" {
		return ""
	}
	return fmt.Sprintf(`[outputs.to_artifact]
This DAG node imports a local artifact from tool %q using result field %q.
After a successful tool call, the final response must be exactly one JSON object like {"success":true,"source_tool":%q,"%s":"<path>"}.
Do not return only natural language and do not invent a path. If the tool cannot run or required inputs are missing, return {"success":false,"source_tool":%q,"error":"<reason>"} without %q.`, sourceTool, pathField, sourceTool, pathField, sourceTool, pathField)
}

// composePrompt 拼接 inputs 前缀、artifact 输出契约与 first_turn。
//   - 两者都为空 → 返回空串，表示未指定 Prompt（以免 service 层以 「空填充」 处理）。
//   - prefix / artifactContract 非空 + first_turn 非空 → 前缀段 + "\n\n[first_turn]\n" + first_turn。
//   - 仅 prefix 非空 → 返回 prefix（末尾别加叠加 first_turn header）。
//   - 仅 first_turn 非空 → 原样返回 first_turn，保证没有 inputs 时不改变已有 prompt。
func composePrompt(prefix, artifactContract, firstTurn string) string {
	prefix = strings.TrimRight(prefix, "\n")
	artifactContract = strings.TrimRight(artifactContract, "\n")
	var sections []string
	if prefix != "" {
		sections = append(sections, prefix)
	}
	if artifactContract != "" {
		sections = append(sections, artifactContract)
	}
	if firstTurn != "" {
		if len(sections) == 0 {
			sections = append(sections, firstTurn)
		} else {
			sections = append(sections, "[first_turn]\n"+firstTurn)
		}
	}
	return strings.Join(sections, "\n\n")
}
