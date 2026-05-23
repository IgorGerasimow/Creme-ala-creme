package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// captureLogs swaps the package logger to write into a buffer and returns the
// buffer plus a cleanup func that restores stdout-backed logging.
func captureLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	prevOutput := loggerOutput
	prevLogger := logger
	setLoggerOutput(buf, zerolog.DebugLevel)
	return buf, func() {
		loggerOutput = prevOutput
		logger = prevLogger
	}
}

// firstLogLine returns the first JSON log entry written to buf, parsed as a
// map[string]any. Fails the test if buf has no parseable line.
func firstLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	scanner := buf.Bytes()
	nl := bytes.IndexByte(scanner, '\n')
	if nl < 0 {
		nl = len(scanner)
	}
	var m map[string]any
	if err := json.Unmarshal(scanner[:nl], &m); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nraw=%q", err, string(scanner[:nl]))
	}
	return m
}

func TestRedactSensitive(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOut string // exact match
		mustNot []string
	}{
		{
			name:    "empty",
			input:   "",
			wantOut: "",
		},
		{
			name:    "url with credentials",
			input:   "postgres://hello:supersecret@db:5432/hellodb",
			mustNot: []string{"hello:supersecret", "supersecret"},
		},
		{
			name:    "bearer token",
			input:   "Authorization: Bearer abc123XYZ_token_value",
			mustNot: []string{"abc123XYZ_token_value"},
		},
		{
			name:    "password kv",
			input:   "config: password=hunter2 retries=3",
			mustNot: []string{"hunter2"},
		},
		{
			name:    "database_url kv",
			input:   "DATABASE_URL=postgres://u:p@h/d failed to dial",
			mustNot: []string{"postgres://u:p@h/d"},
		},
		{
			name:    "no secret passes through",
			input:   "user requested /healthz",
			wantOut: "user requested /healthz",
		},
		{
			name:    "case insensitive Authorization",
			input:   "AUTHORIZATION = mytopsecret",
			mustNot: []string{"mytopsecret"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSensitive(tt.input)
			if tt.wantOut != "" && got != tt.wantOut {
				t.Fatalf("redactSensitive(%q)=%q want %q", tt.input, got, tt.wantOut)
			}
			for _, s := range tt.mustNot {
				if strings.Contains(got, s) {
					t.Fatalf("redactSensitive(%q)=%q must not contain %q", tt.input, got, s)
				}
			}
		})
	}
}

func TestSafeErr(t *testing.T) {
	if got := safeErr(nil); got != "" {
		t.Fatalf("safeErr(nil) = %q want empty", got)
	}
	got := safeErr(errors.New("dial postgres://u:p@db:5432/x: connection refused"))
	if strings.Contains(got, "u:p@db") {
		t.Fatalf("safeErr leaked credentials: %q", got)
	}
}

func TestLoggerFromContext_NoSpan(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()
	loggerFromContext(context.Background()).Info().Msg("nospan")
	line := firstLogLine(t, buf)
	if _, has := line["trace_id"]; has {
		t.Fatalf("trace_id should not be set without an active span: %v", line)
	}
}

func TestLoggerFromContext_WithSpan(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	exp := tracetest.NewInMemoryExporter()
	tp := newTestTracerProvider(t, exp)
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	defer span.End()

	loggerFromContext(ctx).Info().Msg("withspan")
	line := firstLogLine(t, buf)
	tid, _ := line["trace_id"].(string)
	if tid == "" || tid != span.SpanContext().TraceID().String() {
		t.Fatalf("trace_id mismatch: log=%q want=%q", tid, span.SpanContext().TraceID().String())
	}
	if sid, _ := line["span_id"].(string); sid == "" || sid != span.SpanContext().SpanID().String() {
		t.Fatalf("span_id mismatch: log=%q want=%q", sid, span.SpanContext().SpanID().String())
	}
}

func TestStatusRecorder_DefaultsTo200(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK}
	if _, err := rec.Write([]byte("hi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.status != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.status)
	}
	if rec.bytes != 2 {
		t.Fatalf("bytes=%d want 2", rec.bytes)
	}
}

