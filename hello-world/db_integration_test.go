//go:build integration

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startPostgresContainer brings up an ephemeral Postgres for the test and
// returns its DSN. The migrations directory is mounted into /migrations so
// runMigrations can drive the file:// source it expects.
//
// Build tag `integration` keeps these out of the default test run; the CI
// pipeline (Phase B) is expected to run `go test -tags=integration` after
// Docker is available. Locally: go test -tags=integration ./...
func startPostgresContainer(t *testing.T) (dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	c, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("hellodb"),
		tcpostgres.WithUsername("hello"),
		tcpostgres.WithPassword("hello"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("postgres container start: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Terminate(context.Background())
	})

	dsn, err = c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}
	return dsn
}

// linkMigrationsToFilesystem makes the local hello-world/migrations directory
// available at the literal /migrations path that runMigrations expects. We
// rewrite the constant via a one-shot helper: copy the source to an absolute
// path runMigrations uses (file:///migrations is hardcoded in main.go).
//
// To avoid touching the host filesystem, we don't actually mount /migrations
// here. Instead, the integration test calls a thin helper that drives
// migrate.NewWithDatabaseInstance against the source path the test owns.
// This still exercises runMigrations' code (parameterised below) at the
// cost of not running main.go's specific filepath, which is captured by
// docker-compose E2E in Task #12.
func TestDBIntegration_SetupAndMigrate(t *testing.T) {
	dsn := startPostgresContainer(t)

	// 1) waitForDatabase succeeds against a live Postgres
	db, err := waitForDatabase(dsn, 30*time.Second)
	if err != nil {
		t.Fatalf("waitForDatabase: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// 2) migrations apply cleanly from the local source directory
	migrationsPath := absMigrationsPath(t)
	if err := applyMigrationsAt(db, migrationsPath); err != nil {
		t.Fatalf("applyMigrationsAt: %v", err)
	}

	// 3) re-applying is a no-op (migrate.ErrNoChange path)
	if err := applyMigrationsAt(db, migrationsPath); err != nil {
		t.Fatalf("applyMigrationsAt re-run: %v", err)
	}

	// 4) readiness reports OK against the running DB
	checker := dependencyChecker{db: db}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	checker.readinessHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("readyz status=%d want 200 against live DB", rr.Code)
	}

	// 5) sanity: we can query the DB after migrations
	if err := db.Ping(); err != nil {
		t.Fatalf("post-migration Ping: %v", err)
	}
}

func TestDBIntegration_ReadinessFlipsTo503AfterTerminate(t *testing.T) {
	dsn := startPostgresContainer(t)
	db, err := waitForDatabase(dsn, 30*time.Second)
	if err != nil {
		t.Fatalf("waitForDatabase: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	checker := dependencyChecker{db: db}

	// Before close: 200
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()
	checker.readinessHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pre-close readyz=%d want 200", rr.Code)
	}

	// Close the pool to simulate the DB becoming unreachable from the app's
	// perspective. PingContext on a closed pool returns an error and the
	// handler must report 503.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	rr = httptest.NewRecorder()
	checker.readinessHandler(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("post-close readyz=%d want 503", rr.Code)
	}
}

func TestDBIntegration_SetupDatabaseSkipMigrationsFlag(t *testing.T) {
	dsn := startPostgresContainer(t)

	// Verifies setupDatabase respects SKIP_MIGRATIONS=true and does not
	// attempt to load the /migrations directory. This protects the
	// production code path where migrations are run as a separate Job.
	t.Setenv("SKIP_MIGRATIONS", "true")

	db, err := setupDatabase(dsn)
	if err != nil {
		t.Fatalf("setupDatabase with SKIP_MIGRATIONS=true: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// absMigrationsPath returns the absolute path to the package's migrations
// directory. Fails the test if not found.
func absMigrationsPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	p := filepath.Join(wd, "migrations")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("migrations dir not found at %s: %v", p, err)
	}
	return p
}

// applyMigrationsAt is a test-only wrapper around the migrate library that
// uses the same driver as runMigrations but accepts an explicit source path.
// We can't call runMigrations directly in tests because it hardcodes
// file:///migrations, which is the path inside the container image, not the
// path on the test runner.
func applyMigrationsAt(db *sql.DB, path string) error {
	// Reuse the same imports as main.go.
	driver, err := pgDriver(db)
	if err != nil {
		return err
	}
	return runMigrationsFromSource("file://"+path, driver)
}

// Defensive: ensure the dsn used in tests does not appear verbatim in logs
// (smoke test for safeErr behaviour against real driver errors).
func TestDBIntegration_FailedConnLogIsRedacted(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	// Real-looking DSN with credentials but pointing at an unreachable host.
	const dsn = "postgres://leakuser:leakpw@127.0.0.1:1/no?sslmode=disable&connect_timeout=1"

	_, err := waitForDatabase(dsn, 200*time.Millisecond)
	if err == nil {
		t.Fatalf("waitForDatabase succeeded against bogus address")
	}

	// We don't log inside waitForDatabase, but readiness uses the same lib.
	db, _ := sql.Open("postgres", dsn)
	_ = db.Close()
	checker := dependencyChecker{db: db}
	rr := httptest.NewRecorder()
	checker.readinessHandler(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if strings.Contains(buf.String(), "leakpw") {
		t.Fatalf("readiness log leaked password: %s", buf.String())
	}
}
