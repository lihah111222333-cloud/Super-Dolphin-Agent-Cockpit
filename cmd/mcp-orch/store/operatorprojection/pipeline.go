package operatorprojection

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentobject"
)

// Pipeline 编排CONTENT密封、对象持久化与prepared元数据登记。
type Pipeline struct {
	db      *sql.DB
	q       *sqlc.Queries
	sealer  contentcrypto.KeyCreatorSealer
	objects contentobject.Store
}

// PrepareRequest 携带operator reservation与seal/object边界字段。
type PrepareRequest struct {
	ReservationID         string
	SourceID              string
	Seal                  contentcrypto.SealRequest
	ObjectReceiptMetadata contentobject.PutReceipt
	CreatedAt             string
	UpdatedAt             string
}

// NewPipeline 构造真实依赖；Unavailable或typed-nil依赖立即拒绝。
func NewPipeline(db *sql.DB, sealer contentcrypto.KeyCreatorSealer, objects contentobject.Store) (*Pipeline, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database required", ErrInvalid)
	}
	if sealer == nil || errors.Is(sealer.Validate(), contentcrypto.ErrKeyProviderMissing) {
		return nil, contentcrypto.ErrKeyProviderMissing
	}
	if err := sealer.Validate(); err != nil {
		return nil, err
	}
	if objects == nil || errors.Is(objects.Validate(), contentobject.ErrObjectStoreMissing) {
		return nil, contentobject.ErrObjectStoreMissing
	}
	if err := objects.Validate(); err != nil {
		return nil, err
	}
	return &Pipeline{db: db, q: sqlc.New(db), sealer: sealer, objects: objects}, nil
}

// SealAndPrepare 按CONTENT意图顺序执行；外部IO失败不写DB。
// SealAndPrepare 按CONTENT顺序执行各阶段。
func (p *Pipeline) SealAndPrepare(ctx context.Context, r PrepareRequest) (sqlc.OrchestrationOperatorContentObject, error) {
	if p == nil || p.db == nil || p.sealer == nil || p.objects == nil {
		return sqlc.OrchestrationOperatorContentObject{}, ErrInvalid
	}
	res, src, err := p.validatePrepareReservation(ctx, r)
	if err != nil {
		return sqlc.OrchestrationOperatorContentObject{}, err
	}
	sealed, err := p.sealPayload(ctx, r, res)
	if err != nil {
		return sqlc.OrchestrationOperatorContentObject{}, err
	}
	receipt, err := p.putSealedObject(ctx, r, sealed)
	if err != nil {
		return sqlc.OrchestrationOperatorContentObject{}, err
	}
	return p.persistPrepared(ctx, r, res, src, sealed, receipt)
}

// validatePrepareReservation 校验reservation、source及绑定字段。
func (p *Pipeline) validatePrepareReservation(ctx context.Context, r PrepareRequest) (sqlc.OrchestrationOperatorReservation, sqlc.OrchestrationOperatorSource, error) {
	if !validPrepareHeader(r) {
		return sqlc.OrchestrationOperatorReservation{}, sqlc.OrchestrationOperatorSource{}, ErrInvalid
	}
	res, src, e := p.loadReservationSource(ctx, r)
	if e != nil {
		return res, src, e
	}
	if !reservationSourceMatches(res, src) || !sealMatchesReservation(r, res) {
		return res, src, ErrInvalid
	}
	return res, src, nil
}

// validPrepareHeader 检查prepare请求的reservation、来源和时间字段。
func validPrepareHeader(r PrepareRequest) bool {
	return strings.TrimSpace(r.ReservationID) != "" && strings.TrimSpace(r.SourceID) != "" && canonicalUTC(r.CreatedAt) && canonicalUTC(r.UpdatedAt)
}

// loadReservationSource 从同一数据库读取reservation与source快照。
func (p *Pipeline) loadReservationSource(ctx context.Context, r PrepareRequest) (sqlc.OrchestrationOperatorReservation, sqlc.OrchestrationOperatorSource, error) {
	res, err := p.q.GetOperatorReservation(ctx, sqlc.GetOperatorReservationParams{ReservationID: r.ReservationID})
	if err != nil {
		return res, sqlc.OrchestrationOperatorSource{}, err
	}
	src, err := p.q.GetOperatorSourceID(ctx, sqlc.GetOperatorSourceIDParams{SourceID: r.SourceID})
	return res, src, err
}

