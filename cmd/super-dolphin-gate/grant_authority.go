package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type actionGrantSigner interface {
	SignActionGrant(gatecontract.ActionGrant) (gatecontract.ActionGrant, error)
}

type actionGrantVerifier interface {
	VerifyActionGrant(gatecontract.ActionGrant) error
}

type ed25519ActionGrantSigner struct {
	identity   gatecontract.SignerIdentity
	privateKey ed25519.PrivateKey
}

type ed25519ActionGrantVerifier struct {
	identity  gatecontract.SignerIdentity
	publicKey ed25519.PublicKey
}

type actionGrantIntent struct {
	Receipt           gatecontract.ResultReceipt
	InvocationOwner   string
	Audience          gatecontract.ActionAudience
	ActionPolicy      string
	RemoteURL         string
	Ref               string
	OldSHA            string
	NewSHA            string
	ReleaseRepository string
	ReleaseTag        string
	ReleaseCommitSHA  string
	ReleaseAssets     []gatecontract.ReleaseAsset
	ActionAttemptID   string
	RequestNonce      string
}

type actionGrantExpectation struct {
	Audience          gatecontract.ActionAudience
	RepoID            string
	InvocationID      string
	SourceTreeSHA     string
	Generation        uint64
	RemoteURL         string
	Ref               string
	OldSHA            string
	NewSHA            string
	ReleaseRepository string
	ReleaseTag        string
	ReleaseCommitSHA  string
	ReleaseAssets     []gatecontract.ReleaseAsset
	ActionAttemptID   string
}

type actionGrantService struct {
	store            *coordinatorStore
	signer           actionGrantSigner
	verifier         actionGrantVerifier
	receiptAuthority hookResultReceiptAuthority
	adapter          gatecontract.TrustedAdapterIdentity
	ttl              time.Duration
	now              func() time.Time
}

func newEd25519ActionGrantSigner(
	identity gatecontract.SignerIdentity,
	privateKey ed25519.PrivateKey,
	publicKey ed25519.PublicKey,
) (*ed25519ActionGrantSigner, error) {
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("action grant signer identity: %w", err)
	}
	if len(privateKey) != ed25519.PrivateKeySize || len(publicKey) != ed25519.PublicKeySize ||
		!privateKey.Public().(ed25519.PublicKey).Equal(publicKey) {
		return nil, errors.New("action grant Ed25519 private and public keys do not match")
	}
	return &ed25519ActionGrantSigner{
		identity: identity, privateKey: append(ed25519.PrivateKey(nil), privateKey...),
	}, nil
}

func newEd25519ActionGrantVerifier(
	identity gatecontract.SignerIdentity,
	publicKey ed25519.PublicKey,
) (*ed25519ActionGrantVerifier, error) {
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("action grant verifier identity: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("action grant public key must be Ed25519")
	}
	return &ed25519ActionGrantVerifier{identity: identity, publicKey: append(ed25519.PublicKey(nil), publicKey...)}, nil
}

// newActionGrantService 校验并组装单次授权的签名、验签、回执与持久化依赖。
func newActionGrantService(
	store *coordinatorStore,
	signer actionGrantSigner,
	verifier actionGrantVerifier,
	receiptAuthority hookResultReceiptAuthority,
	adapter gatecontract.TrustedAdapterIdentity,
	ttl time.Duration,
	now func() time.Time,
) (*actionGrantService, error) {
	if store == nil || interfaceIsNil(signer) || interfaceIsNil(verifier) || interfaceIsNil(receiptAuthority) || now == nil {
		return nil, errors.New("action grant service dependencies are required")
	}
	if err := adapter.Validate(); err != nil {
		return nil, fmt.Errorf("action grant adapter: %w", err)
	}
	if ttl <= 0 || ttl > 15*time.Minute {
		return nil, errors.New("action grant ttl must be within 1ns..15m")
	}
	return &actionGrantService{
		store: store, signer: signer, verifier: verifier, receiptAuthority: receiptAuthority,
		adapter: adapter, ttl: ttl, now: now,
	}, nil
}

