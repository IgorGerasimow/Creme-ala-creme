# HelloWorldSLOBurnSlow (1d / 3d windows)

**Severity:** warning
**Page:** no — open a ticket
**SLO:** 99.9% availability over 30 days. Budget = 0.1% / 30d.

## Symptoms

- One of `HelloWorldSLOBurnSlow1d` or `HelloWorldSLOBurnSlow3d` firing.
- Persistent low-grade error rate that does not show up as a fast burn,
  but is steadily eating the monthly budget.
- At 3x steady-state, the monthly budget is exhausted in ~10 days.
- At 1x steady-state, the budget is exhausted exactly at 30 days.

## Immediate Actions

This is a **ticket-grade** alert, not a page. Do NOT scramble — instead,
investigate during business hours and prevent the slow burn from
escalating into a fast burn.

1. Identify the dominant error class over the long window:
   ```promql
   sum by (handler, status) (rate(http_requests_total{job=~".*hello-world.*",status!~"2.."}[6h]))
   ```
2. Inspect logs for a recurring pattern:
   ```bash
   kubectl -n hello-world logs -l app.kubernetes.io/name=hello-world --since=6h | jq -r 'select(.level=="error") | .msg' | sort | uniq -c | sort -rn | head -20
   ```
3. Correlate with deploys, dependency outages, traffic shifts.
4. File a ticket with the findings; assign to the service owner.
5. Apply mitigation in the next sprint (or sooner if the budget burn
   accelerates).

## Escalation

- If the slow burn becomes a fast burn: follow `HelloWorldSLOBurnFast`
  runbook.
- If 80% of monthly budget is consumed: notify engineering management
  and freeze non-essential changes.

## Links

- SRE workbook (multi-window multi-burn): https://sre.google/workbook/alerting-on-slos/
- Dashboard: https://grafana.example.com/d/hello-world-slo
