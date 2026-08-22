package remotecicli

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestPostgreSQLAuthorityOpener 聚合 strict config、错误净化和 database/sql opener 证据。
func TestPostgreSQLAuthorityOpener(t *testing.T) {
	t.Run("strict_config_and_field_guard", testPostgreSQLAuthorityStrictConfigAndFieldGuard)
	t.Run("rejects_missing_or_shared_environment", testPostgreSQLAuthorityRejectsMissingOrSharedEnvironment)
	t.Run("opens_pings_and_closes_both_roles", testPostgreSQLAuthorityOpensPingsAndClosesBothRoles)
	t.Run("sanitizes_open_ping_close_failures", testPostgreSQLAuthoritySanitizesFailures)
	t.Run("closes_handle_returned_with_open_error", testPostgreSQLAuthorityClosesHandleReturnedWithOpenError)
	t.Run("real_postgresql_opt_in", testPostgreSQLAuthorityRealOptIn)
}

func testPostgreSQLAuthorityStrictConfigAndFieldGuard(t *testing.T) {
	config := validPostgreSQLAuthorityConfig()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePostgreSQLAuthorityConfig(data)
	if err != nil || decoded != config {
		t.Fatalf("DecodePostgreSQLAuthorityConfig() = %#v, %v", decoded, err)
	}
	for name, payload := range map[string][]byte{
		"unknown":  append(data[:len(data)-1], []byte(`,"dsn":"secret"}`)...),
		"trailing": append(append([]byte(nil), data...), []byte("\n{}")...),
		"null":     []byte("null"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePostgreSQLAuthorityConfig(payload); err == nil {
				t.Fatal("strict decoder accepted invalid config")
			}
		})
	}
	producer := postgreSQLAuthorityJSONFields(reflect.TypeFor[PostgreSQLAuthorityConfig]())
	consumer := PostgreSQLAuthorityConfigConsumerFields()
	if err := ValidatePostgreSQLAuthorityConfigConsumerFields(consumer); err != nil || len(producer) != len(consumer) {
		t.Fatalf("field guard = %v producer=%v consumer=%v", err, producer, consumer)
	}
	if err := ValidatePostgreSQLAuthorityConfigConsumerFields(consumer[:len(consumer)-1]); err == nil {
		t.Fatal("field guard accepted missing consumer")
	}
}

func testPostgreSQLAuthorityRejectsMissingOrSharedEnvironment(t *testing.T) {
	shared := validPostgreSQLAuthorityConfig()
	shared.ReceiptDSNEnv = shared.OwnerDSNEnv
	if err := shared.Validate(); err == nil {
		t.Fatal("Validate() accepted shared owner/receipt env")
	}
	config := validPostgreSQLAuthorityConfig()
	dependencies := PostgreSQLAuthorityDependencies{LookupEnv: func(string) (string, bool) { return "", false }, Open: unexpectedPostgreSQLOpen(t)}
	if _, err := OpenPostgreSQLAuthorities(context.Background(), config, dependencies); err == nil {
		t.Fatal("OpenPostgreSQLAuthorities() accepted missing environment")
	} else {
		assertPostgreSQLAuthorityErrorSafe(t, err, "missing-secret")
	}
}

func testPostgreSQLAuthorityOpensPingsAndClosesBothRoles(t *testing.T) {
	ownerState := &fakePostgreSQLState{}
	receiptState := &fakePostgreSQLState{}
	owner := newFakeSQLDB(ownerState)
	receipt := newFakeSQLDB(receiptState)
	var calls []string
	dependencies := PostgreSQLAuthorityDependencies{
		LookupEnv: func(name string) (string, bool) {
			return map[string]string{"OWNER_DSN": "owner-secret", "RECEIPT_DSN": "receipt-secret"}[name], true
		},
		Open: func(driver, dsn string) (*sql.DB, error) {
			calls = append(calls, driver+":"+dsn)
			if dsn == "owner-secret" {
				return owner, nil
			}
			return receipt, nil
		},
	}
	authorities, err := OpenPostgreSQLAuthorities(context.Background(), validPostgreSQLAuthorityConfig(), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"pgx:owner-secret", "pgx:receipt-secret"}) || ownerState.pings != 1 || receiptState.pings != 1 {
		t.Fatalf("open/ping calls = %v owner=%d receipt=%d", calls, ownerState.pings, receiptState.pings)
	}
	if err := authorities.Close(); err != nil || ownerState.closes != 1 || receiptState.closes != 1 {
		t.Fatalf("Close() = %v owner=%d receipt=%d", err, ownerState.closes, receiptState.closes)
	}
}

