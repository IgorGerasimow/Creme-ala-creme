package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-feature/go-sdk/openfeature"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func Test_parseBoolEnv(t *testing.T) {
	tests := []struct {
		in   string
		def  bool
		want bool
	}{
		{"1", false, true},
		{"true", false, true},
		{"on", false, true},
		{"0", true, false},
		{"false", true, false},
		{"", true, true},
		{"", false, false},
	}
	for _, tt := range tests {
		if got := parseBoolEnv(tt.in, tt.def); got != tt.want {
			t.Fatalf("parseBoolEnv(%q,%v)=%v want %v", tt.in, tt.def, got, tt.want)
		}
	}
}

func TestTracingExportsAfterAdminEnable(t *testing.T) {
	ctx := context.Background()

	// Reset feature flag defaults and overrides to a known state
	defaultTracing.Store(false)
	defaultMetrics.Store(false)
	overridesValue.Store(flagOverrides{})
	metricsEnabled = false
	mtr = nil

	// Reset tracer state
	tracerInitialized.Store(false)
	tracerInitMu.Lock()
	tracerShutdownFn = nil
	tracerInitMu.Unlock()
	shutdownTracerProvider(context.Background())

	// Use a noop provider for OpenFeature evaluations
	openfeature.SetProvider(openfeature.NewNoopProvider())
	ofClient = openfeature.NewClient("test")

	exp := tracetest.NewInMemoryExporter()
	tracerProviderFactory = func(ctx context.Context) (func(context.Context) error, error) {
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
		)
		otel.SetTracerProvider(tp)
		return tp.Shutdown, nil
	}
	defer func() {
		tracerProviderFactory = initTracer
		shutdownTracerProvider(context.Background())
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}()

	if tracerInitialized.Load() {
		t.Fatalf("tracer should not be initialized before any enablement")
	}
	if enabled := isTracingEnabled(ctx); enabled {
		t.Fatalf("tracing should be disabled by default")
	}

	// Enable tracing via admin override after startup
	req := httptest.NewRequest(http.MethodPost, "/admin/flags", strings.NewReader(`{"tracing": true}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	adminFlagsHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin enable tracing returned status %d", rr.Code)
	}

	// Trigger handler which should now emit a span
	helloReq := httptest.NewRequest(http.MethodGet, "/", nil)
	helloRec := httptest.NewRecorder()
	helloHandler(helloRec, helloReq)
	if helloRec.Code != http.StatusOK {
		t.Fatalf("hello handler status = %d want 200", helloRec.Code)
	}

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatalf("expected spans to be exported after enabling tracing")
	}
	if spans[0].Name != "helloHandler" {
		t.Fatalf("unexpected span name %q", spans[0].Name)
	}
}
