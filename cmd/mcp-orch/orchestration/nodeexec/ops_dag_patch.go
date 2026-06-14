package nodeexec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrDAGPatchUnknownField 是 DAGPatch.UnmarshalJSON 遇到非白名单字段时返回的
// sentinel。DAG 元数据 patch 与 NodePatch 一样走 strict 入口，避免调用方误以
// 为 status / version / metadata 这类字段被接受但实际被 Go JSON 静默丢弃。
var ErrDAGPatchUnknownField = errors.New("dag patch: unknown field")

var ErrUpdateDAGPayloadInvalid = errors.New("update_dag: invalid payload")

// UnmarshalJSON 解码JSON。
func (op *OpUpdateDAG) UnmarshalJSON(data []byte) error {
	type updateDAGWire struct {
		Op    OpKind    `json:"op"`
		Patch *DAGPatch `json:"patch"`
	}
	var wire updateDAGWire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		if errors.Is(err, ErrDAGPatchUnknownField) {
			return fmt.Errorf("%w: %w", ErrUpdateDAGPayloadInvalid, err)
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown field") {
			return fmt.Errorf("%w: %v (allowed: op/patch)", ErrUpdateDAGPayloadInvalid, err)
		}
		return err
	}
	if wire.Patch == nil {
		return fmt.Errorf("%w: patch is required", ErrUpdateDAGPayloadInvalid)
	}
	op.Patch = *wire.Patch
	return nil
}

// UnmarshalJSON 解码JSON。
func (p *DAGPatch) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*p = DAGPatch{}
		return nil
	}
	type dagPatchPlain DAGPatch
	var plain dagPatchPlain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plain); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "unknown field") {
			return fmt.Errorf("%w: %v (allowed: title/description/trigger/cron_expr/owner_id/schedule_enabled)", ErrDAGPatchUnknownField, err)
		}
		return err
	}
	*p = DAGPatch(plain)
	return nil
}
