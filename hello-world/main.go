package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

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
		http.Error(w, fmt.Sprintf("not ready: %v", err), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (c dependencyChecker) livenessHandler(w http.ResponseWriter, r *http.Request) {
	if err := c.pingDatabase(r.Context()); err != nil {
		http.Error(w, fmt.Sprintf("not live: %v", err), http.StatusInternalServerError)
		return
	}
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

func logWithTraceID(ctx context.Context, msg string) {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		log.Printf("trace_id=%s %s", sc.TraceID().String(), msg)
		return
	}
	log.Printf(msg)
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Dynamic tracing flag (OpenFeature override-able)
	if isTracingEnabled(ctx) {
		var span trace.Span
		ctx, span = otel.Tracer("hello-world").Start(ctx, "helloHandler")
		defer span.End()
	}

	start := time.Now()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("hello world"))
	dur := time.Since(start).Seconds()
	if isMetricsEnabled(ctx) && mtr != nil {
		mtr.reqCount.WithLabelValues("/", r.Method, "200").Inc()
		mtr.reqDuration.WithLabelValues("/", r.Method).Observe(dur)
	}
	logWithTraceID(ctx, fmt.Sprintf("Handled / request from %s in %.4fs", r.RemoteAddr, dur))
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
			attribute.String("service.version", "1.0.0"),
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

func main() {
	// Feature flags defaults via env vars
	metricsDefault := getBoolEnv("ENABLE_METRICS", false)
	tracingDefault := getBoolEnv("ENABLE_TRACING", false)
	adminFlagsEnabled := getBoolEnv("ADMIN_FLAGS_ENABLED", false)

	// Initialize OpenFeature (flagd) client for dynamic flags
	initFeatureFlags(tracingDefault, metricsDefault)

	var (
		db    *sql.DB
		err   error
		dbURL = os.Getenv("DATABASE_URL")
	)
	if dbURL != "" {
		db, err = setupDatabase(dbURL)
		if err != nil {
			log.Fatalf("database initialization failed: %v", err)
		}
		defer func() {
			if cerr := db.Close(); cerr != nil {
				log.Printf("database close error: %v", cerr)
			}
		}()
	} else {
		log.Printf("DATABASE_URL not set, skipping migrations")
	}

	// Tracer provider is created lazily on first enable; initialize now if desired
	ctx := context.Background()
	if tracingDefault {
		if shutdown, err := initTracer(ctx); err != nil {
			log.Printf("tracing init failed, continuing without tracing: %v", err)
		} else {
			defer func() {
				if err := shutdown(context.Background()); err != nil {
					log.Printf("tracer shutdown error: %v", err)
				}
			}()
		}
	}

	// Always register metrics collectors; recording/serving is gated dynamically
	mtr = enableMetrics()

	checker := dependencyChecker{db: db}

	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)
	mux.HandleFunc("/readyz", checker.readinessHandler)
	mux.HandleFunc("/livez", checker.livenessHandler)

	// Metrics endpoint gated dynamically per-request
	promHandler := promhttp.Handler()
	mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMetricsEnabled(r.Context()) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("metrics disabled"))
			return
		}
		promHandler.ServeHTTP(w, r)
	}))

	// Admin flags (local/dev): GET returns current; POST sets; POST /reset clears overrides
	if adminFlagsEnabled {
		mux.HandleFunc("/admin/flags", adminFlagsHandler)
		mux.HandleFunc("/admin/flags/reset", adminFlagsResetHandler)
		log.Printf("Admin flags endpoint enabled (no auth): /admin/flags")
	}

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	log.Printf("Starting hello-world on %s (feature flags via OpenFeature/flagd; admin=%v)", addr, adminFlagsEnabled)

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("server failed: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("Received signal %s, initiating graceful shutdown", sig)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
		cancel()
		<-serverErr
	}
}

func setupDatabase(databaseURL string) (*sql.DB, error) {
	db, err := waitForDatabase(databaseURL, 45*time.Second)
	if err != nil {
		return nil, err
	}
	if err := runMigrations(db); err != nil {
		db.Close()
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
		db.Close()
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("database not reachable within deadline: %w", pingErr)
		}
		time.Sleep(2 * time.Second)
	}
}

func runMigrations(db *sql.DB) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("create driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance("file:///migrations", "postgres", driver)
	if err != nil {
		return fmt.Errorf("new migrate: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	if err == migrate.ErrNoChange {
		log.Printf("migrations: no change")
	} else {
		log.Printf("migrations: applied successfully")
	}
	return nil
}
