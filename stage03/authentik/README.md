# Stage 03 - Authentik with CloudNativePG

Deploy a single Authentik server, worker, and CNPG-managed PostgreSQL instance
for the test environment at:

```text
https://authentik.tc.jku.internal
```

SMTP and automated backups are deliberately deferred. **TODO before relying on
this installation: configure off-host database backups, back up Authentik's
secret key and file storage, and test a complete restore.**

## Integration with the existing cluster

| Existing component | Integration |
| --- | --- |
| CNPG operator | Owns `authentik-db`, creates database `authentik` and its application credentials |
| Talos local-path storage | 10 GiB database PVC and 1 GiB Authentik file PVC on encrypted node storage |
| Cilium shared Gateway | New HTTPS listener on `internal-web` at `192.168.200.221`; namespace opt-in and HTTPRoute |
| cert-manager web issuer | Gateway annotation creates `authentik-tls` in `observability` |
| cert-manager internal issuer | Separate PostgreSQL and Authentik backend certificates with full service DNS names |
| trust-manager | Existing public-root ConfigMap for Authentik and Gateway backend validation; new public-root Secret for CNPG |
| Cilium policies / Hubble | Limit database and server ingress; use Hubble to diagnose connection drops |
| Existing DHI registry Secret | Namespace-local copy for CNPG's DHI instance-manager init image |

The complete connection path is:

```text
Browser --HTTPS--> Cilium Gateway --verified HTTPS--> Authentik server
                                                    |
Authentik worker -----------------------------------+--verified PostgreSQL TLS--> CNPG
```

Both Authentik workloads use the primary's `-rw` Service directly with
`sslmode=verify-full`. PostgreSQL rejects non-TLS network connections. CNPG
manages replication client certificates separately from your internal server CA.
Neither database nor application Pods receive the intermediate signing key.

The Authentik backend certificate is imported from a mounted Secret into its
database using a blueprint and selected on the default Brand. Authentik serves
certificates from its database, so merely mounting the Secret is insufficient.
The worker's certificate discovery updates the same managed certificate object
on renewal, including private-key rotation. The Gateway validates the internal
service name while preserving the original HTTP Host header.

This does not yet make Hubble or the demo service require login. Add an Authentik
application/provider and the appropriate OIDC or proxy/outpost integration per
consumer in a subsequent step. Authentik itself must stay reachable without an
authentication dependency on itself. Kubernetes API OIDC integration is also a
separate change to Talos, not a prerequisite for this deployment.

## Versions and test topology

- Authentik upstream Helm chart and server/worker image: `2026.8.3`.
- PostgreSQL: `ghcr.io/cloudnative-pg/postgresql:18.6-minimal-trixie`.
- Existing CNPG operator: DHI `1.30.0`, chart `0.29.0`.
- Existing trust-manager: DHI `0.24.0`, chart `v0.24.0`.

Authentik 2026.8 supports PostgreSQL 14-18. The database image is CNPG's upstream
operand image; it is separate from the DHI operator. This Authentik release uses
PostgreSQL for sessions and background coordination and needs no Redis deployment.

Server and worker share `/data` through one local `ReadWriteOnce` PVC. Worker
affinity keeps them on the same node even if more nodes are added. `Recreate`
updates avoid overlapping versions writing this storage, and cause downtime.
The database has no replica and cannot fail over. Adding nodes alone does not
make either component highly available; revisit file storage and scheduling
before increasing application replicas.

The existing `local-path` StorageClass has `reclaimPolicy: Delete`: deleting a
PVC can delete its data. Its size requests are not reliable disk quotas. Monitor
node free space; retain the namespace, PVCs, and Secrets during upgrades.

## Prerequisites

Complete the [CNPG operator installation](../cloudnative-pg/README.md) and the
[cert-manager/trust-manager/Gateway installation](../cert-manager/README.md).
Use Bash with Helm 3 or 4, `kubectl`, `jq`, and OpenSSL. All commands below start
from `stage03`:

```bash
cd stage03
kubectl config current-context
kubectl get nodes
kubectl get storageclass local-path
kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=5m
kubectl get clusterissuer tc-jku-web-ca tc-jku-cluster-ca
kubectl -n observability get gateway internal-web
```

Ensure administrator devices trust your private root CA. Create a DNS A record
for `authentik.tc.jku.internal` pointing to `192.168.200.221`. This repository
does not manage your DNS server.

## 1. Namespace, pull credentials, and application secret

```bash
kubectl apply -f authentik/manifests/namespace.yaml
(
  set -euo pipefail
  kubectl -n kube-system get secret dhi-pull-secret -o json |
    jq '{apiVersion: "v1", kind: "Secret",
         metadata: {name: "dhi-pull-secret", namespace: "authentik"},
         type: .type, data: .data}' |
    kubectl apply -f -
)
```

