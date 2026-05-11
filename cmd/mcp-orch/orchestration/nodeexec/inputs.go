package nodeexec

import (
	"context"
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

// ErrInputsValidation 是 inputs 装载阶段被判为“节点配置错误”的 sentinel。
// 例：from_nodes 引用不存在的 node_key；from_sharedfiles 引用不存在的 path；
// 未接通 reader 但节点 inputs 又引用了。assembleInputs 返回时同时携带
// FailureClassValidation；测试可用 errors.Is 该哨兵断言。
//
// ErrInputsValidation marks inputs-stage failures that should map to
// FailureClass=validation (missing node_key / missing sharedfile / inputs
// referenced but reader not wired). assembleInputs returns it alongside the
// validation class so callers can errors.Is in tests.
var ErrInputsValidation = errors.New("nodeexec: inputs validation")

// assembleInputs 按 cfg.Inputs 拉取 prev nodes result + sharedfiles，拼成一个
// 将注入到 LaunchRequest.Prompt 头部的前缀字符串。
//
// 返回值：
//   - prefix：拼接后的文本。空表示无需注入（cfg.Inputs 为空）。
//   - err：担结原因。为 nil 表示装载成功。
//   - class：err 非 nil 时携带的 FailureClass。使调用方不用重推。
//
// assembleInputs gathers configured prev results / sharedfiles into a single
// prompt prefix. Returns (prefix, nil, "") on success. On failure returns
// ("", err, class) where class is FailureClassValidation for missing refs /
// missing readers, and the transient/quota default for infra errors flowing
// through classifyAgentLaunchError.
func (e *AgentExecutor) assembleInputs(
	ctx context.Context,
	cfg *AgentNodeConfig,
	runCtx RunContext,
	node Node,
) (string, error, FailureClass) {
	if cfg == nil || !inputsConfigured(&cfg.Inputs) {
		return "", nil, ""
	}
	dagKey := resolveDagKey(runCtx, node)

	sections, err, class := e.collectInputSections(ctx, &cfg.Inputs, dagKey)
	if err != nil {
		return "", err, class
	}
	if len(sections) == 0 {
		return "", nil, ""
	}
	return strings.Join(sections, "\n\n"), nil, ""
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
// collectInputSections loads each configured source and returns the non-empty
// sections; split out to keep assembleInputs under the cyclomatic budget.
func (e *AgentExecutor) collectInputSections(
	ctx context.Context,
	in *InputsConfig,
	dagKey string,
) ([]string, error, FailureClass) {
	var sections []string
	if len(in.FromNodes) > 0 {
		section, err, class := e.loadFromNodes(ctx, dagKey, in.FromNodes)
		if err != nil {
			return nil, err, class
		}
		if section != "" {
			sections = append(sections, section)
		}
	}
	if len(in.FromSharedfiles) > 0 {
		section, err, class := e.loadFromSharedfiles(ctx, in.FromSharedfiles)
		if err != nil {
			return nil, err, class
		}
		if section != "" {
			sections = append(sections, section)
		}
	}
	return sections, nil, ""
}

// loadFromNodes 拉取 cfg.Inputs.FromNodes 列出的 prev node result。
//
// reader == nil 但配置非空 → validation（避免默默吞掉注入需求）。
// node_key 不存在 → validation。
// result 为空 → 注入「(empty)」占位（上游可能未配 outputs.to_node_result）。
func (e *AgentExecutor) loadFromNodes(
	ctx context.Context,
	dagKey string,
	nodeKeys []string,
) (string, error, FailureClass) {
	if e.prevResults == nil {
		return "", fmt.Errorf("%w: inputs.from_nodes set but prev-results reader not wired", ErrInputsValidation), FailureClassValidation
	}
	if dagKey == "" {
		return "", fmt.Errorf("%w: cannot resolve from_nodes without dag_key (runCtx + node both empty)", ErrInputsValidation), FailureClassValidation
	}

	var b strings.Builder
	b.WriteString("[inputs.from_nodes]\n")
	for _, raw := range nodeKeys {
		key := strings.TrimSpace(raw)
		if key == "" {
			// 空串 → 静默跳过，不是 validation 错（取其尽量汇入意图）。
			continue
		}
		result, exists, err := e.prevResults.GetNodeResult(ctx, dagKey, key)
		if err != nil {
			return "", fmt.Errorf("read prev node %q result: %w", key, err), classifyAgentLaunchError(err)
		}
		if !exists {
			return "", fmt.Errorf("%w: from_nodes references unknown node_key %q", ErrInputsValidation, key), FailureClassValidation
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
	return b.String(), nil, ""
}

// loadFromSharedfiles 拉取 cfg.Inputs.FromSharedfiles 列出的文件内容。
//
// reader == nil 但配置非空 → validation。文件不存在 → validation。读失败 →
// classifyAgentLaunchError（一般 transient）。
func (e *AgentExecutor) loadFromSharedfiles(
	ctx context.Context,
	paths []string,
) (string, error, FailureClass) {
	if e.sharedfiles == nil {
		return "", fmt.Errorf("%w: inputs.from_sharedfiles set but sharedfile reader not wired", ErrInputsValidation), FailureClassValidation
	}

	var b strings.Builder
	b.WriteString("[inputs.from_sharedfiles]\n")
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		content, exists, err := e.sharedfiles.ReadSharedfile(ctx, path)
		if err != nil {
			return "", fmt.Errorf("read sharedfile %q: %w", path, err), classifyAgentLaunchError(err)
		}
		if !exists {
			return "", fmt.Errorf("%w: from_sharedfiles references unknown path %q", ErrInputsValidation, path), FailureClassValidation
		}
		fmt.Fprintf(&b, "## sharedfile:%s\n", path)
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil, ""
}

// composePrompt 拼接 inputs 前缀与 first_turn。F1.2 设计决策：
//   - 两者都为空 → 返回空串，表示未指定 Prompt（以免 service 层以 「空填充」 处理）。
//   - prefix 非空 + first_turn 非空 → prefix + "\n\n[first_turn]\n" + first_turn。
//   - 仅 prefix 非空 → 返回 prefix（末尾别加叠加 first_turn header）。
//   - 仅 first_turn 非空 → 返回 first_turn（保证与 F1.1 现状完全一致）。
//
// composePrompt joins the inputs prefix with first_turn under the F1.2
// design decision. When only first_turn is present it returns first_turn
// verbatim, preserving F1.1 behavior bit-for-bit so unrelated tests stay
// stable.
func composePrompt(prefix, firstTurn string) string {
	prefix = strings.TrimRight(prefix, "\n")
	if prefix == "" {
		return firstTurn
	}
	if firstTurn == "" {
		return prefix
	}
	return prefix + "\n\n[first_turn]\n" + firstTurn
}
