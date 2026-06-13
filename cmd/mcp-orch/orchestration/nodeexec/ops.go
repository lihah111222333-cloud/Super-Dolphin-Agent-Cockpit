package nodeexec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// task_dag_apply_ops typed payload —— 蓝图 v2 §9 + 实施计划 S4.1 + S4.2。
// 4 个动词 + base_version OCC 是 AI 设计师与用户共用的"动态可重写 DAG"原语。
// 本文件只定义 wire schema 和 marshal/unmarshal 契约；ApplyOps 的状态变更由
// orchestration service 层执行，nodeexec 侧测试覆盖 typed dispatch、strict
// decode、三态 patch 与 banned-field 拦截。

// OpKind 标识 ops 数组中每个元素的种类（discriminator）。
type OpKind string

const (
	OpKindUpdateDAG  OpKind = "update_dag"
	OpKindAddNode    OpKind = "add_node"
	OpKindUpdateNode OpKind = "update_node"
	OpKindRemoveNode OpKind = "remove_node"
)

// Op 是 ops 数组单元的 sealed 接口。
// 实现者只能是本文件 4 个 typed struct（编译期检查由 Kind() 方法保证）。
type Op interface {
	// Kind 返回这条 op 的类型。
	Kind() OpKind
}

// DAGPatch 是 update_dag 允许修改的 DAG 元数据子集。
// 字段全 *T，nil 表示"不改"，*T 表示"改成此值"。
type DAGPatch struct {
	Title           *string `json:"title,omitempty"`
	Description     *string `json:"description,omitempty"`
	Trigger         *string `json:"trigger,omitempty"`   // manual | auto | scheduled | external
	CronExpr        *string `json:"cron_expr,omitempty"` // 仅 trigger=scheduled 时有意义
	OwnerID         *string `json:"owner_id,omitempty"`
	ScheduleEnabled *bool   `json:"schedule_enabled,omitempty"`
}

// OpUpdateDAG 修改 DAG 元数据（draft/ready 状态）。
type OpUpdateDAG struct {
	Patch DAGPatch `json:"patch"`
}

// Kind 返回 DAG patch 操作类型。
func (OpUpdateDAG) Kind() OpKind { return OpKindUpdateDAG }

// NodeSpec 是创建节点时的完整字段集（与 nodeexec.Node 解耦：
// Node 是执行视图，NodeSpec 是编辑视图含 DependsOn）。
type NodeSpec struct {
	NodeKey    string          `json:"node_key"`
	Title      string          `json:"title"`
	NodeType   string          `json:"node_type"` // agent | automation | hybrid
	AssignedTo string          `json:"assigned_to,omitempty"`
	DependsOn  []string        `json:"depends_on,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"` // 由 ParseNodeConfig 解码（S5.2）
}

// OpAddNode 加节点。draft/ready 下由 service 层持久化到模板；running/active run
// 下当前先 fail-fast，等 runtime append 闭环后再恢复动态追加约束。
type OpAddNode struct {
	Node NodeSpec `json:"node"`
}

// Kind 返回 DAG patch 操作类型。
func (OpAddNode) Kind() OpKind { return OpKindAddNode }

// UnmarshalJSON 解码JSON。
func (op *OpAddNode) UnmarshalJSON(data []byte) error {
	type addNodeWire struct {
		Op   OpKind   `json:"op"`
		Node NodeSpec `json:"node"`
	}
	var wire addNodeWire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return addNodeStrictDecodeError(err, "op/node")
	}
	op.Node = wire.Node
	return nil
}

// UnmarshalJSON 解码JSON。
func (n *NodeSpec) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*n = NodeSpec{}
		return nil
	}
	type nodeSpecPlain NodeSpec
	var plain nodeSpecPlain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plain); err != nil {
		return addNodeStrictDecodeError(err, "node_key/title/node_type/assigned_to/depends_on/config")
	}
	*n = NodeSpec(plain)
	return nil
}

func addNodeStrictDecodeError(err error, allowed string) error {
	errMsg := err.Error()
	if strings.Contains(errMsg, "unknown field") {
		return fmt.Errorf("add_node: %v (allowed: %s)", err, allowed)
	}
	return err
}