func testPostgreSQLAuthoritySanitizesFailures(t *testing.T) {
	secret := "postgres://user:password@10.0.0.9/database"
	raw := errors.New("driver failed for " + secret)
	cases := map[string]PostgreSQLAuthorityDependencies{
		"open": {LookupEnv: secretLookup(secret), Open: func(string, string) (*sql.DB, error) { return nil, raw }},
		"ping": {LookupEnv: secretLookup(secret), Open: func(string, string) (*sql.DB, error) {
			return newFakeSQLDB(&fakePostgreSQLState{pingErr: raw}), nil
		}},
		"ping_close": {LookupEnv: secretLookup(secret), Open: func(string, string) (*sql.DB, error) {
			return newFakeSQLDB(&fakePostgreSQLState{pingErr: raw, closeErr: raw}), nil
		}},
		"close": {LookupEnv: secretLookup(secret), Open: func(string, string) (*sql.DB, error) {
			return newFakeSQLDB(&fakePostgreSQLState{closeErr: raw}), nil
		}},
		"receipt_open_owner_close": receiptOpenOwnerCloseDependencies(secret, raw),
	}
	for name, dependencies := range cases {
		t.Run(name, func(t *testing.T) {
			authorities, err := OpenPostgreSQLAuthorities(context.Background(), validPostgreSQLAuthorityConfig(), dependencies)
			if err == nil {
				err = authorities.Close()
			}
			if err == nil {
				t.Fatal("failure injection returned nil")
			}
			assertPostgreSQLAuthorityErrorSafe(t, err, secret)
			assertPostgreSQLAuthorityErrorSafe(t, err, raw.Error())
			if name == "close" || name == "ping_close" || name == "receipt_open_owner_close" {
				assertPostgreSQLAuthorityJoinedCloseFailures(t, err)
			}
		})
	}
}

func testPostgreSQLAuthorityClosesHandleReturnedWithOpenError(t *testing.T) {
	raw := errors.New("raw open failure with secret")
	for name, closeErr := range map[string]error{"close_success": nil, "close_failure": raw} {
		t.Run(name, func(t *testing.T) {
			state := &fakePostgreSQLState{closeErr: closeErr}
			dependencies := PostgreSQLAuthorityDependencies{LookupEnv: secretLookup("secret"), Open: func(string, string) (*sql.DB, error) {
				database := newFakeSQLDB(state)
				if err := database.PingContext(context.Background()); err != nil {
					t.Fatalf("prime fake database handle: %v", err)
				}
				return database, raw
			}}
			if _, err := OpenPostgreSQLAuthorities(context.Background(), validPostgreSQLAuthorityConfig(), dependencies); err == nil {
				t.Fatal("non-nil handle plus open error was accepted")
			} else {
				assertPostgreSQLAuthorityErrorSafe(t, err, raw.Error())
				assertPostgreSQLAuthorityOpenCleanupCauses(t, err, closeErr != nil)
			}
			if state.closes != 1 {
				t.Fatalf("handle closes = %d, want 1", state.closes)
			}
		})
	}
}

