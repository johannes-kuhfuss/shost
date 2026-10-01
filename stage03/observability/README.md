# Stage 03 - Observability

Deploy Grafana, Prometheus, Loki, Tempo, and Grafana Alloy in the existing
`observability` namespace. Grafana uses a local account; Authentik integration
and demo-service instrumentation are separate follow-up steps.

| Component | Chart repository / version | Application | Purpose |
| --- | --- | --- | --- |
| Grafana | grafana-community / 13.2.7 | 13.2.3 | UI, dashboards, signal navigation |
| Prometheus | prometheus-community / 29.35.0 | 3.15.0 | Metrics, 15-day retention, 8 GB TSDB size limit |
| Loki | grafana / 7.3.0 | 3.6.12 | Logs, 7-day retention |
| Tempo | grafana-community / 3.1.0 | 3.1.0 | Traces, 7-day retention, span metrics and exemplars |
| Alloy | grafana / 1.13.0 | 1.20.0 | Container log collection and OTLP ingestion |

`versions.env` pins the charts, including their default application and helper
images. Loki explicitly pins its patch image to the chart's advertised appVersion.
Grafana and Tempo use the maintained community repository; their old
charts in the Grafana repository are deprecated. This is a single-instance lab
deployment, with no Prometheus Operator, Kafka, object-store service, or
Alertmanager. The Prometheus chart also installs kube-state-metrics.

## Data flow and scope

```text
Kubernetes container log API -> Alloy -> Loki
Application OTLP             -> Alloy -> Loki / Prometheus / Tempo
Kubelet, cAdvisor, API server, kube-state-metrics -> Prometheus
Tempo span metrics and exemplars                -> Prometheus
Grafana -> Loki / Prometheus / Tempo
Browser -> HTTPS Gateway -> verified HTTPS Grafana
```

Alloy reads pod logs across the cluster using the Kubernetes API, with no root
user or host mounts. This includes existing demo-service console output, but
does not change or instrument that application. API-based log collection is
appropriate for this small cluster; it adds kubelet/API traffic and does not
collect Talos system-service logs. Prometheus uses the API server proxy for
kubelet and cAdvisor scrapes, verifying the API server's certificate. The extra
`nodes/proxy` RBAC permission is scoped to the Prometheus service account;
that permission grants broader kubelet proxy access than metrics alone.

The Platform overview dashboard shows node readiness, running pods, restarts,
container CPU/memory, scrape health, and observability logs. Arbitrary service
annotation scraping is disabled. Add explicit scrape jobs when enabling
component-specific metrics, such as Cilium or CloudNativePG. Talos host disk
and system-service monitoring, notification routing, and application dashboards
are not included in this first step.

## Prerequisites

Complete the [cert-manager, trust-manager, and shared Gateway setup](../cert-manager/README.md).
The cluster needs Cilium policies, Gateway API including BackendTLSPolicy v1,
and the existing `local-path` StorageClass. The commands below use Bash, Helm
3.19+ (or Helm 4), kubectl, and OpenSSL, from `stage03/observability`.

Allocate capacity for four PVCs: Grafana 1 GiB and each backend 10 GiB. The
configured requests total roughly 1.2 GiB RAM plus Kubernetes overhead; limits
allow about 5 GiB. Actual requirements depend on workload and query volume.
All PVCs use node-local storage and cannot fail over to another node. Time
retention does not cap Loki/Tempo disk use; local-path size requests are not
disk quotas. Prometheus's size retention also excludes some WAL/head overhead.
Monitor node free space and adjust ingestion, retention, and capacity together.

Create a DNS record for `grafana.tc.jku.internal` pointing to `192.168.200.221`.
Browsers must trust your web CA root.

## Validate before installation

The rendered workloads target Pod Security Admission's **Restricted** standard,
including sidecars, init containers, and Helm test pods. Prometheus and its
config-reloader explicitly disable privilege escalation and drop all capabilities;
Prometheus and Loki set a pod-level `RuntimeDefault` seccomp profile. The renderer
checks these settings along with non-root execution, volume types, and host access.
See the [Kubernetes Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/).

For an installation rejected by PSA, rerun the Prometheus and Loki
`helm upgrade --install` commands below with the updated values. No namespace
policy changes are needed. Check the rollout and events afterward; the local
checks do not replace admission by the cluster.

The renderer downloads the pinned charts and performs Helm linting and checks
for matching Services, persistent storage, TLS, and Grafana provisioning. It
does not access a cluster. Python 3 with `PyYAML==6.0.3` is needed for this check.

```bash
python3 -m venv /tmp/shost-observability-venv
source /tmp/shost-observability-venv/bin/activate
python3 -m pip install PyYAML==6.0.3
bash render.sh /tmp/shost-observability-rendered
```