// newProductionActionGrantService 从仓库外私有配置装配宿主 ActionGrant authority。
func newProductionActionGrantService(
	config productionCoordinatorConfig,
	store *coordinatorStore,
	receiptAuthority hookResultReceiptAuthority,
) (*actionGrantService, error) {
	publicKey, err := decodeActionGrantPublicKey(config.ActionGrantAuthority.PublicKey)
	if err != nil {
		return nil, err
	}
	privateKey, err := loadProductionActionGrantPrivateKey(config)
	if err != nil {
		return nil, err
	}
	signer, err := newEd25519ActionGrantSigner(
		config.ActionGrantAuthority.Signer, privateKey, publicKey,
	)
	if err != nil {
		return nil, err
	}
	verifier, err := newEd25519ActionGrantVerifier(config.ActionGrantAuthority.Signer, publicKey)
	if err != nil {
		return nil, err
	}
	adapterDigest, err := currentExecutableDigest()
	if err != nil {
		return nil, err
	}
	adapter := gatecontract.TrustedAdapterIdentity{
		Name: "git-pre-push", BinaryDigest: adapterDigest, Signer: config.ActionGrantAuthority.Signer,
	}
	return newActionGrantService(
		store, signer, verifier, receiptAuthority, adapter,
		time.Duration(config.ActionGrantAuthority.TTLSeconds)*time.Second, time.Now,
	)
}

// loadProductionActionGrantPrivateKey 解码独立 production owner 私钥并确认其与配置的 ActionGrant 验证公钥匹配。
func loadProductionActionGrantPrivateKey(config productionCoordinatorConfig) (ed25519.PrivateKey, error) {
	publicKey, err := decodeActionGrantPublicKey(config.ActionGrantAuthority.PublicKey)
	if err != nil {
		return nil, err
	}
	path, err := canonicalProductionFile("action grant private key", config.ActionGrantAuthority.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	data, err := readProductionCoordinatorConfig(path)
	if err != nil {
		return nil, fmt.Errorf("read action grant private key: %w", err)
	}
	var encoded productionActionGrantPrivateKey
	if err := gatecontract.DecodeStrictJSON(data, &encoded); err != nil {
		return nil, fmt.Errorf("decode action grant private key: %w", err)
	}
	privateKey, err := base64.StdEncoding.Strict().DecodeString(encoded.PrivateKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("action grant private key must be canonical base64 Ed25519")
	}
	key := ed25519.PrivateKey(privateKey)
	if !key.Public().(ed25519.PublicKey).Equal(publicKey) {
		return nil, errors.New("action grant Ed25519 private and public keys do not match")
	}
	return append(ed25519.PrivateKey(nil), key...), nil
}

func decodeActionGrantPublicKey(encoded string) (ed25519.PublicKey, error) {
	publicKey, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("action grant public key must be canonical base64 Ed25519")
	}
	return ed25519.PublicKey(publicKey), nil
}

func currentExecutableDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve action grant adapter executable: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open action grant adapter executable: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil)), nil
}

// Issue 在向调用方交付前先持久化已签名的 issued grant。
func (service *actionGrantService) Issue(
	ctx context.Context,
	intent actionGrantIntent,
) (gatecontract.ActionGrant, error) {
	if err := service.validateIntent(ctx, intent); err != nil {
		return gatecontract.ActionGrant{}, err
	}
	receiptDigest, err := gatecontract.ResultReceiptDigest(intent.Receipt)
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("digest action grant receipt: %w", err)
	}
	if existing, loadErr := service.store.actionGrantByNonce(ctx, intent.RequestNonce); loadErr == nil {
		return service.verifyIdempotentIssue(intent, receiptDigest, existing)
	} else if !errors.Is(loadErr, errActionGrantNotFound) {
		return gatecontract.ActionGrant{}, loadErr
	}
	now := service.now().UTC()
	request := service.buildRequest(intent, receiptDigest, now, now.Add(service.ttl))
	grantID, err := actionGrantID(request)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	grant, err := service.signer.SignActionGrant(gatecontract.ActionGrant{
		SchemaVersion: 1, GrantID: grantID, Request: request, State: gatecontract.ActionGrantStateIssued,
		IssuedAt: now, ExpiresAt: request.ExpiresAt,
	})
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	stored, inserted, err := service.store.issueActionGrant(ctx, grant)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	if inserted {
		return stored, nil
	}
	return service.verifyIdempotentIssue(intent, receiptDigest, stored)
}

