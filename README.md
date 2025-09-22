## Trunk-based development and CI -> Production

This repository uses trunk-based development with main as the single trunk. Below is the exact CI -> Production flow as implemented in .github/workflows/ci.yml.

Flow for Pull Requests targeting main (preview builds)
1) Checkout code
2) Set up Go toolchain (1.22)
3) Static analysis and security checks (SAST/SCA)
   - golangci-lint (lint)
   - go vet (static checks)
   - gofmt check (fails if formatting differs)
   - gosec (SAST)
   - govulncheck (Go vulnerability analysis)
4) CVE checks for the application code (SCA)
   - Trivy filesystem scan (severity: HIGH, CRITICAL -> fail)
5) Build container image
   - docker build -t hello-world:pr-<PR_NUMBER>
6) CVE checks for the container image
   - Trivy image scan (severity: HIGH, CRITICAL -> fail)
7) Integration test (container)
   - Run container on port 8080 with ENABLE_METRICS=true
   - Probe / to generate requests
   - Validate /metrics contains http_requests_total{handler="/",method="GET",status="200"}
8) DAST (non-authenticated baseline)
   - OWASP ZAP baseline scan against http://localhost:8080 (non-blocking by default)
9) Signing gate (emulated)
   - Create image.signature and verify presence; gate pushes on signature
10) Push PR image to ECR (runner IAM)
   - Tag and push: ${ECR_REGISTRY}/${ECR_REPOSITORY}:pr-<PR_NUMBER>

Flow for pushes to main (continuous delivery)
1) Same quality gates as PRs (steps 1–8)
2) Image tagging
   - IMAGE_TAG=latest (for main)
   - Also tag sha-<GITHUB_SHA>
3) Signing gate (emulated)
   - Require image.signature before any push
4) Push signed images to ECR (runner IAM)
   - ${ECR_REGISTRY}/${ECR_REPOSITORY}:latest
   - ${ECR_REGISTRY}/${ECR_REPOSITORY}:sha-<GITHUB_SHA>
5) Deploy to Kubernetes (best-effort)
   - Ensure kubectl is available and the runner has cluster access
   - If Flux/HelmRelease manifests are present:
     - Apply hello-world/deploy/flux/platform-design-gitrepository.yaml (if exists)
     - Apply hello-world/deploy/flux/hello-world-helmrelease.yaml (if exists)
   - If cluster or manifests are not available, step exits gracefully without failing the pipeline

Assumptions and environment
- Runners: self-hosted on AWS with IAM instance profile (no GitHub secrets for AWS)
- ECR: ECR_REPOSITORY and AWS_REGION are set at workflow env
- Image immutability: images are content-addressed/tags include sha for traceability
- Feature flags: app supports runtime toggles (OpenFeature/flagd in prod; admin endpoints in dev)

Notes
- You can enforce branch protections on main to require all quality gates before merging.
- Optional: add environment protection rules for deploy if you want manual approvals.

## Feature flags (dynamic, no restarts)
- Production: OpenFeature + flagd flags evaluated per request
  - tracing_enabled: toggle tracing
  - metrics_enabled: toggle Prometheus metrics and /metrics endpoint
- Local/dev: admin endpoints (no auth when ADMIN_FLAGS_ENABLED=true)
  - GET /admin/flags, POST /admin/flags, POST /admin/flags/reset

## TBD checklist (status)

- [x] App: add /readyz & /livez, graceful shutdown
- [ ] App: add JSON logs with trace ids
- [ ] Secrets: ESO+SOPS or Vault; DATABASE_URL via Secret (not plain env)
- [ ] CI immutability: switch Helm values to digests; GitOps PR to flux repo; env approvals
- [ ] Ingress/Gateway: HTTPS via cert-manager; smoke tests
- [ ] Alerts & runbooks: Slack/webhook, SLOs, runbook URLs
- [ ] RBAC & quotas: Role/RoleBinding; LimitRange/ResourceQuota
- [ ] Ephemeral envs: PR previews; auto cleanup
- [ ] Tests: increase coverage; add DB/OTEL integration tests
- [x] Prometheus rules applied by operator from repo (preferred) — rules file provided; flux integration present, CLI apply removed from CI
- [x] Security scanners in CI: golangci-lint, gosec, govulncheck, Trivy (fs+image), ZAP baseline
- [x] Trunk-based pipeline: PR image push, main deploy, signing gate (emulated)
- [x] Feature flags: OpenFeature + flagd (prod) and admin endpoints (dev)


