# Demo Service

The demo service exercises the platform's HTTP hosting, health probes, graceful
shutdown, backend TLS, and automatic certificate rotation.

## Capabilities

- Hosting
- Certificates
- Graceful request draining
- Kubernetes startup, readiness, and liveness probes

## Endpoints

| Path | Purpose |
| --- | --- |
| `/` | Basic service response |
| `/health/startup` | Startup probe |
| `/health/ready` | Readiness probe; returns 503 while draining |
| `/health/live` | Liveness probe |
| `/logs` | Form to write a message with Debug, Info, Warn, or Error severity (GET displays the form; POST sends the message) |
| `/certificate` | Web page displaying the current TLS certificate's subject, issuer, serial number, DNS names, validity dates, and SHA-256 fingerprint |

`/certificate` returns 503 when TLS is disabled or no certificate has been
loaded. It never returns private-key material.

## Build

The builder uses `dhi.io/golang:1.27.1-debian13-dev`. Authenticate with your
Docker Hub username (or organization name) and read-only access token before
building. The development variant supplies the shell and build tools needed
by the Dockerfile's `RUN` instructions.

```bash
docker login dhi.io
docker build -t johanneskuhfuss/demo-service .
```

Adjust the tag to your account / linking.

For GitHub Actions, configure repository secrets `DHI_USERNAME` and `DHI_TOKEN`
with the same read-only registry access. The workflow logs in before building
and logs out afterward. Fork pull requests and Dependabot pull requests run
the Go and manifest checks but skip the authenticated image build and container
smoke test; those checks run on ordinary same-repository pull requests and pushes.
The Kubernetes pull Secret does not authenticate local or CI builds.

The final image is a non-root `scratch` image. It contains only the service
binary and listens on plain HTTP by default:

```bash
docker run --rm --read-only --user 65532:65532 -p 8080:8080 \
  johanneskuhfuss/demo-service
curl http://localhost:8080/health/ready
```

## Kubernetes deployment with Helm

Run these commands from `stage03/demo-service` using Helm 3.19+ or Helm 4.
The chart owns the Deployment, Service, backend Certificate, BackendTLSPolicy,
and HTTPRoute. Resource names and selectors stay `demo-service`; install only
one release per namespace. TLS, probes, security settings, and certificate
rotation retain their existing behavior.

Prerequisites are the Gateway API CRDs (including BackendTLSPolicy v1), Cilium
Gateway controller, cert-manager, the configured ClusterIssuer, trust-manager
and its internal CA bundle, and the shared `observability/internal-web` Gateway
with its `demo-service` listener and frontend certificate. These platform
resources remain outside the application release. See the
[platform setup](../cert-manager/README.md).

Build and push an image with a unique version or commit tag to your registry.
Set `IMAGE_TAG` to that published tag; the chart deliberately has no default
image tag. Override `image.repository` if you use a different registry.

```bash
export IMAGE_TAG='replace-with-published-version-or-commit-tag'

# One-time namespace bootstrap; preserves the Gateway and trust-bundle labels.
kubectl apply -f manifests/namespace.yaml

helm upgrade --install demo-service ./chart \
  --namespace demo-service \
  --values values-lab.yaml \
  --set-string image.tag="$IMAGE_TAG" \
  --wait --timeout 5m
```

The namespace remains outside Helm so uninstalling the application does not
delete the namespace or controller-managed trust bundle. For a different
namespace, create it with the same opt-in labels and use that namespace in the
Helm command. The backend certificate DNS name and TLS policy automatically
follow the release namespace and `clusterDomain`.

`chart/values.yaml` documents the available settings: image, replica count,
resource requests/limits, logging, shutdown timing, hostname, gateway reference,
certificate issuer/lifetime, and trust bundle. `values-lab.yaml` holds the lab
platform references. The schema checks required values and logging options;
template validation requires the termination grace period to exceed drain plus
shutdown time. Use immutable tags so Helm rollback restores a known image.

### Migrate an existing kubectl deployment

Keep the namespace and existing resources in place. Review the rendered chart
before adoption, using the image tag you intend to run:

