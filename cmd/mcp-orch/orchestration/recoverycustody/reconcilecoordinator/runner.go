package reconcilecoordinator

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type claimedJob struct {
	JobID      string
	Handle     recoverycustody.SubmissionHandle
	ClaimToken string
	Attempts   int
}

type resultDecision struct {
	status   string
	detail   string
	state    JobState
	next     time.Time
	terminal bool
}

// RunOnce 最多 claim 一个到期 job 并精确 reconcile 一次；所有 mutation 均校验 owner fence 与 claim token。
func (c *Coordinator) RunOnce(ctx context.Context) (bool, error) {
	if c.fenced.Load() {
		return false, ErrOwnerFenced
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := clockNow(c.cfg)
	job, err := c.claimDue(ctx, now)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	outcome, reconcileErr := c.authority.ReconcileResolution(ctx, job.Handle)
	return true, c.recordResult(ctx, job, outcome, reconcileErr, clockNow(c.cfg))
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (c *Coordinator) claimDue(ctx context.Context, now time.Time) (claimedJob, error) {
	var claimed claimedJob
	nowText := canonicalTime(now)
	err := c.withImmediate(ctx, func(conn *sql.Conn) error {
		if err := c.assertOwner(ctx, conn, now); err != nil {
			return err
		}
		jobID, raw, attempts, err := selectDueJob(ctx, conn, nowText, c.cfg.OwnerEpoch)
		if err != nil {
			return err
		}
		handle, err := decodeClaimHandle(raw)
		if err != nil {
			return err
		}
		token, err := randomToken()
		if err != nil {
			return err
		}
		leaseUntil := canonicalTime(now.Add(c.cfg.ClaimTTL))
		if err := updateClaim(ctx, conn, c.cfg, jobID, token, leaseUntil, nowText); err != nil {
			return err
		}
		if err := c.appendJournal(ctx, conn, jobID, "claimed", StateRunning, token, "single owner claim", nowText); err != nil {
			return err
		}
		claimed = claimedJob{JobID: jobID, Handle: handle, ClaimToken: token, Attempts: attempts + 1}
		return nil
	})
	return claimed, err
}

func selectDueJob(ctx context.Context, conn *sql.Conn, nowText string, ownerEpoch int64) (string, string, int, error) {
	var jobID, raw string
	var attempts int
	err := conn.QueryRowContext(ctx, `SELECT job_id, handle_json, attempts FROM reconcile_jobs
WHERE ((state IN ('queued', 'retry_wait') AND next_run_at <= ?)
   OR (state = 'running' AND (lease_expires_at <= ? OR owner_epoch <> ?)))
ORDER BY next_run_at, job_id LIMIT 1`, nowText, nowText, ownerEpoch).Scan(&jobID, &raw, &attempts)
	return jobID, raw, attempts, err
}

func decodeClaimHandle(raw string) (recoverycustody.SubmissionHandle, error) {
	var handle recoverycustody.SubmissionHandle
	if err := json.Unmarshal([]byte(raw), &handle); err != nil {
		return handle, fmt.Errorf("%w: handle JSON", ErrSchemaMismatch)
	}
	if err := handle.Validate(); err != nil {
		return handle, fmt.Errorf("%w: handle validation", ErrSchemaMismatch)
	}
	return handle, nil
}

func updateClaim(ctx context.Context, conn *sql.Conn, cfg Config, jobID, token, leaseUntil, nowText string) error {
	result, err := conn.ExecContext(ctx, `UPDATE reconcile_jobs SET state='running', owner_id=?, owner_epoch=?, claim_token=?, lease_expires_at=?, attempts=attempts+1, updated_at=?
WHERE job_id=? AND ((state IN ('queued','retry_wait') AND next_run_at <= ?) OR (state='running' AND (lease_expires_at <= ? OR owner_epoch <> ?)))`, cfg.OwnerID, cfg.OwnerEpoch, token, leaseUntil, nowText, jobID, nowText, nowText, cfg.OwnerEpoch)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrOwnerFenced
	}
	return nil
}

func (c *Coordinator) assertOwner(ctx context.Context, conn *sql.Conn, now time.Time) error {
	var id, token, leaseText string
	var epoch int64
	if err := conn.QueryRowContext(ctx, `SELECT owner_id, owner_epoch, owner_token, lease_expires_at FROM coordinator_owner WHERE singleton=1`).Scan(&id, &epoch, &token, &leaseText); err != nil {
		return ErrSchemaMismatch
	}
	if id != c.cfg.OwnerID || epoch != c.cfg.OwnerEpoch || token != c.cfg.OwnerToken {
		return ErrOwnerFenced
	}
	lease, err := time.Parse(time.RFC3339Nano, leaseText)
	if err != nil || !now.Before(lease) {
		return ErrOwnerFenced
	}
	_, err = conn.ExecContext(ctx, `UPDATE coordinator_owner SET lease_expires_at=?, updated_at=? WHERE singleton=1 AND owner_id=? AND owner_epoch=? AND owner_token=?`, canonicalTime(now.Add(c.cfg.ClaimTTL)), canonicalTime(now), id, epoch, token)
	return err
}

func (c *Coordinator) recordResult(ctx context.Context, job claimedJob, outcome recoverycustody.Outcome, reconcileErr error, now time.Time) error {
	decision := c.decideResult(job, outcome, reconcileErr, now)
	return c.persistResult(ctx, job, decision, now)
}

func (c *Coordinator) decideResult(job claimedJob, outcome recoverycustody.Outcome, reconcileErr error, now time.Time) resultDecision {
	status, detail, reconcileErr := normalizeResult(job.Handle, outcome, reconcileErr)
	terminal := reconcileErr == nil && isTerminalStatus(status)
	if !terminal && job.Attempts >= c.cfg.MaxAttempts {
		status = recoverycustody.StatusIndeterminate
		terminal = true
		if detail == "" {
			detail = "max retry attempts exhausted"
		}
	}
	if !terminal && detail == "" {
		detail = "authority still pending or indeterminate"
	}
	state := StateRetryWait
	next := now.Add(c.retryDelay(job.Attempts))
	if terminal {
		state = StateSucceeded
		if status != recoverycustody.StatusAvailable && status != recoverycustody.StatusAborted {
			state = StateIndeterminate
		}
		next = now
	}
	return resultDecision{status: status, detail: detail, state: state, next: next, terminal: terminal}
}

func normalizeResult(expected recoverycustody.SubmissionHandle, outcome recoverycustody.Outcome, reconcileErr error) (string, string, error) {
	status := outcome.Status
	detail := ""
	if reconcileErr != nil {
		// Authority errors can contain credentials, local paths, request payloads,
		// or provider diagnostics. Persist only a bounded public classification.
		detail = "authority reconciliation failed"
	}
	if reconcileErr != nil && status == "" {
		status = recoverycustody.StatusIndeterminate
	}
	if !isKnownStatus(status) {
		reconcileErr = fmt.Errorf("%w: unsupported authority status", ErrSchemaMismatch)
		status = recoverycustody.StatusIndeterminate
		detail = "authority returned unsupported status"
	}
	status, detail, reconcileErr = normalizeHandle(expected, outcome, status, detail, reconcileErr)
	status, detail, reconcileErr = normalizeReceipt(outcome, status, detail, reconcileErr)
	return status, detail, reconcileErr
}

func normalizeHandle(expected recoverycustody.SubmissionHandle, outcome recoverycustody.Outcome, status, detail string, reconcileErr error) (string, string, error) {
	if reconcileErr != nil {
		return status, detail, reconcileErr
	}
	if outcome.Handle != expected {
		return recoverycustody.StatusIndeterminate, "authority returned a different handle", recoverycustody.ErrCustodyConflict
	}
	if err := outcome.Handle.Validate(); err != nil {
		return recoverycustody.StatusIndeterminate, "authority returned invalid handle", err
	}
	return status, detail, nil
}

func normalizeReceipt(outcome recoverycustody.Outcome, status, detail string, reconcileErr error) (string, string, error) {
	if reconcileErr != nil || !isTerminalStatus(status) || outcome.Receipt != nil {
		return status, detail, reconcileErr
	}
	err := fmt.Errorf("%w: terminal outcome lacks receipt", ErrSchemaMismatch)
	return recoverycustody.StatusIndeterminate, "terminal outcome missing receipt", err
}

func isKnownStatus(status string) bool {
	switch status {
	case recoverycustody.StatusPending, recoverycustody.StatusIndeterminate,
		recoverycustody.StatusAvailable, recoverycustody.StatusAborted:
		return true
	default:
		return false
	}
}

func isTerminalStatus(status string) bool {
	return status == recoverycustody.StatusAvailable || status == recoverycustody.StatusAborted
}

func (c *Coordinator) persistResult(ctx context.Context, job claimedJob, decision resultDecision, now time.Time) error {
	return c.withImmediate(ctx, func(conn *sql.Conn) error {
		if err := c.assertOwner(ctx, conn, now); err != nil {
			return err
		}
		var state JobState
		var owner, token string
		var epoch int64
		if err := conn.QueryRowContext(ctx, `SELECT state, owner_id, claim_token, owner_epoch FROM reconcile_jobs WHERE job_id=?`, job.JobID).Scan(&state, &owner, &token, &epoch); err != nil {
			return ErrJobNotFound
		}
		if state != StateRunning || owner != c.cfg.OwnerID || epoch != c.cfg.OwnerEpoch || token != job.ClaimToken {
			return ErrOwnerFenced
		}
		nowText := canonicalTime(now)
		_, err := conn.ExecContext(ctx, `UPDATE reconcile_jobs SET state=?, next_run_at=?, owner_id=NULL, owner_epoch=NULL, claim_token=NULL, lease_expires_at=NULL, last_status=?, last_error=?, updated_at=? WHERE job_id=? AND state='running' AND owner_id=? AND owner_epoch=? AND claim_token=?`, decision.state, canonicalTime(decision.next), decision.status, decision.detail, nowText, job.JobID, c.cfg.OwnerID, c.cfg.OwnerEpoch, job.ClaimToken)
		if err != nil {
			return err
		}
		event := "retry_scheduled"
		if decision.terminal {
			event = "reconciled_terminal"
		}
		if err := c.appendJournal(ctx, conn, job.JobID, event, decision.state, "", decision.detail, nowText); err != nil {
			return err
		}
		return c.appendAudit(ctx, conn, job.JobID, event, decision.state, decision.status, decision.detail, nowText)
	})
}

func (c *Coordinator) retryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return c.cfg.RetryBase
	}
	shift := attempt - 1
	if shift > 30 {
		shift = 30
	}
	d := c.cfg.RetryBase * time.Duration(uint64(1)<<uint(shift))
	if d < 0 || d > c.cfg.RetryMax {
		return c.cfg.RetryMax
	}
	return d
}

