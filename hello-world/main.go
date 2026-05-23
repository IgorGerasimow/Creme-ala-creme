package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rs/zerolog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// version is injected at build time via -ldflags "-X main.version=<version>"
var version = "dev"

type appMetrics struct {
	reqCount    *prometheus.CounterVec
	reqDuration *prometheus.HistogramVec
}

var (
	mtr *appMetrics
)

type dependencyChecker struct {
	db *sql.DB
}

func (c dependencyChecker) pingDatabase(ctx context.Context) error {
	if c.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database ping: %w", err)
	}
	return nil
}

func (c dependencyChecker) readinessHandler(w http.ResponseWriter, r *http.Request) {
	if err := c.pingDatabase(r.Context()); err != nil {
		// Use safeErr to strip any DSN/credential substring the driver
		// might have embedded in the error message.
		loggerFromContext(r.Context()).Warn().
			Str("error", safeErr(err)).
			Msg("readiness check failed")
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

// securityHeaders adds standard HTTP security headers to all responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (c dependencyChecker) livenessHandler(w http.ResponseWriter, r *http.Request) {
	// Liveness probe should only check if the app process is responsive
	// NOT external dependencies. Database issues should affect readiness, not liveness.
	// If we check DB here, database outages will cause Kubernetes to restart healthy pods.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("alive"))
}

func enableMetrics() *appMetrics {
	mc := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Count of HTTP requests processed, labeled by status and method.",
		},
		[]string{"handler", "method", "status"},
	)
	mh := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "Histogram of latencies for HTTP requests.",
		},
		[]string{"handler", "method"},
	)
	prometheus.MustRegister(mc, mh)
	return &appMetrics{reqCount: mc, reqDuration: mh}
}

func getBoolEnv(name string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}

// helloHandler responds with a fixed greeting and records metrics when the
// metrics feature flag is enabled. Access logging is handled centrally by
// accessLogMiddleware, so this function does not log per-request lines.
func helloHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if isTracingEnabled(ctx) {
		var span trace.Span
		ctx, span = otel.Tracer("hello-world").Start(ctx, "helloHandler")
		defer span.End()
	}

	start := time.Now()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("hello world"))

	if isMetricsEnabled(ctx) && mtr != nil {
		dur := time.Since(start).Seconds()
		mtr.reqCount.WithLabelValues("/", r.Method, "200").Inc()
		mtr.reqDuration.WithLabelValues("/", r.Method).Observe(dur)
	}
}

func initTracer(ctx context.Context) (func(context.Context) error, error) {
	// Uses OTEL_EXPORTER_OTLP_ENDPOINT (e.g., http://otel-collector:4318) if set
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create otlp http exporter: %w", err)
	}

	svcName := os.Getenv("OTEL_SERVICE_NAME")
	if svcName == "" {
		svcName = "hello-world"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", svcName),
			attribute.String("service.version", version),
			attribute.String("env", os.Getenv("ENVIRONMENT")),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// buildMux assembles the routing tree. It is pure (no side effects beyond
// global Prometheus metrics collectors registered once by enableMetrics) so
// that tests can exercise the exact production routing.
func buildMux(checker dependencyChecker, adminFlagsEnabled bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)
	mux.HandleFunc("/readyz", checker.readinessHandler)
	mux.HandleFunc("/livez", checker.livenessHandler)

	promHandler := promhttp.Handler()
	mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMetricsEnabled(r.Context()) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("metrics disabled"))
			return
		}
		promHandler.ServeHTTP(w, r)
	}))

	if adminFlagsEnabled {
		mux.HandleFunc("/admin/flags", adminAuthMiddleware(adminFlagsHandler))
		mux.HandleFunc("/admin/flags/reset", adminAuthMiddleware(adminFlagsResetHandler))
		if os.Getenv("ADMIN_API_KEY") != "" {
			logger.Info().Msg("admin flags endpoint enabled with API key authentication")
		} else {
			logger.Warn().Msg("admin flags endpoint enabled WITHOUT authentication (dev only)")
		}
	}
	return mux
}