## Main instructions
I'm assuming we are moving with `platform` approach - in which the repository with the application contains a sufficient set of configurations for running this application in kubernetes.
 - github pipeline - running tests and security scans 
 - alertmanager - alert rules
 - prometheus config for local test 
 - hello-world/deploy - flux deploy config ( I'm much more familiar with Argo - did what I can :) 
 - docker-compose for local development and alerts check ( TBD in github pipelines )
 - structure of application repo might look may not look logical - for example, why are the rules of  Prometheus in the root - and the migrations inside hello-world, there are two reasons 
    - the repository name is incorrect
    - a matter of habit

This design implies only two env's - non-prod and prod. 
 - non-prod - it is `shared` with dev/stage/demo/preview or any other without real load
 - prod is a prod :)

https://github.com/IgorGerasimow/platform-design - this repository contain: 
 - generic helm chart https://github.com/IgorGerasimow/platform-design/tree/main/helm
 - terragrunt for spinning up aws resources https://github.com/IgorGerasimow/platform-design/tree/main/terragrunt
 - example of Argocd apps and deploys https://github.com/IgorGerasimow/platform-design/tree/main/apps
 - git hub pipelines  https://github.com/IgorGerasimow/platform-design/tree/main/.github/workflows  

<img src="docs/12factor.svg" alt="12-Factor overview" width="800"/>

## Observability and Alerts

The app exposes Prometheus metrics at `/metrics` via `promhttp`. Key metrics:

- `http_requests_total{handler,method,status}`: total requests and status codes.
- `http_request_duration_seconds_bucket{handler,method,le}`: histogram buckets for latency.
- `promhttp_metric_handler_errors_total`: metrics handler errors.

PrometheusRule manifests are provided under `hello-world/monitoring/prometheus-rules.yaml` with alerts:

- HelloWorldTargetDown (critical): `up == 0` for targets matching job `.*hello-world.*` for 2m
- HelloWorldHighErrorRate (warning): non-2xx > 5% over 5m
- HelloWorldHighLatencyP95 (warning): P95 latency > 500ms over 5m
- HelloWorldNoTraffic (info): no requests over 10m
- HelloWorldMetricsHandlerErrors (warning): metrics handler errors in 10m


- Prometheus scrape config labels the app with `job: <something-containing-hello-world>` or adjust the label matcher in the rules.
- Alertmanager routes alerts based on `severity` and `service: hello-world` labels.

Apply the rules (example with kube-prometheus-stack):

```bash
kubectl apply -f hello-world/monitoring/prometheus-rules.yaml
```

## Run locally with Docker Compose

Start the app and Prometheus locally and have Prometheus scrape the app metrics:

```bash
docker compose up --build
```

- App: `http://localhost:8080/` and metrics at `http://localhost:8080/metrics`
- Health probes: readiness at `http://localhost:8080/readyz`, liveness at `http://localhost:8080/livez`
- Prometheus UI: `http://localhost:9090/`
  - Check `Status -> Targets` to see `hello-world` as UP
  - Try queries like: `sum by (status) (rate(http_requests_total[5m]))`

## Database and Migrations

This example contain 2 methods of migration - local for testing implemented with docker-compose as tool and ci based as a step in pipeline, second method are correct and must be executed outside of app itself to prevent problems in case of failed migration but deployed application 

The app optionally connects to Postgres if `DATABASE_URL` is set and runs SQL migrations on startup using golang-migrate. Local compose includes a `db` service and sets `DATABASE_URL` for the app.

- Migrations live in `hello-world/migrations` and are copied into the container at `/migrations`.
- Example `DATABASE_URL` (local): `postgres://hello:hello@db:5432/hellodb?sslmode=disable`.
- On startup, the app applies any pending migrations; if there are none, it continues.

To run locally:
```bash
docker compose up --build
```

To inspect the DB:
```bash
psql postgresql://hello:hello@localhost:5432/hellodb -c "\dt"
```


