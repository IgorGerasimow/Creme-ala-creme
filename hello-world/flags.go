package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	flagd "github.com/open-feature/go-sdk-contrib/providers/flagd/pkg"
	"github.com/open-feature/go-sdk/openfeature"
)

// Dynamic feature flags manager with OpenFeature (flagd) + optional admin overrides.

type flagOverrides struct {
	// nil means no override; non-nil value is authoritative
	Tracing *bool `json:"tracing,omitempty"`
	Metrics *bool `json:"metrics,omitempty"`
}

var (
	ofClient              *openfeature.Client
	defaultTracing        atomic.Bool
	defaultMetrics        atomic.Bool
	overridesValue        atomic.Value // stores flagOverrides
	tracerProviderFactory = initTracer

	tracerInitMu      sync.Mutex
	tracerInitialized atomic.Bool
	tracerShutdownFn  func(context.Context) error
)

// init ensures overridesValue holds a zero flagOverrides and ofClient is
// non-nil before any code that might call them executes. Without this, a
// flag evaluation from a test that hasn't called initFeatureFlags() would
// panic on the atomic.Value type assertion or nil dereference.
func init() {
	overridesValue.Store(flagOverrides{})
	_ = openfeature.SetProvider(openfeature.NoopProvider{})
	ofClient = openfeature.NewClient("hello-world")
}

func initFeatureFlags(tracingDefault, metricsDefault bool) {
	// Set defaults
	defaultTracing.Store(tracingDefault)
	defaultMetrics.Store(metricsDefault)
	overridesValue.Store(flagOverrides{})

	// Only initialize the flagd remote provider when the operator has
	// explicitly opted in via FLAGD_HOST. Otherwise stay on the SDK's
	// built-in NoopProvider; this keeps tests and bare-metal runs from
	// spawning a background event-stream connection that we then need to
	// shut down safely (the openfeature SDK has had data races in
	// provider-swap on Shutdown — explicit opt-in avoids them entirely).
	host := os.Getenv("FLAGD_HOST")
	if host == "" {
		_ = openfeature.SetProvider(openfeature.NoopProvider{})
		ofClient = openfeature.NewClient("hello-world")
		return
	}
	portStr := getenvDefault("FLAGD_PORT", "8013")

	portU, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		log.Printf("flagd: invalid FLAGD_PORT %q, falling back to NoopProvider: %v", portStr, err)
		_ = openfeature.SetProvider(openfeature.NoopProvider{})
		ofClient = openfeature.NewClient("hello-world")
		return
	}

	provider := flagd.NewProvider(
		flagd.WithHost(host),
		flagd.WithPort(uint16(portU)),
		flagd.WithEventStreamConnectionMaxAttempts(3),
	)
	_ = openfeature.SetProvider(provider)
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
//
// Authentication: Requires X-Admin-API-Key header matching ADMIN_API_KEY env var
// If ADMIN_API_KEY is not set, endpoints are INSECURE (dev/local only)

func adminAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := os.Getenv("ADMIN_API_KEY")

		// Fail closed: if no API key is configured, reject all requests
		if apiKey == "" {
			logger.Warn().
				Str("remote_addr", r.RemoteAddr).
				Str("path", r.URL.Path).
				Msg("admin endpoint rejected: ADMIN_API_KEY not configured")
			http.Error(w, "Forbidden: admin API key not configured", http.StatusForbidden)
			return
		}

		// Check Authorization header (Bearer token) — constant-time comparison
		authHeader := r.Header.Get("Authorization")
		expected := "Bearer " + apiKey
		if authHeader != "" && subtle.ConstantTimeCompare([]byte(authHeader), []byte(expected)) == 1 {
			next(w, r)
			return
		}

		// Check X-Admin-API-Key header — constant-time comparison
		providedKey := r.Header.Get("X-Admin-API-Key")
		if providedKey != "" && subtle.ConstantTimeCompare([]byte(providedKey), []byte(apiKey)) == 1 {
			next(w, r)
			return
		}

		logger.Warn().
			Str("remote_addr", r.RemoteAddr).
			Str("path", r.URL.Path).
			Msg("unauthorized admin endpoint access attempt")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
}

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
		// support JSON body (limit to 1KB to prevent memory exhaustion)
		var body flagOverrides
		if ct := r.Header.Get("Content-Type"); ct == "application/json" || ct == "application/json; charset=utf-8" {
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "Bad Request: invalid JSON", http.StatusBadRequest)
				return
			}
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
		logger.Warn().Str("error", safeErr(err)).Msg("tracing init failed, continuing without tracing")
		return
	}
	tracerShutdownFn = shutdown
	tracerInitialized.Store(true)
	logger.Info().Msg("tracing provider initialized")
}

func shutdownTracerProvider(ctx context.Context) {
	tracerInitMu.Lock()
	shutdown := tracerShutdownFn
	tracerShutdownFn = nil
	tracerInitialized.Store(false)
	tracerInitMu.Unlock()

	if shutdown != nil {
		if err := shutdown(ctx); err != nil {
			logger.Error().Str("error", safeErr(err)).Msg("tracer shutdown error")
		}
	}
}
