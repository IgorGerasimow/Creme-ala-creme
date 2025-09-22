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
=======
	"os"
	"testing"
)

func TestGetBoolEnv(t *testing.T) {
	const envVar = "TEST_BOOL_FLAG"

	tests := []struct {
		name   string
		set    bool
		value  string
		def    bool
		expect bool
	}{
		{name: "truthy numeric", set: true, value: "1", def: false, expect: true},
		{name: "truthy word", set: true, value: "true", def: false, expect: true},
		{name: "truthy on", set: true, value: "on", def: false, expect: true},
		{name: "falsy numeric", set: true, value: "0", def: true, expect: false},
		{name: "falsy word", set: true, value: "false", def: true, expect: false},
		{name: "unset defaults true", set: false, def: true, expect: true},
		{name: "unset defaults false", set: false, def: false, expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(envVar, tt.value)
			} else {
				os.Unsetenv(envVar)
			}

			if got := getBoolEnv(envVar, tt.def); got != tt.expect {
				t.Fatalf("getBoolEnv(%q,%v)=%v want %v", envVar, tt.def, got, tt.expect)
			}
		})
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