// Verify 证明签名、持久 issued 状态、当前回执代次与精确动作绑定。
func (service *actionGrantService) Verify(
	ctx context.Context,
	grant gatecontract.ActionGrant,
	expected actionGrantExpectation,
) error {
	if err := service.verifier.VerifyActionGrant(grant); err != nil {
		return fmt.Errorf("verify action grant signature: %w", err)
	}
	stored, err := service.store.actionGrantByID(ctx, grant.GrantID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(stored, grant) {
		return errors.New("presented action grant does not match durable state")
	}
	if stored.State != gatecontract.ActionGrantStateIssued {
		return fmt.Errorf("action grant is %s", stored.State)
	}
	if !service.now().UTC().Before(stored.ExpiresAt) {
		_, expireErr := service.Expire(ctx, stored.GrantID)
		return errors.Join(errors.New("action grant expired"), expireErr)
	}
	if !actionGrantMatchesExpectation(stored.Request, expected) {
		return errors.New("action grant audience or action binding mismatch")
	}
	return service.verifyCurrentReceipt(ctx, stored.Request)
}

// Consume 执行唯一可成功的 issued 到 consumed 比较交换。
func (service *actionGrantService) Consume(
	ctx context.Context,
	grant gatecontract.ActionGrant,
	expected actionGrantExpectation,
) (gatecontract.ActionGrant, error) {
	if err := service.Verify(ctx, grant, expected); err != nil {
		return gatecontract.ActionGrant{}, err
	}
	consumedAt := service.now().UTC()
	if consumedAt.After(grant.ExpiresAt) {
		return gatecontract.ActionGrant{}, errors.New("action grant expired before consumption")
	}
	next := grant
	next.State = gatecontract.ActionGrantStateConsumed
	next.ConsumedAt = &consumedAt
	next.Signature = ""
	signed, err := service.signer.SignActionGrant(next)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	return service.store.transitionActionGrant(ctx, gatecontract.ActionGrantStateIssued, signed)
}

// Revoke 幂等保留既有 revoked 终态并拒绝其它终态。
func (service *actionGrantService) Revoke(ctx context.Context, grantID string) (gatecontract.ActionGrant, error) {
	grant, err := service.store.actionGrantByID(ctx, grantID)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	if err := service.verifier.VerifyActionGrant(grant); err != nil {
		return gatecontract.ActionGrant{}, err
	}
	if grant.State == gatecontract.ActionGrantStateRevoked {
		return grant, nil
	}
	if grant.State != gatecontract.ActionGrantStateIssued {
		return gatecontract.ActionGrant{}, fmt.Errorf("cannot revoke action grant in state %s", grant.State)
	}
	revokedAt := service.now().UTC()
	next := grant
	next.State = gatecontract.ActionGrantStateRevoked
	next.RevokedAt = &revokedAt
	next.Signature = ""
	signed, err := service.signer.SignActionGrant(next)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	return service.store.transitionActionGrant(ctx, gatecontract.ActionGrantStateIssued, signed)
}

// Expire 仅在签名期限到达后持久关闭 issued grant。
func (service *actionGrantService) Expire(ctx context.Context, grantID string) (gatecontract.ActionGrant, error) {
	grant, err := service.store.actionGrantByID(ctx, grantID)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	if err := service.verifier.VerifyActionGrant(grant); err != nil {
		return gatecontract.ActionGrant{}, err
	}
	if grant.State == gatecontract.ActionGrantStateExpired {
		return grant, nil
	}
	if grant.State != gatecontract.ActionGrantStateIssued {
		return gatecontract.ActionGrant{}, fmt.Errorf("cannot expire action grant in state %s", grant.State)
	}
	if service.now().UTC().Before(grant.ExpiresAt) {
		return gatecontract.ActionGrant{}, errors.New("action grant has not expired")
	}
	next := grant
	next.State = gatecontract.ActionGrantStateExpired
	next.Signature = ""
	signed, err := service.signer.SignActionGrant(next)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	return service.store.transitionActionGrant(ctx, gatecontract.ActionGrantStateIssued, signed)
}

// validateIntent 在签发前复验 attempt、passed receipt 与当前 generation。
func (service *actionGrantService) validateIntent(ctx context.Context, intent actionGrantIntent) error {
	for name, value := range map[string]string{
		"invocation_owner": intent.InvocationOwner, "action_policy": intent.ActionPolicy,
		"action_attempt_id": intent.ActionAttemptID, "request_nonce": intent.RequestNonce,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("action grant intent %s is required", name)
		}
	}
	if intent.Receipt.Status != gatecontract.ResultStatusPassed {
		return errors.New("action grant requires a passed result receipt")
	}
	if err := gatecontract.ValidateActionAttemptID(intent.ActionAttemptID); err != nil {
		return fmt.Errorf("validate action grant attempt: %w", err)
	}
	if err := service.receiptAuthority.VerifyCurrentResultReceipt(ctx, intent.Receipt); err != nil {
		return fmt.Errorf("verify current action grant receipt: %w", err)
	}
	return nil
}

