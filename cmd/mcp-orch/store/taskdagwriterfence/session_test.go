package taskdagwriterfence

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// TestWriterSessionTxBoundMutationAndRollback 验证 callback 事务绑定与错误回滚。
func TestWriterSessionTxBoundMutationAndRollback(t *testing.T) {
	db := openCandidateDB(t)
	initial := WriterFence{SchemaVersion: 1, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	bootstrapCandidate(t, db, initial)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE session_probe (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create session probe: %v", err)
	}
	session, err := OpenWriterSession(t.Context(), db, Admission{Fence: initial})
	if err != nil {
		t.Fatalf("OpenWriterSession() = %v", err)
	}

	assertSessionMutationCommits(t, session)
	assertSessionProbeCount(t, db, 1)
	assertSessionMutationRollsBack(t, session)
	assertSessionProbeCount(t, db, 1)
}

// assertSessionMutationCommits 验证 callback 获得事务绑定对象并可嵌套复用同一事务。
func assertSessionMutationCommits(t *testing.T, session *WriterSession) {
	t.Helper()
	callbackCalls := 0
	err := session.WithMutation(t.Context(), func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		callbackCalls++
		if _, ok := txdb.(*sql.DB); ok {
			t.Fatal("callback received *sql.DB instead of tx-bound DBTX")
		}
		if txq == nil {
			t.Fatal("callback received nil tx queries")
		}
		if _, err := txdb.ExecContext(t.Context(), `INSERT INTO session_probe(id, value) VALUES(1, 'committed')`); err != nil {
			return err
		}
		// WithImmediateTxOrReuse must keep the same transaction and observe
		// the uncommitted row through its tx-bound query set.
		return sqlctx.WithImmediateTxOrReuse(t.Context(), txdb, txq, func(_ *sqlc.Queries, reused sqlc.DBTX) error {
			var count int
			if err := reused.QueryRowContext(t.Context(), `SELECT count(*) FROM session_probe`).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return errors.New("tx reuse did not observe uncommitted session row")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("committing session mutation = %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("callback calls after success = %d, want 1", callbackCalls)
	}
}

// assertSessionMutationRollsBack 验证 callback 返回错误时业务写入不会部分提交。
func assertSessionMutationRollsBack(t *testing.T, session *WriterSession) {
	t.Helper()
	rollbackErr := errors.New("rollback session mutation")
	err := session.WithMutation(t.Context(), func(_ *sqlc.Queries, txdb sqlc.DBTX) error {
		if _, err := txdb.ExecContext(t.Context(), `INSERT INTO session_probe(id, value) VALUES(2, 'rolled-back')`); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rollback session mutation = %v, want rollback sentinel", err)
	}
}

// TestWriterSessionStaleFenceSkipsCallbackAndSticks 验证 stale CAS 在 callback 前阻断并 sticky。
func TestWriterSessionStaleFenceSkipsCallbackAndSticks(t *testing.T) {
	db := openCandidateDB(t)
	initial := WriterFence{SchemaVersion: 1, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	bootstrapCandidate(t, db, initial)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE session_probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create session probe: %v", err)
	}
	session, err := OpenWriterSession(t.Context(), db, Admission{Fence: initial})
	if err != nil {
		t.Fatalf("OpenWriterSession() = %v", err)
	}
	next := WriterFence{SchemaVersion: 1, WriterEpoch: 2, DeploymentFence: "deployment-2"}
	if err := Advance(t.Context(), db, initial, next); err != nil {
		t.Fatalf("Advance() = %v", err)
	}

	callbackCalls := 0
	err = session.WithMutation(t.Context(), func(_ *sqlc.Queries, txdb sqlc.DBTX) error {
		callbackCalls++
		_, err := txdb.ExecContext(t.Context(), `INSERT INTO session_probe(id) VALUES(1)`)
		return err
	})
	if !errors.Is(err, ErrStaleWriter) {
		t.Fatalf("stale session mutation = %v, want ErrStaleWriter", err)
	}
	if callbackCalls != 0 {
		t.Fatalf("stale callback calls = %d, want 0", callbackCalls)
	}
	assertSessionProbeCount(t, db, 0)

	err = session.WithMutation(t.Context(), func(*sqlc.Queries, sqlc.DBTX) error {
		callbackCalls++
		return nil
	})
	if !errors.Is(err, ErrAlreadyFenced) {
		t.Fatalf("second stale session mutation = %v, want ErrAlreadyFenced", err)
	}
	if callbackCalls != 0 {
		t.Fatalf("second stale callback calls = %d, want 0", callbackCalls)
	}
}

// TestWriterSessionRejectsNilDependencies 验证 nil/取消上下文均 fail-fast。
func TestWriterSessionRejectsNilDependencies(t *testing.T) {
	var session *WriterSession
	if err := session.WithMutation(context.Background(), func(*sqlc.Queries, sqlc.DBTX) error { return nil }); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("nil session mutation = %v, want ErrInvalidAdmission", err)
	}
	db := openCandidateDB(t)
	initial := WriterFence{SchemaVersion: 1, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	bootstrapCandidate(t, db, initial)
	opened, err := OpenWriterSession(t.Context(), db, Admission{Fence: initial})
	if err != nil {
		t.Fatalf("OpenWriterSession() = %v", err)
	}
	if err := opened.WithMutation(context.Background(), nil); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("nil callback mutation = %v, want ErrInvalidAdmission", err)
	}
	var typedNilCtx *typedNilContext
	if err := opened.WithMutation(typedNilCtx, func(*sqlc.Queries, sqlc.DBTX) error { return nil }); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("typed-nil context mutation = %v, want ErrInvalidAdmission", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if err := opened.WithMutation(ctx, func(*sqlc.Queries, sqlc.DBTX) error { return nil }); err == nil {
		t.Fatal("canceled context mutation unexpectedly succeeded")
	}
}

type typedNilContext struct{}

func (*typedNilContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*typedNilContext) Done() <-chan struct{}       { return nil }
func (*typedNilContext) Err() error                  { return nil }
func (*typedNilContext) Value(any) any               { return nil }

// TestWriterSessionCommitReplayBarrierRejectsSecondCallback 验证 commit unknown 重试不会重放业务 callback。
func TestWriterSessionCommitReplayBarrierRejectsSecondCallback(t *testing.T) {
	session := &WriterSession{owner: &Owner{}}
	callbackSucceeded := true
	callbackRan := false
	var callbackErr error
	err := session.mutationAttempt(t.Context(), nil, nil, func(*sqlc.Queries, sqlc.DBTX) error {
		callbackRan = true
		return nil
	}, &callbackRan, &callbackSucceeded, &callbackErr)
	if !errors.Is(err, ErrCommitUnknown) {
		t.Fatalf("replayed mutation attempt = %v, want ErrCommitUnknown", err)
	}
	if !callbackSucceeded || callbackErr != nil {
		t.Fatalf("replay barrier state changed: succeeded=%v callbackErr=%v", callbackSucceeded, callbackErr)
	}
}

func assertSessionProbeCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM session_probe`).Scan(&got); err != nil {
		t.Fatalf("count session probe rows: %v", err)
	}
	if got != want {
		t.Fatalf("session probe rows = %d, want %d", got, want)
	}
}
