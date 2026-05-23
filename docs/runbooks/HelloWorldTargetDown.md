# HelloWorldTargetDown

**Severity:** critical
**Page:** yes — #oncall

## Symptoms

- Prometheus reports `up{job=~".*hello-world.*"} == 0` for 2+ minutes.
- `/readyz` or `/livez` not responding.
- Ingress returns 502/503 from the gateway.

## Immediate Actions

1. Confirm the alert is real, not a Prometheus scrape misconfig:
   ```bash
   kubectl -n hello-world get pods -l app.kubernetes.io/name=hello-world
   kubectl -n hello-world get endpoints
   kubectl -n monitoring port-forward svc/prometheus 9090
   # browse to http://localhost:9090/targets
   ```
2. Check pod status and recent events:
   ```bash
   kubectl -n hello-world describe pod -l app.kubernetes.io/name=hello-world
   kubectl -n hello-world logs -l app.kubernetes.io/name=hello-world --tail=200
   ```
3. If the deployment is crashlooping after a recent release, **roll back**:
   ```bash
   kubectl -n hello-world rollout undo deploy/hello-world
   ```
4. If the issue is at the ingress / cert layer:
   ```bash
   kubectl -n hello-world get certificate,ingress
   kubectl -n cert-manager logs -l app=cert-manager --tail=200
   ```
5. If the upstream (DB / OTEL collector) is unhealthy, check those first
   before restarting the app.

## Escalation

- If unresolved in 15 min: page the platform on-call (#oncall).
- If a security incident is suspected: page security-oncall.

## Links

- Dashboard: https://grafana.example.com/d/hello-world
- Recent deploys: https://github.com/100rd/Creme-ala-creme/actions
- Architecture: ../architecture/