// buildServer wraps mux in the request middleware chain and applies the
// standard production timeouts. addr should be a value suitable for
// http.Server.Addr (e.g., ":8080").
func buildServer(addr string, mux http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           accessLogMiddleware(securityHeaders(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}
}

// resolveListenAddr returns ":<PORT>" when PORT is set, ":8080" otherwise.
func resolveListenAddr() string {
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return ":8080"
}

// initApp performs the application bootstrap that does not require the
// server to run. It is split from main() so tests can exercise the
// configuration path without binding a TCP port. Returns the assembled
// http.Server and a cleanup callback the caller must defer.
func initApp() (*http.Server, func(), error) {
	initLogger()
	// "version" is already part of every log line via initLogger; emit a
	// dedicated startup event for log-search visibility.
	logger.Info().Msg("starting hello-world application")

	metricsDefault := getBoolEnv("ENABLE_METRICS", false)
	tracingDefault := getBoolEnv("ENABLE_TRACING", false)
	adminFlagsEnabled := getBoolEnv("ADMIN_FLAGS_ENABLED", false)

	initFeatureFlags(tracingDefault, metricsDefault)

	var db *sql.DB
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		var err error
		db, err = setupDatabase(dbURL)
		if err != nil {
			return nil, func() {}, fmt.Errorf("database initialization: %w", err)
		}
	} else {
		logger.Info().Msg("DATABASE_URL not set, skipping database setup")
	}

	if tracingDefault {
		ensureTracerProvider(context.Background())
	}

	mtr = enableMetrics()

	checker := dependencyChecker{db: db}
	srv := buildServer(resolveListenAddr(), buildMux(checker, adminFlagsEnabled))

	cleanup := func() {
		shutdownTracerProvider(context.Background())
		if db != nil {
			if cerr := db.Close(); cerr != nil {
				logger.Error().Str("error", safeErr(cerr)).Msg("database close error")
			}
		}
	}
	return srv, cleanup, nil
}

// runServer blocks until either the server fails or a SIGINT/SIGTERM is
// received. It performs a graceful shutdown with a 10s timeout. Returns any
// non-Recoverable server error.
func runServer(srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	logger.Info().Str("addr", srv.Addr).Msg("server started")

	select {
	case err := <-serverErr:
		return err
	case sig := <-sigCh:
		logger.Info().Str("signal", sig.String()).Msg("received shutdown signal")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error().Str("error", safeErr(err)).Msg("server shutdown error")
		}
		<-serverErr
		return nil
	}
}

func main() {
	srv, cleanup, err := initApp()
	if err != nil {
		// initLogger has already run inside initApp on the happy path; in
		// the error path we still need a usable logger.
		if logger.GetLevel() == zerolog.Disabled {
			initLogger()
		}
		logger.Fatal().Str("error", safeErr(err)).Msg("application init failed")
	}
	defer cleanup()
	if err := runServer(srv); err != nil {
		logger.Fatal().Str("error", safeErr(err)).Msg("server failed")
	}
}

// dbStartupTimeout is the maximum duration setupDatabase will wait for a
// successful Ping before giving up. It is a package-level variable rather
// than a constant so tests can shorten it.
var dbStartupTimeout = 45 * time.Second

func setupDatabase(databaseURL string) (*sql.DB, error) {
	db, err := waitForDatabase(databaseURL, dbStartupTimeout)
	if err != nil {
		return nil, err
	}

	// Skip migrations if SKIP_MIGRATIONS=true (they should be run via Kubernetes Job)
	skipMigrations := getBoolEnv("SKIP_MIGRATIONS", false)
	if skipMigrations {
		logger.Info().Msg("SKIP_MIGRATIONS=true, migrations will not run in application")
		return db, nil
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func waitForDatabase(databaseURL string, timeout time.Duration) (*sql.DB, error) {
	deadline := time.Now().Add(timeout)
	for {
		db, err := sql.Open("postgres", databaseURL)
		if err != nil {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("database open failed within deadline: %w", err)
			}
			time.Sleep(2 * time.Second)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pingErr := db.PingContext(ctx)
		cancel()
		if pingErr == nil {
			return db, nil
		}
		_ = db.Close()
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("database not reachable within deadline: %w", pingErr)
		}
		time.Sleep(2 * time.Second)
	}
}

// runMigrations applies any pending migrations from /migrations on the
// container filesystem. It is a thin convenience wrapper around
// runMigrationsFromSource so tests can drive the same code against a local
// directory.
func runMigrations(db *sql.DB) error {
	driver, err := pgDriver(db)
	if err != nil {
		return err
	}
	return runMigrationsFromSource("file:///migrations", driver)
}

// pgDriver wraps postgres.WithInstance with error wrapping for clearer
// diagnostics.
func pgDriver(db *sql.DB) (database.Driver, error) {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return nil, fmt.Errorf("create driver: %w", err)
	}
	return driver, nil
}

// runMigrationsFromSource applies migrations from the given source URL using
// the supplied driver. Logs progress; returns nil when the source has no
// pending changes.
func runMigrationsFromSource(sourceURL string, driver database.Driver) error {
	m, err := migrate.NewWithDatabaseInstance(sourceURL, "postgres", driver)
	if err != nil {
		return fmt.Errorf("new migrate: %w", err)
	}

	err = m.Up()
	switch {
	case err == nil:
		logger.Info().Msg("migrations: applied successfully")
	case errors.Is(err, migrate.ErrNoChange):
		logger.Info().Msg("migrations: no change")
		return nil
	default:
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
