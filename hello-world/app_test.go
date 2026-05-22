package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResolveListenAddr_DefaultPort(t *testing.T) {
	t.Setenv("PORT", "")
	if got := resolveListenAddr(); got != ":8080" {
		t.Fatalf("resolveListenAddr=%q want :8080", got)
	}
}

func TestResolveListenAddr_RespectsPort(t *testing.T) {
	t.Setenv("PORT", "9090")
	if got := resolveListenAddr(); got != ":9090" {
		t.Fatalf("resolveListenAddr=%q want :9090", got)
	}
}

func TestBuildServer_WrapsHandlerWithSecurityAndAccessLog(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := buildServer(":0", inner)
	if srv.ReadHeaderTimeout == 0 {
		t.Fatalf("ReadHeaderTimeout must be set")
	}
	if srv.WriteTimeout == 0 {
		t.Fatalf("WriteTimeout must be set")
	}
	if srv.Handler == nil {
		t.Fatalf("Handler is nil")
	}

	// Exercise it: a request should produce security headers (proves
	// securityHeaders middleware was applied) and an access log line.
	buf, restore := captureLogs(t)
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)

	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers not applied by buildServer")
	}
	if !strings.Contains(buf.String(), `"http_request"`) {
		t.Fatalf("access log not applied by buildServer: %s", buf.String())
	}
}

func TestBuildMux_RoutesAreWired(t *testing.T) {
	resetFeatureFlagsForTest(t)
	checker := dependencyChecker{db: nil}
	mux := buildMux(checker, false)

	cases := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{"/", http.StatusOK, "hello world"},
		{"/livez", http.StatusOK, "alive"},
		{"/readyz", http.StatusOK, "ready"},
		{"/metrics", http.StatusNotFound, "metrics disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rr.Code != tc.wantStatus {
				t.Fatalf("%s status=%d want %d", tc.path, rr.Code, tc.wantStatus)
			}
			if !strings.Contains(rr.Body.String(), tc.wantBody) {
				t.Fatalf("%s body=%q want contains %q", tc.path, rr.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestBuildMux_AdminFlagsDisabledByDefault(t *testing.T) {
	resetFeatureFlagsForTest(t)
	checker := dependencyChecker{db: nil}
	mux := buildMux(checker, false)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/flags", nil))
	// /admin/flags is NOT registered when adminFlagsEnabled=false, so this
	// falls through to "/" catch-all and returns "hello world".
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (catch-all)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "hello world") {
		t.Fatalf("expected catch-all body, got %q", rr.Body.String())
	}
}

func TestBuildMux_AdminFlagsEnabled_RegistersAdminRoutes(t *testing.T) {
	resetFeatureFlagsForTest(t)
	// Without ADMIN_API_KEY, the middleware fails closed with 403.
	t.Setenv("ADMIN_API_KEY", "")
	checker := dependencyChecker{db: nil}
	mux := buildMux(checker, true)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/flags", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403 (admin auth must fail closed)", rr.Code)
	}
}

func TestInitApp_NoDatabaseConfig_Succeeds(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("ENABLE_METRICS", "false")
	t.Setenv("ENABLE_TRACING", "false")
	t.Setenv("ADMIN_FLAGS_ENABLED", "false")
	t.Setenv("PORT", "0")
	resetFeatureFlagsForTest(t)

	srv, cleanup, err := initApp()
	if err != nil {
		t.Fatalf("initApp: %v", err)
	}
	defer cleanup()
	if srv == nil {
		t.Fatalf("initApp returned nil server")
	}
	if srv.Handler == nil {
		t.Fatalf("server has no handler")
	}
}

func TestInitTracer_Succeeds(t *testing.T) {
	// otlptracehttp.New does not perform a network call, so initTracer
	// must return a non-nil shutdown function and a nil error even when
	// the endpoint is unreachable. We point at localhost:1 just to be
	// explicit about not connecting to anything real.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_SERVICE_NAME", "creme-test")
	t.Setenv("ENVIRONMENT", "test")

	shutdown, err := initTracer(context.Background())
	if err != nil {
		t.Fatalf("initTracer: %v", err)
	}
	if shutdown == nil {
		t.Fatalf("shutdown is nil")
	}
	// Shutdown should not panic; allow it to fail (unreachable endpoint).
	_ = shutdown(context.Background())
}

func TestInitTracer_DefaultsServiceName(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	shutdown, err := initTracer(context.Background())
	if err != nil {
		t.Fatalf("initTracer: %v", err)
	}
	_ = shutdown(context.Background())
}

func TestInitApp_BadDatabaseURL_ReturnsError(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x:x@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	t.Setenv("ENABLE_METRICS", "false")
	t.Setenv("ENABLE_TRACING", "false")
	t.Setenv("ADMIN_FLAGS_ENABLED", "false")
	resetFeatureFlagsForTest(t)

	// Short-circuit the 45s production deadline; we only need to assert
	// that the error path returns a non-nil error.
	prev := dbStartupTimeout
	dbStartupTimeout = 300 * time.Millisecond
	defer func() { dbStartupTimeout = prev }()

	_, cleanup, err := initApp()
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil {
		t.Fatalf("expected initApp to fail with unreachable DATABASE_URL")
	}
	if strings.Contains(err.Error(), "127.0.0.1:1") && !strings.Contains(err.Error(), "REDACTED") {
		// We don't actually require redaction at the error-return level
		// (the caller does that via safeErr before logging) — this is
		// informational only.
		t.Logf("initApp error includes raw address (caller is responsible for safeErr): %v", err)
	}
}
