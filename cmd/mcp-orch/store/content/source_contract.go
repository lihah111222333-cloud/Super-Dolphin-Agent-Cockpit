package content

import "context"

// SourceKind 标识CONTENT来源联合分支。
type SourceKind string

const SourceKindOperator SourceKind = "operator"

// OperatorSourceBinding 保存operator source的完整current selector。
type OperatorSourceBinding struct {
	SourceID, FenceRef, PrincipalRef, ScopeRef, AuthContext string
	Generation                                              int64
}

// SourceResolution 是reservation的已校验operator来源解析结果。
type SourceResolution struct {
	ReservationID string
	Binding       OperatorSourceBinding
}

// StoreSourceAPI 是来源绑定的窄owner接口。
type StoreSourceAPI interface {
	BindOperatorSource(context.Context, string, OperatorSourceBinding) (SourceResolution, error)
	ResolveSource(context.Context, string) (SourceResolution, error)
}
