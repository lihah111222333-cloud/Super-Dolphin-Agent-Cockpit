// Package routingtest 提供 prompt_routing_tests 表的只读存储层。
// 这些用例由运维或管理员维护，router/runTests 会读取启用行来验证 RuleRouter 仍能映射到预期 prompt_key。
package routingtest

import (
	"context"
	"time"

	db "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	sqlc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// Reader 是 routing test 对外暴露的只读接口。
// 写入留给 SQL 或未来管理 UI，避免 CI 或运行时测试误改生产路由用例。
type Reader interface {
	ListEnabled(ctx context.Context) ([]RoutingTest, error)
}

// RoutingTest 是一条启用的 prompt 路由验收用例。
// ExpectedPromptKey 表示给定输入应命中的模板，用于检测路由规则漂移。
type RoutingTest struct {
	ID                int64     `json:"id"`
	Input             string    `json:"input"`
	ExpectedPromptKey string    `json:"expected_prompt_key"`
	Note              string    `json:"note,omitempty"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type store struct {
	q *sqlc.Queries
}

// NewStore 创建基于 sqlc 的只读 routing test 存储。
// 返回 Reader 便于调用方在测试中替换 fake，同时保持生产实现不暴露写接口。
func NewStore(q *sqlc.Queries) Reader {
	return &store{q: q}
}

// ListEnabled 读取所有启用的 prompt routing 测试用例。
// 该方法只做行映射，不在存储层修改 enabled 状态或补写测试结果。
func (s *store) ListEnabled(ctx context.Context) ([]RoutingTest, error) {
	rows, err := s.q.ListEnabledPromptRoutingTests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RoutingTest, len(rows))
	for i, row := range rows {
		out[i] = RoutingTest{
			ID:                row.ID,
			Input:             row.Input,
			ExpectedPromptKey: row.ExpectedPromptKey,
			Note:              row.Note,
			Enabled:           row.Enabled != 0,
			CreatedAt:         db.TimeFromMillis(row.CreatedAt),
			UpdatedAt:         db.TimeFromMillis(row.UpdatedAt),
		}
	}
	return out, nil
}
