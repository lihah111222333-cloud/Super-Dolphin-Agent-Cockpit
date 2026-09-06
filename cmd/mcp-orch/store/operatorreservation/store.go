// Package operatorreservation persists source-fenced reservations.
package operatorreservation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

var (
	ErrInvalid     = errors.New("invalid reservation")
	ErrNotFound    = errors.New("reservation not found")
	ErrConflict    = errors.New("reservation conflict")
	ErrStaleSource = errors.New("stale source")
)
var hex = regexp.MustCompile(`^[0-9a-f]+$`)

// Request is the immutable reservation payload.
type Request struct {
	ReservationID, SourceID, SourceFenceRef, PrincipalRef, ScopeRef, AuthContext                                                                  string
	Generation                                                                                                                                    int64
	WorkspaceRef, CallerPrincipalRef, IdempotencyNamespace, IdempotencyKeyDigest, ContentIntent, ContentProof, ContentCandidate, ContentDigestKey string
	SizeBytes                                                                                                                                     int64
	MediaType, ProfileRef, QuotaRef                                                                                                               string
	RetentionSeconds                                                                                                                              int64
	Deadline                                                                                                                                      string
	Source                                                                                                                                        operatorsource.Selector
}

// Reservation wraps the generated row.
type Reservation struct {
	sqlc.OrchestrationOperatorReservation
}

// Store persists reservations.
type Store struct {
	db *sql.DB
	q  *sqlc.Queries
}

// NewStore 构造显式数据库owner。
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database required", ErrInvalid)
	}
	return &Store{db: db, q: sqlc.New(db)}, nil
}

// Create 原子创建或精确重放operator reservation。
func (s *Store) Create(ctx context.Context, r Request) (Reservation, error) {
	if err := valid(r); err != nil {
		return Reservation{}, err
	}
	var out Reservation
	err := sqlctx.WithImmediateTx(ctx, s.db, s.q, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		if err := current(ctx, q, r.Source); err != nil {
			return err
		}
		old, e := q.ResolveOperatorReservation(ctx, sqlc.ResolveOperatorReservationParams{IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest})
		if e == nil {
			if !same(old, r) {
				return ErrConflict
			}
			out = Reservation{old}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
		row, e := q.CreateOperatorReservation(ctx, sqlc.CreateOperatorReservationParams{ReservationID: r.ReservationID, SourceID: r.SourceID, SourceFenceRef: r.SourceFenceRef, PrincipalRef: r.PrincipalRef, ScopeRef: r.ScopeRef, AuthContext: r.AuthContext, Generation: r.Generation, WorkspaceRef: r.WorkspaceRef, CallerPrincipalRef: r.CallerPrincipalRef, IdempotencyNamespace: r.IdempotencyNamespace, IdempotencyKeyDigest: r.IdempotencyKeyDigest, ContentIntent: r.ContentIntent, ContentProof: r.ContentProof, ContentCandidate: r.ContentCandidate, ContentDigestKey: r.ContentDigestKey, SizeBytes: r.SizeBytes, MediaType: r.MediaType, ProfileRef: r.ProfileRef, QuotaRef: r.QuotaRef, RetentionSeconds: r.RetentionSeconds, Deadline: r.Deadline, CreatedAt: now, UpdatedAt: now})
		if e != nil {
			return e
		}
		out = Reservation{row}
		return nil
	})
	return out, err
}

// Resolve 只返回仍绑定当前source的reservation。
func (s *Store) Resolve(ctx context.Context, ns, digest string) (Reservation, error) {
	if ns == "" || !hex.MatchString(digest) {
		return Reservation{}, ErrInvalid
	}
	row, e := s.q.ResolveOperatorReservation(ctx, sqlc.ResolveOperatorReservationParams{IdempotencyNamespace: ns, IdempotencyKeyDigest: digest})
	if errors.Is(e, sql.ErrNoRows) {
		return Reservation{}, ErrNotFound
	}
	if e != nil {
		return Reservation{}, e
	}
	src := operatorsource.Selector{SourceID: row.SourceID, SourceFenceRef: row.SourceFenceRef, PrincipalRef: row.PrincipalRef, ScopeRef: row.ScopeRef, AuthContext: row.AuthContext, Generation: row.Generation}
	if e = current(ctx, s.q, src); e != nil {
		return Reservation{}, ErrStaleSource
	}
	return Reservation{row}, nil
}