func (c *Coordinator) withImmediate(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if err := fn(conn); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		// The transaction may have committed despite the driver error. Stop this
		// process from issuing a second claim/reconcile until a supervisor decides
		// which durable outcome is authoritative.
		c.fenced.Store(true)
		// Deliberately omit the driver error: it may contain a filesystem path or
		// backend diagnostics. ErrCommitOutcomeUnknown is the only safe contract.
		return ErrCommitOutcomeUnknown
	}
	return nil
}

func (c *Coordinator) appendJournal(ctx context.Context, conn *sql.Conn, jobID, event string, state JobState, token, detail, now string) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO reconcile_journal(job_id,event,state,claim_token,detail,created_at) VALUES(?,?,?,?,?,?)`, jobID, event, state, nullable(token), detail, now)
	return err
}

func (c *Coordinator) appendAudit(ctx context.Context, conn *sql.Conn, jobID, event string, state JobState, status, detail, now string) error {
	_, err := conn.ExecContext(ctx, `INSERT INTO reconcile_audit(job_id,event,state,status,detail,owner_id,owner_epoch,created_at) VALUES(?,?,?,?,?,?,?,?)`, jobID, event, state, status, detail, c.cfg.OwnerID, c.cfg.OwnerEpoch, now)
	return err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// Takeover 原子推进 coordinator owner fence；旧实例立即失效，in-flight claim 由 successor 接管。
func (c *Coordinator) Takeover(ctx context.Context, nextEpoch int64, nextToken string) error {
	return c.TakeoverTo(ctx, c.cfg.OwnerID, nextEpoch, nextToken)
}

// TakeoverTo 原子推进 epoch/token 并切换 owner 身份；当前进程立即失效，supervisor 必须重开 successor。
func (c *Coordinator) TakeoverTo(ctx context.Context, nextOwnerID string, nextEpoch int64, nextToken string) error {
	if c.fenced.Load() {
		return ErrOwnerFenced
	}
	if err := validateTakeoverInput(c.cfg.OwnerEpoch, nextEpoch, nextToken); err != nil {
		return err
	}
	if strings.TrimSpace(nextOwnerID) == "" || len(nextOwnerID) > 256 || strings.TrimSpace(nextOwnerID) != nextOwnerID {
		return ErrOwnerFenced
	}
	err := c.withImmediate(ctx, func(conn *sql.Conn) error { return c.takeoverTx(ctx, conn, nextOwnerID, nextEpoch, nextToken) })
	if err == nil {
		// The takeover caller is the old process. It must stop mutating immediately;
		// a successor reopens with the new epoch/token after supervisor handoff.
		c.fenced.Store(true)
	}
	return err
}

func validateTakeoverInput(currentEpoch, nextEpoch int64, token string) error {
	if nextEpoch <= currentEpoch || len(token) != 64 || !isLowerHex(token) {
		return ErrOwnerFenced
	}
	return nil
}

func (c *Coordinator) takeoverTx(ctx context.Context, conn *sql.Conn, nextOwnerID string, nextEpoch int64, nextToken string) error {
	owner, epoch, token, err := c.readOwner(ctx, conn)
	if err != nil {
		return err
	}
	if owner != c.cfg.OwnerID || epoch != c.cfg.OwnerEpoch || token != c.cfg.OwnerToken {
		return ErrOwnerFenced
	}
	now := clockNow(c.cfg)
	result, err := conn.ExecContext(ctx, `UPDATE coordinator_owner SET owner_id=?, owner_epoch=?, owner_token=?, lease_expires_at=?, updated_at=? WHERE singleton=1 AND owner_id=? AND owner_epoch=? AND owner_token=?`, nextOwnerID, nextEpoch, nextToken, canonicalTime(now.Add(c.cfg.ClaimTTL)), canonicalTime(now), owner, epoch, token)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrOwnerFenced
	}
	return nil
}

func (c *Coordinator) readOwner(ctx context.Context, conn *sql.Conn) (string, int64, string, error) {
	var owner, token string
	var epoch int64
	if err := conn.QueryRowContext(ctx, `SELECT owner_id, owner_epoch, owner_token FROM coordinator_owner WHERE singleton=1`).Scan(&owner, &epoch, &token); err != nil {
		return "", 0, "", ErrSchemaMismatch
	}
	return owner, epoch, token, nil
}

// ListJobs 返回供 supervisor 与审计工具使用的 durable job 快照。
func (c *Coordinator) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT job_id, operation_id, handle_json, state, attempts, next_run_at, COALESCE(owner_id,''), COALESCE(owner_epoch,0), COALESCE(claim_token,''), COALESCE(lease_expires_at,''), COALESCE(last_status,''), COALESCE(last_error,''), created_at, updated_at FROM reconcile_jobs ORDER BY created_at, job_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Job, 0)
	for rows.Next() {
		var j Job
		var raw, next, lease, created, updated string
		if err := rows.Scan(&j.JobID, &j.OperationID, &raw, &j.State, &j.Attempts, &next, &j.OwnerID, &j.OwnerEpoch, &j.ClaimToken, &lease, &j.LastStatus, &j.LastError, &created, &updated); err != nil {
			return nil, err
		}
		if err := decodeJobTimes(&j, raw, next, lease, created, updated); err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}

func decodeJobTimes(j *Job, raw, next, lease, created, updated string) error {
	if err := json.Unmarshal([]byte(raw), &j.Handle); err != nil || j.Handle.Validate() != nil {
		return ErrSchemaMismatch
	}
	parsed, err := parseTime(next)
	if err != nil {
		return err
	}
	j.NextRunAt = parsed
	if lease != "" {
		parsed, err = parseTime(lease)
		if err != nil {
			return err
		}
		j.LeaseExpiresAt = parsed
	}
	parsed, err = parseTime(created)
	if err != nil {
		return err
	}
	j.CreatedAt = parsed
	parsed, err = parseTime(updated)
	if err != nil {
		return err
	}
	j.UpdatedAt = parsed
	return nil
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, ErrSchemaMismatch
	}
	return parsed, nil
}
