package agent

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
)

// TestStandaloneSessionBindingIdentityContract 锁定真实SQLite不可变绑定，恢复不能伪装成重复创建或SQL重绑。
func TestStandaloneSessionBindingIdentityContract(t *testing.T) {
	t.Run("duplicate-create", testSessionIdentityDuplicateCreation)
	t.Run("immutable-trigger", testSessionIdentityImmutableTrigger)
}

// sessionIdentityFixture 用当前完整迁移和原始SessionStore建立有区别的真实身份。
func sessionIdentityFixture(t *testing.T) (*sql.DB, *SessionStore, standalone.SessionRecord) {
	t.Helper()
	db := sessionTestDatabase(t)
	store := NewSessionStore(db)
	record := standalone.SessionRecord{AgentID: "agent", ThreadID: "thread", Provider: "codex", Name: "worker",
		CWD: t.TempDir(), ParentID: "parent", Model: "model", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if err := store.CreateSession(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	return db, store, record
}

// testSessionIdentityDuplicateCreation 动态枚举SessionRecord字段，任何重复创建均不覆盖已有行或留下孤立thread。
func testSessionIdentityDuplicateCreation(t *testing.T) {
	typ := reflect.TypeFor[standalone.SessionRecord]()
	for i := 0; i < typ.NumField(); i++ {
		t.Run(typ.Field(i).Name, func(t *testing.T) {
			db, store, record := sessionIdentityFixture(t)
			bindingBefore := sessionIdentityRow(t, db, "agent_provider_binding")
			threadBefore := sessionIdentityRow(t, db, "agent_threads")
			changeSessionIdentityField(t, reflect.ValueOf(&record).Elem().Field(i))
			if err := store.CreateSession(context.Background(), record); err == nil {
				t.Fatal("duplicate session identity was overwritten")
			}
			assertSessionIdentityRowsUnchanged(t, db, bindingBefore, threadBefore)
		})
	}
}

// changeSessionIdentityField 对字段施加真实非零变化，未知字段类型立即阻断测试以要求补齐契约。
func changeSessionIdentityField(t *testing.T, field reflect.Value) {
	t.Helper()
	if field.Type() == reflect.TypeFor[time.Time]() {
		field.Set(reflect.ValueOf(field.Interface().(time.Time).Add(time.Second)))
		return
	}
	if field.Kind() != reflect.String {
		t.Fatalf("uncovered session field type: %s", field.Type())
	}
	if field.String() == "codex" {
		field.SetString("claude")
		return
	}
	field.SetString(field.String() + "-other")
}

// testSessionIdentityImmutableTrigger 直接负例证明现有SQL触发器拒绝核心owner与既定native身份重绑。
func testSessionIdentityImmutableTrigger(t *testing.T) {
	for _, column := range []string{"agent_id", "provider", "provider_thread_id", "codex_instance_key", "codex_model_provider", "provider_recovery_home"} {
		t.Run(column, func(t *testing.T) {
			db, _, _ := sessionIdentityFixture(t)
			if _, err := db.Exec("UPDATE agent_provider_binding SET codex_instance_key='instance',codex_model_provider='model-provider',provider_recovery_home='/provider-home'"); err != nil {
				t.Fatal(err)
			}
			bindingBefore := sessionIdentityRow(t, db, "agent_provider_binding")
			threadBefore := sessionIdentityRow(t, db, "agent_threads")
			_, err := db.Exec("UPDATE agent_provider_binding SET " + column + "='foreign' WHERE agent_id='agent'")
			if err == nil || !strings.Contains(err.Error(), "identity is immutable") {
				t.Fatalf("SQL rebind %s error=%v, want immutable identity trigger", column, err)
			}
			assertSessionIdentityRowsUnchanged(t, db, bindingBefore, threadBefore)
		})
	}
}

// sessionIdentityRow 动态读取全部真实列并要求只有一行，新增字段自动进入不变性比较。
func sessionIdentityRow(t *testing.T, db *sql.DB, table string) map[string]any {
	t.Helper()
	rows, err := db.Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) == 0 || !rows.Next() {
		t.Fatalf("%s identity row missing", table)
	}
	result := scanSessionIdentityRow(t, rows, columns)
	if rows.Next() {
		t.Fatalf("%s contains an orphan or duplicate row", table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return result
}

// assertSessionIdentityRowsUnchanged 比较全部绑定和thread字段，不能仅凭行数漏掉覆盖副作用。
func assertSessionIdentityRowsUnchanged(t *testing.T, db *sql.DB, bindingBefore, threadBefore map[string]any) {
	t.Helper()
	bindingAfter := sessionIdentityRow(t, db, "agent_provider_binding")
	threadAfter := sessionIdentityRow(t, db, "agent_threads")
	if !reflect.DeepEqual(bindingBefore, bindingAfter) {
		t.Fatalf("binding changed: before=%v after=%v", bindingBefore, bindingAfter)
	}
	if !reflect.DeepEqual(threadBefore, threadAfter) {
		t.Fatalf("thread changed: before=%v after=%v", threadBefore, threadAfter)
	}
}

// scanSessionIdentityRow 将动态列映射成完整比较值，保持schema新增字段可见。
func scanSessionIdentityRow(t *testing.T, rows *sql.Rows, columns []string) map[string]any {
	t.Helper()
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]any, len(columns))
	for i, column := range columns {
		result[column] = values[i]
	}
	return result
}
