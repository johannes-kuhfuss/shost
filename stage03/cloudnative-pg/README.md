# Stage 03 - CloudNativePG operator

Install the CloudNativePG (CNPG) operator using the upstream Helm chart and
Docker Hardened Image (DHI). This prepares the cluster to manage PostgreSQL
databases for Authentik and future consumers.

This step installs the operator, CRDs, RBAC, and admission webhooks. It creates
no PostgreSQL instances, databases, persistent volumes, or backups. Database
certificates and trust-manager Secret targets belong to the subsequent database
setup; the operator manages its own webhook certificates.

## Versions and design

| Component | Pinned version |
| --- | --- |
| Upstream `cnpg/cloudnative-pg` Helm chart | `0.29.0` |
| Operator image | `dhi.io/cloudnative-pg:1.30.0` |

The chart's application version is also `1.30.0`. CNPG 1.30 supports Kubernetes
1.36, matching Stage 02's example Kubernetes version `1.36.3`. The DHI image
uses the regular non-root runtime variant, not the development or FIPS variant.
The tag pins the application release; it is not an immutable image digest.

The Helm release is `cnpg` in `cnpg-system`. Its Deployment is explicitly named
`cnpg-controller-manager`. One operator replica watches all namespaces and works
with either the one-node or three-node Stage 02 topology. Operator replica count
is independent of PostgreSQL replica count. Resource requests are initial lab
defaults and should be reviewed as the number of database clusters grows.

The security context uses DHI's UID/GID 65532. An `fsGroup` makes the chart's
writable volumes accessible while keeping the container root filesystem read-only.

## Prerequisites

- Complete Stage 02 and verify that the Talos nodes and Cilium are healthy.
- Use a deployment machine with Helm 3 or 4, `kubectl`, and `jq`; commands below use
  Bash, as in the other stage installation guides.
- Use a Kubernetes context with permission to install CRDs and cluster RBAC.
- Have `Secret/dhi-pull-secret` in `kube-system`, as configured in
  [Stage 02](../../stage02/docs/Stage-02%20Automated%20Installation.md#dhi-registry-access-before-cilium).
- Nodes must be able to pull from `dhi.io`. Workstation registry login does not
  supply Kubernetes image-pull credentials.

From the repository root:

```bash
cd stage03
kubectl config current-context
kubectl get nodes
helm list --all-namespaces
```

These instructions assume CNPG is not already installed by another release.
Use one cluster-wide operator installation.

## 1. Create the namespace and copy the registry credential

```bash
kubectl apply -f cloudnative-pg/manifests/namespace.yaml
(
  set -euo pipefail
  kubectl -n kube-system get secret dhi-pull-secret -o json |
    jq '{apiVersion: "v1", kind: "Secret",
         metadata: {name: "dhi-pull-secret", namespace: "cnpg-system"},
         type: .type, data: .data}' |
    kubectl apply -f -
)
kubectl -n cnpg-system get secret dhi-pull-secret
```

Run credential commands without shell tracing. The pipeline transfers the Secret
directly without writing credentials into the checkout. Repeat the copy after
rotating the source credential; namespace-local copies do not synchronize.

## 2. Install the operator

```bash
helm repo add cnpg https://cloudnative-pg.github.io/charts
helm repo update cnpg

helm upgrade --install cnpg cnpg/cloudnative-pg \
  --namespace cnpg-system \
  --version 0.29.0 \
  --values cloudnative-pg/values.yaml \
  --wait --timeout 5m
```

Helm installs the CRDs from the pinned chart. The chart also sets
`OPERATOR_IMAGE_NAME` to the overridden DHI image, keeping the operator's
instance-manager image reference consistent.

## 3. Verify installation

```bash
kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=5m
kubectl wait --for=condition=Established \
  crd/clusters.postgresql.cnpg.io --timeout=1m
kubectl -n cnpg-system get deployment cnpg-controller-manager \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
kubectl -n cnpg-system get pods
kubectl -n cnpg-system get service cnpg-webhook-service
kubectl -n cnpg-system get endpointslices \
  -l kubernetes.io/service-name=cnpg-webhook-service
kubectl -n cnpg-system logs deployment/cnpg-controller-manager --tail=100
kubectl get clusters.postgresql.cnpg.io --all-namespaces
```

Expect a ready operator Pod, image `dhi.io/cloudnative-pg:1.30.0`, an established
Cluster CRD, and a ready webhook endpoint. The last command should show no
PostgreSQL clusters on a fresh installation. Operator readiness does not yet test
database provisioning, persistence, or failover.

For `ImagePullBackOff`, inspect Pod events with `kubectl -n cnpg-system describe
pod <pod-name>` and check the namespace-local credential and registry access.
For admission webhook timeouts, check that the Kubernetes API server can reach
the operator's webhook on TCP 9443 through the chart's Service on port 443.

## Local rendering and upgrades

To render without accessing or changing a Kubernetes cluster:

```bash
helm template cnpg cnpg/cloudnative-pg \
  --namespace cnpg-system \
  --version 0.29.0 \
  --kube-version 1.36.3 \
  --values cloudnative-pg/values.yaml > /tmp/cnpg-rendered.yaml
```

For upgrades, review CNPG release notes and the Kubernetes support matrix, then
update the chart pin in this guide and the matching DHI image in `values.yaml`.
Render the new chart before repeating the installation command. The chart
manages CRD updates and marks CRDs to be retained on uninstall. Do not delete
CNPG CRDs as part of routine cleanup: deleting them also deletes the associated
custom resources. Once databases exist, operator upgrades can also affect their
instance managers and must be planned accordingly.

## Next step: Authentik's database

Create the Authentik namespace and a separate CNPG `Cluster` there, with its own
storage, database credentials, TLS certificate, CA trust Secret, and backup
configuration. The PostgreSQL image is separate from the operator image selected
here. Ensure database Pods can also pull the DHI operator image used for the
instance manager by providing registry credentials in the database namespace.

Keep the existing intermediate CA constraint and use full service names under
`svc.cluster.local` for database certificates and Authentik connections.

## Sources

- [Docker's CNPG image catalog](https://hub.docker.com/hardened-images/catalog/dhi/cloudnative-pg/images)
- [Docker's CNPG image guide](https://hub.docker.com/hardened-images/catalog/dhi/cloudnative-pg/guides)
- [Pinned upstream chart](https://github.com/cloudnative-pg/charts/tree/cloudnative-pg-v0.29.0/charts/cloudnative-pg)
- [CNPG supported releases](https://cloudnative-pg.io/docs/1.30/supported_releases/)
- [CNPG installation and upgrades](https://cloudnative-pg.io/docs/1.30/installation_upgrade/)
