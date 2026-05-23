# HelloWorldHighLatencyP95

**Severity:** warning
**Page:** no — #monitoring

## Symptoms

- P95 request latency > 500ms over 5 minutes.
- User-reported slowness.

## Immediate Actions

1. Identify which handler is slow:
   ```promql
   histogram_quantile(0.95,
     sum by (le, handler) (rate(http_request_duration_seconds_bucket{job=~".*hello-world.*"}[5m]))
   )
   ```
2. Check CPU / memory pressure:
   ```bash
   kubectl -n hello-world top pod -l app.kubernetes.io/name=hello-world
   ```
3. If saturation is the cause, scale up or raise resource limits:
   ```bash
   kubectl -n hello-world scale deploy/hello-world --replicas=4
   ```
4. Check downstream (DB query time):
   ```bash
   kubectl -n hello-world exec deploy/hello-world -- /bin/sh -c 'psql "$DATABASE_URL" -c "SELECT pid, query, state FROM pg_stat_activity WHERE state <> '\''idle'\''"'
   ```
5. If correlated with a recent deploy, roll back.

## Escalation

- If latency exceeds 2s for 5+ minutes: page on-call.
- If downstream DB is the cause: page DBA on-call.

## Links

- Dashboard: https://grafana.example.com/d/hello-world-latency
