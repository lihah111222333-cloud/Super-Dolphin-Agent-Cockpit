package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TakeoverAuthority 在已验证 authority lease 下为 successor 原子推进
// DB-resident writer fence；调用方随后必须用 next 配置重新 OpenAuthority。
func TakeoverAuthority(ctx context.Context, config Config, lease AuthorityLease, next WriterFence) error {
	config, scope, err := validateAuthorityInputs(ctx, config, lease)
	if err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if next.WriterEpoch <= config.WriterEpoch {
		return fmt.Errorf("%w: takeover epoch must increase", ErrWriterFenceSchema)
	}
	return withValidatedAuthorityScope(ctx, lease, scope, config.Path, func() error {
		return takeoverAuthorityDatabase(ctx, config, scope, next)
	})
}

// takeoverAuthorityDatabase 校验现有 authority schema 后，在单一立即事务内
// 以 expected token CAS 推进 successor token。
func takeoverAuthorityDatabase(ctx context.Context, config Config, scope AuthorityScope, next WriterFence) (resultErr error) {
	if err := requireExistingAuthorityDatabase(config.Path); err != nil {
		return err
	}
	db, err := platformsqlite.Open(ctx, platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		return safeAuthorityError("open authority for takeover", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, safeAuthorityError("close authority takeover database", closeErr))
		}
	}()
	_, err = verifyAuthoritySchemaForTakeover(ctx, db, config, scope)
	if err != nil {
		return err
	}
	return withWriterFenceImmediate(ctx, db, config.WriterFence(), func(conn *sql.Conn) error {
		return takeoverWriterFence(ctx, conn, config.WriterFence(), next)
	})
}
