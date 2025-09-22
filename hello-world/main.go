package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
	metricsEnabled bool
	mtr            *appMetrics
)

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

	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		if err := runMigrations(dbURL); err != nil {
			log.Fatalf("migrations failed: %v", err)
		}
	} else {
		log.Printf("DATABASE_URL not set, skipping migrations")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer shutdownTracerProvider(context.Background())
	if tracingDefault {
		ensureTracerProvider(ctx)
	}

	// Always register metrics collectors; recording/serving is gated dynamically
	mtr = enableMetrics()

	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)

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

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownTracerProvider(context.Background())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown error: %v", err)
		}
	}()

	log.Printf("Starting hello-world on %s (feature flags via OpenFeature/flagd; admin=%v)", addr, adminFlagsEnabled)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server failed: %v", err)
	}
}

func runMigrations(databaseURL string) error {
	var db *sql.DB
	var err error
	deadline := time.Now().Add(45 * time.Second)
	for {
		db, err = sql.Open("postgres", databaseURL)
		if err == nil {
			if pingErr := db.Ping(); pingErr == nil {
				break
			}
			db.Close()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database not reachable within deadline: %w", err)
		}
		time.Sleep(2 * time.Second)
	}
	defer db.Close()

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
