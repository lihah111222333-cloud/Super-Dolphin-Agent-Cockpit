package content

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentobject"
)

// Store 独占CONTENT-1 reservation/object/ref metadata并组合显式crypto/object backend。
type Store struct {
	db      *sql.DB
	queries *storedb.Queries
	sealer  contentcrypto.KeyCreatorSealer
	objects contentobject.Store
	now     func() time.Time
}

// NewStore 构造CONTENT owner；key provider或private object backend不可用时立即失败。
func NewStore(db *sql.DB, sealer contentcrypto.KeyCreatorSealer, objects contentobject.Store) (*Store, error) {
	if db == nil {
		return nil, errors.New("content owner database is required")
	}
	if nilContentDependency(sealer) {
		return nil, contentcrypto.ErrKeyProviderMissing
	}
	if err := sealer.Validate(); err != nil {
		return nil, fmt.Errorf("validate content key provider: %w", err)
	}
	if nilContentDependency(objects) {
		return nil, contentobject.ErrObjectStoreMissing
	}
	if err := objects.Validate(); err != nil {
		return nil, fmt.Errorf("validate content object store: %w", err)
	}
	return &Store{db: db, queries: storedb.New(db), sealer: sealer, objects: objects, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.db == nil || s.queries == nil || nilContentDependency(s.sealer) || nilContentDependency(s.objects) || s.now == nil {
		return ErrStoreUninitialized
	}
	return ctx.Err()
}

func nilContentDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

var _ StoreAPI = (*Store)(nil)
