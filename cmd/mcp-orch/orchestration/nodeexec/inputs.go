package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// F1.2 —— inputs 装载路径。拆出到独立文件是为了：
//   - 避免 executor_agent.go::Execute 本体圈复杂度超阈（与 F1.5 spawnWriteback 同思路）；
//   - 拼接格式 「inputs prefix + first_turn」 是 F1.2 的设计决策（蓝图 v2 仅给出 schema，
//     未锁 prompt 拼接顺序），集中在一处便于 H7 summarization 接管后重构。
//
// F1.2 inputs assembly is isolated here so the Execute body stays under the
// cyclomatic-complexity guard, and so the prompt-prefix format (a F1.2 design
// decision the blueprint did not pin down) lives in one place that H7
// summarization can later rewrite.

// ErrInputsValidation 是 inputs 装载阶段被判为「节点配置错误」的 sentinel。
// 例：from_nodes 引用不存在的 node_key；from_sharedfiles 引用不存在的 path；
// 未接通 reader 但节点 inputs 又引用了。assembleInputs 返回的 *InputsError 通过
// Unwrap 把它暴露出来，调用方可以用 errors.Is(err, ErrInputsValidation) 断言。
//
// ErrInputsValidation marks inputs-stage failures that should map to
// FailureClass=validation (missing node_key / missing sharedfile / inputs
// referenced but reader not wired). Unwrapped through *InputsError so
// callers can errors.Is on it.
var ErrInputsValidation = errors.New("nodeexec: inputs validation")

// InputsError 是 assembleInputs 的结构化错误返回。
// 收敛 batch 第 4 项：把原来三参解构 (string, error, FailureClass) 升级为
// (string, *InputsError)，让 errors.As 一次拿到 FailureClass，并保留 Unwrap
// 链给 errors.Is(ErrInputsValidation) 用。
//
// 设计要点：
//   - Class 永远非空（FailureClassValidation / Transient 等）；
//   - Err 是底层因（fmt.Errorf 包装或 sentinel）；
//   - 实现 error 接口透传 Err.Error()；Unwrap 透传 Err，errors.Is/As 自然穿透。
//
// InputsError is the structured error returned by assembleInputs after the
// port-unification batch. Callers run errors.As(err, &inputsErr) once to get
// both the message and the FailureClass; errors.Is(err, ErrInputsValidation)
// still works through the Unwrap chain.
type InputsError struct {
	Class FailureClass
	Err   error
}

// Error implements error. nil-safe (returns "").
func (e *InputsError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap exposes the wrapped error so errors.Is(ErrInputsValidation) and
// errors.As(target) keep working through the InputsError shell.
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

// assembleInputs 按 cfg.Inputs 拉取 prev nodes result + sharedfiles，拼成一个
// 将注入到 LaunchRequest.Prompt 头部的前缀字符串。
//
// 返回值：
//   - prefix：拼接后的文本。空表示无需注入（cfg.Inputs 为空）。
//   - inputsErr：失败原因（结构化，含 Class）；nil 表示装载成功。
//
// assembleInputs gathers configured prev results / sharedfiles into a single
// prompt prefix. Returns (prefix, nil) on success. On failure returns
// ("", *InputsError) with Class set to validation for missing refs / missing
// readers, transient/quota etc. for infra errors flowing through
// classifyAgentLaunchError.
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

// inputsConfigured 报告 cfg.Inputs 是否含需要拉取的来源。
// inputsConfigured reports whether the inputs config has anything to load.
func inputsConfigured(in *InputsConfig) bool {
	if in == nil {
		return false
	}
	return len(in.FromNodes) > 0 || len(in.FromSharedfiles) > 0
}

// resolveDagKey 优先从 runCtx 取 dag_key，缺失时回退到 node 自带字段。
// resolveDagKey prefers the runCtx-supplied dag_key, falling back to the node's
// own field (mirrors resolveSpawnKeys in executor_agent.go).
func resolveDagKey(runCtx RunContext, node Node) string {
	if k := strings.TrimSpace(runCtx.DagKey); k != "" {
		return k
	}
	return strings.TrimSpace(node.DagKey)
}

// collectInputSections 依次拉取 from_nodes / from_sharedfiles 两节，返回
// 非空节的拼接列表。拆出的意义：避免 assembleInputs CC 超阈。
//
// inputs 数据源均走 RunContext（PrevResults / SharedFileReader），收敛 batch 后与
// AutomationExecutor 共享同一端口语义。
//
// collectInputSections loads each configured source and returns the non-empty
// sections; split out to keep assembleInputs under the cyclomatic budget.
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
// prev == nil 但配置非空 → validation（避免默默吞掉注入需求）。
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
			// 空串 → 静默跳过，不是 validation 错（取其尽量汇入意图）。
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

// composePrompt 拼接 inputs 前缀、artifact 输出契约与 first_turn。F1.2 设计决策：
//   - 两者都为空 → 返回空串，表示未指定 Prompt（以免 service 层以 「空填充」 处理）。
//   - prefix / artifactContract 非空 + first_turn 非空 → 前缀段 + "\n\n[first_turn]\n" + first_turn。
//   - 仅 prefix 非空 → 返回 prefix（末尾别加叠加 first_turn header）。
//   - 仅 first_turn 非空 → 返回 first_turn（保证与 F1.1 现状完全一致）。
//
// composePrompt joins the inputs prefix and artifact contract with first_turn.
// When only first_turn is present it returns first_turn verbatim, preserving
// F1.1 behavior bit-for-bit so unrelated tests stay stable.
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