func TestStatusRecorder_ExplicitWriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: rr, status: http.StatusOK}
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusTeapot {
		t.Fatalf("status=%d want 418", rec.status)
	}
	// Second WriteHeader must be ignored (committed)
	rec.WriteHeader(http.StatusInternalServerError)
	if rec.status != http.StatusTeapot {
		t.Fatalf("status changed after commit: %d", rec.status)
	}
}

func TestAccessLogMiddleware_EmitsOneJSONLine(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	handler := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodPost, "/probe", nil)
	req.RemoteAddr = "10.0.0.5:33333"
	req.Header.Set("User-Agent", "test/1.0")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", rr.Code)
	}
	line := firstLogLine(t, buf)
	for _, k := range []string{"level", "timestamp", "service", "handler", "method", "status", "duration_ms", "remote_addr", "user_agent"} {
		if _, ok := line[k]; !ok {
			t.Fatalf("missing key %q in log line: %v", k, line)
		}
	}
	if line["handler"] != "/probe" {
		t.Fatalf("handler=%v want /probe", line["handler"])
	}
	if line["method"] != "POST" {
		t.Fatalf("method=%v want POST", line["method"])
	}
	if statusF, _ := line["status"].(float64); int(statusF) != http.StatusAccepted {
		t.Fatalf("status=%v want 202", line["status"])
	}
	if line["remote_addr"] != "10.0.0.5" {
		t.Fatalf("remote_addr=%v want 10.0.0.5", line["remote_addr"])
	}
}

func TestAccessLogMiddleware_DoesNotLogBodyOrQuery(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	handler := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/x?token=SUPER_SECRET_VALUE",
		strings.NewReader(`{"password":"hunter2"}`))
	req.Header.Set("Authorization", "Bearer ANOTHER_SECRET")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	raw := buf.String()
	for _, s := range []string{"SUPER_SECRET_VALUE", "hunter2", "ANOTHER_SECRET"} {
		if strings.Contains(raw, s) {
			t.Fatalf("access log leaked %q: %s", s, raw)
		}
	}
}

func TestAccessLogMiddleware_TraceCorrelation(t *testing.T) {
	buf, restore := captureLogs(t)
	defer restore()

	exp := tracetest.NewInMemoryExporter()
	tp := newTestTracerProvider(t, exp)
	tracer := tp.Tracer("test")

	handler := accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "inner")
		// store span context back in request context so middleware sees it
		ctx := trace.ContextWithSpan(r.Context(), span)
		*r = *r.WithContext(ctx)
		span.End()
		w.WriteHeader(http.StatusOK)
	}))

	// pre-seed an active span on the inbound request
	ctx, parent := tracer.Start(context.Background(), "outer")
	defer parent.End()
	req := httptest.NewRequest(http.MethodGet, "/t", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	line := firstLogLine(t, buf)
	tid, _ := line["trace_id"].(string)
	if tid == "" || tid != parent.SpanContext().TraceID().String() {
		t.Fatalf("trace_id missing or mismatched: got=%q want=%q", tid, parent.SpanContext().TraceID().String())
	}
}

func TestClientIP_StripsPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.5:6543"
	if got := clientIP(r); got != "192.168.1.5" {
		t.Fatalf("clientIP=%q want 192.168.1.5", got)
	}
	r.RemoteAddr = "noport"
	if got := clientIP(r); got != "noport" {
		t.Fatalf("clientIP=%q want noport (no port)", got)
	}
}

func TestInitLogger_RespectsLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("LOG_FORMAT", "")
	initLogger()
	if logger.GetLevel() != zerolog.WarnLevel {
		t.Fatalf("level=%v want warn", logger.GetLevel())
	}
}

func TestInitLogger_BadLevelFallsBackToInfo(t *testing.T) {
	t.Setenv("LOG_LEVEL", "not-a-real-level")
	t.Setenv("LOG_FORMAT", "")
	initLogger()
	if logger.GetLevel() != zerolog.InfoLevel {
		t.Fatalf("level=%v want info", logger.GetLevel())
	}
}