func testPostgreSQLAuthorityRealOptIn(t *testing.T) {
	ownerDSN, ownerOK := os.LookupEnv("SUPER_DOLPHIN_TEST_PG_OWNER_DSN")
	receiptDSN, receiptOK := os.LookupEnv("SUPER_DOLPHIN_TEST_PG_RECEIPT_DSN")
	if !ownerOK || !receiptOK || strings.TrimSpace(ownerDSN) == "" || strings.TrimSpace(receiptDSN) == "" {
		t.Skip("real PostgreSQL test requires explicit owner and receipt DSN environment")
	}
	config := PostgreSQLAuthorityConfig{SchemaVersion: PostgreSQLAuthorityConfigSchema, OwnerDSNEnv: "SUPER_DOLPHIN_TEST_PG_OWNER_DSN", ReceiptDSNEnv: "SUPER_DOLPHIN_TEST_PG_RECEIPT_DSN"}
	authorities, err := OpenPostgreSQLAuthorities(context.Background(), config, DefaultPostgreSQLAuthorityDependencies())
	if err != nil {
		t.Fatal(err)
	}
	if err := authorities.Close(); err != nil {
		t.Fatal(err)
	}
}

func validPostgreSQLAuthorityConfig() PostgreSQLAuthorityConfig {
	return PostgreSQLAuthorityConfig{SchemaVersion: PostgreSQLAuthorityConfigSchema, OwnerDSNEnv: "OWNER_DSN", ReceiptDSNEnv: "RECEIPT_DSN"}
}

func secretLookup(secret string) func(string) (string, bool) {
	return func(string) (string, bool) { return secret, true }
}

func receiptOpenOwnerCloseDependencies(secret string, raw error) PostgreSQLAuthorityDependencies {
	openCount := 0
	return PostgreSQLAuthorityDependencies{LookupEnv: secretLookup(secret), Open: func(string, string) (*sql.DB, error) {
		openCount++
		if openCount == 1 {
			return newFakeSQLDB(&fakePostgreSQLState{closeErr: raw}), nil
		}
		return nil, raw
	}}
}

func unexpectedPostgreSQLOpen(t *testing.T) PostgreSQLDatabaseOpener {
	t.Helper()
	return func(string, string) (*sql.DB, error) {
		t.Fatal("database opener called after invalid environment")
		return nil, nil
	}
}

func assertPostgreSQLAuthorityErrorSafe(t *testing.T, err error, forbidden string) {
	t.Helper()
	var typed PostgreSQLAuthorityError
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want PostgreSQLAuthorityError", err)
	}
	if strings.Contains(err.Error(), forbidden) {
		t.Fatalf("error leaked forbidden material: %q", err.Error())
	}
}

func assertPostgreSQLAuthorityJoinedCloseFailures(t *testing.T, err error) {
	t.Helper()
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("close error = %#v, want errors.Join with two failures", err)
	}
}

func assertPostgreSQLAuthorityOpenCleanupCauses(t *testing.T, err error, closeFailed bool) {
	t.Helper()
	joined, ok := err.(interface{ Unwrap() []error })
	want := 1
	if closeFailed {
		want = 2
	}
	if !ok || len(joined.Unwrap()) != want {
		t.Fatalf("open cleanup error = %#v, want %d safe causes", err, want)
	}
}

func postgreSQLAuthorityJSONFields(value reflect.Type) []string {
	fields := make([]string, 0, value.NumField())
	for field := range value.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		fields = append(fields, name)
	}
	return fields
}

type fakePostgreSQLState struct {
	pingErr, closeErr error
	pings, closes     int
}

func newFakeSQLDB(state *fakePostgreSQLState) *sql.DB {
	return sql.OpenDB(fakePostgreSQLConnector{state: state})
}

type fakePostgreSQLConnector struct{ state *fakePostgreSQLState }

func (connector fakePostgreSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakePostgreSQLConn{state: connector.state}, nil
}
func (connector fakePostgreSQLConnector) Driver() driver.Driver {
	return fakePostgreSQLDriver{state: connector.state}
}

type fakePostgreSQLDriver struct{ state *fakePostgreSQLState }

func (database fakePostgreSQLDriver) Open(string) (driver.Conn, error) {
	return &fakePostgreSQLConn{state: database.state}, nil
}

type fakePostgreSQLConn struct{ state *fakePostgreSQLState }

func (connection *fakePostgreSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (connection *fakePostgreSQLConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not supported")
}
func (connection *fakePostgreSQLConn) Ping(context.Context) error {
	connection.state.pings++
	return connection.state.pingErr
}
func (connection *fakePostgreSQLConn) Close() error {
	connection.state.closes++
	return connection.state.closeErr
}
