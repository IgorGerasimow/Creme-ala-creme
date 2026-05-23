# HelloWorldMetricsHandlerErrors

**Severity:** warning
**Page:** no — #monitoring

## Symptoms

- `promhttp_metric_handler_errors_total` incrementing over 10 minutes.
- `/metrics` may return 500 or partial content.
- Prometheus scrape may show partial data.

## Immediate Actions

1. Curl `/metrics` directly and inspect the response:
   ```bash
   kubectl -n hello-world port-forward deploy/hello-world 8080:8080 &
   curl -i http://localhost:8080/metrics
   ```
2. Check logs for encoding errors:
   ```bash
   kubectl -n hello-world logs -l app.kubernetes.io/name=hello-world --tail=200 | grep -i 'metric\|prom'
   ```
3. Look for memory pressure (handler errors are sometimes OOM-adjacent):
   ```bash
   kubectl -n hello-world top pod -l app.kubernetes.io/name=hello-world
   ```
4. If a recent deploy introduced a metric with invalid labels, **roll
   back** and file a follow-up to fix the instrumentation.

## Escalation

- If metric collection has been broken for 30+ minutes: treat as critical
  (observability blind-spot).

## Links

- promhttp upstream docs: https://pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp
