# HelloWorldSLOBurnFast (1h / 6h windows)

**Severity:** critical
**Page:** yes — #oncall
**SLO:** 99.9% availability over 30 days. Budget = 0.1% / 30d.

## Symptoms

- One of `HelloWorldSLOBurnFast1h` or `HelloWorldSLOBurnFast6h` firing.
- Error budget is being consumed faster than 6x — 14.4x the steady-state
  rate.
- At 14.4x, the entire monthly error budget is consumed in ~2 days.
- At 6x, it is consumed in ~5 days.

## Immediate Actions

This alert is correlated with `HelloWorldHighErrorRate` and/or
`HelloWorldTargetDown` — start with their runbooks first.

1. Compute current burn rate to decide urgency:
   ```promql
   1000 * job:hello_world_request_errors:ratio_rate5m
   ```
   A value of 14.4 means "consuming budget 14.4x faster than steady-state".

2. Triage by error class:
   ```promql
   sum by (status) (rate(http_requests_total{job=~".*hello-world.*"}[5m]))
   ```
3. **Freeze deploys.** No deploys to prod until burn rate is back under 1.
4. Roll back the most recent deploy if the burn started after it:
   ```bash
   kubectl -n hello-world rollout undo deploy/hello-world
   ```
5. If burn persists after rollback, declare an incident and engage
   the on-call channel.

## Recovery

- Burn rate should drop within 5 minutes of mitigation.
- The alert auto-resolves when the recording rules drop below the burn
  threshold (`14.4 * 0.001` for fast-1h, `6 * 0.001` for fast-6h).

## Escalation

- If unresolved in 30 min: page engineering manager.
- If 50% of monthly budget is consumed: declare a Sev-1.

## Links

- SRE workbook (multi-window multi-burn): https://sre.google/workbook/alerting-on-slos/
- Dashboard: https://grafana.example.com/d/hello-world-slo