func (service *actionGrantService) buildRequest(
	intent actionGrantIntent,
	receiptDigest string,
	requestedAt time.Time,
	expiresAt time.Time,
) gatecontract.GrantRequest {
	challenge := sha256.Sum256([]byte("action-grant-challenge\x00" + intent.RequestNonce))
	return gatecontract.GrantRequest{
		ReceiptID: intent.Receipt.ReceiptID, ReceiptDigest: receiptDigest, RepoID: intent.Receipt.RepoID,
		InvocationID: intent.Receipt.InvocationID, InvocationOwner: intent.InvocationOwner,
		SubscriberCapability: string(intent.Audience), Adapter: service.adapter,
		ProcessChallenge: fmt.Sprintf("sha256:%x", challenge), SourceTreeSHA: intent.Receipt.Source.SourceTreeSHA,
		Generation: intent.Receipt.Generation, Audience: intent.Audience, ActionPolicy: intent.ActionPolicy,
		RemoteURL: intent.RemoteURL, Ref: intent.Ref, OldSHA: intent.OldSHA, NewSHA: intent.NewSHA,
		ReleaseRepository: intent.ReleaseRepository, ReleaseTag: intent.ReleaseTag,
		ReleaseCommitSHA: intent.ReleaseCommitSHA, ReleaseAssets: append([]gatecontract.ReleaseAsset(nil), intent.ReleaseAssets...),
		ActionAttemptID: intent.ActionAttemptID, RequestNonce: intent.RequestNonce,
		RequestedAt: requestedAt, ExpiresAt: expiresAt,
	}
}

func (service *actionGrantService) verifyIdempotentIssue(
	intent actionGrantIntent,
	receiptDigest string,
	existing gatecontract.ActionGrant,
) (gatecontract.ActionGrant, error) {
	if err := service.verifier.VerifyActionGrant(existing); err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("verify persisted action grant: %w", err)
	}
	want := service.buildRequest(intent, receiptDigest, existing.IssuedAt, existing.ExpiresAt)
	if !reflect.DeepEqual(existing.Request, want) {
		return gatecontract.ActionGrant{}, errors.New("action grant nonce was already used for a different request")
	}
	return existing, nil
}

// verifyCurrentReceipt 从 durable job 重载并复验 grant 绑定的当前权威回执。
func (service *actionGrantService) verifyCurrentReceipt(
	ctx context.Context,
	request gatecontract.GrantRequest,
) error {
	record, err := service.store.jobByReceiptID(ctx, request.ReceiptID)
	if err != nil {
		return fmt.Errorf("load action grant receipt: %w", err)
	}
	receipt := record.Receipt
	if receipt == nil {
		return errors.New("action grant receipt is missing")
	}
	digest, err := gatecontract.ResultReceiptDigest(*receipt)
	if err != nil {
		return err
	}
	if actionGrantReceiptDrifted(*receipt, digest, request) {
		return errors.New("action grant drifted from its passed result receipt")
	}
	if err := service.receiptAuthority.VerifyCurrentResultReceipt(ctx, *receipt); err != nil {
		return fmt.Errorf("verify current action grant receipt: %w", err)
	}
	return nil
}

// actionGrantReceiptDrifted 比较回执摘要及全部授权身份字段。
func actionGrantReceiptDrifted(
	receipt gatecontract.ResultReceipt,
	digest string,
	request gatecontract.GrantRequest,
) bool {
	if digest != request.ReceiptDigest || receipt.RepoID != request.RepoID ||
		receipt.InvocationID != request.InvocationID || receipt.Generation != request.Generation ||
		receipt.Source.SourceTreeSHA != request.SourceTreeSHA || receipt.Status != gatecontract.ResultStatusPassed {
		return true
	}
	return request.Audience == gatecontract.ActionAudienceRelease &&
		(receipt.Source.Commit == nil || receipt.Source.Commit.SHA != request.ReleaseCommitSHA)
}

