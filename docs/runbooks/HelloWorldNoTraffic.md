# HelloWorldNoTraffic

**Severity:** info
**Page:** no — log-only

## Symptoms

- No HTTP requests observed for 10+ minutes.
- May be normal during off-hours for some environments.

## Immediate Actions

1. Confirm this is unexpected (check environment, time of day, traffic
   patterns).
2. Verify ingress is healthy:
   ```bash
   kubectl -n ingress-nginx get pods
   curl -sk -H 'Host: hello-world.local' https://<ingress-ip>/livez
   ```
3. Verify Service endpoints:
   ```bash
   kubectl -n hello-world get endpoints hello-world
   ```
4. Check DNS resolution from a client.

## Escalation

- If traffic is expected and ingress is healthy: open a ticket for
  upstream routing / DNS investigation.

## Links

- Dashboard: https://grafana.example.com/d/hello-world