CI also validates Alloy, Prometheus, Loki, and Tempo configuration using their
pinned container images. Local checks cannot establish cluster connectivity,
storage permissions, certificate issuance, or Gateway behavior; verify those
after installation.

## Install

Check `kubectl config current-context` before applying. Preserve the existing
namespace: it also contains the shared Gateway and Hubble resources.

```bash
source versions.env
helm repo add grafana https://grafana.github.io/helm-charts --force-update
helm repo add grafana-community https://grafana-community.github.io/helm-charts --force-update
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
helm repo update

# The namespace now opts into the internal CA bundle; Gateway adds grafana.
kubectl apply -f ../cert-manager/manifests/namespace.yaml
kubectl apply -f ../cert-manager/manifests/gateway.yaml
kubectl apply -f manifests/
kubectl -n observability wait configmap/tc-jku-internal-ca --for=create --timeout=2m
kubectl -n observability wait certificate/grafana-backend --for=condition=Ready --timeout=2m
kubectl -n observability wait certificate/grafana-tls --for=condition=Ready --timeout=2m
```

Create the local login once. This stores the bootstrap password in a Kubernetes
Secret without putting it in a values file, shell history, or a process argument.
Enter a strong password when prompted. Do not run with shell tracing (`set -x`).

```bash
if ! kubectl -n observability get secret grafana-admin >/dev/null 2>&1; then
  (
    set -euo pipefail
    umask 077
    credentials_dir="$(mktemp -d)"
    trap 'rm -rf -- "$credentials_dir"' EXIT
    printf admin > "$credentials_dir/admin-user"
    read -r -s -p 'Grafana admin password: ' grafana_password
    printf '\n'
    test -n "$grafana_password"
    printf '%s' "$grafana_password" > "$credentials_dir/admin-password"
    unset grafana_password
    kubectl -n observability create secret generic grafana-admin \
      --from-file=admin-user="$credentials_dir/admin-user" \
      --from-file=admin-password="$credentials_dir/admin-password"
  )
fi
```

Install each release independently. Keep these release names and namespace;
the provisioned endpoints and policies reference them.

```bash
helm upgrade --install prometheus prometheus-community/prometheus \
  --version "$PROMETHEUS_CHART_VERSION" --namespace observability \
  -f prometheus/values.yaml --wait --timeout 10m
helm upgrade --install loki grafana/loki \
  --version "$LOKI_CHART_VERSION" --namespace observability \
  -f loki/values.yaml --wait --timeout 10m
helm upgrade --install tempo grafana-community/tempo \
  --version "$TEMPO_CHART_VERSION" --namespace observability \
  -f tempo/values.yaml --wait --timeout 10m
helm upgrade --install alloy grafana/alloy \
  --version "$ALLOY_CHART_VERSION" --namespace observability \
  -f alloy/values.yaml --set-file alloy.configMap.content=alloy/config.alloy \
  --wait --timeout 10m
helm upgrade --install grafana grafana-community/grafana \
  --version "$GRAFANA_CHART_VERSION" --namespace observability \
  -f grafana/values.yaml \
  --set-file dashboards.platform.overview.json=grafana/overview.json \
  --wait --timeout 10m
```

Open **<https://grafana.tc.jku.internal>** and log in as `admin` with the password
you entered. Anonymous access and self-registration are disabled. Grafana stores
users in its persistent SQLite database. The Secret initializes a new database;
changing the Secret does not reset an existing account's password. Use Grafana's
account settings for subsequent password changes and keep the bootstrap Secret
consistent with your recovery procedure.

The backend certificate is mounted as a directory and Grafana reloads it every
minute. The Gateway validates its service DNS name against the trust-manager
CA bundle. Backend telemetry uses HTTP inside the cluster, protected by Cilium
ingress policies; this setup does not provide end-to-end TLS for OTLP/storage.

## Verify

```bash
kubectl -n observability get pods,pvc
kubectl -n observability describe httproute grafana
kubectl -n observability describe backendtlspolicy grafana
kubectl -n observability describe gateway internal-web
curl --fail --cacert /path/to/web-root-ca.pem \
  https://grafana.tc.jku.internal/api/health
```

Require `Accepted=True` and `ResolvedRefs=True` on the route, and `Accepted=True`
on BackendTLSPolicy. Check the Gateway's `grafana` listener and certificate, and
compare the distributed internal CA fingerprint as described in the platform
guide. A running Grafana pod alone does not verify the Gateway's backend TLS.

In Grafana, open **Platform / Platform overview**. After two scrape intervals,
node/pod metrics should appear and scrape targets should be up. In Explore:

- Prometheus: `up`, `kube_node_info`, and `container_memory_working_set_bytes`.
- Loki: `{namespace="observability"}` and `{namespace="demo-service"}`.
- Tempo: an empty result is expected until an OTLP client sends traces.