// reservationSourceMatches 比较reservation与当前operator source的全部绑定字段。
func reservationSourceMatches(res sqlc.OrchestrationOperatorReservation, src sqlc.OrchestrationOperatorSource) bool {
	return res.SourceID == src.SourceID && res.SourceFenceRef == src.SourceFenceRef && res.PrincipalRef == src.PrincipalRef && res.ScopeRef == src.ScopeRef && res.AuthContext == src.AuthContext && res.Generation == src.Generation && src.State == "active"
}

// sealMatchesReservation 确认seal request没有越过reservation边界。
func sealMatchesReservation(r PrepareRequest, res sqlc.OrchestrationOperatorReservation) bool {
	return r.Seal.SourceFenceRef == res.SourceFenceRef && r.Seal.TicketID != "" && r.Seal.OperationID != "" && r.Seal.ObjectID != "" && r.Seal.MediaType == res.MediaType && r.Seal.ObjectVersion == 1
}

// sealPayload 仅调用真实密封器。
func (p *Pipeline) sealPayload(ctx context.Context, r PrepareRequest, _ sqlc.OrchestrationOperatorReservation) (contentcrypto.SealedPayload, error) {
	return p.sealer.CreateAndSeal(ctx, r.Seal)
}

// putSealedObject 持久化密文并校验回执绑定。
func (p *Pipeline) putSealedObject(ctx context.Context, r PrepareRequest, s contentcrypto.SealedPayload) (contentobject.PutReceipt, error) {
	put := contentobject.PutRequest{SchemaVersion: 1, OperationID: s.OperationID, RequestDigest: s.RequestDigest, ObjectID: r.Seal.ObjectID, ObjectVersion: r.Seal.ObjectVersion, CiphertextHash: s.CiphertextHash, CiphertextSize: s.CiphertextSize, Ciphertext: s.Ciphertext}
	receipt, e := p.objects.Put(ctx, put)
	if e != nil {
		return receipt, e
	}
	if receipt.ObjectID != put.ObjectID || receipt.CiphertextHash != put.CiphertextHash || receipt.CiphertextSize != put.CiphertextSize {
		return receipt, contentobject.ErrObjectConflict
	}
	return receipt, nil
}

// persistPrepared 在立即事务中登记prepared元数据。
func (p *Pipeline) persistPrepared(ctx context.Context, r PrepareRequest, res sqlc.OrchestrationOperatorReservation, src sqlc.OrchestrationOperatorSource, s contentcrypto.SealedPayload, receipt contentobject.PutReceipt) (out sqlc.OrchestrationOperatorContentObject, err error) {
	err = sqlctx.WithImmediateTx(ctx, p.db, p.q, func(q *sqlc.Queries, _ sqlc.DBTX) error {
		var e error
		out, e = q.CreatePreparedObject(ctx, sqlc.CreatePreparedObjectParams{ObjectID: r.Seal.ObjectID, ReservationID: r.ReservationID, SourceID: src.SourceID, SourceGeneration: src.Generation, AeadAlgorithm: s.Algorithm, KeyWrapProfile: s.KeyWrapProfile, KeyHandleRef: s.KeyHandleRef, KeyReceiptRef: s.KeyReceiptRef, KeyReceiptDigest: s.KeyReceiptDigest, NonceHex: hex.EncodeToString(s.Nonce), AadProfile: r.Seal.AADProfile, AadDigest: s.AADDigest, MediaType: r.Seal.MediaType, SizeBytes: s.CiphertextSize, CiphertextSha256: s.CiphertextHash, ObjectReceiptRef: receipt.ReceiptRef, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
		return e
	})
	return out, err
}

// canonicalUTC 检查固定UTC时间字段的可解析性。
// canonicalUTC 检查固定UTC时间字段的可解析性。
func canonicalUTC(v string) bool {
	t, e := time.Parse(time.RFC3339Nano, v)
	return e == nil && t.UTC().Format(time.RFC3339Nano) == v
}
