package nodeexec

import (
	"encoding/json"
	"errors"
	"fmt"
)

// task_dag_apply_ops typed payload —— 蓝图 v2 §9 + 实施计划 S4.1 + S4.2。
// 4 个动词 + base_version OCC 是 AI 设计师与用户共用的"动态可重写 DAG"原语。
// 骨架阶段只定 schema 和 marshal/unmarshal；ApplyOps 真实执行在 F4.1-F4.5。

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
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Trigger     *string `json:"trigger,omitempty"`   // manual | auto | scheduled
	CronExpr    *string `json:"cron_expr,omitempty"` // 仅 trigger=scheduled 时有意义
	OwnerID     *string `json:"owner_id,omitempty"`
}

// OpUpdateDAG 修改 DAG 元数据（draft/ready 状态）。
type OpUpdateDAG struct {
	Patch DAGPatch `json:"patch"`
}

func (OpUpdateDAG) Kind() OpKind { return OpKindUpdateDAG }

// NodeSpec 是创建节点时的完整字段集（与 nodeexec.Node 解耦：
// Node 是执行视图，NodeSpec 是编辑视图含 DependsOn）。
type NodeSpec struct {
	NodeKey   string          `json:"node_key"`
	Title     string          `json:"title"`
	NodeType  string          `json:"node_type"` // agent | automation | hybrid
	DependsOn []string        `json:"depends_on,omitempty"`
	Config    json.RawMessage `json:"config,omitempty"` // 由 ParseNodeConfig 解码（S5.2）
}

// OpAddNode 加节点（draft/ready/running 状态都允许；
// running 状态下 service 层会校验 depends_on 必须指向 done 节点——动态可重写约束）。
type OpAddNode struct {
	Node NodeSpec `json:"node"`
}

func (OpAddNode) Kind() OpKind { return OpKindAddNode }

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
// 拦下；禁改 agent_key —— agent_key 是 config 内的字段，patch 顶层不许出现
// （改 agent 路由应通过 assigned_to 改 wakeup 派发，或改 config 里嵌的字段）。
type NodePatch struct {
	Title      *string         `json:"title,omitempty"`
	AssignedTo *string         `json:"assigned_to,omitempty"`
	DependsOn  *[]string       `json:"depends_on,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// ErrNodePatchBannedField 是 NodePatch.UnmarshalJSON 遇到非白名单顶层字段时
// 返回的 sentinel。errors.Is 可命中。F4.2 service 层把它包成
// ErrApplyOpsInvalid 上抛，让 MCP 调用方收到「禁改字段」的明确反馈。
var ErrNodePatchBannedField = errors.New("node patch: banned top-level field")

// nodePatchAllowedKeys 白名单：JSON 顶层 key 必须在此集合内。
// 与 NodePatch 结构体字段一一对应；新增字段时双向维护。
var nodePatchAllowedKeys = map[string]struct{}{
	"title":       {},
	"assigned_to": {},
	"depends_on":  {},
	"config":      {},
}

// UnmarshalJSON 走 strict 模式：拿 RawMessage map 过白名单，再分发给
// 结构体本体解码。Banned key 顶层出现一律拒——含 status / node_key /
// node_type / agent_key 这 4 个禁改字段，以及任何拼写错误的随机 key。
//
// 双 pass 设计：encoding/json 默认 unknown fields 静默忽略，无 strict 开关；
// 用 map[string]json.RawMessage 自前过白名单是 stdlib 下的标准做法。代价：
// 多一次解码 + map 分配，但 patch 顶层字段固定 ≤ 4 个、影响可忽略。
func (p *NodePatch) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*p = NodePatch{}
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("node patch: %w", err)
	}
	for key := range raw {
		if _, ok := nodePatchAllowedKeys[key]; !ok {
			return fmt.Errorf("%w: %q (allowed: title/assigned_to/depends_on/config)", ErrNodePatchBannedField, key)
		}
	}
	// 第二 pass：结构体本体解码。用 type alias 跳过 UnmarshalJSON 自调用
	// 防递归。
	type nodePatchPlain NodePatch
	var plain nodePatchPlain
	if err := json.Unmarshal(data, &plain); err != nil {
		return fmt.Errorf("node patch decode: %w", err)
	}
	*p = NodePatch(plain)
	return nil
}

// OpUpdateNode 修改单个节点（draft/ready 状态）。
type OpUpdateNode struct {
	NodeKey string    `json:"node_key"`
	Patch   NodePatch `json:"patch"`
}

func (OpUpdateNode) Kind() OpKind { return OpKindUpdateNode }

// OpRemoveNode 删除节点（draft/ready 状态；service 层校验
// 被依赖的节点不能删，必须先删下游或改 depends_on）。
type OpRemoveNode struct {
	NodeKey string `json:"node_key"`
}

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

// UnmarshalJSON 按每条 op 的 "op" 字段 dispatch 到 typed struct。
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
