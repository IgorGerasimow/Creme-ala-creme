# Runbooks

Operational runbooks linked from PrometheusRule `runbook_url` annotations.
Each runbook contains: symptoms, immediate actions, escalation, and links.

| Alert | Severity | Runbook |
| --- | --- | --- |
| HelloWorldTargetDown | critical | [HelloWorldTargetDown.md](HelloWorldTargetDown.md) |
| HelloWorldHighErrorRate | warning | [HelloWorldHighErrorRate.md](HelloWorldHighErrorRate.md) |
| HelloWorldHighLatencyP95 | warning | [HelloWorldHighLatencyP95.md](HelloWorldHighLatencyP95.md) |
| HelloWorldNoTraffic | info | [HelloWorldNoTraffic.md](HelloWorldNoTraffic.md) |
| HelloWorldMetricsHandlerErrors | warning | [HelloWorldMetricsHandlerErrors.md](HelloWorldMetricsHandlerErrors.md) |
| HelloWorldSLOBurnFast1h / 6h | critical | [HelloWorldSLOBurnFast.md](HelloWorldSLOBurnFast.md) |
| HelloWorldSLOBurnSlow1d / 3d | warning | [HelloWorldSLOBurnSlow.md](HelloWorldSLOBurnSlow.md) |

## Conventions

- Every runbook starts with **Symptoms** so the on-call can confirm the alert.
- **Immediate Actions** are ordered from cheapest/safest to most invasive.
- **Escalation** lists who to ping and on which channel.
- **Links** points to dashboards, logs, and recent change logs.
