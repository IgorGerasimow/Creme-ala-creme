package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	flagd "github.com/open-feature/flagd-go-sdk/pkg/provider"
	"github.com/open-feature/go-sdk/openfeature"
)

// Dynamic feature flags manager with OpenFeature (flagd) + optional admin overrides.

type flagOverrides struct {
	// nil means no override; non-nil value is authoritative
	Tracing *bool `json:"tracing,omitempty"`
	Metrics *bool `json:"metrics,omitempty"`
}

var (
	ofClient              openfeature.Client
	defaultTracing        atomic.Bool
	defaultMetrics        atomic.Bool
	overridesValue        atomic.Value // stores flagOverrides
	tracerProviderFactory = initTracer

	tracerInitMu      sync.Mutex
	tracerInitialized atomic.Bool
	tracerShutdownFn  func(context.Context) error
)

func initFeatureFlags(tracingDefault, metricsDefault bool) {
	// Set defaults
	defaultTracing.Store(tracingDefault)
	defaultMetrics.Store(metricsDefault)
	overridesValue.Store(flagOverrides{})

	// Initialize flagd provider if available, else noop
	host := getenvDefault("FLAGD_HOST", "flagd")
	port := getenvDefault("FLAGD_PORT", "8013")

	provider := flagd.NewProvider(
		flagd.WithHost(host),
		flagd.WithPort(port),
		flagd.WithMaxEventStreamRetries(3),
		flagd.WithMaxProviderReadyWait(time.Second*3),
	)
	openfeature.SetProvider(provider)
	ofClient = openfeature.NewClient("hello-world")
}

func getenvDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func isTracingEnabled(ctx context.Context) bool {
	ov := overridesValue.Load().(flagOverrides)
	if ov.Tracing != nil {
		if *ov.Tracing {
			ensureTracerProvider(ctx)
		}
		return *ov.Tracing
	}
	// Evaluate via OpenFeature with default
	def := defaultTracing.Load()
	val, err := ofClient.BooleanValue(ctx, "tracing_enabled", def, openfeature.EvaluationContext{})
	if err != nil {
		return def
	}
	if val {
		ensureTracerProvider(ctx)
	}
	return val
}

func isMetricsEnabled(ctx context.Context) bool {
	ov := overridesValue.Load().(flagOverrides)
	if ov.Metrics != nil {
		return *ov.Metrics
	}
	def := defaultMetrics.Load()
	val, err := ofClient.BooleanValue(ctx, "metrics_enabled", def, openfeature.EvaluationContext{})
	if err != nil {
		return def
	}
	return val
}

// Admin endpoints (enable with ADMIN_FLAGS_ENABLED=true)
// GET /admin/flags -> current values and overrides
// POST /admin/flags body: {"tracing": true/false, "metrics": true/false}
// POST /admin/flags?tracing=true&metrics=false also supported
// POST /admin/flags/reset -> clears overrides

func adminFlagsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp := map[string]any{
			"defaults": map[string]bool{
				"tracing": defaultTracing.Load(),
				"metrics": defaultMetrics.Load(),
			},
			"overrides": overridesValue.Load().(flagOverrides),
		}
		writeJSON(w, http.StatusOK, resp)
		return
	case http.MethodPost:
		ov := overridesValue.Load().(flagOverrides)
		// support query params
		if q := r.URL.Query().Get("tracing"); q != "" {
			if b, err := strconv.ParseBool(q); err == nil {
				ov.Tracing = &b
			}
		}
		if q := r.URL.Query().Get("metrics"); q != "" {
			if b, err := strconv.ParseBool(q); err == nil {
				ov.Metrics = &b
			}
		}
		// support JSON body
		var body flagOverrides
		if ct := r.Header.Get("Content-Type"); ct == "application/json" || ct == "application/json; charset=utf-8" {
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Tracing != nil {
				ov.Tracing = body.Tracing
			}
			if body.Metrics != nil {
				ov.Metrics = body.Metrics
			}
		}
		overridesValue.Store(ov)
		writeJSON(w, http.StatusOK, map[string]any{"overrides": ov})
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
}

func adminFlagsResetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	overridesValue.Store(flagOverrides{})
	writeJSON(w, http.StatusOK, map[string]any{"overrides": overridesValue.Load()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func ensureTracerProvider(ctx context.Context) {
	if tracerInitialized.Load() {
		return
	}
	tracerInitMu.Lock()
	defer tracerInitMu.Unlock()
	if tracerInitialized.Load() {
		return
	}

	shutdown, err := tracerProviderFactory(ctx)
	if err != nil {
		log.Printf("tracing init failed, continuing without tracing: %v", err)
		return
	}
	tracerShutdownFn = shutdown
	tracerInitialized.Store(true)
}

func shutdownTracerProvider(ctx context.Context) {
	tracerInitMu.Lock()
	shutdown := tracerShutdownFn
	tracerShutdownFn = nil
	tracerInitialized.Store(false)
	tracerInitMu.Unlock()

	if shutdown != nil {
		if err := shutdown(ctx); err != nil {
			log.Printf("tracer shutdown error: %v", err)
		}
	}
}