// NodePatch 是 update_node 允许修改的节点字段白名单。F4.2 落地后唯一接收的
// patch 顶层 key 是 4 个：title / assigned_to / depends_on / config。任何其他
// 顶层 key（含禁改的 node_key / node_type / status / agent_key 与拼写错误的
// 随机字段）由 UnmarshalJSON 严格拒。
//
// 三态语义：
//   - Title / AssignedTo: *string —— nil 不改 / 指向 "" 清空 / 指向 v 改成 v
//   - DependsOn: *[]string —— nil 不改 / *[] 清空 / *[a,b] 设置
//   - Config: json.RawMessage —— 空（len==0 或 "null"）不改 / 非空覆盖整个 JSON
//     （结构性 patch 留给 schema 解码后再做，骨架阶段语义保持"整片替换"）
//
// 关键不变量：禁改 status —— status 由生命周期路径管，ApplyOps 不许碰；禁改
// node_key / node_type —— wire 上无字段位、即便用户硬塞也由 strict unmarshal
// 拦下；agent_key 顶层不许出现，config 内只允许直接路径 config.exec.agent_key
// 或 hybrid 的 config.exec.verifier.agent_key 随完整 exec 配置一起保存；改 agent
// 路由应通过 assigned_to 改 wakeup 派发。
type NodePatch struct {
	Title      *string         `json:"title,omitempty"`
	AssignedTo *string         `json:"assigned_to,omitempty"`
	DependsOn  *[]string       `json:"depends_on,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// ErrNodePatchBannedField 是 NodePatch.UnmarshalJSON 遇到禁改字段时返回的
// sentinel。errors.Is 可命中。F4.2 service 层把它包成 ErrApplyOpsInvalid
// 上抛，让 MCP 调用方收到「禁改字段」的明确反馈。
//
// 拒的场景：
//   - patch 顶层出现非白名单 key（含禁改 4 件套 + 拼写错误随机 key）。
//   - patch.config 任意嵌套层出现 banned key：status / node_key /
//     node_type；agent_key 只允许作为直接 config.exec.agent_key 或
//     config.exec.verifier.agent_key 出现，其余位置一律拒绝。
var ErrNodePatchBannedField = errors.New("node patch: banned field")

// nodePatchBannedDeepKeys 是 patch.config 内要拒绝的 key 集合。status 由
// 生命周期管，node_key / node_type 不可改；agent_key 只允许作为完整 exec
// 配置的一部分出现在直接路径 config.exec.agent_key。
//
// 深层校验对应 R2 P1（嵌套 banned key 测试 gap）：拒掉
// `{"config":{"agent_key":"evil"}}` 与 `{"config":{"nested":{"status":"x"}}}`。
// 与 executor_automation.go 的 automationOutputsForbiddenKeys 不同：
// 后者是 outputs 子 schema 的「agent prompt 注入」语义，本集合只管节点
// 身份与生命周期字段。
var nodePatchBannedDeepKeys = map[string]struct{}{
	"status":    {},
	"node_key":  {},
	"node_type": {},
	"agent_key": {},
}

// UnmarshalJSON 走 strict 模式：用 json.Decoder + DisallowUnknownFields 做
// 顶层白名单校验，再递归扫 config 内层禁改 key。
//
// 实现说明（R1 P1 #1 改造）：
//   - stdlib 标准做法是 json.NewDecoder(...).DisallowUnknownFields()。原先
//     双 pass map[string]json.RawMessage 也能做，但注释把它说成「标准做法」
//     不准确——decoder strict 模式才是 stdlib 一等公民。
//   - decoder 在解码到非白名单字段时返回 `json: unknown field "extra"` 错误。
//     我们包成 ErrNodePatchBannedField 让 errors.Is 路径不变。
//   - 用 type alias 跳过 UnmarshalJSON 自调用，防递归。
//   - 顶层 strict 解码 + 单独遍 walk patch.Config 做深层 banned key 校验，
//     避免漏掉 `config.agent_key` 这类「藏在内层」的禁改字段。
func (p *NodePatch) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*p = NodePatch{}
		return nil
	}
	type nodePatchPlain NodePatch
	var plain nodePatchPlain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plain); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown field") {
			return fmt.Errorf("%w: %v (allowed: title/assigned_to/depends_on/config)", ErrNodePatchBannedField, err)
		}
		return fmt.Errorf("node patch decode: %w", err)
	}
	if err := validateConfigDoesNotContainBannedKeys(plain.Config); err != nil {
		return err
	}
	*p = NodePatch(plain)
	return nil
}

// validateConfigDoesNotContainBannedKeys 递归扫 raw 内的所有 object key，
// 命中 nodePatchBannedDeepKeys → 包成 ErrNodePatchBannedField。
//
// 空 / null / 非对象 / 数组里的标量都直接放行；只在递归到 object 时检查。
// 数组元素与对象值都继续递归，覆盖 `{"config":{"nested":{"agent_key":...}}}` /
// `{"config":{"arr":[{"agent_key":...}]}}` 这类深嵌套 case。
func validateConfigDoesNotContainBannedKeys(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("decode node config for banned-key scan: %w", err)
	}
	return walkConfigForBannedKeys(v, nil)
}

// walkConfigForBannedKeys 对任意 JSON 值做深度优先扫，命中 banned key 返回。
func walkConfigForBannedKeys(v any, path []string) error {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if _, banned := nodePatchBannedDeepKeys[key]; banned && !isAllowedConfigAgentKey(key, path) {
				return fmt.Errorf("%w: config contains banned key %q (status/node_key/node_type are not patchable; agent_key is only allowed at config.exec.agent_key or config.exec.verifier.agent_key)", ErrNodePatchBannedField, key)
			}
			if err := walkConfigForBannedKeys(child, append(path, key)); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := walkConfigForBannedKeys(child, append(path, "[]")); err != nil {
				return err
			}
		}
	}
	return nil
}

// isAllowedConfigAgentKey 判断allowed配置代理键是否可用。
func isAllowedConfigAgentKey(key string, path []string) bool {
	if key != "agent_key" || len(path) == 0 {
		return false
	}
	if len(path) == 1 && path[0] == "exec" {
		return true
	}
	return len(path) == 2 && path[0] == "exec" && path[1] == "verifier"
}

// OpUpdateNode 修改单个节点（draft/ready 状态）。
type OpUpdateNode struct {
	NodeKey string    `json:"node_key"`
	Patch   NodePatch `json:"patch"`
}

// Kind 返回 DAG patch 操作类型。
func (OpUpdateNode) Kind() OpKind { return OpKindUpdateNode }

// OpRemoveNode 删除节点（draft/ready 状态；service 层校验
// 被依赖的节点不能删，必须先删下游或改 depends_on）。
type OpRemoveNode struct {
	NodeKey string `json:"node_key"`
}

// Kind 返回 DAG patch 操作类型。
func (OpRemoveNode) Kind() OpKind { return OpKindRemoveNode }

// OpsRequest 是 task_dag_apply_ops 的入参。
type OpsRequest struct {
	DagKey      string `json:"dag_key"`
	BaseVersion int64  `json:"base_version"`
	Ops         Ops    `json:"ops"`
}

// OpsResponse 是 task_dag_apply_ops 的返回。
type OpsResponse struct {
	NewVersion int64 `json:"new_version"`
}

// Ops 是 Op 切片，自定义 (Un)MarshalJSON 实现 typed dispatch。
type Ops []Op

// opsHeader 提取每条 op 的 discriminator。
type opsHeader struct {
	Op OpKind `json:"op"`
}

// MarshalJSON 把 Op 实现的具体类型连同 "op" 字段一起序列化。
func (ops Ops) MarshalJSON() ([]byte, error) {
	if ops == nil {
		return []byte("null"), nil
	}
	out := make([]json.RawMessage, 0, len(ops))
	for i, op := range ops {
		if op == nil {
			return nil, fmt.Errorf("ops[%d]: nil Op", i)
		}
		body, err := json.Marshal(op)
		if err != nil {
			return nil, fmt.Errorf("ops[%d] marshal body: %w", i, err)
		}
		// 把 "op": kind 注入到 body 的 JSON 对象里。
		merged, err := injectKind(body, op.Kind())
		if err != nil {
			return nil, fmt.Errorf("ops[%d] inject kind: %w", i, err)
		}
		out = append(out, merged)
	}
	return json.Marshal(out)
}

// UnmarshalJSON 解码JSON。
func (ops *Ops) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*ops = nil
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("ops: %w", err)
	}
	result := make(Ops, 0, len(raw))
	for i, item := range raw {
		var head opsHeader
		if err := json.Unmarshal(item, &head); err != nil {
			return fmt.Errorf("ops[%d] header: %w", i, err)
		}
		op, err := decodeOp(head.Op, item)
		if err != nil {
			return fmt.Errorf("ops[%d]: %w", i, err)
		}
		result = append(result, op)
	}
	*ops = result
	return nil
}

// decodeOp 解码op。
func decodeOp(kind OpKind, item json.RawMessage) (Op, error) {
	switch kind {
	case OpKindUpdateDAG:
		var op OpUpdateDAG
		if err := json.Unmarshal(item, &op); err != nil {
			return nil, err
		}
		return op, nil
	case OpKindAddNode:
		var op OpAddNode
		if err := json.Unmarshal(item, &op); err != nil {
			return nil, err
		}
		return op, nil
	case OpKindUpdateNode:
		var op OpUpdateNode
		if err := json.Unmarshal(item, &op); err != nil {
			return nil, err
		}
		return op, nil
	case OpKindRemoveNode:
		var op OpRemoveNode
		if err := json.Unmarshal(item, &op); err != nil {
			return nil, err
		}
		return op, nil
	case "":
		return nil, fmt.Errorf("missing 'op' discriminator")
	default:
		return nil, fmt.Errorf("unknown op kind %q", kind)
	}
}

// injectKind 把 "op": kind 注入到一个 JSON 对象的开头。
// 假定 body 是 {...} 形式；不是则返回错误。
func injectKind(body []byte, kind OpKind) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = make(map[string]json.RawMessage)
	}
	kindBytes, err := json.Marshal(kind)
	if err != nil {
		return nil, err
	}
	obj["op"] = kindBytes
	return json.Marshal(obj)
}