// SignActionGrant 使用宿主 Ed25519 私钥签署规范授权载荷。
func (signer *ed25519ActionGrantSigner) SignActionGrant(
	grant gatecontract.ActionGrant,
) (gatecontract.ActionGrant, error) {
	if signer == nil || len(signer.privateKey) != ed25519.PrivateKeySize {
		return gatecontract.ActionGrant{}, errors.New("action grant signer is not configured")
	}
	grant.Signer = signer.identity
	grant.Signature = ""
	payload, err := gatecontract.ActionGrantSigningPayload(grant)
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("build action grant signing payload: %w", err)
	}
	grant.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(signer.privateKey, payload))
	if err := grant.Validate(); err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("validate signed action grant: %w", err)
	}
	return grant, nil
}

// VerifyActionGrant 校验受信 signer identity 与 Ed25519 签名。
func (verifier *ed25519ActionGrantVerifier) VerifyActionGrant(grant gatecontract.ActionGrant) error {
	if verifier == nil || !reflect.DeepEqual(grant.Signer, verifier.identity) {
		return errors.New("action grant signer identity is not trusted")
	}
	return gatecontract.VerifyActionGrant(grant, verifier.publicKey)
}

// actionGrantMatchesExpectation 比较消费端独立观察到的全部外部动作字段。
func actionGrantMatchesExpectation(request gatecontract.GrantRequest, expected actionGrantExpectation) bool {
	return actionGrantIdentityMatches(request, expected) &&
		actionGrantGitPushMatches(request, expected) &&
		actionGrantReleaseMatches(request, expected)
}

// actionGrantIdentityMatches 校验 grant 请求与已签名动作身份的不可替换字段完全一致。
func actionGrantIdentityMatches(request gatecontract.GrantRequest, expected actionGrantExpectation) bool {
	return request.Audience == expected.Audience && request.RepoID == expected.RepoID &&
		request.InvocationID == expected.InvocationID && request.SourceTreeSHA == expected.SourceTreeSHA &&
		request.Generation == expected.Generation && request.ActionAttemptID == expected.ActionAttemptID
}

func actionGrantGitPushMatches(request gatecontract.GrantRequest, expected actionGrantExpectation) bool {
	return request.RemoteURL == expected.RemoteURL && request.Ref == expected.Ref &&
		request.OldSHA == expected.OldSHA && request.NewSHA == expected.NewSHA
}

func actionGrantReleaseMatches(request gatecontract.GrantRequest, expected actionGrantExpectation) bool {
	return request.ReleaseRepository == expected.ReleaseRepository && request.ReleaseTag == expected.ReleaseTag &&
		request.ReleaseCommitSHA == expected.ReleaseCommitSHA && reflect.DeepEqual(request.ReleaseAssets, expected.ReleaseAssets)
}

func actionGrantID(request gatecontract.GrantRequest) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode action grant identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("grant-%x", digest), nil
}

const coordinatorActionGrantSchema = `
CREATE TABLE IF NOT EXISTS coordinator_action_grants (
 grant_id TEXT PRIMARY KEY,
 request_nonce TEXT NOT NULL UNIQUE,
 receipt_id TEXT NOT NULL,
 state TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 grant_json BLOB NOT NULL
);`

var errActionGrantNotFound = errors.New("action grant not found")