// current 校验selector对应source仍是全局最新active代际。
func current(ctx context.Context, q *sqlc.Queries, s operatorsource.Selector) error {
	row, e := q.GetOperatorSourceID(ctx, sqlc.GetOperatorSourceIDParams{SourceID: s.SourceID})
	if e != nil {
		return e
	}
	if !sourceRowMatches(row, s) {
		return ErrStaleSource
	}
	latest, e := q.GetLatestOperatorSource(ctx)
	if e != nil || latest.SourceID != row.SourceID || latest.Generation != row.Generation || latest.State != "active" {
		return ErrStaleSource
	}
	return nil
}

// sourceRowMatches 比较source的全部不可变selector字段。
func sourceRowMatches(row sqlc.OrchestrationOperatorSource, s operatorsource.Selector) bool {
	return row.SourceFenceRef == s.SourceFenceRef && row.PrincipalRef == s.PrincipalRef && row.ScopeRef == s.ScopeRef && row.AuthContext == s.AuthContext && row.Generation == s.Generation && row.State == "active"
}

// valid 校验reservation全部不可变字段的边界与摘要格式。
func valid(r Request) error {
	if !validTextFields(r) || !validDigests(r) || r.SizeBytes <= 0 || r.RetentionSeconds < 0 {
		return ErrInvalid
	}
	t, e := time.Parse("2006-01-02T15:04:05.000000000Z", r.Deadline)
	if e != nil || t.UTC().Format("2006-01-02T15:04:05.000000000Z") != r.Deadline {
		return ErrInvalid
	}
	return nil
}

// validTextFields 检查所有必填文本字段。
func validTextFields(r Request) bool {
	for _, v := range []string{r.ReservationID, r.SourceID, r.SourceFenceRef, r.PrincipalRef, r.ScopeRef, r.AuthContext, r.WorkspaceRef, r.CallerPrincipalRef, r.IdempotencyNamespace, r.ContentIntent, r.ContentProof, r.ContentCandidate, r.MediaType, r.ProfileRef, r.QuotaRef} {
		if strings.TrimSpace(v) == "" {
			return false
		}
	}
	return true
}

// validDigests 检查固定小写SHA-256摘要。
func validDigests(r Request) bool {
	return len(r.IdempotencyKeyDigest) == 64 && hex.MatchString(r.IdempotencyKeyDigest) && len(r.ContentDigestKey) == 64 && hex.MatchString(r.ContentDigestKey)
}

// same 比较所有持久化字段，防止不同请求静默重放旧reservation。
func same(x sqlc.OrchestrationOperatorReservation, r Request) bool {
	return sameIdentity(x, r) && samePayload(x, r) && samePolicy(x, r)
}

// sameIdentity 比较来源和workspace主体字段。
func sameIdentity(x sqlc.OrchestrationOperatorReservation, r Request) bool {
	return x.ReservationID == r.ReservationID && x.SourceID == r.SourceID && x.SourceFenceRef == r.SourceFenceRef && x.PrincipalRef == r.PrincipalRef && x.ScopeRef == r.ScopeRef && x.AuthContext == r.AuthContext && x.Generation == r.Generation && x.WorkspaceRef == r.WorkspaceRef && x.CallerPrincipalRef == r.CallerPrincipalRef
}

// samePayload 比较内容及幂等字段。
func samePayload(x sqlc.OrchestrationOperatorReservation, r Request) bool {
	return x.IdempotencyNamespace == r.IdempotencyNamespace && x.IdempotencyKeyDigest == r.IdempotencyKeyDigest && x.ContentIntent == r.ContentIntent && x.ContentProof == r.ContentProof && x.ContentCandidate == r.ContentCandidate && x.ContentDigestKey == r.ContentDigestKey && x.SizeBytes == r.SizeBytes && x.MediaType == r.MediaType
}

// samePolicy 比较profile、quota、retention和deadline。
func samePolicy(x sqlc.OrchestrationOperatorReservation, r Request) bool {
	return x.ProfileRef == r.ProfileRef && x.QuotaRef == r.QuotaRef && x.RetentionSeconds == r.RetentionSeconds && x.Deadline == r.Deadline
}