Repeat the copy after rotating registry credentials. Create Authentik's secret
key once; preserve it across upgrades and restores. Run without shell tracing:

```bash
(
  set -euo pipefail
  if [ -z "$(kubectl -n authentik get secret authentik-secrets --ignore-not-found -o name)" ]; then
    umask 077
    secret_dir=$(mktemp -d)
    trap 'rm -f "$secret_dir/secret-key"; rmdir "$secret_dir"' EXIT
    openssl rand -hex 32 | tr -d '\n' > "$secret_dir/secret-key"
    kubectl -n authentik create secret generic authentik-secrets \
      --from-file=secret-key="$secret_dir/secret-key"
  fi
)
```

The database password is generated separately by CNPG in `authentik-db-app`.
Both workloads read that Secret directly; credentials are not stored in Helm
values or committed manifests.

## 2. Publish CA trust and issue certificates

Upgrade trust-manager using the updated shared values:

```bash
helm upgrade --install trust-manager \
  oci://quay.io/jetstack/charts/trust-manager \
  --version v0.24.0 --reset-values --namespace cert-manager \
  --values trust-manager/values.yaml --wait --timeout 5m

kubectl apply -f trust-manager/manifests/internal-ca-bundle.yaml
kubectl apply -f trust-manager/manifests/cnpg-ca-bundle.yaml
kubectl wait --for=condition=Synced bundle/tc-jku-internal-ca --timeout=2m
kubectl wait --for=condition=Synced bundle/tc-jku-cnpg-ca --timeout=2m
kubectl -n authentik get configmap tc-jku-internal-ca
kubectl -n authentik get secret tc-jku-cnpg-ca

kubectl apply -f authentik/manifests/certificates.yaml
kubectl -n authentik wait --for=condition=Ready \
  certificate/authentik-db certificate/authentik-backend --timeout=2m
```

The new Bundle writes **public certificates only**, to namespaces explicitly
labelled `trust.tc.jku.internal/cnpg-ca=true`. Secret-target support grants
trust-manager cluster-wide Secret read access; writes are authorized for the
single name `tc-jku-cnpg-ca`, rather than all existing Secrets. The original
ConfigMap bundle continues to serve its existing consumers.

The root source remains `tc-jku-internal-root-ca` in `cert-manager`. CNPG's CA
Secret and leaf Secret have `cnpg.io/reload` labels. Internal certificates use
only names below `svc.cluster.local`, preserving the existing CA constraints.

## 3. Provision PostgreSQL and file storage

```bash
kubectl apply -f authentik/manifests/network-policies.yaml
kubectl apply -f authentik/manifests/database.yaml
kubectl -n authentik wait --for=condition=Ready cluster/authentik-db --timeout=10m
kubectl -n authentik get secret authentik-db-app
kubectl apply -f authentik/manifests/data.yaml
kubectl apply -f authentik/manifests/blueprints.yaml
```

The file PVC can remain Pending until the application is scheduled because
local-path uses `WaitForFirstConsumer`. Do not wait for that PVC before Helm.
The policies permit PostgreSQL traffic from this application's Pods and database
replicas, and instance-manager access from the CNPG operator. Server ingress
permits Cilium's ingress identity on 9443 and Authentik's own workloads. Egress
is not restricted by these policies. Hubble can show any policy drops.

## 4. Install Authentik and select its backend certificate

```bash
helm repo add authentik https://charts.goauthentik.io
helm repo update authentik
helm upgrade --install authentik authentik/authentik \
  --namespace authentik --version 2026.8.3 \
  --values authentik/values.yaml --wait --timeout 10m

kubectl -n authentik rollout status deployment/authentik-server --timeout=5m
kubectl -n authentik rollout status deployment/authentik-worker --timeout=5m
kubectl -n authentik exec deployment/authentik-worker -- \
  ak apply_blueprint /blueprints/mounted/cm-authentik-platform-blueprints/backend-tls.yaml
```

The final command deterministically applies the backend certificate and Brand
selection before exposing the route; the mounted blueprint is also discovered
automatically. It requires completed database migrations and default blueprints.
If installation is still initializing, inspect worker logs and retry once ready.
Allow time for the server to refresh its certificate cache.

The chart's bundled PostgreSQL is disabled. Automatic Kubernetes outpost
discovery and its broad service-account permissions are disabled too; server
and worker use an application ServiceAccount without Kubernetes API credentials.
Add explicit outpost permissions later if required by a chosen integration.

## 5. Connect the shared Gateway

```bash
kubectl apply -f cert-manager/manifests/gateway.yaml
kubectl -n observability wait --for=condition=Ready certificate/authentik-tls --timeout=2m
kubectl apply -f authentik/manifests/backend-tls-policy.yaml
kubectl apply -f authentik/manifests/http-route.yaml

kubectl -n observability get gateway internal-web
kubectl -n authentik describe httproute authentik
kubectl -n authentik describe backendtlspolicy authentik
```