// ensureCoordinatorActionGrantSchema 创建并核对 durable grant 状态表及 receipt 索引，未知表结构直接失败。
func ensureCoordinatorActionGrantSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, coordinatorActionGrantSchema); err != nil {
		return fmt.Errorf("initialize coordinator action grant SQLite: %w", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(coordinator_action_grants)")
	if err != nil {
		return fmt.Errorf("inspect coordinator action grant schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return errors.Join(fmt.Errorf("scan coordinator action grant schema: %w", err), rows.Close())
		}
		columns[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read coordinator action grant schema: %w", err)
	}
	for _, required := range []string{"grant_id", "request_nonce", "receipt_id", "state", "expires_at", "grant_json"} {
		if !columns[required] {
			return fmt.Errorf("coordinator action grant schema missing column %q", required)
		}
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS coordinator_action_grants_receipt
ON coordinator_action_grants(receipt_id)`); err != nil {
		return fmt.Errorf("create coordinator action grant receipt index: %w", err)
	}
	return nil
}

func (store *coordinatorStore) issueActionGrant(ctx context.Context, grant gatecontract.ActionGrant) (gatecontract.ActionGrant, bool, error) {
	encoded, err := encodeStoredActionGrant(grant)
	if err != nil {
		return gatecontract.ActionGrant{}, false, err
	}
	result, err := store.db.ExecContext(ctx, `INSERT INTO coordinator_action_grants (
grant_id, request_nonce, receipt_id, state, expires_at, grant_json
) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(request_nonce) DO NOTHING`,
		grant.GrantID, grant.Request.RequestNonce, grant.Request.ReceiptID, grant.State,
		grant.ExpiresAt.Format(time.RFC3339Nano), encoded,
	)
	if err != nil {
		return gatecontract.ActionGrant{}, false, fmt.Errorf("persist issued action grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return gatecontract.ActionGrant{}, false, fmt.Errorf("read action grant insert result: %w", err)
	}
	if rows == 1 {
		return grant, true, nil
	}
	existing, err := store.actionGrantByNonce(ctx, grant.Request.RequestNonce)
	return existing, false, err
}

func (store *coordinatorStore) actionGrantByID(ctx context.Context, grantID string) (gatecontract.ActionGrant, error) {
	return scanStoredActionGrant(store.db.QueryRowContext(ctx, `SELECT grant_id, request_nonce,
receipt_id, state, expires_at, grant_json FROM coordinator_action_grants WHERE grant_id = ?`, grantID))
}

func (store *coordinatorStore) actionGrantByNonce(ctx context.Context, nonce string) (gatecontract.ActionGrant, error) {
	return scanStoredActionGrant(store.db.QueryRowContext(ctx, `SELECT grant_id, request_nonce,
receipt_id, state, expires_at, grant_json FROM coordinator_action_grants WHERE request_nonce = ?`, nonce))
}

// transitionActionGrant 以 issued 状态为 compare-and-swap 前置条件，持久化唯一终态及其新签名。
func (store *coordinatorStore) transitionActionGrant(ctx context.Context, from gatecontract.ActionGrantState, next gatecontract.ActionGrant) (gatecontract.ActionGrant, error) {
	encoded, err := encodeStoredActionGrant(next)
	if err != nil {
		return gatecontract.ActionGrant{}, err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_action_grants
SET state = ?, expires_at = ?, grant_json = ? WHERE grant_id = ? AND state = ?`,
		next.State, next.ExpiresAt.Format(time.RFC3339Nano), encoded, next.GrantID, from,
	)
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("transition action grant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("read action grant transition result: %w", err)
	}
	if rows != 1 {
		current, loadErr := store.actionGrantByID(ctx, next.GrantID)
		if loadErr != nil {
			return gatecontract.ActionGrant{}, errors.Join(errors.New("action grant transition lost compare-and-swap"), loadErr)
		}
		return current, fmt.Errorf("action grant is already %s", current.State)
	}
	return next, nil
}

func encodeStoredActionGrant(grant gatecontract.ActionGrant) ([]byte, error) {
	if err := grant.Validate(); err != nil {
		return nil, fmt.Errorf("validate stored action grant: %w", err)
	}
	encoded, err := json.Marshal(grant)
	if err != nil {
		return nil, fmt.Errorf("encode stored action grant: %w", err)
	}
	return encoded, nil
}

// scanStoredActionGrant 严格解码授权载荷，并核对 SQL 索引列、过期时间与 canonical 签名内容未漂移。
func scanStoredActionGrant(row *sql.Row) (gatecontract.ActionGrant, error) {
	var grantID, nonce, receiptID, state, expiresAt string
	var encoded []byte
	if err := row.Scan(&grantID, &nonce, &receiptID, &state, &expiresAt, &encoded); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return gatecontract.ActionGrant{}, errActionGrantNotFound
		}
		return gatecontract.ActionGrant{}, fmt.Errorf("scan stored action grant: %w", err)
	}
	var grant gatecontract.ActionGrant
	if err := gatecontract.DecodeStrictJSON(encoded, &grant); err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("decode stored action grant: %w", err)
	}
	parsedExpiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return gatecontract.ActionGrant{}, fmt.Errorf("parse stored action grant expiry: %w", err)
	}
	if grant.GrantID != grantID || grant.Request.RequestNonce != nonce ||
		grant.Request.ReceiptID != receiptID || string(grant.State) != state || !grant.ExpiresAt.Equal(parsedExpiry) {
		return gatecontract.ActionGrant{}, errors.New("stored action grant columns drifted from canonical payload")
	}
	return grant, nil
}
