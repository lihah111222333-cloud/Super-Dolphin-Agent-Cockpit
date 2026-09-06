package operatorsource

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

const timestampLayout = "2006-01-02T15:04:05.000000000Z"

// ready 校验来源身份与持久化状态，拒绝无效或不匹配的数据。
func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.db == nil || s.queries == nil || ctx == nil {
		return ErrInvalid
	}
	v := reflect.ValueOf(ctx)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return ErrInvalid
		}
	}
	return ctx.Err()
}
func canonical(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsRune(s, 0)
}
func validateKey(k key) error {
	if !canonical(k.PrincipalRef, 512) || !canonical(k.ScopeRef, 512) {
		return ErrInvalid
	}
	return nil
}

// validateSelector 校验来源身份与持久化状态，拒绝无效或不匹配的数据。
func validateSelector(s Selector) error {
	if err := validateKey(key{s.PrincipalRef, s.ScopeRef}); err != nil {
		return err
	}
	if !canonical(s.AuthContext, 512) || !canonical(s.SourceID, 128) || !canonical(s.SourceFenceRef, 128) || s.Generation <= 0 {
		return ErrInvalid
	}
	return nil
}
func identity(p, s, a string, g int64) (string, string, error) {
	// 固定类型元组的JSON编码保留完整边界，避免拼接碰撞。
	encoded, err := json.Marshal(struct {
		Principal, Scope, Auth string
		Generation             int64
	}{p, s, a, g})
	if err != nil {
		return "", "", fmt.Errorf("%w: encode identity: %w", ErrInvalid, err)
	}
	return fmt.Sprintf("operator-source:%x", sha256.Sum256(append([]byte("operator-source/v1\x00"), encoded...))), fmt.Sprintf("operator-fence:%x", sha256.Sum256(append([]byte("operator-fence/v1\x00"), encoded...))), nil
}

// matches 校验来源身份与持久化状态，拒绝无效或不匹配的数据。
func matches(s Source, k Selector) bool {
	return s.PrincipalRef == k.PrincipalRef && s.ScopeRef == k.ScopeRef && s.AuthContext == k.AuthContext && s.SourceID == k.SourceID && s.SourceFenceRef == k.SourceFenceRef && s.Generation == k.Generation
}
func decodeResult(r sqlc.OrchestrationOperatorSource, err error) (Source, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, err
	}
	return decode(r)
}
func parseStamp(raw string) (time.Time, error) {
	t, err := time.Parse(timestampLayout, raw)
	if err != nil || t.Format(timestampLayout) != raw {
		return time.Time{}, fmt.Errorf("%w: noncanonical timestamp", ErrInvalid)
	}
	return t, nil
}

// decode 校验来源身份与持久化状态，拒绝无效或不匹配的数据。
func decode(r sqlc.OrchestrationOperatorSource) (Source, error) {
	sel := Selector{r.PrincipalRef, r.ScopeRef, r.AuthContext, r.SourceID, r.SourceFenceRef, r.Generation}
	if err := validateSelector(sel); err != nil {
		return Source{}, err
	}
	id, fence, err := identity(r.PrincipalRef, r.ScopeRef, r.AuthContext, r.Generation)
	if err != nil {
		return Source{}, err
	}
	if r.SourceID != id || r.SourceFenceRef != fence || r.SchemaVersion != 1 {
		return Source{}, fmt.Errorf("%w: damaged source identity", ErrInvalid)
	}
	created, err := parseStamp(r.CreatedAt)
	if err != nil {
		return Source{}, err
	}
	revoked, err := decodeRevoked(r, created)
	if err != nil {
		return Source{}, err
	}
	return Source{SourceID: r.SourceID, SourceFenceRef: r.SourceFenceRef, PrincipalRef: r.PrincipalRef, ScopeRef: r.ScopeRef, AuthContext: r.AuthContext, Generation: r.Generation, State: r.State, CreatedAt: created, RevokedAt: revoked, SchemaVersion: r.SchemaVersion}, nil
}

// decodeRevoked 校验来源身份与持久化状态，拒绝无效或不匹配的数据。
func decodeRevoked(r sqlc.OrchestrationOperatorSource, created time.Time) (*time.Time, error) {
	if r.State == "active" && r.RevokedAt == nil {
		return nil, nil
	}
	if r.State != "revoked" || r.RevokedAt == nil {
		return nil, ErrInvalid
	}
	revoked, err := parseStamp(*r.RevokedAt)
	if err != nil {
		return nil, err
	}
	if revoked.Before(created) {
		return nil, ErrInvalid
	}
	return &revoked, nil
}