## CI/CD to Kubernetes prod (via Flux)

This repo ships a CI workflow that builds, scans, and publishes the image, validates Prometheus rules, and supports applying rules to a cluster with Prometheus Operator. Delivery to prod happens through Flux by updating a `HelmRelease` image tag.


### Pre-Conditions
- ECR repo name is equal to git repo name
- pushed `latest` and `revision` tags
- image as artifact is unchengable entity trought env's
- we run all jobs on selfhosted runners with proper IAM roles - ECR access, Lambdas ( if needed ) 
- delivery is not ideal for now

### Step-by-step
- Triggers: pushes to `main` or tags under `release/*` trigger prod delivery.
- Build/Test/Scan: run Go linter and tests; build image; run Trivy (fails on HIGH/CRITICAL).
- Tagging: on `release/*` use the release tag and also push `latest`; on `main` push `latest` and `sha-<commit>`.
- Push to ECR: login and push `${ECR_REGISTRY}/${ECR_REPOSITORY}:{tag}` and `sha-<commit>`; push `latest` when applicable.
- Validate alert rules: `promtool check rules hello-world/monitoring/prometheus-rules.yaml` (executed via the Prometheus Docker image in CI).
- Apply alert rules (Kubernetes path): if `PrometheusRule` CRD exists and `KUBE_CONFIG_B64` is set, CI wraps the rules into a `PrometheusRule` and `kubectl apply` in namespace `monitoring` with label `release: kube-prometheus-stack`.
- Update prod deployment (Flux): bump the `HelmRelease` image to the new tag, then commit so Flux reconciles and rolls out.
  - Option A (HelmRelease lives here): edit `hello-world/deploy/flux/hello-world-helmrelease.yaml` values `image.repository` and `image.tag`, commit to `main`.
  - Option B (HelmRelease lives in `platform-design`): open a PR to that repo (see `hello-world/deploy/flux/platform-design-gitrepository.yaml`), changing the same fields.
- Rollout verification (optional): `kubectl -n hello-world rollout status deploy/hello-world` and basic smoke checks.

Example edit for a repo that contains the `HelmRelease`:

```bash
yq -i '
  .spec.values.image.repository = env(ECR_REGISTRY) + "/" + env(ECR_REPOSITORY) |
  .spec.values.image.tag = env(IMAGE_TAG)
' hello-world/deploy/flux/hello-world-helmrelease.yaml
```

### Required secrets (GitHub Actions)
- SLACK_WEBHOOK_URL: notifications.
- PLATFORM_DESIGN_TOKEN: token with PR permissions.

### Prometheus prerequisites
- Prometheus Operator installed (e.g., kube-prometheus-stack) and watching namespace `monitoring`.
- Flux has access to your manifests repo and reconciles frequently (1m by default in `platform-design` `GitRepository`).


## What was improved (Reviewer Fixes)

- **12-Factor & Ops**: Added Helm chart with probes, HPA, PDB, ServiceMonitor, NetworkPolicies; namespace now enforces **Pod Security Standards** (`restricted`).
- **Security**: Hardened image (distroless, non-root, read-only FS); dropped capabilities; seccomp.
- **CI/CD**: Introduced GitHub Actions with lint, tests, SBOM, Trivy scan, and **cosign keyless signing**; optional GitOps PR step.
- **Repo hygiene**: Added `.gitignore`, removed committed binaries and macOS artifacts.
- **Observability**: ServiceMonitor to scrape `/metrics` (Prometheus Operator).

### Using the Helm chart

```bash
helm upgrade --install hello-world ./hello-world/helm/hello-world   --namespace hello-world --create-namespace   --set image.repository=ghcr.io/<org>/hello-world   --set image.tag=<sha-or-version>
```

### Probes & Policies

- Readiness: `GET /readyz` on containerPort 8080 (checks database connectivity when configured)
- Liveness: `GET /livez` (always exposed, independent of feature flags)
- Default-deny `NetworkPolicy` with explicit egress to Postgres and OTEL collector (adjust selectors to your environment).

The Helm chart exposes probe paths via `values.yaml` under `healthProbes` so you can override them per environment if desired.
