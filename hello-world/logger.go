package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// logger is the package-level base logger. It is overwritten by initLogger()
// at startup and may be overwritten again in tests via setLoggerOutput().
var logger zerolog.Logger

// loggerOutput holds the active io.Writer so tests can swap it. Access only
// from the test setup path; production code reads logger directly.
var loggerOutput io.Writer = os.Stdout

// serviceName is the value emitted as the "service" field in every JSON log.
const serviceName = "hello-world"

// initLogger configures the package-level structured JSON logger.
//
// Output is always JSON (one event per line) to stdout. LOG_LEVEL controls
// the minimum level (default "info"). Setting LOG_FORMAT=pretty switches to
// a human-readable console writer; this is intended for local development
// only and is never used by container deployments.
//
// All log lines carry: level, timestamp (RFC3339Nano), service, version.
// Per-event fields (trace_id, span_id, handler, method, status, duration_ms)
// are added by callers, primarily via accessLogMiddleware and
// loggerFromContext.
func initLogger() {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.TimestampFieldName = "timestamp"
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "message"
	zerolog.DurationFieldUnit = time.Millisecond
	zerolog.DurationFieldInteger = false

	var w io.Writer = os.Stdout
	if f := os.Getenv("LOG_FORMAT"); f == "pretty" || f == "console" {
		w = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}
	loggerOutput = w

	level := zerolog.InfoLevel
	if l := os.Getenv("LOG_LEVEL"); l != "" {
		if parsed, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(l))); err == nil {
			level = parsed
		}
	}

	logger = zerolog.New(loggerOutput).
		Level(level).
		With().
		Timestamp().
		Str("service", serviceName).
		Str("version", version).
		Logger()
}

// setLoggerOutput rebuilds the global logger writing to w. Tests use this to
// capture JSON output. Not exported for use outside tests.
func setLoggerOutput(w io.Writer, level zerolog.Level) {
	// Apply the same global field naming as initLogger so test output
	// matches production exactly.
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.TimestampFieldName = "timestamp"
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "message"

	loggerOutput = w
	logger = zerolog.New(w).
		Level(level).
		With().
		Timestamp().
		Str("service", serviceName).
		Str("version", version).
		Logger()
}

// loggerFromContext returns a logger enriched with the active OTEL trace_id
// and span_id when present in ctx. The returned logger is a value-typed
// child and is safe for concurrent use.
//
// Callers MUST treat the returned logger as ephemeral; do not cache it
// across requests because trace context is request-scoped.
func loggerFromContext(ctx context.Context) *zerolog.Logger {
	l := logger.With().Logger()
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		child := l.With().
			Str("trace_id", sc.TraceID().String()).
			Str("span_id", sc.SpanID().String()).
			Logger()
		return &child
	}
	return &l
}

// secretPatterns matches values that must never appear in logs. The list is
// intentionally short and high-signal: each entry represents a class of
// credential the application is known to handle.
//
// We match on URL-embedded credentials and common token shapes. We never log
// raw request bodies, raw headers, raw cookies, or the DATABASE_URL value;
// redactSensitive is a belt-and-suspenders defence for cases where an error
// message or upstream library accidentally embeds one of these.
var (
	// scheme://user:pass@host  ->  scheme://REDACTED@host
	urlCredsPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/\s:@]+:[^/\s:@]+@`)
	// bearer tokens
	bearerPattern = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._\-+/=]{8,}`)
	// generic key=value patterns for sensitive names. RE2 has no
	// bounded backtracking, so we use \S+ and rely on RE2's linear-time
	// matching to keep it safe on adversarial input.
	keyValuePattern = regexp.MustCompile(
		`(?i)\b(password|passwd|api[_\-]?key|secret|token|authorization|auth|x-admin-api-key|database_url|dsn)\b\s*[:=]\s*\S+`,
	)
)

// redactSensitive replaces values that look like credentials with a
// placeholder. It is safe to call on any string. The function is conservative
// — it errs on the side of false-positives (extra redactions) rather than
// false-negatives (leaked secrets).
func redactSensitive(s string) string {
	if s == "" {
		return s
	}
	s = urlCredsPattern.ReplaceAllString(s, "${1}REDACTED@")
	s = bearerPattern.ReplaceAllString(s, "${1}REDACTED")
	s = keyValuePattern.ReplaceAllStringFunc(s, func(m string) string {
		// preserve the key, blank the value
		eq := strings.IndexAny(m, ":=")
		if eq < 0 {
			return "REDACTED"
		}
		return m[:eq+1] + "REDACTED"
	})
	return s
}

// safeErr returns a redacted, log-safe representation of err. nil errors
// produce the empty string.
func safeErr(err error) string {
	if err == nil {
		return ""
	}
	return redactSensitive(err.Error())
}

// statusRecorder wraps http.ResponseWriter to capture the status code and
// the number of bytes written. It defaults to 200 if WriteHeader is never
// called explicitly (Go's net/http does this implicitly on the first Write).
type statusRecorder struct {
	http.ResponseWriter
	status    int
	bytes     int
	committed bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.committed {
		return
	}
	r.status = code
	r.committed = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.committed {
		r.status = http.StatusOK
		r.committed = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// accessLogMiddleware emits exactly one structured JSON log line per HTTP
// request. The line carries:
//
//	level, timestamp, service, version
//	handler, method, status, duration_ms, remote_addr, user_agent, bytes
//	trace_id, span_id  (when tracing is enabled for the request)
//
// When the OTEL tracing feature flag is enabled, the middleware starts a
// server-side span around the downstream handler so that one trace
// identifier covers the full request lifecycle. The handler receives the
// updated context via r.WithContext and any inner spans it creates become
// children of this one. This is the only place we mint a request-level
// span; handlers should only start spans for inner work.
//
// The middleware never logs request bodies, query strings, headers, or
// cookies — those may carry secrets. Values that flow into the log
// (currently only User-Agent) are passed through redactSensitive first.
func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := r.Context()

		if isTracingEnabled(ctx) {
			tr := otel.Tracer(serviceName)
			var span trace.Span
			ctx, span = tr.Start(ctx, "HTTP "+r.Method+" "+r.URL.Path)
			defer span.End()
			r = r.WithContext(ctx)
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		dur := time.Since(start)
		loggerFromContext(ctx).Info().
			Str("handler", r.URL.Path).
			Str("method", r.Method).
			Int("status", rec.status).
			Float64("duration_ms", float64(dur.Microseconds())/1000.0).
			Int("bytes", rec.bytes).
			Str("remote_addr", clientIP(r)).
			Str("user_agent", redactSensitive(r.UserAgent())).
			Msg("http_request")
	})
}

// clientIP returns a best-effort client IP. We deliberately do NOT trust
// X-Forwarded-For / X-Real-IP in this app because there is no documented
// trusted proxy chain; relying on those headers in this configuration would
// let any client spoof their address. Phase A surfaces RemoteAddr only.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	// strip port for cleaner logs
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}
