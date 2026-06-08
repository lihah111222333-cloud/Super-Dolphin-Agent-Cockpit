package embeddedpg

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestPackagedPostgresSmoke(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_PACKAGED_POSTGRES_ROOT"))
	if root == "" {
		t.Skip("set SUPER_DOLPHIN_PACKAGED_POSTGRES_ROOT to a packaged postgres/<goos-goarch> runtime root")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve SUPER_DOLPHIN_PACKAGED_POSTGRES_ROOT: %v", err)
	}
	binDir := filepath.Join(root, "bin")
	shareDir := packagedSmokeShareDir(t, root)
	temp := shortPackagedSmokeTempDir(t)
	cfg := contract.EmbeddedPostgresConfig{
		Enabled:      true,
		Owner:        true,
		BinDir:       binDir,
		ShareDir:     shareDir,
		DataDir:      filepath.Join(temp, "data"),
		RuntimeDir:   filepath.Join(temp, "runtime"),
		LogPath:      filepath.Join(temp, "postgres.log"),
		DatabaseName: "super_dolphin",
		UserName:     "super_dolphin",
		Port:         55432,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := Start(ctx, cfg); err != nil {
		t.Fatalf("Start() first launch error = %v", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer stopCancel()
			if err := Stop(stopCtx, cfg); err != nil {
				t.Fatalf("Stop() cleanup error = %v", err)
			}
		}
	}()

	assertPackagedSmokeQuery(t, cfg)
	assertPackagedSmokeDirMode(t, cfg.DataDir, 0o700)
	assertPackagedSmokeDirMode(t, cfg.RuntimeDir, 0o700)

	if err := Start(ctx, cfg); err != nil {
		t.Fatalf("Start() repeat launch error = %v", err)
	}
	assertPackagedSmokeQuery(t, cfg)

	if err := Stop(ctx, cfg); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stopped = true
	running, err := postgresRunning(context.Background(), cfg, filepath.Join(cfg.BinDir, executableName("pg_ctl")))
	if err != nil {
		t.Fatalf("pg_ctl status after Stop() error = %v", err)
	}
	if running {
		t.Fatal("postgres still reports running after Stop()")
	}
}

func packagedSmokeShareDir(t *testing.T, root string) string {
	t.Helper()
	for _, candidate := range postgresShareDirCandidates(root) {
		if info, err := os.Stat(filepath.Join(candidate, "postgres.bki")); err == nil && !info.IsDir() {
			return candidate
		}
	}
	t.Fatalf("missing postgres.bki under packaged postgres root %s", root)
	return ""
}

func shortPackagedSmokeTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sd-pg-smoke-")
	if err != nil {
		t.Fatalf("create short packaged postgres smoke dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("remove packaged postgres smoke dir %s: %v", dir, err)
		}
	})
	return dir
}

func assertPackagedSmokeQuery(t *testing.T, cfg contract.EmbeddedPostgresConfig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, packagedSmokePostgresDSN(cfg))
	if err != nil {
		t.Fatalf("connect to packaged postgres: %v", err)
	}
	defer conn.Close(context.Background())

	var got int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&got); err != nil {
		t.Fatalf("query packaged postgres: %v", err)
	}
	if got != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", got)
	}
}

func packagedSmokePostgresDSN(cfg contract.EmbeddedPostgresConfig) string {
	values := url.Values{}
	values.Set("sslmode", "disable")
	host := "localhost:" + strconv.Itoa(cfg.Port)
	if runtime.GOOS == "windows" {
		host = "127.0.0.1:" + strconv.Itoa(cfg.Port)
	} else {
		values.Set("host", cfg.RuntimeDir)
	}
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.User(cfg.UserName),
		Host:     host,
		Path:     "/postgres",
		RawQuery: values.Encode(),
	}).String()
}

func assertPackagedSmokeDirMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %s, want %s", path, got, want)
	}
}

func Example_packagedPostgresSmokeCommand() {
	fmt.Println(`SUPER_DOLPHIN_PACKAGED_POSTGRES_ROOT="$PWD/dist/package/macos/Super Dolphin.app/Contents/Resources/postgres/$(go env GOOS)-$(go env GOARCH)" go test ./internal/platform/embeddedpg -run '^TestPackagedPostgresSmoke$' -count=1 -v`)
	// Output:
	// SUPER_DOLPHIN_PACKAGED_POSTGRES_ROOT="$PWD/dist/package/macos/Super Dolphin.app/Contents/Resources/postgres/$(go env GOOS)-$(go env GOARCH)" go test ./internal/platform/embeddedpg -run '^TestPackagedPostgresSmoke$' -count=1 -v
}
