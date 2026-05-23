package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Tests in this file exercise integration of multiple package components
// without external dependencies (no DB, no OTEL collector). They are unit
// tests, but they reach more lines than narrow per-handler tests, which is
// how we drive coverage of routing, middleware composition, and gating
// logic up over the 80% bar.

func TestRouter_WithMiddleware_FullStack(t *testing.T) {
	resetFeatureFlagsForTest(t)
	checker := dependencyChecker{db: nil}

	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)
	mux.HandleFunc("/readyz", checker.readinessHandler)
	mux.HandleFunc("/livez", checker.livenessHandler)

	srv := httptest.NewServer(accessLogMiddleware(securityHeaders(mux)))
	defer srv.Close()

	buf, restore := captureLogs(t)
	defer restore()

	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		// Note: Go's http.ServeMux treats "/" as a catch-all, so any path
		// not otherwise matched routes to helloHandler. This is the
		// documented behaviour of net/http and Phase B may revisit it.
		{"hello", "/", http.StatusOK, "hello world"},
		{"livez", "/livez", http.StatusOK, "alive"},
		{"readyz nil db", "/readyz", http.StatusOK, "ready"},
		{"catch-all routes to hello", "/something-else", http.StatusOK, "hello world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status=%d want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantBody != "" {
				body, _ := io.ReadAll(resp.Body)
				if !strings.Contains(string(body), tc.wantBody) {
					t.Fatalf("body=%q want contains %q", string(body), tc.wantBody)
				}
			}
			// security headers must be present on every response
			if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("missing X-Content-Type-Options")
			}
		})
	}

	// at least one log line per request should have been emitted
	if !strings.Contains(buf.String(), `"http_request"`) {
		t.Fatalf("no http_request log lines emitted: %s", buf.String())
	}
}

func TestMetricsGate_DisabledReturns404(t *testing.T) {
	resetFeatureFlagsForTest(t)
	// metrics flag is false by default
	promHandler := promhttp.Handler()
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMetricsEnabled(r.Context()) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("metrics disabled"))
			return
		}
		promHandler.ServeHTTP(w, r)
	})

	rr := httptest.NewRecorder()
	gated.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 when metrics disabled", rr.Code)
	}
}

func TestMetricsGate_EnabledReturns200(t *testing.T) {
	resetFeatureFlagsForTest(t)
	tru := true
	overridesValue.Store(flagOverrides{Metrics: &tru})

	promHandler := promhttp.Handler()
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMetricsEnabled(r.Context()) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		promHandler.ServeHTTP(w, r)
	})

	rr := httptest.NewRecorder()
	gated.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 when metrics enabled", rr.Code)
	}
}

func TestWaitForDatabase_DeadlineRespected(t *testing.T) {
	// 127.0.0.1:1 should never accept Postgres connections; use a very
	// short timeout so the test completes in well under a second.
	start := time.Now()
	_, err := waitForDatabase("postgres://x:x@127.0.0.1:1/nope?sslmode=disable&connect_timeout=1", 200*time.Millisecond)
	dur := time.Since(start)
	if err == nil {
		t.Fatalf("waitForDatabase succeeded against bogus address")
	}
	if dur > 5*time.Second {
		t.Fatalf("waitForDatabase took too long: %v", dur)
	}
}

func TestInitFeatureFlags_BadPortFallsBackGracefully(t *testing.T) {
	t.Setenv("FLAGD_HOST", "127.0.0.1")
	t.Setenv("FLAGD_PORT", "not-a-number")
	// Must not panic; must leave ofClient usable for evaluations.
	initFeatureFlags(false, false)
	if ofClient == nil {
		t.Fatalf("ofClient is nil after init with bad port")
	}
	// Evaluating a flag should return the default (no panic).
	got := isMetricsEnabled(context.Background())
	if got {
		t.Fatalf("metrics enabled with default false")
	}
}

func TestEnsureTracerProvider_IsIdempotent(t *testing.T) {
	// Reset state
	tracerInitialized.Store(false)
	tracerInitMu.Lock()
	tracerShutdownFn = nil
	tracerInitMu.Unlock()

	calls := 0
	prev := tracerProviderFactory
	tracerProviderFactory = func(ctx context.Context) (func(context.Context) error, error) {
		calls++
		return func(context.Context) error { return nil }, nil
	}
	defer func() { tracerProviderFactory = prev }()

	ctx := context.Background()
	ensureTracerProvider(ctx)
	ensureTracerProvider(ctx)
	ensureTracerProvider(ctx)
	if calls != 1 {
		t.Fatalf("factory called %d times, want exactly 1 (idempotent)", calls)
	}
	shutdownTracerProvider(ctx)
}

func TestShutdownTracerProvider_SafeWhenUninitialized(t *testing.T) {
	tracerInitialized.Store(false)
	tracerInitMu.Lock()
	tracerShutdownFn = nil
	tracerInitMu.Unlock()
	// Must not panic
	shutdownTracerProvider(context.Background())
}
