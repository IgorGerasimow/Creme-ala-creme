# HelloWorldHighErrorRate

**Severity:** warning
**Page:** no — #monitoring

## Symptoms

- Non-2xx ratio above 5% over 5 minutes.
- Users may report sporadic 5xx responses.
- `HelloWorldSLOBurnFast1h` may follow if the rate sustains.

## Immediate Actions

1. Identify which status codes dominate:
   ```promql
   sum by (status) (rate(http_requests_total{job=~".*hello-world.*"}[5m]))
   ```
2. Inspect recent logs for stack traces or upstream failures:
   ```bash
   kubectl -n hello-world logs -l app.kubernetes.io/name=hello-world --tail=300 | jq -r 'select(.level=="error")'
   ```
3. Check downstream dependencies (Postgres, OTEL collector):
   ```bash
   kubectl -n hello-world exec deploy/hello-world -- nc -vz postgres 5432
   ```
4. If correlated with a recent deploy, **roll back**:
   ```bash
   kubectl -n hello-world rollout undo deploy/hello-world
   ```
5. If correlated with traffic spike, scale up:
   ```bash
   kubectl -n hello-world scale deploy/hello-world --replicas=4
   ```

## Escalation

- If error rate exceeds 25% for 5+ minutes: treat as critical.
- If downstream is the cause: page that team's on-call.

## Links

- Dashboard: https://grafana.example.com/d/hello-world
- Recent deploys: https://github.com/100rd/Creme-ala-creme/actions
