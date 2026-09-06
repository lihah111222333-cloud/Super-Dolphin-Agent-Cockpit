package sqliteauthority

import (
	"errors"
	"testing"

	platformsqlite "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TestAuthorityStaleWriterSubmitFailsBeforeBusinessCallback 验证旧 token 的
// Submit 在业务回调前被 DB-resident CAS 拒绝且不产生 submission 行。
func TestAuthorityStaleWriterSubmitFailsBeforeBusinessCallback(t *testing.T) {
	config := authorityTestConfig(t, "stale-submit")
	authority := openWriterEpochAuthority(t, config)
	defer closeOutcomeAuthority(t, authority)
	next := config.WriterFence()
	next.WriterEpoch = 2
	next.DeploymentFence = "deployment-2"
	if err := TakeoverAuthority(t.Context(), config, acceptingLease{}, next); err != nil {
		t.Fatalf("TakeoverAuthority() error = %v", err)
	}

	outcome, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if !errors.Is(err, ErrStaleWriter) || outcome.Handle.OperationID != "" || !authority.queryOnly.Load() {
		t.Fatalf("stale Submit() = (%+v, %v), queryOnly=%v; want stale, zero outcome and sticky query-only", outcome, err, authority.queryOnly.Load())
	}
	if got := countOutcomeRows(t, authority.db, "submissions"); got != 0 {
		t.Fatalf("stale Submit() wrote %d submissions, want 0", got)
	}
	if _, err := authority.Submit(t.Context(), schemaTestRequest(t)); !errors.Is(err, ErrAuthorityReadOnly) {
		t.Fatalf("second Submit() error = %v, want ErrAuthorityReadOnly", err)
	}
}

// TestAuthorityStaleWriterResolveFailsBeforeBusinessCallback 验证旧 token 的
// Resolve 在候选插入前失败，并保留已有 pending submission。
func TestAuthorityStaleWriterResolveFailsBeforeBusinessCallback(t *testing.T) {
	config := authorityTestConfig(t, "stale-resolve")
	authority := openWriterEpochAuthority(t, config)
	defer closeOutcomeAuthority(t, authority)
	pending, err := authority.Submit(t.Context(), schemaTestRequest(t))
	if err != nil {
		t.Fatalf("seed Submit() error = %v", err)
	}
	next := config.WriterFence()
	next.WriterEpoch = 2
	next.DeploymentFence = "deployment-2"
	if err := TakeoverAuthority(t.Context(), config, acceptingLease{}, next); err != nil {
		t.Fatalf("TakeoverAuthority() error = %v", err)
	}
	resolution := outcomeResolutionRequest(t, pending.Handle, "stale-resolve")
	outcome, err := authority.Resolve(t.Context(), resolution)
	if !errors.Is(err, ErrStaleWriter) || outcome.Handle.OperationID != "" || !authority.queryOnly.Load() {
		t.Fatalf("stale Resolve() = (%+v, %v), queryOnly=%v; want stale, zero outcome and sticky query-only", outcome, err, authority.queryOnly.Load())
	}
	if got := countOutcomeRows(t, authority.db, "resolutions"); got != 0 {
		t.Fatalf("stale Resolve() wrote %d resolutions, want 0", got)
	}
	if got := countOutcomeRows(t, authority.db, "submissions"); got != 1 {
		t.Fatalf("stale Resolve() changed submissions to %d, want 1", got)
	}
}

// TestAuthorityTakeoverRequiresLeaseAndFencesCurrentInstance 验证高层接管在
// authority lease 内推进 token，旧实例立即 query-only，successor 可按新 token 打开。
func TestAuthorityTakeoverRequiresLeaseAndFencesCurrentInstance(t *testing.T) {
	config := authorityTestConfig(t, "takeover")
	authority := openWriterEpochAuthority(t, config)
	next := config.WriterFence()
	next.WriterEpoch = 2
	next.DeploymentFence = "deployment-2"
	if err := authority.Takeover(t.Context(), next); err != nil {
		authority.Close()
		t.Fatalf("Takeover() error = %v", err)
	}
	if !authority.queryOnly.Load() {
		authority.Close()
		t.Fatal("Takeover() did not fence current instance")
	}
	if _, err := authority.Submit(t.Context(), schemaTestRequest(t)); !errors.Is(err, ErrAuthorityReadOnly) {
		authority.Close()
		t.Fatalf("fenced Submit() error = %v, want ErrAuthorityReadOnly", err)
	}
	if err := authority.Close(); err != nil {
		t.Fatalf("close old authority: %v", err)
	}
	config.WriterEpoch = next.WriterEpoch
	config.DeploymentFence = next.DeploymentFence
	successor, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority(successor) error = %v", err)
	}
	defer closeOutcomeAuthority(t, successor)
	if got := successor.WriterFence(); got != next {
		t.Fatalf("successor writer fence = %+v, want %+v", got, next)
	}
	if _, err := successor.Submit(t.Context(), schemaTestRequest(t)); err != nil {
		t.Fatalf("successor Submit() error = %v", err)
	}
}