```bash
helm template demo-service ./chart --namespace demo-service \
  -f values-lab.yaml --set-string image.tag="$IMAGE_TAG" > /tmp/demo-service.yaml
kubectl diff -f /tmp/demo-service.yaml
```

`kubectl diff` exits with status 1 when it finds changes. Confirm that names,
selectors, gateway references, and certificate settings match the existing
installation. Then run the deployment command above with `--take-ownership`
added **once**. This explicitly adopts the five existing application resources
into the release. Do not delete them first. A pod rollout is expected if the
image or pod configuration changes. Subsequent upgrades omit this flag and use
Helm exclusively; the old raw application manifests have been replaced by the
chart. The namespace manifest is still used for bootstrap.

Helm's [ownership documentation](https://docs.helm.sh/docs/v3/topics/advanced/)
describes adoption. Avoid automatic uninstall-on-failure during the initial
adoption: inspect a failed release and correct it with another upgrade. Helm
cannot roll back to the pre-Helm deployment because it has no release revision
for that state.

### Verify, upgrade, and remove

Helm waiting for the Deployment is not an end-to-end Gateway TLS check. Verify
the controller-managed resources after installation:

```bash
kubectl -n demo-service wait certificate/demo-service-backend --for=condition=Ready --timeout=2m
kubectl -n demo-service wait configmap/tc-jku-internal-ca --for=create --timeout=2m
kubectl -n demo-service describe backendtlspolicy demo-service
kubectl -n demo-service describe httproute demo-service
curl --fail --cacert /path/to/internal-root-ca.pem https://demo.tc.jku.internal/health/ready
```

Check `Accepted=True` for the backend policy and `Accepted=True` and
`ResolvedRefs=True` for the route. The platform guide also describes verifying
the distributed CA fingerprint. Set a new `IMAGE_TAG` and repeat the upgrade
command to deploy another version. Inspect or roll back recorded releases with:

```bash
helm history demo-service --namespace demo-service
helm rollback demo-service <revision> --namespace demo-service --wait --timeout 5m
```

Remove the application with `helm uninstall demo-service --namespace demo-service`.
The namespace, trust bundle, and shared platform remain. The backend TLS Secret
is managed by cert-manager; its deletion depends on cert-manager's certificate
owner-reference configuration. Delete the namespace separately only when its
remaining resources are no longer needed.

## TLS

TLS is enabled by setting `USE_TLS=true` and mounting a certificate and matching
private key. For example:

```bash
docker run --rm --read-only --user 65532:65532 -p 8443:8443 \
  -e USE_TLS=true \
  -e CERT_FILE=/var/run/demo-service/tls/tls.crt \
  -e KEY_FILE=/var/run/demo-service/tls/tls.key \
  -v /path/to/tls:/var/run/demo-service/tls:ro \
  johanneskuhfuss/demo-service
```

The certificate files are watched and reloaded without restarting the server.
TLS 1.3 is required; Go negotiates HTTP/2 automatically when the client supports
it. The Kubernetes manifests request and mount the certificate through
cert-manager.

## Configuration

The `/probes` page can temporarily disable readiness for a duration entered in
seconds (1–3600, default 60). The response includes the recovery time and a
countdown that works while the pod is unreachable through the Service. The
server automatically ends the pause at its deadline without another UI request.
Kubernetes needs to observe readiness success and update routing before Service
access returns. Shutdown still keeps the pod unready. A new pause replaces the
previous deadline; restarting the application clears the pause.

| Variable | Default | Description |
| --- | --- | --- |
| `POD_NAME` | empty | Pod name from the Downward API (`metadata.name`) |
| `POD_IP` | empty | Pod IP from the Downward API (`status.podIP`) |
| `POD_NAMESPACE` | empty | Pod namespace from the Downward API (`metadata.namespace`) |
| `NODE_NAME` | empty | Node name from the Downward API (`spec.nodeName`) |
| `SERVER_HOST` | empty (all interfaces) | Listen address |
| `SERVER_PORT` | `8080` | Plain HTTP port |
| `SERVER_TLS_PORT` | `8443` | HTTPS port |
| `USE_TLS` | `false` | Enable HTTPS |
| `CERT_FILE` | `/var/run/demo-service/tls/tls.crt` | PEM certificate path |
| `KEY_FILE` | `/var/run/demo-service/tls/tls.key` | PEM private-key path |
| `DRAIN_REQUESTS_TIME` | `12` | Seconds to become unready before shutdown |
| `GRACEFUL_SHUTDOWN_TIME` | `10` | Seconds allowed for active requests |
| `GIN_MODE` | `release` | Gin mode |
| `LOG_TO_LOGGER` | `false` | Enable Gin access logs |
| `LOG_FORMAT` | `text` | Console format: `text` or `json` |
| `LOG_LEVEL` | `info` | Minimum severity: `debug`, `info`, `warn`, or `error` |

