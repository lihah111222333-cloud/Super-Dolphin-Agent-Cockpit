package sqliteauthority

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

// ListActiveResolutions 在 startup/recovery 扫描期间列出 authority 中仍为 active 的
// resolution。该 API 只读、按 schema scanner 校验每一行，并在 lease 失效时 fail-closed。
func (a *Authority) ListActiveResolutions(ctx context.Context) ([]ActiveResolution, error) {
	if err := a.validateActiveResolutionScanInput(ctx); err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, selectActiveResolutionsSQL)
	if err != nil {
		return nil, safeAuthorityError("list active resolutions", err)
	}
	defer rows.Close()
	active := make([]ActiveResolution, 0)
	for rows.Next() {
		stored, scanErr := scanStoredResolution(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("%w: active resolution row: %v", ErrSchemaMismatch, scanErr)
		}
		if stored.State != resolutionStateActive {
			return nil, fmt.Errorf("%w: active scan returned non-active state", ErrSchemaMismatch)
		}
		active = append(active, ActiveResolution{ResolutionID: stored.Expected.ResolutionID,
			Request: stored.Expected.ResolutionRequest, Expected: stored.Expected})
	}
	if err := rows.Err(); err != nil {
		return nil, safeAuthorityError("iterate active resolutions", err)
	}
	if err := a.validateAuthorityReadPath(ctx); err != nil {
		return nil, err
	}
	return active, nil
}

// validateActiveResolutionScanInput 校验扫描实例、上下文以及前后共用的
// authority lease/path 身份，避免扫描函数把边界判断与行解码混在一起。
func (a *Authority) validateActiveResolutionScanInput(ctx context.Context) error {
	if a == nil || a.db == nil {
		return ErrSchemaMismatch
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.validateAuthorityReadPath(ctx)
}

// validateAuthorityReadPath 在只读扫描前后同时复核内核租约与 SQLite 文件对象身份。
func (a *Authority) validateAuthorityReadPath(ctx context.Context) error {
	if err := a.lease.Validate(ctx, a.scope); err != nil {
		return ErrLeaseRejected
	}
	return a.validateDatabaseIdentity()
}

// ListActiveResolutionHandles 是 coordinator 使用的窄扫描端口，避免消费者自行解释
// authority 的 expected-resolution 内部字段。
func (a *Authority) ListActiveResolutionHandles(ctx context.Context) ([]recoverycustody.SubmissionHandle, error) {
	items, err := a.ListActiveResolutions(ctx)
	if err != nil {
		return nil, err
	}
	handles := make([]recoverycustody.SubmissionHandle, 0, len(items))
	for _, item := range items {
		handles = append(handles, item.Request.Handle)
	}
	return handles, nil
}