The HTTPRoute should report `Accepted=True` and `ResolvedRefs=True`; check the
BackendTLSPolicy's acceptance as well. No ReferenceGrant is needed: the route's
backend Service and the policy's CA ConfigMap are in the same namespace. The
Gateway's existing namespace selector permits this route to attach.

Visit `https://authentik.tc.jku.internal/if/flow/initial-setup/` and set the initial
administrator credentials. Enrol an MFA method after setup. SMTP is not configured,
so email-based recovery and notifications are unavailable for this test run.

## Verification and troubleshooting

- Log in, create a test user, and confirm it remains after restarting the server.
- Check server and worker logs for migration, PostgreSQL TLS, or task errors.
- Verify that the Gateway presents the web-issuer certificate and that backend
  validation succeeds without disabling certificate verification.
- Confirm database TLS from both application workloads:

  ```bash
  for workload in authentik-server authentik-worker; do
    kubectl -n authentik exec deployment/"$workload" -- ak shell -c \
      'from django.db import connection; c = connection.cursor(); c.execute("SELECT ssl, version FROM pg_stat_ssl WHERE pid = pg_backend_pid()"); print(c.fetchone())'
  done
  ```

  Expect `True` and a TLS version. The Helm configuration additionally requires
  CA and hostname verification on those connections.

- Restart the server and worker one at a time and verify login and uploaded files
  persist. Single-replica restarts interrupt service.
- Test certificate renewal using `cmctl renew -n authentik authentik-backend`
  and `cmctl renew -n authentik authentik-db` if the optional cert-manager CLI is
  installed. Check renewed expiry dates and connections. Authentik's worker
  watches mounted certificates and has an hourly discovery fallback; renewal
  has a 15-day margin. If needed, rerun the blueprint command and inspect worker
  logs. CNPG should reload its labelled certificate Secret automatically.

For 502 responses, check the backend Certificate, blueprint application, policy
status, and server certificate selection. Do not bypass verification. For an
endless loading screen or incorrect redirects, check the original Host and
`X-Forwarded-Proto` headers and Authentik's trusted proxy CIDRs. This configuration
uses Authentik's default private-network proxy ranges, matching the current
private Cilium network; narrow them to observed gateway source ranges when
hardening the deployment. Database policy drops or missing registry credentials
can be diagnosed with Hubble and Pod events respectively.

## Operations and follow-up work

**No backups are configured.** Before moving beyond this test, add the Barman
Cloud Plugin and an off-host object store, scheduled base backups and WAL
archiving, a backup of `/data` and `authentik-secrets`, and a restore drill.
Replication alone is not a backup.

Keep `authentik-secrets`, `authentik-db-app`, the database Cluster, and PVCs when
upgrading. A chart uninstall is not a database reset. Do not delete the namespace
to upgrade the application. If credentials change, restart both workloads so
their Secret-backed environment variables refresh. Authentik migrations run on
startup; downgrading the Helm release does not undo database migrations.

When expanding, reassess PostgreSQL replication and physical host placement,
shared/object file storage, application replicas, SMTP, and monitoring. There is
no Prometheus Operator installation in this repository, so ServiceMonitors are
not enabled. Existing Hubble provides network visibility but does not replace
database health, disk capacity, or backup monitoring.

## Local validation

Render the pinned application chart without contacting the Kubernetes API:

```bash
helm template authentik authentik/authentik \
  --namespace authentik --version 2026.8.3 --kube-version 1.36.3 \
  --values authentik/values.yaml > /tmp/authentik-rendered.yaml
```

On the deployment machine, after the relevant CRDs and namespace exist, validate
the manifests against the API before following the ordered installation steps:

```bash
kubectl apply --dry-run=server -f authentik/manifests/
```

## Sources

- [Authentik Kubernetes installation](https://docs.goauthentik.io/install-config/install/kubernetes/)
- [Pinned Authentik chart](https://github.com/goauthentik/helm/tree/authentik-2026.8.3/charts/authentik)
- [Authentik configuration](https://docs.goauthentik.io/install-config/configuration/)
- [Certificates and discovery](https://docs.goauthentik.io/sys-mgmt/certificates/)
- [Blueprint tags](https://docs.goauthentik.io/customize/blueprints/v1/tags/)
- [Authentik backup and restore](https://docs.goauthentik.io/sys-mgmt/ops/backup-restore/)
- [CNPG certificates](https://cloudnative-pg.io/docs/1.30/certificates/)
- [CNPG PostgreSQL images](https://github.com/cloudnative-pg/postgres-containers)
- [Trust-manager](https://cert-manager.io/docs/trust/trust-manager/)
