package localci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// issueOpaqueHandshake 生成 owner-local 单次握手检查点；它不是 SignedJobToken。
func (s *schedulerState) issueOpaqueHandshake(
	ctx context.Context,
	binding opaqueHandshakeBinding,
	ttl time.Duration,
) (string, error) {
	if err := validateOpaqueHandshakeBinding(binding); err != nil {
		return "", err
	}
	if ttl <= 0 {
		return "", errors.New("opaque handshake TTL must be positive")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate opaque handshake: %w", err)
	}
	token := hex.EncodeToString(secret)
	hash := hashOpaqueHandshake(token)
	expiresAt := s.now().Add(ttl).UnixNano()
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO scheduler_handshake_tokens
		(token_hash, job_id, invocation_id, container_id, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		hash,
		binding.jobID,
		binding.invocationID,
		binding.containerID,
		expiresAt,
	); err != nil {
		return "", fmt.Errorf("persist opaque handshake: %w", err)
	}
	return token, nil
}

// consumeOpaqueHandshake 校验窄 owner 绑定，并通过 CAS 单次消费握手 token。
func (s *schedulerState) consumeOpaqueHandshake(
	ctx context.Context,
	token string,
	binding opaqueHandshakeBinding,
) (retErr error) {
	if strings.TrimSpace(token) == "" {
		return errors.New("opaque handshake token is required")
	}
	if err := validateOpaqueHandshakeBinding(binding); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin opaque handshake consumption: %w", err)
	}
	defer rollbackTransaction(tx, &retErr, "opaque handshake consumption")
	record, err := readOpaqueHandshake(ctx, tx, hashOpaqueHandshake(token))
	if err != nil {
		return err
	}
	now := s.now()
	if err := validateStoredOpaqueHandshake(record, binding, now); err != nil {
		return err
	}
	if err := consumeStoredOpaqueHandshake(ctx, tx, token, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit opaque handshake consumption: %w", err)
	}
	return nil
}

type storedOpaqueHandshake struct {
	binding    opaqueHandshakeBinding
	expiresAt  int64
	consumedAt sql.NullInt64
}

func readOpaqueHandshake(ctx context.Context, tx *sql.Tx, hash string) (storedOpaqueHandshake, error) {
	var record storedOpaqueHandshake
	err := tx.QueryRowContext(
		ctx,
		`SELECT job_id, invocation_id, container_id, expires_at, consumed_at
		FROM scheduler_handshake_tokens WHERE token_hash = ?`,
		hash,
	).Scan(
		&record.binding.jobID,
		&record.binding.invocationID,
		&record.binding.containerID,
		&record.expiresAt,
		&record.consumedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return storedOpaqueHandshake{}, errors.New("unknown opaque handshake token")
	}
	if err != nil {
		return storedOpaqueHandshake{}, fmt.Errorf("read opaque handshake token: %w", err)
	}
	return record, nil
}

func validateStoredOpaqueHandshake(record storedOpaqueHandshake, binding opaqueHandshakeBinding, now time.Time) error {
	if record.binding != binding {
		return errors.New("opaque handshake binding mismatch")
	}
	if record.consumedAt.Valid {
		return errHandshakeConsumed
	}
	if !now.Before(time.Unix(0, record.expiresAt)) {
		return errHandshakeExpired
	}
	return nil
}

func consumeStoredOpaqueHandshake(ctx context.Context, tx *sql.Tx, token string, now time.Time) error {
	result, err := tx.ExecContext(
		ctx,
		"UPDATE scheduler_handshake_tokens SET consumed_at = ? WHERE token_hash = ? AND consumed_at IS NULL",
		now.UnixNano(),
		hashOpaqueHandshake(token),
	)
	if err != nil {
		return fmt.Errorf("consume opaque handshake token: %w", err)
	}
	if err := requireOneRow(result, "consume opaque handshake token"); err != nil {
		return errHandshakeConsumed
	}
	return nil
}

func validateOpaqueHandshakeBinding(binding opaqueHandshakeBinding) error {
	if strings.TrimSpace(string(binding.jobID)) == "" {
		return errors.New("opaque handshake job ID is required")
	}
	if strings.TrimSpace(string(binding.invocationID)) == "" {
		return errors.New("opaque handshake invocation ID is required")
	}
	if strings.TrimSpace(binding.containerID) == "" {
		return errors.New("opaque handshake container ID is required")
	}
	return nil
}

func hashOpaqueHandshake(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