Inspect failures with `kubectl -n observability logs deploy/alloy -c alloy`,
`kubectl -n observability logs deploy/prometheus-server -c prometheus-server`,
and `kubectl -n observability logs statefulset/tempo`. Check Cilium/Hubble for
denied traffic. Inspect Prometheus targets through a local port forward:

```bash
kubectl -n observability port-forward service/prometheus-server 9090:80
# In another terminal: open http://localhost:9090/targets
```

For a synthetic check independent of the demo service, open these three
additional port forwards in separate terminals, then run the smoke script:

```bash
kubectl -n observability port-forward service/alloy 4318:4318
kubectl -n observability port-forward service/loki 3100:3100
kubectl -n observability port-forward service/tempo 3200:3200
# With the Prometheus forward above still running:
python3 smoke.py
```

The script submits one OTLP log, trace, and metric, then checks backend queries
and a trace exemplar. It uses only Python's standard library. Port forwarding
tests the ingestion pipeline, not the namespace ingress policy or browser UI.
Use its printed trace ID to verify Grafana log-to-trace and trace-to-log links.
The sample log is retained like other telemetry and expires normally.

## Application contract for step 2

No application namespace is opted into OTLP by this installation. Enable it
when instrumenting that application:

```bash
kubectl label namespace <application-namespace> telemetry.tc.jku.internal/client=true
```

Use `alloy.observability.svc.cluster.local:4317` for OTLP/gRPC or
`http://alloy.observability.svc.cluster.local:4318` for OTLP/HTTP. Set
`OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf` when using HTTP; exporters append
`/v1/logs`, `/v1/metrics`, or `/v1/traces`. Set `service.name` explicitly and use
cumulative metric temporality. Supply `k8s.pod.uid` as a resource attribute for
reliable enrichment (source connection IP is a fallback).

Grafana data-source UIDs are `prometheus`, `loki`, and `tempo`. Links support
OTLP log `trace_id` metadata and JSON log `trace_id` fields. Trace-to-log queries
match `service.name` to Loki's `service_name`; for container logs, Alloy uses the
pod's `app.kubernetes.io/name` label, falling back to the container name. Keep
that label and the application's OTel service name consistent. An additional
request ID can be searchable, but trace IDs power the provisioned links.

Trace-to-metrics links use Tempo-generated span metrics. Prometheus exemplar
storage and Tempo's exemplar remote-write are enabled. Metrics-to-trace links
require exemplars; ordinary aggregate metrics cannot identify every request.
IDs stay out of metric labels and Loki index labels. Choose one log delivery
path per application (container collection or OTLP) to avoid duplicate logs.

## Upgrade, persistence, and removal

Edit values, render again, review the diff, then repeat the pinned Helm commands.
Use `helm history <release> -n observability` and
`helm rollback <release> <revision> -n observability --wait` for compatible
rollbacks. Check upstream storage/config migration notes before version changes;
a Helm rollback does not undo on-disk format migrations.

Grafana and Prometheus PVCs have Helm's keep policy. Loki and Tempo StatefulSet
PVC deletion is disabled. Back up Grafana's database (with a consistent SQLite
backup or while stopped), its admin Secret, and any telemetry you need to keep
before destructive changes. Retained standalone PVCs may require explicit Helm
adoption when reinstalling; do not delete them just to make an install succeed.

Alloy's batching and retry queues are in memory. Collector restarts/outages can
lose in-flight OTLP data; API log tailing is not an exactly-once durable queue
and may replay or miss logs around restarts and kubelet rotation. This is the
lab's deliberate reliability tradeoff. Do not increase replicas without
revisiting collection duplication, storage, and backend deployment modes.

To remove the stack, uninstall only the five releases. Remove its route,
certificate, RBAC, and policies from `manifests/`, and remove only the `grafana`
listener from the shared Gateway. **Do not delete the `observability` namespace
or shared Gateway.** Leave PVCs and the admin Secret unless you deliberately
want to erase them. The StorageClass uses `reclaimPolicy: Delete`; deleting a
PVC may permanently delete its data.

## References

- [Alloy Kubernetes log collection](https://grafana.com/docs/alloy/latest/reference/components/loki/loki.source.kubernetes/)
- [Prometheus OpenTelemetry ingestion](https://prometheus.io/docs/guides/opentelemetry/)
- [Loki native OTLP ingestion](https://grafana.com/docs/loki/latest/send-data/alloy/examples/alloy-otel-logs/)
- [Tempo deployment modes](https://grafana.com/docs/tempo/latest/reference-tempo-architecture/deployment-modes/)
- [Grafana trace-to-log configuration](https://grafana.com/docs/grafana/latest/datasources/tempo/configure-tempo-data-source/configure-trace-to-logs/)
