package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-feature/go-sdk/openfeature"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestTraceCorrelation_LogAndExporterAgree is the canonical Item 1 check:
// a request that triggers a span must produce both an exported span AND a
// JSON access log line, and they must agree on trace_id+span_id.
func TestTraceCorrelation_LogAndExporterAgree(t *testing.T) {
	resetFeatureFlagsForTest(t)

	// Force tracing ON for this test
	tru := true
	overridesValue.Store(flagOverrides{Tracing: &tru})

	// Wire an in-memory exporter into the global TracerProvider so the
	// helloHandler's tracer (otel.Tracer("hello-world")) records into it.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	otel.SetTracerProvider(tp)
	tracerInitialized.Store(true) // skip ensureTracerProvider's lazy init
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		tracerInitialized.Store(false)
	})

	// Use the noop OpenFeature provider so isTracingEnabled honours our
	// override deterministically.
	_ = openfeature.SetProvider(openfeature.NoopProvider{})
	ofClient = openfeature.NewClient("test")

	buf, restore := captureLogs(t)
	defer restore()

	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)
	handler := accessLogMiddleware(securityHeaders(mux))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:11111"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}

	spans := exp.GetSpans()
	// We expect two spans: the middleware's request span and the
	// helloHandler child span. Order is unspecified.
	if len(spans) < 1 {
		t.Fatalf("got %d spans, want >=1", len(spans))
	}
	// Find the request-level (middleware) span — it has no parent.
	var span *tracetest.SpanStub
	for i := range spans {
		if !spans[i].Parent.IsValid() {
			span = &spans[i]
			break
		}
	}
	if span == nil {
		t.Fatalf("no root span found in: %+v", spans)
	}

	// Find the http_request log line for "/"
	var line map[string]any
	for _, raw := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m["message"] == "http_request" && m["handler"] == "/" {
			line = m
			break
		}
	}
	if line == nil {
		t.Fatalf("no http_request log line found in:\n%s", buf.String())
	}

	logTraceID, _ := line["trace_id"].(string)
	logSpanID, _ := line["span_id"].(string)
	wantTraceID := span.SpanContext.TraceID().String()
	wantSpanID := span.SpanContext.SpanID().String()

	if logTraceID != wantTraceID {
		t.Fatalf("trace_id mismatch: log=%q span=%q", logTraceID, wantTraceID)
	}
	if logSpanID != wantSpanID {
		t.Fatalf("span_id mismatch: log=%q span=%q", logSpanID, wantSpanID)
	}
}

// TestTraceCorrelation_NoSpanNoTraceIDInLog asserts that when tracing is
// disabled, the JSON log line for a request omits trace_id/span_id (i.e.
// they aren't injected as empty strings, which would be noisy and could
// mask correlation bugs).
func TestTraceCorrelation_NoSpanNoTraceIDInLog(t *testing.T) {
	resetFeatureFlagsForTest(t)
	buf, restore := captureLogs(t)
	defer restore()

	mux := http.NewServeMux()
	mux.HandleFunc("/", helloHandler)
	handler := accessLogMiddleware(securityHeaders(mux))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	for _, raw := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m["message"] != "http_request" {
			continue
		}
		if _, has := m["trace_id"]; has {
			t.Fatalf("trace_id present in log when tracing disabled: %v", m)
		}
		if _, has := m["span_id"]; has {
			t.Fatalf("span_id present in log when tracing disabled: %v", m)
		}
		return
	}
	t.Fatalf("no http_request log line found: %s", buf.String())
}