// TestTakeoverAuthoritySupportsSuccessorWithoutOldInstance 验证旧进程不在内存
// 时，successor 仍可在 lease 下用 expected token 完成原子接管。
func TestTakeoverAuthoritySupportsSuccessorWithoutOldInstance(t *testing.T) {
	config := authorityTestConfig(t, "takeover-successor")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	next := config.WriterFence()
	next.WriterEpoch = 2
	next.DeploymentFence = "deployment-2"
	if err := TakeoverAuthority(t.Context(), config, acceptingLease{}, next); err != nil {
		t.Fatalf("TakeoverAuthority() error = %v", err)
	}
	config.WriterEpoch = next.WriterEpoch
	config.DeploymentFence = next.DeploymentFence
	successor, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority(successor) error = %v", err)
	}
	defer closeOutcomeAuthority(t, successor)
	staleNext := next
	staleNext.WriterEpoch = 3
	staleNext.DeploymentFence = "deployment-3"
	oldConfig := config
	oldConfig.WriterEpoch = 1
	oldConfig.DeploymentFence = "deployment-1"
	if err := TakeoverAuthority(t.Context(), oldConfig, acceptingLease{}, staleNext); !errors.Is(err, ErrStaleWriter) {
		t.Fatalf("stale TakeoverAuthority() error = %v, want ErrStaleWriter", err)
	}
	if got := successor.WriterFence(); got != next {
		t.Fatalf("successor fence changed after stale takeover = %+v, want %+v", got, next)
	}
}

// TestTakeoverAuthorityLeaseRejectionDoesNotWrite 验证 lease 拒绝在打开或
// CAS 前 fail-fast，旧 writer token 保持不变。
func TestTakeoverAuthorityLeaseRejectionDoesNotWrite(t *testing.T) {
	config := authorityTestConfig(t, "takeover-lease-rejected")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	next := config.WriterFence()
	next.WriterEpoch = 2
	next.DeploymentFence = "deployment-2"
	lease := &localNonAuthoritativeLifecycleLease{rejectOnCall: 1}
	if err := TakeoverAuthority(t.Context(), config, lease, next); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("TakeoverAuthority() error = %v, want ErrLeaseRejected", err)
	}
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		t.Fatalf("open authority DB: %v", err)
	}
	defer db.Close()
	if got, err := readWriterFence(t.Context(), db); err != nil || got != config.WriterFence() {
		t.Fatalf("writer fence after rejected takeover = (%+v, %v), want %+v", got, err, config.WriterFence())
	}
}