An optional `.env` file can provide the same values. Existing process
environment variables take precedence.

The deployment injects the four Kubernetes fields through Downward API environment
variables. The status page displays them, or `N/A` when they are unset (for example,
when running locally). Values are read at application startup.

## Logging

The service uses Go's `log/slog` with structured attributes and writes to stderr.
The Logs page sends messages through the same logger with request context and
`log.source=web`. Messages must contain 1–4,096 characters. The page reports
when `LOG_LEVEL` filters out the chosen severity; use `LOG_LEVEL=debug` to
exercise all four levels. Successful submissions redirect to prevent refresh
from sending the message again.
The default text output is readable in the terminal and through `kubectl logs`:

```text
time=2026-09-17T14:32:10Z level=INFO msg=Listening service.name=demo-service server.address=:8080
```

Set `LOG_FORMAT=json` for one JSON object per line. Both formats honor
`LOG_LEVEL`. Invalid logging settings fail startup. Configuration errors before
the logger is initialized use a bootstrap text logger.

`LOG_TO_LOGGER=true` enables structured access records with the HTTP method,
route template, response status, and duration. Responses in the 4xx range log
at warn; 5xx responses log at error. Query strings, request headers, and raw URL
paths are omitted. Panic recovery logs at error even when access logs are off.
HTTP server errors and certificate watcher events use the same logger.

Logging calls carry the available context so a future OpenTelemetry `otelslog`
handler can correlate records with active spans. This change does not install
tracing or export telemetry. JSON console output is ordinary slog JSON, not
OTLP JSON. When adding export, combine the console handler and OTel bridge with
`slog.NewMultiHandler`, configure service resource metadata and batched OTLP
export, and flush the provider during shutdown. Keep console text enabled for
operators; avoid collecting the same records through both console scraping and
OTLP.

## Verification

Run the local checks with:

```bash
go test -race ./...
go vet ./...
```

The race detector requires CGO and a C compiler. Linux runs also exercise
projected Secret rotation by atomically replacing the `..data` symlink and
checking the certificate served on a new TLS connection. Watcher tests wait for
reload events rather than assuming a fixed filesystem delay.

Run the Chromium readiness countdown test with Node.js and Go installed:

```bash
cd e2e
npm ci
npx playwright install --with-deps chromium
npm test
```

The browser test starts its own HTTP service on `127.0.0.1:18081`, disables
readiness, takes the browser offline, checks that the countdown completes, and
reconnects to verify recovery. It simulates loss of Service access; it does not
require a Kubernetes cluster. Set `DEMO_SERVICE_BINARY` to an absolute executable
path to use a prebuilt service instead of `go run .`. CI runs this browser test
alongside the Linux race tests.

The repository workflow also validates the Kubernetes manifests, builds the
container, and smoke-tests it as UID/GID 65532 with a read-only root filesystem.
It lints the Helm chart, renders the lab and alternate-namespace configurations,
and rejects missing image tags, invalid log levels, and insufficient shutdown
budgets. Kubeconform checks built-in Kubernetes resources; custom resources
without available schemas are skipped. Their acceptance is checked against the
installed controllers during deployment verification. To check the chart locally:

```bash
helm lint chart --strict -f values-lab.yaml --set-string image.tag=ci
helm template demo-service chart --namespace demo-service \
  -f values-lab.yaml --set-string image.tag=ci
```

All test certificates are generated at test time; no reusable private key is
stored in the repository.
