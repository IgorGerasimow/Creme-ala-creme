# Local validation manifests (kind cluster)

These manifests are NOT part of the Helm chart. They exist so the chart can be
exercised end-to-end on a single-node kind cluster without depending on real
cloud secret backends or Let's Encrypt.

In production these resources are provisioned by the platform-design repo
(real ClusterIssuer + real SecretStore + managed Postgres).

## Apply order

```bash
# 1. Cluster-wide prerequisites (cert-manager + external-secrets already installed)
kubectl apply -f hello-world/deploy/local/cluster-issuer.yaml
kubectl apply -f hello-world/deploy/local/secret-store.yaml

# 2. Postgres in its own namespace (PSS=baseline; the postgres:16-alpine entrypoint
#    needs more privileges than 'restricted' allows during initdb).
kubectl apply -f hello-world/deploy/local/postgres.yaml

# 3. The hello-world Helm chart. The chart creates the 'hello-world' namespace
#    with PSS=restricted (namespace.create=true in values-local.yaml).
helm install hello-world hello-world/helm/hello-world \
  --namespace hello-world \
  --create-namespace \
  -f hello-world/helm/hello-world/values-local.yaml
```

## Teardown

```bash
helm uninstall hello-world -n hello-world
kubectl delete -f hello-world/deploy/local/postgres.yaml
kubectl delete -f hello-world/deploy/local/secret-store.yaml
kubectl delete -f hello-world/deploy/local/cluster-issuer.yaml
kubectl delete namespace hello-world
kubectl delete namespace hello-world-db
```