// TestConfigRequiresExplicitWriterFence 验证 writer epoch 与 deployment fence
// 缺失、非正或非规范时不会走 Bootstrap/Open 的隐式默认路径。
func TestConfigRequiresExplicitWriterFence(t *testing.T) {
	base := authorityTestConfig(t, "config-fence")
	cases := []Config{
		func() Config { value := base; value.WriterEpoch = 0; return value }(),
		func() Config { value := base; value.DeploymentFence = ""; return value }(),
		func() Config { value := base; value.DeploymentFence = " deployment-1"; return value }(),
		func() Config { value := base; value.DeploymentFence = "deployment-\x00-1"; return value }(),
		func() Config { value := base; value.DeploymentFence = string([]byte{0xff}); return value }(),
	}
	for index, config := range cases {
		if err := config.Validate(); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("case %d Config.Validate() = %v, want ErrInvalidConfig", index, err)
		}
	}
}

// TestOpenRejectsWriterFenceMismatch 验证 Open 不把配置 token 当作数据库默认值。
func TestOpenRejectsWriterFenceMismatch(t *testing.T) {
	config := authorityTestConfig(t, "open-fence-mismatch")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"epoch":      func(value *Config) { value.WriterEpoch++ },
		"deployment": func(value *Config) { value.DeploymentFence = "deployment-2" },
	} {
		t.Run(name, func(t *testing.T) {
			mismatched := config
			mutate(&mismatched)
			authority, err := OpenAuthority(t.Context(), mismatched, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
			if authority != nil || !errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("OpenAuthority(mismatched fence) = (%v, %v), want nil ErrSchemaMismatch", authority, err)
			}
		})
	}
}

// TestBootstrapSeedsDatabaseRevisionAndWriterFence 验证 revision 3 与 mode/fence
// row 在同一新 authority schema 中持久化，wire 请求版本仍独立为 1。
func TestBootstrapSeedsDatabaseRevisionAndWriterFence(t *testing.T) {
	config := authorityTestConfig(t, "bootstrap-fence")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		t.Fatalf("open authority DB: %v", err)
	}
	defer db.Close()
	var revision int64
	if err := db.QueryRowContext(t.Context(), `SELECT schema_version FROM authority_schema WHERE singleton = 1`).Scan(&revision); err != nil {
		t.Fatalf("read database schema revision: %v", err)
	}
	if revision != AuthorityDatabaseSchemaRevision {
		t.Fatalf("database schema revision = %d, want %d", revision, AuthorityDatabaseSchemaRevision)
	}
	fence, err := readWriterFence(t.Context(), db)
	if err != nil {
		t.Fatalf("read writer fence: %v", err)
	}
	if fence != config.WriterFence() {
		t.Fatalf("writer fence = %+v, want %+v", fence, config.WriterFence())
	}
}

// TestOpenStoresExactWriterFence 验证 Open 返回的 authority 保留经过校验的 token。
func TestOpenStoresExactWriterFence(t *testing.T) {
	config := authorityTestConfig(t, "open-fence-store")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	defer closeOutcomeAuthority(t, authority)
	if got := authority.WriterFence(); got != config.WriterFence() {
		t.Fatalf("stored writer fence = %+v, want %+v", got, config.WriterFence())
	}
}

// TestOpenRejectsRevisionOneWithoutImplicitMigration 验证旧 authority DB 不会
// 被自动补 writer fence/mode 或迁移到 revision 3。
func TestOpenRejectsRevisionOneWithoutImplicitMigration(t *testing.T) {
	config := authorityTestConfig(t, "revision-one")
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: config.Path})
	if err != nil {
		t.Fatalf("open legacy fixture DB: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE authority_schema (
singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
schema_version INTEGER NOT NULL CHECK (schema_version = 1),
schema_digest TEXT NOT NULL, created_at TEXT NOT NULL
) STRICT`); err != nil {
		db.Close()
		t.Fatalf("create revision-one schema: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO authority_schema(singleton, schema_version, schema_digest, created_at) VALUES(1, ?, ?, ?)`, AuthoritySchemaVersion, schemaTestDigest("a"), "2026-09-02T00:00:00Z"); err != nil {
		db.Close()
		t.Fatalf("seed revision-one schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close revision-one DB: %v", err)
	}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if authority != nil || !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("OpenAuthority(revision one) = (%v, %v), want nil ErrSchemaMismatch", authority, err)
	}
}

