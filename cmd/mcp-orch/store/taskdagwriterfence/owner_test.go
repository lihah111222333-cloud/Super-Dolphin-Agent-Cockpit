package taskdagwriterfence

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestWriterFenceRejectsImplicitAdmission(t *testing.T) {
	fence := WriterFence{SchemaVersion: WriterFenceSchemaVersion, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	for name, value := range map[string]WriterFence{
		"zero epoch":      {SchemaVersion: 1, DeploymentFence: "deployment-1"},
		"missing deploy":  {SchemaVersion: 1, WriterEpoch: 1},
		"trim drift":      {SchemaVersion: 1, WriterEpoch: 1, DeploymentFence: " deployment-1"},
		"unknown version": {SchemaVersion: 2, WriterEpoch: 1, DeploymentFence: "deployment-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := value.Validate(); !errors.Is(err, ErrInvalidAdmission) {
				t.Fatalf("Validate() = %v, want ErrInvalidAdmission", err)
			}
		})
	}
	if err := fence.Validate(); err != nil {
		t.Fatalf("valid fence rejected: %v", err)
	}
}

func TestWriterFenceMutationCASStaleIsZeroRows(t *testing.T) {
	db := openCandidateDB(t)
	initial := WriterFence{SchemaVersion: 1, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	bootstrapCandidate(t, db, initial)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE mutation_probe (id INTEGER PRIMARY KEY, writer_epoch INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create mutation probe: %v", err)
	}
	owner, err := Open(t.Context(), db, Admission{Fence: initial})
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	next := WriterFence{SchemaVersion: 1, WriterEpoch: 2, DeploymentFence: "deployment-2"}
	if err := Advance(t.Context(), db, initial, next); err != nil {
		t.Fatalf("Advance() = %v", err)
	}
	called := false
	err = owner.Mutate(t.Context(), func(tx *sql.Tx) error {
		called = true
		_, execErr := tx.ExecContext(t.Context(), `INSERT INTO mutation_probe(id, writer_epoch) VALUES(1, 1)`)
		return execErr
	})
	if !errors.Is(err, ErrStaleWriter) || called {
		t.Fatalf("stale Mutate() = %v, callback=%v; want ErrStaleWriter before callback", err, called)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM mutation_probe`).Scan(&count); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("stale mutation rows = %d, want zero", count)
	}
	if err := owner.Mutate(t.Context(), func(*sql.Tx) error { return nil }); !errors.Is(err, ErrAlreadyFenced) {
		t.Fatalf("second stale Mutate() = %v, want ErrAlreadyFenced", err)
	}
}

func TestWriterFenceSubprocessABCutover(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "candidate.db")
	initial := WriterFence{SchemaVersion: 1, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	db := openDBAt(t, dbPath)
	bootstrapCandidate(t, db, initial)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE mutation_probe (id INTEGER PRIMARY KEY, writer_epoch INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create stable mutation probe: %v", err)
	}
	defer db.Close()
	ready := filepath.Join(t.TempDir(), "ready")
	proceed := filepath.Join(t.TempDir(), "proceed")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWriterEpochSubprocessHelper$", "-test.v")
	cmd.Env = append(os.Environ(),
		"TASKDAG_WRITER_FENCE_HELPER=1",
		"TASKDAG_WRITER_FENCE_DB="+dbPath,
		"TASKDAG_WRITER_FENCE_READY="+ready,
		"TASKDAG_WRITER_FENCE_PROCEED="+proceed,
	)
	running, err := startCommand(cmd)
	if err != nil {
		t.Fatalf("start subprocess A: %v", err)
	}
	waitForFile(t, ready)
	next := WriterFence{SchemaVersion: 1, WriterEpoch: 2, DeploymentFence: "deployment-2"}
	if err := Advance(t.Context(), db, initial, next); err != nil {
		t.Fatalf("B Advance() = %v", err)
	}
	if err := os.WriteFile(proceed, []byte("go"), 0o600); err != nil {
		t.Fatalf("signal subprocess A: %v", err)
	}
	if err := running.Wait(); err != nil {
		t.Fatalf("subprocess A error: %v; output=%s", err, running.Output())
	}
	if !strings.Contains(running.Output(), "stale_writer") {
		t.Fatalf("subprocess A output=%q, want stale_writer", running.Output())
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM mutation_probe`).Scan(&count); err != nil {
		t.Fatalf("count subprocess rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("subprocess stale rows = %d, want zero", count)
	}
}

func TestWriterEpochSubprocessHelper(t *testing.T) {
	if os.Getenv("TASKDAG_WRITER_FENCE_HELPER") != "1" {
		return
	}
	db := openDBAt(t, os.Getenv("TASKDAG_WRITER_FENCE_DB"))
	defer db.Close()
	fence := WriterFence{SchemaVersion: 1, WriterEpoch: 1, DeploymentFence: "deployment-1"}
	owner, err := Open(t.Context(), db, Admission{Fence: fence})
	if err != nil {
		t.Fatal(err)
	}
	// Keep an already-open SQLite connection alive while B performs cutover.
	// The mutation below uses the pool's second connection, proving that an old
	// process holding a RW connection is still fenced by the DB-resident CAS.
	db.SetMaxOpenConns(2)
	heldConn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer heldConn.Close()
	if err := os.WriteFile(os.Getenv("TASKDAG_WRITER_FENCE_READY"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, os.Getenv("TASKDAG_WRITER_FENCE_PROCEED"))
	err = owner.Mutate(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO mutation_probe(id, writer_epoch) VALUES(1, 1)`)
		return err
	})
	if errors.Is(err, ErrStaleWriter) {
		fmt.Println("stale_writer")
		return
	}
	t.Fatalf("A mutation error = %v, want ErrStaleWriter", err)
}

func openCandidateDB(t *testing.T) *sql.DB {
	t.Helper()
	return openDBAt(t, filepath.Join(t.TempDir(), "candidate.db"))
}

func openDBAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("open candidate SQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func bootstrapCandidate(t *testing.T, db *sql.DB, fence WriterFence) {
	t.Helper()
	if err := Bootstrap(t.Context(), db, fence); err != nil {
		t.Fatalf("Bootstrap() = %v", err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

type runningCommand struct {
	cmd    *exec.Cmd
	output strings.Builder
}

// startCommand 启动测试子进程但不额外创建裸 goroutine，由调用方显式 Wait。
func startCommand(cmd *exec.Cmd) (*runningCommand, error) {
	running := &runningCommand{cmd: cmd}
	cmd.Stdout = &running.output
	cmd.Stderr = &running.output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return running, nil
}

// Wait 等待测试子进程退出并返回其退出错误。
func (c *runningCommand) Wait() error {
	if c == nil || c.cmd == nil {
		return errors.New("running command is nil")
	}
	return c.cmd.Wait()
}

// Output 返回子进程已收集的诊断输出。
func (c *runningCommand) Output() string {
	if c == nil {
		return ""
	}
	return c.output.String()
}