// TestOpenAndBootstrapRejectRevisionTwoWithoutImplicitMigration 验证旧 revision-2
// authority 文件不会被 Open 或 Bootstrap 隐式迁移到 revision 3。该夹具只保留旧
// authority_schema/authority_meta 元数据，且使用独立服务的私有 SQLite 边界。
func TestOpenAndBootstrapRejectRevisionTwoWithoutImplicitMigration(t *testing.T) {
	config := authorityTestConfig(t, "revision-two")
	config.ExternalService = true
	config.ProductSQLitePath = ""
	config.WorkspaceRoot = ""
	installRevisionTwoAuthorityFixture(t, config.Path)

	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if authority != nil || !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("OpenAuthority(revision two) = (%v, %v), want nil ErrSchemaMismatch", authority, err)
	}
	assertRevisionTwoAuthorityFixtureIntact(t, config.Path)

	if err := Bootstrap(t.Context(), config, acceptingLease{}); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("Bootstrap(revision two) error = %v, want ErrSchemaMismatch", err)
	}
	assertRevisionTwoAuthorityFixtureIntact(t, config.Path)
}

// installRevisionTwoAuthorityFixture 构造等价旧 v2 文件；不调用当前 schema DDL，
// 从而避免测试本身借助生产 schema 或迁移路径生成 fixture。
func installRevisionTwoAuthorityFixture(t *testing.T, path string) {
	t.Helper()
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("open revision-two fixture DB: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close revision-two fixture DB: %v", err)
		}
	}()
	for _, statement := range []string{
		`CREATE TABLE authority_schema (
singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
schema_version INTEGER NOT NULL,
schema_digest TEXT NOT NULL,
created_at TEXT NOT NULL
) STRICT`,
		`CREATE TABLE authority_meta (
singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
store_id TEXT NOT NULL,
revision_marker TEXT NOT NULL
) STRICT`,
		`INSERT INTO authority_schema(singleton, schema_version, schema_digest, created_at) VALUES(1, 2, 'legacy-v2-digest', '2026-09-02T00:00:00Z')`,
		`INSERT INTO authority_meta(singleton, store_id, revision_marker) VALUES(1, 'store-1', 'legacy-v2-meta')`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("install revision-two fixture: %v", err)
		}
	}
}

// assertRevisionTwoAuthorityFixtureIntact 只读确认拒绝路径没有重写或补齐旧文件。
func assertRevisionTwoAuthorityFixtureIntact(t *testing.T, path string) {
	t.Helper()
	db, err := platformsqlite.Open(t.Context(), platformsqlite.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("reopen revision-two fixture DB: %v", err)
	}
	defer db.Close()
	var revision int64
	if err := db.QueryRowContext(t.Context(), `SELECT schema_version FROM authority_schema WHERE singleton = 1`).Scan(&revision); err != nil {
		t.Fatalf("read revision-two schema version: %v", err)
	}
	if revision != 2 {
		t.Fatalf("revision-two schema version = %d, want 2", revision)
	}
	var marker string
	if err := db.QueryRowContext(t.Context(), `SELECT revision_marker FROM authority_meta WHERE singleton = 1`).Scan(&marker); err != nil {
		t.Fatalf("read revision-two metadata marker: %v", err)
	}
	if marker != "legacy-v2-meta" {
		t.Fatalf("revision-two metadata marker = %q, want unchanged legacy-v2-meta", marker)
	}
	var writerFenceTables int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'authority_writer_fence'`).Scan(&writerFenceTables); err != nil {
		t.Fatalf("check revision-two writer fence table: %v", err)
	}
	if writerFenceTables != 0 {
		t.Fatalf("revision-two fixture gained authority_writer_fence table (%d rows)", writerFenceTables)
	}
}

// openWriterEpochAuthority 创建已显式 bootstrap/open 的本地测试 authority。
func openWriterEpochAuthority(t *testing.T, config Config) *Authority {
	t.Helper()
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	return authority
}
